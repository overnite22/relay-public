package guac

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestEncodeCountsRunes(t *testing.T) {
	got := New("key", "é€", "").Encode()
	if got != "3.key,2.é€,0.;" {
		t.Fatalf("%q", got)
	}
}

func TestSplit(t *testing.T) {
	cases := []struct {
		in    string
		n     int
		elems []string
		err   bool
	}{
		{"4.sync,3.123;rest", 13, []string{"sync", "123"}, false},
		{"4.sync,3.12", 0, nil, false},
		{"4.sy", 0, nil, false},
		{"5.a;b;c,1.x;", 12, []string{"a;b;c", "x"}, false},
		{"2.é€;", 8, []string{"é€"}, false},
		{"x.sync;", 0, nil, true},
		{"4.syncX", 0, nil, true},
		{"0.,4.ping,2.10;", 15, []string{"", "ping", "10"}, false},
	}
	for _, c := range cases {
		n, elems, err := Split([]byte(c.in))
		if (err != nil) != c.err || n != c.n || strings.Join(elems, "|") != strings.Join(c.elems, "|") {
			t.Errorf("%q : n=%d elems=%q err=%v", c.in, n, elems, err)
		}
	}
	// UTF-8 coupé au milieu d'un caractère : incomplet, pas une erreur.
	b := []byte("2.é€;")
	if n, _, err := Split(b[:5]); n != 0 || err != nil {
		t.Errorf("UTF-8 tronqué : %d %v", n, err)
	}
}

func TestParseRoundTrip(t *testing.T) {
	in := New("connect", "VERSION_1_5_0", "srv;1", "", "mot de passe é")
	out, err := Parse(in.Encode())
	if err != nil || out.Opcode != "connect" || strings.Join(out.Args, "|") != strings.Join(in.Args, "|") {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := Parse("4.sync;4.sync;"); err == nil {
		t.Fatal("deux instructions acceptées par Parse")
	}
}

// fakeGuacd joue le rôle de guacd pour un échange complet.
func fakeGuacd(t *testing.T, check func(connect Instruction)) (addr string, fromClient chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	fromClient = make(chan string, 16)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		sel, err := readInstruction(r)
		if err != nil || sel.Opcode != "select" || sel.Args[0] != "ssh" {
			t.Errorf("select : %+v %v", sel, err)
			return
		}
		c.Write([]byte(New("args", "VERSION_1_5_0", "hostname", "port", "username", "password").Encode()))
		var connect Instruction
		for {
			in, err := readInstruction(r)
			if err != nil {
				t.Errorf("handshake : %v", err)
				return
			}
			if in.Opcode == "connect" {
				connect = in
				break
			}
		}
		check(connect)
		c.Write([]byte(New("ready", "$abc").Encode()))
		// Une instruction envoyée en deux morceaux pour tester le regroupement.
		c.Write([]byte("4.sync,3."))
		time.Sleep(20 * time.Millisecond)
		c.Write([]byte("123;"))
		for {
			in, err := readInstruction(r)
			if err != nil {
				return
			}
			fromClient <- in.Encode()
		}
	}()
	return ln.Addr().String(), fromClient
}

func TestHandshakeAndRelay(t *testing.T) {
	addr, fromClient := fakeGuacd(t, func(c Instruction) {
		want := "VERSION_1_5_0|srv.lab|22|root|s3cr;t"
		if got := strings.Join(c.Args, "|"); got != want {
			t.Errorf("connect : %q, attendu %q", got, want)
		}
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"guacamole"}})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		br := bufio.NewReader(conn)
		id, err := Handshake(conn, br, Config{Protocol: "ssh", Params: map[string]string{
			"hostname": "srv.lab", "port": "22", "username": "root", "password": "s3cr;t",
		}})
		if err != nil || id != "$abc" {
			t.Errorf("handshake : %q %v", id, err)
			return
		}
		if err := Relay(r.Context(), ws, conn, br, "tunnel-1", nil); err != nil {
			t.Errorf("relay : %v", err)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{Subprotocols: []string{"guacamole"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()

	read := func() string {
		_, b, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read(); got != "0.,8.tunnel-1;" {
		t.Fatalf("UUID du tunnel : %q", got)
	}
	if got := read(); got != "4.sync,3.123;" {
		t.Fatalf("instruction regroupée : %q", got)
	}
	// Ping interne : renvoyé au navigateur, jamais transmis à guacd.
	ws.Write(ctx, websocket.MessageText, []byte("0.,4.ping,2.42;"))
	if got := read(); got != "0.,4.ping,2.42;" {
		t.Fatalf("écho du ping : %q", got)
	}
	ws.Write(ctx, websocket.MessageText, []byte("3.key,5.65307,1.1;4.sync,3.123;"))
	for _, want := range []string{"3.key,5.65307,1.1;", "4.sync,3.123;"} {
		select {
		case got := <-fromClient:
			if got != want {
				t.Fatalf("reçu par guacd : %q, attendu %q", got, want)
			}
		case <-ctx.Done():
			t.Fatal("rien reçu côté guacd")
		}
	}
	ws.Close(websocket.StatusNormalClosure, "")
}

func TestHandshakeError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		defer c.Close()
		r := bufio.NewReader(c)
		readInstruction(r)
		c.Write([]byte(New("error", "Protocole inconnu", "256").Encode()))
	}()
	conn, _ := net.Dial("tcp", ln.Addr().String())
	defer conn.Close()
	_, err := Handshake(conn, bufio.NewReader(conn), Config{Protocol: "nope"})
	if err == nil || !strings.Contains(err.Error(), "Protocole inconnu") {
		t.Fatalf("%v", err)
	}
}
