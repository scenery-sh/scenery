// Package assistanttoken owns opaque public assistant handles, approval
// tokens, and anonymous initiator cookies.
package assistanttoken

import (
	"errors"
	"net/http"
	"time"
)

const (
	ConversationPrefix = "conv1_"
	ApprovalPrefix     = "appr1_"
	CookieName         = "scenery_assistant_initiator"
	TokenVersion       = 1
	DefaultTokenTTL    = 24 * time.Hour
	MaxCookieTTL       = 30 * 24 * time.Hour
)

var (
	// ErrNotFound deliberately covers malformed, expired, owner-mismatched,
	// and unknown-key tokens. Callers must not reveal which case occurred.
	ErrNotFound = tokenError("assistant resource not found")
	// ErrKeyUnavailable is reserved for failures obtaining the active sealing
	// key. Verification failures always normalize to ErrNotFound.
	ErrKeyUnavailable = errors.New("assistant token key unavailable")
)

type tokenError string

func (err tokenError) Error() string { return string(err) }

type ConversationClaims struct {
	Version            int       `json:"version"`
	AssistantAddress   string    `json:"assistant_address"`
	Nonce              string    `json:"nonce"`
	OwnerDigest        string    `json:"owner_digest"`
	ConversationDigest string    `json:"conversation_digest"`
	PrivateSessionID   string    `json:"private_session_id"`
	ContinuationToken  string    `json:"continuation_token"`
	IssuedAt           time.Time `json:"issued_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

type ApprovalClaims struct {
	Version            int       `json:"version"`
	AssistantAddress   string    `json:"assistant_address"`
	ConversationDigest string    `json:"conversation_digest"`
	RunID              string    `json:"run_id"`
	ApprovalID         string    `json:"approval_id"`
	OwnerDigest        string    `json:"owner_digest"`
	DecisionContext    string    `json:"decision_context"`
	Nonce              string    `json:"nonce"`
	IssuedAt           time.Time `json:"issued_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// ConversationExpectation identifies the public route and owner allowed to
// consume a conversation handle.
type ConversationExpectation struct {
	AssistantAddress string
	OwnerDigest      string
}

// ApprovalExpectation identifies the exact approval interaction allowed to
// consume an approval token.
type ApprovalExpectation struct {
	AssistantAddress string
	OwnerDigest      string
	ConversationID   string
}

type Manager struct {
	Keys Keyring
	Now  func() time.Time
	TTL  time.Duration
}

type InitiatorSigner struct {
	// Key is the single-key development fallback. Production callers should
	// provide Keys so active and rotated key IDs can be resolved uniformly.
	Key    []byte
	KeyID  string
	Keys   Keyring
	Now    func() time.Time
	TTL    time.Duration
	Secure bool
	Domain string
	Path   string
}

// AnonymousIdentity is the provider-neutral initiator identity carried by the
// signed browser cookie.
type AnonymousIdentity struct {
	Version   int
	ID        string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Keyring supplies the active framework key and resolves an embedded key ID
// during rotation. Keys must be AES-compatible lengths (16, 24, or 32 bytes).
type Keyring interface {
	ActiveKey() (id string, key []byte, err error)
	Key(id string) ([]byte, error)
}

// StaticKeyring is a small in-memory keyring for local development and tests.
// Keys are ordered newest first; old entries remain available by ID for token
// verification after rotation.
type StaticKeyring struct {
	CurrentID  string
	CurrentKey []byte
	Previous   map[string][]byte
}

func (ring StaticKeyring) ActiveKey() (string, []byte, error) {
	if ring.CurrentID == "" || len(ring.CurrentKey) == 0 {
		return "", nil, ErrKeyUnavailable
	}
	return ring.CurrentID, append([]byte(nil), ring.CurrentKey...), nil
}

func (ring StaticKeyring) Key(id string) ([]byte, error) {
	if id == ring.CurrentID && len(ring.CurrentKey) > 0 {
		return append([]byte(nil), ring.CurrentKey...), nil
	}
	if key := ring.Previous[id]; len(key) > 0 {
		return append([]byte(nil), key...), nil
	}
	return nil, ErrNotFound
}

// NewStaticKeyring creates a keyring with one active key and optional old
// keys. Previous entries are keyed as supplied and are never used for sealing.
func NewStaticKeyring(currentID string, currentKey []byte, previous map[string][]byte) StaticKeyring {
	copyPrevious := make(map[string][]byte, len(previous))
	for id, key := range previous {
		copyPrevious[id] = append([]byte(nil), key...)
	}
	return StaticKeyring{CurrentID: currentID, CurrentKey: append([]byte(nil), currentKey...), Previous: copyPrevious}
}

// Cookie returns the signed identity as a browser-safe HttpOnly cookie.
func (identity AnonymousIdentity) Cookie(value string, signer InitiatorSigner) *http.Cookie {
	path := signer.Path
	if path == "" {
		path = "/"
	}
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     path,
		Domain:   signer.Domain,
		Expires:  identity.ExpiresAt.UTC(),
		MaxAge:   maxAge(identity.ExpiresAt, identity.IssuedAt),
		HttpOnly: true,
		Secure:   signer.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func maxAge(expiresAt, issuedAt time.Time) int {
	seconds := int(expiresAt.Sub(issuedAt).Seconds())
	if seconds < 1 {
		return 0
	}
	return seconds
}
