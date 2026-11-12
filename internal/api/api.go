// Package api expose l'API HTTP de Relay et le tunnel WebSocket des sessions.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/guac"
	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/totp"
	"github.com/overnite22/relay-public/internal/vault"
)

// Version est fixée à la compilation (-ldflags).
var Version = "dev"

// Options paramètre le serveur API.
type Options struct {
	AdminUsers []string
	SSHHostKey func(context.Context, string) (string, error)
	Store      *store.Store
	DataDir    string
	Vault      *vault.Vault
	Auth       *auth.Authenticator
	GuacdAddr  string
	// BridgeHost est le nom par lequel guacd joint Relay (ex. « relay ») ;
	// il active les consoles Proxmox natives, relayées par guacd.
	BridgeHost    string
	RecordingsDir string
	PublicOrigin  string // ex. https://relay.example.com ; vide = même hôte
	Static        http.Handler
	Log           *slog.Logger
}

// Server porte l'état de l'API.
type Server struct {
	dataGuard   sync.RWMutex
	opt         Options
	tokens      *sessionTokens
	log         *slog.Logger
	runs        runRegistry
	diagnostics diagnosticCache
}

// New construit le serveur.
func New(opt Options) *Server {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	s := &Server{opt: opt, tokens: newSessionTokens(), log: opt.Log, runs: runRegistry{runs: map[string]*activeRun{}}}
	if opt.Auth != nil && opt.Store != nil {
		opt.Auth.SetTokenResolver(s.resolveDeviceToken)
	}
	return s
}

// Handler renvoie le routeur HTTP complet.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(securityHeaders)
	if s.opt.Static != nil {
		for _, path := range []string{"/manifest.webmanifest", "/sw.js", "/icons/relay-192.png", "/icons/relay-512.png", "/icons/relay-maskable-512.png", "/icons/apple-touch-icon.png"} {
			r.Handle(path, s.opt.Static)
		}
	}
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": Version})
	})
	r.Group(func(r chi.Router) {
		r.Use(s.opt.Auth.Middleware)
		r.Use(s.dataAccess)
		r.Route("/api", func(r chi.Router) {
			r.Use(s.csrf)
			r.Use(s.deviceUsage)
			s.deviceRoutes(r)
			r.Get("/me", s.me)
			r.Post("/backups/export", s.exportBackup)
			r.Post("/backups/restore", s.restoreBackup)
			r.Get("/tree", s.tree)
			r.Get("/connection-status", s.listConnectionStatus)
			r.Post("/folders", s.saveFolder)
			r.Put("/folders/{id}", s.saveFolder)
			r.Put("/folders/{id}/credentials", s.setFolderCredentials)
			r.Get("/workspaces", s.listWorkspaces)
			r.Post("/workspaces", s.saveWorkspace)
			r.Put("/workspaces/{id}", s.saveWorkspace)
			r.Delete("/workspaces/{id}", s.deleteWorkspace)
			r.Delete("/folders/{id}", s.deleteFolder)
			r.Get("/entries/{id}", s.getEntry)
			r.Post("/entries", s.saveEntry)
			r.Put("/entries/{id}", s.saveEntry)
			r.Delete("/entries/{id}", s.deleteEntry)
			r.Get("/entries/{id}/attachments", s.listAttachments)
			r.Post("/entries/{id}/attachments", s.addAttachment)
			r.Get("/entries/{id}/attachments/{attachmentID}", s.downloadAttachment)
			r.Delete("/entries/{id}/attachments/{attachmentID}", s.deleteAttachment)
			r.Post("/entries/{id}/connect", s.connect)
			r.Post("/entries/{id}/test-connection", s.testConnection)
			r.Get("/credentials", s.listCredentials)
			r.Post("/credentials", s.saveCredential)
			r.Put("/credentials/{id}", s.saveCredential)
			r.Delete("/credentials/{id}", s.deleteCredential)
			r.Post("/credentials/{id}/reveal", s.revealCredential)
			r.Get("/credentials/{id}/totp", s.credentialTOTP)
			r.Get("/audit", s.listAudit)
			r.Post("/quick-connect", s.quickConnect)
			r.Get("/tunnel", s.tunnel)
			r.Get("/recordings", s.listRecordings)
			r.Get("/recordings/{id}", s.readRecording)
			r.Delete("/recordings/{id}", s.deleteRecording)
			s.integrationRoutes(r)
			s.commandRoutes(r)
		})
		if s.opt.Static != nil {
			r.Handle("/*", s.opt.Static)
		}
	})
	return r
}

// ---------- Helpers ----------

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// deviceUsage trace la dernière utilisation d'un jeton d'appareil.
func (s *Server) deviceUsage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.touchDevice(r)
		next.ServeHTTP(w, r)
	})
}

// csrf exige, pour toute requête qui modifie, l'en-tête X-Relay (impossible
// à poser depuis un autre site sans pré-vol CORS, que nous ne servons pas)
// et, si configurée, l'origine publique.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Relay") != "1" {
				writeErr(w, http.StatusForbidden, "en-tête X-Relay manquant")
				return
			}
			if o := r.Header.Get("Origin"); s.opt.PublicOrigin != "" && o != "" && o != s.opt.PublicOrigin {
				writeErr(w, http.StatusForbidden, "origine refusée")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var inv store.ErrInvalid
	switch {
	case errors.As(err, &inv):
		writeErr(w, http.StatusBadRequest, inv.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "introuvable")
	default:
		s.log.Error("erreur interne", "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "erreur interne")
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return store.ErrInvalid{Msg: "requête JSON invalide : " + err.Error()}
	}
	return nil
}

func (s *Server) audit(r *http.Request, action, target, detail string) {
	if err := s.opt.Store.Audit(r.Context(), auth.User(r.Context()), action, target, detail); err != nil {
		s.log.Error("journal", "err", err)
	}
}

// ---------- Utilisateur / arbre ----------

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": auth.User(r.Context()), "version": Version, "canManageBackups": s.canManageBackups(r)})
}

func (s *Server) tree(w http.ResponseWriter, r *http.Request) {
	folders, err := s.opt.Store.ListFolders(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	entries, err := s.opt.Store.ListEntries(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders, "entries": entries})
}

// ---------- Dossiers ----------

func (s *Server) saveFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string  `json:"name"`
		ParentID *string `json:"parentId"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	f := store.Folder{ID: chi.URLParam(r, "id"), Name: in.Name, ParentID: in.ParentID}
	created := f.ID == ""
	f, err := s.opt.Store.SaveFolder(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action := "folder.update"
	if created {
		action = "folder.create"
	}
	s.audit(r, action, f.Name, "")
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.opt.Store.DeleteFolder(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "folder.delete", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Entrées ----------

type entryInput struct {
	FolderID     *string         `json:"folderId"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	Host         string          `json:"host"`
	Port         int             `json:"port"`
	URL          string          `json:"url"`
	Username     string          `json:"username"`
	CredentialID *string         `json:"credentialId"`
	Settings     json.RawMessage `json:"settings"`
	Notes        string          `json:"notes"`
	Favorite     bool            `json:"favorite"`
}

func (s *Server) getEntry(w http.ResponseWriter, r *http.Request) {
	e, err := s.opt.Store.GetEntry(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) saveEntry(w http.ResponseWriter, r *http.Request) {
	var in entryInput
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	e := store.Entry{
		ID: chi.URLParam(r, "id"), FolderID: in.FolderID, Name: in.Name, Kind: in.Kind, Host: in.Host,
		Port: in.Port, URL: in.URL, Username: in.Username, CredentialID: in.CredentialID,
		Settings: in.Settings, Notes: in.Notes, Favorite: in.Favorite,
	}
	created := e.ID == ""
	e, err := s.opt.Store.SaveEntry(r.Context(), e)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action := "entry.update"
	if created {
		action = "entry.create"
	}
	s.audit(r, action, e.Name, e.Kind)
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	e, err := s.opt.Store.GetEntry(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.opt.Store.DeleteEntry(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	// Une entrée synchronisée supprimée à la main ne doit pas revenir.
	if e.SourceID != nil {
		_ = s.opt.Store.ExcludeFromSync(r.Context(), *e.SourceID, e.ExternalID)
	}
	s.audit(r, "entry.delete", e.Name, e.Kind)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Identifiants ----------

type credentialInput struct {
	Name       string  `json:"name"`
	Username   string  `json:"username"`
	Domain     string  `json:"domain"`
	Notes      string  `json:"notes"`
	Password   *string `json:"password"`   // nil = inchangé, "" = effacé
	PrivateKey *string `json:"privateKey"` // idem
	Passphrase *string `json:"passphrase"`
	TOTP       *string `json:"totp"`
}

func aad(id, field string) string { return "credential:" + id + ":" + field }

func (s *Server) listCredentials(w http.ResponseWriter, r *http.Request) {
	cs, err := s.opt.Store.ListCredentials(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) saveCredential(w http.ResponseWriter, r *http.Request) {
	var in credentialInput
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id := chi.URLParam(r, "id")
	var c store.Credential
	created := id == ""
	if created {
		c.ID = store.NewID()
	} else {
		var err error
		if c, err = s.opt.Store.GetCredential(r.Context(), id); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	c.Name, c.Username, c.Domain, c.Notes = in.Name, in.Username, in.Domain, in.Notes
	if in.TOTP != nil && strings.TrimSpace(*in.TOTP) != "" {
		if _, err := totp.Parse(*in.TOTP); err != nil {
			s.fail(w, r, store.ErrInvalid{Msg: "secret TOTP invalide (base32 ou URI otpauth://totp attendus)"})
			return
		}
	}
	if in.PrivateKey != nil && *in.PrivateKey != "" && !strings.Contains(*in.PrivateKey, "-----BEGIN") {
		s.fail(w, r, store.ErrInvalid{Msg: "clé privée invalide (format PEM / OpenSSH attendu)"})
		return
	}
	fields := []struct {
		in   *string
		dst  *string
		name string
	}{
		{in.Password, &c.PasswordEnc, "password"},
		{in.PrivateKey, &c.PrivateKeyEnc, "privateKey"},
		{in.Passphrase, &c.PassphraseEnc, "passphrase"},
		{in.TOTP, &c.TOTPEnc, "totp"},
	}
	for _, f := range fields {
		if f.in == nil {
			continue
		}
		v := *f.in
		if f.name == "totp" {
			v = strings.TrimSpace(v)
		}
		sealed, err := s.opt.Vault.Seal(v, aad(c.ID, f.name))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		*f.dst = sealed
	}
	c, err := s.opt.Store.SaveCredential(r.Context(), c)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action := "credential.update"
	if created {
		action = "credential.create"
	}
	s.audit(r, action, c.Name, "")
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, err := s.opt.Store.GetCredential(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.opt.Store.DeleteCredential(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "credential.delete", c.Name, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) openField(c store.Credential, field string) (string, error) {
	var enc string
	switch field {
	case "password":
		enc = c.PasswordEnc
	case "privateKey":
		enc = c.PrivateKeyEnc
	case "passphrase":
		enc = c.PassphraseEnc
	case "totp":
		enc = c.TOTPEnc
	default:
		return "", store.ErrInvalid{Msg: "champ inconnu"}
	}
	return s.opt.Vault.Open(enc, aad(c.ID, field))
}

func (s *Server) revealCredential(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Field string `json:"field"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.opt.Store.GetCredential(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.openField(c, in.Field)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "credential.reveal", c.Name, in.Field)
	writeJSON(w, http.StatusOK, map[string]string{"value": v})
}

func (s *Server) credentialTOTP(w http.ResponseWriter, r *http.Request) {
	c, err := s.opt.Store.GetCredential(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw, err := s.openField(c, "totp")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if raw == "" {
		writeErr(w, http.StatusNotFound, "pas de TOTP pour cet identifiant")
		return
	}
	p, err := totp.Parse(raw)
	if err != nil {
		s.fail(w, r, store.ErrInvalid{Msg: "secret TOTP enregistré invalide"})
		return
	}
	code, rem := p.Code(time.Now())
	s.audit(r, "credential.totp", c.Name, "")
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "remaining": rem, "period": p.Period})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ev, err := s.opt.Store.ListAudit(r.Context(), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

// ---------- Sessions ----------

type sessionOptions struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	DPI      int    `json:"dpi"`
	Timezone string `json:"timezone"`
	Record   bool   `json:"record"`
	// Native : console Proxmox relayée par guacd (applications), au lieu de noVNC.
	Native bool `json:"native"`
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var in sessionOptions
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	e, err := s.opt.Store.GetEntry(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if e.Kind == store.KindWeb {
		s.fail(w, r, store.ErrInvalid{Msg: "une entrée web s'ouvre dans le navigateur"})
		return
	}
	if e.Kind == store.KindPVE && in.Native {
		s.connectPVENative(w, r, e, in)
		return
	}
	if e.Kind == store.KindPVE {
		if in.Record {
			s.fail(w, r, store.ErrInvalid{Msg: "enregistrement indisponible pour cette session"})
			return
		}
		s.connectPVE(w, r, e)
		return
	}
	sec, err := s.sessionSecrets(r.Context(), e)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.prepareGuacSession(w, r, e, sec, in)
}

func (s *Server) sessionSecrets(ctx context.Context, e store.Entry) (secrets, error) {
	sec := secrets{username: e.Username, user: auth.User(ctx)}
	id, err := s.opt.Store.EffectiveCredentialID(ctx, e)
	if err != nil {
		return sec, err
	}
	if id == nil {
		return sec, nil
	}
	c, err := s.opt.Store.GetCredential(ctx, *id)
	if err != nil {
		return sec, err
	}
	if sec.username == "" {
		sec.username = c.Username
	}
	sec.domain = c.Domain
	for _, f := range []struct {
		dst  *string
		name string
	}{{&sec.password, "password"}, {&sec.privateKey, "privateKey"}, {&sec.passphrase, "passphrase"}} {
		if *f.dst, err = s.openField(c, f.name); err != nil {
			return secrets{}, fmt.Errorf("déchiffrement de %s : %w", f.name, err)
		}
	}
	return sec, nil
}

func (s *Server) prepareGuacSession(w http.ResponseWriter, r *http.Request, e store.Entry, sec secrets, in sessionOptions) {
	if in.Record && s.opt.RecordingsDir == "" {
		s.fail(w, r, store.ErrInvalid{Msg: "enregistrement indisponible pour cette session"})
		return
	}
	clamp := func(v, lo, hi, def int) int {
		if v < lo || v > hi {
			return def
		}
		return v
	}
	cfg, err := buildConfig(e, sec,
		clamp(in.Width, 100, 8192, 1280), clamp(in.Height, 100, 8192, 720), clamp(in.DPI, 48, 400, 96), in.Timezone)
	if err != nil {
		s.fail(w, r, store.ErrInvalid{Msg: "paramètres avancés invalides pour ce type d'entrée"})
		return
	}
	tok := s.tokens.put(pendingSession{cfg: cfg, user: auth.User(r.Context()), entryID: e.ID, name: e.Name, record: in.Record})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": tok, "protocol": e.Kind})
}

// Codes d'état Guacamole renvoyés au navigateur en cas d'échec.
const (
	statusUpstreamError       = "515" // 0x0203
	statusUpstreamUnavailable = "520" // 0x0208
	statusClientUnauthorized  = "769" // 0x0301
)

func (s *Server) tunnel(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{Subprotocols: []string{"guacamole"}}
	if s.opt.PublicOrigin != "" {
		opts.OriginPatterns = []string{strings.TrimPrefix(strings.TrimPrefix(s.opt.PublicOrigin, "https://"), "http://")}
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(8 << 20)
	ctx := r.Context()
	user := auth.User(ctx)

	sendErr := func(msg, code string) {
		_ = ws.Write(ctx, websocket.MessageText, []byte(guac.New("", "relay-error").Encode()+guac.New("error", msg, code).Encode()))
		ws.Close(websocket.StatusNormalClosure, "")
	}

	p, ok := s.tokens.take(r.URL.Query().Get("token"), user)
	if !ok {
		sendErr("Jeton de session invalide ou expiré", statusClientUnauthorized)
		return
	}
	if p.cfg.Protocol == "ssh" {
		lookup := s.opt.SSHHostKey
		if lookup == nil {
			lookup = s.verifySSHHostKey
		}
		port, _ := strconv.Atoi(p.cfg.Params["port"])
		key, err := lookup(ctx, connectionAddress(store.Entry{Host: p.cfg.Params["hostname"], Port: port}))
		if err != nil {
			sendErr("Connexion SSH refusée : "+err.Error(), statusUpstreamError)
			return
		}
		p.cfg.Params["host-key"] = key
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", s.opt.GuacdAddr)
	if err != nil {
		s.log.Error("guacd injoignable", "addr", s.opt.GuacdAddr, "err", err)
		sendErr("Le service guacd est injoignable", statusUpstreamUnavailable)
		return
	}
	defer conn.Close()
	br := bufio.NewReaderSize(conn, 64<<10)
	connID, err := guac.Handshake(conn, br, p.cfg)
	p.cfg.Params = nil // les secrets ne sont plus nécessaires
	if err != nil {
		s.log.Warn("échec de la négociation guacd", "entry", p.name, "err", err)
		sendErr("Échec de l'ouverture de la session : "+err.Error(), statusUpstreamError)
		return
	}
	start := time.Now()
	var capture *sessionCapture
	var recording *guac.Recording
	if p.record {
		capture, err = s.startRecording(p, user, start)
		if err != nil {
			s.log.Error("création de l'enregistrement", "err", err)
			sendErr("Impossible de créer l'enregistrement", statusUpstreamError)
			return
		}
		defer capture.discard()
		recording = capture.recording
	}
	_ = s.opt.Store.Audit(context.Background(), user, "session.open", p.name, connID)
	err = guac.Relay(ctx, ws, conn, br, connID, recording)
	if capture != nil {
		if saveErr := capture.finish(); saveErr != nil {
			s.log.Error("enregistrement de session", "err", saveErr)
		} else if capture.meta.Size > 0 {
			_ = s.opt.Store.Audit(context.Background(), user, "recording.save", p.name, capture.meta.ID)
		}
	}
	detail := "durée " + time.Since(start).Round(time.Second).String()
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		s.log.Info("session terminée", "entry", p.name, "err", err)
	}
	_ = s.opt.Store.Audit(context.Background(), user, "session.close", p.name, detail)
}
