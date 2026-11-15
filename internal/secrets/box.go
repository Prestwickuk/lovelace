package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Box encrypts and decrypts sensitive integration credentials using AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// NewBox constructs an AEAD secret box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

// SealMap encrypts a string key/value credential map into nonce || ciphertext.
func (b *Box) SealMap(creds map[string]string) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("secret box is not initialized")
	}
	plain, err := json.Marshal(creds)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plain, nil)
	return sealed, nil
}

// OpenMap decrypts nonce || ciphertext back into a string key/value credential map.
func (b *Box) OpenMap(ciphertext []byte) (map[string]string, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("secret box is not initialized")
	}
	ns := b.aead.NonceSize()
	if len(ciphertext) <= ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, data := ciphertext[:ns], ciphertext[ns:]
	plain, err := b.aead.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt credentials: %w", err)
	}
	var out map[string]string
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, fmt.Errorf("decode decrypted credentials: %w", err)
	}
	return out, nil
}

// MaskSecret returns a safe non-reversible hint exposing at most the last 4 characters.
func MaskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 4 {
		return "••••"
	}
	return "••••" + string(r[len(r)-4:])
}
