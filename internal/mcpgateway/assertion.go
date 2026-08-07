package mcpgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"scenery.sh/internal/mcpcontract"
)

// AssertionClaims are the provider-neutral claims signed for a private helper
// request.  They intentionally contain no application token or external MCP
// credential.
type AssertionClaims struct {
	Audience           string            `json:"audience"`
	AssistantAddress   string            `json:"assistant_address"`
	Principal          string            `json:"principal"`
	ConversationDigest string            `json:"conversation_digest"`
	CapabilityRevision string            `json:"capability_revision"`
	RequestID          string            `json:"request_id,omitempty"`
	TraceContext       map[string]string `json:"trace_context,omitempty"`
	IdempotencyKey     string            `json:"idempotency_key,omitempty"`
	ExpiresAt          int64             `json:"expires_at"`
	NotBefore          int64             `json:"not_before,omitempty"`
	Nonce              string            `json:"nonce"`
}

// SignAssertion serializes and signs claims with HMAC-SHA256.  The resulting
// token is suitable for AssertionHeader.  Production callers should use a
// fresh nonce and short expiry for every request.
func SignAssertion(secret []byte, claims AssertionClaims) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("empty assertion secret")
	}
	if claims.Audience == "" || claims.AssistantAddress == "" || claims.Principal == "" || claims.ConversationDigest == "" || claims.CapabilityRevision == "" || claims.ExpiresAt <= 0 || claims.Nonce == "" {
		return "", errors.New("incomplete assertion claims")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	return encoded + "." + signature, nil
}

// HMACAssertionVerifier verifies the token produced by SignAssertion.
type HMACAssertionVerifier struct {
	Secret      []byte
	Audience    string
	Now         func() time.Time
	Header      string
	MaxSkew     time.Duration
	MaxLifetime time.Duration
}

func (v HMACAssertionVerifier) Verify(ctx context.Context, req *http.Request) (mcpcontract.ToolCallContext, error) {
	if req == nil || len(v.Secret) == 0 || v.Audience == "" {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	header := v.Header
	if header == "" {
		header = AssertionHeader
	}
	token := strings.TrimSpace(req.Header.Get(header))
	if token == "" {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	mac := hmac.New(sha256.New, v.Secret)
	_, _ = mac.Write([]byte(parts[0]))
	want, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(mac.Sum(nil), want) {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	var claims AssertionClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Audience == "" || claims.AssistantAddress == "" || claims.Principal == "" || claims.ConversationDigest == "" || claims.CapabilityRevision == "" || claims.Nonce == "" {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	clock := now()
	maxSkew := v.MaxSkew
	if maxSkew <= 0 {
		maxSkew = 30 * time.Second
	}
	if claims.ExpiresAt <= clock.Add(-maxSkew).Unix() || (claims.NotBefore > 0 && claims.NotBefore > clock.Add(maxSkew).Unix()) {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	maxLifetime := v.MaxLifetime
	if maxLifetime <= 0 {
		maxLifetime = 5 * time.Minute
	}
	if time.Unix(claims.ExpiresAt, 0).After(clock.Add(maxLifetime + maxSkew)) {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	if v.Audience != "" && claims.Audience != v.Audience {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	return mcpcontract.ToolCallContext{
		Principal:          claims.Principal,
		AssistantAddress:   claims.AssistantAddress,
		ConversationDigest: claims.ConversationDigest,
		CapabilityRevision: claims.CapabilityRevision,
		RequestID:          claims.RequestID,
		TraceContext:       claims.TraceContext,
		IdempotencyKey:     claims.IdempotencyKey,
	}, nil
}

// StaticAssertionVerifier is a deterministic verifier useful for in-process
// gateway tests and local fake helpers.  Unknown tokens fail closed.
type StaticAssertionVerifier map[string]mcpcontract.ToolCallContext

func (v StaticAssertionVerifier) Verify(_ context.Context, req *http.Request) (mcpcontract.ToolCallContext, error) {
	if req == nil {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	value, ok := v[strings.TrimSpace(req.Header.Get(AssertionHeader))]
	if !ok {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	if value.AssistantAddress == "" || value.Principal == "" || value.ConversationDigest == "" || value.CapabilityRevision == "" {
		return mcpcontract.ToolCallContext{}, ErrUnauthorized
	}
	return value, nil
}
