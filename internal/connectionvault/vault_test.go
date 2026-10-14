package connectionvault

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"scenery.sh/errs"
)

var testNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

type memoryStore struct {
	row                                        Connection
	present                                    bool
	lockErr, saveErr, checkpointErr, commitErr error
	locks, checkpoints, commits                int
	pendingStates, expiredStates               int
	expireErr                                  error
}
type memoryLease struct {
	store  *memoryStore
	staged *Connection
	expire bool
}

func cloneConnection(row Connection) Connection {
	row.Scopes = slices.Clone(row.Scopes)
	row.AccessCiphertext = slices.Clone(row.AccessCiphertext)
	row.RefreshCiphertext = slices.Clone(row.RefreshCiphertext)
	return row
}
func (s *memoryStore) Lock(ctx context.Context, _, _ string) (Lease, error) {
	s.locks++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.lockErr != nil {
		return nil, s.lockErr
	}
	return &memoryLease{store: s}, nil
}
func (l *memoryLease) Load(context.Context) (Connection, error) {
	if !l.store.present {
		return Connection{}, ErrNotConnected
	}
	return cloneConnection(l.store.row), nil
}
func (l *memoryLease) Save(ctx context.Context, row Connection) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.store.saveErr != nil {
		return l.store.saveErr
	}
	copy := cloneConnection(row)
	l.staged = &copy
	return nil
}
func (l *memoryLease) ExpireStates(context.Context) error {
	if l.store.expireErr != nil {
		return l.store.expireErr
	}
	l.expire = true
	return nil
}

func (l *memoryLease) expireStates() {
	if l.expire {
		l.store.expiredStates += l.store.pendingStates
		l.store.pendingStates = 0
		l.expire = false
	}
}
func (l *memoryLease) Checkpoint(ctx context.Context, row Connection) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.store.checkpointErr != nil {
		return l.store.checkpointErr
	}
	l.store.row, l.store.present = cloneConnection(row), true
	l.store.checkpoints++
	l.expireStates()
	return nil
}
func (l *memoryLease) Commit() error {
	if l.store.commitErr != nil {
		return l.store.commitErr
	}
	if l.staged != nil {
		l.store.row, l.store.present = cloneConnection(*l.staged), true
	}
	l.store.commits++
	l.expireStates()
	return nil
}
func (*memoryLease) Close() error { return nil }

type testProvider struct {
	grant              Grant
	err, revokeErr     error
	calls, revocations int
	onRefresh          func(context.Context, string) (Grant, error)
	onRevoke           func()
}

func (*testProvider) Authorize(Authorization) (string, error)                 { return "", nil }
func (p *testProvider) Exchange(context.Context, CodeExchange) (Grant, error) { return p.grant, p.err }
func (p *testProvider) Refresh(ctx context.Context, token string) (Grant, error) {
	p.calls++
	if p.onRefresh != nil {
		return p.onRefresh(ctx, token)
	}
	return p.grant, p.err
}
func (p *testProvider) Revoke(context.Context, string) error {
	p.revocations++
	if p.onRevoke != nil {
		p.onRevoke()
	}
	return p.revokeErr
}

func vaultFixture(t *testing.T, provider string) (*Vault, *memoryStore, *testProvider) {
	t.Helper()
	cipher := TokenCipher{Key: func() ([]byte, error) { return []byte("12345678901234567890123456789012"), nil }}
	seal := func(value string) []byte {
		data, err := cipher.Seal(value)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	s := &memoryStore{present: true, row: Connection{ID: "stable", UserID: "owner", ProviderID: provider, Issuer: "https://issuer.example", Subject: "subject", Resource: "api", TokenType: "Bearer", Scopes: []string{"Read", "write"}, Status: Active, RefreshCiphertext: seal("old-refresh"), AccessCiphertext: seal("old-access"), AccessExpiresAt: testNow.Add(-time.Minute), CreatedAt: testNow.Add(-time.Hour)}}
	p := &testProvider{grant: Grant{AccessToken: "new-access", RefreshToken: "rotated-refresh", TokenType: "Bearer", ExpiresAt: testNow.Add(time.Hour)}}
	v, err := New(s, cipher, []Registration{{ID: provider, Issuer: s.row.Issuer, Resource: "api", AllowedScopes: []string{"Read", "write"}, Provider: p}}, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	return v, s, p
}

func TestVaultPersistsRotationBeforeDenyingReducedScope(t *testing.T) {
	for _, provider := range []string{"google", "independent"} {
		t.Run(provider, func(t *testing.T) {
			v, s, p := vaultFixture(t, provider)
			p.grant.Scopes, p.grant.ScopesPresent = []string{"Read"}, true
			_, err := v.Token(t.Context(), "owner", provider, []string{"write"})
			if errs.Code(err) != errs.ConnectionScopeMissing {
				t.Fatalf("scope error: %v", err)
			}
			refresh, err := v.cipher.Open(s.row.RefreshCiphertext)
			if err != nil || refresh != "rotated-refresh" || s.row.Status != Active || !slices.Equal(s.row.Scopes, []string{"Read"}) || s.checkpoints != 1 {
				t.Fatalf("rotation lost: %#v, %v", s.row, err)
			}
			token, err := v.Token(t.Context(), "owner", provider, []string{"Read"})
			if err != nil || token != "new-access" || p.calls != 1 {
				t.Fatalf("cache: %q, %v, calls=%d", token, err, p.calls)
			}
		})
	}
}

func TestVaultRetainsOmittedRefreshAndScope(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	p.grant.RefreshToken = ""
	oldCipher := slices.Clone(s.row.RefreshCiphertext)
	if _, err := v.Token(t.Context(), "owner", "independent", nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(oldCipher, s.row.RefreshCiphertext) || !slices.Equal(s.row.Scopes, []string{"Read", "write"}) {
		t.Fatal("omitted values overwrote the retained grant")
	}
}

func TestVaultConfigurationAndOwnershipFailuresDoNotChangeConsent(t *testing.T) {
	for _, scenario := range []string{"key", "owner", "resource", "issuer", "token_type", "not_configured", "disallowed_scope"} {
		t.Run(scenario, func(t *testing.T) {
			v, s, p := vaultFixture(t, "independent")
			provider, scopes := "independent", []string{"Read"}
			want := errs.ConnectionConfiguration
			switch scenario {
			case "key":
				v.cipher = TokenCipher{Key: func() ([]byte, error) { return nil, errors.New("missing key") }}
			case "owner":
				s.row.UserID = "other"
			case "resource":
				s.row.Resource = "other-api"
			case "issuer":
				s.row.Issuer = "https://other.example"
			case "token_type":
				s.row.TokenType = "DPoP"
			case "not_configured":
				provider = "request-controlled-url"
				want = errs.ConnectionNotConfigured
			case "disallowed_scope":
				scopes = []string{"read"}
				want = errs.PermissionDenied
			}
			_, err := v.Token(t.Context(), "owner", provider, scopes)
			if errs.Code(err) != want || s.row.Status != Active || p.calls != 0 || s.checkpoints != 0 {
				t.Fatalf("classification/state: %v, %#v", err, s.row)
			}
		})
	}
}

func TestVaultRefreshFailureClassificationsAndPersistence(t *testing.T) {
	for _, scenario := range []struct {
		kind   FailureKind
		code   errs.ErrCode
		status string
	}{{Revoked, errs.ConnectionReauthRequired, ReauthRequired}, {Misconfigured, errs.ConnectionConfiguration, Active}, {Temporary, errs.ConnectionUnavailable, Active}, {UnknownOutcome, errs.ConnectionUnavailable, ReauthRequired}} {
		t.Run(string(scenario.kind), func(t *testing.T) {
			v, s, p := vaultFixture(t, "independent")
			p.err = ProviderError{scenario.kind}
			_, err := v.Token(t.Context(), "owner", "independent", nil)
			if errs.Code(err) != scenario.code || s.row.Status != scenario.status {
				t.Fatalf("failure: %v, status=%s", err, s.row.Status)
			}
			if scenario.status == ReauthRequired {
				_, err = v.Token(t.Context(), "owner", "independent", nil)
				if errs.Code(err) != errs.ConnectionReauthRequired || p.calls != 1 {
					t.Fatal("unusable credential was retried")
				}
			}
		})
	}
}

func TestVaultFailedFinalWriteLeavesDurableRefreshIntent(t *testing.T) {
	for _, scenario := range []string{"save", "commit", "status_update", "checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			v, s, p := vaultFixture(t, "independent")
			switch scenario {
			case "save":
				s.saveErr = errors.New("write failed")
			case "commit":
				s.commitErr = errors.New("commit failed")
			case "status_update":
				p.err = ProviderError{Revoked}
				s.saveErr = errors.New("status failed")
			case "checkpoint":
				s.checkpointErr = errors.New("intent failed")
			}
			_, err := v.Token(t.Context(), "owner", "independent", nil)
			if errs.Code(err) != errs.ConnectionUnavailable {
				t.Fatalf("unpersisted transition reported success: %v", err)
			}
			if scenario == "checkpoint" {
				if p.calls != 0 || s.row.Status != Active {
					t.Fatal("provider called without durable intent")
				}
				return
			}
			if s.row.Status != RefreshPending {
				t.Fatal("failed final write lost refresh intent")
			}
			s.saveErr, s.commitErr = nil, nil
			_, err = v.Token(t.Context(), "owner", "independent", nil)
			if errs.Code(err) != errs.ConnectionReauthRequired || p.calls != 1 {
				t.Fatal("unknown refresh outcome was retried")
			}
		})
	}
}

func TestVaultCancellationPreservesUnknownOutcome(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.onRefresh = func(ctx context.Context, _ string) (Grant, error) { cancel(); return Grant{}, ctx.Err() }
	_, err := v.Token(ctx, "owner", "independent", nil)
	if !errors.Is(err, context.Canceled) || s.row.Status != RefreshPending {
		t.Fatalf("cancellation: %v, status=%s", err, s.row.Status)
	}
}

func TestVaultCachesOAuthGrantWithoutRefreshCredential(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	s.row.RefreshCiphertext = nil
	s.row.AccessExpiresAt = testNow.Add(time.Hour)
	token, err := v.Token(t.Context(), "owner", "independent", nil)
	if err != nil || token != "old-access" || p.calls != 0 {
		t.Fatalf("optional refresh: %q, %v", token, err)
	}
	s.row.AccessExpiresAt = testNow
	_, err = v.Token(t.Context(), "owner", "independent", nil)
	if errs.Code(err) != errs.ConnectionReauthRequired {
		t.Fatalf("expired non-refreshable grant: %v", err)
	}
}

func TestVaultDisconnectCommitsBeforeFailedRevocation(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	p.revokeErr = errors.New("provider secret must never be exposed")
	p.onRevoke = func() {
		if s.row.Status != Disconnected || len(s.row.RefreshCiphertext) > 0 {
			t.Fatal("revocation preceded local disconnect")
		}
	}
	row, err := v.Disconnect(t.Context(), "owner", "independent")
	if err != nil || row.Status != Disconnected || row.RevocationStatus != "failed" {
		t.Fatalf("disconnect: %#v, %v", row, err)
	}
	_, err = v.Token(t.Context(), "owner", "independent", nil)
	if errs.Code(err) != errs.ConnectionReauthRequired || p.calls != 0 {
		t.Fatal("disconnected connection issued a token")
	}
}

func TestVaultConnectPreservesSlotAndRequiresExplicitAccountSwitch(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	p.grant.Subject, p.grant.Issuer = "other-subject", s.row.Issuer
	if err := v.Connect(t.Context(), "owner", "independent", nil, p.grant); errs.Code(err) != errs.FailedPrecondition {
		t.Fatalf("implicit account switch: %v", err)
	}
	if _, err := v.Disconnect(t.Context(), "owner", "independent"); err != nil {
		t.Fatal(err)
	}
	if err := v.Connect(t.Context(), "owner", "independent", []string{"Read"}, p.grant); err != nil {
		t.Fatal(err)
	}
	if s.row.ID != "stable" || s.row.Subject != "other-subject" || s.row.Status != Active || !s.row.DisconnectedAt.IsZero() {
		t.Fatalf("reconnect: %#v", s.row)
	}
}

func TestVaultRejectsUnsupportedRefreshedTokenAfterSavingRotation(t *testing.T) {
	v, s, p := vaultFixture(t, "independent")
	p.grant.TokenType = "DPoP"
	_, err := v.Token(t.Context(), "owner", "independent", nil)
	refresh, _ := v.cipher.Open(s.row.RefreshCiphertext)
	if errs.Code(err) != errs.ConnectionConfiguration || s.row.TokenType != "DPoP" || refresh != "rotated-refresh" {
		t.Fatalf("unsupported token: %v, type=%s", err, s.row.TokenType)
	}
}

func TestScopeTokensRemainOpaqueAndRegistrationIsImmutable(t *testing.T) {
	if _, err := NormalizeScopes([]string{"two scopes"}); err == nil {
		t.Fatal("combined scope expression accepted")
	}
	got, err := NormalizeScopes([]string{"Read", "read", "Read"})
	if err != nil || !slices.Equal(got, []string{"Read", "read"}) {
		t.Fatalf("scope canonicalization: %v, %v", got, err)
	}
	v, _, _ := vaultFixture(t, "independent")
	r, _, err := v.Registration("independent", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.AllowedScopes[0] = "changed"
	if _, _, err := v.Registration("independent", []string{"Read"}); err != nil {
		t.Fatal("caller mutated registration")
	}
}
