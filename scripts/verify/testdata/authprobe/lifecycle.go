package main

import (
	"time"

	"scenery.sh/auth"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

func userLifecycleJourney(p *probe) {
	userID, otherID, tenantID := newID(), newID(), newID()
	_ = take(p.q.CreateTenant(p.ctx, authdb.CreateTenantParams{ID: tenantID, Name: "Lifecycle"}))
	for _, user := range []struct {
		id    authdb.UUID
		email string
	}{{userID, "offboard@example.test"}, {otherID, "other@example.test"}} {
		_ = take(p.q.CreateUser(p.ctx, authdb.CreateUserParams{ID: user.id, DisplayName: user.email, PrimaryEmail: user.email, NormalizedPrimaryEmail: user.email}))
	}
	createSession := func(owner authdb.UUID, token string) authdb.UUID {
		row := take(p.q.CreateRefreshSession(p.ctx, authdb.CreateRefreshSessionParams{ID: newID(), UserID: owner, TokenHash: token, ActiveTenantID: tenantID, ExpiresAt: time.Now().Add(time.Hour)}))
		return row.ID
	}
	sessionID, otherSessionID := createSession(userID, "offboard-token"), createSession(otherID, "other-token")
	user := auth.AuthUserID(idString(userID))
	offboardToken := take(auth.GenerateAccessToken(auth.AccessTokenOptions{UserID: user, TenantID: auth.TenantID(idString(tenantID)), SessionID: idString(sessionID), ExpiresIn: time.Hour}))
	_, _, err := auth.AuthHandler(p.ctx, offboardToken)
	must(err)
	p.check(true, "active refresh-backed access token authenticates")
	_, err = p.db.ExecContext(p.ctx, `CREATE FUNCTION scenery.reject_lifecycle_revoke() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced revoke failure'; END $$; CREATE TRIGGER reject_lifecycle_revoke BEFORE UPDATE ON scenery.scenery_auth_refresh_sessions FOR EACH ROW WHEN (OLD.user_id = '`+idString(userID)+`'::uuid) EXECUTE FUNCTION scenery.reject_lifecycle_revoke();`)
	must(err)
	p.check(auth.DisableUser(p.ctx, user, "company_offboarding") != nil, "forced session-revoke failure rejects user disable")
	p.check(!take(p.q.GetUserByID(p.ctx, userID)).DisabledAt.Valid, "failed session revocation rolls user disable back")
	_, _, err = auth.AuthHandler(p.ctx, offboardToken)
	must(err)
	p.check(true, "rollback preserves the original active access token")
	_, err = p.db.ExecContext(p.ctx, `DROP TRIGGER reject_lifecycle_revoke ON scenery.scenery_auth_refresh_sessions; DROP FUNCTION scenery.reject_lifecycle_revoke()`)
	must(err)
	must(auth.DisableUser(p.ctx, user, "company_offboarding"))
	first := take(p.q.GetUserByID(p.ctx, userID))
	p.check(first.DisabledAt.Valid, "user disable persists")
	session := take(p.q.GetRefreshSessionByID(p.ctx, sessionID))
	p.check(session.RevokedAt.Valid && session.RevokedReason == "company_offboarding", "disable revokes sessions with the requested reason")
	p.check(!take(p.q.GetRefreshSessionByID(p.ctx, otherSessionID)).RevokedAt.Valid, "disable preserves unrelated user sessions")
	_, _, err = auth.AuthHandler(p.ctx, offboardToken)
	p.check(errs.Code(err) == errs.PermissionDenied, "disabled user access token is rejected")
	must(auth.DisableUser(p.ctx, user, "company_offboarding_retry"))
	p.check(take(p.q.GetUserByID(p.ctx, userID)).DisabledAt.Time.Equal(first.DisabledAt.Time), "repeated disable preserves disabled_at")
	must(auth.EnableUser(p.ctx, user))
	p.check(!take(p.q.GetUserByID(p.ctx, userID)).DisabledAt.Valid, "enable clears disabled state")
	p.check(take(p.q.GetRefreshSessionByID(p.ctx, sessionID)).RevokedAt.Valid, "enable never restores revoked sessions")
	_, _, err = auth.AuthHandler(p.ctx, offboardToken)
	p.check(errs.Code(err) == errs.Unauthenticated, "old token remains invalid after re-enable")
	must(auth.EnableUser(p.ctx, user))
	newSessionID := createSession(userID, "new-token")
	must(auth.RevokeUserSessions(p.ctx, user, "security_reset"))
	session = take(p.q.GetRefreshSessionByID(p.ctx, newSessionID))
	p.check(session.RevokedAt.Valid && session.RevokedReason == "security_reset", "standalone revocation persists its reason")
	must(auth.RevokeUserSessions(p.ctx, user, "security_reset_retry"))
	impersonationID, impersonationSessionID := newID(), newID()
	_ = take(p.q.CreateRefreshSession(p.ctx, authdb.CreateRefreshSessionParams{ID: impersonationSessionID, UserID: otherID, TokenHash: "impersonation-token", ActiveTenantID: tenantID, ExpiresAt: time.Now().Add(time.Hour), ActorUserID: userID, ImpersonationID: impersonationID, ImpersonationReason: "support"}))
	impersonationToken := take(auth.GenerateAccessToken(auth.AccessTokenOptions{UserID: auth.AuthUserID(idString(otherID)), TenantID: auth.TenantID(idString(tenantID)), SessionID: idString(impersonationSessionID), ActorUserID: user, ImpersonationID: idString(impersonationID), ExpiresIn: time.Hour}))
	_, _, err = auth.AuthHandler(p.ctx, impersonationToken)
	must(err)
	p.check(true, "active impersonation session authenticates")
	must(auth.DisableUser(p.ctx, user, "actor_offboarding"))
	must(auth.EnableUser(p.ctx, user))
	_, _, err = auth.AuthHandler(p.ctx, impersonationToken)
	p.check(errs.Code(err) == errs.Unauthenticated, "actor offboarding permanently revokes impersonation access")
	session = take(p.q.GetRefreshSessionByID(p.ctx, impersonationSessionID))
	p.check(session.RevokedAt.Valid && session.RevokedReason == "actor_offboarding", "actor session revocation persists its reason")
	missing := auth.AuthUserID("11111111-1111-1111-1111-111111111111")
	p.check(errs.Code(auth.DisableUser(p.ctx, missing, "offboarding")) == errs.NotFound, "disabling a missing user reports not found")
	p.check(errs.Code(auth.EnableUser(p.ctx, missing)) == errs.NotFound, "enabling a missing user reports not found")
	p.check(errs.Code(auth.RevokeUserSessions(p.ctx, missing, "offboarding")) == errs.NotFound, "revoking a missing user reports not found")
}
