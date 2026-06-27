package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/sqlitedb"
)

type sqliteDBOptions struct {
	AppRoot string
	Service string
	Args    []string
	JSON    bool
}

func dbCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery db list|path|shell|apply|seed|setup|reset|drop|snapshot|diff|branch [--app-root <path>]")
	}
	switch args[0] {
	case "list":
		return dbListCommand(args[1:])
	case "path":
		return dbPathCommand(args[1:])
	case "shell":
		return dbShellCommand(args[1:])
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
	case "diff":
		return runDBGeneratedDiff(os.Stdout, args[1:])
	case "branch":
		return dbBranchCommand(args[1:])
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
	if err := runDatabaseApplyCommand(ctx, appRoot, cfg, cfg.Database.Apply); err != nil {
		return err
	}
	result := buildDBApplyResult(appRoot, cfg)
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintln(stdout, "scenery: database apply complete")
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
	if err := runDatabaseApplyCommand(ctx, appRoot, cfg, cfg.Database.Apply); err != nil {
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
	Command string `json:"command,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	Status  string `json:"status"`
}

func buildDBApplyResult(appRoot string, cfg appcfg.Config) dbApplyResult {
	return dbApplyResult{
		SchemaVersion: "scenery.db.apply.result.v1",
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: cfg.SourcePath(appRoot),
		},
		Apply: dbApplyRecord{
			Command: cfg.Database.Apply.Command,
			CWD:     cfg.Database.Apply.CWD,
			Status:  "applied",
		},
	}
}

func runDatabaseApplyCommand(ctx context.Context, appRoot string, cfg appcfg.Config, apply appcfg.DatabaseApplyConfig) error {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	env, err = managedDatabaseLifecycleEnv(ctx, appRoot, cfg, env)
	if err != nil {
		return err
	}
	return runDatabaseApplyCommandWithEnv(ctx, appRoot, apply, env)
}

func runDatabaseApplyCommandWithEnv(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string) error {
	return runDatabaseApplyCommandWithEnvIO(ctx, appRoot, apply, env, os.Stdout, os.Stderr)
}

func runDatabaseApplyCommandWithEnvIO(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string, stdout, stderr io.Writer) error {
	command := strings.TrimSpace(apply.Command)
	if command == "" {
		return fmt.Errorf("database.apply is not configured")
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

func dbListCommand(args []string) error {
	opts, err := parseSQLiteDBArgs(args, false)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	services, err := resolveSQLiteServicesForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{
			"schema_version": "scenery.db.sqlite.list.v1",
			"databases":      services,
		})
	}
	for _, svc := range services {
		fmt.Fprintf(os.Stdout, "%s\t%s\n", svc.Name, svc.Path)
	}
	return nil
}

func dbPathCommand(args []string) error {
	opts, err := parseSQLiteDBArgs(args, true)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	svc, err := resolveSQLiteServiceForCLI(context.Background(), appRoot, cfg, opts.Service)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, svc.Path)
	return nil
}

func dbShellCommand(args []string) error {
	opts, err := parseSQLiteDBArgs(args, true)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	svc, err := resolveSQLiteServiceForCLI(context.Background(), appRoot, cfg, opts.Service)
	if err != nil {
		return err
	}
	program, err := exec.LookPath("sqlite3")
	if err != nil {
		return fmt.Errorf("sqlite3 not found in PATH")
	}
	cmd := exec.Command(program, append([]string{svc.Path}, opts.Args...)...)
	cmd.Dir = appRoot
	cmd.Env = envpolicy.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func dbDropCommand(args []string) error {
	opts, err := parseSQLiteDBArgs(args, false)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	services, err := resolveSQLiteServicesForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	for _, svc := range filterSQLiteServices(services, opts.Service) {
		if err := os.Remove(svc.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, "dropped scenery sqlite database files")
	return nil
}

func dbResetCommand(args []string) error {
	opts, err := parseSQLiteDBArgs(args, false)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	services, err := resolveSQLiteServicesForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	for _, svc := range filterSQLiteServices(services, opts.Service) {
		if err := os.Remove(svc.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := sqlitedb.EnsureFiles(context.Background(), []sqlitedb.Service{svc}); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout, "reset scenery sqlite database files")
	return nil
}

func dbSnapshotCommand(args []string) error {
	opts, err := parseDBSnapshotArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	services, err := resolveSQLiteServicesForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	dir := filepath.Join(appRoot, ".scenery", "db", "snapshots", opts.Name)
	switch opts.Action {
	case "create":
		if err := sqlitedb.Snapshot(context.Background(), services, dir); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "created scenery database snapshot %s at %s\n", opts.Name, dir)
	case "restore":
		for _, svc := range services {
			source := filepath.Join(dir, filepath.Base(svc.Path))
			if _, err := os.Stat(source); err != nil {
				return err
			}
			if err := sqlitedb.Backup(context.Background(), source, svc.Path); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stdout, "restored scenery database snapshot %s from %s\n", opts.Name, dir)
	default:
		return fmt.Errorf("unknown db snapshot action %q", opts.Action)
	}
	return nil
}

func resolveDatabaseURLForConfig(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string, useManaged bool) (string, error) {
	env, err := managedDatabaseLifecycleEnv(ctx, appRoot, cfg, baseEnv)
	if err != nil {
		return "", err
	}
	services := cfg.SQLiteServices()
	if len(services) != 1 {
		return "", fmt.Errorf("sqlite service name is required when %d services are configured", len(services))
	}
	envName := services[0].DatabaseURLEnv
	if value, _ := lookupEnvValue(env, envName); value != "" {
		return value, nil
	}
	if value, _ := lookupEnvValue(env, appDatabaseURLEnv); value != "" {
		return value, nil
	}
	return "", fmt.Errorf("database URL is not configured; set %s", envName)
}

func managedDatabaseLifecycleEnv(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string) ([]string, error) {
	env, _, err := managedSQLiteEnv(ctx, appRoot, cfg, nil)
	if err != nil {
		return nil, err
	}
	if len(env) == 0 {
		return baseEnv, nil
	}
	return overlayEnv(envWithoutKeys(baseEnv, sqliteEnvKeys(cfg)...), envMap(env)), nil
}

func resolveSQLiteServicesForCLI(ctx context.Context, appRoot string, cfg appcfg.Config) ([]sqlitedb.Service, error) {
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{AppRoot: appRoot, Config: cfg, Mode: sqlitedb.ModeLocal})
	if err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("no sqlite dev.services are configured")
	}
	if err := sqlitedb.EnsureFiles(ctx, services); err != nil {
		return nil, err
	}
	return services, nil
}

func resolveSQLiteServiceForCLI(ctx context.Context, appRoot string, cfg appcfg.Config, name string) (sqlitedb.Service, error) {
	services, err := resolveSQLiteServicesForCLI(ctx, appRoot, cfg)
	if err != nil {
		return sqlitedb.Service{}, err
	}
	if name == "" && len(services) == 1 {
		return services[0], nil
	}
	for _, svc := range services {
		if svc.Name == name {
			return svc, nil
		}
	}
	if name == "" {
		return sqlitedb.Service{}, fmt.Errorf("sqlite service name is required")
	}
	return sqlitedb.Service{}, fmt.Errorf("sqlite service %q is not configured", name)
}

func filterSQLiteServices(services []sqlitedb.Service, name string) []sqlitedb.Service {
	if strings.TrimSpace(name) == "" {
		return services
	}
	for _, svc := range services {
		if svc.Name == name {
			return []sqlitedb.Service{svc}
		}
	}
	return nil
}

func sqliteEnvKeys(cfg appcfg.Config) []string {
	keys := []string{appDatabaseURLEnv, legacyDatabaseURLEnv, "SCENERY_SQLITE_DATABASES_JSON"}
	for _, svc := range cfg.SQLiteServices() {
		keys = append(keys, svc.DatabaseURLEnv, svc.DatabasePathEnv)
	}
	return keys
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

type dbResetOptions struct {
	AppRoot string
}

type dbSnapshotOptions struct {
	Action  string
	Name    string
	AppRoot string
}

func parseSQLiteDBArgs(args []string, serviceRequired bool) (sqliteDBOptions, error) {
	var opts sqliteDBOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return sqliteDBOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--json":
			opts.JSON = true
		default:
			if opts.Service == "" {
				opts.Service = args[i]
				opts.Args = append(opts.Args, args[i+1:]...)
				i = len(args)
				break
			}
			return sqliteDBOptions{}, fmt.Errorf("unknown argument %q", args[i])
		}
	}
	if serviceRequired && opts.Service == "" {
		opts.Args = nil
	}
	return opts, nil
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
		case "--name":
			i++
			if i >= len(args) {
				return dbSnapshotOptions{}, fmt.Errorf("missing value for --name")
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
		return dbSnapshotOptions{}, fmt.Errorf("usage: scenery db snapshot create|restore --name <name> [--app-root <path>]")
	}
	if strings.TrimSpace(opts.Name) == "" {
		return dbSnapshotOptions{}, fmt.Errorf("db snapshot requires --name")
	}
	return opts, nil
}
