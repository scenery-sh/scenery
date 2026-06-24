package db

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/pgxpool"
	sceneryruntime "scenery.sh/runtime"
)

const (
	defaultDatabaseURLEnv = "DatabaseURL"
	managedDatabaseURLEnv = "SCENERY_MANAGED_DATABASE_URL"
	appRootEnv            = "SCENERY_APP_ROOT"
)

var (
	defaultPoolMu  sync.Mutex
	defaultPool    *pgxpool.Pool
	defaultPoolDSN string

	loadDotEnv        = sceneryruntime.LoadDotEnvIntoEnv
	discoverRoot      = app.DiscoverRoot
	getEnv            = envpolicy.Get
	parseConfig       = pgxpool.ParseConfig
	newPoolWithConfig = pgxpool.NewWithConfig
)

// Get returns the app process's shared pool for the default Scenery Postgres database.
func Get(ctx context.Context) (*pgxpool.Pool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dsn, source, err := resolveDefaultDatabaseURL()
	if err != nil {
		return nil, err
	}
	cfg, err := parseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("scenery db: invalid database URL in %s", source)
	}

	defaultPoolMu.Lock()
	defer defaultPoolMu.Unlock()
	if defaultPool != nil && defaultPoolDSN == dsn {
		return defaultPool, nil
	}
	pool, err := newPoolWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("scenery db: create pool for %s: %s", source, redactPoolError(err, dsn))
	}
	if defaultPool != nil {
		defaultPool.Close()
	}
	defaultPool = pool
	defaultPoolDSN = dsn
	return defaultPool, nil
}

// MustGet returns the shared default database pool or panics when it cannot be created.
func MustGet(ctx context.Context) *pgxpool.Pool {
	pool, err := Get(ctx)
	if err != nil {
		panic(err)
	}
	return pool
}

func resolveDefaultDatabaseURL() (dsn string, source string, err error) {
	if err := loadDotEnv(); err != nil {
		return "", "", fmt.Errorf("scenery db: load .env: %w", err)
	}
	cfg, err := discoverAppConfig()
	if err != nil {
		return "", "", err
	}
	envName := strings.TrimSpace(cfg.DatabaseURLEnv())
	if envName == "" {
		envName = defaultDatabaseURLEnv
	}
	if dsn := strings.TrimSpace(getEnv(envName)); dsn != "" {
		return dsn, envName, nil
	}
	if dsn := strings.TrimSpace(getEnv(managedDatabaseURLEnv)); dsn != "" {
		return dsn, managedDatabaseURLEnv, nil
	}
	return "", "", fmt.Errorf("scenery db: database URL is not configured; set %s or run under `scenery up` with managed Postgres", envName)
}

func discoverAppConfig() (app.Config, error) {
	start := strings.TrimSpace(getEnv(appRootEnv))
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return app.Config{}, fmt.Errorf("scenery db: resolve current directory: %w", err)
		}
		start = cwd
	}
	_, cfg, err := discoverRoot(start)
	if err != nil {
		if errors.Is(err, app.ErrRootNotFound) {
			return app.Config{}, fmt.Errorf("scenery db: app config not found; set %s or run inside an app root with %s", appRootEnv, app.PrimaryConfigFilename)
		}
		return app.Config{}, fmt.Errorf("scenery db: read app config: %w", err)
	}
	if !hasManagedPostgres(cfg) {
		return app.Config{}, fmt.Errorf("scenery db: dev.services.postgres is not configured")
	}
	return cfg, nil
}

func hasManagedPostgres(cfg app.Config) bool {
	for name, svc := range cfg.Dev.Services {
		kind := strings.TrimSpace(svc.Kind)
		if kind == "" && name == "postgres" {
			kind = "postgres"
		}
		if kind == "postgres" {
			return true
		}
	}
	return false
}

func redactPoolError(err error, dsn string) string {
	msg := err.Error()
	if dsn != "" {
		msg = strings.ReplaceAll(msg, dsn, "<redacted>")
	}
	u, parseErr := url.Parse(dsn)
	if parseErr != nil || u.User == nil {
		return msg
	}
	if password, ok := u.User.Password(); ok && password != "" {
		msg = strings.ReplaceAll(msg, password, "<redacted>")
	}
	return msg
}
