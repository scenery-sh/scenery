package assistanttoken

import (
	"errors"
	"time"
)

// SealApproval creates an opaque approval token bound to one conversation,
// run, approval request, owner, and decision context. The public conversation
// handle is represented by a domain-separated digest so one opaque token is
// never recursively embedded inside another.
func (m Manager) SealApproval(claims ApprovalClaims) (string, error) {
	now := m.now()
	if err := validateApprovalClaims(&claims, now, m.ttl()); err != nil {
		return "", err
	}
	payload, err := marshalClaims(claims)
	if err != nil {
		return "", err
	}
	return sealEnvelope(ApprovalPrefix, approvalAAD, m.Keys, payload)
}

// UnsealApproval authenticates and validates an approval token against the
// complete external expectation. The sealed approval ID, run ID, and decision
// context are returned from the authenticated claims; callers do not need a
// process-local cache to reconstruct them. Zero or partial expectations fail
// closed with ErrNotFound.
func (m Manager) UnsealApproval(token string, expected ApprovalExpectation) (ApprovalClaims, error) {
	if expected.AssistantAddress == "" || expected.OwnerDigest == "" || expected.ConversationID == "" {
		return ApprovalClaims{}, ErrNotFound
	}
	plaintext, _, err := openEnvelope(ApprovalPrefix, approvalAAD, token, m.Keys)
	if err != nil {
		return ApprovalClaims{}, ErrNotFound
	}
	var claims ApprovalClaims
	if err := unmarshalClaims(plaintext, &claims); err != nil {
		return ApprovalClaims{}, ErrNotFound
	}
	now := m.now()
	if claims.Version != TokenVersion || expired(claims.ExpiresAt, now) || claims.IssuedAt.After(now.Add(5*time.Minute)) {
		return ApprovalClaims{}, ErrNotFound
	}
	if err := validateDecodedApproval(claims, m.ttl()); err != nil {
		return ApprovalClaims{}, ErrNotFound
	}
	if !equalString(expected.AssistantAddress, claims.AssistantAddress) || !equalString(expected.OwnerDigest, claims.OwnerDigest) || !equalString(ConversationDigest(expected.ConversationID), claims.ConversationDigest) {
		return ApprovalClaims{}, ErrNotFound
	}
	return claims, nil
}

// SealApprovalToken is an explicit alias for SealApproval.
func (m Manager) SealApprovalToken(claims ApprovalClaims) (string, error) {
	return m.SealApproval(claims)
}

func validateDecodedApproval(claims ApprovalClaims, ttl time.Duration) error {
	if claims.Version != TokenVersion || claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) {
		return errors.New("invalid approval claims")
	}
	if claims.ExpiresAt.After(claims.IssuedAt.Add(ttl)) {
		return errors.New("approval expiry exceeds manager TTL")
	}
	for _, field := range []struct {
		name, value string
	}{
		{"assistant_address", claims.AssistantAddress},
		{"conversation_digest", claims.ConversationDigest},
		{"run_id", claims.RunID},
		{"approval_id", claims.ApprovalID},
		{"owner_digest", claims.OwnerDigest},
		{"decision_context", claims.DecisionContext},
	} {
		if err := validateTokenString(field.name, field.value, true); err != nil {
			return err
		}
	}
	if len(claims.Nonce) != nonceBytes*2 || claims.Nonce != lowerHex(claims.Nonce) {
		return errors.New("invalid approval nonce")
	}
	if _, err := decodeLowerHex(claims.Nonce); err != nil {
		return err
	}
	return nil
}
