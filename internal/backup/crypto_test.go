package backup

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestChunkedArchiveIntegrity(t *testing.T) {
	data := make([]byte, blockSize*2+17)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	var encrypted bytes.Buffer
	if err := Encrypt(&encrypted, bytes.NewReader(data), "password"); err != nil {
		t.Fatal(err)
	}
	var restored bytes.Buffer
	if err := Decrypt(&restored, bytes.NewReader(encrypted.Bytes()), "password"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, restored.Bytes()) {
		t.Fatal("archive changed")
	}
	broken := append([]byte(nil), encrypted.Bytes()...)
	broken[len(broken)/2] ^= 1
	restored.Reset()
	if err := Decrypt(&restored, bytes.NewReader(broken), "password"); err == nil {
		t.Fatal("altered chunk accepted")
	}
}
