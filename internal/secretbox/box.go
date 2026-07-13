// Package secretbox encrypts credential material before persistence.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const KeyBytes = 32

// Box uses AES-256-GCM with a fresh random nonce for every value.
type Box struct {
	aead    cipher.AEAD
	version string
}

func New(key []byte, version string) (*Box, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("credential encryption key must be %d bytes", KeyBytes)
	}
	if strings.TrimSpace(version) == "" {
		return nil, errors.New("credential encryption key version is required")
	}
	block, err := aes.NewCipher(append([]byte(nil), key...))
	if err != nil {
		return nil, fmt.Errorf("initialize AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize GCM: %w", err)
	}
	return &Box{aead: aead, version: strings.TrimSpace(version)}, nil
}

func NewBase64(encoded, version string) (*Box, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode credential encryption key: %w", err)
	}
	return New(raw, version)
}

func (b *Box) KeyVersion() string {
	if b == nil {
		return ""
	}
	return b.version
}

func (b *Box) Encrypt(plaintext []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("credential encryption is not initialized")
	}
	if len(plaintext) == 0 {
		return nil, errors.New("credential plaintext is required")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := b.aead.Seal(nil, nonce, plaintext, []byte(b.version))
	result := make([]byte, 1+len(nonce)+len(sealed))
	result[0] = byte(len(nonce))
	copy(result[1:], nonce)
	copy(result[1+len(nonce):], sealed)
	return result, nil
}

func (b *Box) Decrypt(ciphertext []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, errors.New("credential encryption is not initialized")
	}
	if len(ciphertext) < 2 {
		return nil, errors.New("credential ciphertext is invalid")
	}
	nonceSize := int(ciphertext[0])
	if nonceSize != b.aead.NonceSize() || len(ciphertext) <= 1+nonceSize {
		return nil, errors.New("credential ciphertext nonce is invalid")
	}
	plaintext, err := b.aead.Open(nil, ciphertext[1:1+nonceSize], ciphertext[1+nonceSize:], []byte(b.version))
	if err != nil {
		return nil, errors.New("credential ciphertext authentication failed")
	}
	return plaintext, nil
}
