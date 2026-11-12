package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
)

func TestUserinfoMode(t *testing.T) {
	var calls atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Cookie") != "_sso=ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"email":"Moi@Lab.fr"}`))
	}))
	defer idp.Close()

	a, err := New(Config{Mode: "userinfo", UserinfoURL: idp.URL, Allowed: []string{"moi@lab.fr"}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Cookie", "_sso=ok")
	for i := 0; i < 3; i++ {
		u, _, err := a.Resolve(req)
		if err != nil || u != "moi@lab.fr" {
			t.Fatalf("%q %v", u, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cache inopérant : %d appels", calls.Load())
	}
	bad := httptest.NewRequest("GET", "/api/me", nil)
	bad.Header.Set("Cookie", "_sso=ko")
	if _, _, err := a.Resolve(bad); err != ErrUnauthenticated {
		t.Fatalf("cookie invalide : %v", err)
	}
	if _, _, err := a.Resolve(httptest.NewRequest("GET", "/", nil)); err != ErrUnauthenticated {
		t.Fatalf("sans cookie : %v", err)
	}
}

func TestAllowedList(t *testing.T) {
	a, _ := New(Config{Mode: "dev", DevUser: "autre@lab.fr", Allowed: []string{"moi@lab.fr"}})
	if _, _, err := a.Resolve(httptest.NewRequest("GET", "/", nil)); err != ErrForbidden {
		t.Fatalf("%v", err)
	}
}

func TestHeaderModeTrustsOnlyProxies(t *testing.T) {
	a, err := New(Config{Mode: "header", Header: "X-Forwarded-Email",
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-Email", "moi@lab.fr")
	req.RemoteAddr = "172.18.0.5:5555"
	if u, _, err := a.Resolve(req); err != nil || u != "moi@lab.fr" {
		t.Fatalf("proxy de confiance : %q %v", u, err)
	}
	req.RemoteAddr = "192.0.2.50:5555"
	if _, _, err := a.Resolve(req); err != ErrUnauthenticated {
		t.Fatalf("en-tête accepté depuis une IP non fiable : %v", err)
	}
}

func TestMiddleware(t *testing.T) {
	a, _ := New(Config{Mode: "dev", DevUser: "moi@lab.fr"})
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(User(r.Context())))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Body.String() != "moi@lab.fr" {
		t.Fatalf("%q", rec.Body.String())
	}
}

func TestBearerToken(t *testing.T) {
	a, err := New(Config{Mode: "dev", DevUser: "dev@lab.fr", Allowed: []string{"dev@lab.fr", "mobile@lab.fr"}})
	if err != nil {
		t.Fatal(err)
	}
	a.SetTokenResolver(func(_ context.Context, tok string) (string, error) {
		if tok == "secret" {
			return "mobile@lab.fr", nil
		}
		if tok == "banned" {
			return "autre@lab.fr", nil
		}
		return "", ErrUnauthenticated
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer rlt_secret")
	u, m, err := a.Resolve(req)
	if err != nil || u != "mobile@lab.fr" || m != MethodToken {
		t.Fatalf("jeton valide : %q %q %v", u, m, err)
	}
	for _, h := range []string{"Bearer rlt_nope", "Bearer secret", "Bearer rlt_banned"} {
		req.Header.Set("Authorization", h)
		if _, _, err := a.Resolve(req); err == nil {
			t.Fatalf("%s accepté", h)
		}
	}
	// Sans en-tête, le mode de base s'applique.
	req.Header.Del("Authorization")
	if u, m, err := a.Resolve(req); err != nil || u != "dev@lab.fr" || m != "" {
		t.Fatalf("mode dev : %q %q %v", u, m, err)
	}
}
