package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/store"
)

const localCookie = "relay_session"

type loginBucket struct {
	count int
	until time.Time
}
type loginGate struct {
	sync.Mutex
	buckets map[string]loginBucket
	work    chan struct{}
}

func (g *loginGate) allow(key string, limit int) bool {
	g.Lock()
	defer g.Unlock()
	now := time.Now()
	if g.buckets == nil {
		g.buckets = map[string]loginBucket{}
	}
	for k, b := range g.buckets {
		if !now.Before(b.until) {
			delete(g.buckets, k)
		}
	}
	b, exists := g.buckets[key]
	if !exists {
		if len(g.buckets) >= 2048 {
			return false
		}
		b.until = now.Add(time.Minute)
	}
	if b.count >= limit {
		return false
	}
	b.count++
	g.buckets[key] = b
	return true
}
func (s *Server) localMode() bool { return s.opt.Auth != nil && s.opt.Auth.Mode() == "local" }
func (s *Server) localRoutes(r chi.Router) {
	r.Route("/api/auth", func(r chi.Router) {
		r.Use(s.csrf)
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					ctl := http.NewResponseController(w)
					_ = ctl.SetReadDeadline(time.Now().Add(10 * time.Second))
					defer ctl.SetReadDeadline(time.Time{})
				}
				next.ServeHTTP(w, r)
			})
		})
		r.Get("/status", s.authStatus)
		r.Post("/setup", s.localSetup)
		r.Post("/login", s.localLogin)
		r.Post("/logout", s.localLogout)
	})
}
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	initialized := true
	if s.localMode() {
		n, err := s.opt.Store.LocalUserCount(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		initialized = n > 0
	}
	writeJSON(w, 200, map[string]any{"mode": s.opt.Auth.Mode(), "initialized": initialized})
}
func (s *Server) localOnly(w http.ResponseWriter) bool {
	if !s.localMode() {
		writeErr(w, 404, "authentification locale désactivée")
		return false
	}
	return true
}
func canonicalEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || len(email) > 254 || parsed.Address != email {
		return "", store.ErrInvalid{Msg: "adresse e-mail invalide"}
	}
	return email, nil
}
func (s *Server) passwordWork(w http.ResponseWriter) bool {
	select {
	case s.login.work <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "5")
		writeErr(w, 429, "réessayez dans quelques secondes")
		return false
	}
}
func (s *Server) loginAttempt(w http.ResponseWriter, r *http.Request, email, password string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	// Unknown strings share one bucket, so arbitrary emails cannot exhaust
	// limiter storage and prevent established accounts from logging in.
	key := email
	if email != "#bootstrap" {
		_, err := s.opt.Store.LocalUser(r.Context(), email)
		if errors.Is(err, store.ErrNotFound) {
			key = "#unknown"
		} else if err != nil {
			s.fail(w, r, err)
			return false
		}
	}
	if !s.passwordWork(w) {
		return false
	}
	if !s.login.allow(key, 10) {
		if r.URL.Path == "/api/auth/login" {
			// Keep throttled and incorrect credentials indistinguishable:
			// same Argon2 cost and generic 401, without checking the real hash.
			auth.VerifyPassword(password, auth.DummyPasswordHash)
			<-s.login.work
			writeErr(w, 401, "identifiants invalides")
		} else {
			<-s.login.work
			w.Header().Set("Retry-After", "60")
			writeErr(w, 429, "trop de tentatives, réessayez dans une minute")
		}
		return false
	}
	return true
}
func (s *Server) secureLocalCookie() bool {
	u, err := url.Parse(s.opt.PublicOrigin)
	if err != nil {
		return true
	}
	host := u.Hostname()
	return !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1"))
}
func (s *Server) issueLocalSession(w http.ResponseWriter, r *http.Request, email, passwordHash string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(12 * time.Hour)
	if err := s.opt.Store.CreateLocalSession(r.Context(), email, hashToken(token), expires.UnixMilli(), passwordHash); err != nil {
		return err
	}
	// Revoke an old browser session while rotating to a freshly generated token.
	if c, err := r.Cookie(localCookie); err == nil {
		_ = s.opt.Store.RevokeLocalSession(r.Context(), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: localCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.secureLocalCookie(), SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600, Expires: expires})
	return nil
}
func (s *Server) resolveLocalSession(r *http.Request) (string, error) {
	c, err := r.Cookie(localCookie)
	if err != nil || len(c.Value) != 43 {
		return "", auth.ErrUnauthenticated
	}
	email, err := s.opt.Store.ResolveLocalSession(r.Context(), hashToken(c.Value))
	if err != nil {
		return "", auth.ErrUnauthenticated
	}
	return email, nil
}
func (s *Server) localSetup(w http.ResponseWriter, r *http.Request) {
	if !s.localOnly(w) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.loginAttempt(w, r, "#bootstrap", in.Password) {
		return
	}
	defer func() { <-s.login.work }()
	if s.opt.LocalSetupToken == "" || subtle.ConstantTimeCompare([]byte(in.Token), []byte(s.opt.LocalSetupToken)) != 1 {
		writeErr(w, 403, "code d'initialisation invalide")
		return
	}
	email, err := canonicalEmail(in.Email)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err = s.opt.Store.CreateLocalUser(r.Context(), store.LocalUser{Email: email, PasswordHash: hash, Admin: true}, true, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.opt.DataDir != "" {
		_ = os.Remove(filepath.Join(s.opt.DataDir, "setup.key"))
	}
	if err = s.issueLocalSession(w, r, email, hash); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.opt.Store.Audit(r.Context(), email, "user.bootstrap", email, "")
	writeJSON(w, 201, map[string]bool{"ok": true})
}
func (s *Server) localLogin(w http.ResponseWriter, r *http.Request) {
	if !s.localOnly(w) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if len(email) > 254 || len(in.Password) > 1024 {
		writeErr(w, 401, "identifiants invalides")
		return
	}
	if !s.loginAttempt(w, r, email, in.Password) {
		return
	}
	defer func() { <-s.login.work }()
	user, err := s.opt.Store.LocalUser(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	hash := user.PasswordHash
	if err != nil {
		hash = auth.DummyPasswordHash
	}
	valid := auth.VerifyPassword(in.Password, hash)
	if !valid || err != nil || user.Disabled {
		_ = s.opt.Store.Audit(r.Context(), email, "login.failed", "", "")
		writeErr(w, 401, "identifiants invalides")
		return
	}
	if err = s.issueLocalSession(w, r, email, hash); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.opt.Store.Audit(r.Context(), email, "login.success", "", "")
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) localLogout(w http.ResponseWriter, r *http.Request) {
	if !s.localOnly(w) {
		return
	}
	if c, err := r.Cookie(localCookie); err == nil {
		if err = s.opt.Store.RevokeLocalSession(r.Context(), hashToken(c.Value)); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: localCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.secureLocalCookie(), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(204)
}
func (s *Server) localAdministrator(r *http.Request) bool {
	if !s.localMode() || auth.Method(r.Context()) == auth.MethodToken {
		return false
	}
	user, err := s.opt.Store.LocalUser(r.Context(), auth.User(r.Context()))
	return err == nil && user.Admin && !user.Disabled
}
func (s *Server) localAdminGuard(w http.ResponseWriter, r *http.Request) bool {
	if !s.localAdministrator(r) {
		writeErr(w, 403, "administrateur local requis depuis une session web")
		return false
	}
	return true
}
func (s *Server) listLocalUsers(w http.ResponseWriter, r *http.Request) {
	if !s.localAdminGuard(w, r) {
		return
	}
	users, err := s.opt.Store.LocalUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, users)
}
func (s *Server) createLocalUser(w http.ResponseWriter, r *http.Request) {
	if !s.localAdminGuard(w, r) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	email, err := canonicalEmail(in.Email)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.passwordWork(w) {
		return
	}
	defer func() { <-s.login.work }()
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err = s.opt.Store.CreateLocalUser(r.Context(), store.LocalUser{Email: email, PasswordHash: hash, Admin: in.Admin}, false, localProof(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.create", email, "")
	writeJSON(w, 201, map[string]bool{"ok": true})
}
func (s *Server) updateLocalUser(w http.ResponseWriter, r *http.Request) {
	if !s.localAdminGuard(w, r) {
		return
	}
	email := chi.URLParam(r, "email")
	var in struct {
		Password string `json:"password"`
		Admin    *bool  `json:"admin"`
		Disabled *bool  `json:"disabled"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	hash := ""
	if in.Password != "" {
		if !s.passwordWork(w) {
			return
		}
		defer func() { <-s.login.work }()
		var err error
		hash, err = auth.HashPassword(in.Password)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	if err := s.opt.Store.UpdateLocalUser(r.Context(), email, hash, in.Admin, in.Disabled, "", localProof(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.update", email, "")
	w.WriteHeader(204)
}
func (s *Server) changeLocalPassword(w http.ResponseWriter, r *http.Request) {
	if !s.localOnly(w) || !s.deviceGuard(w, r) {
		return
	}
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.loginAttempt(w, r, auth.User(r.Context()), in.Current) {
		return
	}
	defer func() { <-s.login.work }()
	user, err := s.opt.Store.LocalUser(r.Context(), auth.User(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !auth.VerifyPassword(in.Current, user.PasswordHash) {
		writeErr(w, 403, "mot de passe actuel invalide")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err = s.opt.Store.UpdateLocalUser(r.Context(), user.Email, hash, nil, nil, user.PasswordHash, localProof(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "user.password", user.Email, "")
	w.WriteHeader(204)
}

// Local account/device authority is checked on every request, without a cache.
func (s *Server) activeLocalUser(ctx context.Context, email string) bool {
	u, err := s.opt.Store.LocalUser(ctx, email)
	return err == nil && !u.Disabled
}

func localProof(r *http.Request) []byte {
	c, err := r.Cookie(localCookie)
	if err != nil {
		return nil
	}
	return hashToken(c.Value)
}
