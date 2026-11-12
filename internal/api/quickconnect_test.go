package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overnite22/relay-public/internal/auth"
)

func TestQuickConnectPreparesAnEphemeralSession(t *testing.T) {
	e := newEnv(t, "")
	a, err := auth.New(auth.Config{Mode: "dev", DevUser: "quick@local"})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Store: e.st, Auth: a, RecordingsDir: t.TempDir()})
	handler := s.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/quick-connect", strings.NewReader(body))
		r.Header.Set("X-Relay", "1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	for _, body := range []string{
		`{"kind":"web","host":"localhost"}`,
		`{"kind":"ssh","host":"https://localhost"}`,
		`{"kind":"ssh","host":"localhost","port":65536}`,
		`{"kind":"ssh","host":"localhost","credentialId":"x","password":"secret"}`,
	} {
		if w := post(body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid target: %d", w.Code)
		}
	}
	w := post(`{"kind":"rdp","host":"[2001:db8::1]","username":"tester","domain":"LAB","password":"test-password","record":true}`)
	var response map[string]string
	err = json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != http.StatusOK || err != nil || strings.Contains(w.Body.String(), "test-password") ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("quick connect: %d %v", w.Code, err)
	}
	p, ok := s.tokens.take(response["token"], "quick@local")
	if !ok || p.cfg.Protocol != "rdp" || !p.record ||
		p.cfg.Params["hostname"] != "2001:db8::1" || p.cfg.Params["port"] != "3389" ||
		p.cfg.Params["password"] != "test-password" || p.cfg.Params["domain"] != "LAB" {
		t.Fatal("incorrect ephemeral session")
	}
	if _, ok := s.tokens.take(response["token"], "quick@local"); ok {
		t.Fatal("token could be used twice")
	}
	entries, err := e.st.ListEntries(context.Background())
	if err != nil || len(entries) != 0 {
		t.Fatalf("inventory changed: %d %v", len(entries), err)
	}
}
