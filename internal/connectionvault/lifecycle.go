package connectionvault

import (
	"context"
	"errors"
	"strings"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/connectionapi"
)

// Connect installs a verified authorization-code grant. The HTTP owner verifies
// the one-time handshake before calling this method; no login identity is written.
func (v *Vault) Connect(ctx context.Context, user, provider string, requested []string, grant Grant) error {
	r, required, err := v.Registration(provider, requested)
	if err != nil {
		return err
	}
	if grant.Issuer != r.Issuer || grant.Subject == "" || grant.Resource != "" && grant.Resource != r.Resource {
		return failure(errs.ConnectionConfiguration, "connection account identity is invalid")
	}
	if err := validateGrant(grant); err != nil {
		return failure(errs.ConnectionConfiguration, "connection grant is invalid")
	}
	if !grant.ScopesPresent {
		// RFC 6749 omits scope only when it equals the authorization request.
		grant.Scopes, grant.ScopesPresent = required, true
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	lease, err := v.store.Lock(ctx, user, provider)
	if err != nil {
		return storageFailure(err)
	}
	defer func() { _ = lease.Close() }()
	row, err := lease.Load(ctx)
	if err != nil && !errors.Is(err, ErrNotConnected) {
		return storageFailure(err)
	}
	if err == nil && row.Status != Disconnected && (!bound(row, user, r) || row.Subject != grant.Subject) {
		return failure(errs.FailedPrecondition, "disconnect the existing account before connecting another account or resource")
	}
	if row.Status != Active {
		row.RefreshCiphertext = nil
	}
	row.UserID, row.ProviderID = user, provider
	row.Issuer, row.Subject, row.Resource, row.Email = grant.Issuer, grant.Subject, r.Resource, grant.Email
	row, err = v.applyGrant(row, grant)
	if err != nil {
		return err
	}
	row.Status, row.LastRefreshError, row.RevocationStatus = Active, "", ""
	row.ConnectedAt, row.UpdatedAt = v.now(), v.now()
	row.DisconnectedAt, row.LastRefreshAt = time.Time{}, time.Time{}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = v.now()
	}
	if err := lease.Save(ctx, row); err != nil {
		return storageFailure(err)
	}
	if err := lease.Commit(); err != nil {
		return storageFailure(err)
	}
	if !strings.EqualFold(row.TokenType, "Bearer") {
		return failure(errs.ConnectionConfiguration, "connection token type is unsupported")
	}
	if !ContainsScopes(row.Scopes, required) {
		return failure(errs.ConnectionScopeMissing, "connection grant is missing required scopes")
	}
	return nil
}

// Status returns credential-free metadata under owner/provider serialization.
func (v *Vault) Status(ctx context.Context, user, provider string) (connectionapi.Status, error) {
	r, _, err := v.Registration(provider, nil)
	if err != nil {
		return connectionapi.Status{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	lease, err := v.store.Lock(ctx, user, provider)
	if err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	defer func() { _ = lease.Close() }()
	row, err := lease.Load(ctx)
	if errors.Is(err, ErrNotConnected) {
		return connectionapi.Status{Provider: provider, Status: Disconnected}, nil
	}
	if err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	if !bound(row, user, r) {
		return connectionapi.Status{}, failure(errs.ConnectionConfiguration, "connection configuration changed; reconnect")
	}
	if err := lease.Commit(); err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	return metadata(row), nil
}

// Disconnect durably stops issuance before attempting revocation. It retains
// the lease during revocation so a reconnect cannot race revocation of its token.
// Failed/unsupported revocation is metadata; local disconnect still succeeds.
func (v *Vault) Disconnect(ctx context.Context, user, provider string) (connectionapi.Status, error) {
	r, _, err := v.Registration(provider, nil)
	if err != nil {
		return connectionapi.Status{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	lease, err := v.store.Lock(ctx, user, provider)
	if err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	defer func() { _ = lease.Close() }()
	row, err := lease.Load(ctx)
	if errors.Is(err, ErrNotConnected) {
		return connectionapi.Status{Provider: provider, Status: Disconnected}, nil
	}
	if err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	if row.UserID != user || row.ProviderID != provider {
		return connectionapi.Status{}, failure(errs.PermissionDenied, "connection owner does not match")
	}
	if row.Status == Disconnected {
		if err := lease.Commit(); err != nil {
			return connectionapi.Status{}, storageFailure(err)
		}
		return metadata(row), nil
	}
	token, revokeStatus := "", "unsupported"
	if !bound(row, user, r) {
		revokeStatus = "configuration_changed"
	} else {
		data := row.RefreshCiphertext
		if len(data) == 0 {
			data = row.AccessCiphertext
		}
		token, err = v.cipher.Open(data)
		if err != nil {
			revokeStatus = "credential_unavailable"
		}
	}
	row.Status, row.RevocationStatus = Disconnected, "pending"
	row.AccessCiphertext, row.RefreshCiphertext = nil, nil
	row.AccessExpiresAt, row.LastRefreshError = time.Time{}, ""
	row.DisconnectedAt, row.UpdatedAt = v.now(), v.now()
	if err := lease.Checkpoint(ctx, row); err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	if token != "" {
		err := r.Provider.Revoke(ctx, token)
		switch {
		case err == nil:
			revokeStatus = "succeeded"
		case errors.Is(err, ErrRevocationUnsupported):
			revokeStatus = "unsupported"
		default:
			revokeStatus = "failed"
		}
	}
	row.RevocationStatus = revokeStatus
	if err := lease.Save(ctx, row); err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	if err := lease.Commit(); err != nil {
		return connectionapi.Status{}, storageFailure(err)
	}
	return metadata(row), nil
}

func metadata(row Connection) connectionapi.Status {
	result := connectionapi.Status{Provider: row.ProviderID, Status: row.Status, Issuer: row.Issuer, Subject: row.Subject, Resource: row.Resource, Email: row.Email, Scopes: append([]string(nil), row.Scopes...), RevocationStatus: row.RevocationStatus}
	if row.Status == RefreshPending {
		result.Status = ReauthRequired
		result.ReauthReason = string(UnknownOutcome)
	}
	if row.Status == ReauthRequired {
		result.ReauthReason = row.LastRefreshError
	}
	if !row.ConnectedAt.IsZero() {
		stamp := row.ConnectedAt.UTC()
		result.ConnectedAt = &stamp
	}
	if !row.LastRefreshAt.IsZero() {
		stamp := row.LastRefreshAt.UTC()
		result.LastRefreshAt = &stamp
	}
	if row.Status == Disconnected {
		result.Issuer = ""
		result.Subject = ""
		result.Resource = ""
		result.Email = ""
		result.Scopes = nil
	}
	return result
}
