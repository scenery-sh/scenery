package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	authdb "scenery.sh/auth/db/gen"
)

func newRandomToken(byteLen int) (string, error) {
	if byteLen <= 0 {
		return "", fmt.Errorf("token length must be positive")
	}
	raw := make([]byte, byteLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newRefreshToken(sessionID authdb.UUID) (string, error) {
	id := uuidString(sessionID)
	if id == "" {
		return "", fmt.Errorf("session id is required")
	}
	secret, err := newRandomToken(32)
	if err != nil {
		return "", err
	}
	return id + "." + secret, nil
}

func parseRefreshToken(token string) (authdb.UUID, error) {
	sessionID, _, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || strings.TrimSpace(sessionID) == "" {
		return authdb.UUID{}, fmt.Errorf("invalid refresh token")
	}
	return parseUUID(sessionID)
}
