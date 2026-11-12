package auth

import "testing"

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("a strong test password")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("a strong test password", hash) || VerifyPassword("wrong test password", hash) {
		t.Fatal("password verification failed")
	}
	if _, err = HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if VerifyPassword("anything", DummyPasswordHash) {
		t.Fatal("dummy hash accepted")
	}
	for _, s := range []string{"", "argon2id-v1:", "argon2id-v1:AAAA:AAAA"} {
		if VerifyPassword("anything", s) {
			t.Fatal("malformed hash accepted")
		}
	}
}
