package assistanttoken

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConversationSealOpenIsOpaqueAndBindsOwner(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	key := bytes.Repeat([]byte{0x11}, 32)
	ring := NewStaticKeyring("current", key, nil)
	manager := Manager{Keys: ring, Now: func() time.Time { return now }, TTL: time.Hour}
	claims := ConversationClaims{
		AssistantAddress:   "assistant/support",
		OwnerDigest:        OwnerDigest("principal-a"),
		ConversationDigest: ConversationDigest("conversation-a"),
		PrivateSessionID:   "private-session-9",
		ContinuationToken:  "private-continuation-9",
		IssuedAt:           now,
		ExpiresAt:          now.Add(time.Hour),
	}
	token, err := manager.SealConversation(claims)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, ConversationPrefix) {
		t.Fatalf("conversation token = %q", token)
	}
	encoded := strings.TrimPrefix(token, ConversationPrefix)
	if encoded == "" || encoded != strings.ToLower(encoded) {
		t.Fatalf("conversation ciphertext is not lowercase hex: %q", encoded)
	}
	for _, secret := range []string{"assistant/support", claims.OwnerDigest, claims.ConversationDigest, claims.PrivateSessionID, claims.ContinuationToken} {
		if strings.Contains(token, secret) {
			t.Fatalf("conversation token leaked plaintext %q: %q", secret, token)
		}
	}
	expected := ConversationExpectation{AssistantAddress: claims.AssistantAddress, OwnerDigest: claims.OwnerDigest}
	opened, err := manager.UnsealConversation(token, expected)
	if err != nil {
		t.Fatal(err)
	}
	if opened.AssistantAddress != claims.AssistantAddress || opened.OwnerDigest != claims.OwnerDigest || opened.ConversationDigest != claims.ConversationDigest || opened.PrivateSessionID != claims.PrivateSessionID || opened.ContinuationToken != claims.ContinuationToken || opened.Nonce == "" {
		t.Fatalf("opened claims = %#v", opened)
	}

	variants := []struct {
		candidate   string
		expectation ConversationExpectation
	}{
		{token[:len(token)-1] + string(flipHex(token[len(token)-1])), expected},
		{token, ConversationExpectation{AssistantAddress: claims.AssistantAddress, OwnerDigest: OwnerDigest("principal-b")}},
		{"conv1_not-hex", expected},
	}
	for index, variant := range variants {
		if _, err := manager.UnsealConversation(variant.candidate, variant.expectation); err != ErrNotFound || !errors.Is(err, ErrNotFound) { //nolint:errorlint // Opaque token failures must return the exact sentinel, not a wrapper.
			t.Fatalf("candidate %d error = %v, want exact ErrNotFound", index, err)
		}
	}

	expiredManager := Manager{Keys: ring, Now: func() time.Time { return now.Add(time.Hour) }}
	if _, err := expiredManager.UnsealConversation(token, expected); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired error = %v, want ErrNotFound", err)
	}
}

func TestClaimsRejectTrailingJSONValue(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ring := NewStaticKeyring("current", bytes.Repeat([]byte{0x77}, 32), nil)
	manager := Manager{Keys: ring, Now: func() time.Time { return now }}
	claims := ConversationClaims{AssistantAddress: "assistant/support", OwnerDigest: OwnerDigest("principal"), ConversationDigest: ConversationDigest("conversation"), PrivateSessionID: "session", ContinuationToken: "continuation", IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := validateConversationClaims(&claims, now, manager.ttl()); err != nil {
		t.Fatal(err)
	}
	payload, err := marshalClaims(claims)
	if err != nil {
		t.Fatal(err)
	}
	malformed, err := sealEnvelope(ConversationPrefix, conversationAAD, ring, append(payload, []byte(`{}`)...))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UnsealConversation(malformed, ConversationExpectation{AssistantAddress: "assistant/support", OwnerDigest: OwnerDigest("principal")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trailing JSON error = %v", err)
	}
}

func TestTokenSizeBounds(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ring := NewStaticKeyring("current", bytes.Repeat([]byte{0x79}, 32), nil)
	manager := Manager{Keys: ring, Now: func() time.Time { return now }}

	conversation := ConversationClaims{
		AssistantAddress:   "assistant/support",
		OwnerDigest:        OwnerDigest("principal"),
		ConversationDigest: ConversationDigest("conversation"),
		PrivateSessionID:   strings.Repeat("s", maxStringBytes),
		ContinuationToken:  "continuation",
		IssuedAt:           now,
		ExpiresAt:          now.Add(time.Hour),
	}
	token, err := manager.SealConversation(conversation)
	if err != nil {
		t.Fatalf("max-sized conversation helper value rejected: %v", err)
	}
	if encoded := len(strings.TrimPrefix(token, ConversationPrefix)); encoded > maxTokenHexBytes {
		t.Fatalf("conversation token hex length = %d, want <= %d", encoded, maxTokenHexBytes)
	}
	if len(token) <= maxStringBytes {
		t.Fatalf("conversation token length = %d, want a regression fixture beyond %d bytes", len(token), maxStringBytes)
	}
	nestedApproval, err := manager.SealApproval(ApprovalClaims{
		AssistantAddress:   conversation.AssistantAddress,
		ConversationDigest: ConversationDigest(token),
		RunID:              "run-long-conversation",
		ApprovalID:         "approval-long-conversation",
		OwnerDigest:        conversation.OwnerDigest,
		DecisionContext:    "capability:local",
		IssuedAt:           now,
		ExpiresAt:          now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("approval bound to a valid long conversation token was rejected: %v", err)
	}
	if _, err := manager.UnsealApproval(nestedApproval, ApprovalExpectation{
		AssistantAddress: conversation.AssistantAddress,
		OwnerDigest:      conversation.OwnerDigest,
		ConversationID:   token,
	}); err != nil {
		t.Fatalf("approval bound to a valid long conversation token did not reopen: %v", err)
	}
	conversation.PrivateSessionID += "s"
	if _, err := manager.SealConversation(conversation); err == nil {
		t.Fatal("conversation helper value beyond 1 KiB was accepted")
	}

	approval := ApprovalClaims{
		AssistantAddress:   "assistant/support",
		ConversationDigest: ConversationDigest("conv1_opaque"),
		RunID:              "run-1",
		ApprovalID:         "approval-1",
		OwnerDigest:        OwnerDigest("principal"),
		DecisionContext:    strings.Repeat("d", maxStringBytes),
		IssuedAt:           now,
		ExpiresAt:          now.Add(time.Hour),
	}
	approvalToken, err := manager.SealApproval(approval)
	if err != nil {
		t.Fatalf("max-sized approval decision context rejected: %v", err)
	}
	if encoded := len(strings.TrimPrefix(approvalToken, ApprovalPrefix)); encoded > maxTokenHexBytes {
		t.Fatalf("approval token hex length = %d, want <= %d", encoded, maxTokenHexBytes)
	}
	approval.DecisionContext += "d"
	if _, err := manager.SealApproval(approval); err == nil {
		t.Fatal("approval decision context beyond 1 KiB was accepted")
	}

	block, err := aes.NewCipher(ring.CurrentKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	maxPlaintext := maxEnvelopeSize - 2 - len(ring.CurrentID) - gcm.NonceSize() - gcm.Overhead()
	if maxPlaintext <= 0 {
		t.Fatalf("invalid envelope bounds: max plaintext = %d", maxPlaintext)
	}
	bounded, err := sealEnvelope(ConversationPrefix, conversationAAD, ring, bytes.Repeat([]byte{'x'}, maxPlaintext))
	if err != nil {
		t.Fatalf("envelope at size boundary rejected: %v", err)
	}
	if encoded := len(strings.TrimPrefix(bounded, ConversationPrefix)); encoded != maxTokenHexBytes {
		t.Fatalf("boundary envelope hex length = %d, want %d", encoded, maxTokenHexBytes)
	}
	if _, err := sealEnvelope(ConversationPrefix, conversationAAD, ring, bytes.Repeat([]byte{'x'}, maxPlaintext+1)); err == nil {
		t.Fatal("envelope beyond 8 KiB hex bound was accepted")
	}
}

func TestUnsealRequiresCompleteExpectationsAndTTLBound(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ring := NewStaticKeyring("current", bytes.Repeat([]byte{0x78}, 32), nil)
	short := Manager{Keys: ring, Now: func() time.Time { return now }, TTL: time.Hour}
	claims := ConversationClaims{AssistantAddress: "assistant/support", OwnerDigest: OwnerDigest("principal"), ConversationDigest: ConversationDigest("conversation"), PrivateSessionID: "session", ContinuationToken: "continuation", IssuedAt: now, ExpiresAt: now.Add(2 * time.Hour)}
	if _, err := short.SealConversation(claims); err == nil {
		t.Fatal("accepted conversation expiry beyond manager TTL")
	}
	long := Manager{Keys: ring, Now: func() time.Time { return now }, TTL: 2 * time.Hour}
	token, err := long.SealConversation(claims)
	if err != nil {
		t.Fatal(err)
	}
	expected := ConversationExpectation{AssistantAddress: claims.AssistantAddress, OwnerDigest: claims.OwnerDigest}
	if _, err := short.UnsealConversation(token, expected); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unseal accepted token beyond current manager TTL: %v", err)
	}
	for _, partial := range []ConversationExpectation{{}, {AssistantAddress: claims.AssistantAddress}, {OwnerDigest: claims.OwnerDigest}} {
		if _, err := long.UnsealConversation(token, partial); !errors.Is(err, ErrNotFound) {
			t.Fatalf("partial conversation expectation = %#v, err=%v", partial, err)
		}
	}

	approvalConversationID := "conv1_opaque"
	approvalClaims := ApprovalClaims{AssistantAddress: claims.AssistantAddress, ConversationDigest: ConversationDigest(approvalConversationID), RunID: "run-1", ApprovalID: "approval-1", OwnerDigest: claims.OwnerDigest, DecisionContext: "context", IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	approval, err := long.SealApproval(approvalClaims)
	if err != nil {
		t.Fatal(err)
	}
	for _, partial := range []ApprovalExpectation{{}, {AssistantAddress: approvalClaims.AssistantAddress}, {AssistantAddress: approvalClaims.AssistantAddress, OwnerDigest: approvalClaims.OwnerDigest}} {
		if _, err := long.UnsealApproval(approval, partial); !errors.Is(err, ErrNotFound) {
			t.Fatalf("partial approval expectation = %#v, err=%v", partial, err)
		}
	}
}

func TestApprovalSealOpenBindsDecisionContextAndRotatesKeys(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	oldKey := bytes.Repeat([]byte{0x22}, 32)
	newKey := bytes.Repeat([]byte{0x33}, 32)
	oldManager := Manager{Keys: NewStaticKeyring("old", oldKey, nil), Now: func() time.Time { return now }}
	conversationID := "conv1_opaque"
	claims := ApprovalClaims{
		AssistantAddress: "assistant/support", ConversationDigest: ConversationDigest(conversationID), RunID: "run-9", ApprovalID: "approval-7",
		OwnerDigest: OwnerDigest("principal-a"), DecisionContext: "tool=house__process_scene;effect=destructive",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	token, err := oldManager.SealApproval(claims)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, ApprovalPrefix) || strings.ToLower(strings.TrimPrefix(token, ApprovalPrefix)) != strings.TrimPrefix(token, ApprovalPrefix) {
		t.Fatalf("approval token format = %q", token)
	}
	for _, secret := range []string{claims.AssistantAddress, claims.ConversationDigest, claims.RunID, claims.ApprovalID, claims.OwnerDigest, claims.DecisionContext} {
		if strings.Contains(token, secret) {
			t.Fatalf("approval token leaked plaintext %q", secret)
		}
	}
	rotated := Manager{Keys: NewStaticKeyring("new", newKey, map[string][]byte{"old": oldKey}), Now: func() time.Time { return now }}
	expected := ApprovalExpectation{AssistantAddress: claims.AssistantAddress, OwnerDigest: claims.OwnerDigest, ConversationID: conversationID}
	opened, err := rotated.UnsealApproval(token, expected)
	if err != nil {
		t.Fatal(err)
	}
	if opened.ApprovalID != claims.ApprovalID || opened.DecisionContext != claims.DecisionContext || opened.Nonce == "" {
		t.Fatalf("opened approval claims = %#v", opened)
	}
	if _, err := rotated.UnsealApproval(token, ApprovalExpectation{AssistantAddress: claims.AssistantAddress, OwnerDigest: OwnerDigest("principal-b"), ConversationID: conversationID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong approval owner error = %v", err)
	}
	if _, err := (Manager{Keys: NewStaticKeyring("new", newKey, nil), Now: func() time.Time { return now }}).UnsealApproval(token, expected); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key error = %v", err)
	}
}

func TestInitiatorSignerCookieFlagsSigningAndRotation(t *testing.T) {
	now := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)
	key := bytes.Repeat([]byte{0x44}, 32)
	signer := InitiatorSigner{Key: key, Now: func() time.Time { return now }, TTL: time.Hour}
	cookie, err := signer.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name != CookieName || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || !strings.HasPrefix(cookie.Value, initiatorPrefix) {
		t.Fatalf("initiator cookie = %#v", cookie)
	}
	identity, err := signer.Verify(cookie)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ID == "" || identity.Version != TokenVersion || identity.ExpiresAt.Before(now) {
		t.Fatalf("identity = %#v", identity)
	}
	boundedSigner := InitiatorSigner{Key: key, Now: func() time.Time { return now }, TTL: 365 * 24 * time.Hour}
	boundedIdentity, _, err := boundedSigner.IssueIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if boundedIdentity.ExpiresAt.After(now.Add(MaxCookieTTL)) {
		t.Fatalf("cookie expiry exceeded cap: %s", boundedIdentity.ExpiresAt)
	}
	if OwnerDigest(identity.ID) == identity.ID {
		t.Fatal("owner digest did not hash identity")
	}

	tampered := *cookie
	tampered.Value = cookie.Value[:len(cookie.Value)-1] + string(flipHex(cookie.Value[len(cookie.Value)-1]))
	if _, err := signer.Verify(&tampered); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tampered cookie error = %v", err)
	}
	expiredSigner := signer
	expiredSigner.Now = func() time.Time { return now.Add(time.Hour) }
	if _, err := expiredSigner.Verify(cookie); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired cookie error = %v", err)
	}

	oldKey := bytes.Repeat([]byte{0x55}, 32)
	newKey := bytes.Repeat([]byte{0x66}, 32)
	oldSigner := InitiatorSigner{Keys: NewStaticKeyring("old", oldKey, nil), Now: func() time.Time { return now }, TTL: time.Hour}
	oldCookie, err := oldSigner.Issue()
	if err != nil {
		t.Fatal(err)
	}
	rotatedSigner := InitiatorSigner{Keys: NewStaticKeyring("new", newKey, map[string][]byte{"old": oldKey}), Now: func() time.Time { return now }, TTL: time.Hour}
	rotatedIdentity, replacement, err := rotatedSigner.VerifyOrRotate(oldCookie)
	if err != nil || replacement == nil {
		t.Fatalf("rotation identity=%#v replacement=%#v err=%v", rotatedIdentity, replacement, err)
	}
	if rotatedIdentity.ID != mustVerify(t, oldSigner, oldCookie).ID {
		t.Fatalf("rotation changed anonymous identity")
	}
	if _, err := rotatedSigner.Verify(replacement); err != nil {
		t.Fatalf("replacement verification: %v", err)
	}
}

func flipHex(char byte) byte {
	if char == '0' {
		return '1'
	}
	return '0'
}

func mustVerify(t *testing.T, signer InitiatorSigner, cookie *http.Cookie) AnonymousIdentity {
	t.Helper()
	identity, err := signer.Verify(cookie)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}
