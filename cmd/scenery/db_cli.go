package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/sqlitedb"
)

type sqliteDBOptions struct {
	AppRoot string
	Service string
	Args    []string
	JSON    bool
	Yes     bool
}

func dbCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery db list|path|shell|apply|seed|setup|reset|drop|snapshot|diff|branch|server [--app-root <path>]")
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
	case "server":
		return dbServerCommand(args[1:])
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
	ctx := context.Background()
	sqliteServices, err := resolveSQLiteServicesForCLIOptional(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	postgresServices, err := resolvePostgresServicesForCLI(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	if len(sqliteServices)+len(postgresServices) == 0 {
		return fmt.Errorf("no database dev.services are configured")
	}
	records := databaseListRecords(sqliteServices, postgresServices)
	if opts.JSON {
		return writeInspectJSON(os.Stdout, databaseListResponse{SchemaVersion: "scenery.db.list.v2", Databases: records})
	}
	for _, db := range records {
		target := db.Path
		if target == "" {
			target = db.URL
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", db.Service, db.Engine, target)
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
	postgresServices := cfg.PostgresServices()
	if _, ok := cfg.PostgresService(opts.Service); ok || opts.Service == "" && len(postgresServices) == 1 && len(cfg.SQLiteServices()) == 0 {
		return fmt.Errorf("postgres services have no file path; use `scenery db list --json` or `scenery db shell`")
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
	if svc, ok, err := resolvePostgresServiceForCLI(context.Background(), appRoot, cfg, opts.Service); err != nil {
		return err
	} else if ok {
		program, err := exec.LookPath("psql")
		if err != nil {
			return fmt.Errorf("psql not found in PATH; cannot open postgres database %s", svc.Database)
		}
		cmd := exec.Command(program, append([]string{svc.URL}, opts.Args...)...)
		cmd.Dir = appRoot
		cmd.Env = envpolicy.Environ()
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
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
	opts, err := parseDBTargetArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	var services []sqlitedb.Service
	if shouldResolveSQLiteForDBTarget(cfg, opts.Service) {
		services, err = resolveSQLiteServicesForCLIOptional(ctx, appRoot, cfg)
		if err != nil {
			return err
		}
	}
	var postgresServices []postgresdb.Service
	if shouldResolvePostgresForDBTarget(cfg, opts.Service) {
		postgresServices, err = resolvePostgresServicesForCLI(ctx, appRoot, cfg)
		if err != nil {
			return err
		}
	}
	sqliteTargets := filterSQLiteServices(services, opts.Service)
	postgresTargets := filterPostgresServices(postgresServices, opts.Service)
	if strings.TrimSpace(opts.Service) != "" && len(sqliteTargets)+len(postgresTargets) == 0 {
		return fmt.Errorf("database service %q is not configured", opts.Service)
	}
	for _, svc := range sqliteTargets {
		if err := os.Remove(svc.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := dropPostgresServices(ctx, postgresTargets, opts); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "dropped scenery database services")
	return nil
}

func dbResetCommand(args []string) error {
	opts, err := parseDBTargetArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	var services []sqlitedb.Service
	if shouldResolveSQLiteForDBTarget(cfg, opts.Service) {
		services, err = resolveSQLiteServicesForCLIOptional(ctx, appRoot, cfg)
		if err != nil {
			return err
		}
	}
	var postgresServices []postgresdb.Service
	if shouldResolvePostgresForDBTarget(cfg, opts.Service) {
		postgresServices, err = resolvePostgresServicesForCLI(ctx, appRoot, cfg)
		if err != nil {
			return err
		}
	}
	sqliteTargets := filterSQLiteServices(services, opts.Service)
	postgresTargets := filterPostgresServices(postgresServices, opts.Service)
	if strings.TrimSpace(opts.Service) != "" && len(sqliteTargets)+len(postgresTargets) == 0 {
		return fmt.Errorf("database service %q is not configured", opts.Service)
	}
	for _, svc := range sqliteTargets {
		if err := os.Remove(svc.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := sqlitedb.EnsureFiles(ctx, []sqlitedb.Service{svc}); err != nil {
			return err
		}
	}
	if err := resetPostgresServices(ctx, postgresTargets, opts); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "reset scenery database services")
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
	sqliteServices, err := resolveSQLiteServicesForCLIOptional(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	postgresServices, err := resolvePostgresServicesForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	dir := filepath.Join(appRoot, ".scenery", "db", "snapshots", opts.Name)
	switch opts.Action {
	case "create":
		if err := sqlitedb.Snapshot(context.Background(), sqliteServices, dir); err != nil {
			return err
		}
		if err := snapshotPostgresServices(context.Background(), postgresServices, dir); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "created scenery database snapshot %s at %s\n", opts.Name, dir)
	case "restore":
		if len(postgresServices) > 0 && !opts.Yes {
			return fmt.Errorf("postgres snapshot restore requires --yes")
		}
		for _, svc := range sqliteServices {
			source := filepath.Join(dir, filepath.Base(svc.Path))
			if _, err := os.Stat(source); err != nil {
				return err
			}
			if err := sqlitedb.Backup(context.Background(), source, svc.Path); err != nil {
				return err
			}
		}
		if err := restorePostgresServices(context.Background(), postgresServices, dir); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "restored scenery database snapshot %s from %s\n", opts.Name, dir)
	default:
		return fmt.Errorf("unknown db snapshot action %q", opts.Action)
	}
	return nil
}

type dbServerOptions struct {
	Action string
	JSON   bool
	Yes    bool
}

type dbServerStatusResponse struct {
	SchemaVersion string                               `json:"schema_version"`
	OK            bool                                 `json:"ok"`
	Container     string                               `json:"container"`
	Image         string                               `json:"image,omitempty"`
	Status        string                               `json:"status"`
	Port          int                                  `json:"port,omitempty"`
	URL           string                               `json:"url,omitempty"`
	Databases     []postgresdb.DatabaseInfo            `json:"databases,omitempty"`
	Leases        map[string]localagent.SubstrateLease `json:"leases,omitempty"`
	StatePath     string                               `json:"state_path,omitempty"`
}

func dbServerCommand(args []string) error {
	opts, err := parseDBServerArgs(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch opts.Action {
	case "status":
		status, err := postgresServerStatus(ctx)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(os.Stdout, status)
		}
		fmt.Fprintf(os.Stdout, "%s\t%s\n", status.Container, status.Status)
		if status.Port > 0 {
			fmt.Fprintf(os.Stdout, "port\t%d\n", status.Port)
		}
		return nil
	case "start":
		if _, err := ensureSharedPostgresServer(ctx, "", nil); err != nil {
			return err
		}
		status, err := postgresServerStatus(ctx)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(os.Stdout, status)
		}
		fmt.Fprintln(os.Stdout, "started scenery postgres server")
		return nil
	case "stop":
		if err := stopSharedPostgresServer(ctx, opts.Yes); err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(os.Stdout, map[string]any{"schema_version": "scenery.db.server.stop.v1", "ok": true, "container": postgresServerContainer})
		}
		fmt.Fprintln(os.Stdout, "stopped scenery postgres server")
		return nil
	case "logs":
		out, err := postgresDocker.Run(ctx, "logs", "--tail", "200", postgresServerContainer)
		if out != "" {
			fmt.Fprintln(os.Stdout, out)
		}
		return err
	default:
		return fmt.Errorf("unknown db server command %q", opts.Action)
	}
}

func parseDBServerArgs(args []string) (dbServerOptions, error) {
	if len(args) == 0 {
		return dbServerOptions{}, fmt.Errorf("usage: scenery db server status|start|stop|logs [--json] [--yes]")
	}
	opts := dbServerOptions{Action: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--yes":
			opts.Yes = true
		default:
			return dbServerOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func postgresServerStatus(ctx context.Context) (dbServerStatusResponse, error) {
	resp := dbServerStatusResponse{SchemaVersion: "scenery.db.server.status.v1", Container: postgresServerContainer, Status: "absent"}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return resp, err
	}
	resp.StatePath = postgresServerStatePath(paths)
	if data, err := os.ReadFile(resp.StatePath); err == nil {
		var state postgresServerState
		if err := json.Unmarshal(data, &state); err != nil {
			return resp, err
		}
		resp.Image = state.Image
		resp.Port = state.Port
		resp.URL = state.publicURL()
		if status, err := postgresContainerStatus(ctx, state.Container); err == nil && status != "" {
			resp.Status = status
			resp.OK = status == "running"
		}
		if resp.OK {
			if admin, err := openPostgresAdmin(ctx, state.databaseURL("postgres")); err == nil {
				resp.Databases, _ = postgresdb.ListSceneryDatabases(ctx, admin)
				_ = admin.Close()
			}
		}
	}
	if client, err := localagent.DefaultClient(); err == nil {
		if substrate, err := client.GetSubstrate(ctx, localagent.SubstratePostgres); err == nil {
			resp.Leases = substrate.Leases
		}
	}
	return resp, nil
}

func stopSharedPostgresServer(ctx context.Context, yes bool) error {
	status, err := postgresServerStatus(ctx)
	if err != nil {
		return err
	}
	if len(status.Leases) > 0 && !yes {
		return fmt.Errorf("postgres server has %d lease(s); pass --yes to stop anyway", len(status.Leases))
	}
	if _, err := postgresDocker.Run(ctx, "stop", postgresServerContainer); err != nil {
		return err
	}
	if client, err := localagent.DefaultClient(); err == nil {
		_, _ = client.DeleteSubstrate(ctx, localagent.SubstratePostgres)
	}
	return nil
}

func resolveDatabaseURLForConfig(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string, useManaged bool) (string, error) {
	env := baseEnv
	if useManaged {
		var err error
		env, err = managedDatabaseLifecycleEnv(ctx, appRoot, cfg, baseEnv)
		if err != nil {
			return "", err
		}
	}
	return resolveDatabaseURLForConfigFromEnv(cfg, env)
}

func resolveDatabaseURLForConfigFromEnv(cfg appcfg.Config, env []string) (string, error) {
	if svc, ok := cfg.PostgresService("db"); ok {
		return databaseURLFromEnvList(env, svc.DatabaseURLEnv, "postgres", svc.Name)
	}
	if svc, ok := cfg.SQLiteService("db"); ok {
		return databaseURLFromEnvList(env, svc.DatabaseURLEnv, "sqlite", svc.Name)
	}
	sqliteServices := cfg.SQLiteServices()
	postgresServices := cfg.PostgresServices()
	total := len(sqliteServices) + len(postgresServices)
	if total != 1 {
		return "", fmt.Errorf("database service name is required when %d services are configured", total)
	}
	if len(postgresServices) == 1 {
		return databaseURLFromEnvList(env, postgresServices[0].DatabaseURLEnv, "postgres", postgresServices[0].Name)
	}
	return databaseURLFromEnvList(env, sqliteServices[0].DatabaseURLEnv, "sqlite", sqliteServices[0].Name)
}

func resolveDatabaseURLForServiceFromEnv(cfg appcfg.Config, env []string, service string) (string, error) {
	service = strings.TrimSpace(service)
	if service != "" && service != "." {
		if svc, ok := cfg.PostgresService(service); ok {
			return databaseURLFromEnvList(env, svc.DatabaseURLEnv, "postgres", svc.Name)
		}
		if svc, ok := cfg.SQLiteService(service); ok {
			return databaseURLFromEnvList(env, svc.DatabaseURLEnv, "sqlite", svc.Name)
		}
		if len(cfg.SQLiteServices())+len(cfg.PostgresServices()) > 1 {
			return "", fmt.Errorf("seed service %q has no matching database service; configure dev.services.%s or use a single database service", service, service)
		}
	}
	return resolveDatabaseURLForConfigFromEnv(cfg, env)
}

func databaseURLFromEnvList(env []string, envName, engine, service string) (string, error) {
	for _, key := range []string{envName, appDatabaseURLEnv} {
		if value, _ := lookupEnvValue(env, key); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s service %q database URL is not configured; set %s", engine, service, envName)
}

func managedDatabaseLifecycleEnv(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string) ([]string, error) {
	env, _, err := managedSQLiteEnv(ctx, appRoot, cfg, nil)
	if err != nil {
		return nil, err
	}
	postgresEnv, _, err := managedPostgresEnv(ctx, appRoot, cfg, nil, baseEnv)
	if err != nil {
		return nil, err
	}
	env = append(env, postgresEnv...)
	if len(env) == 0 {
		return baseEnv, nil
	}
	keys := append(sqliteEnvKeys(cfg), postgresEnvKeys(cfg)...)
	return overlayEnv(envWithoutKeys(baseEnv, keys...), envMap(env)), nil
}

func resolveSQLiteServicesForCLI(ctx context.Context, appRoot string, cfg appcfg.Config) ([]sqlitedb.Service, error) {
	services, err := resolveSQLiteServicesForCLIOptional(ctx, appRoot, cfg)
	if err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("no sqlite dev.services are configured")
	}
	return services, nil
}

func resolveSQLiteServicesForCLIOptional(ctx context.Context, appRoot string, cfg appcfg.Config) ([]sqlitedb.Service, error) {
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{AppRoot: appRoot, Config: cfg, Mode: sqlitedb.ModeLocal})
	if err != nil {
		return nil, err
	}
	if err := sqlitedb.EnsureFiles(ctx, services); err != nil {
		return nil, err
	}
	return services, nil
}

func resolvePostgresServicesForCLI(ctx context.Context, appRoot string, cfg appcfg.Config) ([]postgresdb.Service, error) {
	if len(cfg.PostgresServices()) == 0 {
		return nil, nil
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return nil, err
	}
	_, services, err := managedPostgresEnv(ctx, appRoot, cfg, nil, baseEnv)
	return services, err
}

func resolvePostgresServiceForCLI(ctx context.Context, appRoot string, cfg appcfg.Config, name string) (postgresdb.Service, bool, error) {
	services, err := resolvePostgresServicesForCLI(ctx, appRoot, cfg)
	if err != nil {
		return postgresdb.Service{}, false, err
	}
	if name == "" && len(services) == 1 && len(cfg.SQLiteServices()) == 0 {
		return services[0], true, nil
	}
	for _, svc := range services {
		if svc.Name == name {
			return svc, true, nil
		}
	}
	return postgresdb.Service{}, false, nil
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

func filterPostgresServices(services []postgresdb.Service, name string) []postgresdb.Service {
	if strings.TrimSpace(name) == "" {
		return services
	}
	for _, svc := range services {
		if svc.Name == name {
			return []postgresdb.Service{svc}
		}
	}
	return nil
}

func shouldResolveSQLiteForDBTarget(cfg appcfg.Config, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	if _, ok := cfg.PostgresService(name); ok {
		return false
	}
	return true
}

func shouldResolvePostgresForDBTarget(cfg appcfg.Config, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return len(cfg.PostgresServices()) > 0
	}
	_, ok := cfg.PostgresService(name)
	return ok
}

func databaseListRecords(sqliteServices []sqlitedb.Service, postgresServices []postgresdb.Service) []databaseListRecord {
	records := make([]databaseListRecord, 0, len(sqliteServices)+len(postgresServices))
	for _, svc := range sqliteServices {
		records = append(records, databaseListRecord{
			Engine:         "sqlite",
			Service:        svc.Name,
			Path:           svc.Path,
			FileLabel:      svc.FileLabel,
			URL:            svc.URL,
			DatabaseURLEnv: svc.DatabaseURLEnv,
			Source:         "managed",
		})
	}
	for _, svc := range postgresServices {
		records = append(records, databaseListRecord{
			Engine:         "postgres",
			Service:        svc.Name,
			Database:       svc.Database,
			URL:            postgresdb.RedactURL(svc.URL),
			DatabaseURLEnv: svc.DatabaseURLEnv,
			Source:         string(svc.Source),
		})
	}
	return records
}

func resetPostgresServices(ctx context.Context, services []postgresdb.Service, opts sqliteDBOptions) error {
	targets := filterPostgresServices(services, opts.Service)
	if len(targets) == 0 {
		return nil
	}
	if len(targets) > 1 && !opts.Yes {
		return fmt.Errorf("resetting multiple postgres services requires --yes")
	}
	admin, err := managedPostgresAdmin(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	for _, svc := range targets {
		if svc.Source == postgresdb.SourceExternal {
			return fmt.Errorf("refusing to reset external postgres service %s from %s", svc.Name, svc.DatabaseURLEnv)
		}
		if err := postgresdb.ResetDatabase(ctx, admin, svc.Database); err != nil {
			return err
		}
	}
	return nil
}

func dropPostgresServices(ctx context.Context, services []postgresdb.Service, opts sqliteDBOptions) error {
	targets := filterPostgresServices(services, opts.Service)
	if len(targets) == 0 {
		return nil
	}
	if len(targets) > 1 && !opts.Yes {
		return fmt.Errorf("dropping multiple postgres services requires --yes")
	}
	admin, err := managedPostgresAdmin(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	for _, svc := range targets {
		if svc.Source == postgresdb.SourceExternal {
			return fmt.Errorf("refusing to drop external postgres service %s from %s", svc.Name, svc.DatabaseURLEnv)
		}
		if err := postgresdb.DropDatabase(ctx, admin, svc.Database); err != nil {
			return err
		}
	}
	return nil
}

func snapshotPostgresServices(ctx context.Context, services []postgresdb.Service, dir string) error {
	if len(services) == 0 {
		return nil
	}
	program, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf("pg_dump not found in PATH; cannot snapshot postgres services")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, svc := range services {
		cmd := exec.CommandContext(ctx, program, "-Fc", "-f", filepath.Join(dir, svc.Name+".postgres.dump"), svc.URL)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	return nil
}

func restorePostgresServices(ctx context.Context, services []postgresdb.Service, dir string) error {
	if len(services) == 0 {
		return nil
	}
	program, err := exec.LookPath("pg_restore")
	if err != nil {
		return fmt.Errorf("pg_restore not found in PATH; cannot restore postgres services")
	}
	for _, svc := range services {
		source := filepath.Join(dir, svc.Name+".postgres.dump")
		if _, err := os.Stat(source); err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, program, "--clean", "--if-exists", "-d", svc.URL, source)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	return nil
}

func managedPostgresAdmin(ctx context.Context) (*sql.DB, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(postgresServerStatePath(paths))
	if err != nil {
		return nil, err
	}
	var state postgresServerState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return openPostgresAdmin(ctx, state.databaseURL("postgres"))
}

func sqliteEnvKeys(cfg appcfg.Config) []string {
	keys := []string{appDatabaseURLEnv, legacyDatabaseURLEnv, "SCENERY_SQLITE_DATABASES_JSON"}
	for _, svc := range cfg.SQLiteServices() {
		keys = append(keys, svc.DatabaseURLEnv, svc.DatabasePathEnv)
	}
	return keys
}

func postgresEnvKeys(cfg appcfg.Config) []string {
	keys := []string{postgresdb.RegistryEnv}
	for _, svc := range cfg.PostgresServices() {
		keys = append(keys, svc.DatabaseURLEnv)
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
	Yes     bool
}

type databaseListResponse struct {
	SchemaVersion string               `json:"schema_version"`
	Databases     []databaseListRecord `json:"databases"`
}

type databaseListRecord struct {
	Engine         string `json:"engine"`
	Service        string `json:"service"`
	Path           string `json:"path,omitempty"`
	FileLabel      string `json:"file_label,omitempty"`
	Database       string `json:"database,omitempty"`
	URL            string `json:"url,omitempty"`
	DatabaseURLEnv string `json:"database_url_env"`
	Source         string `json:"source,omitempty"`
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
		case "--yes":
			opts.Yes = true
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

func parseDBTargetArgs(args []string) (sqliteDBOptions, error) {
	var opts sqliteDBOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return sqliteDBOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--yes":
			opts.Yes = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return sqliteDBOptions{}, fmt.Errorf("unknown flag %q", args[i])
			}
			if opts.Service != "" {
				return sqliteDBOptions{}, fmt.Errorf("unexpected argument %q", args[i])
			}
			opts.Service = args[i]
		}
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
		case "--yes":
			opts.Yes = true
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
