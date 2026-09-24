package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
)

// maxConfigInputBytes bounds a value read from a prompt or stdin.
const maxConfigInputBytes = appconfig.MaxSecretBytes

// configSecretBackendOverride is the in-process secret backend seam for tests.
var configSecretBackendOverride func(*appconfig.Store) (appconfig.SecretBackend, error)

// configRemoteTransportOverride is the in-process SSH transport seam for tests.
var configRemoteTransportOverride configRemoteTransport

// configCatalogOverride is the in-process catalog seam for tests; production
// compiles the checkout.
var configCatalogOverride func(root string, cfg app.Config) (appconfig.Catalog, error)

// configStdin is the command's stdin; tests replace it.
var configStdin io.Reader = os.Stdin

type configOptions struct {
	AppRoot        string
	Env            string
	Output         string
	Stdin          bool
	Null           bool
	ExpectRevision string
}

// configSession is one command's application, environment and authority.
type configSession struct {
	root       string
	cfg        app.Config
	env        app.ResolvedEnv
	catalog    appconfig.Catalog
	deployable bool
	// store and target: a local environment uses store; a deployable one
	// resolves on its single SSH target.
	store  *appconfig.Store
	target string
}

func configCommand(args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: scenery config show|set|unset")
	}
	if err := flagBeforeWord(args, "a config subcommand"); err != nil {
		return err
	}
	switch args[0] {
	case "show":
		return configShowCommand(os.Stdout, args[1:])
	case "set":
		return configSetCommand(os.Stdout, args[1:])
	case "unset":
		return configUnsetCommand(os.Stdout, args[1:])
	case "receive":
		return configReceiveCommand(os.Stdout, args[1:])
	default:
		return usageErrorf("unknown config command %q; use show, set or unset", args[0])
	}
}

func parseConfigOptions(name string, args []string, mutation bool) (configOptions, []string, error) {
	var opts configOptions
	flags := newCLIFlagSet("config " + name)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Env, "env", "", "")
	flags.StringVar(&opts.Output, "o", "", "")
	if mutation {
		flags.StringVar(&opts.ExpectRevision, "expect-revision", "", "")
	}
	if name == "set" {
		flags.BoolVar(&opts.Stdin, "stdin", false, "")
		flags.BoolVar(&opts.Null, "null", false, "")
	}
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return opts, nil, err
	}
	if opts.Output != "" && opts.Output != "json" {
		return opts, nil, usageErrorf("-o supports only json")
	}
	opts.Env = strings.TrimSpace(opts.Env)
	if mutation && opts.Env == "" {
		return opts, nil, usageErrorf("config %s requires --env; configuration writes never assume an environment", name)
	}
	if cliFlagSet(flags, "env") && opts.Env == "" {
		return opts, nil, usageErrorf("--env must not be empty")
	}
	if opts.ExpectRevision != "" && !appconfig.ValidRevision(opts.ExpectRevision) {
		return opts, nil, usageErrorf("--expect-revision %q is not a configuration revision", opts.ExpectRevision)
	}
	return opts, positionals, nil
}

// openConfigSession discovers the application, compiles its catalog without
// starting anything, and selects the environment's authority.
func openConfigSession(opts configOptions) (*configSession, error) {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return nil, err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return nil, err
	}
	envName := opts.Env
	if envName == "" {
		envName = "local"
	}
	env, err := cfg.ResolveEnv(envName)
	if err != nil {
		return nil, usageErrorf("%v", err)
	}
	if strings.TrimSpace(cfg.ID) == "" {
		return nil, preconditionErrorf("environment configuration needs an explicit stable application id; add \"id\": %q to %s", cfg.AppID(), app.PrimaryConfigFilename)
	}
	if !appconfig.ValidIdentifier(cfg.ID) || !appconfig.ValidIdentifier(env.Name) {
		return nil, preconditionErrorf("application id %q and environment %q must be lowercase path-safe identifiers", cfg.ID, env.Name)
	}
	catalog, err := compileConfigCatalog(root, cfg)
	if err != nil {
		return nil, err
	}
	session := &configSession{root: root, cfg: cfg, env: env, catalog: catalog, deployable: env.Deployable()}
	if session.deployable {
		if len(env.Deploy.SSH) != 1 {
			return nil, preconditionErrorf("envs.%s.deploy.ssh must name exactly one target; configuration never chooses between targets", env.Name)
		}
		session.target = env.Deploy.SSH[0]
		return session, nil
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return nil, err
	}
	session.store, err = appconfig.OpenStore(paths.Home, cfg.ID)
	if err != nil {
		return nil, preconditionErrorf("%v", err)
	}
	return session, nil
}

// compileConfigCatalog compiles the checkout and derives its configuration
// catalog. It reads no secrets and starts no services.
func compileConfigCatalog(root string, cfg app.Config) (appconfig.Catalog, error) {
	if configCatalogOverride != nil {
		return configCatalogOverride(root, cfg)
	}
	result, err := compiler.Compile(root)
	if err != nil {
		return appconfig.Catalog{}, err
	}
	if !result.Valid() {
		return appconfig.Catalog{}, preconditionErrorf("the application does not compile; run `scenery check` for its diagnostics")
	}
	options := configFrameworkOptions(cfg)
	options.SQL = len(result.SQLRequirements) > 0
	catalog, err := appconfig.BuildCatalog(result.Manifest, options)
	if err != nil {
		return appconfig.Catalog{}, preconditionErrorf("%v", err)
	}
	return catalog, nil
}

func configFrameworkOptions(cfg app.Config) appconfig.FrameworkOptions {
	return appconfig.FrameworkOptions{StandardAuth: cfg.Auth.Enabled, GoogleOAuth: cfg.Auth.Enabled && cfg.Auth.GoogleOAuth.Enabled}
}

func (s *configSession) secretBackend() (appconfig.SecretBackend, error) {
	if configSecretBackendOverride != nil {
		return configSecretBackendOverride(s.store)
	}
	return appconfig.DefaultSecretBackend(s.store)
}

// readDocument reads the environment's desired document from its authority.
func (s *configSession) readDocument(ctx context.Context) (appconfig.Document, *configTargetActive, error) {
	if s.deployable {
		response, err := s.exchange(ctx, configRemoteRequest{Operation: "read"})
		if err != nil {
			return appconfig.Document{}, nil, err
		}
		return response.document(s.cfg.ID, s.env.Name), response.Active, nil
	}
	document, _, err := s.store.Read(s.env.Name)
	if err != nil {
		return appconfig.Document{}, nil, preconditionErrorf("read %s configuration: %v", s.env.Name, err)
	}
	return document, nil, nil
}

type configShowInput struct {
	Key        string          `json:"key"`
	Type       string          `json:"type"`
	Sensitive  bool            `json:"sensitive"`
	Source     string          `json:"source"`
	State      string          `json:"state"`
	Value      json.RawMessage `json:"value,omitempty"`
	Default    json.RawMessage `json:"default,omitempty"`
	Required   bool            `json:"required"`
	Problem    string          `json:"problem,omitempty"`
	Consumers  []string        `json:"consumers"`
	SecretNote string          `json:"secret,omitempty"`
}

type configShowResult struct {
	machine.PayloadIdentity
	AppID           string            `json:"app_id"`
	Environment     string            `json:"environment"`
	Authority       string            `json:"authority"`
	Target          string            `json:"target,omitempty"`
	DesiredRevision string            `json:"desired_revision"`
	CatalogRevision string            `json:"catalog_revision"`
	Valid           bool              `json:"valid"`
	Missing         []string          `json:"missing"`
	Unused          []string          `json:"unused"`
	Inputs          []configShowInput `json:"inputs"`
	Applied         configApplied     `json:"applied"`
}

// configApplied is the observed runtime adoption of a revision. An
// unavailable observation is never reported as applied.
type configApplied struct {
	State           string               `json:"state"`
	Revision        string               `json:"revision,omitempty"`
	CatalogRevision string               `json:"catalog_revision,omitempty"`
	Problem         string               `json:"problem,omitempty"`
	Others          *configOtherRuntimes `json:"other_runtimes,omitempty"`
}

// configOtherRuntimes summarizes the application's other running local
// runtimes of the environment.
type configOtherRuntimes struct {
	Total   int `json:"total"`
	Applied int `json:"applied"`
}

func configShowCommand(stdout io.Writer, args []string) error {
	opts, positionals, err := parseConfigOptions("show", args, false)
	if err != nil {
		return err
	}
	if len(positionals) > 1 {
		return usageErrorf("config show takes at most one KEY")
	}
	session, err := openConfigSession(opts)
	if err != nil {
		return err
	}
	ctx := context.Background()
	document, active, err := session.readDocument(ctx)
	if err != nil {
		return err
	}
	resolution := appconfig.Resolve(session.catalog, document, session.deployable)
	result := configShowResult{
		PayloadIdentity: machine.NewPayloadIdentity("scenery.config.show"),
		AppID:           session.cfg.ID, Environment: session.env.Name, Authority: "local",
		DesiredRevision: document.Revision, CatalogRevision: session.catalog.Revision,
		Valid: resolution.Valid(), Missing: resolution.Missing(), Unused: resolution.Unused,
		Inputs: []configShowInput{},
	}
	if result.Missing == nil {
		result.Missing = []string{}
	}
	if session.deployable {
		result.Authority, result.Target = "target", session.target
		result.Applied = configApplied{State: "not_deployed"}
		if active != nil {
			result.Applied = configApplied{State: "active", Revision: active.Revision, CatalogRevision: active.CatalogRevision}
		}
	} else {
		result.Applied = observeLocalConfigApplication(ctx, session.store, session.root, session.env.Name, document.Revision)
	}
	for _, entry := range resolution.Entries {
		if len(positionals) == 1 && entry.Key != positionals[0] {
			continue
		}
		input, _ := session.catalog.Lookup(entry.Key)
		shown := configShowInput{
			Key: entry.Key, Type: entry.Type, Sensitive: entry.Sensitive, Source: entry.Source, State: entry.State,
			Value: entry.Value, Default: input.Default, Required: input.Required(session.deployable), Problem: entry.Problem, Consumers: []string{},
		}
		for _, consumer := range input.Consumers {
			shown.Consumers = append(shown.Consumers, consumer.Service)
		}
		if entry.Sensitive {
			shown.Value, shown.SecretNote = nil, "not_configured"
			if entry.Secret != nil {
				shown.SecretNote = "configured"
			}
		}
		result.Inputs = append(result.Inputs, shown)
	}
	if len(positionals) == 1 && len(result.Inputs) == 0 {
		return usageErrorf("%s is not a configuration input of this application; run `scenery config show --env %s` for the catalog", positionals[0], session.env.Name)
	}
	if opts.Output == "json" {
		return writeCLIJSON(stdout, result)
	}
	renderConfigShow(stdout, result)
	return nil
}

type configChangeResult struct {
	machine.PayloadIdentity
	AppID            string   `json:"app_id"`
	Environment      string   `json:"environment"`
	Authority        string   `json:"authority"`
	Target           string   `json:"target,omitempty"`
	Key              string   `json:"key"`
	Operation        string   `json:"operation"`
	Changed          bool     `json:"changed"`
	PreviousRevision string   `json:"previous_revision"`
	Revision         string   `json:"revision"`
	Missing          []string `json:"missing"`
	Application      string   `json:"application"`
}

func configSetCommand(stdout io.Writer, args []string) error {
	opts, positionals, err := parseConfigOptions("set", args, true)
	if err != nil {
		return err
	}
	if len(positionals) == 0 || len(positionals) > 2 {
		return usageErrorf("usage: scenery config set KEY [VALUE] --env NAME [--stdin | --null]")
	}
	modes := 0
	for _, used := range []bool{len(positionals) == 2, opts.Stdin, opts.Null} {
		if used {
			modes++
		}
	}
	if modes > 1 {
		return usageErrorf("give the value once: as VALUE, with --stdin, or as --null")
	}
	session, err := openConfigSession(opts)
	if err != nil {
		return err
	}
	key := positionals[0]
	input, ok := session.catalog.Lookup(key)
	if !ok {
		return usageErrorf("%s is not a configuration input of this application", key)
	}
	mutation := configRemoteRequest{Operation: "set", Key: key, ExpectRevision: opts.ExpectRevision}
	switch {
	case input.Sensitive:
		if len(positionals) == 2 || opts.Null {
			return usageErrorf("%s is a secret: never pass it as an argument; use the hidden prompt or --stdin", key)
		}
		// Read before any lock or remote call.
		secret, err := readConfigSecret(opts.Stdin, key)
		if err != nil {
			return err
		}
		mutation.Secret = secret
	case opts.Null:
		value, err := appconfig.CanonicalValue(input, json.RawMessage("null"))
		if err != nil {
			return usageErrorf("%v", err)
		}
		mutation.Value = value
	default:
		text := ""
		if opts.Stdin {
			data, err := readConfigStdin()
			if err != nil {
				return err
			}
			text = string(data)
		} else if len(positionals) == 2 {
			text = positionals[1]
		} else {
			return usageErrorf("%s needs a VALUE, --stdin or --null", key)
		}
		value, err := appconfig.ParseText(input, text)
		if err != nil {
			return usageErrorf("%v", err)
		}
		mutation.Value = value
	}
	return session.applyMutation(stdout, opts, mutation)
}

func configUnsetCommand(stdout io.Writer, args []string) error {
	opts, positionals, err := parseConfigOptions("unset", args, true)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return usageErrorf("usage: scenery config unset KEY --env NAME")
	}
	session, err := openConfigSession(opts)
	if err != nil {
		return err
	}
	key := positionals[0]
	if _, ok := session.catalog.Lookup(key); !ok {
		return usageErrorf("%s is not a configuration input of this application", key)
	}
	return session.applyMutation(stdout, opts, configRemoteRequest{Operation: "unset", Key: key, ExpectRevision: opts.ExpectRevision})
}

func (s *configSession) applyMutation(stdout io.Writer, opts configOptions, request configRemoteRequest) error {
	ctx := context.Background()
	var mutation appconfig.MutationResult
	if s.deployable {
		response, err := s.exchange(ctx, request)
		if err != nil {
			return err
		}
		mutation = *response.Result
	} else {
		var err error
		mutation, err = applyLocalConfigMutation(ctx, s.store, s.env.Name, request, func() (appconfig.SecretBackend, error) { return s.secretBackend() })
		if err != nil {
			return err
		}
	}
	document, _, err := s.readDocument(ctx)
	if err != nil {
		return err
	}
	result := configChangeResult{
		PayloadIdentity: machine.NewPayloadIdentity("scenery.config.change"),
		AppID:           s.cfg.ID, Environment: s.env.Name, Authority: "local", Key: request.Key, Operation: request.Operation,
		Changed: mutation.Changed, PreviousRevision: mutation.PreviousRevision, Revision: mutation.Revision,
		Missing:     appconfig.Resolve(s.catalog, document, s.deployable).Missing(),
		Application: "running local runtimes of this application apply it automatically",
	}
	if result.Missing == nil {
		result.Missing = []string{}
	}
	if s.deployable {
		result.Authority, result.Target = "target", s.target
		result.Application = "applied by the next scenery deploy --env " + s.env.Name
	}
	if opts.Output == "json" {
		return writeCLIJSON(stdout, result)
	}
	renderConfigChange(stdout, result)
	return nil
}

// applyLocalConfigMutation performs one mutation against a local store. A
// secret is created as a new immutable version before the document
// references it; a failed publication leaves only an orphan version.
func applyLocalConfigMutation(ctx context.Context, store *appconfig.Store, environment string, request configRemoteRequest, backend func() (appconfig.SecretBackend, error)) (appconfig.MutationResult, error) {
	mutation := appconfig.Mutation{Key: request.Key, ExpectRevision: request.ExpectRevision}
	switch {
	case request.Operation == "unset":
		mutation.Unset = true
	case request.Secret != nil:
		secrets, err := backend()
		if err != nil {
			return appconfig.MutationResult{}, unavailableErrorf("%v", err)
		}
		if err := secrets.Ready(ctx); err != nil {
			return appconfig.MutationResult{}, unavailableErrorf("%v", err)
		}
		version, err := secrets.Create(ctx, environment, request.Key, request.Secret)
		if err != nil {
			return appconfig.MutationResult{}, err
		}
		mutation.Secret = &version
	default:
		mutation.Value = request.Value
	}
	result, err := store.Mutate(ctx, environment, mutation)
	if errors.Is(err, appconfig.ErrRevisionConflict) {
		return result, &codedCLIError{err: fmt.Errorf("revision_conflict: %w", err), code: 3}
	}
	if err != nil {
		return result, preconditionErrorf("update %s configuration: %v", environment, err)
	}
	if result.Changed {
		pruneConfigHistory(ctx, store, environment, backend)
	}
	return result, nil
}

// pruneConfigHistory bounds retained revisions and removes secret versions no
// retained revision references. Pruning failures never fail the mutation.
func pruneConfigHistory(ctx context.Context, store *appconfig.Store, environment string, backend func() (appconfig.SecretBackend, error)) {
	secrets, err := backend()
	var known map[string][]appconfig.SecretVersion
	if err == nil {
		known, err = secrets.Versions(ctx, environment)
	}
	if err != nil {
		known = nil
	}
	result, err := store.Prune(ctx, environment, known)
	if err != nil || secrets == nil {
		return
	}
	for key, versions := range result.OrphanSecrets {
		for _, version := range versions {
			_ = secrets.Remove(ctx, environment, key, version)
		}
	}
}

func readConfigStdin() ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(configStdin, int64(maxConfigInputBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigInputBytes {
		return nil, usageErrorf("stdin value exceeds %d bytes", maxConfigInputBytes)
	}
	return data, nil
}

// readConfigSecret reads exact secret bytes from stdin, or from a hidden
// terminal prompt. Without a terminal and without --stdin it fails instead of
// waiting.
func readConfigSecret(fromStdin bool, key string) ([]byte, error) {
	var value []byte
	var err error
	if fromStdin {
		value, err = readConfigStdin()
	} else {
		terminal, ok := configStdin.(*os.File)
		if !ok || !isTerminal(terminal) {
			return nil, usageErrorf("%s is a secret and no terminal is available for a hidden prompt; pipe it with --stdin", key)
		}
		value, err = readHiddenLine(terminal, cliStderr, "Value for "+key)
	}
	if err != nil {
		return nil, err
	}
	if len(value) == 0 {
		return nil, usageErrorf("%s: an empty secret is not a value; use scenery config unset", key)
	}
	if len(value) > maxConfigInputBytes {
		return nil, usageErrorf("%s: secret exceeds %d bytes", key, maxConfigInputBytes)
	}
	return value, nil
}
