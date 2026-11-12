package api

import (
	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/web"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestPWAAssetsArePublicAndApplicationStaysAuthenticated(t *testing.T) {
	a, err := auth.New(auth.Config{Mode: "header", Header: "X-User", TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Auth: a, Static: web.Handler()}).Handler()
	for _, path := range []string{"/manifest.webmanifest", "/sw.js", "/icons/relay-192.png", "/icons/relay-512.png", "/icons/relay-maskable-512.png", "/icons/apple-touch-icon.png"} {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if path == "/sw.js" && (w.Header().Get("Service-Worker-Allowed") != "/" || w.Header().Get("Cache-Control") != "no-cache") {
			t.Fatal("worker caching or scope incorrect")
		}
	}
	for _, path := range []string{"/", "/api/tree", "/api/credentials", "/icons/other.png"} {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("unprotected path %s: %d", path, w.Code)
		}
	}
}
