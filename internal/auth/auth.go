// Package auth identifie l'utilisateur derrière le SSO.
//
// Trois modes :
//   - "userinfo" (défaut) : le cookie de session est transmis à oauth2-proxy
//     (/oauth2/userinfo) qui renvoie l'e-mail de l'utilisateur ;
//   - "header" : un en-tête posé par un reverse proxy de confiance
//     (adresse source dans RELAY_TRUSTED_PROXIES) ;
//   - "dev" : utilisateur fixe, pour le développement local uniquement.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

type ctxKey struct{}

type methodKey struct{}

// MethodToken : requête authentifiée par un jeton d'appareil (Authorization: Bearer).
const MethodToken = "token"

// TokenPrefix précède tout jeton d'appareil Relay.
const TokenPrefix = "rlt_"

// TokenResolver traduit un jeton porteur (sans son préfixe) en utilisateur.
// Il renvoie ErrUnauthenticated si le jeton est inconnu.
type TokenResolver func(ctx context.Context, token string) (string, error)

// ErrUnauthenticated : aucune identité valide.
var ErrUnauthenticated = errors.New("non authentifié")

// ErrForbidden : identité valide mais absente de la liste autorisée.
var ErrForbidden = errors.New("accès refusé")

// Config paramètre l'authentification.
type Config struct {
	Mode           string // userinfo | header | dev
	UserinfoURL    string // ex. http://oauth2-proxy:4180/oauth2/userinfo
	Header         string // mode header : nom de l'en-tête
	TrustedProxies []netip.Prefix
	DevUser        string
	Allowed        []string // e-mails autorisés (vide = tout utilisateur authentifié)
}

// Authenticator résout l'utilisateur d'une requête.
type Authenticator struct {
	cfg     Config
	client  *http.Client
	allowed map[string]bool

	mu    sync.Mutex
	cache map[[32]byte]cached

	tokens TokenResolver
}

type cached struct {
	user string
	exp  time.Time
}

// New construit un Authenticator.
func New(cfg Config) (*Authenticator, error) {
	switch cfg.Mode {
	case "userinfo":
		if cfg.UserinfoURL == "" {
			return nil, errors.New("auth: RELAY_OAUTH2_USERINFO_URL requis en mode userinfo")
		}
	case "header":
		if cfg.Header == "" || len(cfg.TrustedProxies) == 0 {
			return nil, errors.New("auth: RELAY_USER_HEADER et RELAY_TRUSTED_PROXIES requis en mode header")
		}
	case "dev":
		if cfg.DevUser == "" {
			return nil, errors.New("auth: RELAY_DEV_USER requis en mode dev")
		}
	default:
		return nil, errors.New("auth: mode inconnu " + cfg.Mode)
	}
	a := &Authenticator{
		cfg:     cfg,
		client:  &http.Client{Timeout: 3 * time.Second},
		allowed: map[string]bool{},
		cache:   map[[32]byte]cached{},
	}
	for _, u := range cfg.Allowed {
		if u = strings.ToLower(strings.TrimSpace(u)); u != "" {
			a.allowed[u] = true
		}
	}
	return a, nil
}

// SetTokenResolver active l'authentification par jeton d'appareil.
func (a *Authenticator) SetTokenResolver(f TokenResolver) { a.tokens = f }

// Resolve renvoie l'e-mail (en minuscules) de l'utilisateur de la requête
// et la méthode employée ("" = SSO/dev, MethodToken = jeton d'appareil).
func (a *Authenticator) Resolve(r *http.Request) (string, string, error) {
	var user, method string
	var err error
	switch {
	case a.tokens != nil && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
		raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if !strings.HasPrefix(raw, TokenPrefix) {
			return "", "", ErrUnauthenticated
		}
		user, err = a.tokens(r.Context(), strings.TrimPrefix(raw, TokenPrefix))
		method = MethodToken
	case a.cfg.Mode == "dev":
		user = a.cfg.DevUser
	case a.cfg.Mode == "header":
		user, err = a.fromHeader(r)
	default:
		user, err = a.fromUserinfo(r)
	}
	if err != nil {
		return "", "", err
	}
	user = strings.ToLower(strings.TrimSpace(user))
	if user == "" {
		return "", "", ErrUnauthenticated
	}
	if len(a.allowed) > 0 && !a.allowed[user] {
		return "", "", ErrForbidden
	}
	return user, method, nil
}

func (a *Authenticator) fromHeader(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", ErrUnauthenticated
	}
	ip = ip.Unmap()
	for _, p := range a.cfg.TrustedProxies {
		if p.Contains(ip) {
			return r.Header.Get(a.cfg.Header), nil
		}
	}
	return "", ErrUnauthenticated
}

func (a *Authenticator) fromUserinfo(r *http.Request) (string, error) {
	cookie := r.Header.Get("Cookie")
	if cookie == "" {
		return "", ErrUnauthenticated
	}
	key := sha256.Sum256([]byte(cookie))
	a.mu.Lock()
	if c, ok := a.cache[key]; ok && time.Now().Before(c.exp) {
		a.mu.Unlock()
		return c.user, nil
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.UserinfoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", ErrUnauthenticated
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrUnauthenticated
	}
	var info struct {
		Email string `json:"email"`
		User  string `json:"user"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&info); err != nil {
		return "", ErrUnauthenticated
	}
	user := info.Email
	if user == "" {
		user = info.User
	}
	a.mu.Lock()
	if len(a.cache) > 1000 {
		a.cache = map[[32]byte]cached{}
	}
	a.cache[key] = cached{user: user, exp: time.Now().Add(30 * time.Second)}
	a.mu.Unlock()
	return user, nil
}

// Middleware refuse les requêtes non authentifiées et place l'utilisateur
// dans le contexte.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, method, err := a.Resolve(r)
		if err != nil {
			code, msg := http.StatusUnauthorized, "Authentification requise"
			if errors.Is(err, ErrForbidden) {
				code, msg = http.StatusForbidden, "Accès refusé pour ce compte"
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, user)
		if method != "" {
			ctx = context.WithValue(ctx, methodKey{}, method)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Method renvoie la méthode d'authentification de la requête ("" = SSO/dev).
func Method(ctx context.Context) string {
	m, _ := ctx.Value(methodKey{}).(string)
	return m
}

// User renvoie l'utilisateur placé dans le contexte par Middleware.
func User(ctx context.Context) string {
	u, _ := ctx.Value(ctxKey{}).(string)
	return u
}
