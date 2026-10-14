package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

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
	connectIndependent(p, session)
	row := take(p.q.GetConnectionByUser(p.ctx, authdb.GetConnectionByUserParams{UserID: id(session.User.ID), ProviderID: "independent"}))
	p.check(!row.AccessTokenExpiresAt.Valid && len(row.RefreshTokenCiphertext) == 0, "optional expiry and refresh are absent without synthetic values")
	p.check(take(auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")) == "independent-access", "fresh unknown-expiry access token is issued once")
	_, err := auth.ConnectionTokenForUser(p.ctx, session.User.ID, "independent", "files.read")
	p.check(errs.Code(err) == errs.ConnectionReauthRequired && p.independent.refreshCalls.Load() == 0, "consumed non-refreshable grant requires reauthorization")
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
}
