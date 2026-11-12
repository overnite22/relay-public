// Package vault chiffre les secrets stockés en base (AES-256-GCM).
//
// Chaque valeur chiffrée est liée à son emplacement par des données
// authentifiées (AAD), par ex. "credential:<id>:password" : un chiffré copié
// vers un autre champ ou un autre identifiant ne se déchiffre pas.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	keySize = 32
	prefix  = "v1:"
)

// ErrDecrypt signale un chiffré invalide, altéré ou lié à un autre emplacement.
var ErrDecrypt = errors.New("vault: déchiffrement impossible")

// Vault chiffre et déchiffre des secrets avec la clé maître.
type Vault struct {
	aead cipher.AEAD
	key  []byte
}

// New construit un Vault à partir d'une clé de 32 octets.
func New(key []byte) (*Vault, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("vault: la clé maître doit faire %d octets (reçu %d)", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead, key: append([]byte(nil), key...)}, nil
}

// LoadOrCreateKey lit la clé maître (base64) depuis path, ou la génère avec
// les droits 0600 si le fichier n'existe pas. created indique une création.
func LoadOrCreateKey(path string) (key []byte, created bool, err error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, false, fmt.Errorf("vault: clé maître illisible dans %s : %w", path, err)
		}
		if len(key) != keySize {
			return nil, false, fmt.Errorf("vault: la clé maître de %s doit faire %d octets", path, keySize)
		}
		return key, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	key = make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		return nil, false, err
	}
	return key, true, f.Sync()
}

// Seal chiffre plaintext lié à aad. Une valeur vide reste vide (pas de secret).
func (v *Vault) Seal(plaintext, aad string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := v.aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return prefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open déchiffre une valeur produite par Seal avec le même aad.
func (v *Vault) Open(sealed, aad string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, prefix) {
		return "", ErrDecrypt
	}
	raw, err := base64.StdEncoding.DecodeString(sealed[len(prefix):])
	if err != nil || len(raw) < v.aead.NonceSize() {
		return "", ErrDecrypt
	}
	n := v.aead.NonceSize()
	pt, err := v.aead.Open(nil, raw[:n], raw[n:], []byte(aad))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(pt), nil
}

// BackupKey is included only inside the password-encrypted backup archive.
func (v *Vault) BackupKey() []byte { return append([]byte(nil), v.key...) }
