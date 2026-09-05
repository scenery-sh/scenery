package eve

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AssertionClaims are the provider-neutral claims signed for a Scenery MCP
// call. The assertion is short-lived and intentionally contains no bearer
// token, cookie, or external credential.
type AssertionClaims struct {
	Audience           string    `json:"audience"`
	AssistantAddress   string    `json:"assistant_address"`
	Principal          string    `json:"principal"`
	ConversationDigest string    `json:"conversation_digest"`
	CapabilityRevision string    `json:"capability_revision"`
	ExpiresAt          time.Time `json:"-"`
	Nonce              string    `json:"nonce"`
}

type wireAssertionClaims struct {
	Audience           string `json:"audience"`
	AssistantAddress   string `json:"assistant_address"`
	Principal          string `json:"principal"`
	ConversationDigest string `json:"conversation_digest"`
	CapabilityRevision string `json:"capability_revision"`
	ExpiresAt          int64  `json:"expires_at"`
	Nonce              string `json:"nonce"`
}

// NewAssertion creates a compact payload.signature assertion. An empty nonce
// is filled with cryptographically random bytes. TTL is bounded to prevent a
// generated helper from accidentally creating a long-lived bridge credential.
func NewAssertion(secret []byte, claims AssertionClaims, now time.Time, ttl time.Duration) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("eve MCP bridge secret is required")
	}
	if claims.Audience == "" || claims.AssistantAddress == "" || claims.Principal == "" || claims.ConversationDigest == "" || claims.CapabilityRevision == "" {
		return "", errors.New("eve assertion claims are incomplete")
	}
	if ttl <= 0 {
		ttl = defaultAssertionTTL
	}
	if ttl > maxAssertionTTL {
		return "", fmt.Errorf("eve assertion TTL exceeds %s", maxAssertionTTL)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if claims.Nonce == "" {
		randomNonce := make([]byte, 16)
		if _, err := rand.Read(randomNonce); err != nil {
			return "", fmt.Errorf("generate Eve assertion nonce: %w", err)
		}
		claims.Nonce = base64.RawURLEncoding.EncodeToString(randomNonce)
	}
	if claims.ExpiresAt.IsZero() {
		claims.ExpiresAt = now.Add(ttl)
	}
	if !claims.ExpiresAt.After(now) || claims.ExpiresAt.Sub(now) > maxAssertionTTL {
		return "", errors.New("eve assertion expiry is outside the short-lived window")
	}
	wire := wireAssertionClaims{
		Audience:           claims.Audience,
		AssistantAddress:   claims.AssistantAddress,
		Principal:          claims.Principal,
		ConversationDigest: claims.ConversationDigest,
		CapabilityRevision: claims.CapabilityRevision,
		ExpiresAt:          claims.ExpiresAt.Unix(),
		Nonce:              claims.Nonce,
	}
	encodedJSON, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("marshal Eve assertion claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(encodedJSON)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

// VerifyAssertion verifies a generated assertion and returns its claims. It
// is primarily useful to the local fake MCP gateway and tests; production Go
// dispatch should continue to bind authorization to its own request context.
func VerifyAssertion(secret []byte, assertion string, now time.Time) (AssertionClaims, error) {
	if len(secret) == 0 {
		return AssertionClaims{}, errors.New("eve MCP bridge secret is required")
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return AssertionClaims{}, errors.New("invalid Eve assertion")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(got, want) {
		return AssertionClaims{}, errors.New("invalid Eve assertion signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return AssertionClaims{}, errors.New("invalid Eve assertion payload")
	}
	var wire wireAssertionClaims
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return AssertionClaims{}, fmt.Errorf("decode Eve assertion payload: %w", err)
	}
	if wire.Audience == "" || wire.AssistantAddress == "" || wire.Principal == "" || wire.ConversationDigest == "" || wire.CapabilityRevision == "" || wire.Nonce == "" || wire.ExpiresAt <= 0 {
		return AssertionClaims{}, errors.New("eve assertion claims are incomplete")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if time.Unix(wire.ExpiresAt, 0).Before(now.UTC()) {
		return AssertionClaims{}, errors.New("eve assertion expired")
	}
	return AssertionClaims{
		Audience:           wire.Audience,
		AssistantAddress:   wire.AssistantAddress,
		Principal:          wire.Principal,
		ConversationDigest: wire.ConversationDigest,
		CapabilityRevision: wire.CapabilityRevision,
		ExpiresAt:          time.Unix(wire.ExpiresAt, 0).UTC(),
		Nonce:              wire.Nonce,
	}, nil
}
