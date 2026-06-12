package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
)

type psqlOptions struct {
	AppRoot    string
	Args       []string
	UseManaged bool
}

func dbCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery db psql|apply|seed|setup|reset|drop|snapshot|branch|postgres [--app-root <path>]")
	}
	switch args[0] {
	case "psql":
		return psqlCommandWithOptions(args[1:], true)
	case "apply":
		return dbApplyCommand(args[1:])
	case "seed":
		return dbSeedCommand(args[1:])
	case "setup":
		return dbSetupCommand(args[1:])
	case "reset":
		return dbResetCommand(args[1:])
	case "drop":
		return dbDropCommand(args[1:])
	case "snapshot":
		return dbSnapshotCommand(args[1:])
	case "branch":
		return dbBranchCommand(args[1:])
	case "postgres":
		return dbPostgresCommand(args[1:])
	default:
		return fmt.Errorf("unknown db command %q", args[0])
	}
}

func dbApplyCommand(args []string) error {
	return runDBApply(context.Background(), os.Stdout, args)
}

func runDBApply(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseDBApplyArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	if err := runDatabaseApplyProvider(ctx, appRoot, cfg, cfg.Database.Apply); err != nil {
		return err
	}
	result := buildDBApplyResult(appRoot, cfg)
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "scenery: database apply complete using %s provider\n", result.Apply.Provider)
	return nil
}

func dbSyncCommand(args []string) error {
	opts, err := parseDBResetArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := runDatabaseApplyProvider(ctx, appRoot, cfg, cfg.Database.Apply); err != nil {
		return err
	}
	if sqlcPlan, ok, err := buildSQLCGeneratorPlan(appRoot, cfg); err != nil {
		return err
	} else if ok {
		return runSQLCGenerator(ctx, os.Stdout, appRoot, sqlcPlan, false)
	}
	fmt.Fprintln(os.Stdout, "scenery: database sync complete; no sqlc generator configured")
	return nil
}

type dbApplyOptions struct {
	AppRoot string
	JSON    bool
}

type dbApplyResult struct {
	SchemaVersion string             `json:"schema_version"`
	App           inspectdata.AppRef `json:"app"`
	Apply         dbApplyRecord      `json:"apply"`
}

type dbApplyRecord struct {
	Provider string `json:"provider"`
	Command  string `json:"command,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Status   string `json:"status"`
}

func buildDBApplyResult(appRoot string, cfg appcfg.Config) dbApplyResult {
	provider := firstNonEmpty(cfg.Database.Apply.Provider, "exec")
	return dbApplyResult{
		SchemaVersion: "scenery.db.apply.result.v1",
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".scenery.json"),
		},
		Apply: dbApplyRecord{
			Provider: provider,
			Command:  cfg.Database.Apply.Command,
			CWD:      cfg.Database.Apply.CWD,
			Status:   "applied",
		},
	}
}

func runDatabaseApplyProvider(ctx context.Context, appRoot string, cfg appcfg.Config, apply appcfg.DatabaseApplyConfig) error {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	env, err = managedDatabaseLifecycleEnv(ctx, appRoot, cfg, env)
	if err != nil {
		return err
	}
	return runDatabaseApplyProviderWithEnv(ctx, appRoot, apply, env)
}

func runDatabaseApplyProviderWithEnv(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string) error {
	return runDatabaseApplyProviderWithEnvIO(ctx, appRoot, apply, env, os.Stdout, os.Stderr)
}

func runDatabaseApplyProviderWithEnvIO(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string, stdout, stderr io.Writer) error {
	command := strings.TrimSpace(apply.Command)
	if command == "" {
		return fmt.Errorf("database.apply is not configured")
	}
	provider := firstNonEmpty(apply.Provider, "exec")
	if provider != "exec" {
		return fmt.Errorf("unsupported database apply provider %q", apply.Provider)
	}
	program, args := shellInvocation(command)
	return runLifecycleExec(ctx, lifecycleExecRequest{
		Dir:     resolveLifecycleCWD(appRoot, apply.CWD),
		Env:     overlayEnv(env, apply.Env),
		Program: program,
		Args:    args,
		Stdin:   os.Stdin,
		Stdout:  stdout,
		Stderr:  stderr,
	})
}

func dbDropCommand(args []string) error {
	opts, err := parseDBResetArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	plan, err := managedPostgresPlanForCurrentSession(context.Background(), appRoot, cfg, baseEnv)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("dev.services.postgres is not configured")
	}
	if err := dropManagedPostgresDatabase(context.Background(), plan.AdminURL, plan.DatabaseName); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "dropped scenery managed database %s for current dev runtime\n", plan.DatabaseName)
	return nil
}

func psqlCommandWithOptions(args []string, useManaged bool) error {
	opts, err := parsePSQLArgs(args)
	if err != nil {
		return err
	}
	opts.UseManaged = useManaged
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	invocation, err := buildPSQLInvocationForConfig(context.Background(), appRoot, cfg, baseEnv, opts)
	if err != nil {
		return err
	}
	cmd := exec.Command(invocation.Program, invocation.Args...)
	cmd.Dir = invocation.Dir
	cmd.Env = invocation.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func dbResetCommand(args []string) error {
	opts, err := parseDBResetArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	plan, err := managedPostgresPlanForCurrentSession(context.Background(), appRoot, cfg, baseEnv)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("dev.services.postgres is not configured")
	}
	if err := resetManagedPostgresDatabase(context.Background(), plan.AdminURL, plan.DatabaseName); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "reset scenery managed database %s\n", plan.DatabaseName)
	return nil
}

func dbSnapshotCommand(args []string) error {
	opts, err := parseDBSnapshotArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	session, err := currentAgentSessionForAppRoot(ctx, appRoot)
	if err != nil {
		return err
	}
	path, err := managedPostgresSnapshotPath(appRoot, session.SessionID, opts.Name)
	if err != nil {
		return err
	}
	target, err := resolveDBSnapshotTarget(ctx, appRoot, cfg, baseEnv, session)
	if err != nil {
		return err
	}
	switch opts.Action {
	case "create":
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := createDBSnapshot(ctx, target, path); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "created scenery database snapshot %s at %s\n", opts.Name, path)
		return nil
	case "restore":
		if _, err := os.Stat(path); err != nil {
			return err
		}
		if err := restoreDBSnapshot(ctx, appRoot, cfg, target, path); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "restored scenery database snapshot %s from %s\n", opts.Name, path)
		return nil
	default:
		return fmt.Errorf("unknown db snapshot action %q", opts.Action)
	}
}

type dbSnapshotTarget struct {
	Kind        string
	DatabaseURL string
	Env         []string
	Plan        *managedPostgresPlan
	BranchPin   *worktreeDBPin
}

func resolveDBSnapshotTarget(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string, session *localagent.Session) (dbSnapshotTarget, error) {
	if name, svc, ok := managedPostgresDeclared(cfg); ok {
		if managedPostgresUsesExternalDatabase(baseEnv) {
			dsn, err := externalPostgresDatabaseURL(baseEnv)
			if err != nil {
				return dbSnapshotTarget{}, err
			}
			return dbSnapshotTarget{Kind: "postgres", DatabaseURL: dsn, Env: baseEnv}, nil
		}
		if postgresServiceUsesBranching(svc) {
			var pin worktreeDBPin
			var connection dbBranchConnectionInfo
			var err error
			if firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), dbBranchDefaultPolicy) == "session" {
				resolution, err := ensureDBBranchPinForSession(ctx, appRoot, cfg, session)
				if err != nil {
					return dbSnapshotTarget{}, err
				}
				pin = resolution.Pin
				connection, err = dbBranchProviderForConfig(cfg).Connection(ctx, pin)
				if err != nil {
					return dbSnapshotTarget{}, fmt.Errorf("dev.services.%s could not resolve Postgres branch connection: %w", name, err)
				}
			} else {
				pin, connection, err = resolveDBBranchConnection(ctx, appRoot, cfg)
				if err != nil {
					return dbSnapshotTarget{}, fmt.Errorf("dev.services.%s could not resolve Postgres branch connection: %w", name, err)
				}
			}
			return dbSnapshotTarget{Kind: "postgres_branch", DatabaseURL: connection.DatabaseURL, Env: baseEnv, BranchPin: &pin}, nil
		}
	}
	plan, err := managedPostgresPlanForCurrentSession(ctx, appRoot, cfg, baseEnv)
	if err != nil {
		return dbSnapshotTarget{}, err
	}
	if plan == nil {
		return dbSnapshotTarget{}, fmt.Errorf("dev.services.postgres is not configured")
	}
	return dbSnapshotTarget{Kind: "postgres", DatabaseURL: plan.DatabaseURL, Env: baseEnv, Plan: plan}, nil
}

func createDBSnapshot(ctx context.Context, target dbSnapshotTarget, path string) error {
	if target.Plan != nil {
		if err := ensureManagedPostgresDatabase(ctx, target.Plan.AdminURL, target.Plan.DatabaseName); err != nil {
			return err
		}
	}
	return runPGDumpSnapshot(ctx, target.DatabaseURL, target.Env, path)
}

func restoreDBSnapshot(ctx context.Context, appRoot string, cfg appcfg.Config, target dbSnapshotTarget, path string) error {
	if target.Plan != nil {
		if err := resetManagedPostgresDatabase(ctx, target.Plan.AdminURL, target.Plan.DatabaseName); err != nil {
			return err
		}
	}
	if target.Kind == "postgres_branch" && target.BranchPin != nil {
		if err := dbBranchProviderForConfig(cfg).ResetBranch(ctx, *target.BranchPin, dbBranchOptions{AppRoot: appRoot}); err != nil {
			return err
		}
		pin, connection, err := resolveDBBranchConnection(ctx, appRoot, cfg)
		if err != nil {
			return err
		}
		target.BranchPin = &pin
		target.DatabaseURL = connection.DatabaseURL
	}
	return runPSQLSnapshotRestore(ctx, target.DatabaseURL, target.Env, path)
}

func runPGDumpSnapshot(ctx context.Context, databaseURL string, env []string, path string) error {
	program, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf("pg_dump not found in PATH")
	}
	dsnArg, env := psqlDatabaseArgAndEnv(databaseURL, env)
	cmd := exec.CommandContext(ctx, program, "--file", path, "--no-publications", "--no-subscriptions", dsnArg)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runPSQLSnapshotRestore(ctx context.Context, databaseURL string, env []string, path string) error {
	program, err := exec.LookPath("psql")
	if err != nil {
		return fmt.Errorf("psql not found in PATH")
	}
	dsnArg, env := psqlDatabaseArgAndEnv(databaseURL, env)
	cmd := exec.CommandContext(ctx, program, dsnArg, "-v", "ON_ERROR_STOP=1", "-f", path)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type psqlInvocation struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
}

func parsePSQLArgs(args []string) (psqlOptions, error) {
	var opts psqlOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return psqlOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			opts.Args = append(opts.Args, args[i:]...)
			return opts, nil
		}
	}
	return opts, nil
}

func buildPSQLInvocation(appRoot string, baseEnv []string, opts psqlOptions) (psqlInvocation, error) {
	return buildPSQLInvocationForConfig(context.Background(), appRoot, appcfg.Config{}, baseEnv, opts)
}

func buildPSQLInvocationForConfig(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string, opts psqlOptions) (psqlInvocation, error) {
	program, err := exec.LookPath("psql")
	if err != nil {
		return psqlInvocation{}, fmt.Errorf("psql not found in PATH")
	}
	dsn, err := resolveDatabaseURLForConfig(ctx, appRoot, cfg, baseEnv, opts.UseManaged)
	if err != nil {
		return psqlInvocation{}, err
	}
	env, err := appEnvWithDotEnv(baseEnv, appRoot)
	if err != nil {
		return psqlInvocation{}, err
	}
	dsnArg, env := psqlDatabaseArgAndEnv(dsn, env)
	return psqlInvocation{
		Program: program,
		Args:    append([]string{dsnArg}, opts.Args...),
		Dir:     appRoot,
		Env:     env,
	}, nil
}

func psqlDatabaseArgAndEnv(dsn string, env []string) (string, []string) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil {
		return dsn, env
	}
	password, ok := u.User.Password()
	if !ok {
		return dsn, env
	}
	u.User = url.User(u.User.Username())
	return u.String(), overlayEnv(env, map[string]string{"PGPASSWORD": password})
}

func resolveDatabaseURLForConfig(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string, useManaged bool) (string, error) {
	if useManaged {
		if _, svc, ok := managedPostgresDeclared(cfg); ok {
			if managedPostgresUsesExternalDatabase(baseEnv) {
				return externalPostgresDatabaseURL(baseEnv)
			}
			if postgresServiceUsesBranching(svc) {
				var session *localagent.Session
				if firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), dbBranchDefaultPolicy) == "session" {
					active, err := currentAgentSessionForAppRoot(ctx, appRoot)
					if err != nil {
						return "", err
					}
					session = active
				}
				dsn, err := resolveDBBranchDatabaseURL(ctx, appRoot, cfg, session)
				if err != nil {
					return "", fmt.Errorf("dev.services.postgres kind %q could not resolve database branch connection: %w", svc.Kind, err)
				}
				return dsn, nil
			}
		}
		plan, err := managedPostgresPlanForCurrentSession(ctx, appRoot, cfg, baseEnv)
		if err != nil {
			return "", err
		}
		if plan != nil {
			if err := ensureManagedPostgresDatabase(ctx, plan.AdminURL, plan.DatabaseName); err != nil {
				return "", err
			}
			return plan.DatabaseURL, nil
		}
	}
	dsn, _, err := discoverDatabaseURLFromEnvList(appRoot, baseEnv)
	if err != nil {
		return "", err
	}
	return dsn, nil
}

func managedDatabaseLifecycleEnv(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string) ([]string, error) {
	if _, _, ok := managedPostgresDeclared(cfg); !ok {
		return baseEnv, nil
	}
	if managedPostgresUsesExternalDatabase(baseEnv) {
		dsn, err := externalPostgresDatabaseURL(baseEnv)
		if err != nil {
			return nil, err
		}
		return overlayEnv(envWithoutKeys(baseEnv, legacyDatabaseURLEnv), map[string]string{
			appDatabaseURLEnv: dsn,
		}), nil
	}
	dsn, err := resolveDatabaseURLForConfig(ctx, appRoot, cfg, baseEnv, true)
	if err != nil {
		return nil, err
	}
	values := map[string]string{
		appDatabaseURLEnv:               dsn,
		"SCENERY_MANAGED_DATABASE_URL":  dsn,
		"SCENERY_MANAGED_DATABASE_NAME": databaseNameFromURL(dsn),
	}
	if envName := dbDatabaseURLEnv(cfg); envName != "" {
		values[envName] = dsn
	}
	return overlayEnv(envWithoutKeys(baseEnv, legacyDatabaseURLEnv), values), nil
}

func databaseNameFromURL(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

type dbResetOptions struct {
	AppRoot string
}

type dbSnapshotOptions struct {
	Action  string
	Name    string
	AppRoot string
}

func parseDBResetArgs(args []string) (dbResetOptions, error) {
	var opts dbResetOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbResetOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			return dbResetOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parseDBApplyArgs(args []string) (dbApplyOptions, error) {
	var opts dbApplyOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbApplyOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		default:
			return dbApplyOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parseDBSnapshotArgs(args []string) (dbSnapshotOptions, error) {
	var opts dbSnapshotOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "create", "restore":
			if opts.Action != "" {
				return dbSnapshotOptions{}, fmt.Errorf("db snapshot action already set")
			}
			opts.Action = args[i]
			i++
			if i >= len(args) {
				return dbSnapshotOptions{}, fmt.Errorf("missing snapshot name; expected: scenery db snapshot %s <name> [--app-root <path>]", opts.Action)
			}
			opts.Name = args[i]
		case "--app-root":
			i++
			if i >= len(args) {
				return dbSnapshotOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			return dbSnapshotOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if opts.Action == "" {
		return dbSnapshotOptions{}, fmt.Errorf("missing db snapshot action; expected: scenery db snapshot create <name> [--app-root <path>] or scenery db snapshot restore <name> [--app-root <path>]")
	}
	if opts.Name == "" {
		return dbSnapshotOptions{}, fmt.Errorf("missing snapshot name; expected: scenery db snapshot %s <name> [--app-root <path>]", opts.Action)
	}
	return opts, nil
}

func managedPostgresSnapshotPath(appRoot, sessionID, name string) (string, error) {
	label := localagentLabel(name)
	if label == "" {
		return "", fmt.Errorf("snapshot name must contain at least one letter or digit")
	}
	return filepath.Join(appRoot, ".scenery", "sessions", sessionID, "db", "snapshots", label+".sql"), nil
}
