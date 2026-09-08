package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

const (
	postgresServerImage = "postgres:18@sha256:4aabea78cf39b90e834caf3af7d602a18565f6fe2508705c8d01aa63245c2e20"
	postgresServerUser  = "scenery"
)

type postgresDockerRunner interface {
	Run(context.Context, ...string) (string, error)
}

type execPostgresDockerRunner struct{ environment []string }

func (r execPostgresDockerRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if r.environment != nil {
		cmd.Env = r.environment
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), postgresDockerFailure(err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Docker arguments can include POSTGRES_PASSWORD and stderr can repeat them.
// Keep both out of public errors; output is returned separately for object lookup.
func postgresDockerFailure(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return &codedCLIError{code: 4, err: fmt.Errorf("docker is unavailable: docker was not found in PATH; install Docker or provide an external DATABASE_URL")}
	}
	return &codedCLIError{code: 4, err: fmt.Errorf("docker could not complete the managed Postgres operation; check Docker availability, context and permissions, or provide an external DATABASE_URL")}
}

var (
	openPostgresDatabase = postgresdb.Open
	openPostgresAdmin    = postgresdb.Open
)

func managedDatabaseEnv(ctx context.Context, appRoot string, cfg app.Config, requirements compiler.SQLRequirements, baseEnv []string) ([]string, postgresdb.Database, error) {
	cfgs, err := resolveSQLSupply(requirements, baseEnv, true)
	if err != nil {
		return nil, postgresdb.Database{}, err
	}
	if len(cfgs) == 0 {
		return nil, postgresdb.Database{}, nil
	}
	services := make([]postgresdb.Service, 0, len(cfgs))
	databaseEnv := appDatabaseURLEnv
	if value := lookupEnvValue(baseEnv, databaseEnv); value != "" {
		if err := validateAppPostgresURL(value); err != nil {
			return nil, postgresdb.Database{}, err
		}
		for _, svc := range cfgs {
			serviceURL, err := postgresdb.ServiceURL(value, svc.Schema)
			if err != nil {
				return nil, postgresdb.Database{}, fmt.Errorf("derive postgres URL for service %s schema %s: %w", svc.Name, svc.Schema, err)
			}
			services = append(services, postgresdb.Service{
				Name:   svc.Name,
				Schema: svc.Schema,
				URL:    serviceURL,
			})
		}
		database := postgresdb.Database{Database: postgresdb.DatabaseNameFromURL(value), URL: value, Source: postgresdb.SourceExternal, Schemas: services}
		return postgresdb.Env(database), database, nil
	}

	resolver, err := newWorktreePostgresResolver(ctx, appRoot, cfg.AppID())
	if err != nil {
		return nil, postgresdb.Database{}, err
	}
	// SQL names and retained ownership must use the same canonical root,
	// including macOS /var aliases and explicitly symlinked parent paths.
	appRoot = resolver.paths.AppRoot
	server, err := resolver.ensure(ctx)
	if err != nil {
		return nil, postgresdb.Database{}, err
	}
	admin, err := openPostgresAdmin(ctx, worktreePostgresURL(server, "postgres"))
	if err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("connect to managed postgres server: %w", err)
	}
	defer func() { _ = admin.Close() }()
	dbName := postgresname.DatabaseNameFor(cfg.AppID(), appRoot)
	if err := postgresdb.EnsureDatabase(ctx, admin, dbName); err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("ensure postgres database %s: %w", dbName, err)
	}
	baseURL := worktreePostgresURL(server, dbName)
	appDB, err := openPostgresDatabase(ctx, baseURL)
	if err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("connect to managed postgres database %s: %w", dbName, err)
	}
	defer func() { _ = appDB.Close() }()
	if err := postgresdb.EnsureSchema(ctx, appDB, "scenery"); err != nil {
		return nil, postgresdb.Database{}, fmt.Errorf("ensure postgres schema scenery: %w", err)
	}
	for _, svc := range cfgs {
		if err := postgresdb.EnsureSchema(ctx, appDB, svc.Schema); err != nil {
			return nil, postgresdb.Database{}, fmt.Errorf("ensure postgres schema %s for service %s: %w", svc.Schema, svc.Name, err)
		}
		serviceURL, err := postgresdb.ServiceURL(baseURL, svc.Schema)
		if err != nil {
			return nil, postgresdb.Database{}, fmt.Errorf("derive postgres URL for service %s schema %s: %w", svc.Name, svc.Schema, err)
		}
		services = append(services, postgresdb.Service{
			Name:   svc.Name,
			Schema: svc.Schema,
			URL:    serviceURL,
		})
	}
	database := postgresdb.Database{Database: dbName, URL: baseURL, Source: postgresdb.SourceManaged, Schemas: services, AppRoot: resolver.paths.AppRoot, ResourceID: server.InstanceID}
	return postgresdb.Env(database), database, nil
}

func isMissingDockerObject(out string, err error) bool {
	msg := strings.ToLower(out)
	if err != nil {
		msg += " " + strings.ToLower(err.Error())
	}
	return strings.Contains(msg, "no such object") ||
		strings.Contains(msg, "no such container") ||
		strings.Contains(msg, "no such volume")
}

func randomPostgresPassword() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func emitPostgresReadyEvents(ctx context.Context, sink *devEventSink, database postgresdb.Database) {
	if sink == nil {
		return
	}
	for _, svc := range database.Schemas {
		sink.Emit(ctx, devdash.DevSource{ID: "postgres:" + svc.Name, Kind: "substrate", Name: svc.Name, Role: "database", Status: "running"}, "info", "Postgres service database ready", map[string]any{
			"service":  svc.Name,
			"engine":   "postgres",
			"database": database.Database,
			"schema":   svc.Schema,
			"source":   string(database.Source),
		})
	}
}
