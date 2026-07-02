package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/sqlitedb"
	sceneryruntime "scenery.sh/runtime"
)

const appRootEnv = "SCENERY_APP_ROOT"

var (
	poolsMu sync.Mutex
	pools   = map[string]*sql.DB{}

	loadDotEnv   = sceneryruntime.LoadDotEnvIntoEnv
	discoverRoot = app.DiscoverRoot
	getEnv       = envpolicy.Get
)

func Get(ctx context.Context, service ...string) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dsn, source, err := resolveDatabaseURL(service...)
	if err != nil {
		return nil, err
	}
	path, err := sqlitedb.ParseURL(dsn)
	if err != nil {
		return nil, fmt.Errorf("scenery db: invalid SQLite database URL in %s", source)
	}

	poolsMu.Lock()
	defer poolsMu.Unlock()
	if pool := pools[dsn]; pool != nil {
		return pool, nil
	}
	pool, err := sqlitedb.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("scenery db: open SQLite database from %s: %w", source, err)
	}
	pools[dsn] = pool
	return pool, nil
}

func MustGet(ctx context.Context, service ...string) *sql.DB {
	pool, err := Get(ctx, service...)
	if err != nil {
		panic(err)
	}
	return pool
}

func Close(service ...string) error {
	dsn, _, err := resolveDatabaseURL(service...)
	if err != nil {
		return err
	}
	poolsMu.Lock()
	defer poolsMu.Unlock()
	pool := pools[dsn]
	delete(pools, dsn)
	if pool == nil {
		return nil
	}
	return pool.Close()
}

func resolveDatabaseURL(service ...string) (dsn string, source string, err error) {
	if err := loadDotEnv(); err != nil {
		return "", "", fmt.Errorf("scenery db: load .env: %w", err)
	}
	cfg, err := discoverAppConfig()
	if err != nil {
		return "", "", err
	}
	name := ""
	if len(service) > 0 {
		name = strings.TrimSpace(service[0])
	}
	var svc app.SQLiteServiceConfig
	if name == "" {
		services := cfg.SQLiteServices()
		if svc, ok := cfg.SQLiteService("db"); ok {
			return databaseURLForService(svc)
		}
		if len(services) != 1 {
			return "", "", fmt.Errorf("scenery db: sqlite service name is required when %d services are configured", len(services))
		}
		return databaseURLForService(services[0])
	} else {
		var ok bool
		svc, ok = cfg.SQLiteService(name)
		if !ok {
			if dsn, source := databaseURLForDiscoveredService(name); dsn != "" {
				return dsn, source, nil
			}
			return "", "", fmt.Errorf("scenery db: sqlite service %q is not configured", name)
		}
	}
	return databaseURLForService(svc)
}

func databaseURLForService(svc app.SQLiteServiceConfig) (dsn string, source string, err error) {
	for _, envName := range []string{svc.DatabaseURLEnv, "DatabaseURL"} {
		if dsn := strings.TrimSpace(getEnv(envName)); dsn != "" {
			return dsn, envName, nil
		}
	}
	return "", "", fmt.Errorf("scenery db: database URL is not configured; set %s or run under `scenery up`", svc.DatabaseURLEnv)
}

func databaseURLForDiscoveredService(name string) (string, string) {
	raw := strings.TrimSpace(getEnv("SCENERY_SQLITE_DATABASES_JSON"))
	if raw == "" {
		return "", ""
	}
	var records []map[string]any
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return "", ""
	}
	for _, record := range records {
		recordName := strings.TrimSpace(fmt.Sprint(firstNonNil(record["service"], record["name"])))
		if recordName != name {
			continue
		}
		for _, key := range []string{"url", "database_url", "dsn"} {
			if value := strings.TrimSpace(fmt.Sprint(record[key])); strings.HasPrefix(value, "sqlite:") {
				return value, "SCENERY_SQLITE_DATABASES_JSON"
			}
		}
		if value := strings.TrimSpace(fmt.Sprint(record["path"])); strings.HasPrefix(value, "/") {
			return sqlitedb.URLForPath(value), "SCENERY_SQLITE_DATABASES_JSON"
		}
	}
	return "", ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return ""
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
	return cfg, nil
}
