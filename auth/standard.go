package auth

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/runtime"
)

//go:embed db/gen/schema.sql
var standardAuthSchema embed.FS

type StandardConfig struct {
	Enabled               bool
	GoogleOAuth           GoogleOAuthConfig
	DevBootstrap          DevBootstrapConfig
	AutoBootstrapDatabase bool
}

type GoogleOAuthConfig struct {
	Enabled       bool
	AllowedScopes []string
}

type DevBootstrapConfig struct {
	Enabled          bool
	DefaultUserEmail string
	DefaultUserID    string
	DefaultTenantID  string
	TokenTTL         time.Duration
}

type EmailMessage struct {
	From    string
	To      []string
	Subject string
	Text    string
}

type EmailSender func(context.Context, EmailMessage) error

var defaultEmailSender EmailSender = func(context.Context, EmailMessage) error { return nil }

func SetEmailSender(sender EmailSender) {
	if sender == nil {
		defaultEmailSender = func(context.Context, EmailMessage) error { return nil }
		return
	}
	defaultEmailSender = sender
}

var standardAuthState struct {
	mu   sync.Mutex
	cfg  StandardConfig
	svc  *Service
	once sync.Once
	err  error
}

func currentStandardConfig() StandardConfig {
	standardAuthState.mu.Lock()
	defer standardAuthState.mu.Unlock()
	return standardAuthState.cfg
}

func RegisterStandard(config StandardConfig) error {
	if !config.Enabled {
		return nil
	}
	if err := runtime.LoadDotEnvIntoEnv(); err != nil {
		return err
	}
	config = normalizeStandardConfig(config)
	applyStandardSecrets(config)

	standardAuthState.mu.Lock()
	standardAuthState.cfg = config
	standardAuthState.mu.Unlock()

	runtime.RegisterAuthHandler(&runtime.AuthHandler{
		Service: "auth",
		Name:    "AuthHandler",
		Authenticate: func(ctx context.Context, token string) (runtime.AuthInfo, error) {
			uid, data, err := AuthHandler(ctx, token)
			if err != nil {
				return runtime.AuthInfo{}, err
			}
			return runtime.AuthInfo{UID: string(uid), Data: data}, nil
		},
	})
	registerStandardAuthEndpoints(config)
	return nil
}

func normalizeStandardConfig(config StandardConfig) StandardConfig {
	if strings.TrimSpace(config.DevBootstrap.DefaultUserID) == "" {
		config.DevBootstrap.DefaultUserID = "dev-user"
	}
	if strings.TrimSpace(config.DevBootstrap.DefaultTenantID) == "" {
		config.DevBootstrap.DefaultTenantID = "00000000-0000-0000-0000-000000000001"
	}
	if config.DevBootstrap.TokenTTL == 0 {
		config.DevBootstrap.TokenTTL = 24 * time.Hour
	}
	return config
}

func applyStandardSecrets(config StandardConfig) {
	secrets.JWTSecret = strings.TrimSpace(envpolicy.Get("JWT_SECRET"))
	if strings.TrimSpace(secrets.JWTSecret) == "" && isLocalRuntime() {
		secrets.JWTSecret = "scenery-local-development-secret"
	}
	secrets.GoogleOAuthClientID = strings.TrimSpace(envpolicy.Get("GOOGLE_OAUTH_CLIENT_ID"))
	secrets.GoogleOAuthClientSecret = strings.TrimSpace(envpolicy.Get("GOOGLE_OAUTH_CLIENT_SECRET"))
	secrets.PublicAppURL = strings.TrimSpace(envpolicy.Get("SCENERY_PUBLIC_APP_URL"))
	secrets.APIBaseURL = strings.TrimSpace(envpolicy.Get("SCENERY_API_BASE_URL"))
	secrets.AuthCookieDomain = strings.TrimSpace(envpolicy.Get("AUTH_COOKIE_DOMAIN"))
	secrets.AuthEmailFrom = strings.TrimSpace(envpolicy.Get("AUTH_EMAIL_FROM"))
}

func standardAuthService(ctx context.Context) (*Service, error) {
	standardAuthState.mu.Lock()
	defer standardAuthState.mu.Unlock()
	standardAuthState.once.Do(func() {
		cfg := standardAuthState.cfg
		databaseURL := strings.TrimSpace(envpolicy.Get("DATABASE_URL"))
		if strings.TrimSpace(databaseURL) == "" {
			standardAuthState.err = fmt.Errorf("standard auth database URL is not configured (DATABASE_URL)")
			return
		}
		authURL, err := postgresdb.ServiceURL(databaseURL, "scenery")
		if err != nil {
			standardAuthState.err = fmt.Errorf("standard auth database URL must be postgres:// or postgresql://: %w", err)
			return
		}
		pool, err := postgresdb.Open(ctx, authURL)
		if err != nil {
			standardAuthState.err = fmt.Errorf("connect standard auth database: %w", err)
			return
		}
		if cfg.AutoBootstrapDatabase {
			if err := bootstrapStandardAuthSchema(ctx, pool); err != nil {
				pool.Close()
				standardAuthState.err = clarifyStandardAuthTenantError(err)
				return
			}
		}
		standardAuthState.svc = &Service{db: pool, query: authdb.New(pool), now: time.Now}
		runtime.MarkServiceInitialized("auth", func(context.Context) {
			pool.Close()
		})
	})
	return standardAuthState.svc, standardAuthState.err
}

func registerStandardAuthEndpoints(config StandardConfig) {
	registerStandardJSON("auth", "SignupEmail", runtime.Public, "/auth/signup/email", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *EmailSignupParams) (*EmailSignupResponse, error) {
		return svc.SignupEmail(ctx, input)
	})
	registerStandardJSON("auth", "ConfirmEmailVerification", runtime.Public, "/auth/email-verification/confirm", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *EmailVerificationConfirmParams) (*AuthSessionResponse, error) {
		return svc.ConfirmEmailVerification(ctx, input)
	})
	registerStandardJSON("auth", "ResendEmailVerification", runtime.Public, "/auth/email-verification/resend", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *EmailVerificationResendParams) (*EmailVerificationResendResponse, error) {
		return svc.ResendEmailVerification(ctx, input)
	})
	registerStandardJSON("auth", "LoginEmail", runtime.Public, "/auth/login/email", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *EmailLoginParams) (*AuthSessionResponse, error) {
		return svc.LoginEmail(ctx, input)
	})
	registerStandardCookie("auth", "Refresh", runtime.Public, "/auth/refresh", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *RefreshParams) (*AuthSessionResponse, error) {
		return svc.Refresh(ctx, input)
	})
	registerStandardCookie("auth", "Logout", runtime.Public, "/auth/logout", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *RefreshParams) (*LogoutResponse, error) {
		return svc.Logout(ctx, input)
	})
	registerStandardEmpty("auth", "Me", runtime.Auth, "/auth/me", http.MethodGet, func(ctx context.Context, svc *Service, _ []string) (*AuthBootstrapResponse, error) {
		return svc.Me(ctx)
	})
	registerStandardJSON("auth", "RequestPasswordReset", runtime.Public, "/auth/password-reset/request", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *PasswordResetRequestParams) (*PasswordResetRequestResponse, error) {
		return svc.RequestPasswordReset(ctx, input)
	})
	registerStandardJSON("auth", "ConfirmPasswordReset", runtime.Public, "/auth/password-reset/confirm", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *PasswordResetConfirmParams) (*AuthSessionResponse, error) {
		return svc.ConfirmPasswordReset(ctx, input)
	})
	registerStandardOrganizations()
	registerStandardImpersonation()
	registerStandardJSONNoService("users", "DevBootstrap", runtime.Public, "/users/dev-bootstrap", http.MethodPost, func(ctx context.Context, _ []string, input *DevBootstrapParams) (*AuthResponse, error) {
		return DevBootstrap(ctx, input)
	})
	if config.GoogleOAuth.Enabled {
		registerStandardJSON("auth", "GoogleConnectStart", runtime.Auth, "/auth/google/connect/start", http.MethodPost, func(ctx context.Context, svc *Service, _ []string, input *GoogleConnectStartParams) (*GoogleConnectStartResponse, error) {
			return svc.GoogleConnectStart(ctx, input)
		})
		registerStandardEmpty("auth", "GetGoogleConnection", runtime.Auth, "/auth/google/connection", http.MethodGet, func(ctx context.Context, svc *Service, _ []string) (*GoogleConnectionResponse, error) {
			return svc.GetGoogleConnection(ctx)
		})
		registerStandardEmpty("auth", "DisconnectGoogleConnection", runtime.Auth, "/auth/google/connection/disconnect", http.MethodPost, func(ctx context.Context, svc *Service, _ []string) (*GoogleConnectionResponse, error) {
			return svc.DisconnectGoogleConnection(ctx)
		})
		runtime.RegisterEndpoint(&runtime.Endpoint{
			Service:    "auth",
			Name:       "GoogleStart",
			Access:     runtime.Public,
			Raw:        true,
			Path:       "/auth/google/start",
			Methods:    []string{http.MethodGet},
			RawHandler: GoogleStart,
		})
		runtime.RegisterEndpoint(&runtime.Endpoint{
			Service:    "auth",
			Name:       "GoogleCallback",
			Access:     runtime.Public,
			Raw:        true,
			Path:       "/auth/google/callback",
			Methods:    []string{http.MethodGet},
			RawHandler: GoogleCallback,
		})
	}
}

func registerStandardJSON[I, O any](service, name string, access runtime.Access, path, method string, invoke func(context.Context, *Service, []string, *I) (*O, error)) {
	registerStandardContract(service, name, access, path, method,
		func(request *http.Request) (any, error) {
			input, err := runtime.DecodeContractJSON[I](request)
			return &input, err
		},
		func(ctx context.Context, svc *Service, path []string, input any) (any, error) {
			return invoke(ctx, svc, path, input.(*I))
		},
	)
}

func registerStandardCookie[O any](service, name string, access runtime.Access, path, method string, invoke func(context.Context, *Service, []string, *RefreshParams) (*O, error)) {
	registerStandardContract(service, name, access, path, method,
		func(request *http.Request) (any, error) {
			return &RefreshParams{RefreshToken: resolveRefreshToken(nil, request.Header)}, nil
		},
		func(ctx context.Context, svc *Service, path []string, input any) (any, error) {
			return invoke(ctx, svc, path, input.(*RefreshParams))
		},
	)
}

func resolveRefreshToken(params *RefreshParams, headers http.Header) string {
	if params != nil && strings.TrimSpace(params.RefreshToken) != "" {
		return strings.TrimSpace(params.RefreshToken)
	}
	for _, name := range refreshCookieReadOrder {
		if value, present := refreshCookieValue(headers, name); present {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func refreshCookieValue(headers http.Header, name string) (string, bool) {
	if headers == nil {
		return "", false
	}
	request := http.Request{Header: headers}
	if cookie, err := request.Cookie(name); err == nil {
		return cookie.Value, true
	}
	return "", cookieNamePresent(headers, name)
}

func cookieNamePresent(headers http.Header, name string) bool {
	for _, line := range headers.Values("Cookie") {
		for part := range strings.SplitSeq(line, ";") {
			candidate, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			if strings.TrimSpace(candidate) == name {
				return true
			}
		}
	}
	return false
}

func registerStandardEmpty[O any](service, name string, access runtime.Access, path, method string, invoke func(context.Context, *Service, []string) (*O, error)) {
	registerStandardContract(service, name, access, path, method, func(*http.Request) (any, error) { return nil, nil }, func(ctx context.Context, svc *Service, path []string, _ any) (any, error) {
		return invoke(ctx, svc, path)
	})
}

func registerStandardJSONNoService[I, O any](service, name string, access runtime.Access, path, method string, invoke func(context.Context, []string, *I) (*O, error)) {
	registerStandardContractWithInvoke(service, name, access, path, method,
		func(request *http.Request) (any, error) {
			input, err := runtime.DecodeContractJSON[I](request)
			return &input, err
		},
		func(ctx context.Context, path []string, input any) (any, error) { return invoke(ctx, path, input.(*I)) },
	)
}

func registerStandardContract(service, name string, access runtime.Access, path, method string, decode func(*http.Request) (any, error), invoke func(context.Context, *Service, []string, any) (any, error)) {
	registerStandardContractWithInvoke(service, name, access, path, method, decode, func(ctx context.Context, path []string, input any) (any, error) {
		svc, err := standardAuthService(ctx)
		if err != nil {
			return nil, clarifyStandardAuthTenantError(err)
		}
		out, err := invoke(ctx, svc, path, input)
		return out, clarifyStandardAuthTenantError(err)
	})
}

func registerStandardContractWithInvoke(service, name string, access runtime.Access, path, method string, decode func(*http.Request) (any, error), invoke func(context.Context, []string, any) (any, error)) {
	pathNames := standardPathNames(path)
	runtime.RegisterEndpoint(&runtime.Endpoint{
		Service: service, Name: name, Access: access, Path: path, Methods: []string{method},
		DecodeContractRequest: func(request *http.Request, values map[string]string) (runtime.ContractDecodedRequest, error) {
			input, err := decode(request)
			path := make([]any, len(pathNames))
			for i, name := range pathNames {
				path[i] = values[name]
			}
			return runtime.ContractDecodedRequest{Payload: input, PathArgs: path}, err
		},
		Invoke: func(ctx context.Context, path []any, input any) (any, error) {
			values := make([]string, len(path))
			for i := range path {
				values[i], _ = path[i].(string)
			}
			return invoke(ctx, values, input)
		},
		EncodeContractOutcome: encodeStandardContractOutcome,
	})
}

func encodeStandardContractOutcome(_ *http.Request, outcome any) (runtime.ContractHTTPResponse, error) {
	response, err := runtime.EncodeContractJSON(http.StatusOK, outcome)
	if err != nil {
		return response, err
	}
	switch value := outcome.(type) {
	case *AuthSessionResponse:
		response.Headers.Add("Set-Cookie", value.SetCookie)
	case *LogoutResponse:
		response.Headers.Add("Set-Cookie", value.SetCookie)
		if value.legacySetCookie != "" {
			response.Headers.Add("Set-Cookie", value.legacySetCookie)
		}
	}
	return response, nil
}

func standardPathNames(path string) []string {
	var names []string
	for part := range strings.SplitSeq(path, "/") {
		if name := strings.TrimPrefix(part, ":"); name != part && name != "" {
			names = append(names, name)
		}
	}
	return names
}

func bootstrapStandardAuthSchema(ctx context.Context, pool *sql.DB) error {
	data, err := standardAuthSchema.ReadFile("db/gen/schema.sql")
	if err != nil {
		return fmt.Errorf("read standard auth schema: %w", err)
	}
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin standard auth schema bootstrap: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('scenery.standard.auth', 0))`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("lock standard auth schema bootstrap: %w", err)
	}
	for _, statement := range splitSQLStatements(string(data)) {
		if strings.TrimSpace(statement) == "" || strings.HasPrefix(strings.TrimSpace(statement), "--") {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			return clarifyStandardAuthTenantError(fmt.Errorf("bootstrap standard auth schema: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit standard auth schema bootstrap: %w", err)
	}
	return nil
}

func clarifyStandardAuthTenantError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "scenery_auth_tenants") || strings.Contains(message, "scenery_auth_tenant") {
		return fmt.Errorf("%w (standard auth owns framework tenant state in Postgres table scenery.scenery_auth_tenants; this is standard auth state, not an app-local tenants service)", err)
	}
	return err
}

func splitSQLStatements(sql string) []string {
	parts := strings.Split(sql, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		lines := strings.Split(part, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			kept = append(kept, line)
		}
		statements = append(statements, strings.TrimSpace(strings.Join(kept, "\n")))
	}
	return statements
}
