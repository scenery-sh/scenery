package connectionruntime

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/connectionapi"
	"scenery.sh/internal/connectionvault"
	"scenery.sh/internal/connectionvault/sqlstore"
	"scenery.sh/internal/runtimeapi"
)

// Register installs one provider-neutral administration family and distinct
// callback URIs for the configured issuers. No endpoint returns credentials.
func (s *Service) Register() {
	if len(s.providers) == 0 {
		return
	}
	host := s.options.Host
	register := func(name, suffix, method string, body bool, invoke func(context.Context, string, any) (any, error)) {
		host.RegisterEndpoint(appsdk.Endpoint{Service: "auth", Name: name, Access: runtimeapi.Auth, Path: "/auth/connections/:provider" + suffix, Methods: []string{method},
			Decode: func(req *http.Request, values map[string]string) (any, []any, error) {
				var payload any
				if body {
					if _, configured := s.providers[values["provider"]]; configured {
						cfg, err := s.provider(values["provider"])
						if err != nil {
							return nil, nil, err
						}
						callback, _ := url.Parse(cfg.callbackURI)
						// A host-only binding cookie cannot follow a callback to
						// another hostname. Forwarded headers cannot grant it scope.
						if !strings.EqualFold((&url.URL{Host: req.Host}).Hostname(), callback.Hostname()) {
							return nil, nil, coded(errs.InvalidArgument, "connection start must use the configured callback hostname")
						}
					}
					input := &connectionapi.StartParams{}
					if err := host.DecodeJSON(req, input); err != nil {
						return nil, nil, err
					}
					payload = input
				}
				return payload, []any{values["provider"]}, nil
			},
			Invoke: func(ctx context.Context, path []any, payload any) (any, error) {
				provider, _ := path[0].(string)
				return invoke(ctx, provider, payload)
			},
			Encode: func(_ *http.Request, outcome any) (appsdk.Response, error) {
				result, err := host.EncodeJSON(http.StatusOK, outcome)
				if err != nil {
					return result, err
				}
				if start, ok := outcome.(*connectionapi.StartResponse); ok && start.SetCookie != "" {
					if result.Headers == nil {
						result.Headers = http.Header{}
					}
					result.Headers.Add("Set-Cookie", start.SetCookie)
				}
				return result, nil
			},
		})
	}
	register("ConnectionStart", "/start", http.MethodPost, true, func(ctx context.Context, provider string, payload any) (any, error) {
		return s.start(ctx, provider, payload.(*connectionapi.StartParams))
	})
	register("ConnectionStatus", "", http.MethodGet, false, func(ctx context.Context, provider string, _ any) (any, error) {
		user, _, err := s.options.Principal(ctx)
		if err != nil {
			return nil, err
		}
		return s.vault.Status(ctx, user, provider)
	})
	register("ConnectionDisconnect", "/disconnect", http.MethodPost, false, func(ctx context.Context, provider string, _ any) (any, error) {
		user, _, err := s.options.Principal(ctx)
		if err != nil {
			return nil, err
		}
		return s.vault.Disconnect(ctx, user, provider)
	})
	for provider := range s.providers {
		host.RegisterEndpoint(appsdk.Endpoint{Service: "auth", Name: "ConnectionCallback_" + provider, Access: runtimeapi.Public, Path: "/auth/connections/" + provider + "/callback", Methods: []string{http.MethodGet}, RawHandler: func(w http.ResponseWriter, req *http.Request) { s.callback(w, req, provider) }})
	}
}

func (s *Service) callback(w http.ResponseWriter, req *http.Request, provider string) {
	cfg, err := s.provider(provider)
	if err != nil {
		errs.HTTPError(w, err)
		return
	}
	clear := browserCookie(provider, cfg.callbackURI, "", true)
	cookie, err := req.Cookie(clear.Name)
	if err != nil || cookie.Value == "" || req.URL.Query().Get("state") == "" {
		errs.HTTPError(w, coded(errs.InvalidArgument, "connection authorization state is invalid"))
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	if issuer := req.URL.Query().Get("iss"); issuer != "" && issuer != cfg.registration.Issuer {
		errs.HTTPError(w, coded(errs.InvalidArgument, "connection issuer does not match"))
		return
	}
	state, lease, err := (sqlstore.Store{Open: s.open}).LockState(ctx, authdb.ConsumeConnectionStateParams{StateHash: hash(req.URL.Query().Get("state")), ProviderID: provider, BrowserHash: hash(cookie.Value), Issuer: cfg.registration.Issuer, Resource: cfg.registration.Resource, ClientID: cfg.clientID, CallbackUri: cfg.callbackURI})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, connectionvault.ErrOwnerUnavailable) {
			errs.HTTPError(w, coded(errs.InvalidArgument, "connection authorization state is invalid or expired"))
		} else {
			errs.HTTPError(w, coded(errs.ConnectionUnavailable, "connection storage is unavailable"))
		}
		return
	}
	defer func() { _ = lease.Close() }()
	http.SetCookie(w, clear)
	if req.URL.Query().Get("error") != "" {
		s.redirect(w, req, state.RedirectPath, "error", "connection_authorization_denied")
		return
	}
	scopes, err := connectionvault.ParseScopes(state.RequestedScopes)
	if err != nil {
		s.redirect(w, req, state.RedirectPath, "error", "connection_configuration")
		return
	}
	grant, err := cfg.registration.Provider.Exchange(ctx, connectionvault.CodeExchange{Code: req.URL.Query().Get("code"), Verifier: state.PkceVerifier, NonceHash: state.NonceHash, CallbackURI: state.CallbackUri, Scopes: scopes})
	if err != nil {
		s.redirect(w, req, state.RedirectPath, "error", "connection_exchange_failed")
		return
	}
	if err := s.vault.Connect(ctx, lease, idString(state.UserID), provider, scopes, grant); err != nil {
		s.redirect(w, req, state.RedirectPath, "error", string(errs.Code(err)))
		return
	}
	s.redirect(w, req, state.RedirectPath, "connection_connected", provider)
}

func (s *Service) redirect(w http.ResponseWriter, req *http.Request, path, key, value string) {
	u, _ := url.Parse(safePath(path))
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	base := ""
	if s.options.ApplicationURL != nil {
		base = s.options.ApplicationURL()
	}
	if base == "" {
		base = appsdk.Metadata().APIBaseURL
	}
	http.Redirect(w, req, strings.TrimRight(base, "/")+u.String(), http.StatusFound)
}
