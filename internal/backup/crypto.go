// Package backup encrypts portable Relay archives with a password.
package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"

	"golang.org/x/crypto/scrypt"
)

const MaxSize = 512 << 20
const blockSize = 64 << 10
const magic = "RELAYBK1"

func makeCipher(header []byte, password string) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(password), header[8:40], 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(header []byte, index uint32) []byte {
	n := make([]byte, 12)
	copy(n, header[40:48])
	binary.BigEndian.PutUint32(n[8:], index)
	return n
}

func Encrypt(dst io.Writer, src io.Reader, password string) error {
	header := make([]byte, 48)
	copy(header, magic)
	if _, err := rand.Read(header[8:]); err != nil {
		return err
	}
	aead, err := makeCipher(header, password)
	if err != nil {
		return err
	}
	if _, err = dst.Write(header); err != nil {
		return err
	}
	buf := make([]byte, blockSize)
	var total int64
	for index := uint32(0); ; index++ {
		n, err := io.ReadFull(src, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		total += int64(n)
		if total > MaxSize {
			return errors.New("archive trop volumineuse (512 Mio maximum)")
		}
		sealed := aead.Seal(nil, nonce(header, index), buf[:n], header)
		if err := binary.Write(dst, binary.BigEndian, uint32(len(sealed))); err != nil {
			return err
		}
		if _, err := dst.Write(sealed); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

func Decrypt(dst io.Writer, src io.Reader, password string) error {
	header := make([]byte, 48)
	if _, err := io.ReadFull(src, header); err != nil {
		return errors.New("archive incomplète")
	}
	if !bytes.Equal(header[:8], []byte(magic)) {
		return errors.New("format de sauvegarde inconnu")
	}
	aead, err := makeCipher(header, password)
	if err != nil {
		return err
	}
	var total int64
	for index := uint32(0); ; index++ {
		var size uint32
		if err := binary.Read(src, binary.BigEndian, &size); err != nil {
			return errors.New("archive incomplète")
		}
		if size < uint32(aead.Overhead()) || size > blockSize+uint32(aead.Overhead()) {
			return errors.New("archive invalide")
		}
		sealed := make([]byte, size)
		if _, err := io.ReadFull(src, sealed); err != nil {
			return errors.New("archive incomplète")
		}
		data, err := aead.Open(nil, nonce(header, index), sealed, header)
		if err != nil {
			return errors.New("mot de passe incorrect ou archive altérée")
		}
		if len(data) == 0 {
			var extra [1]byte
			n, err := src.Read(extra[:])
			if n != 0 || err != io.EOF {
				return errors.New("données ajoutées à l’archive")
			}
			return nil
		}
		total += int64(len(data))
		if total > MaxSize {
			return errors.New("archive trop volumineuse")
		}
		if _, err := dst.Write(data); err != nil {
			return err
		}
	}
}
