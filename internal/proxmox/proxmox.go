// Package proxmox est un client minimal de l'API Proxmox VE (jeton d'API).
package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Client parle à un cluster Proxmox VE.
type Client struct {
	base string
	auth string
	http *http.Client
}

// New construit un client. tokenID = "utilisateur@royaume!jeton".
func New(baseURL, tokenID, secret string, verifyTLS bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: !verifyTLS} //nolint:gosec // choix explicite par source (certificats auto-signés)
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		auth: "PVEAPIToken=" + tokenID + "=" + secret,
		http: &http.Client{Transport: tr, Timeout: 20 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api2/json"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("proxmox : %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(resp.Status)
		if resp.StatusCode == http.StatusUnauthorized {
			msg = "jeton refusé (401)"
		} else if resp.StatusCode == http.StatusForbidden {
			msg = "droits insuffisants (403)"
		}
		return fmt.Errorf("proxmox %s %s : %s", method, path, msg)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("proxmox : réponse illisible : %w", err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// Version renvoie la version de Proxmox VE (test de connexion).
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	err := c.do(ctx, http.MethodGet, "/version", nil, &v)
	return v.Version, err
}

// Resource est un élément de /cluster/resources.
type Resource struct {
	Type     string `json:"type"` // node, qemu, lxc, storage...
	Node     string `json:"node"`
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Template int    `json:"template"`
	Tags     string `json:"tags"`
}

// Resources liste nœuds et invités du cluster.
func (c *Client) Resources(ctx context.Context) ([]Resource, error) {
	var out []Resource
	err := c.do(ctx, http.MethodGet, "/cluster/resources", nil, &out)
	return out, err
}

// NodeIPs renvoie l'adresse IP de chaque nœud (via /cluster/status).
func (c *Client) NodeIPs(ctx context.Context) (map[string]string, error) {
	var st []struct {
		Type string `json:"type"`
		Name string `json:"name"`
		IP   string `json:"ip"`
	}
	if err := c.do(ctx, http.MethodGet, "/cluster/status", nil, &st); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range st {
		if s.Type == "node" && s.IP != "" {
			out[s.Name] = s.IP
		}
	}
	return out, nil
}

// GuestInfo regroupe ce qu'on sait d'un invité en marche.
type GuestInfo struct {
	IPv4    string
	Windows bool
}

// Guest interroge l'IP (interfaces LXC ou agent QEMU) et le type d'OS.
// Les erreurs sont ignorées : l'information est facultative.
func (c *Client) Guest(ctx context.Context, node, typ string, vmid int) GuestInfo {
	var gi GuestInfo
	base := fmt.Sprintf("/nodes/%s/%s/%d", url.PathEscape(node), typ, vmid)
	if typ == "lxc" {
		var ifs []struct {
			Name string `json:"name"`
			Inet string `json:"inet"`
		}
		if c.do(ctx, http.MethodGet, base+"/interfaces", nil, &ifs) == nil {
			for _, i := range ifs {
				if i.Name != "lo" && i.Inet != "" {
					gi.IPv4 = strings.SplitN(i.Inet, "/", 2)[0]
					break
				}
			}
		}
		return gi
	}
	var cfg struct {
		OSType string `json:"ostype"`
	}
	if c.do(ctx, http.MethodGet, base+"/config", nil, &cfg) == nil {
		gi.Windows = strings.HasPrefix(cfg.OSType, "win")
	}
	var ag struct {
		Result []struct {
			Name string `json:"name"`
			IPs  []struct {
				Type string `json:"ip-address-type"`
				Addr string `json:"ip-address"`
			} `json:"ip-addresses"`
		} `json:"result"`
	}
	if c.do(ctx, http.MethodGet, base+"/agent/network-get-interfaces", nil, &ag) == nil {
		for _, r := range ag.Result {
			if r.Name == "lo" || strings.HasPrefix(strings.ToLower(r.Name), "loopback") {
				continue
			}
			for _, ip := range r.IPs {
				if ip.Type == "ipv4" && !strings.HasPrefix(ip.Addr, "127.") && !strings.HasPrefix(ip.Addr, "169.254.") {
					gi.IPv4 = ip.Addr
					return gi
				}
			}
		}
	}
	return gi
}

// VNCTicket est la réponse de vncproxy.
type VNCTicket struct {
	Port   string
	Ticket string
}

// VNCProxy ouvre un proxy VNC (websocket) pour un invité.
func (c *Client) VNCProxy(ctx context.Context, node, typ string, vmid int) (VNCTicket, error) {
	var r struct {
		Port   json.Number `json:"port"`
		Ticket string      `json:"ticket"`
	}
	path := fmt.Sprintf("/nodes/%s/%s/%d/vncproxy", url.PathEscape(node), typ, vmid)
	if err := c.do(ctx, http.MethodPost, path, url.Values{"websocket": {"1"}}, &r); err != nil {
		return VNCTicket{}, err
	}
	if _, err := strconv.Atoi(r.Port.String()); err != nil || r.Ticket == "" {
		return VNCTicket{}, fmt.Errorf("proxmox : réponse vncproxy inattendue")
	}
	return VNCTicket{Port: r.Port.String(), Ticket: r.Ticket}, nil
}

// DialVNC ouvre le WebSocket VNC de Proxmox pour un ticket obtenu par VNCProxy.
func (c *Client) DialVNC(ctx context.Context, node, typ string, vmid int, t VNCTicket) (*websocket.Conn, error) {
	u, err := url.Parse(c.base)
	if err != nil {
		return nil, err
	}
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	q := url.Values{"port": {t.Port}, "vncticket": {t.Ticket}}
	target := fmt.Sprintf("%s://%s/api2/json/nodes/%s/%s/%d/vncwebsocket?%s", scheme, u.Host, url.PathEscape(node), typ, vmid, q.Encode())
	ws, resp, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient:   c.http,
		HTTPHeader:   http.Header{"Authorization": {c.auth}},
		Subprotocols: []string{"binary"},
	})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("proxmox vncwebsocket : %s", resp.Status)
		}
		return nil, fmt.Errorf("proxmox vncwebsocket : %w", err)
	}
	ws.SetReadLimit(64 << 20)
	return ws, nil
}
