// Package crypto encrypts small secrets at rest, such as the equipment password (RN-31).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

type Sealer struct {
	aead cipher.AEAD
}

// NewSealer builds an AES-256-GCM sealer from a 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("crypto: key must have 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext; the random nonce is prepended to the ciphertext.
// associated binds the ciphertext to a context (for example, the order id).
func (s *Sealer) Seal(plaintext, associated []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plaintext, associated), nil
}

// Open decrypts data produced by Seal.
func (s *Sealer) Open(data, associated []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("crypto: ciphertext too short")
	}
	return s.aead.Open(nil, data[:n], data[n:], associated)
}
