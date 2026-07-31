package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

type currentUserTestDriver struct {
	rows [][]driver.Value
	err  error
}

type currentUserTestConn struct {
	rows [][]driver.Value
	err  error
}

type currentUserTestRows struct {
	rows  [][]driver.Value
	index int
}

var currentUserDriverSequence atomic.Uint64

func (d *currentUserTestDriver) Open(string) (driver.Conn, error) {
	return &currentUserTestConn{rows: d.rows, err: d.err}, nil
}

func (c *currentUserTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not used by current-user tests")
}

func (c *currentUserTestConn) Close() error { return nil }

func (c *currentUserTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not used by current-user tests")
}

func (c *currentUserTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "FROM scenery.scenery_auth_users") || len(args) != 1 {
		return nil, fmt.Errorf("unexpected current-user query %q with %#v", query, args)
	}
	if c.err != nil {
		return nil, c.err
	}
	return &currentUserTestRows{rows: c.rows}, nil
}

func (r *currentUserTestRows) Columns() []string {
	return []string{"id", "display_name", "avatar_url", "primary_email", "normalized_primary_email", "email_verified_at", "disabled_at", "can_impersonate_users", "created_at", "updated_at"}
}

func (r *currentUserTestRows) Close() error { return nil }

func (r *currentUserTestRows) Next(destination []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(destination, r.rows[r.index])
	r.index++
	return nil
}

func currentUserTestService(t *testing.T, rows [][]driver.Value, queryErr error) *Service {
	t.Helper()
	name := fmt.Sprintf("scenery-current-user-test-%d", currentUserDriverSequence.Add(1))
	sql.Register(name, &currentUserTestDriver{rows: rows, err: queryErr})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return &Service{db: database, query: authdb.New(database), now: time.Now}
}

func installCurrentUserTestService(t *testing.T, svc *Service) {
	t.Helper()
	resetStandardAuthStateForTest(t)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = StandardConfig{Enabled: true}
	standardAuthState.svc = svc
	standardAuthState.once.Do(func() {})
	standardAuthState.mu.Unlock()
}

func currentUserRow(verified bool, disabled bool) []driver.Value {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	var verifiedAt driver.Value
	if verified {
		verifiedAt = now
	}
	var disabledAt driver.Value
	if disabled {
		disabledAt = now
	}
	return []driver.Value{
		"11111111-1111-1111-1111-111111111111",
		"Fleet Owner",
		"https://example.test/avatar.png",
		"Owner@Example.test",
		"owner@example.test",
		verifiedAt,
		disabledAt,
		true,
		now,
		now,
	}
}

func TestCurrentUserReturnsLiveVerifiedAndUnverifiedProfiles(t *testing.T) {
	for _, test := range []struct {
		name     string
		verified bool
	}{
		{name: "verified", verified: true},
		{name: "unverified", verified: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := currentUserTestService(t, [][]driver.Value{currentUserRow(test.verified, false)}, nil)
			installCurrentUserTestService(t, svc)
			data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111", TenantID: "22222222-2222-2222-2222-222222222222"}
			profile, err := CurrentUser(WithContext(t.Context(), UID(data.UserID), data))
			if err != nil {
				t.Fatal(err)
			}
			if profile.ID != string(data.UserID) || profile.Email != "Owner@Example.test" || profile.DisplayName != "Fleet Owner" || profile.AvatarURL != "https://example.test/avatar.png" || profile.EmailVerified != test.verified || !profile.CanImpersonateUsers {
				t.Fatalf("CurrentUser profile = %#v", profile)
			}
		})
	}
}

func TestCurrentUserFailsClosed(t *testing.T) {
	if _, err := CurrentUser(t.Context()); errs.Code(err) != errs.Unauthenticated {
		t.Fatalf("missing auth error = %v (code %q), want unauthenticated", err, errs.Code(err))
	}

	t.Run("invalid user id", func(t *testing.T) {
		svc := currentUserTestService(t, nil, nil)
		installCurrentUserTestService(t, svc)
		data := &AuthData{UserID: "not-a-uuid"}
		if _, err := CurrentUser(WithContext(t.Context(), UID(data.UserID), data)); errs.Code(err) != errs.Unauthenticated {
			t.Fatalf("invalid user error = %v (code %q), want unauthenticated", err, errs.Code(err))
		}
	})

	t.Run("standard auth not configured", func(t *testing.T) {
		resetStandardAuthStateForTest(t)
		data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111"}
		if _, err := CurrentUser(WithContext(t.Context(), UID(data.UserID), data)); errs.Code(err) != errs.FailedPrecondition {
			t.Fatalf("configuration error = %v (code %q), want failed_precondition", err, errs.Code(err))
		}
	})

	t.Run("missing user", func(t *testing.T) {
		svc := currentUserTestService(t, nil, nil)
		installCurrentUserTestService(t, svc)
		data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111"}
		if _, err := CurrentUser(WithContext(t.Context(), UID(data.UserID), data)); errs.Code(err) != errs.Unauthenticated {
			t.Fatalf("missing user error = %v (code %q), want unauthenticated", err, errs.Code(err))
		}
	})

	t.Run("disabled user", func(t *testing.T) {
		svc := currentUserTestService(t, [][]driver.Value{currentUserRow(true, true)}, nil)
		installCurrentUserTestService(t, svc)
		data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111"}
		if _, err := CurrentUser(WithContext(t.Context(), UID(data.UserID), data)); errs.Code(err) != errs.PermissionDenied {
			t.Fatalf("disabled user error = %v (code %q), want permission_denied", err, errs.Code(err))
		}
	})
}

func TestCurrentUserPropagatesDatabaseError(t *testing.T) {
	want := errors.New("database unavailable")
	svc := currentUserTestService(t, nil, want)
	installCurrentUserTestService(t, svc)
	data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111"}
	_, err := CurrentUser(WithContext(t.Context(), UID(data.UserID), data))
	if !errors.Is(err, want) {
		t.Fatalf("CurrentUser error = %v, want original database error", err)
	}
}

var _ driver.QueryerContext = (*currentUserTestConn)(nil)
