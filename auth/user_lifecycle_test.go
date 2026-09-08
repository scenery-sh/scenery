package auth

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

func TestUserLifecycleValidatesInputBeforeConfiguration(t *testing.T) {
	resetStandardAuthStateForTest(t)
	validID := AuthUserID("11111111-1111-1111-1111-111111111111")
	for name, run := range map[string]func() error{
		"invalid id":       func() error { return DisableUser(t.Context(), AuthUserID("invalid"), "offboarding") },
		"blank reason":     func() error { return DisableUser(t.Context(), validID, "  ") },
		"multiline reason": func() error { return RevokeUserSessions(t.Context(), validID, "offboard\nsecret") },
		"long reason": func() error {
			return DisableUser(t.Context(), validID, string(make([]byte, maxLifecycleReasonLength+1)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			if code := errs.Code(run()); code != errs.InvalidArgument {
				t.Fatalf("error code = %q, want %q", code, errs.InvalidArgument)
			}
		})
	}
	if code := errs.Code(EnableUser(t.Context(), validID)); code != errs.FailedPrecondition {
		t.Fatalf("unconfigured enable code = %q, want %q", code, errs.FailedPrecondition)
	}
}

func TestAuthHandlerPreservesSessionlessDevTokens(t *testing.T) {
	resetStandardAuthStateForTest(t)
	secrets.JWTSecret = "sessionless-dev-test"
	t.Cleanup(func() { secrets.JWTSecret = "" })
	token, err := GenerateToken("dev-user", "00000000-0000-0000-0000-000000000001", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	uid, data, err := AuthHandler(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if uid != "dev-user" || data.SessionID != "" {
		t.Fatalf("sessionless auth = uid %q data %#v", uid, data)
	}
}

func TestUserLifecyclePostgres(t *testing.T) {
	failure := errors.New("revoke failed")
	user := authdb.SceneryAuthUser{ID: mustParseAuthUUID(t, authSQLUserID)}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "atomic disable", true: "rollback disable"}[fail], func(t *testing.T) {
			revoke := authSQLStep{name: "RevokeUserRefreshSessions", check: func(_ string, args []driver.NamedValue) {
				if args[0].Value != "offboarding" {
					t.Fatalf("reason=%v", args)
				}
			}}
			if fail {
				revoke.err = failure
			}
			svc, script := authSQLService(t, authSQLStep{name: "DisableUserByID", rows: [][]driver.Value{authSQLUserRow(user)}}, revoke)
			installCurrentUserTestService(t, svc)
			err := DisableUser(t.Context(), AuthUserID(authSQLUserID), " offboarding ")
			end := "commit"
			if fail {
				end = "rollback"
				if !errors.Is(err, failure) {
					t.Fatalf("failure=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(script.events, []string{"begin", "DisableUserByID", "RevokeUserRefreshSessions", end}) {
				t.Fatalf("events=%v", script.events)
			}
		})
	}
	t.Run("enable does not recreate sessions", func(t *testing.T) {
		svc, _ := authSQLService(t, authSQLStep{name: "EnableUserByID", rows: [][]driver.Value{authSQLUserRow(user)}})
		installCurrentUserTestService(t, svc)
		if err := EnableUser(t.Context(), AuthUserID(authSQLUserID)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("revoke preserves enabled state", func(t *testing.T) {
		svc, _ := authSQLService(t, authSQLStep{name: "GetUserByID", rows: [][]driver.Value{authSQLUserRow(user)}}, authSQLStep{name: "RevokeUserRefreshSessions"})
		installCurrentUserTestService(t, svc)
		if err := RevokeUserSessions(t.Context(), AuthUserID(authSQLUserID), "offboarding"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing user", func(t *testing.T) {
		svc, _ := authSQLService(t, authSQLStep{name: "EnableUserByID", err: sql.ErrNoRows})
		installCurrentUserTestService(t, svc)
		if err := EnableUser(t.Context(), AuthUserID(authSQLUserID)); errs.Code(err) != errs.NotFound {
			t.Fatalf("missing user=%v", err)
		}
	})
}
