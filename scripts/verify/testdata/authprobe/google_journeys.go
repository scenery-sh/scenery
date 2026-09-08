package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"scenery.sh/auth"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

func oauthBrowserJourney(p *probe) {
	callback, response := p.googleFlow("/welcome")
	p.redirect(response, "https://app.example.test/welcome")
	session, _ := p.refresh(cookie(response))
	claims := take(auth.ValidateToken(session.Token))
	p.check(claims.UserID != "" && claims.TenantID != "", "OAuth refresh yields user and tenant")
	me, _ := call[auth.AuthBootstrapResponse](p, http.MethodGet, "/auth/me", nil, session.Token, "")
	p.check(me.User.ID == string(claims.UserID) && me.CurrentTenantID == string(claims.TenantID), "authenticated Me preserves OAuth identity and tenant")
	replay, _ := p.request(http.MethodGet, callback, nil, "", "")
	p.redirect(replay, "https://app.example.test/sign-in?error=oauth_state")
	verifiedID := p.signup("linked@example.test", true)
	p.google.set(func(f *fakeGoogle) { f.subject = "linked-subject"; f.email = "linked@example.test" })
	_, linked := p.googleFlow("/")
	p.redirect(linked, "https://app.example.test/")
	identity := take(p.q.GetAuthIdentityByProviderSubject(p.ctx, authdb.GetAuthIdentityByProviderSubjectParams{Provider: "google", ProviderSubject: "linked-subject"}))
	p.check(identity.UserID == verifiedID, "Google links the existing verified email identity")
	prepared := take(p.q.CreateUser(p.ctx, authdb.CreateUserParams{ID: newID(), DisplayName: "Prepared User", PrimaryEmail: "prepared@example.test", NormalizedPrimaryEmail: "prepared@example.test"}))
	p.google.set(func(f *fakeGoogle) { f.subject = "prepared-subject"; f.email = "prepared@example.test" })
	_, preparedResponse := p.googleFlow("/")
	p.redirect(preparedResponse, "https://app.example.test/")
	identity = take(p.q.GetAuthIdentityByProviderSubject(p.ctx, authdb.GetAuthIdentityByProviderSubjectParams{Provider: "google", ProviderSubject: "prepared-subject"}))
	p.check(identity.UserID == prepared.ID, "Google links a provider-free prepared user")
	p.check(take(p.q.GetUserByID(p.ctx, prepared.ID)).EmailVerifiedAt.Valid, "Google verifies the prepared user")
	p.signup("unverified@example.test", false)
	p.google.set(func(f *fakeGoogle) { f.subject = "unverified-subject"; f.email = "unverified@example.test" })
	_, unverified := p.googleFlow("/")
	p.redirect(unverified, "https://app.example.test/sign-in?error=google_link_precondition")
	_, err := p.q.GetAuthIdentityByProviderSubject(p.ctx, authdb.GetAuthIdentityByProviderSubjectParams{Provider: "google", ProviderSubject: "unverified-subject"})
	p.check(errors.Is(err, sql.ErrNoRows), "rejected email link creates no Google identity")
	p.google.set(func(f *fakeGoogle) {
		f.subject = "nonce-subject"
		f.email = "nonce@example.test"
		f.nonceOverride = "wrong-nonce"
	})
	_, nonce := p.googleFlow("/")
	p.redirect(nonce, "https://app.example.test/sign-in?error=google_id_token")
	p.google.set(func(f *fakeGoogle) {
		f.subject = "email-unverified-subject"
		f.email = "email-unverified@example.test"
		f.nonceOverride = ""
		f.verified = false
	})
	_, email := p.googleFlow("/")
	p.redirect(email, "https://app.example.test/sign-in?error=google_email_unverified")
}

func connectionRedirectJourney(p *probe) {
	session, _ := p.signin()
	start, _ := call[auth.GoogleConnectStartResponse](p, http.MethodPost, "/auth/google/connect/start", auth.GoogleConnectStartParams{Scopes: []string{gmailModify}}, session.Token, "")
	authorize := take(url.Parse(start.AuthorizeURL))
	p.check(authorize.Query().Get("redirect_uri") == "https://api.example.test/auth/google/callback", "Google connection uses the API callback origin")
	p.check(authorize.Host == "accounts.google.com" && authorize.Path == "/o/oauth2/v2/auth", "connection uses the current public Google authorization endpoint")
}

func (p *probe) connect(session auth.AuthSessionResponse, redirect string) *http.Response {
	start, _ := call[auth.GoogleConnectStartResponse](p, http.MethodPost, "/auth/google/connect/start", auth.GoogleConnectStartParams{Scopes: []string{gmailModify}, RedirectPath: redirect}, session.Token, "")
	callback := take(url.Parse(p.google.authorize(start.AuthorizeURL)))
	response, _ := p.request(http.MethodGet, callback.RequestURI(), nil, "", "")
	p.check(response.StatusCode == http.StatusFound && strings.Contains(response.Header.Get("Location"), "google_connected=1"), "authenticated connection callback succeeds")
	return response
}

func (p *probe) connection() auth.AuthSessionResponse {
	session, _ := p.signin()
	p.google.set(func(f *fakeGoogle) { f.access = "initial-access"; f.refresh = "initial-refresh"; f.scope = gmailModify })
	p.connect(session, "/")
	return session
}

func (p *probe) expire(userID string) {
	_, err := p.db.ExecContext(p.ctx, `update scenery.scenery_auth_google_connections set access_token_expires_at = now() - interval '1 minute' where user_id = $1`, id(userID))
	must(err)
}

func connectionStoreJourney(p *probe) {
	session := p.connection()
	connection := take(p.q.GetGoogleConnectionByUser(p.ctx, id(session.User.ID)))
	p.check(connection.Status == "active" && connection.Email == "person@example.test" && containsScope(strings.Fields(connection.Scopes), gmailModify), "persisted connection keeps email, active status and granted scope")
	p.check(len(connection.RefreshTokenCiphertext) > 0 && !strings.Contains(string(connection.RefreshTokenCiphertext), "initial-refresh"), "refresh token is not persisted in plaintext")
	p.expire(session.User.ID)
	_ = take(auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailModify))
	p.check(p.google.usedRefresh("initial-refresh"), "public token refresh decrypts the exact persisted refresh token")
	status, _ := call[auth.GoogleConnectionResponse](p, http.MethodGet, "/auth/google/connection", nil, session.Token, "")
	p.check(status.Status == "active" && containsScope(status.Scopes, gmailModify), "HTTP connection status exposes active granted scopes")
	disconnected, _ := call[auth.GoogleConnectionResponse](p, http.MethodPost, "/auth/google/connection/disconnect", nil, session.Token, "")
	p.check(disconnected.Status == "disconnected", "HTTP disconnect reports disconnected")
	p.check(p.google.revokeCalls.Load() == 1, "disconnect revokes the Google token exactly once")
}

func connectionErrorJourney(p *probe) {
	session, _ := p.signin()
	start, _ := call[auth.GoogleConnectStartResponse](p, http.MethodPost, "/auth/google/connect/start", auth.GoogleConnectStartParams{Scopes: []string{gmailModify}, RedirectPath: "/settings"}, session.Token, "")
	authorize := take(url.Parse(start.AuthorizeURL))
	response, _ := p.request(http.MethodGet, "/auth/google/callback?error=access_denied&state="+url.QueryEscape(authorize.Query().Get("state")), nil, "", "")
	p.redirect(response, "https://app.example.test/settings?error=google_oauth")
}

func tokenRotationJourney(p *probe) {
	session := p.connection()
	p.check(take(auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailModify)) == "initial-access", "cached public access token is returned unchanged")
	p.expire(session.User.ID)
	p.google.set(func(f *fakeGoogle) {
		f.queue = append(f.queue, fakeRefresh{access: "rotated-access", refresh: "rotated-refresh", scope: gmailModify})
	})
	var wg sync.WaitGroup
	values := make([]string, 2)
	failures := make([]error, 2)
	start := make(chan struct{})
	for index := range values {
		wg.Go(func() {
			<-start
			values[index], failures[index] = auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailModify)
		})
	}
	close(start)
	wg.Wait()
	for index, value := range values {
		must(failures[index])
		p.check(value == "rotated-access", "concurrent refresh caller receives rotated access token")
	}
	p.check(p.google.refreshCalls.Load() == 1, "PostgreSQL row lock single-flights concurrent refresh")
	connection := take(p.q.GetGoogleConnectionByUser(p.ctx, id(session.User.ID)))
	p.check(!strings.Contains(string(connection.RefreshTokenCiphertext), "rotated-refresh"), "rotated refresh token remains encrypted")
	p.expire(session.User.ID)
	_ = take(auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailModify))
	p.check(p.google.usedRefresh("rotated-refresh"), "subsequent refresh consumes the exact rotated persisted token")
}

func tokenRetryJourney(p *probe) {
	session := p.connection()
	p.expire(session.User.ID)
	p.google.set(func(f *fakeGoogle) {
		f.queue = append(f.queue, fakeRefresh{status: http.StatusInternalServerError, errorCode: "server_error"}, fakeRefresh{access: "retried-access", scope: gmailModify})
	})
	p.check(take(auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailModify)) == "retried-access", "transient Google error retries to success")
	p.check(p.google.refreshCalls.Load() == 2, "transient refresh performs exactly two requests")
	p.expire(session.User.ID)
	p.google.set(func(f *fakeGoogle) {
		f.queue = append(f.queue, fakeRefresh{status: http.StatusBadRequest, errorCode: "invalid_grant"})
	})
	_, err := auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailModify)
	p.check(errs.Code(err) == errs.GoogleReauthRequired, "permanent Google failure requires reauthorization")
	connection := take(p.q.GetGoogleConnectionByUser(p.ctx, id(session.User.ID)))
	p.check(connection.Status == "reauth_required" && connection.LastRefreshError == "invalid_grant", "permanent refresh failure is durably persisted")
	before := p.google.refreshCalls.Load()
	_, err = auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailModify)
	p.check(errs.Code(err) == errs.GoogleReauthRequired && p.google.refreshCalls.Load() == before, "persisted reauthorization state fails without another Google call")
}

func tokenMissingScopeJourney(p *probe) {
	session := p.connection()
	_, err := auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, gmailRead)
	p.check(errs.Code(err) == errs.GoogleScopeMissing, "allowed but ungranted scope is reported as missing")
}

func tokenDisallowedScopeJourney(p *probe) {
	session, _ := p.signin()
	p.google.set(func(f *fakeGoogle) {
		f.access = "initial-access"
		f.refresh = "initial-refresh"
		f.scope = gmailModify + " " + calendarEvents
	})
	p.connect(session, "/")
	_, err := auth.GoogleAccessTokenForUser(p.ctx, session.User.ID, calendarEvents)
	p.check(errs.Code(err) == errs.PermissionDenied, "provider grant cannot bypass the application scope allowlist")
}
