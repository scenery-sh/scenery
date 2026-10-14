package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"scenery.sh/auth"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
	"scenery.sh/runtime"
)

type connectionAdmissionResult struct {
	Code   errs.ErrCode `json:"code"`
	Issued bool         `json:"issued"`
}

func registerConnectionAuthorityEndpoints() {
	for _, access := range []runtime.Access{runtime.Auth, runtime.Public} {
		access := access
		runtime.RegisterEndpoint(&runtime.Endpoint{
			Service: "probe", Name: "ConnectionAuthority_" + string(access), Access: access, Raw: true,
			Path: "/probe/connection-authority/" + string(access), Methods: []string{http.MethodGet},
			RawHandler: func(w http.ResponseWriter, req *http.Request) {
				ctx := req.Context()
				if req.URL.Query().Get("context") == "background" {
					ctx = context.Background()
				}
				owner := req.URL.Query().Get("owner")
				if req.URL.Query().Get("context") == "forged" {
					ctx = auth.WithContext(context.Background(), auth.UID(owner), &auth.AuthData{UserID: auth.AuthUserID(owner)})
				}
				result := make(chan connectionAdmissionResult, 1)
				go func() {
					token, err := auth.ConnectionTokenForUser(ctx, owner, "independent", "files.read")
					result <- connectionAdmissionResult{Code: errs.Code(err), Issued: token == "independent-access"}
				}()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(<-result)
			},
		})
	}
}

func connectionAuthorityJourney(p *probe) {
	session, _ := p.signin()
	connectIndependent(p, session)
	for _, scenario := range []struct {
		access, mode, owner string
		issued              bool
	}{
		{"auth", "inherited", session.User.ID, true},
		{"auth", "inherited", uuid.NewString(), false},
		{"auth", "background", uuid.NewString(), false},
		{"auth", "forged", uuid.NewString(), false},
		{"public", "inherited", session.User.ID, false},
		{"public", "background", session.User.ID, false},
	} {
		path := "/probe/connection-authority/" + scenario.access + "?" + url.Values{"context": {scenario.mode}, "owner": {scenario.owner}}.Encode()
		_, body := p.request(http.MethodGet, path, nil, session.Token, "")
		var result connectionAdmissionResult
		must(json.Unmarshal(body, &result))
		p.check(result.Issued == scenario.issued && (scenario.issued && result.Code == errs.OK || !scenario.issued && result.Code == errs.PermissionDenied), "helper goroutine enforces "+scenario.access+"/"+scenario.mode+" invocation authority")
	}
	p.check(take(auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")) == "independent-access", "real queued worker admission allows explicit owner selection")
	must(p.workloads.bus.Publish(p.ctx, runtime.ContractEventMessage{ID: "connection-event", BusAddress: "probe/event_bus/connections", Channel: "connections", ContractAddress: "probe/event/connection", ContractVersion: 1, Payload: []byte(session.User.ID)}))
	select {
	case result := <-p.workloads.events:
		p.check(result.Issued && result.Code == errs.OK, "admitted event workload identity selects the authorized standard-auth owner")
	case <-p.ctx.Done():
		must(p.ctx.Err())
	}
	p.workloads.owners <- session.User.ID
	select {
	case result := <-p.workloads.schedules:
		p.check(result.Issued && result.Code == errs.OK, "admitted declared schedule workload identity selects the authorized standard-auth owner")
	case <-p.ctx.Done():
		must(p.ctx.Err())
	}
}

func connectIndependent(p *probe, session auth.AuthSessionResponse) {
	start, _ := call[auth.ConnectionStartResponse](p, http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}}, session.Token, "")
	response, _ := p.request(http.MethodGet, p.independent.authorize(start.AuthorizeURL), nil, "", "")
	p.check(response.StatusCode == 302 && strings.Contains(response.Header.Get("Location"), "connection_connected=independent"), "owned provider connected through the public callback")
}

func connectionNoExpiryJourney(p *probe) {
	p.independent.mu.Lock()
	p.independent.noExpiry = true
	p.independent.mu.Unlock()
	session, _ := p.signin()
	for _, short := range []bool{false, true} {
		p.independent.mu.Lock()
		p.independent.shortExpiry = short
		p.independent.mu.Unlock()
		connectIndependent(p, session)
		row := take(p.q.GetConnectionByUser(p.ctx, authdb.GetConnectionByUserParams{UserID: id(session.User.ID), ProviderID: "independent"}))
		p.check(row.AccessTokenExpiresAt.Valid == short && len(row.RefreshTokenCiphertext) == 0, "provider-declared expiry is preserved with no optional refresh credential")
		p.check(take(auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")) == "independent-access", "fresh unknown- or short-expiry non-refreshable token can be issued")
		if short {
			_, err := p.db.ExecContext(p.ctx, `UPDATE scenery.scenery_auth_connections SET access_token_expires_at=now()-interval '1 second' WHERE user_id=$1 AND provider_id='independent'`, id(session.User.ID))
			must(err)
		}
		_, err := auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")
		p.check(errs.Code(err) == errs.ConnectionReauthRequired && p.independent.refreshCalls.Load() == 0, "consumed unknown-expiry or expired non-refreshable grant requires reauthorization")
	}
}

func connectionRepeatDisconnectJourney(p *probe) {
	session, _ := p.signin()
	for _, absent := range []bool{true, false} {
		if !absent {
			connectIndependent(p, session)
			_, _ = call[auth.ConnectionResponse](p, http.MethodPost, "/auth/connections/independent/disconnect", nil, session.Token, "")
		}
		start, _ := call[auth.ConnectionStartResponse](p, http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}}, session.Token, "")
		callback := p.independent.authorize(start.AuthorizeURL)
		status, _ := call[auth.ConnectionResponse](p, http.MethodPost, "/auth/connections/independent/disconnect", nil, session.Token, "")
		response, _ := p.request(http.MethodGet, callback, nil, "", "")
		p.check(status.Status == "disconnected" && response.StatusCode == 400, "successful disconnect invalidates a pending callback for an absent or already disconnected slot")
	}
	connectionInflightDisconnect(p, session)
}

// Hold the real provider exchange after consumption, then observe the competing
// HTTP disconnect waiting on PostgreSQL's shared owner/provider advisory lease.
func connectionInflightDisconnect(p *probe, session auth.AuthSessionResponse) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	p.independent.mu.Lock()
	p.independent.beforeExchange = func(req *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-req.Context().Done():
		}
	}
	p.independent.mu.Unlock()
	start, _ := call[auth.ConnectionStartResponse](p, http.MethodPost, "/auth/connections/independent/start", auth.ConnectionStartParams{Scopes: []string{"files.read"}}, session.Token, "")
	callback := p.independent.authorize(start.AuthorizeURL)
	request := take(http.NewRequestWithContext(p.ctx, http.MethodGet, "http://api.example.test"+callback, nil))
	for _, cookie := range p.browserCookies {
		request.AddCookie(cookie)
	}
	type result struct {
		response *http.Response
		err      error
	}
	invoke := func(req *http.Request) <-chan result {
		done := make(chan result, 1)
		go func() { response, err := p.client.Do(req); done <- result{response, err} }()
		return done
	}
	callbackDone := invoke(request)
	select {
	case <-entered:
	case <-p.ctx.Done():
		panic(failure{p.ctx.Err()})
	}
	var consumed bool
	must(p.db.QueryRowContext(p.ctx, `SELECT consumed_at IS NOT NULL FROM scenery.scenery_auth_connection_states WHERE user_id=$1 AND provider_id='independent' ORDER BY created_at DESC LIMIT 1`, id(session.User.ID)).Scan(&consumed))
	p.check(consumed, "callback consumes its state durably before the held remote exchange")
	replay := take(p.client.Do(request.Clone(p.ctx)))
	_, replayErr := io.Copy(io.Discard, replay.Body)
	must(replayErr)
	must(replay.Body.Close())
	p.check(replay.StatusCode == 400 && len(replay.Cookies()) == 0, "consumed callback cannot replay while its provider exchange is still in flight")
	disconnect := take(http.NewRequestWithContext(p.ctx, http.MethodPost, "http://api.example.test/auth/connections/independent/disconnect", nil))
	disconnect.Header.Set("Authorization", "Bearer "+session.Token)
	disconnectDone := invoke(disconnect)
	wait, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	for {
		var blocked bool
		must(p.db.QueryRowContext(wait, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`).Scan(&blocked))
		if blocked {
			break
		}
		select {
		case <-disconnectDone:
			p.check(false, "disconnect cannot finish ahead of the in-flight callback lease")
		case <-wait.Done():
			panic(failure{wait.Err()})
		case <-time.After(10 * time.Millisecond):
		}
	}
	p.check(true, "disconnect waits on the owner/provider lease held by the in-flight callback")
	close(release)
	callbackResult, disconnectResult := <-callbackDone, <-disconnectDone
	must(callbackResult.err)
	must(disconnectResult.err)
	_, err := io.Copy(io.Discard, callbackResult.response.Body)
	must(err)
	must(callbackResult.response.Body.Close())
	var status auth.ConnectionResponse
	must(json.NewDecoder(disconnectResult.response.Body).Decode(&status))
	must(disconnectResult.response.Body.Close())
	row := take(p.q.GetConnectionByUser(p.ctx, authdb.GetConnectionByUserParams{UserID: id(session.User.ID), ProviderID: "independent"}))
	p.check(callbackResult.response.StatusCode == 302 && disconnectResult.response.StatusCode == 200 && status.Status == "disconnected" && row.Status == "disconnected" && len(row.RefreshTokenCiphertext) == 0 && len(row.AccessTokenCiphertext) == 0, "completed disconnect wins over an already consumed callback and clears its credentials")
	_, tokenErr := auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")
	p.check(errs.Code(tokenErr) == errs.ConnectionReauthRequired, "late callback cannot restore worker token issuance after disconnect")
	p.independent.mu.Lock()
	p.independent.beforeExchange = nil
	p.independent.mu.Unlock()
	connectIndependent(p, session)
	p.check(true, "a new consent flow after disconnect can reconnect normally")
}
