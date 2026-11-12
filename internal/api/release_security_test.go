package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overnite22/relay-public/internal/auth"
	"golang.org/x/crypto/ssh"
)

func TestBackupRequiresExplicitSSOAdministrator(t *testing.T) {
	e := newEnv(t, "")
	a, _ := auth.New(auth.Config{Mode: "dev", DevUser: "user@example.com"})
	s := New(Options{Auth: a, Store: e.st, AdminUsers: []string{"admin@example.com"}})
	for _, path := range []string{"/api/backups/export", "/api/backups/restore"} {
		req := httptest.NewRequest("POST", path, strings.NewReader("invalid"))
		req.Header.Set("X-Relay", "1")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("non-admin accepted %s: %d", path, w.Code)
		}
	}
	a, _ = auth.New(auth.Config{Mode: "dev", DevUser: "admin@example.com"})
	s = New(Options{Auth: a, Store: e.st, AdminUsers: []string{" ADMIN@example.com "}})
	raw := strings.Repeat("x", 43)
	hash := sha256.Sum256([]byte(raw))
	if _, err := e.st.CreateDevice(context.Background(), "admin@example.com", "test", hash[:]); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/backups/export", "/api/backups/restore"} {
		req := httptest.NewRequest("POST", path, strings.NewReader("invalid"))
		req.Header.Set("X-Relay", "1")
		req.Header.Set("Authorization", "Bearer rlt_"+raw)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("bearer accepted %s: %d", path, w.Code)
		}
	}
}

func TestDriveNamespaceHasNoEmailAliasCollision(t *testing.T) {
	if safeDir("a+b@example.com") == safeDir("a_b@example.com") {
		t.Fatal("RDP drives collide")
	}
	if len(safeDir("a+b@example.com")) != 64 {
		t.Fatal("unexpected drive namespace")
	}
}

func TestInteractiveSSHHostKeyIsPinnedBeforeAuthentication(t *testing.T) {
	e := newEnv(t, "")
	s := New(Options{Store: e.st})
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewSignerFromKey(private)
	rsaPrivate, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsaKey, _ := ssh.NewSignerFromKey(rsaPrivate)
	ecdsaPrivate, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecdsaKey, _ := ssh.NewSignerFromKey(ecdsaPrivate)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				config := &ssh.ServerConfig{NoClientAuth: true}
				config.AddHostKey(key)
				config.AddHostKey(rsaKey)
				config.AddHostKey(ecdsaKey)
				server, _, _, _ := ssh.NewServerConn(conn, config)
				if server != nil {
					server.Close()
				}
			}()
		}
	}()
	address := ln.Addr().String()
	line, err := s.verifySSHHostKey(context.Background(), address)
	if err != nil || !strings.Contains(line, "ecdsa-sha2-nistp256 ") {
		t.Fatalf("first pin failed: %s %v", line, err)
	}
	if _, err := s.verifySSHHostKey(context.Background(), address); err != nil {
		t.Fatal(err)
	}
	e.st.ForgetHostKey(context.Background(), address)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	different, _ := ssh.NewSignerFromKey(other)
	e.st.RememberHostKey(context.Background(), address, different.PublicKey().Marshal(), "test")
	if _, err := s.verifySSHHostKey(context.Background(), address); err == nil {
		t.Fatal("changed host key accepted")
	}
}
