package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/guac"
)

func TestRecordingsArePrivateToOwner(t *testing.T) {
	a, err := auth.New(auth.Config{
		Mode: "header", Header: "X-Test-User",
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, "")
	s := New(Options{Store: e.st, Auth: a, RecordingsDir: t.TempDir()})
	capture, err := s.startRecording(pendingSession{name: "Test", cfg: guac.Config{Protocol: "ssh"}}, "alice@local", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer capture.discard()
	frame := guac.New("sync", "10").Encode()
	capture.recording.Capture([]byte(frame), []string{"sync", "10"})
	if err := capture.finish(); err != nil {
		t.Fatal(err)
	}

	handler := s.Handler()
	do := func(method, path, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Test-User", user)
		req.Header.Set("X-Relay", "1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	path := "/api/recordings/" + capture.meta.ID
	if got := do(http.MethodGet, "/api/recordings", "bob@local"); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatalf("liste d'un autre utilisateur : %d %s", got.Code, got.Body)
	}
	if got := do(http.MethodGet, path, "bob@local"); got.Code != http.StatusNotFound {
		t.Fatalf("lecture d'un autre utilisateur : %d", got.Code)
	}
	do(http.MethodDelete, path, "bob@local")
	if got := do(http.MethodGet, path, "alice@local"); got.Code != http.StatusOK || got.Body.String() != frame {
		t.Fatalf("lecture du propriétaire : %d %s", got.Code, got.Body)
	}
	if got := do(http.MethodDelete, path, "alice@local"); got.Code != http.StatusNoContent {
		t.Fatalf("suppression du propriétaire : %d", got.Code)
	}
	if got := do(http.MethodGet, path, "alice@local"); got.Code != http.StatusNotFound {
		t.Fatalf("lecture après suppression : %d", got.Code)
	}
}
