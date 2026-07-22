package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/scn"
)

type generateOptions struct {
	Subject string
	AppRoot string
	Target  string
	Lang    string
	Output  string
	DryRun  bool
	JSON    bool
}

type generatorExecutionPlan struct {
	Graph generatorGraphResponse
	SQLC  *sqlcGeneratorPlan
}

type generatorGraphResponse struct {
	cliPayloadIdentity
	App         inspectdata.AppRef       `json:"app"`
	Generators  []generatorRecord        `json:"generators"`
	DBArtifacts []databaseArtifactRecord `json:"db_artifacts"`
}

type generatorRecord struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Inputs  []string `json:"inputs,omitempty"`
	Outputs []string `json:"outputs,omitempty"`
	Tool    string   `json:"tool,omitempty"`
}

type sqlcGeneratorPlan struct {
	Record     generatorRecord
	ConfigPath string
	Schemas    []sqlcSchemaPlan
	Queries    []string
}

type sqlcSchemaPlan struct {
	SQLCSchema  string
	AtlasSource string
	AtlasDevURL string
	Engine      string
}

type databaseArtifactRecord struct {
	Service string `json:"service"`
	Engine  string `json:"engine,omitempty"`
	Kind    string `json:"kind"`
	Role    string `json:"role"`
	Path    string `json:"path"`
}

type lifecycleExecRequest struct {
	Dir     string
	Env     []string
	Program string
	Args    []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

type lifecycleHooks struct {
	runExec    func(context.Context, lifecycleExecRequest) error
	outputExec func(context.Context, lifecycleExecRequest) ([]byte, error)
}

func defaultLifecycleHooks() lifecycleHooks {
	return lifecycleHooks{runExec: defaultRunLifecycleExec, outputExec: defaultOutputLifecycleExec}
}

func (h lifecycleHooks) withDefaults() lifecycleHooks {
	if h.runExec == nil {
		h.runExec = defaultRunLifecycleExec
	}
	if h.outputExec == nil {
		h.outputExec = defaultOutputLifecycleExec
	}
	return h
}

func defaultRunLifecycleExec(ctx context.Context, req lifecycleExecRequest) error {
	cmd := exec.CommandContext(ctx, req.Program, req.Args...)
	cmd.Dir = req.Dir
	if req.Env != nil {
		cmd.Env = req.Env
	}
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	return cmd.Run()
}

func defaultOutputLifecycleExec(ctx context.Context, req lifecycleExecRequest) ([]byte, error) {
	cmd := exec.CommandContext(ctx, req.Program, req.Args...)
	cmd.Dir = req.Dir
	if req.Env != nil {
		cmd.Env = req.Env
	}
	cmd.Stdin = req.Stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("%s %s: %w: %s", req.Program, strings.Join(req.Args, " "), err, detail)
		}
		return nil, fmt.Errorf("%s %s: %w", req.Program, strings.Join(req.Args, " "), err)
	}
	return out, nil
}

func generateCommand(args []string) error {
	return runGenerate(context.Background(), os.Stdout, args)
}

func runGenerate(ctx context.Context, stdout io.Writer, args []string) error {
	return runGenerateWithHooks(ctx, stdout, args, defaultLifecycleHooks())
}

func runGenerateWithHooks(ctx context.Context, stdout io.Writer, args []string, hooks lifecycleHooks) error {
	opts, err := parseGenerateArgs(args)
	if err != nil {
		return err
	}
	appRoot, cfg, hasApp, err := discoverLifecycleRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	plan, err := buildGenerateExecutionPlan(appRoot, cfg, hasApp, opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		return renderGeneratorPlan(stdout, opts.JSON, plan.Graph)
	}
	if err := executeGeneratorPlan(ctx, stdout, appRoot, cfg, opts, plan, hooks); err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, plan.Graph)
	}
	return nil
}

func parseGenerateArgs(args []string) (generateOptions, error) {
	opts := generateOptions{Subject: "all"}
	flags := newCLIFlagSet("generate")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "")
	registerJSONOutput(flags, &opts.JSON)
	flags.StringVar(&opts.Lang, "lang", "", "")
	flags.StringVar(&opts.Output, "output", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return generateOptions{}, err
	}
	if len(positionals) > 0 && positionals[0] == "sqlc" {
		opts.Subject = "sqlc"
		positionals = positionals[1:]
	}
	if cliFlagSet(flags, "lang") || cliFlagSet(flags, "output") {
		return generateOptions{}, fmt.Errorf("client output flags are not supported here; declare a typescript_client in %s", scn.AppFilename)
	}
	if len(positionals) > 0 {
		return generateOptions{}, fmt.Errorf("unexpected argument %q", positionals[0])
	}
	return opts, nil
}

func discoverLifecycleRoot(appRootOpt string) (string, appcfg.Config, bool, error) {
	start, err := resolveAppRoot(appRootOpt)
	if err != nil {
		return "", appcfg.Config{}, false, err
	}
	if appRoot, cfg, err := appcfg.DiscoverRoot(start); err == nil {
		return appRoot, cfg, true, nil
	} else if appRootOpt != "" {
		return "", appcfg.Config{}, false, err
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", appcfg.Config{}, false, err
	}
	return abs, appcfg.Config{Name: filepath.Base(abs)}, false, nil
}

func buildGenerateExecutionPlan(appRoot string, cfg appcfg.Config, hasApp bool, opts generateOptions) (generatorExecutionPlan, error) {
	plan := generatorExecutionPlan{
		Graph: baseGeneratorGraph(appRoot, cfg, hasApp),
	}

	if opts.Subject == "all" || opts.Subject == "sqlc" {
		sqlcPlan, ok, err := buildSQLCGeneratorPlan(appRoot, cfg)
		if err != nil {
			return generatorExecutionPlan{}, err
		}
		if !ok {
			if opts.Subject == "sqlc" {
				return generatorExecutionPlan{}, fmt.Errorf("sqlc generator is not configured")
			}
		} else {
			plan.SQLC = sqlcPlan
			plan.Graph.Generators = append(plan.Graph.Generators, sqlcPlan.Record)
		}
	}
	plan.Graph.DBArtifacts = buildDatabaseArtifactRecords(appRoot, plan.SQLC)
	if opts.Subject == "all" && len(plan.Graph.Generators) == 0 {
		return generatorExecutionPlan{}, fmt.Errorf("no generators are configured")
	}
	return plan, nil
}

func buildInspectGeneratorsResponse(appRoot string, cfg appcfg.Config) (generatorGraphResponse, error) {
	graph := baseGeneratorGraph(appRoot, cfg, true)
	sqlcPlan, ok, err := buildSQLCGeneratorPlan(appRoot, cfg)
	if err != nil {
		return generatorGraphResponse{}, err
	}
	if ok {
		graph.Generators = append(graph.Generators, sqlcPlan.Record)
	}
	graph.DBArtifacts = buildDatabaseArtifactRecords(appRoot, sqlcPlan)
	return graph, nil
}

func baseGeneratorGraph(appRoot string, cfg appcfg.Config, hasApp bool) generatorGraphResponse {
	graph := generatorGraphResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.inspect.generators"),
		App: inspectdata.AppRef{
			Name:       cfg.Name,
			ID:         cfg.ID,
			Root:       appRoot,
			ConfigPath: cfg.SourcePath(appRoot),
		},
		Generators:  []generatorRecord{},
		DBArtifacts: []databaseArtifactRecord{},
	}
	if !hasApp {
		graph.App.ConfigPath = ""
	}
	return graph
}

func buildSQLCGeneratorPlan(appRoot string, cfg appcfg.Config) (*sqlcGeneratorPlan, bool, error) {
	conf := cfg.Generators.SQLC
	if conf.Provider != "" && conf.Provider != "sqlc" {
		return nil, false, fmt.Errorf("unsupported sqlc generator provider %q", conf.Provider)
	}
	configRel := firstNonEmpty(conf.Config, "sqlc.yaml")
	configPath := configRel
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(appRoot, configPath)
	}
	configExists := pathExists(configPath)
	if !configExists && conf.Provider == "" && len(conf.Schemas) == 0 {
		return nil, false, nil
	}
	if !configExists {
		return nil, false, fmt.Errorf("sqlc config %s does not exist", configPath)
	}

	sqlcCfg, err := readSQLCConfig(configPath)
	if err != nil {
		return nil, false, err
	}
	schemaPlans := configuredSQLCSchemaPlans(conf)
	knownSchemas := map[string]bool{}
	schemaPlanIndex := map[string]int{}
	for _, schema := range schemaPlans {
		key := filepath.ToSlash(schema.SQLCSchema)
		knownSchemas[key] = true
		schemaPlanIndex[key] = len(schemaPlanIndex)
	}
	var inputs []string
	var outputs []string
	var queries []string
	inputs = append(inputs, filepath.ToSlash(configRel))
	for _, block := range sqlcCfg.SQL {
		engine := normalizeSQLCEngine(block.Engine)
		for _, query := range block.Queries.Values {
			query = filepath.ToSlash(query)
			inputs = append(inputs, query)
			queries = append(queries, query)
		}
		for _, schema := range block.Schema.Values {
			schema = filepath.ToSlash(schema)
			outputs = append(outputs, schema)
			if !knownSchemas[schema] {
				plan := inferSQLCSchemaPlan(appRoot, conf, schema)
				plan.Engine = engine
				schemaPlans = append(schemaPlans, plan)
				knownSchemas[schema] = true
				schemaPlanIndex[schema] = len(schemaPlans) - 1
			} else if idx, ok := schemaPlanIndex[schema]; ok && schemaPlans[idx].Engine == "" {
				schemaPlans[idx].Engine = engine
			}
		}
		if block.Gen.Go.Out != "" {
			outputs = append(outputs, filepath.ToSlash(block.Gen.Go.Out))
		}
	}
	if err := validateSQLCSchemaEngines(cfg, schemaPlans); err != nil {
		return nil, false, err
	}
	for _, schema := range schemaPlans {
		if schema.AtlasSource != "" {
			inputs = append(inputs, filepath.ToSlash(schema.AtlasSource))
			if schema.SQLCSchema != "" {
				outputs = append(outputs, filepath.ToSlash(schema.SQLCSchema))
			}
			continue
		}
		if schema.SQLCSchema != "" {
			inputs = append(inputs, filepath.ToSlash(schema.SQLCSchema))
		}
	}
	record := generatorRecord{
		ID:      "sqlc",
		Kind:    "sqlc",
		Inputs:  uniqueSorted(inputs),
		Outputs: uniqueSorted(outputs),
		Tool:    "sqlc",
	}
	return &sqlcGeneratorPlan{
		Record:     record,
		ConfigPath: filepath.ToSlash(configRel),
		Schemas:    schemaPlans,
		Queries:    uniqueSorted(queries),
	}, true, nil
}

func buildDatabaseArtifactRecords(appRoot string, sqlcPlan *sqlcGeneratorPlan) []databaseArtifactRecord {
	var records []databaseArtifactRecord
	seen := map[string]bool{}
	keepMissing := map[string]bool{}
	engineByPath := map[string]string{}
	engineByService := map[string]string{}
	if sqlcPlan != nil {
		for _, schema := range sqlcPlan.Schemas {
			engine := normalizeSQLCEngine(schema.Engine)
			service := serviceNameForDBArtifact(schema.SQLCSchema)
			if service != "" && service != "." && engine != "" {
				engineByService[service] = engine
			}
			if schema.SQLCSchema != "" && engine != "" {
				engineByPath[filepath.ToSlash(schema.SQLCSchema)] = engine
			}
			if schema.AtlasSource != "" && engine != "" {
				engineByPath[filepath.ToSlash(schema.AtlasSource)] = engine
			}
		}
	}
	add := func(path, kind, role string) {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" {
			return
		}
		service := serviceNameForDBArtifact(path)
		key := service + "\x00" + kind + "\x00" + role + "\x00" + path
		if seen[key] {
			return
		}
		seen[key] = true
		records = append(records, databaseArtifactRecord{
			Service: service,
			Engine:  firstNonEmpty(engineByPath[path], engineByService[service]),
			Kind:    kind,
			Role:    role,
			Path:    path,
		})
	}

	if sqlcPlan != nil {
		for _, schema := range sqlcPlan.Schemas {
			add(schema.AtlasSource, "schema-source", "schema")
			if schema.SQLCSchema != "" {
				keepMissing[filepath.ToSlash(schema.SQLCSchema)] = true
			}
			if schema.AtlasSource != "" {
				add(schema.SQLCSchema, "generated-schema", "generated-source")
			} else {
				add(schema.SQLCSchema, "schema-source", "schema")
			}
		}
		for _, query := range sqlcPlan.Queries {
			add(query, "query", "query-generation-input")
		}
	}
	for _, relDir := range discoverDBArtifactServiceDirs(appRoot) {
		relRoot := filepath.ToSlash(filepath.Join(relDir, "db"))
		add(filepath.ToSlash(filepath.Join(relRoot, "schema.hcl")), "schema-source", "schema")
		add(filepath.ToSlash(filepath.Join(relRoot, "queries.sql")), "query", "query-generation-input")
		add(filepath.ToSlash(filepath.Join(relRoot, "gen", "schema.sql")), "generated-schema", "generated-source")
		add(filepath.ToSlash(filepath.Join(relRoot, "seed.sql")), "seed", "initial-data")
	}

	filtered := records[:0]
	for _, record := range records {
		if keepMissing[record.Path] {
			filtered = append(filtered, record)
			continue
		}
		if pathExists(filepath.Join(appRoot, filepath.FromSlash(record.Path))) {
			filtered = append(filtered, record)
		}
	}
	records = filtered
	sort.Slice(records, func(i, j int) bool {
		if records[i].Service != records[j].Service {
			return records[i].Service < records[j].Service
		}
		if databaseArtifactKindRank(records[i].Kind) != databaseArtifactKindRank(records[j].Kind) {
			return databaseArtifactKindRank(records[i].Kind) < databaseArtifactKindRank(records[j].Kind)
		}
		return records[i].Path < records[j].Path
	})
	return records
}

// skipDBArtifactServiceDir keeps service-listing-specific skips ("ui" and any
// dot directory) on top of the shared appwalk policy.
// discoverDBArtifactServiceDirs returns app-root-relative service directories
// that own a db/ artifact root. Services may nest one level below a domain
// directory (for example solar/projects/db), so both depths are scanned.
func discoverDBArtifactServiceDirs(appRoot string) []string {
	var out []string
	entries, err := os.ReadDir(appRoot)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() || skipDBArtifactServiceDir(appRoot, entry.Name()) {
			continue
		}
		if pathExists(filepath.Join(appRoot, entry.Name(), "db")) {
			out = append(out, entry.Name())
		}
		nested, err := os.ReadDir(filepath.Join(appRoot, entry.Name()))
		if err != nil {
			continue
		}
		for _, child := range nested {
			if !child.IsDir() || child.Name() == "db" {
				continue
			}
			rel := filepath.Join(entry.Name(), child.Name())
			if skipDBArtifactServiceDir(appRoot, rel) {
				continue
			}
			if pathExists(filepath.Join(appRoot, rel, "db")) {
				out = append(out, rel)
			}
		}
	}
	sort.Strings(out)
	return out
}

func skipDBArtifactServiceDir(appRoot, name string) bool {
	switch name {
	case "", ".", "..", "ui":
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return appwalk.SkipDir(appRoot, filepath.Join(appRoot, name))
}

func serviceNameForDBArtifact(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if part == "db" && i > 0 {
			return parts[i-1]
		}
	}
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "."
}

func databaseArtifactKindRank(kind string) int {
	switch kind {
	case "schema-source":
		return 0
	case "query":
		return 1
	case "generated-schema":
		return 2
	case "seed":
		return 3
	default:
		return 99
	}
}

func configuredSQLCSchemaPlans(conf appcfg.SQLCGeneratorConfig) []sqlcSchemaPlan {
	var out []sqlcSchemaPlan
	for _, schema := range conf.Schemas {
		out = append(out, sqlcSchemaPlan{
			SQLCSchema:  filepath.ToSlash(schema.SQLCSchema),
			AtlasSource: filepath.ToSlash(schema.AtlasSource),
			AtlasDevURL: firstNonEmpty(schema.AtlasDevURL, conf.DevURL),
		})
	}
	return out
}

func inferSQLCSchemaPlan(appRoot string, conf appcfg.SQLCGeneratorConfig, schemaRel string) sqlcSchemaPlan {
	return sqlcSchemaPlan{
		SQLCSchema:  filepath.ToSlash(schemaRel),
		AtlasDevURL: conf.DevURL,
	}
}

type sqlcConfigFile struct {
	SQL []sqlcConfigBlock `yaml:"sql"`
}

type sqlcConfigBlock struct {
	Engine  string         `yaml:"engine"`
	Schema  yamlStringList `yaml:"schema"`
	Queries yamlStringList `yaml:"queries"`
	Gen     struct {
		Go struct {
			Out string `yaml:"out"`
		} `yaml:"go"`
	} `yaml:"gen"`
}

func validateSQLCSchemaEngines(cfg appcfg.Config, schemas []sqlcSchemaPlan) error {
	for _, schema := range schemas {
		service := serviceNameForDBArtifact(schema.SQLCSchema)
		if service == "." || service == "" {
			continue
		}
		engine := normalizeSQLCEngine(schema.Engine)
		if _, ok := cfg.DatabaseService(service); ok {
			if engine != "" && engine != "postgres" {
				return fmt.Errorf("sqlc schema %s belongs to database service %s but uses engine %q; plan 0097 is Postgres-only, set engine: postgresql", schema.SQLCSchema, service, schema.Engine)
			}
		}
	}
	return nil
}

func normalizeSQLCEngine(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgres", "postgresql":
		return "postgres"
	default:
		return strings.ToLower(strings.TrimSpace(engine))
	}
}

type yamlStringList struct {
	Values []string
}

func (l *yamlStringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(value.Value) != "" {
			l.Values = []string{value.Value}
		}
	case yaml.SequenceNode:
		for _, item := range value.Content {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("expected string list item")
			}
			if strings.TrimSpace(item.Value) != "" {
				l.Values = append(l.Values, item.Value)
			}
		}
	case 0:
		return nil
	default:
		return fmt.Errorf("expected string or string list")
	}
	return nil
}

func readSQLCConfig(path string) (sqlcConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sqlcConfigFile{}, err
	}
	var cfg sqlcConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return sqlcConfigFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func executeGeneratorPlan(ctx context.Context, stdout io.Writer, appRoot string, cfg appcfg.Config, opts generateOptions, plan generatorExecutionPlan, hooks lifecycleHooks) error {
	if plan.SQLC != nil {
		if err := runSQLCGeneratorWithHooks(ctx, stdout, appRoot, plan.SQLC, opts.JSON, hooks); err != nil {
			return err
		}
	}
	return nil
}

func runSQLCGeneratorWithHooks(ctx context.Context, stdout io.Writer, appRoot string, plan *sqlcGeneratorPlan, quiet bool, hooks lifecycleHooks) error {
	hooks = hooks.withDefaults()
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return err
	}
	for _, schema := range plan.Schemas {
		if schema.SQLCSchema == "" || schema.AtlasSource == "" {
			continue
		}
		if strings.TrimSpace(schema.AtlasDevURL) == "" {
			return fmt.Errorf("generators.sqlc schema %s uses atlas_source but no dev_url is configured", schema.SQLCSchema)
		}
		sourcePath := schema.AtlasSource
		if !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(appRoot, filepath.FromSlash(sourcePath))
		}
		out, err := hooks.outputExec(ctx, lifecycleExecRequest{
			Dir:     appRoot,
			Env:     env,
			Program: "atlas",
			Args: []string{
				"schema", "inspect",
				"--url", "file://" + filepath.ToSlash(sourcePath),
				"--dev-url", schema.AtlasDevURL,
				"--format", "{{ sql . }}",
			},
		})
		if err != nil {
			return err
		}
		schemaPath := schema.SQLCSchema
		if !filepath.IsAbs(schemaPath) {
			schemaPath = filepath.Join(appRoot, filepath.FromSlash(schemaPath))
		}
		if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
			return err
		}
		data := append([]byte("-- GENERATED: do not edit. Run `scenery generate sqlc` to refresh.\n\n"), out...)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
		if err := os.WriteFile(schemaPath, data, 0o644); err != nil {
			return err
		}
		if !quiet {
			fmt.Fprintf(stdout, "scenery: generated SQLC schema at %s\n", schemaPath)
		}
	}
	args := []string{"generate"}
	if plan.ConfigPath != "" && plan.ConfigPath != "sqlc.yaml" {
		args = append(args, "-f", plan.ConfigPath)
	}
	if err := hooks.runExec(ctx, lifecycleExecRequest{
		Dir:     appRoot,
		Env:     env,
		Program: "sqlc",
		Args:    args,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}); err != nil {
		return err
	}
	if !quiet {
		fmt.Fprintln(stdout, "scenery: generated SQLC artifacts")
	}
	return nil
}

func renderGeneratorPlan(stdout io.Writer, jsonMode bool, graph generatorGraphResponse) error {
	if jsonMode {
		return writeInspectJSON(stdout, graph)
	}
	for _, generator := range graph.Generators {
		fmt.Fprintf(stdout, "%s %s\n", generator.ID, generator.Kind)
		if len(generator.Outputs) > 0 {
			fmt.Fprintf(stdout, "  outputs: %s\n", strings.Join(generator.Outputs, ", "))
		}
	}
	return nil
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(filepath.ToSlash(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func shellInvocation(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "/bin/sh", []string{"-c", command}
}

func overlayEnv(base []string, values map[string]string) []string {
	if len(values) == 0 {
		return append([]string(nil), base...)
	}
	env := append([]string(nil), base...)
	index := make(map[string]int, len(env))
	for i, item := range env {
		if key, _, ok := strings.Cut(item, "="); ok {
			index[key] = i
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := key + "=" + values[key]
		if i, ok := index[key]; ok {
			env[i] = entry
			continue
		}
		env = append(env, entry)
	}
	return env
}
