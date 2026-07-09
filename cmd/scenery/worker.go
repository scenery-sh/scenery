package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	durablestore "scenery.sh/internal/durable/store"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

type workerOptions struct {
	AppRoot   string
	Env       string
	LogFormat string
}

type workerDurableTokenCreateOptions struct {
	AppRoot string
	Service string
	Name    string
	ID      string
	JSON    bool
}

type workerDurableOptions struct {
	AppRoot   string
	Env       string
	LogFormat string
	Endpoint  string
	Token     string
	Services  []string
}

type workerDurableJobsOptions struct {
	AppRoot string
	Service string
	JobID   string
	Limit   int
	Action  string
	JSON    bool
}

type workerDurableTokenCreateResponse struct {
	SchemaVersion string `json:"schema_version"`
	App           struct {
		Name string `json:"name"`
		Root string `json:"root"`
	} `json:"app"`
	Service string `json:"service"`
	DBPath  string `json:"db_path"`
	Token   struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Secret    string `json:"secret"`
		TokenHash string `json:"token_hash"`
	} `json:"token"`
}

type workerDurableJobsResponse struct {
	SchemaVersion string             `json:"schema_version"`
	Service       string             `json:"service"`
	DBPath        string             `json:"db_path"`
	Jobs          []durableJobRecord `json:"jobs,omitempty"`
	Job           *durableJobRecord  `json:"job,omitempty"`
	Events        []durableJobEvent  `json:"events,omitempty"`
	Action        string             `json:"action,omitempty"`
	OK            bool               `json:"ok,omitempty"`
}

type durableJobRecord struct {
	ID          string `json:"id"`
	TaskName    string `json:"task_name"`
	State       string `json:"state"`
	DedupeKey   string `json:"dedupe_key,omitempty"`
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	ErrorCodec  string `json:"error_codec,omitempty"`
	Error       string `json:"error,omitempty"`
}

type durableJobEvent struct {
	Seq          int64  `json:"seq"`
	Attempt      int    `json:"attempt,omitempty"`
	EventType    string `json:"event_type"`
	PayloadCodec string `json:"payload_codec"`
	CreatedAt    string `json:"created_at"`
}

func workerCommand(args []string) error {
	if len(args) > 0 && args[0] == "durable" {
		return durableWorkerCommand(args[1:], os.Stdout)
	}
	opts, err := parseWorkerArgs(args)
	if err != nil {
		return err
	}
	return runWorkerFunc(opts)
}

var runWorkerFunc = runWorker
var runWorkerDurableTokenCreateFunc = runWorkerDurableTokenCreate
var runWorkerDurableJobsFunc = runWorkerDurableJobs

func durableWorkerCommand(args []string, stdout io.Writer) error {
	if len(args) >= 2 && args[0] == "token" && args[1] == "create" {
		opts, err := parseWorkerDurableTokenCreateArgs(args[2:])
		if err != nil {
			return err
		}
		return runWorkerDurableTokenCreateFunc(opts, stdout)
	}
	if len(args) >= 1 && args[0] == "jobs" {
		opts, err := parseWorkerDurableJobsArgs(args[1:])
		if err != nil {
			return err
		}
		return runWorkerDurableJobsFunc(opts, stdout)
	}
	if len(args) == 0 {
		return fmt.Errorf("scenery worker durable requires --endpoint and --token")
	}
	opts, err := parseWorkerDurableArgs(args)
	if err != nil {
		return err
	}
	return runWorkerDurable(opts)
}

func parseWorkerArgs(args []string) (workerOptions, error) {
	opts := workerOptions{LogFormat: "text"}
	flags := newCLIFlagSet("worker")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Env, "env", "", "")
	flags.StringVar(&opts.LogFormat, "log-format", opts.LogFormat, "")
	for _, name := range []string{"port", "p", "listen", "verbose", "v", "json", "dashboard", "watch"} {
		rejectCLIFlag(flags, name, "--"+name+" is not supported by `scenery worker`")
	}
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return workerOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return workerOptions{}, err
	}
	opts.Env = strings.TrimSpace(opts.Env)
	if cliFlagSet(flags, "env") && opts.Env == "" {
		return workerOptions{}, fmt.Errorf("--env must not be empty")
	}
	if opts.LogFormat != "text" && opts.LogFormat != "json" {
		return workerOptions{}, fmt.Errorf("invalid --log-format %q", opts.LogFormat)
	}
	return opts, nil
}

func parseWorkerDurableTokenCreateArgs(args []string) (workerDurableTokenCreateOptions, error) {
	var opts workerDurableTokenCreateOptions
	flags := newCLIFlagSet("worker durable token create")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Service, "service", "", "")
	flags.StringVar(&opts.Name, "name", "", "")
	flags.StringVar(&opts.ID, "id", "", "")
	flags.BoolVar(&opts.JSON, "json", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return workerDurableTokenCreateOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return workerDurableTokenCreateOptions{}, err
	}
	opts.Service, opts.Name, opts.ID = strings.TrimSpace(opts.Service), strings.TrimSpace(opts.Name), strings.TrimSpace(opts.ID)
	if opts.Service == "" {
		return workerDurableTokenCreateOptions{}, fmt.Errorf("--service is required")
	}
	if opts.Name == "" {
		opts.Name = opts.Service + " durable worker"
	}
	return opts, nil
}

func parseWorkerDurableArgs(args []string) (workerDurableOptions, error) {
	opts := workerDurableOptions{LogFormat: "text"}
	flags := newCLIFlagSet("worker durable")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Env, "env", "", "")
	flags.StringVar(&opts.LogFormat, "log-format", opts.LogFormat, "")
	flags.StringVar(&opts.Endpoint, "endpoint", "", "")
	flags.StringVar(&opts.Token, "token", "", "")
	flags.Func("service", "", func(value string) error { opts.Services = append(opts.Services, strings.TrimSpace(value)); return nil })
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return workerDurableOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return workerDurableOptions{}, err
	}
	opts.Env = strings.TrimSpace(opts.Env)
	opts.Endpoint = strings.TrimRight(strings.TrimSpace(opts.Endpoint), "/")
	opts.Token = strings.TrimSpace(opts.Token)
	if cliFlagSet(flags, "env") && opts.Env == "" {
		return workerDurableOptions{}, fmt.Errorf("--env must not be empty")
	}
	if opts.LogFormat != "text" && opts.LogFormat != "json" {
		return workerDurableOptions{}, fmt.Errorf("invalid --log-format %q", opts.LogFormat)
	}
	for _, service := range opts.Services {
		if service == "" {
			return workerDurableOptions{}, fmt.Errorf("--service must not be empty")
		}
	}
	if opts.Endpoint == "" {
		return workerDurableOptions{}, fmt.Errorf("--endpoint is required")
	}
	if opts.Token == "" {
		return workerDurableOptions{}, fmt.Errorf("--token is required")
	}
	return opts, nil
}

func parseWorkerDurableJobsArgs(args []string) (workerDurableJobsOptions, error) {
	opts := workerDurableJobsOptions{Limit: 100}
	flags := newCLIFlagSet("worker durable jobs")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Service, "service", "", "")
	flags.IntVar(&opts.Limit, "limit", opts.Limit, "")
	flags.BoolVar(&opts.JSON, "json", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return workerDurableJobsOptions{}, err
	}
	if len(positionals) == 0 {
		return workerDurableJobsOptions{}, fmt.Errorf("scenery worker durable jobs requires list, inspect, cancel, or retry")
	}
	opts.Action = positionals[0]
	switch opts.Action {
	case "list":
	case "inspect", "cancel", "retry":
		if len(positionals) < 2 {
			return workerDurableJobsOptions{}, fmt.Errorf("scenery worker durable jobs %s requires a job id", opts.Action)
		}
		opts.JobID = strings.TrimSpace(positionals[1])
		if opts.JobID == "" {
			return workerDurableJobsOptions{}, fmt.Errorf("job id must not be empty")
		}
		positionals = append(positionals[:1], positionals[2:]...)
	default:
		return workerDurableJobsOptions{}, fmt.Errorf("unknown scenery worker durable jobs command %q", opts.Action)
	}
	if len(positionals) > 1 {
		return workerDurableJobsOptions{}, fmt.Errorf("unexpected argument %q", positionals[1])
	}
	opts.Service = strings.TrimSpace(opts.Service)
	if opts.Limit < 1 || opts.Limit > 500 {
		return workerDurableJobsOptions{}, fmt.Errorf("--limit must be between 1 and 500")
	}
	if opts.Service == "" {
		return workerDurableJobsOptions{}, fmt.Errorf("--service is required")
	}
	return opts, nil
}

func runWorker(opts workerOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	result, ok, err := build.LoadReusableBinary(root, cfg)
	if err != nil {
		return err
	}
	if ok {
		if err := build.WriteLatestBuildManifest(result, "compiled"); err != nil {
			return err
		}
		return startWorkerApp(root, cfg, result.Binary, opts)
	}
	result, err = build.App(root, cfg)
	if err != nil {
		return err
	}
	return startWorkerApp(root, cfg, result.Binary, opts)
}

func runWorkerDurable(opts workerDurableOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	result, ok, err := build.LoadReusableBinary(root, cfg)
	if err != nil {
		return err
	}
	if ok {
		if err := build.WriteLatestBuildManifest(result, "compiled"); err != nil {
			return err
		}
		return startDurableWorkerApp(root, cfg, result.Binary, opts)
	}
	result, err = build.App(root, cfg)
	if err != nil {
		return err
	}
	return startDurableWorkerApp(root, cfg, result.Binary, opts)
}

func startWorkerApp(root string, cfg app.Config, binary string, opts workerOptions) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	cmd := commandTreeContext(ctx, binary)
	cmd.Dir = root
	extra := []string{"SCENERY_ROLE=worker"}
	env, err := appProcessEnv(root, cfg, opts.LogFormat, opts.Env, extra...)
	if err != nil {
		return err
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return err
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("scenery worker exited: %w", err)
	}
	return nil
}

func startDurableWorkerApp(root string, cfg app.Config, binary string, opts workerDurableOptions) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	cmd := commandTreeContext(ctx, binary)
	cmd.Dir = root
	extra := []string{
		"SCENERY_ROLE=worker",
		"SCENERY_DURABLE_ENDPOINT=" + opts.Endpoint,
		"SCENERY_DURABLE_TOKEN=" + opts.Token,
	}
	if len(opts.Services) > 0 {
		extra = append(extra, "SCENERY_DURABLE_SERVICES="+strings.Join(opts.Services, ","))
	}
	env, err := appProcessEnv(root, cfg, opts.LogFormat, opts.Env, extra...)
	if err != nil {
		return err
	}
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return err
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("scenery durable worker exited: %w", err)
	}
	return nil
}

func runWorkerDurableTokenCreate(opts workerDurableTokenCreateOptions, stdout io.Writer) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	service, err := durablestore.NormalizeServiceName(opts.Service)
	if err != nil {
		return err
	}
	databaseURL, err := durableDatabaseURLForCLI(root, cfg, service)
	if err != nil {
		return err
	}
	db, err := durablestore.Open(context.Background(), service, databaseURL, durablestore.Options{})
	if err != nil {
		return err
	}
	defer db.Close()

	secret, err := randomDurableTokenSecret()
	if err != nil {
		return err
	}
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		id, err = randomDurableTokenID()
		if err != nil {
			return err
		}
	}
	token, err := db.CreateWorkerToken(context.Background(), durablestore.WorkerTokenRequest{
		ID:     id,
		Name:   opts.Name,
		Secret: secret,
	})
	if err != nil {
		return err
	}
	resp := workerDurableTokenCreateResponse{
		SchemaVersion: "scenery.durable.worker_token.create.v1",
		Service:       service,
		DBPath:        postgresdb.RedactURL(databaseURL),
	}
	resp.App.Name = cfg.Name
	resp.App.Root = root
	resp.Token.ID = token.ID
	resp.Token.Name = token.Name
	resp.Token.Secret = secret
	resp.Token.TokenHash = token.TokenHash
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}
	_, _ = fmt.Fprintf(stdout, "created durable worker token %s for service %s\n", token.ID, service)
	_, _ = fmt.Fprintf(stdout, "secret: %s\n", secret)
	return nil
}

func runWorkerDurableJobs(opts workerDurableJobsOptions, stdout io.Writer) error {
	root, cfg, db, databaseURL, service, err := openWorkerDurableStore(opts.AppRoot, opts.Service)
	if err != nil {
		return err
	}
	_ = root
	_ = cfg
	defer db.Close()
	resp := workerDurableJobsResponse{
		SchemaVersion: "scenery.durable.jobs.v1",
		Service:       service,
		DBPath:        postgresdb.RedactURL(databaseURL),
		Action:        opts.Action,
	}
	switch opts.Action {
	case "list":
		jobs, err := db.ListJobs(context.Background(), opts.Limit)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			resp.Jobs = append(resp.Jobs, durableJobRecordFromStore(job))
		}
	case "inspect":
		job, ok, err := db.GetJob(context.Background(), opts.JobID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("durable job %q not found", opts.JobID)
		}
		record := durableJobRecordFromStore(job)
		resp.Job = &record
		events, err := db.JobEvents(context.Background(), opts.JobID)
		if err != nil {
			return err
		}
		for _, event := range events {
			resp.Events = append(resp.Events, durableJobEventFromStore(event))
		}
	case "cancel":
		if err := db.CancelJob(context.Background(), opts.JobID); err != nil {
			return err
		}
		resp.OK = true
	case "retry":
		if err := db.RetryJob(context.Background(), opts.JobID); err != nil {
			return err
		}
		resp.OK = true
	default:
		return fmt.Errorf("unknown durable jobs action %q", opts.Action)
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}
	switch opts.Action {
	case "list":
		for _, job := range resp.Jobs {
			_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", job.ID, job.TaskName, job.State)
		}
	case "inspect":
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", resp.Job.ID, resp.Job.TaskName, resp.Job.State)
	default:
		_, _ = fmt.Fprintf(stdout, "%s %s\n", opts.Action, opts.JobID)
	}
	return nil
}

func openWorkerDurableStore(appRoot, serviceName string) (string, app.Config, *durablestore.Store, string, string, error) {
	start, err := resolveAppRoot(appRoot)
	if err != nil {
		return "", app.Config{}, nil, "", "", err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return "", app.Config{}, nil, "", "", err
	}
	service, err := durablestore.NormalizeServiceName(serviceName)
	if err != nil {
		return "", app.Config{}, nil, "", "", err
	}
	databaseURL, err := durableDatabaseURLForCLI(root, cfg, service)
	if err != nil {
		return "", app.Config{}, nil, "", "", err
	}
	db, err := durablestore.Open(context.Background(), service, databaseURL, durablestore.Options{})
	if err != nil {
		return "", app.Config{}, nil, "", "", err
	}
	return root, cfg, db, databaseURL, service, nil
}

func durableDatabaseURLForCLI(root string, cfg app.Config, service string) (string, error) {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), root)
	if err != nil {
		return "", err
	}
	if value, _ := lookupEnvValue(env, cfg.DatabaseURLEnv()); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	if value, _ := lookupEnvValue(env, postgresdb.RegistryEnv); strings.TrimSpace(value) != "" {
		registry, err := postgresdb.DecodeRegistry(value)
		if err == nil && strings.TrimSpace(registry.URL) != "" {
			return registry.URL, nil
		}
	}
	serviceEnv := postgresdb.ServiceDatabaseURLEnv(service)
	if value, _ := lookupEnvValue(env, serviceEnv); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	return "", fmt.Errorf("durable store requires %s for service %s", cfg.DatabaseURLEnv(), service)
}

func durableJobRecordFromStore(job durablestore.JobDetail) durableJobRecord {
	return durableJobRecord{
		ID:          job.ID,
		TaskName:    job.TaskName,
		State:       job.State,
		DedupeKey:   job.DedupeKey,
		Attempt:     job.Attempt,
		MaxAttempts: job.MaxAttempts,
		CreatedAt:   job.CreatedAt,
		UpdatedAt:   job.UpdatedAt,
		CompletedAt: job.CompletedAt,
		ErrorCodec:  job.ErrorCodec,
		Error:       string(job.ErrorBlob),
	}
}

func durableJobEventFromStore(event durablestore.JobEvent) durableJobEvent {
	return durableJobEvent{
		Seq:          event.Seq,
		Attempt:      event.Attempt,
		EventType:    event.EventType,
		PayloadCodec: event.PayloadCodec,
		CreatedAt:    event.CreatedAt,
	}
}

func randomDurableTokenSecret() (string, error) {
	return randomDurableTokenString(32)
}

func randomDurableTokenID() (string, error) {
	value, err := randomDurableTokenString(12)
	if err != nil {
		return "", err
	}
	return "tok_" + value, nil
}

func randomDurableTokenString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
