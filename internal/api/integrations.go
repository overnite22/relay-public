package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/netbox"
	"github.com/overnite22/relay-public/internal/proxmox"
	"github.com/overnite22/relay-public/internal/rdmimport"
	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/syncer"
)

func (s *Server) integrationRoutes(r chi.Router) {
	r.Get("/sources", s.listSources)
	r.Post("/sources", s.saveSource)
	r.Put("/sources/{id}", s.saveSource)
	r.Delete("/sources/{id}", s.deleteSource)
	r.Post("/sources/{id}/test", s.testSource)
	r.Post("/sources/{id}/sync", s.syncSourceNow)
	r.Post("/import/rdm/preview", s.importPreview)
	r.Post("/import/rdm/apply", s.importApply)
	r.Get("/vnc-tunnel", s.vncTunnel)
	r.Get("/duplicates", s.listDuplicates)
	r.Post("/entries/{id}/merge", s.mergeEntries)
	r.Post("/sources/{id}/clear-exclusions", s.clearExclusions)
}

// ---------- Sources ----------

func sourceAAD(id string) string { return "source:" + id + ":secret" }

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	src, err := s.opt.Store.ListSources(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) saveSource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind        string          `json:"kind"`
		Name        string          `json:"name"`
		URL         string          `json:"url"`
		TokenID     string          `json:"tokenId"`
		Secret      *string         `json:"secret"`
		VerifyTLS   bool            `json:"verifyTls"`
		Options     json.RawMessage `json:"options"`
		IntervalMin int             `json:"intervalMin"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	var src store.Source
	created := id == ""
	if created {
		src.ID = store.NewID()
	} else {
		var err error
		if src, err = s.opt.Store.GetSource(r.Context(), id); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	src.Kind, src.Name, src.URL, src.TokenID = in.Kind, in.Name, in.URL, in.TokenID
	src.VerifyTLS, src.Options, src.IntervalMin = in.VerifyTLS, in.Options, in.IntervalMin
	if in.Secret != nil {
		sealed, err := s.opt.Vault.Seal(strings.TrimSpace(*in.Secret), sourceAAD(src.ID))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		src.SecretEnc = sealed
	}
	if created && src.SecretEnc == "" {
		s.fail(w, r, store.ErrInvalid{Msg: "le secret du jeton d'API est obligatoire"})
		return
	}
	src, err := s.opt.Store.SaveSource(r.Context(), src)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action := "source.update"
	if created {
		action = "source.create"
	}
	s.audit(r, action, src.Name, src.Kind)
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := s.opt.Store.GetSource(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.opt.Store.DeleteSource(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "source.delete", src.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) proxmoxClient(src store.Source) (*proxmox.Client, error) {
	secret, err := s.opt.Vault.Open(src.SecretEnc, sourceAAD(src.ID))
	if err != nil {
		return nil, err
	}
	return proxmox.New(src.URL, src.TokenID, secret, src.VerifyTLS), nil
}

func (s *Server) netboxClient(src store.Source) (*netbox.Client, error) {
	secret, err := s.opt.Vault.Open(src.SecretEnc, sourceAAD(src.ID))
	if err != nil {
		return nil, err
	}
	return netbox.New(src.URL, secret, src.VerifyTLS), nil
}

func (s *Server) testSource(w http.ResponseWriter, r *http.Request) {
	src, err := s.opt.Store.GetSource(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var version string
	switch src.Kind {
	case store.SourceProxmox:
		c, err := s.proxmoxClient(src)
		if err == nil {
			version, err = c.Version(ctx)
		}
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		version = "Proxmox VE " + version
	case store.SourceNetBox:
		c, err := s.netboxClient(src)
		if err == nil {
			version, err = c.Status(ctx)
		}
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		version = "NetBox " + version
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Connexion réussie : " + version})
}

// syncLocks empêche deux synchronisations simultanées d'une même source.
var syncLocks sync.Map

// RunSync synchronise une source et enregistre son état.
func (s *Server) RunSync(ctx context.Context, src store.Source, user string) (syncer.Result, error) {
	s.dataGuard.RLock()
	defer s.dataGuard.RUnlock()
	if _, busy := syncLocks.LoadOrStore(src.ID, true); busy {
		return syncer.Result{}, errors.New("une synchronisation de cette source est déjà en cours")
	}
	defer syncLocks.Delete(src.ID)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var opt syncer.Options
	_ = json.Unmarshal(src.Options, &opt)
	var plan syncer.Plan
	var err error
	switch src.Kind {
	case store.SourceProxmox:
		var c *proxmox.Client
		if c, err = s.proxmoxClient(src); err == nil {
			plan, err = syncer.CollectProxmox(ctx, c, src, opt)
		}
	case store.SourceNetBox:
		var c *netbox.Client
		if c, err = s.netboxClient(src); err == nil {
			plan, err = syncer.CollectNetBox(ctx, c, opt)
		}
	default:
		err = errors.New("type de source inconnu")
	}
	var res syncer.Result
	if err == nil {
		res, err = syncer.Apply(ctx, s.opt.Store, src, opt, plan)
	}
	status := "OK : " + res.String()
	if err != nil {
		status = "Échec : " + err.Error()
	}
	_ = s.opt.Store.SetSourceStatus(context.Background(), src.ID, status)
	_ = s.opt.Store.Audit(context.Background(), user, "source.sync", src.Name, status)
	return res, err
}

func (s *Server) syncSourceNow(w http.ResponseWriter, r *http.Request) {
	src, err := s.opt.Store.GetSource(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := s.RunSync(r.Context(), src, auth.User(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": res.String(), "result": res})
}

// Scheduler lance les synchronisations périodiques jusqu'à l'annulation de ctx.
func (s *Server) Scheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.dataGuard.RLock()
		sources, err := s.opt.Store.ListSources(ctx)
		if err != nil {
			s.dataGuard.RUnlock()
			continue
		}
		for _, src := range sources {
			if src.IntervalMin <= 0 {
				continue
			}
			if src.LastSyncAt != nil && time.Since(*src.LastSyncAt) < time.Duration(src.IntervalMin)*time.Minute {
				continue
			}
			if _, err := s.RunSync(ctx, src, "synchronisation automatique"); err != nil {
				s.log.Warn("synchronisation", "source", src.Name, "err", err)
			}
		}
		s.dataGuard.RUnlock()
	}
}

// ---------- Import RDM ----------

const maxImport = 32 << 20

func readImport(r *http.Request) (rdmimport.Plan, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxImport+1))
	if err != nil {
		return rdmimport.Plan{}, err
	}
	if len(body) > maxImport {
		return rdmimport.Plan{}, store.ErrInvalid{Msg: "fichier trop volumineux (32 Mo maximum)"}
	}
	p, err := rdmimport.Parse(strings.NewReader(string(body)))
	if err != nil {
		return p, store.ErrInvalid{Msg: err.Error()}
	}
	return p, nil
}

func (s *Server) importPreview(w http.ResponseWriter, r *http.Request) {
	p, err := readImport(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) importApply(w http.ResponseWriter, r *http.Request) {
	p, err := readImport(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	root := r.URL.Query().Get("folder")
	q := r.URL.Query()
	res, err := rdmimport.Apply(r.Context(), s.opt.Store, p, root, rdmimport.Defaults{SSH: q.Get("sshDefault"), RDP: q.Get("rdpDefault")}, func(id, field, v string) (string, error) {
		return s.opt.Vault.Seal(v, aad(id, field))
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "import.rdm", root, fmt.Sprintf("%d entrée(s), %d identifiant(s), %d dossier(s)", res.Entries, res.Credentials, res.Folders))
	writeJSON(w, http.StatusOK, res)
}

// ---------- Console Proxmox (noVNC) ----------

// pveTicket demande à Proxmox un proxy VNC pour l'invité de l'entrée.
// En cas d'échec, la réponse d'erreur est déjà écrite.
func (s *Server) pveTicket(w http.ResponseWriter, r *http.Request, e store.Entry) (*proxmox.Client, pveSettings, proxmox.VNCTicket, bool) {
	var ps pveSettings
	if err := json.Unmarshal(e.Settings, &ps); err != nil || ps.SourceID == "" {
		s.fail(w, r, store.ErrInvalid{Msg: "console Proxmox mal configurée"})
		return nil, ps, proxmox.VNCTicket{}, false
	}
	src, err := s.opt.Store.GetSource(r.Context(), ps.SourceID)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, store.ErrInvalid{Msg: "la source Proxmox de cette console a été supprimée"})
		return nil, ps, proxmox.VNCTicket{}, false
	} else if err != nil {
		s.fail(w, r, err)
		return nil, ps, proxmox.VNCTicket{}, false
	}
	c, err := s.proxmoxClient(src)
	if err != nil {
		s.fail(w, r, err)
		return nil, ps, proxmox.VNCTicket{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	t, err := c.VNCProxy(ctx, ps.Node, ps.Type, ps.VMID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return nil, ps, proxmox.VNCTicket{}, false
	}
	return c, ps, t, true
}

type pveSettings struct {
	SourceID string `json:"sourceId"`
	Node     string `json:"node"`
	VMID     int    `json:"vmid"`
	Type     string `json:"type"`
}

// pendingVNC est une console Proxmox préparée par connect.
type pendingVNC struct {
	client *proxmox.Client
	pve    pveSettings
	ticket proxmox.VNCTicket
	user   string
	name   string
	exp    time.Time
}

var vncTokens = struct {
	sync.Mutex
	m map[string]pendingVNC
}{m: map[string]pendingVNC{}}

// connectPVE prépare une console : vncproxy côté Proxmox, puis jeton local.
// Le ticket VNC est renvoyé au navigateur : c'est le mot de passe RFB attendu
// par Proxmox, valable quelques secondes et pour cette seule console.
func (s *Server) connectPVE(w http.ResponseWriter, r *http.Request, e store.Entry) {
	c, ps, t, ok := s.pveTicket(w, r, e)
	if !ok {
		return
	}
	tok := store.NewID() + store.NewID()
	vncTokens.Lock()
	now := time.Now()
	for k, v := range vncTokens.m {
		if now.After(v.exp) {
			delete(vncTokens.m, k)
		}
	}
	vncTokens.m[tok] = pendingVNC{client: c, pve: ps, ticket: t, user: auth.User(r.Context()), name: e.Name, exp: now.Add(tokenTTL)}
	vncTokens.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"token": tok, "protocol": "pve", "password": t.Ticket})
}

func (s *Server) vncTunnel(w http.ResponseWriter, r *http.Request) {
	user := auth.User(r.Context())
	vncTokens.Lock()
	p, ok := vncTokens.m[r.URL.Query().Get("token")]
	delete(vncTokens.m, r.URL.Query().Get("token"))
	vncTokens.Unlock()
	opts := &websocket.AcceptOptions{Subprotocols: []string{"binary"}}
	if s.opt.PublicOrigin != "" {
		opts.OriginPatterns = []string{strings.TrimPrefix(strings.TrimPrefix(s.opt.PublicOrigin, "https://"), "http://")}
	}
	if !ok || p.user != user || time.Now().After(p.exp) {
		http.Error(w, "jeton invalide ou expiré", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(64 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	up, err := p.client.DialVNC(ctx, p.pve.Node, p.pve.Type, p.pve.VMID, p.ticket)
	if err != nil {
		s.log.Warn("console Proxmox", "entry", p.name, "err", err)
		ws.Close(websocket.StatusInternalError, "Proxmox injoignable")
		return
	}
	defer up.CloseNow()
	start := time.Now()
	_ = s.opt.Store.Audit(context.Background(), user, "session.open", p.name, fmt.Sprintf("console %s %d", p.pve.Type, p.pve.VMID))
	errc := make(chan error, 2)
	pipe := func(dst, src *websocket.Conn) {
		for {
			typ, data, err := src.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if err := dst.Write(ctx, typ, data); err != nil {
				errc <- err
				return
			}
		}
	}
	go pipe(up, ws)
	go pipe(ws, up)
	<-errc
	cancel()
	_ = s.opt.Store.Audit(context.Background(), user, "session.close", p.name, "durée "+time.Since(start).Round(time.Second).String())
}
