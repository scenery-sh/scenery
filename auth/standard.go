package auth

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/internal/envpolicy"
	scenerypgxpool "scenery.sh/pgxpool"
	"scenery.sh/runtime"
)

//go:embed db/gen/schema.sql
var standardAuthSchema embed.FS

type StandardConfig struct {
	Enabled               bool
	DatabaseURLEnv        string
	JWTSecretEnv          string
	RefreshCookieName     string
	AuthCookieDomainEnv   string
	PublicAppURLEnv       string
	APIBaseURLEnv         string
	EmailFromEnv          string
	GoogleOAuth           GoogleOAuthConfig
	DevBootstrap          DevBootstrapConfig
	AutoBootstrapDatabase bool
}

type GoogleOAuthConfig struct {
	Enabled         bool
	ClientIDEnv     string
	ClientSecretEnv string
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
		Service:      "auth",
		Name:         "AuthHandler",
		ParamType:    reflect.TypeOf(""),
		AuthDataType: reflect.TypeOf((*AuthData)(nil)),
		Authenticate: func(ctx context.Context, params any) (runtime.AuthInfo, error) {
			token, _ := params.(string)
			uid, data, err := AuthHandler(ctx, token)
			if err != nil {
				return runtime.AuthInfo{}, err
			}
			return runtime.AuthInfo{UID: string(uid), Data: data}, nil
		},
	})
	registerStandardAuthEndpoints()
	return nil
}

func normalizeStandardConfig(config StandardConfig) StandardConfig {
	if strings.TrimSpace(config.DatabaseURLEnv) == "" {
		config.DatabaseURLEnv = "DatabaseURL"
	}
	if strings.TrimSpace(config.JWTSecretEnv) == "" {
		config.JWTSecretEnv = "JWTSecret"
	}
	if strings.TrimSpace(config.RefreshCookieName) == "" {
		config.RefreshCookieName = "onlv_refresh"
	}
	if strings.TrimSpace(config.AuthCookieDomainEnv) == "" {
		config.AuthCookieDomainEnv = "AuthCookieDomain"
	}
	if strings.TrimSpace(config.PublicAppURLEnv) == "" {
		config.PublicAppURLEnv = "PublicAppURL"
	}
	if strings.TrimSpace(config.APIBaseURLEnv) == "" {
		config.APIBaseURLEnv = "APIBaseURL"
	}
	if strings.TrimSpace(config.EmailFromEnv) == "" {
		config.EmailFromEnv = "AuthEmailFrom"
	}
	if strings.TrimSpace(config.GoogleOAuth.ClientIDEnv) == "" {
		config.GoogleOAuth.ClientIDEnv = "GoogleOAuthClientID"
	}
	if strings.TrimSpace(config.GoogleOAuth.ClientSecretEnv) == "" {
		config.GoogleOAuth.ClientSecretEnv = "GoogleOAuthClientSecret"
	}
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
	refreshCookieName = strings.TrimSpace(config.RefreshCookieName)
	secrets.JWTSecret = firstEnv(config.JWTSecretEnv, "JWT_SECRET", "SCENERY_AUTH_JWT_SECRET")
	if strings.TrimSpace(secrets.JWTSecret) == "" && isLocalRuntime() {
		secrets.JWTSecret = "scenery-local-development-secret"
	}
	secrets.GoogleOAuthClientID = firstEnv(config.GoogleOAuth.ClientIDEnv, "GOOGLE_OAUTH_CLIENT_ID")
	secrets.GoogleOAuthClientSecret = firstEnv(config.GoogleOAuth.ClientSecretEnv, "GOOGLE_OAUTH_CLIENT_SECRET")
	secrets.PublicAppURL = firstEnv(config.PublicAppURLEnv, "PUBLIC_APP_URL", "SCENERY_PUBLIC_APP_URL")
	secrets.APIBaseURL = firstEnv(config.APIBaseURLEnv, "API_BASE_URL", "SCENERY_API_BASE_URL")
	secrets.AuthCookieDomain = firstEnv(config.AuthCookieDomainEnv, "AUTH_COOKIE_DOMAIN", "SCENERY_AUTH_COOKIE_DOMAIN")
	secrets.AuthEmailFrom = firstEnv(config.EmailFromEnv, "AUTH_EMAIL_FROM", "SCENERY_AUTH_EMAIL_FROM")
}

func firstEnv(names ...string) string {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if value := strings.TrimSpace(envpolicy.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func standardAuthService(ctx context.Context) (*Service, error) {
	standardAuthState.mu.Lock()
	defer standardAuthState.mu.Unlock()
	standardAuthState.once.Do(func() {
		cfg := standardAuthState.cfg
		databaseURL := firstEnv(cfg.DatabaseURLEnv, "DATABASE_URL", "SCENERY_AUTH_DATABASE_URL")
		if strings.TrimSpace(databaseURL) == "" {
			standardAuthState.err = fmt.Errorf("standard auth database URL is not configured (%s)", cfg.DatabaseURLEnv)
			return
		}
		pool, err := scenerypgxpool.New(ctx, databaseURL)
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

func registerStandardAuthEndpoints() {
	registerStandardTyped("auth", "SignupEmail", runtime.Public, "/auth/signup/email", []string{http.MethodPost}, (*EmailSignupParams)(nil), (*EmailSignupResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.SignupEmail(ctx, payload.(*EmailSignupParams))
	})
	registerStandardTyped("auth", "ConfirmEmailVerification", runtime.Public, "/auth/email-verification/confirm", []string{http.MethodPost}, (*EmailVerificationConfirmParams)(nil), (*AuthSessionResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.ConfirmEmailVerification(ctx, payload.(*EmailVerificationConfirmParams))
	})
	registerStandardTyped("auth", "ResendEmailVerification", runtime.Public, "/auth/email-verification/resend", []string{http.MethodPost}, (*EmailVerificationResendParams)(nil), (*EmailVerificationResendResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.ResendEmailVerification(ctx, payload.(*EmailVerificationResendParams))
	})
	registerStandardTyped("auth", "LoginEmail", runtime.Public, "/auth/login/email", []string{http.MethodPost}, (*EmailLoginParams)(nil), (*AuthSessionResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.LoginEmail(ctx, payload.(*EmailLoginParams))
	})
	registerStandardTyped("auth", "Refresh", runtime.Public, "/auth/refresh", []string{http.MethodPost}, (*RefreshParams)(nil), (*AuthSessionResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.Refresh(ctx, payload.(*RefreshParams))
	})
	registerStandardTyped("auth", "Logout", runtime.Public, "/auth/logout", []string{http.MethodPost}, (*RefreshParams)(nil), (*LogoutResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.Logout(ctx, payload.(*RefreshParams))
	})
	registerStandardTyped("auth", "Me", runtime.Auth, "/auth/me", []string{http.MethodGet}, nil, (*AuthBootstrapResponse)(nil), func(ctx context.Context, svc *Service, _ []any, _ any) (any, error) {
		return svc.Me(ctx)
	})
	registerStandardTyped("auth", "RequestPasswordReset", runtime.Public, "/auth/password-reset/request", []string{http.MethodPost}, (*PasswordResetRequestParams)(nil), (*PasswordResetRequestResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.RequestPasswordReset(ctx, payload.(*PasswordResetRequestParams))
	})
	registerStandardTyped("auth", "ConfirmPasswordReset", runtime.Public, "/auth/password-reset/confirm", []string{http.MethodPost}, (*PasswordResetConfirmParams)(nil), (*AuthSessionResponse)(nil), func(ctx context.Context, svc *Service, _ []any, payload any) (any, error) {
		return svc.ConfirmPasswordReset(ctx, payload.(*PasswordResetConfirmParams))
	})
	registerStandardOrganizations()
	registerStandardImpersonation()
	registerStandardNoServiceTyped("users", "DevBootstrap", runtime.Public, "/users/dev-bootstrap", []string{http.MethodPost}, (*DevBootstrapParams)(nil), (*AuthResponse)(nil), func(ctx context.Context, _ []any, payload any) (any, error) {
		return DevBootstrap(ctx, payload.(*DevBootstrapParams))
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

func registerStandardTyped(service string, name string, access runtime.Access, path string, methods []string, payload any, response any, invoke func(context.Context, *Service, []any, any) (any, error)) {
	var payloadType reflect.Type
	if payload != nil {
		payloadType = reflect.TypeOf(payload)
	}
	var responseType reflect.Type
	if response != nil {
		responseType = reflect.TypeOf(response)
	}
	runtime.RegisterEndpoint(&runtime.Endpoint{
		Service:      service,
		Name:         name,
		Access:       access,
		Path:         path,
		Methods:      methods,
		PathParams:   pathParamsFromPath(path),
		PayloadType:  payloadType,
		ResponseType: responseType,
		Invoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {
			svc, err := standardAuthService(ctx)
			if err != nil {
				return nil, clarifyStandardAuthTenantError(err)
			}
			out, err := invoke(ctx, svc, pathArgs, payload)
			if err != nil {
				return nil, clarifyStandardAuthTenantError(err)
			}
			return out, nil
		},
	})
}

func registerStandardNoServiceTyped(service string, name string, access runtime.Access, path string, methods []string, payload any, response any, invoke func(context.Context, []any, any) (any, error)) {
	var payloadType reflect.Type
	if payload != nil {
		payloadType = reflect.TypeOf(payload)
	}
	var responseType reflect.Type
	if response != nil {
		responseType = reflect.TypeOf(response)
	}
	runtime.RegisterEndpoint(&runtime.Endpoint{
		Service:      service,
		Name:         name,
		Access:       access,
		Path:         path,
		Methods:      methods,
		PathParams:   pathParamsFromPath(path),
		PayloadType:  payloadType,
		ResponseType: responseType,
		Invoke:       invoke,
	})
}

func pathParamsFromPath(path string) []runtime.ParamSpec {
	var params []runtime.ParamSpec
	for _, part := range strings.Split(path, "/") {
		if !strings.HasPrefix(part, ":") {
			continue
		}
		name := strings.TrimPrefix(part, ":")
		if name == "" {
			continue
		}
		params = append(params, runtime.ParamSpec{Name: name, Kind: runtime.ParamString})
	}
	return params
}

func bootstrapStandardAuthSchema(ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}) error {
	data, err := standardAuthSchema.ReadFile("db/gen/schema.sql")
	if err != nil {
		return fmt.Errorf("read standard auth schema: %w", err)
	}
	sql := string(data)
	replacements := []struct {
		old string
		new string
	}{
		{`CREATE SCHEMA "scenery_auth";`, `CREATE SCHEMA IF NOT EXISTS "scenery_auth";`},
		{`CREATE TABLE "scenery_auth".`, `CREATE TABLE IF NOT EXISTS "scenery_auth".`},
		{`CREATE UNIQUE INDEX "`, `CREATE UNIQUE INDEX IF NOT EXISTS "`},
		{`CREATE INDEX "`, `CREATE INDEX IF NOT EXISTS "`},
	}
	for _, replacement := range replacements {
		sql = strings.ReplaceAll(sql, replacement.old, replacement.new)
	}
	for _, statement := range splitSQLStatements(sql) {
		if strings.TrimSpace(statement) == "" || strings.HasPrefix(strings.TrimSpace(statement), "--") {
			continue
		}
		if _, err := pool.Exec(ctx, statement); err != nil {
			return clarifyStandardAuthTenantError(fmt.Errorf("bootstrap standard auth schema: %w", err))
		}
	}
	return nil
}

func clarifyStandardAuthTenantError(err error) error {
	if err == nil {
		return nil
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		if isStandardAuthTenantReference(pgErr) {
			return fmt.Errorf("%w (standard auth owns framework tenant state in PostgreSQL schema scenery_auth, including scenery_auth.tenants; this is standard auth state, not an app-local tenants service)", err)
		}
		if isAppDomainTenantReference(pgErr) {
			return fmt.Errorf("%w (this references an app-domain tenants relation; standard auth tenant state lives in scenery_auth.tenants and does not require an app-local tenants service)", err)
		}
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "scenery_auth.tenants") || strings.Contains(message, `"scenery_auth"."tenants"`) {
		return fmt.Errorf("%w (standard auth owns framework tenant state in PostgreSQL schema scenery_auth, including scenery_auth.tenants; this is standard auth state, not an app-local tenants service)", err)
	}
	return err
}

func isStandardAuthTenantReference(pgErr *pgconn.PgError) bool {
	if pgErr == nil {
		return false
	}
	schema := strings.TrimSpace(pgErr.SchemaName)
	table := strings.TrimSpace(pgErr.TableName)
	message := strings.ToLower(pgErr.Message)
	if !strings.EqualFold(table, "tenants") && !strings.Contains(message, "tenants") {
		return false
	}
	return strings.EqualFold(schema, "scenery_auth") ||
		strings.Contains(message, "scenery_auth.tenants") ||
		strings.Contains(message, `"scenery_auth"."tenants"`) ||
		(strings.EqualFold(table, "tenants") && strings.Contains(message, "scenery_auth"))
}

func isAppDomainTenantReference(pgErr *pgconn.PgError) bool {
	if pgErr == nil {
		return false
	}
	schema := strings.TrimSpace(pgErr.SchemaName)
	table := strings.TrimSpace(pgErr.TableName)
	message := strings.ToLower(pgErr.Message)
	if strings.EqualFold(schema, "scenery_auth") || strings.Contains(message, "scenery_auth") {
		return false
	}
	return strings.EqualFold(table, "tenants") ||
		strings.Contains(message, `relation "tenants"`) ||
		strings.Contains(message, "relation tenants")
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
