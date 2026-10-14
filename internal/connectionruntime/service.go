// Package connectionruntime composes configured providers and the SQL vault
// behind the SDK host. It owns browser handshakes, never application sign-in.
package connectionruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/authprovider/google"
	"scenery.sh/internal/authprovider/oauth2"
	"scenery.sh/internal/authschema"
	"scenery.sh/internal/connectionapi"
	"scenery.sh/internal/connectionvault"
	"scenery.sh/internal/connectionvault/sqlstore"
	"scenery.sh/runtime/shared"
)

type Options struct {
	Host           appsdk.Host
	Providers      []connectionapi.ProviderConfig
	Open           func(context.Context) (*sql.DB, error)
	Principal      func(context.Context) (user, session string, err error)
	ApplicationURL func() string
	Client         *http.Client
	Now            func() time.Time
}
type configuredProvider struct {
	registration          connectionvault.Registration
	clientID, callbackURI string
}
type Service struct {
	options   Options
	vault     *connectionvault.Vault
	providers map[string]configuredProvider
	schema    schemaChecks
}

func coded(code errs.ErrCode, message string) error { return errs.B().Code(code).Msg(message).Err() }

func New(options Options) (*Service, error) {
	if err := connectionapi.ValidateProviders(options.Providers); err != nil {
		return nil, coded(errs.ConnectionConfiguration, "connection provider declarations are invalid")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	s := &Service{options: options, providers: map[string]configuredProvider{}}
	var registrations []connectionvault.Registration
	for _, definition := range options.Providers {
		prefix := "auth.connections." + definition.ID + "."
		clientID, _, err := options.Host.FrameworkConfigString(prefix + "client_id")
		if err != nil {
			return nil, coded(errs.ConnectionConfiguration, "connection client configuration is unavailable")
		}
		secret, _, err := options.Host.FrameworkConfigSecret(prefix + "client_secret")
		if err != nil {
			return nil, coded(errs.ConnectionConfiguration, "connection client configuration is unavailable")
		}
		config := oauth2.Config{Issuer: definition.Issuer, AuthorizeURL: definition.AuthorizeURL, TokenURL: definition.TokenURL, RevokeURL: definition.RevokeURL, JWKSURL: definition.JWKSURL, AccountURL: definition.AccountURL, SubjectField: definition.SubjectField, EmailField: definition.EmailField, ClientAuth: definition.ClientAuth, Resource: definition.Resource, OIDC: definition.Protocol == "oidc", ClientID: clientID, ClientSecret: string(secret)}
		if definition.Preset == "google" {
			config = google.Config(clientID, string(secret), definition.Resource)
			// Explicit trusted declaration overrides permit a provider proxy or
			// deterministic probe; never take endpoints from an HTTP request.
			if definition.AuthorizeURL != "" {
				config.AuthorizeURL = definition.AuthorizeURL
			}
			if definition.TokenURL != "" {
				config.TokenURL = definition.TokenURL
			}
			if definition.RevokeURL != "" {
				config.RevokeURL = definition.RevokeURL
			}
			if definition.JWKSURL != "" {
				config.JWKSURL = definition.JWKSURL
			}
		}
		callback, _, err := options.Host.FrameworkConfigString(prefix + "callback_uri")
		if err != nil {
			return nil, coded(errs.ConnectionConfiguration, "connection callback configuration is unavailable")
		}
		if callback != "" {
			if err := validateCallbackURI(callback, definition.ID); err != nil {
				return nil, err
			}
		}
		adapter := oauth2.New(config, options.Client, options.Now)
		r := connectionvault.Registration{ID: definition.ID, Issuer: config.Issuer, Resource: config.Resource, AllowedScopes: definition.AllowedScopes, Provider: adapter}
		s.providers[definition.ID] = configuredProvider{registration: r, clientID: clientID, callbackURI: callback}
		registrations = append(registrations, r)
	}
	store := sqlstore.Store{Open: s.open}
	cipher := connectionvault.TokenCipher{Key: s.cipherKey}
	v, err := connectionvault.New(store, cipher, registrations, options.Now)
	if err != nil {
		return nil, err
	}
	s.vault = v
	return s, nil
}

// Registration precedes runtime metadata initialization. Resolve an omitted
// callback only when handling requests, after the advertised API URL is known.
func (s *Service) provider(provider string) (configuredProvider, error) {
	cfg := s.providers[provider]
	if cfg.callbackURI == "" {
		cfg.callbackURI = strings.TrimRight(appsdk.Metadata().APIBaseURL, "/") + "/auth/connections/" + provider + "/callback"
	}
	return cfg, validateCallbackURI(cfg.callbackURI, provider)
}

func validateCallbackURI(callback, provider string) error {
	u, err := url.Parse(callback)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1")) || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/auth/connections/"+provider+"/callback") {
		return coded(errs.ConnectionConfiguration, "connection callback URI is invalid")
	}
	return nil
}

func (s *Service) open(ctx context.Context) (*sql.DB, error) {
	pool, err := s.options.Open(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.schema.ensure(ctx, pool, func() error { return authschema.CheckCurrentConnections(ctx, pool) }); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, coded(errs.ConnectionConfiguration, "connection schema requires explicit migration or recovery")
	}
	return pool, nil
}

func (s *Service) cipherKey() ([]byte, error) {
	value, _, err := s.options.Host.FrameworkConfigSecret("auth.token_cipher_key")
	if err != nil {
		return nil, err
	}
	if encoded := strings.TrimSpace(string(value)); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, coded(errs.ConnectionConfiguration, "auth.token_cipher_key must contain a base64-encoded 32-byte key")
		}
		return key, nil
	}
	if appsdk.Metadata().Environment.Cloud != shared.CloudLocal {
		return nil, coded(errs.ConnectionConfiguration, "auth.token_cipher_key is required")
	}
	jwt, _, err := s.options.Host.FrameworkConfigSecret("auth.jwt_secret")
	if err != nil {
		return nil, err
	}
	seed := strings.TrimSpace(string(jwt))
	if strings.TrimSpace(seed) == "" {
		seed = "scenery-local-development-secret"
	}
	// Historical key input is persisted-data compatibility, not a provider
	// decision. Changing this label requires an explicit re-encryption migration.
	digest := sha256.Sum256([]byte("scenery-google-token:" + seed))
	return digest[:], nil
}

func (s *Service) Token(ctx context.Context, user, provider string, scopes []string) (string, error) {
	return s.vault.Token(ctx, user, provider, scopes)
}
func hash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func random() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func dbID(value string) (authdb.UUID, error) {
	id, err := uuid.Parse(value)
	return authdb.UUID{Bytes: [16]byte(id), Valid: err == nil}, err
}
func idString(value authdb.UUID) string { return uuid.UUID(value.Bytes).String() }

func safePath(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(value, "\\\r\n") {
		return "/"
	}
	return u.String()
}

func browserCookie(provider, callback, value string, clear bool) *http.Cookie {
	u, _ := url.Parse(callback)
	name := "scenery_connection_" + hash(u.Host)[:16] + "_" + provider
	cookie := &http.Cookie{Name: name, Value: value, Path: u.Path, HttpOnly: true, Secure: u.Scheme == "https", SameSite: http.SameSiteLaxMode, MaxAge: 600}
	if clear {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(0, 0)
	}
	return cookie
}

func (s *Service) start(ctx context.Context, provider string, input *connectionapi.StartParams) (*connectionapi.StartResponse, error) {
	if input == nil {
		return nil, coded(errs.InvalidArgument, "connection request is required")
	}
	r, scopes, err := s.vault.Registration(provider, input.Scopes)
	if err != nil {
		return nil, err
	}
	cfg, err := s.provider(provider)
	if err != nil {
		return nil, err
	}
	user, session, err := s.options.Principal(ctx)
	if err != nil {
		return nil, err
	}
	userID, err := dbID(user)
	if err != nil {
		return nil, coded(errs.Unauthenticated, "connection owner is invalid")
	}
	sessionID, err := dbID(session)
	if err != nil {
		return nil, coded(errs.Unauthenticated, "connection requires a standard-auth browser session")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := s.open(ctx)
	if err != nil {
		return nil, coded(errs.ConnectionUnavailable, "connection storage is unavailable")
	}
	q := authdb.New(pool)
	refresh, err := q.GetRefreshSessionByID(ctx, sessionID)
	if err != nil || refresh.UserID != userID || refresh.RevokedAt.Valid || !s.options.Now().Before(refresh.ExpiresAt) {
		return nil, coded(errs.Unauthenticated, "connection browser session is unavailable")
	}
	owner, err := q.GetUserByID(ctx, userID)
	if err != nil || owner.DisabledAt.Valid {
		return nil, coded(errs.PermissionDenied, "connection owner is unavailable")
	}
	consent := true
	row, err := q.GetConnectionByUser(ctx, authdb.GetConnectionByUserParams{UserID: userID, ProviderID: provider})
	if err == nil {
		granted, parseErr := connectionvault.ParseScopes(row.Scopes)
		consent = parseErr != nil || row.Status != connectionvault.Active || len(row.RefreshTokenCiphertext) == 0 || !connectionvault.ContainsScopes(granted, scopes)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, coded(errs.ConnectionUnavailable, "connection storage is unavailable")
	}
	state, err := random()
	if err != nil {
		return nil, err
	}
	verifier, err := random()
	if err != nil {
		return nil, err
	}
	nonce, err := random()
	if err != nil {
		return nil, err
	}
	browser, err := random()
	if err != nil {
		return nil, err
	}
	authorize, err := r.Provider.Authorize(connectionvault.Authorization{State: state, Verifier: verifier, Nonce: nonce, CallbackURI: cfg.callbackURI, Scopes: scopes, Consent: consent})
	if err != nil {
		return nil, coded(errs.ConnectionConfiguration, "connection provider configuration is invalid")
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	if err := q.DeleteExpiredConnectionStates(ctx); err != nil {
		return nil, coded(errs.ConnectionUnavailable, "connection storage is unavailable")
	}
	err = (sqlstore.Store{Open: s.open}).CreateState(ctx, authdb.CreateConnectionStateParams{ID: authdb.UUID{Bytes: [16]byte(id), Valid: true}, StateHash: hash(state), UserID: userID, SessionID: sessionID, ProviderID: provider, Issuer: r.Issuer, Resource: r.Resource, ClientID: cfg.clientID, BrowserHash: hash(browser), CallbackUri: cfg.callbackURI, RequestedScopes: strings.Join(scopes, " "), PkceVerifier: verifier, NonceHash: hash(nonce), RedirectPath: safePath(input.RedirectPath), ExpiresAt: s.options.Now().Add(10 * time.Minute)})
	if err != nil {
		return nil, coded(errs.ConnectionUnavailable, "connection authorization state could not be stored")
	}
	return &connectionapi.StartResponse{AuthorizeURL: authorize, SetCookie: browserCookie(provider, cfg.callbackURI, browser, false).String()}, nil
}
