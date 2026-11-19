// Package secrets encrypts values pierd keeps, like environment variables,
// with a key stored beside the database. A copy of pier.db alone doesn't
// reveal them.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// Box seals and opens values with AES-256-GCM.
type Box struct{ aead cipher.AEAD }

// Load reads the key at path, creating it (readable by pierd only) the
// first time.
func Load(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s isn't a 32-byte key", path)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts a value. additional binds it to where it's stored (the
// project, scope and name), so a sealed value can't be moved to another.
func (b *Box) Seal(value, additional string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(value), []byte(additional))
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value sealed with the same additional data.
func (b *Box) Open(sealed, additional string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("not a sealed value")
	}
	n := b.aead.NonceSize()
	out, err := b.aead.Open(nil, raw[:n], raw[n:], []byte(additional))
	if err != nil {
		return "", errors.New("the value can't be decrypted with this server's key")
	}
	return string(out), nil
}
