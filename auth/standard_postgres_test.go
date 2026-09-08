package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	authdb "scenery.sh/auth/db/gen"
)

func TestStandardAuthBootstrapPostgresSchema(t *testing.T) {
	t.Parallel()
	failure := errors.New("schema execution failed")
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			svc, script := authSQLService(t)
			var statements []string
			script.exec = func(query string, _ []driver.NamedValue) error {
				statements = append(statements, query)
				if fail && len(statements) == 2 {
					return failure
				}
				return nil
			}
			err := bootstrapStandardAuthSchema(t.Context(), svc.db)
			if fail {
				if !errors.Is(err, failure) || !reflect.DeepEqual(script.events, []string{"begin", "rollback"}) {
					t.Fatalf("failure=%v events=%v", err, script.events)
				}
			} else {
				if err != nil || !reflect.DeepEqual(script.events, []string{"begin", "commit"}) {
					t.Fatalf("bootstrap=%v events=%v", err, script.events)
				}
				if !strings.Contains(strings.Join(statements, "\n"), "CREATE TABLE IF NOT EXISTS scenery.scenery_auth_users") {
					t.Fatal("embedded auth schema was not executed")
				}
			}
			if len(statements) < 2 || !strings.Contains(statements[0], "pg_advisory_xact_lock") {
				t.Fatalf("schema was not locked first: %v", statements)
			}
		})
	}
}

func TestDevBootstrapDefaultEmailCreatesUserTenantAndMembership(t *testing.T) {
	user := authdb.SceneryAuthUser{ID: mustParseAuthUUID(t, authSQLUserID), PrimaryEmail: "Owner@Example.test", NormalizedPrimaryEmail: "owner@example.test", EmailVerifiedAt: sql.NullTime{Time: authSQLNow, Valid: true}}
	svc, _ := authSQLService(t,
		authSQLStep{name: "GetUserByNormalizedEmail", err: sql.ErrNoRows},
		authSQLStep{name: "EnsureDevBootstrapUser", rows: [][]driver.Value{authSQLUserRow(user)}, check: func(_ string, args []driver.NamedValue) {
			if args[3].Value != "owner@example.test" {
				t.Fatalf("normalized email=%v", args)
			}
		}},
		authSQLTenantStep(),
		authSQLMembershipStep(t),
		authSQLStep{name: "GetUserByNormalizedEmail", rows: [][]driver.Value{authSQLUserRow(user)}},
		authSQLStep{name: "ListUserMemberships", rows: [][]driver.Value{append(authSQLMembershipRow(), "Workspace", nil)}},
	)
	for range 2 {
		got, tenant, err := resolveDevBootstrapEmailUser(t.Context(), svc.query, "Owner@Example.test", "owner@example.test", authSQLTenantID)
		if err != nil || uuidString(got.ID) != authSQLUserID || uuidString(tenant) != authSQLTenantID {
			t.Fatalf("user=%+v tenant=%s err=%v", got, uuidString(tenant), err)
		}
	}
}

func TestDevBootstrapAttachesExistingUserToConfiguredTenant(t *testing.T) {
	user := authdb.SceneryAuthUser{ID: mustParseAuthUUID(t, authSQLUserID)}
	unrelated := authSQLMembershipRow()
	unrelated[1] = "44444444-4444-4444-4444-444444444444"
	svc, _ := authSQLService(t,
		authSQLStep{name: "GetUserByNormalizedEmail", rows: [][]driver.Value{authSQLUserRow(user)}},
		authSQLStep{name: "ListUserMemberships", rows: [][]driver.Value{append(unrelated, "Other Workspace", nil)}},
		authSQLTenantStep(), authSQLMembershipStep(t),
	)
	got, tenant, err := resolveDevBootstrapEmailUser(context.Background(), svc.query, "owner@example.test", "owner@example.test", authSQLTenantID)
	if err != nil || uuidString(got.ID) != authSQLUserID || uuidString(tenant) != authSQLTenantID {
		t.Fatalf("user=%+v tenant=%s err=%v", got, uuidString(tenant), err)
	}
}

func resetStandardAuthStateForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		standardAuthState.mu.Lock()
		if standardAuthState.svc != nil && standardAuthState.svc.db != nil {
			_ = standardAuthState.svc.db.Close()
		}
		standardAuthState.cfg = StandardConfig{}
		standardAuthState.svc = nil
		standardAuthState.once = sync.Once{}
		standardAuthState.err = nil
		standardAuthState.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}
