package totp

import (
	"encoding/base32"
	"testing"
	"time"
)

// Vecteurs de test de la RFC 6238, annexe B.
func TestRFC6238Vectors(t *testing.T) {
	cases := []struct {
		alg, seed string
		ts        int64
		want      string
	}{
		{"SHA1", "12345678901234567890", 59, "94287082"},
		{"SHA1", "12345678901234567890", 1111111109, "07081804"},
		{"SHA1", "12345678901234567890", 20000000000, "65353130"},
		{"SHA256", "12345678901234567890123456789012", 59, "46119246"},
		{"SHA512", "1234567890123456789012345678901234567890123456789012345678901234", 1234567890, "93441116"},
	}
	for _, c := range cases {
		p := Params{Secret: []byte(c.seed), Digits: 8, Period: 30, Algorithm: c.alg}
		got, _ := p.Code(time.Unix(c.ts, 0))
		if got != c.want {
			t.Errorf("%s @%d : %s, attendu %s", c.alg, c.ts, got, c.want)
		}
	}
}

func TestParseRawSecret(t *testing.T) {
	enc := base32.StdEncoding.EncodeToString([]byte("12345678901234567890"))
	p, err := Parse("  " + enc[:4] + " " + enc[4:] + " ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Digits != 6 || p.Period != 30 || p.Algorithm != "SHA1" {
		t.Fatalf("valeurs par défaut : %+v", p)
	}
	code, rem := p.Code(time.Unix(59, 0))
	if code != "287082" || rem != 1 {
		t.Fatalf("code %s, reste %d", code, rem)
	}
}

func TestParseURI(t *testing.T) {
	p, err := Parse("otpauth://totp/Relay:admin?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&digits=8&period=60&algorithm=sha256")
	if err != nil {
		t.Fatal(err)
	}
	if p.Digits != 8 || p.Period != 60 || p.Algorithm != "SHA256" {
		t.Fatalf("%+v", p)
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"", "!!!", "otpauth://hotp/x?secret=GEZDGNBV", "otpauth://totp/x?secret=GEZDGNBV&digits=12", "otpauth://totp/x?secret=GEZDGNBV&algorithm=md5"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("%q accepté", s)
		}
	}
}
