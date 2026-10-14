// Package connectionvault owns user connections independently of protocol and
// provider adapters. Runtime composition supplies the store, cipher and adapters.
package connectionvault

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotConnected          = errors.New("connection does not exist")
	ErrOwnerUnavailable      = errors.New("connection owner is unavailable")
	ErrRevocationUnsupported = errors.New("provider does not support revocation")
)

const (
	Active         = "active"
	ReauthRequired = "reauth_required"
	Disconnected   = "disconnected"
	RefreshPending = "refresh_pending"
)

// Connection contains encrypted credentials and metadata for exactly one owner
// and provider configuration. It is never serialized to a browser.
type Connection struct {
	ID, UserID, ProviderID                             string
	Issuer, Subject, Resource, Email, TokenType        string
	Scopes                                             []string
	RefreshCiphertext, AccessCiphertext                []byte
	AccessExpiresAt, ConnectedAt, CreatedAt, UpdatedAt time.Time
	LastRefreshAt, DisconnectedAt                      time.Time
	Status, LastRefreshError, RevocationStatus         string
}

type Grant struct {
	AccessToken, RefreshToken, TokenType string
	ExpiresAt                            time.Time
	Scopes                               []string
	ScopesPresent                        bool
	Issuer, Subject, Email, Resource     string
}

type Authorization struct {
	State, Verifier, Nonce, CallbackURI string
	Scopes                              []string
	Consent                             bool
}

type CodeExchange struct {
	Code, Verifier, NonceHash, CallbackURI string
	Scopes                                 []string
}

// Provider is private to runtime composition; applications select a configured
// provider ID rather than installing mutable global adapters.
type Provider interface {
	Authorize(Authorization) (string, error)
	Exchange(context.Context, CodeExchange) (Grant, error)
	Refresh(context.Context, string) (Grant, error)
	Revoke(context.Context, string) error
}

type Registration struct {
	ID, Issuer, Resource string
	AllowedScopes        []string
	Provider             Provider
}

type Cipher interface {
	Seal(string) ([]byte, error)
	Open([]byte) (string, error)
}

// Lease serializes the owner/provider across processes and locks the current row.
// Checkpoint durably saves an intent and retains that serialization while opening
// a new transaction. A crash/failed final commit must leave the intent visible,
// so a potentially rotated refresh credential is never blindly reused.
type Lease interface {
	Load(context.Context) (Connection, error)
	Save(context.Context, Connection) error
	// ExpireStates invalidates pending browser handshakes in the current transaction.
	ExpireStates(context.Context) error
	Checkpoint(context.Context, Connection) error
	Commit() error
	Close() error
}

type Store interface {
	Lock(context.Context, string, string) (Lease, error)
}

// ProviderError carries only a sanitized classification, never a remote body,
// token, authorization code or provider-controlled error description.
type ProviderError struct{ Kind FailureKind }
type FailureKind string

const (
	Revoked        FailureKind = "credential_revoked"
	Misconfigured  FailureKind = "provider_configuration"
	Temporary      FailureKind = "provider_unavailable"
	UnknownOutcome FailureKind = "refresh_outcome_unknown"
)

func (e ProviderError) Error() string { return string(e.Kind) }
