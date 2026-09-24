package main

import (
	"context"
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

// ambientSQLSupplyKeys are the variables that describe SQL supply to app
// processes. The CLI never reads them from its own inherited environment: an
// external server is the selected environment's sql.database_url, and the
// managed database is allocated per worktree.
var ambientSQLSupplyKeys = []string{appDatabaseURLEnv, postgresdb.RegistryEnv}

// sqlSupplyCommands choose between managed and external SQL supply.
var sqlSupplyCommands = map[string]bool{
	"up": true, "worker": true, "db": true, "snapshot": true, "inspect": true,
	"doctor": true, "down": true, "prune": true,
}

// runWithConfiguredSQLSupply is the CLI entrypoint: it replaces inherited SQL
// supply with the configured one before dispatching the command.
func runWithConfiguredSQLSupply(args []string, telemetry *cliTelemetryInvocation) error {
	if err := applyConfiguredSQLSupply(context.Background(), args); err != nil {
		return err
	}
	return runWithCLITelemetry(args, telemetry)
}

// applyConfiguredSQLSupply removes ambient SQL supply from this process and,
// for a command that selects SQL supply, exports the selected environment's
// configured sql.database_url as the external server. Discovery problems are
// left for the command itself to report.
func applyConfiguredSQLSupply(ctx context.Context, args []string) error {
	for _, key := range ambientSQLSupplyKeys {
		if err := envpolicy.Unset(key); err != nil {
			return err
		}
	}
	if len(args) == 0 || !sqlSupplyCommands[args[0]] {
		return nil
	}
	root, cfg, err := discoverConfiguredApp(flagValue(args[1:], "app-root"))
	if err != nil {
		return nil
	}
	env, err := cfg.ResolveEnv(sqlSupplyEnvironment(root, cfg, flagValue(args[1:], "env")))
	if err != nil {
		return nil
	}
	value, configured, err := configuredSQLDatabaseURL(ctx, cfg, env)
	if err != nil || !configured {
		return err
	}
	if args[0] == "up" && !env.Deployable() {
		return preconditionErrorf("environment %s configures %s, one external database for every worktree that runs it; development runtimes each use their own managed database, so unset it with `scenery config unset %s --env %s`", env.Name, appconfig.SQLDatabaseURLKey, appconfig.SQLDatabaseURLKey, env.Name)
	}
	if err := validateAppPostgresURL(value); err != nil {
		return preconditionErrorf("%s of environment %s must be a postgres:// or postgresql:// URL with a host and database name", appconfig.SQLDatabaseURLKey, env.Name)
	}
	return envpolicy.Set(appDatabaseURLEnv, value)
}

// configuredSQLDatabaseURL reveals the environment's sql.database_url from
// the revision its runtime runs.
func configuredSQLDatabaseURL(ctx context.Context, cfg appcfg.Config, env appcfg.ResolvedEnv) (string, bool, error) {
	store, err := devConfigStore(cfg)
	if err != nil || store == nil {
		return "", false, err
	}
	document, err := devConfigDocument(store, cfg, env)
	if err != nil {
		return "", false, preconditionErrorf("read %s configuration: %v", env.Name, err)
	}
	version, ok := document.Secrets[appconfig.SQLDatabaseURLKey]
	if !ok {
		return "", false, nil
	}
	backend, err := defaultConfigSecretBackend(store)
	if err != nil {
		return "", false, unavailableErrorf("%v", err)
	}
	value, err := backend.Resolve(ctx, env.Name, appconfig.SQLDatabaseURLKey, version)
	if err != nil {
		return "", false, unavailableErrorf("resolve configured secret %s: %v", appconfig.SQLDatabaseURLKey, err)
	}
	return strings.TrimSpace(string(value)), true, nil
}

func defaultConfigSecretBackend(store *appconfig.Store) (appconfig.SecretBackend, error) {
	if configSecretBackendOverride != nil {
		return configSecretBackendOverride(store)
	}
	return appconfig.DefaultSecretBackend(store)
}

// sqlSupplyEnvironment is the environment a command's SQL supply belongs to:
// the explicit --env, else the deployable environment whose stable runtime
// root this is, else the default environment.
func sqlSupplyEnvironment(root string, cfg appcfg.Config, explicit string) string {
	if explicit != "" || strings.TrimSpace(cfg.ID) == "" {
		return explicit
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return ""
	}
	home := paths.Home
	if canonical, err := filepath.EvalSymlinks(home); err == nil {
		home = canonical
	}
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	relative, err := filepath.Rel(filepath.Join(home, "deployments", cfg.ID), root)
	if err != nil {
		return ""
	}
	environment, rest, ok := strings.Cut(filepath.ToSlash(relative), "/")
	if !ok || rest != "source" || environment == ".." {
		return ""
	}
	return environment
}

// flagValue returns the last value of --name in args, in either the
// "--name value" or "--name=value" spelling.
func flagValue(args []string, name string) string {
	value := ""
	for index := 0; index < len(args); index++ {
		switch argument := args[index]; {
		case argument == "--":
			return value
		case argument == "--"+name && index+1 < len(args):
			value = args[index+1]
			index++
		case strings.HasPrefix(argument, "--"+name+"="):
			value = strings.TrimPrefix(argument, "--"+name+"=")
		}
	}
	return value
}
