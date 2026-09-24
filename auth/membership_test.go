package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

const membershipTargetID = "44444444-4444-4444-4444-444444444444"

func membershipContext(t *testing.T, tenant TenantID) context.Context {
	t.Helper()
	data := &AuthData{UserID: AuthUserID(authSQLUserID), TenantID: tenant}
	return WithContext(t.Context(), UID(data.UserID), data)
}

func membershipUserStep(t *testing.T, userID string, disabled bool) authSQLStep {
	t.Helper()
	user := authdb.SceneryAuthUser{ID: mustParseAuthUUID(t, userID)}
	if disabled {
		user.DisabledAt = sql.NullTime{Time: authSQLNow, Valid: true}
	}
	return authSQLStep{name: "GetUserByID", rows: [][]driver.Value{authSQLUserRow(user)}, check: func(_ string, args []driver.NamedValue) {
		if args[0].Value != userID {
			t.Fatalf("user lookup args=%v, want %s", args, userID)
		}
	}}
}

func membershipRowStep(t *testing.T, userID, role string) authSQLStep {
	t.Helper()
	row := authSQLMembershipRow()
	row[2], row[3] = userID, role
	return authSQLStep{name: "GetActiveMembership", rows: [][]driver.Value{row}, check: func(_ string, args []driver.NamedValue) {
		if args[0].Value != userID || args[1].Value != authSQLTenantID {
			t.Fatalf("membership lookup args=%v, want %s in %s", args, userID, authSQLTenantID)
		}
	}}
}

func TestCurrentMembershipReadsTheAuthenticatedUsersRole(t *testing.T) {
	for _, role := range []string{RoleOwner, RoleMember} {
		svc, _ := authSQLService(t, membershipUserStep(t, authSQLUserID, false), membershipRowStep(t, authSQLUserID, role))
		installCurrentUserTestService(t, svc)
		got, err := CurrentMembership(membershipContext(t, authSQLTenantID))
		if err != nil {
			t.Fatal(err)
		}
		if got != (Membership{UserID: authSQLUserID, TenantID: authSQLTenantID, Role: role}) {
			t.Fatalf("membership = %+v, want role %s", got, role)
		}
	}
}

func TestCurrentMembershipFailsClosed(t *testing.T) {
	if _, err := CurrentMembership(t.Context()); errs.Code(err) != errs.Unauthenticated {
		t.Fatalf("missing auth = %v, want unauthenticated", err)
	}
	t.Run("no organization session", func(t *testing.T) {
		svc, _ := authSQLService(t)
		installCurrentUserTestService(t, svc)
		if _, err := CurrentMembership(membershipContext(t, "")); errs.Code(err) != errs.Unauthenticated {
			t.Fatalf("missing tenant = %v, want unauthenticated", err)
		}
	})
	t.Run("standard auth not configured", func(t *testing.T) {
		resetStandardAuthStateForTest(t)
		if _, err := CurrentMembership(membershipContext(t, authSQLTenantID)); errs.Code(err) != errs.FailedPrecondition {
			t.Fatalf("configuration = %v, want failed_precondition", err)
		}
	})
	t.Run("disabled user", func(t *testing.T) {
		svc, _ := authSQLService(t, membershipUserStep(t, authSQLUserID, true))
		installCurrentUserTestService(t, svc)
		if _, err := CurrentMembership(membershipContext(t, authSQLTenantID)); errs.Code(err) != errs.PermissionDenied {
			t.Fatalf("disabled user = %v, want permission_denied", err)
		}
	})
	t.Run("no active membership", func(t *testing.T) {
		svc, _ := authSQLService(t, membershipUserStep(t, authSQLUserID, false),
			authSQLStep{name: "GetActiveMembership", err: sql.ErrNoRows})
		installCurrentUserTestService(t, svc)
		if _, err := CurrentMembership(membershipContext(t, authSQLTenantID)); errs.Code(err) != errs.PermissionDenied {
			t.Fatalf("inactive membership = %v, want permission_denied", err)
		}
	})
}

func TestMembershipOfReadsAnActiveMemberOfTheCallersTenant(t *testing.T) {
	caller := func() []authSQLStep {
		return []authSQLStep{membershipUserStep(t, authSQLUserID, false), membershipRowStep(t, authSQLUserID, RoleOwner)}
	}
	t.Run("member", func(t *testing.T) {
		svc, _ := authSQLService(t, append(caller(), membershipUserStep(t, membershipTargetID, false),
			membershipRowStep(t, membershipTargetID, RoleMember))...)
		installCurrentUserTestService(t, svc)
		got, err := MembershipOf(membershipContext(t, authSQLTenantID), membershipTargetID)
		if err != nil || got != (Membership{UserID: membershipTargetID, TenantID: authSQLTenantID, Role: RoleMember}) {
			t.Fatalf("membership = %+v, %v", got, err)
		}
	})
	for name, steps := range map[string][]authSQLStep{
		"unknown user":  {{name: "GetUserByID", err: sql.ErrNoRows}},
		"disabled user": {membershipUserStep(t, membershipTargetID, true)},
		"not a member":  {membershipUserStep(t, membershipTargetID, false), {name: "GetActiveMembership", err: sql.ErrNoRows}},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := authSQLService(t, append(caller(), steps...)...)
			installCurrentUserTestService(t, svc)
			if _, err := MembershipOf(membershipContext(t, authSQLTenantID), membershipTargetID); errs.Code(err) != errs.NotFound {
				t.Fatalf("%s = %v, want not_found", name, err)
			}
		})
	}
	t.Run("invalid user id", func(t *testing.T) {
		svc, _ := authSQLService(t, caller()...)
		installCurrentUserTestService(t, svc)
		if _, err := MembershipOf(membershipContext(t, authSQLTenantID), "not-a-uuid"); errs.Code(err) != errs.InvalidArgument {
			t.Fatalf("invalid id = %v, want invalid_argument", err)
		}
	})
	t.Run("an inactive caller learns nothing", func(t *testing.T) {
		svc, _ := authSQLService(t, membershipUserStep(t, authSQLUserID, false),
			authSQLStep{name: "GetActiveMembership", err: sql.ErrNoRows})
		installCurrentUserTestService(t, svc)
		if _, err := MembershipOf(membershipContext(t, authSQLTenantID), membershipTargetID); errs.Code(err) != errs.PermissionDenied {
			t.Fatalf("inactive caller = %v, want permission_denied", err)
		}
	})
}
