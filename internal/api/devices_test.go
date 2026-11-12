package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overnite22/relay-public/internal/store"
)

func TestDevicePairingAndBearer(t *testing.T) {
	e := newEnv(t, "")

	var created struct {
		Device  store.Device `json:"device"`
		Token   string       `json:"token"`
		Server  string       `json:"server"`
		Pairing string       `json:"pairing"`
	}
	if code := e.do("POST", "/api/devices", map[string]string{"name": "iPhone"}, &created); code != http.StatusCreated {
		t.Fatalf("création : %d", code)
	}
	if !strings.HasPrefix(created.Token, "rlt_") || created.Device.User != "moi@lab.fr" {
		t.Fatalf("réponse inattendue : %+v", created)
	}
	if !strings.HasPrefix(created.Pairing, "relay://pair?") || !strings.Contains(created.Pairing, "token=rlt_") {
		t.Fatalf("QR d'appairage : %s", created.Pairing)
	}
	if code := e.do("POST", "/api/devices", map[string]string{"name": "  "}, nil); code != http.StatusBadRequest {
		t.Fatalf("nom vide accepté : %d", code)
	}

	bearer := func(tok, method, path string) (int, string) {
		req, _ := http.NewRequest(method, e.srv.URL+path, nil)
		req.Header.Set("X-Relay", "1")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf [512]byte
		n, _ := resp.Body.Read(buf[:])
		return resp.StatusCode, string(buf[:n])
	}

	// Le jeton authentifie l'API et la liste des appareils reste réservée au web.
	if code, body := bearer(created.Token, "GET", "/api/me"); code != http.StatusOK || !strings.Contains(body, "moi@lab.fr") {
		t.Fatalf("jeton refusé : %d %s", code, body)
	}
	if code, _ := bearer(created.Token, "GET", "/api/devices"); code != http.StatusForbidden {
		t.Fatalf("gestion des appareils par jeton : %d", code)
	}
	if code, _ := bearer(created.Token, "POST", "/api/devices"); code != http.StatusForbidden {
		t.Fatalf("appairage par jeton : %d", code)
	}
	// Jetons invalides.
	for _, tok := range []string{"rlt_" + strings.Repeat("x", 43), "abc", created.Token + "1", "rlt_"} {
		if code, _ := bearer(tok, "GET", "/api/me"); code != http.StatusUnauthorized {
			t.Fatalf("jeton %q accepté : %d", tok, code)
		}
	}

	// Dernière utilisation tracée, puis révocation.
	var list []store.Device
	e.do("GET", "/api/devices", nil, &list)
	if len(list) != 1 || list[0].LastUsedAt == nil || list[0].LastIP == "" {
		t.Fatalf("usage non tracé : %+v", list)
	}
	if code := e.do("DELETE", "/api/devices/"+created.Device.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("révocation : %d", code)
	}
	if code, _ := bearer(created.Token, "GET", "/api/me"); code != http.StatusUnauthorized {
		t.Fatalf("jeton révoqué encore accepté : %d", code)
	}
	if code := e.do("DELETE", "/api/devices/"+created.Device.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("double révocation : %d", code)
	}
}
