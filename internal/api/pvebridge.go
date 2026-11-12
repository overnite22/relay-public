package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/guac"
	"github.com/overnite22/relay-public/internal/store"
)

// ---------- Console Proxmox native (via guacd) ----------
//
// Les applications n'embarquent pas noVNC : Relay ouvre un port TCP éphémère,
// que seul guacd peut utiliser, et le relie au WebSocket VNC de Proxmox.
// guacd voit un serveur VNC ordinaire protégé par le ticket de la console ;
// la session passe ensuite par le tunnel Guacamole habituel (rendu, enregistrement).

const bridgeAcceptTimeout = 30 * time.Second

func (s *Server) connectPVENative(w http.ResponseWriter, r *http.Request, e store.Entry, in sessionOptions) {
	if s.opt.BridgeHost == "" {
		s.fail(w, r, store.ErrInvalid{Msg: "console native indisponible : RELAY_BRIDGE_HOST n'est pas configuré"})
		return
	}
	if in.Record && s.opt.RecordingsDir == "" {
		s.fail(w, r, store.ErrInvalid{Msg: "enregistrement indisponible pour cette session"})
		return
	}
	c, ps, t, ok := s.pveTicket(w, r, e)
	if !ok {
		return
	}
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	dial := func(ctx context.Context) (*websocket.Conn, error) {
		return c.DialVNC(ctx, ps.Node, ps.Type, ps.VMID, t)
	}
	go s.serveBridge(ln, dial, e.Name)

	clamp := func(v, lo, hi, def int) int {
		if v < lo || v > hi {
			return def
		}
		return v
	}
	cfg := guac.Config{
		Protocol: "vnc",
		Params: map[string]string{
			"hostname":    s.opt.BridgeHost,
			"port":        strconv.Itoa(port),
			"password":    t.Ticket,
			"color-depth": "24",
		},
		Width:    clamp(in.Width, 100, 8192, 1280),
		Height:   clamp(in.Height, 100, 8192, 720),
		DPI:      clamp(in.DPI, 48, 400, 96),
		Timezone: in.Timezone,
	}
	tok := s.tokens.put(pendingSession{cfg: cfg, user: auth.User(r.Context()), entryID: e.ID, name: e.Name, record: in.Record})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": tok, "protocol": "vnc"})
}

// guacdIPs renvoie les adresses de guacd, seules autorisées sur le pont.
func (s *Server) guacdIPs() map[string]bool {
	host, _, err := net.SplitHostPort(s.opt.GuacdAddr)
	if err != nil {
		host = s.opt.GuacdAddr
	}
	out := map[string]bool{}
	if ip := net.ParseIP(host); ip != nil {
		out[ip.String()] = true
		return out
	}
	ips, _ := net.LookupIP(host)
	for _, ip := range ips {
		out[ip.String()] = true
	}
	return out
}

// serveBridge accepte une seule connexion de guacd puis relaie les octets RFB.
func (s *Server) serveBridge(ln net.Listener, dial func(context.Context) (*websocket.Conn, error), name string) {
	defer ln.Close()
	if tl, ok := ln.(*net.TCPListener); ok {
		_ = tl.SetDeadline(time.Now().Add(bridgeAcceptTimeout))
	}
	allowed := s.guacdIPs()
	var conn net.Conn
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		if ip := net.ParseIP(host); ip != nil && allowed[ip.String()] {
			conn = c
			break
		}
		s.log.Warn("pont VNC : connexion refusée", "from", c.RemoteAddr().String())
		c.Close()
	}
	ln.Close() // une seule connexion par console
	defer conn.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 15*time.Second)
	up, err := dial(dialCtx)
	cancelDial()
	if err != nil {
		s.log.Warn("console Proxmox native", "entry", name, "err", err)
		return
	}
	defer up.CloseNow()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 2)
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := up.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()
	go func() {
		for {
			_, data, err := up.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if _, err := conn.Write(data); err != nil {
				errc <- err
				return
			}
		}
	}()
	if err := <-errc; err != nil && err != io.EOF {
		s.log.Debug("pont VNC fermé", "entry", name, "err", err)
	}
}
