package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
	"scenery.sh/internal/runtimeapp"
)

var (
	poolsMu sync.Mutex
	pools   = map[string]*sql.DB{}

	loadDotEnv = runtimeapp.LoadDotEnvIntoEnv
	getEnv     = envpolicy.Get
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
	database, err := postgresdb.DecodeRegistry(getEnv(postgresdb.RegistryEnv))
	if err != nil {
		return resolvedDatabaseURL{}, fmt.Errorf("scenery db: invalid %s SQL supply", postgresdb.RegistryEnv)
	}
	name := ""
	if len(service) > 0 {
		name = strings.TrimSpace(service[0])
	}
	if name == "" {
		services := database.Schemas
		if len(services) > 1 {
			services = nil
			for _, binding := range database.Schemas {
				if binding.Name != "scenery" {
					services = append(services, binding)
				}
			}
		}
		if len(services) != 1 {
			return resolvedDatabaseURL{}, fmt.Errorf("scenery db: database service name is required when %d SQL bindings are supplied", len(services))
		}
		name = services[0].Name
	}
	for _, binding := range database.Schemas {
		if binding.Name == name {
			return resolvedDatabaseURL{Service: name, Schema: binding.Schema, URL: binding.URL, Source: postgresdb.RegistryEnv}, nil
		}
	}
	if len(database.Schemas) > 0 {
		return resolvedDatabaseURL{}, fmt.Errorf("scenery db: database service %q is not in the compiled SQL bindings", name)
	}
	// An explicitly named standalone caller supplies an endpoint directly. No
	// app config or declaration discovery is involved; generated runtimes have
	// already supplied the exact compiled bindings above.
	schema, err := postgresname.SchemaNameFor(name)
	if err != nil {
		return resolvedDatabaseURL{}, fmt.Errorf("scenery db: invalid SQL binding %q: %w", name, err)
	}
	serviceEnv := postgresname.ServiceDatabaseURLEnv(name)
	appEnv := "DATABASE_URL"
	endpoint, err := postgresdb.ResolveServiceEndpoint(schema, strings.TrimSpace(getEnv(serviceEnv)), strings.TrimSpace(getEnv(appEnv)))
	if err != nil {
		return resolvedDatabaseURL{}, fmt.Errorf("scenery db: service %q schema %q requires a valid PostgreSQL URL in %s", name, schema, appEnv)
	}
	if endpoint.URL == "" {
		return resolvedDatabaseURL{}, fmt.Errorf("scenery db: service %q schema %q database URL is not configured; set %s or %s", name, schema, serviceEnv, appEnv)
	}
	source := serviceEnv
	if endpoint.FromBaseURL {
		source = appEnv
	}
	return resolvedDatabaseURL{Service: name, Schema: schema, URL: endpoint.URL, Source: source}, nil
}
