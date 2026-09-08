package auth

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

func TestPrepareImpersonationTargetAndStartUnverifiedSession(t *testing.T) {
	user := authdb.SceneryAuthUser{ID: mustParseAuthUUID(t, authSQLUserID), PrimaryEmail: "Target@Example.test", NormalizedPrimaryEmail: "target@example.test"}
	svc, _ := authSQLService(t,
		authSQLStep{name: "GetUserByNormalizedEmail", err: sql.ErrNoRows},
		authSQLStep{name: "CreateUser", rows: [][]driver.Value{authSQLUserRow(user)}, check: func(_ string, args []driver.NamedValue) {
			if args[1].Value != "Target" || args[4].Value != "target@example.test" {
				t.Fatalf("provider-free profile=%v", args)
			}
		}},
		authSQLStep{name: "GetUserByID", rows: [][]driver.Value{authSQLUserRow(user)}},
		authSQLStep{name: "GetActiveMembership", rows: [][]driver.Value{authSQLMembershipRow()}},
	)
	params := PrepareImpersonationTargetParams{Email: "Target@Example.test", DisplayName: "Target"}
	first, err := resolveImpersonationTarget(t.Context(), svc.query, params, "target@example.test")
	if err != nil || first.EmailVerifiedAt.Valid {
		t.Fatalf("unverified target=%+v err=%v", first, err)
	}
	params.UserID = AuthUserID(authSQLUserID)
	again, err := resolveImpersonationTarget(t.Context(), svc.query, params, "target@example.test")
	if err != nil || uuidString(again.ID) != uuidString(first.ID) {
		t.Fatalf("stable identity=%+v err=%v", again, err)
	}
	tenant, err := svc.ensureImpersonationTenant(t.Context(), svc.query, again, mustParseAuthUUID(t, authSQLTenantID))
	if err != nil || uuidString(tenant) != authSQLTenantID {
		t.Fatalf("unverified tenant=%s err=%v", uuidString(tenant), err)
	}
}

func TestPrepareImpersonationTargetRequiresPrivilege(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "no privilege", true: "disabled actor"}[disabled], func(t *testing.T) {
			actor := authdb.SceneryAuthUser{ID: mustParseAuthUUID(t, authSQLUserID), CanImpersonateUsers: disabled, DisabledAt: sql.NullTime{Time: authSQLNow, Valid: disabled}}
			svc, script := authSQLService(t, authSQLStep{name: "GetUserByID", rows: [][]driver.Value{authSQLUserRow(actor)}})
			installCurrentUserTestService(t, svc)
			ctx := WithContext(t.Context(), UID(authSQLUserID), &AuthData{UserID: AuthUserID(authSQLUserID), TenantID: TenantID(authSQLTenantID)})
			_, err := PrepareImpersonationTarget(ctx, PrepareImpersonationTargetParams{Email: "target@example.test"})
			if errs.Code(err) != errs.PermissionDenied || !reflect.DeepEqual(script.events, []string{"begin", "GetUserByID", "rollback"}) {
				t.Fatalf("privilege err=%v events=%v", err, script.events)
			}
		})
	}
}
