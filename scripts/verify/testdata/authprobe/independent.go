package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"scenery.sh/auth"
	authdb "scenery.sh/auth/db/gen"
)

type fakeOAuth2 struct {
	server                     *httptest.Server
	mu                         sync.Mutex
	challenge, callback, scope string
	refreshCalls               atomic.Int64
	beforeRefresh              func(*http.Request)
}

func newFakeOAuth2() *fakeOAuth2 {
	f := &fakeOAuth2{}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}
func (f *fakeOAuth2) authorize(raw string) string {
	u := take(url.Parse(raw))
	q := u.Query()
	f.mu.Lock()
	f.challenge, f.callback, f.scope = q.Get("code_challenge"), q.Get("redirect_uri"), q.Get("scope")
	f.mu.Unlock()
	callback := take(url.Parse(q.Get("redirect_uri")))
	values := callback.Query()
	values.Set("code", "independent-code")
	values.Set("state", q.Get("state"))
	values.Set("iss", f.server.URL)
	callback.RawQuery = values.Encode()
	return callback.RequestURI()
}
func (f *fakeOAuth2) serve(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch req.URL.Path {
	case "/token":
		must(req.ParseForm())
		client, secret, ok := req.BasicAuth()
		if !ok || client != "independent-client" || secret != "independent-secret" || req.PostForm.Get("resource") != "https://files.example" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		f.mu.Lock()
		challenge, callback, scope := f.challenge, f.callback, f.scope
		f.mu.Unlock()
		access, refresh := "independent-access", "independent-refresh"
		if req.PostForm.Get("grant_type") == "authorization_code" {
			digest := sha256.Sum256([]byte(req.PostForm.Get("code_verifier")))
			if req.PostForm.Get("code") != "independent-code" || req.PostForm.Get("redirect_uri") != callback || base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
		} else {
			f.refreshCalls.Add(1)
			if f.beforeRefresh != nil {
				f.beforeRefresh(req)
			}
			if req.PostForm.Get("refresh_token") != "independent-refresh" {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			access, refresh = "independent-rotated-access", "independent-rotated-refresh"
		}
		// This provider deliberately returns no ID token.
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600, "scope": scope, "resource": "https://files.example"})
	case "/account":
		if req.Header.Get("Authorization") != "Bearer independent-access" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"account_id": "independent-account", "email": "different-account@example.test"})
	case "/revoke":
		w.WriteHeader(200)
	default:
		w.WriteHeader(404)
	}
}

func independentProviderJourney(p *probe) {
	session, _ := p.signin()
	user := take(p.q.GetUserByID(p.ctx, id(session.User.ID)))
	var identities int
	must(p.db.QueryRowContext(p.ctx, `SELECT count(*) FROM scenery.scenery_auth_auth_identities WHERE user_id=$1`, id(session.User.ID)).Scan(&identities))
	start, _ := call[auth.ConnectionStartResponse](p, http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}, RedirectPath: "/settings?tab=connections"}, session.Token, "")
	u := take(url.Parse(start.AuthorizeURL))
	p.check(!strings.Contains(u.Query().Get("scope"), "openid") && u.Query().Get("resource") == "https://files.example", "OAuth2-only authorization has no Google identity scopes and binds its resource")
	callback := p.independent.authorize(start.AuthorizeURL)
	response, _ := p.request(http.MethodGet, callback, nil, "", "")
	p.redirect(response, "https://app.example.test/settings?connection_connected=independent&tab=connections")
	current := take(p.q.GetUserByID(p.ctx, id(session.User.ID)))
	p.check(current.PrimaryEmail == user.PrimaryEmail && current.DisplayName == user.DisplayName && current.AvatarUrl == user.AvatarUrl, "connecting another provider preserves the sign-in profile")
	var after int
	must(p.db.QueryRowContext(p.ctx, `SELECT count(*) FROM scenery.scenery_auth_auth_identities WHERE user_id=$1`, id(session.User.ID)).Scan(&after))
	p.check(after == identities, "external connection creates no login identity")
	status, _ := call[auth.ConnectionResponse](p, http.MethodGet, "/auth/connections/independent", nil, session.Token, "")
	p.check(status.Subject == "independent-account" && status.Provider == "independent" && status.Resource == "https://files.example", "metadata identifies the configured external account and resource")
	_, body := p.request(http.MethodGet, "/auth/connections/independent", nil, session.Token, "")
	p.check(!strings.Contains(string(body), "independent-access") && !strings.Contains(string(body), "independent-refresh") && !strings.Contains(string(body), "ciphertext"), "connection response excludes credentials and ciphertext")
	p.check(take(auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")) == "independent-access", "trusted worker receives the cached non-Google bearer")
	_, err := p.db.ExecContext(p.ctx, `UPDATE scenery.scenery_auth_connections SET access_token_expires_at=now()-interval '1 minute' WHERE user_id=$1 AND provider_id='independent'`, id(session.User.ID))
	must(err)
	p.check(take(auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")) == "independent-rotated-access" && p.independent.refreshCalls.Load() == 1, "same vault refreshes and rotates a non-Google credential")
	disconnected, _ := call[auth.ConnectionResponse](p, http.MethodPost, "/auth/connections/independent/disconnect", nil, session.Token, "")
	p.check(disconnected.Status == "disconnected" && disconnected.RevocationStatus == "succeeded", "non-Google disconnect reports local and provider outcomes")
	row := take(p.q.GetConnectionByUser(p.ctx, authdb.GetConnectionByUserParams{UserID: id(session.User.ID), ProviderID: "independent"}))
	p.check(len(row.RefreshTokenCiphertext) == 0 && len(row.AccessTokenCiphertext) == 0, "disconnect clears both stored credentials")
}

func connectionIsolationJourney(p *probe) {
	session, _ := p.signin()
	start, _ := call[auth.ConnectionStartResponse](p, http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}}, session.Token, "")
	callback := p.independent.authorize(start.AuthorizeURL)
	saved := p.browserCookies
	p.browserCookies = nil
	without, _ := p.request(http.MethodGet, callback, nil, "", "")
	p.check(without.StatusCode == 400, "callback rejects an absent initiating browser cookie")
	p.browserCookies = saved
	wrong := take(url.Parse(callback))
	q := wrong.Query()
	q.Set("iss", "https://wrong.example")
	wrong.RawQuery = q.Encode()
	issuer, _ := p.request(http.MethodGet, wrong.RequestURI(), nil, "", "")
	p.check(issuer.StatusCode == 400, "callback rejects issuer mix-up without consuming legitimate state")
	provider, _ := p.request(http.MethodGet, strings.Replace(callback, "/independent/", "/google/", 1), nil, "", "")
	p.check(provider.StatusCode == 400, "callback rejects a different provider slot")
	valid, _ := p.request(http.MethodGet, callback, nil, "", "")
	p.check(valid.StatusCode == 302 && strings.Contains(valid.Header.Get("Location"), "connection_connected=independent"), "matching browser/provider/issuer callback succeeds")
	replay, _ := p.request(http.MethodGet, callback, nil, "", "")
	p.check(replay.StatusCode == 400, "consumed callback cannot be replayed")
	other := p.signup("another-owner@example.test", true)
	status, _ := call[auth.ConnectionResponse](p, http.MethodGet, "/auth/connections/google", nil, session.Token, "")
	p.check(status.Status == "disconnected", "provider slots do not select another provider's row")
	_, err := p.q.GetConnectionByUser(p.ctx, authdb.GetConnectionByUserParams{UserID: other, ProviderID: "independent"})
	p.check(err != nil, "external credentials remain owned by the initiating user")
}
