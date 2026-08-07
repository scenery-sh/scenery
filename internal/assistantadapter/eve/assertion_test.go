package eve

import (
	"strings"
	"testing"
	"time"
)

func TestAssertionRoundTripAndShortExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	claims := AssertionClaims{
		Audience:           "scenery",
		AssistantAddress:   "assistant/support",
		Principal:          "alice",
		ConversationDigest: "conversation-digest",
		CapabilityRevision: "sha256:capability",
		Nonce:              "nonce",
	}
	token, err := NewAssertion([]byte("bridge-secret"), claims, now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := VerifyAssertion([]byte("bridge-secret"), token, now)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Principal != claims.Principal || parsed.ExpiresAt.Sub(now) != 30*time.Second {
		t.Fatalf("claims=%+v", parsed)
	}
	if _, err := VerifyAssertion([]byte("wrong"), token, now); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if _, err := VerifyAssertion([]byte("bridge-secret"), token, now.Add(31*time.Second)); err == nil {
		t.Fatal("expired assertion accepted")
	}
	if strings.Contains(token, "bridge-secret") || strings.Contains(token, "original") {
		t.Fatal("assertion contains a raw credential")
	}
}

func TestAssertionRejectsLongLifetimeAndIncompleteClaims(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	base := AssertionClaims{Audience: "scenery", AssistantAddress: "assistant/support", Principal: "alice", ConversationDigest: "digest", CapabilityRevision: "rev"}
	if _, err := NewAssertion([]byte("secret"), base, now, 3*time.Minute); err == nil {
		t.Fatal("long assertion TTL accepted")
	}
	base.Principal = ""
	if _, err := NewAssertion([]byte("secret"), base, now, time.Second); err == nil {
		t.Fatal("incomplete claims accepted")
	}
}
