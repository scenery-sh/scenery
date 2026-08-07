package assistanttoken

import (
	"encoding/hex"
	"errors"
	"time"
)

// SealConversation creates an opaque conv1_ handle carrying the private helper
// session and continuation state. Only the active key is used for sealing.
func (m Manager) SealConversation(claims ConversationClaims) (string, error) {
	now := m.now()
	if err := validateConversationClaims(&claims, now, m.ttl()); err != nil {
		return "", err
	}
	payload, err := marshalClaims(claims)
	if err != nil {
		return "", err
	}
	return sealEnvelope(ConversationPrefix, conversationAAD, m.Keys, payload)
}

// UnsealConversation authenticates and validates a conversation handle against
// the complete assistant route and owner expectation. Zero or partial
// expectations fail closed with ErrNotFound.
func (m Manager) UnsealConversation(token string, expected ConversationExpectation) (ConversationClaims, error) {
	if expected.AssistantAddress == "" || expected.OwnerDigest == "" {
		return ConversationClaims{}, ErrNotFound
	}
	plaintext, _, err := openEnvelope(ConversationPrefix, conversationAAD, token, m.Keys)
	if err != nil {
		return ConversationClaims{}, ErrNotFound
	}
	var claims ConversationClaims
	if err := unmarshalClaims(plaintext, &claims); err != nil {
		return ConversationClaims{}, ErrNotFound
	}
	now := m.now()
	if claims.Version != TokenVersion || expired(claims.ExpiresAt, now) || claims.IssuedAt.After(now.Add(5*time.Minute)) {
		return ConversationClaims{}, ErrNotFound
	}
	if err := validateDecodedConversation(claims, m.ttl()); err != nil {
		return ConversationClaims{}, ErrNotFound
	}
	if !equalString(expected.AssistantAddress, claims.AssistantAddress) || !equalString(expected.OwnerDigest, claims.OwnerDigest) {
		return ConversationClaims{}, ErrNotFound
	}
	return claims, nil
}

// SealConversationHandle is an explicit alias for callers that prefer the
// public-handle terminology.
func (m Manager) SealConversationHandle(claims ConversationClaims) (string, error) {
	return m.SealConversation(claims)
}

func validateDecodedConversation(claims ConversationClaims, ttl time.Duration) error {
	if claims.Version != TokenVersion || claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) {
		return errors.New("invalid conversation claims")
	}
	if claims.ExpiresAt.After(claims.IssuedAt.Add(ttl)) {
		return errors.New("conversation expiry exceeds manager TTL")
	}
	for _, field := range []struct {
		name, value string
	}{
		{"assistant_address", claims.AssistantAddress},
		{"owner_digest", claims.OwnerDigest},
		{"conversation_digest", claims.ConversationDigest},
		{"private_session_id", claims.PrivateSessionID},
		{"continuation_token", claims.ContinuationToken},
	} {
		if err := validateTokenString(field.name, field.value, true); err != nil {
			return err
		}
	}
	if len(claims.Nonce) != nonceBytes*2 || claims.Nonce != lowerHex(claims.Nonce) {
		return errors.New("invalid conversation nonce")
	}
	if _, err := decodeLowerHex(claims.Nonce); err != nil {
		return err
	}
	return nil
}

func lowerHex(value string) string {
	for _, char := range value {
		if char >= 'A' && char <= 'F' {
			return ""
		}
	}
	return value
}

func decodeLowerHex(value string) ([]byte, error) {
	if len(value)%2 != 0 {
		return nil, errors.New("invalid lowercase hex")
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return nil, errors.New("invalid lowercase hex")
		}
	}
	return hex.DecodeString(value)
}

// ConversationExpiry returns a convenient expiry for callers constructing
// claims without an explicit deadline.
func (m Manager) ConversationExpiry(issuedAt time.Time) time.Time {
	if issuedAt.IsZero() {
		issuedAt = m.now()
	}
	return issuedAt.UTC().Add(m.ttl())
}
