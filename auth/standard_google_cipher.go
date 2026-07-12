package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"scenery.sh/internal/envpolicy"
)

const googleTokenCipherVersion byte = 1

func sealGoogleToken(token string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	gcm, err := googleTokenGCM()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 1, 1+len(nonce)+len(token)+gcm.Overhead())
	out[0] = googleTokenCipherVersion
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, []byte(token), nil)
	return out, nil
}

func openGoogleToken(ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	gcm, err := googleTokenGCM()
	if err != nil {
		return "", err
	}
	if ciphertext[0] != googleTokenCipherVersion {
		return "", fmt.Errorf("unsupported google token ciphertext version")
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < 1+nonceSize {
		return "", fmt.Errorf("invalid google token ciphertext")
	}
	plain, err := gcm.Open(nil, ciphertext[1:1+nonceSize], ciphertext[1+nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func googleTokenGCM() (cipher.AEAD, error) {
	key, err := googleTokenCipherKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func googleTokenCipherKey() ([]byte, error) {
	cfg := currentStandardConfig()
	envName := strings.TrimSpace(cfg.GoogleOAuth.TokenCipherKeyEnv)
	if envName == "" {
		envName = "AUTH_TOKEN_CIPHER_KEY"
	}
	if value := strings.TrimSpace(envpolicy.Get(envName)); value != "" {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("%s must be base64-encoded", envName)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must decode to 32 bytes", envName)
		}
		return key, nil
	}
	if isLocalRuntime() {
		seed := secrets.JWTSecret
		if strings.TrimSpace(seed) == "" {
			seed = "scenery-local-development-secret"
		}
		sum := sha256.Sum256([]byte("scenery-google-token:" + seed))
		return sum[:], nil
	}
	return nil, fmt.Errorf("%s is required for encrypted Google token storage", envName)
}
