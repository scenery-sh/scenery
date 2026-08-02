package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
	"scenery.sh/runtime"
)

func TestPrepareImpersonationTargetAndStartUnverifiedSession(t *testing.T) {
	ctx := context.Background()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	resetStandardAuthStateForTest(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	runtime.SetAppConfig(runtime.AppConfig{Name: "impersonation-target-test", ListenAddr: "127.0.0.1:0"})

	cfg := normalizeStandardConfig(StandardConfig{Enabled: true, AutoBootstrapDatabase: true})
	applyStandardSecrets(cfg)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = cfg
	standardAuthState.mu.Unlock()
	svc, err := standardAuthService(ctx)
	if err != nil {
		t.Fatalf("standard auth service: %v", err)
	}

	actorID, _ := newUUID()
	actor, err := svc.query.CreateUser(ctx, authdb.CreateUserParams{
		ID: actorID, DisplayName: "Admin", PrimaryEmail: "admin@example.test",
		NormalizedPrimaryEmail: "admin@example.test", EmailVerifiedAt: sql.NullTime{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	if _, err := svc.db.ExecContext(ctx, `update scenery.scenery_auth_users set can_impersonate_users = true where id = $1`, actor.ID); err != nil {
		t.Fatalf("grant impersonation: %v", err)
	}
	tenantID, _ := newUUID()
	if _, err := svc.query.CreateTenant(ctx, authdb.CreateTenantParams{ID: tenantID, Name: "EDGE"}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	membershipID, _ := newUUID()
	if _, err := svc.query.CreateOrganizationMembership(ctx, authdb.CreateOrganizationMembershipParams{
		ID: membershipID, TenantID: tenantID, UserID: actor.ID, Role: roleOwner,
	}); err != nil {
		t.Fatalf("create actor membership: %v", err)
	}
	authData := &AuthData{UserID: AuthUserID(uuidString(actor.ID)), TenantID: TenantID(uuidString(tenantID))}
	authCtx := WithContext(ctx, UID(authData.UserID), authData)

	params := PrepareImpersonationTargetParams{Email: "Target@Example.test", DisplayName: "Target Person"}
	first, err := PrepareImpersonationTarget(authCtx, params)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	second, err := PrepareImpersonationTarget(authCtx, params)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if first.ID == "" || second.ID != first.ID || first.EmailVerified {
		t.Fatalf("prepared profiles first=%+v second=%+v", first, second)
	}
	targetID, _ := parseUUID(first.ID)
	if count, err := svc.query.CountAuthIdentitiesByUser(ctx, targetID); err != nil || count != 0 {
		t.Fatalf("target identities count=%d err=%v, want 0", count, err)
	}
	if _, err := svc.query.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: targetID, TenantID: tenantID}); err != nil {
		t.Fatalf("target membership: %v", err)
	}

	session, err := svc.StartImpersonation(authCtx, &StartImpersonationParams{
		TargetUserID: first.ID, TenantID: uuidString(tenantID), Reason: "test business workflow",
	})
	if err != nil {
		t.Fatalf("start impersonation: %v", err)
	}
	claims, err := ValidateToken(session.Token)
	if err != nil {
		t.Fatalf("validate impersonation token: %v", err)
	}
	if string(claims.UserID) != first.ID || string(claims.ActorUserID) != uuidString(actor.ID) || !claims.Impersonating() {
		t.Fatalf("impersonation claims = %+v", claims)
	}
	if session.User.EmailVerified {
		t.Fatal("impersonation marked provider-free target verified")
	}

	nestedCtx := WithContext(ctx, UID(claims.UserID), claims)
	if _, err := PrepareImpersonationTarget(nestedCtx, PrepareImpersonationTargetParams{Email: "other@example.test"}); errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("nested prepare error=%v code=%s, want permission denied", err, errs.Code(err))
	}
}

func TestPrepareImpersonationTargetRequiresPrivilege(t *testing.T) {
	ctx := context.Background()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	resetStandardAuthStateForTest(t)
	t.Setenv("DATABASE_URL", databaseURL)
	runtime.SetAppConfig(runtime.AppConfig{Name: "impersonation-privilege-test", ListenAddr: "127.0.0.1:0"})
	cfg := normalizeStandardConfig(StandardConfig{Enabled: true, AutoBootstrapDatabase: true})
	applyStandardSecrets(cfg)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = cfg
	standardAuthState.mu.Unlock()
	svc, err := standardAuthService(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, _ := newUUID()
	actor, err := svc.query.CreateUser(ctx, authdb.CreateUserParams{ID: actorID, PrimaryEmail: "member@example.test", NormalizedPrimaryEmail: "member@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	tenantID, _ := newUUID()
	if _, err := svc.query.CreateTenant(ctx, authdb.CreateTenantParams{ID: tenantID, Name: "EDGE"}); err != nil {
		t.Fatal(err)
	}
	membershipID, _ := newUUID()
	if _, err := svc.query.CreateOrganizationMembership(ctx, authdb.CreateOrganizationMembershipParams{ID: membershipID, TenantID: tenantID, UserID: actor.ID, Role: roleMember}); err != nil {
		t.Fatal(err)
	}
	data := &AuthData{UserID: AuthUserID(uuidString(actor.ID)), TenantID: TenantID(uuidString(tenantID))}
	_, err = PrepareImpersonationTarget(WithContext(ctx, UID(data.UserID), data), PrepareImpersonationTargetParams{Email: "target@example.test"})
	if errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("prepare error=%v code=%s, want permission denied", err, errs.Code(err))
	}
}
