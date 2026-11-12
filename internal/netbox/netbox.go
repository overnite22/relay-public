// Package netbox est un client minimal de l'API REST NetBox (lecture seule).
package netbox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client interroge une instance NetBox.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New construit un client. Les jetons NetBox v2 (préfixe "nbt_") utilisent
// l'en-tête Bearer, les jetons historiques l'en-tête Token.
func New(baseURL, token string, verifyTLS bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: !verifyTLS} //nolint:gosec // choix explicite par source
	return &Client{base: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
}

func (c *Client) authHeader() string {
	if strings.HasPrefix(c.token, "nbt_") {
		return "Bearer " + c.token
	}
	return "Token " + c.token
}

func (c *Client) get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("netbox : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := resp.Status
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			msg = "jeton refusé ou droits insuffisants (" + resp.Status + ")"
		}
		return fmt.Errorf("netbox %s : %s", strings.TrimPrefix(rawURL, c.base), msg)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

// Ref est une référence imbriquée (site, rôle, plateforme, cluster...).
type Ref struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// IP est une adresse imbriquée.
type IP struct {
	Address string `json:"address"`
}

// Object est un équipement ou une machine virtuelle.
type Object struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	DisplayURL  string `json:"display_url"`
	PrimaryIP4  *IP    `json:"primary_ip4"`
	PrimaryIP   *IP    `json:"primary_ip"`
	Platform    *Ref   `json:"platform"`
	Site        *Ref   `json:"site"`
	Role        *Ref   `json:"role"`
	DeviceRole  *Ref   `json:"device_role"` // NetBox < 3.6
	Cluster     *Ref   `json:"cluster"`
	Tenant      *Ref   `json:"tenant"`
	Status      struct {
		Value string `json:"value"`
	} `json:"status"`
}

// Address renvoie l'IP principale sans masque.
func (o Object) Address() string {
	for _, ip := range []*IP{o.PrimaryIP4, o.PrimaryIP} {
		if ip != nil && ip.Address != "" {
			return strings.SplitN(ip.Address, "/", 2)[0]
		}
	}
	return ""
}

// RoleName renvoie le rôle, quelle que soit la version de NetBox.
func (o Object) RoleName() string {
	if o.Role != nil {
		return o.Role.Name
	}
	if o.DeviceRole != nil {
		return o.DeviceRole.Name
	}
	return ""
}

func (c *Client) list(ctx context.Context, path string) ([]Object, error) {
	next := c.base + path + "?limit=500&has_primary_ip=true"
	var out []Object
	for page := 0; next != "" && page < 200; page++ {
		var r struct {
			Next    *string  `json:"next"`
			Results []Object `json:"results"`
		}
		if err := c.get(ctx, next, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Results...)
		next = ""
		if r.Next != nil && *r.Next != "" {
			// NetBox renvoie une URL absolue, parfois en http derrière un proxy :
			// on ne garde que le chemin et la requête, appliqués à notre base.
			if u, err := url.Parse(*r.Next); err == nil {
				next = c.base + u.Path + "?" + u.RawQuery
			}
		}
	}
	return out, nil
}

// Devices liste les équipements ayant une IP principale.
func (c *Client) Devices(ctx context.Context) ([]Object, error) {
	return c.list(ctx, "/api/dcim/devices/")
}

// VirtualMachines liste les VM ayant une IP principale.
func (c *Client) VirtualMachines(ctx context.Context) ([]Object, error) {
	return c.list(ctx, "/api/virtualization/virtual-machines/")
}

// Status teste la connexion et renvoie la version de NetBox.
func (c *Client) Status(ctx context.Context) (string, error) {
	var s struct {
		Version string `json:"netbox-version"`
	}
	err := c.get(ctx, c.base+"/api/status/", &s)
	return s.Version, err
}
