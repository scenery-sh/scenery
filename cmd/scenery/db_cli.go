package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

type dbCLIOptions struct {
	AppRoot string
	Service string
	Args    []string
	JSON    bool
	Yes     bool
}

func dbCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery db list|shell|apply|seed|setup|reset|drop|server [--app-root <path>]")
	}
	switch args[0] {
	case "list":
		return dbListCommand(args[1:])
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
	return runDBApplyWithHooks(ctx, stdout, args, defaultLifecycleHooks())
}

func runDBApplyWithHooks(ctx context.Context, stdout io.Writer, args []string, hooks lifecycleHooks) error {
	opts, err := parseDBApplyArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	if err := runDatabaseApplyCommandWithHooks(ctx, appRoot, cfg, cfg.Database.Apply, hooks); err != nil {
		return err
	}
	result := buildDBApplyResult(appRoot, cfg)
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	fmt.Fprintln(stdout, "scenery: database apply complete")
	return nil
}

func dbSyncCommandWithHooks(args []string, hooks lifecycleHooks) error {
	opts, err := parseDBResetArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := runDatabaseApplyCommandWithHooks(ctx, appRoot, cfg, cfg.Database.Apply, hooks); err != nil {
		return err
	}
	if sqlcPlan, ok, err := buildSQLCGeneratorPlan(appRoot, cfg); err != nil {
		return err
	} else if ok {
		return runSQLCGeneratorWithHooks(ctx, os.Stdout, appRoot, sqlcPlan, false, hooks)
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

func runDatabaseApplyCommandWithHooks(ctx context.Context, appRoot string, cfg appcfg.Config, apply appcfg.DatabaseApplyConfig, hooks lifecycleHooks) error {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	env, err = managedDatabaseLifecycleEnv(ctx, appRoot, cfg, env)
	if err != nil {
		return err
	}
	return runDatabaseApplyCommandWithEnvHooks(ctx, appRoot, apply, env, hooks)
}

func runDatabaseApplyCommandWithEnvHooks(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string, hooks lifecycleHooks) error {
	return runDatabaseApplyCommandWithEnvIOHooks(ctx, appRoot, apply, env, os.Stdout, os.Stderr, hooks)
}

func runDatabaseApplyCommandWithEnvIO(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string, stdout, stderr io.Writer) error {
	return runDatabaseApplyCommandWithEnvIOHooks(ctx, appRoot, apply, env, stdout, stderr, defaultLifecycleHooks())
}

func runDatabaseApplyCommandWithEnvIOHooks(ctx context.Context, appRoot string, apply appcfg.DatabaseApplyConfig, env []string, stdout, stderr io.Writer, hooks lifecycleHooks) error {
	command := strings.TrimSpace(apply.Command)
	if command == "" {
		return fmt.Errorf("database.apply is not configured")
	}
	program, args := shellInvocation(command)
	hooks = hooks.withDefaults()
	return hooks.runExec(ctx, lifecycleExecRequest{
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
	opts, err := parseDBCLIArgs(args, false)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	database, err := resolvePostgresDatabaseForCLI(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(database.Database) == "" {
		return fmt.Errorf("no database dev.services are configured")
	}
	record := databaseListRecordFromDatabase(ctx, database)
	if opts.JSON {
		return writeInspectJSON(os.Stdout, databaseListResponse{SchemaVersion: "scenery.db.list.v3", Database: record})
	}
	fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", record.Name, record.Source, record.URL)
	for _, schema := range record.Schemas {
		fmt.Fprintf(os.Stdout, "schema\t%s\t%s\n", schema.Service, schema.Schema)
	}
	return nil
}

func dbShellCommand(args []string) error {
	opts, err := parseDBCLIArgs(args, true)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	database, err := resolvePostgresDatabaseForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	program, err := exec.LookPath("psql")
	if err != nil {
		return fmt.Errorf("psql not found in PATH; cannot open postgres database %s", database.Database)
	}
	cmd := exec.Command(program, append([]string{database.URL}, opts.Args...)...)
	cmd.Dir = appRoot
	cmd.Env = envpolicy.Environ()
	if schema, ok := databaseSchemaByService(database, opts.Service); ok {
		cmd.Env = overlayEnv(cmd.Env, map[string]string{"PGOPTIONS": "-c search_path=" + schema + ",scenery"})
	} else if strings.TrimSpace(opts.Service) != "" {
		return fmt.Errorf("database service %q is not configured", opts.Service)
	}
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
	database, err := resolvePostgresDatabaseForCLI(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.Service) != "" {
		return fmt.Errorf("database service %q is not configured", opts.Service)
	}
	if err := dropPostgresDatabase(ctx, database, opts); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "dropped scenery database")
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
	database, err := resolvePostgresDatabaseForCLI(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	if err := resetPostgresDatabase(ctx, database, opts); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "reset scenery database")
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
	opts := dbServerOptions{}
	flags := newCLIFlagSet("db server")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.Yes, "yes", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return dbServerOptions{}, err
	}
	if len(positionals) == 0 {
		return dbServerOptions{}, fmt.Errorf("usage: scenery db server status|start|stop|logs [-o json] [--yes]")
	}
	opts.Action = positionals[0]
	if len(positionals) > 1 {
		return dbServerOptions{}, fmt.Errorf("unknown argument %q", positionals[1])
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
	if svc, ok := cfg.DatabaseService("db"); ok {
		return databaseURLFromEnvList(env, appDatabaseURLEnv, postgresname.ServiceDatabaseURLEnv(svc.Name), "postgres", svc.Name)
	}
	services := cfg.DatabaseServices()
	if len(services) != 1 {
		return "", fmt.Errorf("database service name is required when %d services are configured", len(services))
	}
	return databaseURLFromEnvList(env, appDatabaseURLEnv, postgresname.ServiceDatabaseURLEnv(services[0].Name), "postgres", services[0].Name)
}

func resolveDatabaseURLForServiceFromEnv(cfg appcfg.Config, env []string, service string) (string, error) {
	service = strings.TrimSpace(service)
	if service != "" && service != "." {
		if svc, ok := cfg.DatabaseService(service); ok {
			return databaseURLFromEnvList(env, appDatabaseURLEnv, postgresname.ServiceDatabaseURLEnv(svc.Name), "postgres", svc.Name)
		}
		if len(cfg.DatabaseServices()) > 1 {
			return "", fmt.Errorf("seed service %q has no matching database service; configure dev.services.%s or use a single database service", service, service)
		}
	}
	return resolveDatabaseURLForConfigFromEnv(cfg, env)
}

func databaseURLFromEnvList(env []string, appEnvName, serviceEnvName, engine, service string) (string, error) {
	for _, key := range []string{serviceEnvName, appEnvName} {
		if value, _ := lookupEnvValue(env, key); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s service %q database URL is not configured; set %s", engine, service, appEnvName)
}

func managedDatabaseLifecycleEnv(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string) ([]string, error) {
	env, _, err := managedDatabaseEnv(ctx, appRoot, cfg, nil, baseEnv)
	if err != nil {
		return nil, err
	}
	if len(env) == 0 {
		return baseEnv, nil
	}
	keys := databaseEnvKeys(cfg)
	return overlayEnv(envWithoutKeys(baseEnv, keys...), envMap(env)), nil
}

func resolvePostgresDatabaseForCLI(ctx context.Context, appRoot string, cfg appcfg.Config) (postgresdb.Database, error) {
	if len(cfg.DatabaseServices()) == 0 {
		return postgresdb.Database{}, nil
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return postgresdb.Database{}, err
	}
	_, database, err := managedDatabaseEnv(ctx, appRoot, cfg, nil, baseEnv)
	return database, err
}

func databaseSchemaByService(database postgresdb.Database, service string) (string, bool) {
	service = strings.TrimSpace(service)
	if service == "" {
		return "", false
	}
	for _, schema := range database.Schemas {
		if schema.Name == service {
			return schema.Schema, true
		}
	}
	return "", false
}

func resetPostgresDatabase(ctx context.Context, database postgresdb.Database, opts dbCLIOptions) error {
	if strings.TrimSpace(database.Database) == "" {
		return nil
	}
	if database.Source == postgresdb.SourceExternal {
		return fmt.Errorf("refusing to reset external postgres database")
	}
	if strings.TrimSpace(opts.Service) != "" {
		schema, ok := databaseSchemaByService(database, opts.Service)
		if !ok {
			return fmt.Errorf("database service %q is not configured", opts.Service)
		}
		db, err := openPostgresDatabase(ctx, database.URL)
		if err != nil {
			return err
		}
		defer db.Close()
		return postgresdb.ResetSchema(ctx, db, schema)
	}
	if !opts.Yes {
		return fmt.Errorf("resetting the managed postgres app database requires --yes")
	}
	admin, err := managedPostgresAdmin(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	return postgresdb.ResetDatabase(ctx, admin, database.Database)
}

func dropPostgresDatabase(ctx context.Context, database postgresdb.Database, opts dbCLIOptions) error {
	if strings.TrimSpace(database.Database) == "" {
		return nil
	}
	if database.Source == postgresdb.SourceExternal {
		return fmt.Errorf("refusing to drop external postgres database")
	}
	admin, err := managedPostgresAdmin(ctx)
	if err != nil {
		return err
	}
	defer admin.Close()
	return postgresdb.DropDatabase(ctx, admin, database.Database)
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

func databaseEnvKeys(cfg appcfg.Config) []string {
	keys := []string{appDatabaseURLEnv, postgresdb.RegistryEnv}
	for _, svc := range cfg.DatabaseServices() {
		keys = append(keys, postgresname.ServiceDatabaseURLEnv(svc.Name))
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

type databaseListResponse struct {
	SchemaVersion string             `json:"schema_version"`
	Database      databaseListRecord `json:"database"`
}

type databaseListRecord struct {
	Name      string                     `json:"name"`
	URL       string                     `json:"url"`
	Source    string                     `json:"source"`
	SizeBytes int64                      `json:"size_bytes,omitempty"`
	Schemas   []databaseListSchemaRecord `json:"schemas"`
}

type databaseListSchemaRecord struct {
	Service string `json:"service"`
	Schema  string `json:"schema"`
	URL     string `json:"url,omitempty"`
}

func databaseListRecordFromDatabase(ctx context.Context, database postgresdb.Database) databaseListRecord {
	record := databaseListRecord{
		Name:   database.Database,
		URL:    postgresdb.RedactURL(database.URL),
		Source: string(database.Source),
	}
	for _, schema := range database.Schemas {
		record.Schemas = append(record.Schemas, databaseListSchemaRecord{
			Service: schema.Name,
			Schema:  schema.Schema,
			URL:     postgresdb.RedactURL(schema.URL),
		})
	}
	if db, err := openPostgresDatabase(ctx, database.URL); err == nil {
		_ = db.QueryRowContext(ctx, `select pg_database_size(current_database())`).Scan(&record.SizeBytes)
		_ = db.Close()
	}
	return record
}

func parseDBCLIArgs(args []string, serviceRequired bool) (dbCLIOptions, error) {
	var opts dbCLIOptions
	flags := newCLIFlagSet("db")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.Yes, "yes", false, "")
	rest, err := parseLeadingCLIFlags(flags, args)
	if err != nil {
		return dbCLIOptions{}, err
	}
	if len(rest) > 0 {
		opts.Service = rest[0]
		opts.Args = append(opts.Args, rest[1:]...)
	}
	if serviceRequired && opts.Service == "" {
		opts.Args = nil
	}
	return opts, nil
}

func parseDBTargetArgs(args []string) (dbCLIOptions, error) {
	var opts dbCLIOptions
	flags := newCLIFlagSet("db")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.Yes, "yes", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return dbCLIOptions{}, err
	}
	if len(positionals) > 0 {
		opts.Service = positionals[0]
	}
	if len(positionals) > 1 {
		return dbCLIOptions{}, fmt.Errorf("unexpected argument %q", positionals[1])
	}
	return opts, nil
}

func parseDBResetArgs(args []string) (dbResetOptions, error) {
	var opts dbResetOptions
	flags := newCLIFlagSet("db reset")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return dbResetOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return dbResetOptions{}, err
	}
	return opts, nil
}

func parseDBApplyArgs(args []string) (dbApplyOptions, error) {
	var opts dbApplyOptions
	flags := newCLIFlagSet("db apply")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return dbApplyOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return dbApplyOptions{}, err
	}
	return opts, nil
}
