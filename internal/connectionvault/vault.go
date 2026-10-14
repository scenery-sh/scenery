package connectionvault

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"scenery.sh/errs"
)

const operationTimeout = 30 * time.Second

var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type Vault struct {
	store     Store
	cipher    Cipher
	providers map[string]Registration
	now       func() time.Time
}

func New(store Store, cipher Cipher, registrations []Registration, now func() time.Time) (*Vault, error) {
	if store == nil || cipher == nil {
		return nil, failure(errs.ConnectionConfiguration, "connection vault dependencies are missing")
	}
	providers := make(map[string]Registration, len(registrations))
	for _, registration := range registrations {
		_, duplicate := providers[registration.ID]
		allowed, err := NormalizeScopes(registration.AllowedScopes)
		if !providerIDPattern.MatchString(registration.ID) || duplicate || err != nil || registration.Issuer == "" || registration.Provider == nil {
			return nil, failure(errs.ConnectionConfiguration, "connection provider registration is invalid")
		}
		registration.AllowedScopes = allowed
		providers[registration.ID] = registration
	}
	if now == nil {
		now = time.Now
	}
	return &Vault{store: store, cipher: cipher, providers: providers, now: now}, nil
}

func failure(code errs.ErrCode, message string) error { return errs.B().Code(code).Msg(message).Err() }

func storageFailure(err error) error {
	if errs.Code(err) == errs.ConnectionConfiguration {
		return err
	}
	if errors.Is(err, ErrOwnerUnavailable) {
		return failure(errs.PermissionDenied, "connection owner is unavailable")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return failure(errs.ConnectionUnavailable, "connection storage is unavailable")
}

func (v *Vault) Registration(id string, scopes []string) (Registration, []string, error) {
	r, ok := v.providers[id]
	if !ok {
		return Registration{}, nil, failure(errs.ConnectionNotConfigured, "connection provider is not configured")
	}
	if r.Provider == nil || r.Issuer == "" {
		return Registration{}, nil, failure(errs.ConnectionConfiguration, "connection provider configuration is invalid")
	}
	required, err := NormalizeScopes(scopes)
	if err != nil {
		return Registration{}, nil, failure(errs.InvalidArgument, "invalid required connection scopes")
	}
	if !ContainsScopes(r.AllowedScopes, required) {
		return Registration{}, nil, failure(errs.PermissionDenied, "requested connection scope is not allowed")
	}
	r.AllowedScopes = slices.Clone(r.AllowedScopes)
	return r, required, nil
}

func bound(row Connection, user string, r Registration) bool {
	return row.UserID == user && row.ProviderID == r.ID && row.Issuer == r.Issuer && row.Resource == r.Resource
}

// Token returns a bearer credential to trusted server code. Required scopes
// describe permissions; they do not downscope a broader provider token.
func (v *Vault) Token(ctx context.Context, user, provider string, scopes []string) (string, error) {
	r, required, err := v.Registration(provider, scopes)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	lease, err := v.store.Lock(ctx, user, provider)
	if err != nil {
		return "", storageFailure(err)
	}
	defer func() { _ = lease.Close() }()
	row, err := lease.Load(ctx)
	if errors.Is(err, ErrNotConnected) {
		return "", failure(errs.ConnectionNotConnected, "connection is not connected")
	}
	if err != nil {
		return "", storageFailure(err)
	}
	if !bound(row, user, r) {
		return "", failure(errs.ConnectionConfiguration, "connection configuration changed; reconnect")
	}
	if row.Status != Active {
		return "", failure(errs.ConnectionReauthRequired, "connection requires reauthorization")
	}
	if !strings.EqualFold(row.TokenType, "Bearer") {
		return "", failure(errs.ConnectionConfiguration, "connection token type is unsupported")
	}
	if !ContainsScopes(row.Scopes, required) {
		return "", failure(errs.ConnectionScopeMissing, "connection is missing required scopes")
	}
	if v.now().Add(time.Minute).Before(row.AccessExpiresAt) && len(row.AccessCiphertext) > 0 {
		token, err := v.cipher.Open(row.AccessCiphertext)
		if err != nil {
			return "", failure(errs.ConnectionConfiguration, "connection credential could not be decrypted")
		}
		if token != "" {
			if err := lease.Commit(); err != nil {
				return "", storageFailure(err)
			}
			return token, nil
		}
	}
	refresh, err := v.cipher.Open(row.RefreshCiphertext)
	if err != nil {
		return "", failure(errs.ConnectionConfiguration, "connection credential could not be decrypted")
	}
	if refresh == "" {
		return "", failure(errs.ConnectionReauthRequired, "connection has no refresh credential")
	}
	row.Status, row.LastRefreshError = RefreshPending, string(UnknownOutcome)
	row.UpdatedAt = v.now()
	if err := lease.Checkpoint(ctx, row); err != nil {
		return "", storageFailure(err)
	}
	grant, err := r.Provider.Refresh(ctx, refresh)
	if err != nil {
		return "", v.refreshFailure(ctx, lease, row, err)
	}
	if err := validateGrant(grant); err != nil {
		return "", v.refreshFailure(ctx, lease, row, err)
	}
	if grant.Issuer != "" && grant.Issuer != row.Issuer || grant.Subject != "" && grant.Subject != row.Subject || grant.Resource != "" && grant.Resource != row.Resource {
		return "", v.refreshFailure(ctx, lease, row, ProviderError{UnknownOutcome})
	}
	row, err = v.applyGrant(row, grant)
	if err != nil {
		return "", err
	}
	row.Status, row.LastRefreshError = Active, ""
	row.LastRefreshAt, row.UpdatedAt = v.now(), v.now()
	if err := lease.Save(ctx, row); err != nil {
		return "", storageFailure(err)
	}
	if err := lease.Commit(); err != nil {
		return "", storageFailure(err)
	}
	if !strings.EqualFold(row.TokenType, "Bearer") {
		return "", failure(errs.ConnectionConfiguration, "refreshed connection token type is unsupported")
	}
	// Rotation and effective grant must survive even when this request is denied.
	if !ContainsScopes(row.Scopes, required) {
		return "", failure(errs.ConnectionScopeMissing, "refreshed connection is missing required scopes")
	}
	return grant.AccessToken, nil
}

func validateGrant(grant Grant) error {
	if grant.AccessToken == "" || strings.ContainsAny(grant.AccessToken, "\r\n") {
		return ProviderError{UnknownOutcome}
	}
	if grant.TokenType == "" {
		return ProviderError{UnknownOutcome}
	}
	if grant.ScopesPresent {
		if _, err := NormalizeScopes(grant.Scopes); err != nil {
			return ProviderError{UnknownOutcome}
		}
	}
	return nil
}

func (v *Vault) applyGrant(row Connection, grant Grant) (Connection, error) {
	var err error
	row.AccessCiphertext, err = v.cipher.Seal(grant.AccessToken)
	if err != nil {
		return row, failure(errs.ConnectionConfiguration, "connection credential could not be encrypted")
	}
	if grant.RefreshToken != "" {
		row.RefreshCiphertext, err = v.cipher.Seal(grant.RefreshToken)
		if err != nil {
			return row, failure(errs.ConnectionConfiguration, "connection credential could not be encrypted")
		}
	}
	row.TokenType, row.AccessExpiresAt = grant.TokenType, grant.ExpiresAt
	if grant.ScopesPresent {
		row.Scopes, _ = NormalizeScopes(grant.Scopes)
	}
	return row, nil
}

func (v *Vault) refreshFailure(ctx context.Context, lease Lease, row Connection, err error) error {
	kind := UnknownOutcome
	var providerError ProviderError
	if errors.As(err, &providerError) {
		kind = providerError.Kind
	}
	code := errs.ConnectionUnavailable
	switch kind {
	case Revoked:
		row.Status, code = ReauthRequired, errs.ConnectionReauthRequired
	case Misconfigured:
		row.Status, code = Active, errs.ConnectionConfiguration
	case Temporary:
		// An explicit protocol refusal did not consume the credential.
		row.Status = Active
	default:
		kind = UnknownOutcome
		row.Status = ReauthRequired
	}
	row.LastRefreshError, row.UpdatedAt = string(kind), v.now()
	if err := lease.Save(ctx, row); err != nil {
		return storageFailure(err)
	}
	if err := lease.Commit(); err != nil {
		return storageFailure(err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return failure(code, "connection token refresh failed: "+string(kind))
}
