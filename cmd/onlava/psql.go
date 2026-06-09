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

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/envpolicy"
	inspectdata "github.com/pbrazdil/onlava/internal/inspect"
	"github.com/pbrazdil/onlava/internal/neonselfhost"
)

type psqlOptions struct {
	AppRoot    string
	Args       []string
	UseManaged bool
}

func dbCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: onlava db psql|apply|seed|setup|reset|drop|snapshot|branch|neon [--app-root <path>]")
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
	case "neon":
		return dbNeonCommand(args[1:])
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
	if err := runDatabaseApplyProvider(ctx, appRoot, cfg.Database.Apply); err != nil {
		return err
	}
	result := buildDBApplyResult(appRoot, cfg)
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "onlava: database apply complete using %s provider\n", result.Apply.Provider)
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
	if err := runDatabaseApplyProvider(ctx, appRoot, cfg.Database.Apply); err != nil {
		return err
	}
	if sqlcPlan, ok, err := buildSQLCGeneratorPlan(appRoot, cfg); err != nil {
		return err
	} else if ok {
		return runSQLCGenerator(ctx, os.Stdout, appRoot, sqlcPlan, false)
	}
	fmt.Fprintln(os.Stdout, "onlava: database sync complete; no sqlc generator configured")
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
		SchemaVersion: "onlava.db.apply.result.v1",
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: filepath.Join(appRoot, ".onlava.json"),
		},
		Apply: dbApplyRecord{
			Provider: provider,
			Command:  cfg.Database.Apply.Command,
			CWD:      cfg.Database.Apply.CWD,
			Status:   "applied",
		},
	}
}

func runDatabaseApplyProvider(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig) error {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	return runDatabaseApplyProviderWithEnv(ctx, appRoot, apply, env)
}

func runDatabaseApplyProviderWithEnv(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string) error {
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
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
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
	session, err := currentAgentSessionForAppRoot(context.Background(), appRoot)
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
	fmt.Fprintf(os.Stdout, "dropped onlava managed database %s for session %s\n", plan.DatabaseName, session.SessionID)
	return nil
}

func psqlCommand(args []string) error {
	return psqlCommandWithOptions(args, false)
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
	fmt.Fprintf(os.Stdout, "reset onlava managed database %s\n", plan.DatabaseName)
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
		fmt.Fprintf(os.Stdout, "created onlava database snapshot %s at %s\n", opts.Name, path)
		return nil
	case "restore":
		if _, err := os.Stat(path); err != nil {
			return err
		}
		if err := restoreDBSnapshot(ctx, appRoot, cfg, target, path); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "restored onlava database snapshot %s from %s\n", opts.Name, path)
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
	NeonPin     *worktreeDBPin
	NeonConn    *neonBranchConnectionInfo
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
		if strings.TrimSpace(svc.Kind) == "neon" {
			var pin worktreeDBPin
			var connection neonBranchConnectionInfo
			var err error
			if firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), neonDefaultBranchPolicy) == "session" {
				resolution, err := ensureNeonBranchPinForSession(ctx, appRoot, cfg, session)
				if err != nil {
					return dbSnapshotTarget{}, err
				}
				pin = resolution.Pin
				connection, err = neonBranchProviderForConfig(cfg).Connection(ctx, pin)
			} else {
				pin, connection, err = resolveNeonBranchConnection(ctx, appRoot, cfg)
			}
			if err != nil {
				return dbSnapshotTarget{}, fmt.Errorf("dev.services.%s kind %q could not resolve Neon branch connection: %w", name, svc.Kind, err)
			}
			return dbSnapshotTarget{Kind: "neon", DatabaseURL: connection.DatabaseURL, Env: baseEnv, NeonPin: &pin, NeonConn: &connection}, nil
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
	if target.Kind == "neon" {
		ok, err := createNeonSelfhostSnapshot(ctx, target, path)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("Neon database snapshots require a ready self-hosted Neon compute container; run `onlava db branch status --json` and ensure the branch is ready")
		}
		return nil
	}
	return runPGDumpSnapshot(ctx, target.DatabaseURL, target.Env, path)
}

func restoreDBSnapshot(ctx context.Context, appRoot string, cfg appcfg.Config, target dbSnapshotTarget, path string) error {
	if target.Plan != nil {
		if err := resetManagedPostgresDatabase(ctx, target.Plan.AdminURL, target.Plan.DatabaseName); err != nil {
			return err
		}
	}
	if target.Kind == "neon" && target.NeonPin != nil {
		if err := neonBranchProviderForConfig(cfg).ResetBranch(ctx, *target.NeonPin, dbBranchOptions{AppRoot: appRoot}); err != nil {
			return err
		}
		pin, connection, err := resolveNeonBranchConnection(ctx, appRoot, cfg)
		if err != nil {
			return err
		}
		target.NeonPin = &pin
		target.NeonConn = &connection
		target.DatabaseURL = connection.DatabaseURL
		ok, err := restoreNeonSelfhostSnapshot(ctx, target, path)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("Neon database snapshot restore requires a ready self-hosted Neon compute container; run `onlava db branch status --json` and ensure the branch is ready")
		}
		return nil
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

const neonSelfhostComputePostgresPort = "55433"

func createNeonSelfhostSnapshot(ctx context.Context, target dbSnapshotTarget, path string) (bool, error) {
	container, ok, err := neonSelfhostSnapshotContainer(target)
	if err != nil || !ok {
		return ok, err
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return true, fmt.Errorf("docker not found in PATH; self-hosted Neon snapshots use pg_dump from the branch compute container")
	}
	file, err := os.Create(path)
	if err != nil {
		return true, err
	}
	defer file.Close()
	cmd := exec.CommandContext(ctx, docker, append(neonSelfhostDockerExecPrefix(target.DatabaseURL, container, false), "pg_dump", "--clean", "--if-exists", "--no-publications", "--no-subscriptions", "-h", "127.0.0.1", "-p", neonSelfhostComputePostgresPort, "-U", target.NeonConn.Endpoint.Role, "-d", target.NeonConn.DatabaseName)...)
	cmd.Stdout = file
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return true, err
	}
	return true, nil
}

func restoreNeonSelfhostSnapshot(ctx context.Context, target dbSnapshotTarget, path string) (bool, error) {
	container, ok, err := neonSelfhostSnapshotContainer(target)
	if err != nil || !ok {
		return ok, err
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return true, fmt.Errorf("docker not found in PATH; self-hosted Neon snapshot restore uses psql from the branch compute container")
	}
	file, err := os.Open(path)
	if err != nil {
		return true, err
	}
	defer file.Close()
	cmd := exec.CommandContext(ctx, docker, append(neonSelfhostDockerExecPrefix(target.DatabaseURL, container, true), "psql", "-h", "127.0.0.1", "-p", neonSelfhostComputePostgresPort, "-U", target.NeonConn.Endpoint.Role, "-d", target.NeonConn.DatabaseName, "-v", "ON_ERROR_STOP=1")...)
	cmd.Stdin = file
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return true, err
	}
	return true, nil
}

func neonSelfhostDockerExecPrefix(databaseURL, container string, stdin bool) []string {
	password := "cloud_admin"
	if parsed, err := url.Parse(databaseURL); err == nil && parsed.User != nil {
		if value, ok := parsed.User.Password(); ok && strings.TrimSpace(value) != "" {
			password = value
		}
	}
	args := []string{"exec"}
	if stdin {
		args = append(args, "-i")
	}
	return append(args, "-e", "PGPASSWORD="+password, container)
}

func neonSelfhostSnapshotContainer(target dbSnapshotTarget) (string, bool, error) {
	if target.NeonPin == nil || target.NeonConn == nil || strings.TrimSpace(target.NeonConn.Endpoint.Host) == "" {
		return "", false, nil
	}
	if target.NeonPin.Provider != neonSelfhostProvider && target.NeonConn.Endpoint.Source != neonSelfhostBranchDriverEndpointSource {
		return "", false, nil
	}
	root, err := neonSubstrateRoot()
	if err != nil {
		return "", false, err
	}
	state, ok, err := neonselfhost.ReadBackendState(filepath.Join(root, "backend.json"))
	if err != nil || !ok {
		return "", false, err
	}
	project, ok := state.Projects[target.NeonPin.Project]
	if !ok {
		return "", false, nil
	}
	branch, ok := project.Branches[target.NeonPin.BranchID]
	if !ok || strings.TrimSpace(branch.ComputeContainer) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(branch.ComputeContainer), true, nil
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
			if strings.TrimSpace(svc.Kind) == "neon" {
				var session *localagent.Session
				if firstNonEmpty(strings.TrimSpace(svc.BranchPolicy), neonDefaultBranchPolicy) == "session" {
					active, err := currentAgentSessionForAppRoot(ctx, appRoot)
					if err != nil {
						return "", err
					}
					session = active
				}
				dsn, err := resolveNeonBranchDatabaseURL(ctx, appRoot, cfg, session)
				if err != nil {
					return "", fmt.Errorf("dev.services.postgres kind %q could not resolve Neon branch connection: %w", svc.Kind, err)
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
				return dbSnapshotOptions{}, fmt.Errorf("missing snapshot name; expected: onlava db snapshot %s <name> [--app-root <path>]", opts.Action)
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
		return dbSnapshotOptions{}, fmt.Errorf("missing db snapshot action; expected: onlava db snapshot create <name> [--app-root <path>] or onlava db snapshot restore <name> [--app-root <path>]")
	}
	if opts.Name == "" {
		return dbSnapshotOptions{}, fmt.Errorf("missing snapshot name; expected: onlava db snapshot %s <name> [--app-root <path>]", opts.Action)
	}
	return opts, nil
}

func managedPostgresSnapshotPath(appRoot, sessionID, name string) (string, error) {
	label := localagentLabel(name)
	if label == "" {
		return "", fmt.Errorf("snapshot name must contain at least one letter or digit")
	}
	return filepath.Join(appRoot, ".onlava", "sessions", sessionID, "db", "snapshots", label+".sql"), nil
}
