package api

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/overnite22/relay-public/internal/store"
	"github.com/overnite22/relay-public/internal/vault"
)

func TestFolderInheritanceForSessionsAndCommands(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	var cred store.Credential
	if code := e.do("POST", "/api/credentials", map[string]any{"name": "linux", "username": "inherited", "password": "inherited-password"}, &cred); code != 200 {
		t.Fatal(code)
	}
	parent, _ := e.st.SaveFolder(ctx, store.Folder{Name: "Linux"})
	child, _ := e.st.SaveFolder(ctx, store.Folder{Name: "Servers", ParentID: &parent.ID})
	if code := e.do("PUT", "/api/folders/"+parent.ID+"/credentials", map[string]any{"credentials": map[string]any{"ssh": cred.ID}}, nil); code != 204 {
		t.Fatal(code)
	}
	entry, _ := e.st.SaveEntry(ctx, store.Entry{Name: "test", Kind: "ssh", Host: "localhost", FolderID: &child.ID})
	id, err := e.st.EffectiveCredentialID(ctx, entry)
	if err != nil || id == nil || *id != cred.ID {
		t.Fatalf("inherit: %v %v", id, err)
	}
	v, _ := vault.New(bytes.Repeat([]byte{1}, 32))
	s := New(Options{Store: e.st, Vault: v})
	sec, err := s.sessionSecrets(ctx, entry)
	if err != nil || sec.username != "inherited" || sec.password != "inherited-password" {
		t.Fatal("session credentials were not inherited", err)
	}
	entry.Settings = json.RawMessage(`{"credentialMode":"none"}`)
	if id, err := e.st.EffectiveCredentialID(ctx, entry); err != nil || id != nil {
		t.Fatal("opt-out ignored")
	}
	entry.Settings = json.RawMessage(`{}`)
	if err := e.st.SetFolderCredentials(ctx, child.ID, map[string]*string{"ssh": nil}); err != nil {
		t.Fatal(err)
	}
	if id, err := e.st.EffectiveCredentialID(ctx, entry); err != nil || id != nil {
		t.Fatal("closest folder override ignored")
	}
	entry.CredentialID = &cred.ID
	if id, err := e.st.EffectiveCredentialID(ctx, entry); err != nil || id == nil {
		t.Fatal("explicit entry credential ignored")
	}
	folders, err := e.st.ListFolders(ctx)
	if err != nil || len(folders) != 2 {
		t.Fatal(err)
	}
	work, err := e.st.SaveWorkspace(ctx, "owner", store.Workspace{Name: "daily", EntryIDs: []string{entry.ID, entry.ID}})
	if err != nil || len(work.EntryIDs) != 1 {
		t.Fatal("workspace deduplication", err)
	}
	items, err := e.st.ListWorkspaces(ctx, "other")
	if err != nil || len(items) != 0 {
		t.Fatal("private workspace exposed")
	}
	if err := e.st.DeleteWorkspace(ctx, "other", work.ID); err != store.ErrNotFound {
		t.Fatal("workspace ownership ignored")
	}
}
