package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func testVault(t *testing.T) *Vault {
	t.Helper()
	v, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSealOpenRoundTrip(t *testing.T) {
	v := testVault(t)
	s, err := v.Seal("P@ssw0rd é", "credential:a:password")
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Open(s, "credential:a:password")
	if err != nil || got != "P@ssw0rd é" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestSealIsRandomized(t *testing.T) {
	v := testVault(t)
	a, _ := v.Seal("x", "aad")
	b, _ := v.Seal("x", "aad")
	if a == b {
		t.Fatal("deux chiffrements identiques : nonce non aléatoire")
	}
}

func TestOpenRejectsOtherAAD(t *testing.T) {
	v := testVault(t)
	s, _ := v.Seal("secret", "credential:a:password")
	if _, err := v.Open(s, "credential:b:password"); err != ErrDecrypt {
		t.Fatalf("attendu ErrDecrypt, reçu %v", err)
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	v := testVault(t)
	s, _ := v.Seal("secret", "aad")
	b := []byte(s)
	b[len(b)-2] ^= 1
	if _, err := v.Open(string(b), "aad"); err != ErrDecrypt {
		t.Fatalf("attendu ErrDecrypt, reçu %v", err)
	}
	if _, err := v.Open("garbage", "aad"); err != ErrDecrypt {
		t.Fatalf("attendu ErrDecrypt, reçu %v", err)
	}
}

func TestEmptyStaysEmpty(t *testing.T) {
	v := testVault(t)
	s, err := v.Seal("", "aad")
	if err != nil || s != "" {
		t.Fatalf("got %q, %v", s, err)
	}
	p, err := v.Open("", "aad")
	if err != nil || p != "" {
		t.Fatalf("got %q, %v", p, err)
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "master.key")
	k1, created, err := LoadOrCreateKey(path)
	if err != nil || !created || len(k1) != 32 {
		t.Fatalf("création : %v %v %d", err, created, len(k1))
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("droits du fichier : %v %v", st.Mode().Perm(), err)
	}
	k2, created, err := LoadOrCreateKey(path)
	if err != nil || created || !bytes.Equal(k1, k2) {
		t.Fatalf("relecture : %v %v", err, created)
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New([]byte("court")); err == nil {
		t.Fatal("clé trop courte acceptée")
	}
}
