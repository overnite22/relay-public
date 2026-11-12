package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Le pont relie guacd (TCP) au WebSocket VNC : les octets passent dans les deux sens.
func TestBridgeRelaysBytes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		_ = ws.Write(r.Context(), websocket.MessageBinary, []byte("RFB 003.008\n"))
		_, data, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		_ = ws.Write(r.Context(), websocket.MessageBinary, append([]byte("echo:"), data...))
		time.Sleep(100 * time.Millisecond)
	}))
	defer upstream.Close()

	s := New(Options{GuacdAddr: "127.0.0.1:4822"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context) (*websocket.Conn, error) {
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(upstream.URL, "http"), nil)
		return c, err
	}
	go s.serveBridge(ln, dial, "test")

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	hello := make([]byte, 12)
	if _, err := io.ReadFull(conn, hello); err != nil || string(hello) != "RFB 003.008\n" {
		t.Fatalf("version RFB : %q %v", hello, err)
	}
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 17)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "echo:RFB 003.008\n" {
		t.Fatalf("écho : %q %v", reply, err)
	}
	// Une seule connexion : le port est refermé ensuite.
	if c2, err := net.DialTimeout("tcp", ln.Addr().String(), 500*time.Millisecond); err == nil {
		c2.Close()
		t.Fatal("second accès au pont accepté")
	}
}

func TestNativeConsoleNeedsBridgeHost(t *testing.T) {
	e := newEnv(t, "")
	var entry struct {
		ID string `json:"id"`
	}
	if code := e.do("POST", "/api/entries", map[string]any{"name": "vm", "kind": "pve", "settings": map[string]any{"sourceId": "x", "node": "n", "vmid": 100, "type": "qemu"}}, &entry); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("création : %d", code)
	}
	var out map[string]string
	if code := e.do("POST", "/api/entries/"+entry.ID+"/connect", map[string]any{"width": 800, "height": 600, "native": true}, &out); code != http.StatusBadRequest || !strings.Contains(out["error"], "RELAY_BRIDGE_HOST") {
		t.Fatalf("sans pont : %d %v", code, out)
	}
}
