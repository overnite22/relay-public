package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/vault"
)

type localEnv struct {
	t      *testing.T
	st     *store.Store
	srv    *httptest.Server
	server *Server
	client *http.Client
}

func newLocalEnv(t *testing.T) *localEnv {
	e := newEnv(t, "")
	a, err := auth.New(auth.Config{Mode: "local"})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := vault.New(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	s := New(Options{Vault: v, DataDir: dir, RecordingsDir: filepath.Join(dir, "recordings"), Auth: a, Store: e.st, LocalSetupToken: "test-bootstrap-code", PublicOrigin: "http://127.0.0.1"})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &localEnv{t: t, st: e.st, srv: srv, server: s, client: &http.Client{Jar: jar}}
}
func (e *localEnv) do(client *http.Client, method, path string, body any) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, e.srv.URL+path, rd)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Relay", "1")
	resp, err := client.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}
func (e *localEnv) bootstrap() {
	e.t.Helper()
	code, b := e.do(e.client, "POST", "/api/auth/setup", map[string]string{"email": "Admin@example.com", "password": "test-admin-password", "token": "test-bootstrap-code"})
	if code != 201 {
		e.t.Fatalf("setup %d %s", code, b)
	}
}
func TestLocalBootstrapLoginAndLogout(t *testing.T) {
	e := newLocalEnv(t)
	if code, _ := e.do(e.client, "GET", "/api/me", nil); code != 401 {
		t.Fatal(code)
	}
	code, b := e.do(e.client, "POST", "/api/auth/setup", map[string]string{"email": "attacker@example.com", "password": "test-admin-password", "token": "wrong"})
	if code != 403 {
		t.Fatalf("invalid setup code: %d %s", code, b)
	}
	if n, _ := e.st.LocalUserCount(context.Background()); n != 0 {
		t.Fatal("created account without setup code")
	}
	e.bootstrap()
	code, b = e.do(e.client, "GET", "/api/me", nil)
	if code != 200 || !strings.Contains(string(b), `"isAdmin":true`) {
		t.Fatalf("admin session %d %s", code, b)
	}
	user, _ := e.st.LocalUser(context.Background(), "admin@example.com")
	if strings.Contains(user.PasswordHash, "test-admin-password") || !auth.VerifyPassword("test-admin-password", user.PasswordHash) {
		t.Fatal("password not correctly hashed")
	}
	code, _ = e.do(e.client, "POST", "/api/auth/setup", map[string]string{"email": "second@example.com", "password": "test-admin-password", "token": "test-bootstrap-code"})
	if code == 201 {
		t.Fatal("second bootstrap accepted")
	}
	code, b = e.do(e.client, "PUT", "/api/users/admin@example.com", map[string]bool{"disabled": true})
	if code != 400 {
		t.Fatalf("last admin disabled: %d %s", code, b)
	}
	if code, _ = e.do(e.client, "POST", "/api/auth/logout", map[string]string{}); code != 204 {
		t.Fatal(code)
	}
	if code, _ = e.do(e.client, "GET", "/api/me", nil); code != 401 {
		t.Fatal("logout failed", code)
	}
	for _, email := range []string{"admin@example.com", "unknown@example.com"} {
		code, b = e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": email, "password": "incorrect-password"})
		if code != 401 || !strings.Contains(string(b), "identifiants invalides") {
			t.Fatalf("credential enumeration %d %s", code, b)
		}
	}
	if code, b = e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": "ADMIN@example.com", "password": "test-admin-password"}); code != 200 {
		t.Fatalf("login %d %s", code, b)
	}
}
func TestLocalAccountAuthorizationAndRevocation(t *testing.T) {
	e := newLocalEnv(t)
	e.bootstrap()
	code, b := e.do(e.client, "POST", "/api/users", map[string]any{"email": "user@example.com", "password": "test-user-password", "admin": false})
	if code != 201 {
		t.Fatalf("create %d %s", code, b)
	}
	jar, _ := cookiejar.New(nil)
	userClient := &http.Client{Jar: jar}
	if code, b = e.do(userClient, "POST", "/api/auth/login", map[string]string{"email": "user@example.com", "password": "test-user-password"}); code != 200 {
		t.Fatalf("login %d %s", code, b)
	}
	for _, p := range []string{"/api/users", "/api/backups/export", "/api/backups/restore"} {
		method := "POST"
		if p == "/api/users" {
			method = "GET"
		}
		if code, _ = e.do(userClient, method, p, map[string]string{}); code != 403 {
			t.Fatalf("user access %s %d", p, code)
		}
	}
	raw := strings.Repeat("d", 43)
	hash := sha256.Sum256([]byte(raw))
	if _, err := e.st.CreateDevice(context.Background(), "admin@example.com", "test", hash[:]); err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("GET", e.srv.URL+"/api/users", nil)
	r.Header.Set("Authorization", "Bearer rlt_"+raw)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("bearer managed users", res.StatusCode)
	}
	if code, b = e.do(e.client, "PUT", "/api/users/user@example.com", map[string]bool{"disabled": true}); code != 204 {
		t.Fatalf("disable %d %s", code, b)
	}
	if code, _ = e.do(userClient, "GET", "/api/me", nil); code != 401 {
		t.Fatal("disabled session survived", code)
	}
	code, _ = e.do(userClient, "POST", "/api/auth/login", map[string]string{"email": "user@example.com", "password": "test-user-password"})
	if code != 401 {
		t.Fatal("disabled login accepted", code)
	}
}
func TestLocalPasswordChangeRevokesSessionsAndDevices(t *testing.T) {
	e := newLocalEnv(t)
	e.bootstrap()
	old, _ := e.st.LocalUser(context.Background(), "admin@example.com")
	raw := strings.Repeat("x", 43)
	hash := sha256.Sum256([]byte(raw))
	e.st.CreateDevice(context.Background(), old.Email, "test", hash[:])
	code, b := e.do(e.client, "POST", "/api/account/password", map[string]string{"current": "test-admin-password", "password": "replacement-password"})
	if code != 204 {
		t.Fatalf("change %d %s", code, b)
	}
	if code, _ = e.do(e.client, "GET", "/api/me", nil); code != 401 {
		t.Fatal("old session survived")
	}
	if _, err := e.st.DeviceByTokenHash(context.Background(), hash[:]); err == nil {
		t.Fatal("paired device survived password change")
	}
	if err := e.st.CreateLocalSession(context.Background(), old.Email, hash[:], time.Now().Add(time.Hour).UnixMilli(), old.PasswordHash); err == nil {
		t.Fatal("stale verified password minted a session")
	}
	if code, _ = e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": old.Email, "password": "test-admin-password"}); code != 401 {
		t.Fatal("old password accepted")
	}
	if code, b = e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": old.Email, "password": "replacement-password"}); code != 200 {
		t.Fatalf("new password %d %s", code, b)
	}
	// Logical backups never replace installation login accounts or revive sessions.
	snap, err := e.st.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range snap.Tables {
		if strings.HasPrefix(table.Name, "local_") {
			t.Fatal("local access policy exported")
		}
	}
}
func TestLocalAuthCSRFThrottleAndCookieFlags(t *testing.T) {
	e := newLocalEnv(t)
	r, _ := http.NewRequest("POST", e.srv.URL+"/api/auth/login", strings.NewReader(`{"email":"x@example.com","password":"bad"}`))
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("login CSRF header not required")
	}
	for i := 0; i < 10; i++ {
		if code, _ := e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": "x@example.com", "password": "bad"}); code != 401 {
			t.Fatal(i, code)
		}
	}
	if code, _ := e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": "x@example.com", "password": "bad"}); code != 401 {
		t.Fatal("throttling exposed account status", code)
	}
	e.server.opt.PublicOrigin = "https://relay.example.com"
	w := httptest.NewRecorder()
	r = httptest.NewRequest("POST", "https://relay.example.com/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","password":"test-admin-password","token":"test-bootstrap-code"}`))
	r.Header.Set("X-Relay", "1")
	e.server.Handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie", cookies)
	}
}

type delayedBody struct {
	start   chan struct{}
	release chan struct{}
	reader  io.Reader
	once    sync.Once
}

func (b *delayedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.start); <-b.release })
	return b.reader.Read(p)
}
func TestRevokedLocalSessionCannotCreatePersistentAuthority(t *testing.T) {
	for _, action := range []struct{ method, path, body string }{{"POST", "/api/users", `{"email":"intruder@example.com","password":"intruder-password","admin":true}`}, {"PUT", "/api/users/user@example.com", `{"admin":true}`}, {"POST", "/api/devices", `{"name":"revoked-cookie-device"}`}} {
		t.Run(action.path, func(t *testing.T) {
			e := newLocalEnv(t)
			e.bootstrap()
			if code, b := e.do(e.client, "POST", "/api/users", map[string]any{"email": "user@example.com", "password": "test-user-password"}); code != 201 {
				t.Fatalf("create %d %s", code, b)
			}
			body := &delayedBody{start: make(chan struct{}), release: make(chan struct{}), reader: strings.NewReader(action.body)}
			r := httptest.NewRequest(action.method, action.path, body)
			r.Header.Set("X-Relay", "1")
			u, _ := url.Parse(e.srv.URL)
			for _, c := range e.client.Jar.Cookies(u) {
				r.AddCookie(c)
			}
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { e.server.Handler().ServeHTTP(w, r); close(done) }()
			<-body.start // The middleware and initial role check have accepted the cookie.
			code, b := e.do(e.client, "POST", "/api/account/password", map[string]string{"current": "test-admin-password", "password": "replacement-password"})
			if code != 204 {
				close(body.release)
				<-done
				t.Fatalf("reset %d %s", code, b)
			}
			close(body.release)
			<-done
			if w.Code < 400 {
				t.Fatalf("revoked in-flight request succeeded: %s %d %s", action.path, w.Code, w.Body.String())
			}
			if _, err := e.st.LocalUser(context.Background(), "intruder@example.com"); err == nil {
				t.Fatal("revoked session minted an admin")
			}
			user, _ := e.st.LocalUser(context.Background(), "user@example.com")
			if user.Admin {
				t.Fatal("revoked session promoted an admin")
			}
			devices, _ := e.st.ListDevices(context.Background(), "admin@example.com")
			if len(devices) != 0 {
				t.Fatal("revoked session minted a device")
			}
		})
	}
}
func TestPublicLoginBodyDoesNotLockContentRestore(t *testing.T) {
	e := newLocalEnv(t)
	body := &delayedBody{start: make(chan struct{}), release: make(chan struct{}), reader: strings.NewReader(`{"email":"user@example.com","password":"incorrect-password"}`)}
	r := httptest.NewRequest("POST", "/api/auth/login", body)
	r.Header.Set("X-Relay", "1")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { e.server.Handler().ServeHTTP(w, r); close(done) }()
	<-body.start
	unlocked := e.server.dataGuard.TryLock()
	if unlocked {
		e.server.dataGuard.Unlock()
	}
	close(body.release)
	<-done
	if !unlocked {
		t.Fatal("public login holds content restore lock while reading an untrusted body")
	}
}
func TestContentRestoreDoesNotReviveDevicesOrReplaceLocalAccounts(t *testing.T) {
	e := newLocalEnv(t)
	e.bootstrap()
	ctx := context.Background()
	raw := strings.Repeat("q", 43)
	hash := sha256.Sum256([]byte(raw))
	d, err := e.st.CreateDevice(ctx, "admin@example.com", "test", hash[:])
	if err != nil {
		t.Fatal(err)
	}
	snap, err := e.st.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A legacy or crafted archive is not an authority for authentication tokens.
	text := func(s string) store.SnapshotCell { return store.SnapshotCell{Text: &s} }
	num := time.Now().UnixMilli()
	snap.Tables = append(snap.Tables, store.SnapshotTable{Name: "device_tokens", Columns: []string{"id", "user", "name", "token_hash", "created_at", "last_used_at", "last_ip"}, Rows: [][]store.SnapshotCell{{text(d.ID), text(d.User), text(d.Name), {Blob: hash[:]}, {Number: &num}, {}, text("")}}})
	if err = e.st.DeleteDevice(ctx, d.User, d.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.st.RestoreSnapshot(ctx, snap, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.DeviceByTokenHash(ctx, hash[:]); err == nil {
		t.Fatal("revoked device revived by content restore")
	}
	if u, err := e.st.LocalUser(ctx, "admin@example.com"); err != nil || !u.Admin {
		t.Fatal("installation account replaced", err)
	}
}

func TestRevokedLocalAdminCannotResumeBackupRequest(t *testing.T) {
	for _, path := range []string{"/api/backups/export", "/api/backups/restore"} {
		t.Run(path, func(t *testing.T) {
			e := newLocalEnv(t)
			e.bootstrap()
			code, archive := e.do(e.client, "POST", "/api/backups/export", map[string]string{"password": "test-backup-password"})
			if code != 200 {
				t.Fatalf("archive %d %s", code, archive)
			}
			payload := []byte(`{"password":"test-backup-password"}`)
			if strings.HasSuffix(path, "restore") {
				payload = archive
			}
			body := &delayedBody{start: make(chan struct{}), release: make(chan struct{}), reader: bytes.NewReader(payload)}
			r := httptest.NewRequest("POST", path, body)
			r.Header.Set("X-Relay", "1")
			r.Header.Set("X-Backup-Password", base64.StdEncoding.EncodeToString([]byte("test-backup-password")))
			u, _ := url.Parse(e.srv.URL)
			for _, c := range e.client.Jar.Cookies(u) {
				r.AddCookie(c)
			}
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { e.server.Handler().ServeHTTP(w, r); close(done) }()
			<-body.start
			code, b := e.do(e.client, "POST", "/api/account/password", map[string]string{"current": "test-admin-password", "password": "replacement-password"})
			if code != 204 {
				close(body.release)
				<-done
				t.Fatalf("reset %d %s", code, b)
			}
			folder, err := e.st.SaveFolder(context.Background(), store.Folder{Name: "after reset"})
			if err != nil {
				t.Fatal(err)
			}
			close(body.release)
			<-done
			if w.Code < 400 {
				t.Fatalf("revoked backup request succeeded %s %d", path, w.Code)
			}
			folders, err := e.st.ListFolders(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, f := range folders {
				found = found || f.ID == folder.ID
			}
			if !found {
				t.Fatal("revoked admin restored stale content")
			}
		})
	}
}
func TestUnknownLoginNamesCannotExhaustKnownAccountAdmission(t *testing.T) {
	e := newLocalEnv(t)
	e.bootstrap()
	for i := 0; i < cap(e.server.login.work); i++ {
		e.server.login.work <- struct{}{}
	}
	for i := 0; i < 2060; i++ {
		code, _ := e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": fmt.Sprintf("unknown%d@example.com", i), "password": "incorrect-password"})
		if code != 429 {
			t.Fatal(code)
		}
	}
	for i := 0; i < cap(e.server.login.work); i++ {
		<-e.server.login.work
	}
	for i := 0; i < 11; i++ {
		code, _ := e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": fmt.Sprintf("unknown%d@example.com", i), "password": "incorrect-password"})
		if code != 401 {
			t.Fatal(code)
		}
	}
	if code, b := e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "test-admin-password"}); code != 200 {
		t.Fatalf("unknown names blocked known account: %d %s", code, b)
	}
}

func TestLocalThrottlingDoesNotVerifyMorePasswordGuesses(t *testing.T) {
	e := newLocalEnv(t)
	e.bootstrap()
	e.do(e.client, "POST", "/api/auth/logout", map[string]string{})
	for i := 0; i < 10; i++ {
		if code, _ := e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "incorrect-password"}); code != 401 {
			t.Fatal(code)
		}
	}
	if code, b := e.do(e.client, "POST", "/api/auth/login", map[string]string{"email": "admin@example.com", "password": "test-admin-password"}); code != 401 {
		t.Fatalf("throttled account still verified real password: %d %s", code, b)
	}
	if code, _ := e.do(e.client, "GET", "/api/me", nil); code != 401 {
		t.Fatal("throttled login minted session")
	}
}
