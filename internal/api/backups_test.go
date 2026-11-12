package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/overnite22/relay-public/internal/auth"
	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/vault"
)

func TestPortableBackupAndAtomicRestore(t *testing.T) {
	makeServer := func(key byte) (*Server, string) {
		dir := t.TempDir()
		st, err := store.Open(filepath.Join(dir, "relay.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		v, err := vault.New(bytes.Repeat([]byte{key}, 32))
		if err != nil {
			t.Fatal(err)
		}
		a, _ := auth.New(auth.Config{Mode: "dev", DevUser: "owner@local"})
		return New(Options{Store: st, Vault: v, Auth: a, DataDir: dir, AdminUsers: []string{"owner@local"}, RecordingsDir: filepath.Join(dir, "recordings")}), dir
	}
	source, sourceDir := makeServer(1)
	ctx := context.Background()
	sealed, _ := source.opt.Vault.Seal("secret-value", aad("credential-id", "password"))
	cred, err := source.opt.Store.SaveCredential(ctx, store.Credential{ID: "credential-id", Name: "admin", Username: "root", PasswordEnc: sealed})
	if err != nil {
		t.Fatal(err)
	}
	sourceSecret, _ := source.opt.Vault.Seal("source-token", sourceAAD("source-id"))
	if _, err := source.opt.Store.SaveSource(ctx, store.Source{ID: "source-id", Kind: "netbox", Name: "NetBox", URL: "https://netbox.test", SecretEnc: sourceSecret}); err != nil {
		t.Fatal(err)
	}
	pendingResults := json.RawMessage(`[{"entryId":"x","status":"running"}]`)
	if err := source.opt.Store.SaveRun(ctx, store.CommandRun{ID: "pending-run", At: time.Now(), User: "owner@local", Command: "uptime", Targets: 1, Results: pendingResults}); err != nil {
		t.Fatal(err)
	}
	folder, _ := source.opt.Store.SaveFolder(ctx, store.Folder{Name: "servers"})
	if err := source.opt.Store.SetFolderCredentials(ctx, folder.ID, map[string]*string{"ssh": &cred.ID}); err != nil {
		t.Fatal(err)
	}
	entry, _ := source.opt.Store.SaveEntry(ctx, store.Entry{Name: "server", Kind: "ssh", Host: "localhost", FolderID: &folder.ID, Notes: "# Documentation"})
	item, err := source.opt.Store.AddAttachment(ctx, entry.ID, "procedure.txt", []byte("procedure"))
	if err != nil {
		t.Fatal(err)
	}
	source.opt.Store.SaveWorkspace(ctx, "owner@local", store.Workspace{Name: "morning", EntryIDs: []string{entry.ID}})
	recordPath := filepath.Join(source.opt.RecordingsDir, strings.Repeat("a", 64), strings.Repeat("b", 32), "session.guac")
	os.MkdirAll(filepath.Dir(recordPath), 0o700)
	os.WriteFile(recordPath, []byte("4.sync,1.1;"), 0o600)
	req := httptest.NewRequest("POST", "/api/backups/export", strings.NewReader(`{"password":"backup-password"}`))
	req.Header.Set("X-Relay", "1")
	out := httptest.NewRecorder()
	source.Handler().ServeHTTP(out, req)
	if out.Code != 200 || bytes.Contains(out.Body.Bytes(), []byte("secret-value")) || bytes.Contains(out.Body.Bytes(), []byte("Documentation")) {
		t.Fatal("export failed", out.Code)
	}
	archive := append([]byte(nil), out.Body.Bytes()...)

	dest, _ := makeServer(2)
	keep, _ := dest.opt.Store.SaveEntry(ctx, store.Entry{Name: "keep if invalid", Kind: "ssh", Host: "localhost"})
	restore := func(data []byte, password string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/backups/restore", bytes.NewReader(data))
		req.Header.Set("X-Relay", "1")
		req.Header.Set("X-Backup-Password", base64.StdEncoding.EncodeToString([]byte(password)))
		w := httptest.NewRecorder()
		dest.Handler().ServeHTTP(w, req)
		return w
	}
	if got := restore(archive, "wrong"); got.Code != http.StatusBadRequest {
		t.Fatal("wrong password accepted", got.Code)
	}
	if _, err := dest.opt.Store.GetEntry(ctx, keep.ID); err != nil {
		t.Fatal("failed restore changed live database")
	}
	if got := restore(archive[:len(archive)-10], "backup-password"); got.Code != http.StatusBadRequest {
		t.Fatal("truncated archive accepted", got.Code)
	}
	dest.dataGuard.RLock()
	busy := restore(archive, "backup-password")
	dest.dataGuard.RUnlock()
	if busy.Code != http.StatusConflict {
		t.Fatal("restore accepted during session", busy.Code)
	}
	if got := restore(archive, "backup-password"); got.Code != 200 {
		t.Fatalf("restore failed: %d %s", got.Code, got.Body)
	}
	if _, err := dest.opt.Store.GetEntry(ctx, keep.ID); err != store.ErrNotFound {
		t.Fatal("restore did not replace inventory")
	}
	restored, err := dest.opt.Store.GetEntry(ctx, entry.ID)
	if err != nil || restored.Notes != "# Documentation" {
		t.Fatal("inventory lost", err)
	}
	sec, err := dest.sessionSecrets(ctx, restored)
	if err != nil || sec.password != "secret-value" || sec.username != "root" {
		t.Fatal("portable vault inheritance failed", err)
	}
	_, data, err := dest.opt.Store.ReadAttachment(ctx, entry.ID, item.ID)
	if err != nil || string(data) != "procedure" {
		t.Fatal("attachment lost", err)
	}
	rec, err := os.ReadFile(filepath.Join(dest.opt.RecordingsDir, strings.Repeat("a", 64), strings.Repeat("b", 32), "session.guac"))
	if err != nil || string(rec) != "4.sync,1.1;" {
		t.Fatal("recording lost", err)
	}
	src, err := dest.opt.Store.GetSource(ctx, "source-id")
	if err != nil {
		t.Fatal(err)
	}
	token, err := dest.opt.Vault.Open(src.SecretEnc, sourceAAD(src.ID))
	if err != nil || token != "source-token" {
		t.Fatal("source token not portable", err)
	}
	run, err := dest.opt.Store.GetRun(ctx, "pending-run")
	if err != nil || run.FinishedAt == nil || run.Failed != 1 || !bytes.Contains(run.Results, []byte("canceled")) {
		t.Fatal("restored run left active", err)
	}
	if files, _ := filepath.Glob(filepath.Join(sourceDir, ".backup-*")); len(files) != 0 {
		t.Fatal("temporary secret files remain")
	}
}
