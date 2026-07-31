package auth

import (
	"context"
	"testing"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
	"scenery.sh/internal/postgresdb"
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
	ctx := context.Background()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	authURL, err := postgresdb.ServiceURL(databaseURL, "scenery")
	if err != nil {
		t.Fatal(err)
	}
	db, err := postgresdb.Open(ctx, authURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrapStandardAuthSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	installCurrentUserTestService(t, &Service{db: db, query: authdb.New(db), now: time.Now})
	query := authdb.New(db)
	secrets.JWTSecret = "user-lifecycle-test-secret"
	t.Cleanup(func() { secrets.JWTSecret = "" })

	userID, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.CreateTenant(ctx, authdb.CreateTenantParams{ID: tenantID, Name: "Lifecycle test"}); err != nil {
		t.Fatal(err)
	}
	otherUserID, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct {
		id    authdb.UUID
		email string
	}{{userID, "offboard@example.test"}, {otherUserID, "other@example.test"}} {
		if _, err := query.CreateUser(ctx, authdb.CreateUserParams{
			ID: user.id, DisplayName: user.email, PrimaryEmail: user.email,
			NormalizedPrimaryEmail: user.email,
		}); err != nil {
			t.Fatal(err)
		}
	}
	createSession := func(t *testing.T, owner authdb.UUID, token string) authdb.UUID {
		t.Helper()
		sessionID, err := newUUID()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := query.CreateRefreshSession(ctx, authdb.CreateRefreshSessionParams{
			ID: sessionID, UserID: owner, TokenHash: token,
			ActiveTenantID: tenantID, ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		return sessionID
	}
	sessionID := createSession(t, userID, "offboard-token")
	otherSessionID := createSession(t, otherUserID, "other-token")
	offboardToken, err := GenerateAccessToken(AccessTokenOptions{
		UserID: AuthUserID(uuidString(userID)), TenantID: TenantID(uuidString(tenantID)),
		SessionID: uuidString(sessionID), ExpiresIn: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AuthHandler(ctx, offboardToken); err != nil {
		t.Fatalf("active session was rejected: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
CREATE FUNCTION scenery.reject_lifecycle_revoke() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'forced revoke failure'; END $$;
CREATE TRIGGER reject_lifecycle_revoke BEFORE UPDATE ON scenery.scenery_auth_refresh_sessions
FOR EACH ROW WHEN (OLD.user_id = '`+uuidString(userID)+`'::uuid) EXECUTE FUNCTION scenery.reject_lifecycle_revoke();`); err != nil {
		t.Fatal(err)
	}
	if err := DisableUser(ctx, AuthUserID(uuidString(userID)), "company_offboarding"); err == nil {
		t.Fatal("DisableUser succeeded despite forced session-revoke failure")
	}
	user, err := query.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.DisabledAt.Valid {
		t.Fatal("user disable was not rolled back with session revocation")
	}
	if _, _, err := AuthHandler(ctx, offboardToken); err != nil {
		t.Fatalf("rolled-back disable invalidated session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_lifecycle_revoke ON scenery.scenery_auth_refresh_sessions; DROP FUNCTION scenery.reject_lifecycle_revoke()`); err != nil {
		t.Fatal(err)
	}

	if err := DisableUser(ctx, AuthUserID(uuidString(userID)), "company_offboarding"); err != nil {
		t.Fatal(err)
	}
	firstDisabled, err := query.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !firstDisabled.DisabledAt.Valid {
		t.Fatal("user is not disabled")
	}
	revoked, err := query.GetRefreshSessionByID(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !revoked.RevokedAt.Valid || revoked.RevokedReason != "company_offboarding" {
		t.Fatalf("revoked session = %#v", revoked)
	}
	otherSession, err := query.GetRefreshSessionByID(ctx, otherSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if otherSession.RevokedAt.Valid {
		t.Fatal("unrelated user session was revoked")
	}
	if _, _, err := AuthHandler(ctx, offboardToken); errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("disabled-user token error = %v (code %q), want permission_denied", err, errs.Code(err))
	}

	if err := DisableUser(ctx, AuthUserID(uuidString(userID)), "company_offboarding_retry"); err != nil {
		t.Fatal(err)
	}
	secondDisabled, err := query.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !secondDisabled.DisabledAt.Time.Equal(firstDisabled.DisabledAt.Time) {
		t.Fatal("idempotent disable changed disabled_at")
	}
	if err := EnableUser(ctx, AuthUserID(uuidString(userID))); err != nil {
		t.Fatal(err)
	}
	enabled, err := query.GetUserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.DisabledAt.Valid {
		t.Fatal("user is still disabled")
	}
	stillRevoked, err := query.GetRefreshSessionByID(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !stillRevoked.RevokedAt.Valid {
		t.Fatal("enabling restored a revoked session")
	}
	if _, _, err := AuthHandler(ctx, offboardToken); errs.Code(err) != errs.Unauthenticated {
		t.Fatalf("re-enabled old token error = %v (code %q), want unauthenticated", err, errs.Code(err))
	}
	if err := EnableUser(ctx, AuthUserID(uuidString(userID))); err != nil {
		t.Fatal(err)
	}

	newSessionID := createSession(t, userID, "new-token")
	if err := RevokeUserSessions(ctx, AuthUserID(uuidString(userID)), "security_reset"); err != nil {
		t.Fatal(err)
	}
	newSession, err := query.GetRefreshSessionByID(ctx, newSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !newSession.RevokedAt.Valid || newSession.RevokedReason != "security_reset" {
		t.Fatalf("standalone revocation = %#v", newSession)
	}
	if err := RevokeUserSessions(ctx, AuthUserID(uuidString(userID)), "security_reset_retry"); err != nil {
		t.Fatal(err)
	}

	impersonationID, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	impersonationSessionID, err := newUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.CreateRefreshSession(ctx, authdb.CreateRefreshSessionParams{
		ID: impersonationSessionID, UserID: otherUserID, TokenHash: "impersonation-token",
		ActiveTenantID: tenantID, ExpiresAt: time.Now().Add(time.Hour), ActorUserID: userID,
		ImpersonationID: impersonationID, ImpersonationReason: "support",
	}); err != nil {
		t.Fatal(err)
	}
	impersonationToken, err := GenerateAccessToken(AccessTokenOptions{
		UserID: AuthUserID(uuidString(otherUserID)), TenantID: TenantID(uuidString(tenantID)),
		SessionID: uuidString(impersonationSessionID), ActorUserID: AuthUserID(uuidString(userID)),
		ImpersonationID: uuidString(impersonationID), ExpiresIn: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AuthHandler(ctx, impersonationToken); err != nil {
		t.Fatalf("valid impersonation token was rejected: %v", err)
	}
	if err := DisableUser(ctx, AuthUserID(uuidString(userID)), "actor_offboarding"); err != nil {
		t.Fatal(err)
	}
	if err := EnableUser(ctx, AuthUserID(uuidString(userID))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AuthHandler(ctx, impersonationToken); errs.Code(err) != errs.Unauthenticated {
		t.Fatalf("revoked impersonation token error = %v (code %q), want unauthenticated", err, errs.Code(err))
	}
	impersonationSession, err := query.GetRefreshSessionByID(ctx, impersonationSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !impersonationSession.RevokedAt.Valid || impersonationSession.RevokedReason != "actor_offboarding" {
		t.Fatalf("actor session revocation = %#v", impersonationSession)
	}

	missing := AuthUserID("11111111-1111-1111-1111-111111111111")
	if code := errs.Code(DisableUser(ctx, missing, "offboarding")); code != errs.NotFound {
		t.Fatalf("missing disable code = %q, want %q", code, errs.NotFound)
	}
	if code := errs.Code(EnableUser(ctx, missing)); code != errs.NotFound {
		t.Fatalf("missing enable code = %q, want %q", code, errs.NotFound)
	}
	if code := errs.Code(RevokeUserSessions(ctx, missing, "offboarding")); code != errs.NotFound {
		t.Fatalf("missing revoke code = %q, want %q", code, errs.NotFound)
	}
}
