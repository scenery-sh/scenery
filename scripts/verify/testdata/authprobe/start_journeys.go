package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"scenery.sh/auth"
)

func connectionStartCookieJourney(p *probe, session auth.AuthSessionResponse) {
	jar := take(cookiejar.New(nil))
	browser := *p.client
	browser.Jar = jar
	// The browser sees the advertised HTTPS URLs; the owned ingress transport
	// delivers plaintext to the same Unix socket as the terminating proxy.
	browser.Transport = roundTrip(func(req *http.Request) (*http.Response, error) {
		wire := req.Clone(req.Context())
		wire.URL.Scheme = "http"
		return p.client.Transport.RoundTrip(wire)
	})
	start := func(host string) (*http.Response, []byte) {
		req := take(http.NewRequestWithContext(p.ctx, http.MethodPost, "https://"+host+"/auth/connections/independent/start", strings.NewReader(`{"scopes":["files.read"]}`)))
		req.Header.Set("Authorization", "Bearer "+session.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Host", "api.example.test")
		response := take(browser.Do(req))
		body := take(io.ReadAll(response.Body))
		must(response.Body.Close())
		return response, body
	}
	var before, after int
	must(p.db.QueryRowContext(p.ctx, `SELECT count(*) FROM scenery.scenery_auth_connection_states`).Scan(&before))
	wrong, body := start("other.example.test")
	must(p.db.QueryRowContext(p.ctx, `SELECT count(*) FROM scenery.scenery_auth_connection_states`).Scan(&after))
	p.check(wrong.StatusCode == 400 && strings.Contains(string(body), `"code":"invalid_argument"`) && len(wrong.Cookies()) == 0 && before == after, "start on another gateway hostname fails before creating state or an undeliverable browser cookie")
	response, body := start("api.example.test")
	p.check(response.StatusCode == 200, "start succeeds on the configured callback hostname")
	var authorization auth.ConnectionStartResponse
	must(json.Unmarshal(body, &authorization))
	callback := p.independent.authorize(authorization.AuthorizeURL)
	callbackURL := take(url.Parse("https://api.example.test" + callback))
	p.check(len(jar.Cookies(callbackURL)) == 1 && len(jar.Cookies(take(url.Parse("https://other.example.test"+callback)))) == 0, "the browser jar delivers its secure host-only binding only to the callback hostname")
	connected := take(browser.Get(callbackURL.String()))
	_, err := io.Copy(io.Discard, connected.Body)
	must(err)
	must(connected.Body.Close())
	p.check(connected.StatusCode == 302 && strings.Contains(connected.Header.Get("Location"), "connection_connected=independent") && len(jar.Cookies(callbackURL)) == 0, "callback completes with the browser-managed cookie and clears it")
}

func connectionStartStorageJourney(p *probe, session auth.AuthSessionResponse) {
	// The first session read admits the JWT; the second is ConnectionStart's
	// independent browser-session check. Fail only that second production read.
	_, err := p.db.ExecContext(p.ctx, `
CREATE SEQUENCE scenery.authprobe_session_reads;
CREATE FUNCTION scenery.authprobe_session_expiry(value timestamptz) RETURNS timestamptz LANGUAGE plpgsql AS $$
BEGIN
 IF nextval('scenery.authprobe_session_reads') = 2 THEN RAISE EXCEPTION 'private start session storage failure'; END IF;
 RETURN value;
END $$;
ALTER TABLE scenery.scenery_auth_refresh_sessions RENAME TO authprobe_refresh_sessions;
CREATE VIEW scenery.scenery_auth_refresh_sessions AS SELECT id,user_id,token_hash,previous_token_hash,previous_token_expires_at,active_tenant_id,scenery.authprobe_session_expiry(expires_at) AS expires_at,rotated_at,revoked_at,revoked_reason,user_agent,ip_hash,actor_user_id,impersonation_id,impersonation_reason,created_at,updated_at FROM scenery.authprobe_refresh_sessions`)
	must(err)
	restore := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := p.db.ExecContext(ctx, `DROP VIEW IF EXISTS scenery.scenery_auth_refresh_sessions; ALTER TABLE scenery.authprobe_refresh_sessions RENAME TO scenery_auth_refresh_sessions; DROP FUNCTION scenery.authprobe_session_expiry(timestamptz); DROP SEQUENCE scenery.authprobe_session_reads`)
		must(err)
	}
	restored := false
	defer func() {
		if !restored {
			restore()
		}
	}()
	failed, body := p.request(http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}}, session.Token, "")
	p.check(failed.StatusCode == 503 && strings.Contains(string(body), `"code":"connection_unavailable"`) && !strings.Contains(string(body), "private start session storage failure") && len(failed.Cookies()) == 0, "an admitted user's session storage failure returns sanitized retryable 503 instead of unauthenticated")
	restore()
	restored = true
	recovered, _ := p.request(http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}}, session.Token, "")
	p.check(recovered.StatusCode == 200, "the same authenticated session can retry start after storage recovery")
}
