// Package totp calcule des codes à usage unique RFC 6238 (TOTP).
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Params décrit un générateur TOTP.
type Params struct {
	Secret    []byte
	Digits    int
	Period    int
	Algorithm string // SHA1, SHA256, SHA512
}

// Parse accepte un secret base32 brut ou une URI otpauth://totp/...
func Parse(s string) (Params, error) {
	s = strings.TrimSpace(s)
	p := Params{Digits: 6, Period: 30, Algorithm: "SHA1"}
	secret := s
	if strings.HasPrefix(strings.ToLower(s), "otpauth://") {
		u, err := url.Parse(s)
		if err != nil {
			return p, fmt.Errorf("totp: URI invalide : %w", err)
		}
		if !strings.EqualFold(u.Host, "totp") {
			return p, errors.New("totp: seules les URI otpauth://totp sont gérées")
		}
		q := u.Query()
		secret = q.Get("secret")
		if d := q.Get("digits"); d != "" {
			n, err := strconv.Atoi(d)
			if err != nil || n < 6 || n > 8 {
				return p, errors.New("totp: digits doit valoir 6 à 8")
			}
			p.Digits = n
		}
		if per := q.Get("period"); per != "" {
			n, err := strconv.Atoi(per)
			if err != nil || n <= 0 || n > 300 {
				return p, errors.New("totp: period invalide")
			}
			p.Period = n
		}
		if a := q.Get("algorithm"); a != "" {
			p.Algorithm = strings.ToUpper(a)
		}
	}
	switch p.Algorithm {
	case "SHA1", "SHA256", "SHA512":
	default:
		return p, fmt.Errorf("totp: algorithme %q non géré", p.Algorithm)
	}
	clean := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(secret))
	clean = strings.TrimRight(clean, "=")
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
	if err != nil || len(key) == 0 {
		return p, errors.New("totp: secret base32 invalide")
	}
	p.Secret = key
	return p, nil
}

// Code renvoie le code valable à l'instant t et les secondes restantes.
func (p Params) Code(t time.Time) (code string, remaining int) {
	counter := uint64(t.Unix()) / uint64(p.Period)
	var h func() hash.Hash
	switch p.Algorithm {
	case "SHA256":
		h = sha256.New
	case "SHA512":
		h = sha512.New
	default:
		h = sha1.New
	}
	mac := hmac.New(h, p.Secret)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < p.Digits; i++ {
		mod *= 10
	}
	code = fmt.Sprintf("%0*d", p.Digits, bin%mod)
	remaining = p.Period - int(uint64(t.Unix())%uint64(p.Period))
	return code, remaining
}
