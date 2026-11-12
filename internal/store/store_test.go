package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ptr(s string) *string { return &s }

func TestFolderTreeAndCycles(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	root, err := s.SaveFolder(ctx, Folder{Name: " Homelab "})
	if err != nil || root.Name != "Homelab" {
		t.Fatalf("%+v %v", root, err)
	}
	child, err := s.SaveFolder(ctx, Folder{Name: "Proxmox", ParentID: &root.ID})
	if err != nil {
		t.Fatal(err)
	}
	root.ParentID = &child.ID
	if _, err := s.SaveFolder(ctx, root); !isInvalid(err) {
		t.Fatalf("cycle accepté : %v", err)
	}
	root.ParentID = &root.ID
	if _, err := s.SaveFolder(ctx, root); !isInvalid(err) {
		t.Fatalf("auto-parent accepté : %v", err)
	}
	if _, err := s.SaveFolder(ctx, Folder{Name: "x", ParentID: ptr("absent")}); !isInvalid(err) {
		t.Fatalf("parent inexistant accepté : %v", err)
	}
	if _, err := s.SaveFolder(ctx, Folder{Name: "  "}); !isInvalid(err) {
		t.Fatalf("nom vide accepté : %v", err)
	}
}

func TestDeleteFolderCascades(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	root, _ := s.SaveFolder(ctx, Folder{Name: "A"})
	sub, _ := s.SaveFolder(ctx, Folder{Name: "B", ParentID: &root.ID})
	e, err := s.SaveEntry(ctx, Entry{Name: "srv", Kind: KindSSH, Host: "10.0.0.1", FolderID: &sub.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEntry(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("entrée encore présente : %v", err)
	}
	fs, _ := s.ListFolders(ctx)
	if len(fs) != 0 {
		t.Fatalf("sous-dossier encore présent : %+v", fs)
	}
}

func TestEntryValidationAndDefaults(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	e, err := s.SaveEntry(ctx, Entry{Name: "DC01", Kind: KindRDP, Host: "dc01.lab"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Port != 3389 || string(e.Settings) != "{}" {
		t.Fatalf("valeurs par défaut : %+v", e)
	}
	bad := []Entry{
		{Name: "", Kind: KindSSH, Host: "h"},
		{Name: "x", Kind: "telnet", Host: "h"},
		{Name: "x", Kind: KindSSH},
		{Name: "x", Kind: KindSSH, Host: "h", Port: 70000},
		{Name: "x", Kind: KindWeb, URL: "ftp://x"},
		{Name: "x", Kind: KindSSH, Host: "h", Settings: []byte(`[1]`)},
		{Name: "x", Kind: KindSSH, Host: "h", CredentialID: ptr("absent")},
	}
	for i, b := range bad {
		if _, err := s.SaveEntry(ctx, b); !isInvalid(err) {
			t.Errorf("cas %d accepté : %v", i, err)
		}
	}
	e.Name, e.Favorite = "DC01 (prod)", true
	e2, err := s.SaveEntry(ctx, e)
	if err != nil || e2.Name != "DC01 (prod)" || !e2.Favorite {
		t.Fatalf("mise à jour : %+v %v", e2, err)
	}
	if _, err := s.SaveEntry(ctx, Entry{ID: "absent", Name: "x", Kind: KindSSH, Host: "h"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mise à jour d'une entrée absente : %v", err)
	}
}

func TestCredentialDetachOnDelete(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	c, err := s.SaveCredential(ctx, Credential{ID: NewID(), Name: "admin lab", Username: "admin", PasswordEnc: "v1:xx"})
	if err != nil || !c.HasPassword || c.HasTOTP {
		t.Fatalf("%+v %v", c, err)
	}
	e, err := s.SaveEntry(ctx, Entry{Name: "srv", Kind: KindSSH, Host: "h", CredentialID: &c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountEntriesUsingCredential(ctx, c.ID); n != 1 {
		t.Fatalf("compte : %d", n)
	}
	if err := s.DeleteCredential(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	e, _ = s.GetEntry(ctx, e.ID)
	if e.CredentialID != nil {
		t.Fatal("l'entrée pointe encore vers l'identifiant supprimé")
	}
}

func TestAudit(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, a := range []string{"un", "deux", "trois"} {
		if err := s.Audit(ctx, "moi@lab", a, "cible", ""); err != nil {
			t.Fatal(err)
		}
	}
	ev, err := s.ListAudit(ctx, 2)
	if err != nil || len(ev) != 2 || ev[0].Action != "trois" {
		t.Fatalf("%+v %v", ev, err)
	}
}

func isInvalid(err error) bool {
	var e ErrInvalid
	return errors.As(err, &e)
}
