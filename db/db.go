package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
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
	resolved, err := resolveDatabaseURL(service...)
	if err != nil {
		return nil, err
	}

	poolsMu.Lock()
	defer poolsMu.Unlock()
	if pool := pools[resolved.URL]; pool != nil {
		return pool, nil
	}
	var pool *sql.DB
	if _, err := postgresdb.ParseURL(resolved.URL); err != nil {
		return nil, fmt.Errorf("scenery db: service %q schema %q must use a postgres:// or postgresql:// URL from %s or DATABASE_URL: %w", resolved.Service, resolved.Schema, resolved.Source, err)
	}
	pool, err = postgresdb.Open(ctx, resolved.URL)
	if err != nil {
		return nil, fmt.Errorf("scenery db: open Postgres database for service %q schema %q from %s: %w", resolved.Service, resolved.Schema, resolved.Source, err)
	}
	pools[resolved.URL] = pool
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
	resolved, err := resolveDatabaseURL(service...)
	if err != nil {
		return err
	}
	poolsMu.Lock()
	defer poolsMu.Unlock()
	pool := pools[resolved.URL]
	delete(pools, resolved.URL)
	if pool == nil {
		return nil
	}
	return pool.Close()
}

type resolvedDatabaseURL struct {
	Service string
	Schema  string
	URL     string
	Source  string
}

func resolveDatabaseURL(service ...string) (resolvedDatabaseURL, error) {
	if err := loadDotEnv(); err != nil {
		return resolvedDatabaseURL{}, fmt.Errorf("scenery db: load .env: %w", err)
	}
	cfg, err := discoverAppConfig()
	if err != nil {
		return resolvedDatabaseURL{}, err
	}
	name := ""
	if len(service) > 0 {
		name = strings.TrimSpace(service[0])
	}
	if name == "" {
		services := cfg.DatabaseServices()
		if len(services) != 1 {
			return resolvedDatabaseURL{}, fmt.Errorf("scenery db: database service name is required when %d services are configured", len(services))
		}
		name = services[0].Name
	}
	if resolved, ok, err := databaseURLForConfiguredService(cfg, name); err != nil || ok {
		return resolved, err
	}
	if resolved, ok := databaseURLForDiscoveredService(name); ok {
		return resolved, nil
	}
	return resolvedDatabaseURL{}, fmt.Errorf("scenery db: database service %q is not configured", name)
}

func databaseURLForConfiguredService(cfg app.Config, name string) (resolvedDatabaseURL, bool, error) {
	svc, ok := cfg.DatabaseService(name)
	if !ok {
		return resolvedDatabaseURL{}, false, nil
	}
	serviceEnv := postgresname.ServiceDatabaseURLEnv(svc.Name)
	if dsn := strings.TrimSpace(getEnv(serviceEnv)); dsn != "" {
		return resolvedDatabaseURL{Service: svc.Name, Schema: svc.Schema, URL: dsn, Source: serviceEnv}, true, nil
	}
	appEnv := "DATABASE_URL"
	if dsn := strings.TrimSpace(getEnv(appEnv)); dsn != "" {
		serviceURL, err := postgresdb.ServiceURL(dsn, svc.Schema)
		if err != nil {
			return resolvedDatabaseURL{}, true, fmt.Errorf("scenery db: service %q schema %q could not derive URL from %s/DATABASE_URL: %w", svc.Name, svc.Schema, appEnv, err)
		}
		return resolvedDatabaseURL{Service: svc.Name, Schema: svc.Schema, URL: serviceURL, Source: appEnv}, true, nil
	}
	return resolvedDatabaseURL{}, true, fmt.Errorf("scenery db: service %q schema %q database URL is not configured; set %s or %s", svc.Name, svc.Schema, serviceEnv, appEnv)
}

func databaseURLForDiscoveredService(name string) (resolvedDatabaseURL, bool) {
	database, err := postgresdb.DecodeRegistry(getEnv(postgresdb.RegistryEnv))
	if err != nil {
		return resolvedDatabaseURL{}, false
	}
	for _, svc := range database.Schemas {
		if svc.Name == name && strings.TrimSpace(svc.URL) != "" {
			return resolvedDatabaseURL{Service: svc.Name, Schema: svc.Schema, URL: svc.URL, Source: postgresdb.RegistryEnv}, true
		}
	}
	return resolvedDatabaseURL{}, false
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
