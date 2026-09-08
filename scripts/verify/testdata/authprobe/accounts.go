package main

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"scenery.sh/auth"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

func schemaJourney(p *probe) {
	var schema string
	must(p.db.QueryRowContext(p.ctx, `select n.nspname from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relname = 'scenery_auth_users'`).Scan(&schema))
	p.check(schema == "scenery", "public auth initialization bootstraps the framework schema")
}

func devNewJourney(p *probe) {
	first := take(auth.DevBootstrap(p.ctx, nil))
	firstClaims := take(auth.ValidateToken(first.Token))
	p.check(firstClaims.TenantID == auth.TenantID(configuredTenant), "new dev user token uses configured tenant")
	second := take(auth.DevBootstrap(p.ctx, nil))
	secondClaims := take(auth.ValidateToken(second.Token))
	p.check(firstClaims.UserID == secondClaims.UserID && firstClaims.TenantID == secondClaims.TenantID, "repeated dev bootstrap keeps user and tenant identity")
	user := take(p.q.GetUserByNormalizedEmail(p.ctx, "petr@example.test"))
	p.check(user.EmailVerifiedAt.Valid, "dev bootstrap verifies its configured local user")
	memberships := take(p.q.ListUserMemberships(p.ctx, user.ID))
	p.check(len(memberships) == 1 && memberships[0].Role == "owner" && idString(memberships[0].TenantID) == configuredTenant, "new dev user has exactly one owner membership")
	p.check(take(p.q.CountAuthIdentitiesByUser(p.ctx, user.ID)) == 0, "dev bootstrap creates no login provider identity")
	p.check(strings.Contains(first.SetCookie, "scenery_refresh=") && strings.Contains(second.SetCookie, "scenery_refresh="), "both bootstrap calls issue refresh cookies")
	var count int
	must(p.db.QueryRowContext(p.ctx, `select count(*) from scenery.scenery_auth_users where normalized_primary_email=$1`, "petr@example.test").Scan(&count))
	p.check(count == 1, "repeated dev bootstrap creates no duplicate user")
}

func devExistingJourney(p *probe) {
	user := take(p.q.EnsureDevBootstrapUser(p.ctx, authdb.EnsureDevBootstrapUserParams{ID: newID(), DisplayName: "Petr", PrimaryEmail: "petr@example.test", NormalizedPrimaryEmail: "petr@example.test"}))
	other := take(p.q.EnsureDevBootstrapTenant(p.ctx, authdb.EnsureDevBootstrapTenantParams{ID: newID(), Name: "Other Workspace"}))
	_ = take(p.q.CreateOrganizationMembership(p.ctx, authdb.CreateOrganizationMembershipParams{ID: newID(), TenantID: other.ID, UserID: user.ID, Role: "owner"}))
	session := take(auth.DevBootstrap(p.ctx, nil))
	claims := take(auth.ValidateToken(session.Token))
	p.check(string(claims.UserID) == idString(user.ID) && session.User.ID == idString(user.ID), "dev bootstrap reuses the existing user")
	p.check(string(claims.TenantID) == configuredTenant, "configured tenant wins over existing unrelated membership")
	p.check(strings.Contains(session.SetCookie, "scenery_refresh="), "existing dev user receives a refresh cookie")
	memberships := take(p.q.ListUserMemberships(p.ctx, user.ID))
	attached := false
	for _, membership := range memberships {
		if idString(membership.TenantID) == configuredTenant {
			attached = membership.Role == "owner"
		}
	}
	p.check(attached, "existing dev user is attached as owner of configured tenant")
	var count int
	must(p.db.QueryRowContext(p.ctx, `select count(*) from scenery.scenery_auth_users where normalized_primary_email=$1`, "petr@example.test").Scan(&count))
	p.check(count == 1, "attaching a dev user does not duplicate it")
}

func refreshReplayJourney(p *probe) {
	p.signup("replay@example.test", true)
	_, login := call[auth.AuthSessionResponse](p, http.MethodPost, "/auth/login/email", auth.EmailLoginParams{Email: "replay@example.test", Password: "correct horse battery staple"}, "", "")
	zero := cookie(login)
	_, one := p.refresh(zero)
	_, two := p.refresh(one)
	response, _ := p.request(http.MethodPost, "/auth/refresh", nil, "", zero)
	p.check(response.StatusCode == http.StatusUnauthorized, "replaying an old refresh token is rejected")
	response, _ = p.request(http.MethodPost, "/auth/refresh", nil, "", two)
	p.check(response.StatusCode == http.StatusUnauthorized, "replay revocation survives the transaction and kills the current token")
}

func (p *probe) actor(privileged bool) (authdb.SceneryAuthUser, authdb.UUID, *auth.AuthData, string) {
	actor := take(p.q.CreateUser(p.ctx, authdb.CreateUserParams{ID: newID(), DisplayName: "Admin", PrimaryEmail: "admin@example.test", NormalizedPrimaryEmail: "admin@example.test", EmailVerifiedAt: sql.NullTime{Time: time.Now(), Valid: true}}))
	if privileged {
		_, err := p.db.ExecContext(p.ctx, `update scenery.scenery_auth_users set can_impersonate_users=true where id=$1`, actor.ID)
		must(err)
	}
	tenant := take(p.q.CreateTenant(p.ctx, authdb.CreateTenantParams{ID: newID(), Name: "EDGE"}))
	_ = take(p.q.CreateOrganizationMembership(p.ctx, authdb.CreateOrganizationMembershipParams{ID: newID(), TenantID: tenant.ID, UserID: actor.ID, Role: "owner"}))
	data := &auth.AuthData{UserID: auth.AuthUserID(idString(actor.ID)), TenantID: auth.TenantID(idString(tenant.ID))}
	token := take(auth.GenerateToken(data.UserID, data.TenantID, time.Hour))
	return actor, tenant.ID, data, token
}

func impersonationJourney(p *probe) {
	actor, tenant, data, token := p.actor(true)
	ctx := auth.WithContext(p.ctx, auth.UID(data.UserID), data)
	params := auth.PrepareImpersonationTargetParams{Email: "Target@Example.test", DisplayName: "Target Person"}
	first := take(auth.PrepareImpersonationTarget(ctx, params))
	second := take(auth.PrepareImpersonationTarget(ctx, params))
	p.check(first.ID != "" && first.ID == second.ID && !first.EmailVerified, "impersonation preparation is idempotent and leaves target unverified")
	p.check(take(p.q.CountAuthIdentitiesByUser(p.ctx, id(first.ID))) == 0, "prepared target has no login identity")
	_ = take(p.q.GetActiveMembership(p.ctx, authdb.GetActiveMembershipParams{UserID: id(first.ID), TenantID: tenant}))
	p.check(true, "prepared target belongs to the actor tenant")
	session, _ := call[auth.AuthSessionResponse](p, http.MethodPost, "/auth/impersonation/start", auth.StartImpersonationParams{TargetUserID: first.ID, TenantID: idString(tenant), Reason: "test business workflow"}, token, "")
	claims := take(auth.ValidateToken(session.Token))
	p.check(string(claims.UserID) == first.ID && string(claims.ActorUserID) == idString(actor.ID) && claims.Impersonating(), "impersonation token keeps distinct effective and actor identities")
	p.check(!session.User.EmailVerified, "impersonation does not verify a provider-free target")
	_, err := auth.PrepareImpersonationTarget(auth.WithContext(p.ctx, auth.UID(claims.UserID), claims), auth.PrepareImpersonationTargetParams{Email: "other@example.test"})
	p.check(errs.Code(err) == errs.PermissionDenied, "nested impersonation preparation is denied")
}

func impersonationPrivilegeJourney(p *probe) {
	_, _, data, _ := p.actor(false)
	_, err := auth.PrepareImpersonationTarget(auth.WithContext(p.ctx, auth.UID(data.UserID), data), auth.PrepareImpersonationTargetParams{Email: "target@example.test"})
	p.check(errs.Code(err) == errs.PermissionDenied, "ordinary membership cannot prepare an impersonation target")
}
