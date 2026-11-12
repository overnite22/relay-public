package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/store"
)

// ---------- Appareils (application mobile) ----------
//
// Un appareil reçoit un jeton porteur « rlt_… », affiché une seule fois
// (QR code d'appairage). Seul son SHA-256 est conservé. Les requêtes
// portant « Authorization: Bearer rlt_… » sont authentifiées sans SSO.

func (s *Server) deviceRoutes(r chi.Router) {
	r.Get("/devices", s.listDevices)
	r.Post("/devices", s.createDevice)
	r.Delete("/devices/{id}", s.deleteDevice)
}

func hashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}

// resolveDeviceToken sert d'auth.TokenResolver.
func (s *Server) resolveDeviceToken(ctx context.Context, tok string) (string, error) {
	if len(tok) < 32 || len(tok) > 128 {
		return "", auth.ErrUnauthenticated
	}
	d, err := s.opt.Store.DeviceByTokenHash(ctx, hashToken(tok))
	if errors.Is(err, store.ErrNotFound) {
		return "", auth.ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	return d.User, nil
}

func (s *Server) touchDevice(r *http.Request) {
	// Appelé après authentification par jeton : trace l'usage sans bloquer.
	if auth.Method(r.Context()) != auth.MethodToken {
		return
	}
	raw := strings.TrimPrefix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), auth.TokenPrefix)
	d, err := s.opt.Store.DeviceByTokenHash(r.Context(), hashToken(strings.TrimSpace(raw)))
	if err != nil {
		return
	}
	_ = s.opt.Store.TouchDevice(r.Context(), d.ID, clientIP(r))
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// deviceGuard interdit aux jetons d'appareil de gérer les appareils :
// un téléphone perdu ne doit pas pouvoir en appairer d'autres.
func (s *Server) deviceGuard(w http.ResponseWriter, r *http.Request) bool {
	if auth.Method(r.Context()) == auth.MethodToken {
		writeErr(w, http.StatusForbidden, "la gestion des appareils se fait depuis Relay web")
		return false
	}
	return true
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	if !s.deviceGuard(w, r) {
		return
	}
	list, err := s.opt.Store.ListDevices(r.Context(), auth.User(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// serverURL est l'adresse publique à inscrire dans le QR code d'appairage.
func (s *Server) serverURL(r *http.Request) string {
	if s.opt.PublicOrigin != "" {
		return s.opt.PublicOrigin
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	if !s.deviceGuard(w, r) {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.fail(w, r, err)
		return
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	d, err := s.opt.Store.CreateDevice(r.Context(), auth.User(r.Context()), in.Name, hashToken(secret))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	token := auth.TokenPrefix + secret
	server := s.serverURL(r)
	s.audit(r, "device.create", d.ID, d.Name)
	writeJSON(w, http.StatusCreated, map[string]any{
		"device":  d,
		"token":   token,
		"server":  server,
		"pairing": "relay://pair?" + url.Values{"server": {server}, "token": {token}}.Encode(),
	})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	if !s.deviceGuard(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.opt.Store.DeleteDevice(r.Context(), auth.User(r.Context()), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "device.delete", id, "")
	w.WriteHeader(http.StatusNoContent)
}
