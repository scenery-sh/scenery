package connectionvault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// TokenCipher preserves the existing version-1 AES-GCM envelope. Runtime owns
// key selection, including the historical local derivation input.
type TokenCipher struct{ Key func() ([]byte, error) }

func (c TokenCipher) aead() (cipher.AEAD, error) {
	if c.Key == nil {
		return nil, fmt.Errorf("connection cipher key source is unavailable")
	}
	key, err := c.Key()
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("connection cipher key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c TokenCipher) Seal(token string) ([]byte, error) {
	if token == "" {
		return nil, nil
	}
	gcm, err := c.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := append([]byte{1}, nonce...)
	return gcm.Seal(out, nonce, []byte(token), nil), nil
}

func (c TokenCipher) Open(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	gcm, err := c.aead()
	if err != nil {
		return "", err
	}
	if data[0] != 1 || len(data) < 1+gcm.NonceSize()+gcm.Overhead() {
		return "", fmt.Errorf("invalid connection ciphertext envelope")
	}
	plain, err := gcm.Open(nil, data[1:1+gcm.NonceSize()], data[1+gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("connection ciphertext could not be decrypted")
	}
	return string(plain), nil
}
