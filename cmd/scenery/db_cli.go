package main

import (
	"context"
	"database/sql"
	"errors"
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
	applyStdout := stdout
	if opts.JSON {
		applyStdout = io.Discard
	}
	if err := runDatabaseApplyCommandWithOutputHooks(ctx, appRoot, cfg, cfg.Database.Apply, applyStdout, os.Stderr, hooks); err != nil {
		return err
	}
	result := buildDBApplyResult(appRoot, cfg)
	if opts.JSON {
		return writeInspectJSON(stdout, result)
	}
	_, _ = fmt.Fprintln(stdout, "scenery: database apply complete")
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
	_, _ = fmt.Fprintln(os.Stdout, "scenery: database sync complete; no sqlc generator configured")
	return nil
}

type dbApplyOptions struct {
	AppRoot string
	JSON    bool
}

type dbApplyResult struct {
	cliPayloadIdentity
	App   inspectdata.AppRef `json:"app"`
	Apply dbApplyRecord      `json:"apply"`
}

type dbApplyRecord struct {
	Command string `json:"command,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	Status  string `json:"status"`
}

func buildDBApplyResult(appRoot string, cfg appcfg.Config) dbApplyResult {
	return dbApplyResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.apply.result"),
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
	return runDatabaseApplyCommandWithOutputHooks(ctx, appRoot, cfg, apply, os.Stdout, os.Stderr, hooks)
}

func runDatabaseApplyCommandWithOutputHooks(ctx context.Context, appRoot string, cfg appcfg.Config, apply appcfg.DatabaseApplyConfig, stdout, stderr io.Writer, hooks lifecycleHooks) (returnErr error) {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	env, closeOperation, err := beginDatabaseLifecycleEnv(ctx, appRoot, cfg, env)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, closeOperation()) }()
	return runDatabaseApplyCommandWithEnvIOHooks(ctx, appRoot, apply, env, stdout, stderr, hooks)
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
		return writeInspectJSON(os.Stdout, databaseListResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.list"), Database: record})
	}
	_, _ = fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", record.Name, record.Source, record.URL)
	for _, schema := range record.Schemas {
		_, _ = fmt.Fprintf(os.Stdout, "schema\t%s\t%s\n", schema.Service, schema.Schema)
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

func dbDropCommand(args []string) (returnErr error) {
	opts, err := parseDBTargetArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if strings.TrimSpace(opts.Service) != "" {
		return fmt.Errorf("database service %q is not configured", opts.Service)
	}
	database, closeOperation, err := beginInactiveDatabaseOperation(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, closeOperation()) }()
	if err := dropPostgresDatabase(ctx, database); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, "dropped scenery database")
	return nil
}

func dbResetCommand(args []string) (returnErr error) {
	opts, err := parseDBTargetArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	var seedPlans []dbSeedPlan
	if strings.TrimSpace(opts.Service) != "" {
		seedPlans, err = discoverDBSeedPlans(appRoot, cfg)
		if err != nil {
			return err
		}
	}
	if opts.Service == "" && !opts.Yes {
		return fmt.Errorf("resetting the managed postgres app database requires --yes")
	}
	database, closeOperation, err := beginInactiveDatabaseOperation(ctx, appRoot, cfg)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, closeOperation()) }()
	if err := resetPostgresDatabase(ctx, database, opts); err != nil {
		return err
	}
	if strings.TrimSpace(opts.Service) != "" {
		if err := deleteDBSeedLedgerForService(ctx, database.URL, cfg.AppID(), opts.Service, seedPlans, defaultDBSeedHooks()); err != nil {
			return fmt.Errorf("reset database seed ledger for service %q: %w", opts.Service, err)
		}
	}
	_, _ = fmt.Fprintln(os.Stdout, "reset scenery database")
	return nil
}

type dbServerOptions struct {
	Action  string
	AppRoot string
	JSON    bool
}

type dbServerStatusResponse struct {
	cliPayloadIdentity
	AppRoot    string                    `json:"app_root"`
	Scope      string                    `json:"scope"`
	ResourceID string                    `json:"resource_id,omitempty"`
	Retained   bool                      `json:"retained_data"`
	OK         bool                      `json:"ok"`
	Container  string                    `json:"container"`
	Image      string                    `json:"image,omitempty"`
	Status     string                    `json:"status"`
	Port       int                       `json:"port,omitempty"`
	URL        string                    `json:"url,omitempty"`
	Databases  []postgresdb.DatabaseInfo `json:"databases,omitempty"`
	StatePath  string                    `json:"state_path,omitempty"`
	Restore    *dbServerRestoreStatus    `json:"restore,omitempty"`
}

type dbServerRestoreStatus struct {
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchivePath   string `json:"archive_path"`
	Mode          string `json:"mode"`
	SQLStarted    bool   `json:"sql_started"`
}

func dbServerCommand(args []string) error {
	opts, err := parseDBServerArgs(args)
	if err != nil {
		return err
	}
	return runWorktreeDBServer(context.Background(), os.Stdout, opts)
}

func parseDBServerArgs(args []string) (dbServerOptions, error) {
	opts := dbServerOptions{}
	flags := newCLIFlagSet("db server")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return dbServerOptions{}, err
	}
	if len(positionals) == 0 {
		return dbServerOptions{}, fmt.Errorf("usage: scenery db server status|start|stop|logs [--app-root <path>] [-o json]")
	}
	opts.Action = positionals[0]
	if len(positionals) > 1 {
		return dbServerOptions{}, fmt.Errorf("unknown argument %q", positionals[1])
	}
	return opts, nil
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
		if value := lookupEnvValue(env, key); value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s service %q database URL is not configured; set %s", engine, service, appEnvName)
}

func managedDatabaseLifecycleEnv(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string) ([]string, error) {
	env, _, err := managedDatabaseEnv(ctx, appRoot, cfg, baseEnv)
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
	return resolvePostgresDatabaseFromEnv(ctx, appRoot, cfg, baseEnv)
}

func resolvePostgresDatabaseFromEnv(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string) (postgresdb.Database, error) {
	if len(cfg.DatabaseServices()) == 0 {
		return postgresdb.Database{}, nil
	}
	if lookupEnvValue(baseEnv, appDatabaseURLEnv) != "" {
		_, database, err := managedDatabaseEnv(ctx, appRoot, cfg, baseEnv)
		return database, err
	}
	resolver, err := newWorktreePostgresResolver(ctx, appRoot, cfg.AppID())
	if err != nil {
		return postgresdb.Database{}, err
	}
	server, running, err := resolver.observe(ctx)
	if err != nil {
		return postgresdb.Database{}, err
	}
	if !running {
		return postgresdb.Database{}, worktreePostgresPrecondition("the selected database is stopped; explicitly start it with db server start before accessing it")
	}
	return databaseForWorktreeServer(resolver.paths.AppRoot, cfg, server)
}

func databaseForWorktreeServer(appRoot string, cfg appcfg.Config, server *localagent.WorktreePostgres) (postgresdb.Database, error) {
	dbName := postgresname.DatabaseNameFor(cfg.AppID(), appRoot)
	baseURL := worktreePostgresURL(server, dbName)
	database := postgresdb.Database{Database: dbName, URL: baseURL, Source: postgresdb.SourceManaged, AppRoot: appRoot, ResourceID: server.InstanceID}
	for _, svc := range cfg.DatabaseServices() {
		serviceURL, err := postgresdb.ServiceURL(baseURL, svc.Schema)
		if err != nil {
			return postgresdb.Database{}, err
		}
		database.Schemas = append(database.Schemas, postgresdb.Service{Name: svc.Name, Schema: svc.Schema, URL: serviceURL})
	}
	return database, nil
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
		defer func() { _ = db.Close() }()
		return postgresdb.ResetSchema(ctx, db, schema)
	}
	if !opts.Yes {
		return fmt.Errorf("resetting the managed postgres app database requires --yes")
	}
	admin, err := managedPostgresAdmin(ctx, database)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close() }()
	return postgresdb.ResetDatabase(ctx, admin, database.Database)
}

func dropPostgresDatabase(ctx context.Context, database postgresdb.Database) error {
	if strings.TrimSpace(database.Database) == "" {
		return nil
	}
	if database.Source == postgresdb.SourceExternal {
		return fmt.Errorf("refusing to drop external postgres database")
	}
	admin, err := managedPostgresAdmin(ctx, database)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close() }()
	return postgresdb.DropDatabase(ctx, admin, database.Database)
}

func managedPostgresAdmin(ctx context.Context, database postgresdb.Database) (*sql.DB, error) {
	if database.Source != postgresdb.SourceManaged || database.AppRoot == "" || database.ResourceID == "" {
		return nil, worktreePostgresPrecondition("the selected database has no managed worktree authority")
	}
	resolver, err := newWorktreePostgresResolver(ctx, database.AppRoot, "")
	if err != nil {
		return nil, err
	}
	record, err := resolver.load()
	if err != nil {
		return nil, err
	}
	if record.Postgres.InstanceID != database.ResourceID || database.Database != postgresname.DatabaseNameFor(record.AppID, record.AppRoot) {
		return nil, worktreePostgresPrecondition("the selected app database does not match retained resource ownership")
	}
	if _, _, err := resolver.inspect(ctx, record); err != nil {
		return nil, err
	}
	return openPostgresAdmin(ctx, worktreePostgresURL(record.Postgres, "postgres"))
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
	cliPayloadIdentity
	Database databaseListRecord `json:"database"`
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
