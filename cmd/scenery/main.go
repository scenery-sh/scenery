package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/stdlog"
	"scenery.sh/internal/vnext"
)

var cliStderr io.Writer = os.Stderr

func main() {
	if err := renderVNextMachineError(os.Stdout, os.Args[1:], run(os.Args[1:])); err != nil {
		if _, ok := errors.AsType[*silentCLIError](err); !ok {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(cliExitCode(err))
	}
}

func renderVNextMachineError(stdout io.Writer, args []string, err error) error {
	output := requestedCLIOutput(args)
	if err == nil || (output != "json" && output != "jsonl") {
		return err
	}
	if _, alreadyRendered := errors.AsType[*silentCLIError](err); alreadyRendered {
		return err
	}
	code := cliExitCode(err)
	kind, _, _ := strings.Cut(err.Error(), ":")
	if code == 2 {
		kind = "invalid_request"
	} else if code == 3 && kind != "revision_conflict" {
		kind = "failed_precondition"
	} else if code == 4 {
		kind = "capability_unavailable"
	} else if code == 5 {
		kind = "permission_denied"
	} else if code == 10 {
		kind = "internal"
	}
	diagnostic := vnext.TransportDiagnostic(kind, err.Error())
	if output == "jsonl" {
		writer := newCLIEventWriter(stdout)
		if encodeErr := writer.write("summary", true, map[string]any{"event_count": 0, "ok": false, "diagnostic": diagnostic}); encodeErr != nil {
			return &silentCLIError{err: fmt.Errorf("internal: encode CLI error event: %w", encodeErr), code: 10}
		}
		return &silentCLIError{err: err, code: code}
	}
	envelope := vnextEnvelope{
		APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: false,
		WorkspaceRevision: nil, ContractRevision: nil, ImplementationRevision: nil, DeploymentRevision: nil,
		Data: nil, Diagnostics: []vnext.Diagnostic{diagnostic},
	}
	if encodeErr := json.NewEncoder(stdout).Encode(envelope); encodeErr != nil {
		return &silentCLIError{err: fmt.Errorf("internal: encode CLI error envelope: %w", encodeErr), code: 10}
	}
	return &silentCLIError{err: err, code: code}
}

func requestedCLIOutput(args []string) string {
	for index, argument := range args {
		if argument == "-o" {
			if index+1 < len(args) {
				return args[index+1]
			}
			return ""
		}
		if strings.HasPrefix(argument, "-o=") {
			return strings.TrimPrefix(argument, "-o=")
		}
	}
	return ""
}

func writeCLIJSON(w io.Writer, data any) error {
	return json.NewEncoder(w).Encode(vnextEnvelope{
		APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: cliDataOK(data),
		WorkspaceRevision: nil, ContractRevision: nil, ImplementationRevision: nil, DeploymentRevision: nil,
		Data: data, Diagnostics: []vnext.Diagnostic{},
	})
}

func cliDataOK(data any) bool {
	value := reflect.ValueOf(data)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Struct {
		if ok := value.FieldByName("OK"); ok.IsValid() && ok.Kind() == reflect.Bool {
			return ok.Bool()
		}
	}
	if value, ok := data.(map[string]any); ok {
		if result, present := value["ok"].(bool); present {
			return result
		}
	}
	return true
}

func decodeCLIJSON(encoded []byte, target any) error {
	var envelope struct {
		APIVersion string          `json:"api_version"`
		Data       json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return err
	}
	if envelope.APIVersion != "scenery.cli.v1" {
		return fmt.Errorf("unexpected CLI API version %q", envelope.APIVersion)
	}
	return json.Unmarshal(envelope.Data, target)
}

func init() {
	stdlog.Install(os.Stderr)
	log.SetFlags(log.LstdFlags)
}

func run(args []string) error {
	if len(args) == 0 {
		writeRootHelp(os.Stdout)
		return nil
	}
	switch args[0] {
	case "help":
		return helpCommand(args[1:])
	case "completion":
		return runVNextCompletion(os.Stdout, args[1:])
	case "up":
		return upCommand(args[1:])
	case "ps":
		return statusCommand(args[1:])
	case "console":
		return consoleCommand(args[1:])
	case "down":
		return downCommand(args[1:])
	case "prune":
		return pruneCommand(args[1:])
	case "db":
		return dbCommand(args[1:])
	case "worktree":
		return worktreeCommand(args[1:])
	case "generate":
		if hasCLIArg(args[1:], "--check") || hasCLIArg(args[1:], "--target") || isVNextGenerate(args[1:]) {
			return runVNextGenerate(os.Stdout, args[1:])
		}
		return generateCommand(args[1:])
	case "task":
		return taskCommand(args[1:])
	case "validate":
		return validateCommand(args[1:])
	case "storage":
		return storageCommand(args[1:])
	case "deploy":
		return deployCommand(args[1:])
	case "symphony":
		return symphonyCommand(args[1:])
	case "worker":
		return workerCommand(args[1:])
	case "version":
		return versionCommand(args[1:])
	case "upgrade":
		return upgradeCommand(args[1:])
	case "doctor":
		return doctorCommand(args[1:])
	case "build":
		return buildCommand(args[1:])
	case "check":
		return checkCommand(args[1:])
	case "fmt":
		return fmtCommand(args[1:])
	case "compile":
		return compileCommand(args[1:])
	case "schema":
		return schemaCommand(args[1:])
	case "list":
		return listCommand(args[1:])
	case "get":
		return getCommand(args[1:])
	case "explain":
		return explainCommand(args[1:])
	case "diff":
		return diffCommand(args[1:])
	case "graph":
		return graphCommand(args[1:])
	case "agent":
		return agentCommand(args[1:])
	case "changes":
		return changesCommand(args[1:])
	case "harness":
		return harnessCommand(args[1:])
	case "inspect":
		return inspectCommand(args[1:])
	case "logs":
		return logsCommand(args[1:])
	case "test":
		return testCommand(args[1:])
	case "traces":
		return tracesCommand(args[1:])
	case "metrics":
		return metricsCommand(args[1:])
	case "system":
		return systemCommand(args[1:])
	case "internal":
		return internalCommand(args[1:])
	default:
		if handled, err := runVNextBindingCLI(os.Stdout, os.Stderr, args); handled {
			return err
		}
		return fmt.Errorf("unknown command %q; use `scenery help`", args[0])
	}
}

type silentCLIError struct {
	err  error
	code int
}

type cliEventEnvelope struct {
	APIVersion             string             `json:"api_version"`
	DiagnosticCatalog      string             `json:"diagnostic_catalog"`
	Sequence               uint64             `json:"sequence"`
	Kind                   string             `json:"kind"`
	Terminal               bool               `json:"terminal"`
	WorkspaceRevision      any                `json:"workspace_revision"`
	ContractRevision       any                `json:"contract_revision"`
	ImplementationRevision any                `json:"implementation_revision"`
	DeploymentRevision     any                `json:"deployment_revision"`
	Data                   any                `json:"data"`
	Diagnostics            []vnext.Diagnostic `json:"diagnostics"`
}

type cliEventWriter struct {
	w        io.Writer
	sequence uint64
}

func newCLIEventWriter(w io.Writer) *cliEventWriter { return &cliEventWriter{w: w} }

func (w *cliEventWriter) write(kind string, terminal bool, data any) error {
	w.sequence++
	return json.NewEncoder(w.w).Encode(cliEventEnvelope{
		APIVersion: "scenery.cli.event.v1", DiagnosticCatalog: vnext.DiagnosticCatalog,
		Sequence: w.sequence, Kind: kind, Terminal: terminal,
		WorkspaceRevision: nil, ContractRevision: nil, ImplementationRevision: nil, DeploymentRevision: nil,
		Data: data, Diagnostics: []vnext.Diagnostic{},
	})
}

func (w *cliEventWriter) event(data any) error { return w.write("event", false, data) }
func (w *cliEventWriter) summary(count int) error {
	return w.write("summary", true, map[string]any{"event_count": count})
}

func (e *silentCLIError) ExitCode() int {
	if e != nil && e.code != 0 {
		return e.code
	}
	return 1
}

type codedCLIError struct {
	err  error
	code int
}

func (e *codedCLIError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *codedCLIError) Unwrap() error { return e.err }
func (e *codedCLIError) ExitCode() int { return e.code }

func cliExitCode(err error) int {
	if err == nil {
		return 0
	}
	type exitCoder interface {
		error
		ExitCode() int
	}
	if coded, ok := errors.AsType[exitCoder](err); ok {
		return coded.ExitCode()
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	kind, _, _ := strings.Cut(message, ":")
	switch kind {
	case "permission_denied":
		return 5
	case "capability_unavailable", "unsupported_profile":
		return 4
	case "revision_conflict", "failed_precondition":
		return 3
	case "invalid_request":
		return 2
	case "internal":
		return 10
	}
	if strings.HasPrefix(message, "usage:") || strings.HasPrefix(message, "unknown command ") || strings.HasPrefix(message, "unknown subcommand ") || strings.HasPrefix(message, "unexpected argument ") || strings.HasPrefix(message, "unsupported output ") || knownCLINotFound(message) {
		return 2
	}
	return 10
}

func knownCLINotFound(message string) bool {
	if !strings.HasSuffix(message, " not found") {
		return false
	}
	for _, prefix := range []string{"resource ", "schema ", "migration service ", "typescript client target ", "http gateway "} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}

func (e *silentCLIError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

var (
	runWithWatchFunc   = runWithWatch
	runDetachedDevFunc = runDetachedDev
)

func upCommand(args []string) error {
	opts, err := parseDevArgs(args)
	if err != nil {
		return err
	}
	if err := validateVNextRuntimePlan(opts.AppRoot); err != nil {
		return err
	}
	restore := configureDevProcessEnv(opts)
	defer restore()
	warnDevEscapeHatches(opts)
	if opts.Detach && !detachedDevChildMode() {
		return runDetachedDevFunc(args, opts)
	}
	listen := resolveDevListenRequest(opts)
	return runWithWatchFunc(listen, opts.Verbose, opts.JSON, opts.AppRoot)
}

func validateVNextRuntimePlan(appRootOption string) error {
	start, err := resolveAppRoot(appRootOption)
	if err != nil {
		return err
	}
	appRoot, _, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	result, err := vnext.Check(appRoot)
	if err != nil {
		return err
	}
	if !result.Valid() {
		return fmt.Errorf("vNext runtime plan is invalid: %s", firstVNextDiagnostic(result.Diagnostics))
	}
	return nil
}

type devOptions struct {
	Listen       string
	Port         int
	ListenSet    bool
	PortSet      bool
	Verbose      bool
	JSON         bool
	Output       string
	AppRoot      string
	Detach       bool
	Wait         string
	ClaimAliases bool
}

type devListenRequest struct {
	Network      string
	Addr         string
	Explicit     bool
	PreferTCP    bool
	ClaimAliases bool
}

func parseDevArgs(args []string) (devOptions, error) {
	opts := devOptions{Port: 4000, Wait: detachedDevWaitReady}
	flags := newCLIFlagSet("up")
	flags.IntVar(&opts.Port, "port", opts.Port, "")
	flags.IntVar(&opts.Port, "p", opts.Port, "")
	flags.StringVar(&opts.Listen, "listen", "", "")
	flags.BoolVar(&opts.Verbose, "verbose", false, "")
	flags.BoolVar(&opts.Verbose, "v", false, "")
	flags.StringVar(&opts.Output, "o", "human", "")
	flags.BoolVar(&opts.Detach, "detach", false, "")
	flags.StringVar(&opts.Wait, "wait", opts.Wait, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.ClaimAliases, "claim-aliases", false, "")
	rejectCLIFlag(flags, "session", "scenery up no longer accepts --session; one app root has one live dev runtime, so use --app-root or a separate Git worktree")
	rejectCLIFlag(flags, "new-session", "scenery up no longer accepts --new-session; use a separate Git worktree for another live code copy")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return devOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return devOptions{}, err
	}
	opts.PortSet = cliFlagSet(flags, "port", "p")
	opts.ListenSet = cliFlagSet(flags, "listen")
	opts.Wait, err = normalizeDetachedDevWaitMode(opts.Wait)
	if err != nil {
		return devOptions{}, err
	}
	if opts.Output != "human" && opts.Output != "json" && opts.Output != "jsonl" {
		return devOptions{}, fmt.Errorf("unsupported output %q", opts.Output)
	}
	if opts.Detach && opts.Output == "jsonl" {
		return devOptions{}, fmt.Errorf("unsupported output %q for detached up; use -o json", opts.Output)
	}
	if !opts.Detach && opts.Output == "json" {
		return devOptions{}, fmt.Errorf("unsupported output %q for streaming up; use -o jsonl", opts.Output)
	}
	opts.JSON = opts.Output != "human"
	return opts, nil
}

func resolveDevListenRequest(opts devOptions) devListenRequest {
	if opts.ListenSet || opts.PortSet {
		return devListenRequest{
			Network:      "tcp",
			Addr:         resolveListenAddr(opts.Listen, opts.Port),
			Explicit:     true,
			ClaimAliases: opts.ClaimAliases,
		}
	}
	return devListenRequest{
		ClaimAliases: opts.ClaimAliases,
	}
}

func configureDevProcessEnv(opts devOptions) func() {
	return applyTemporaryEnv(nil)
}

func warnDevEscapeHatches(opts devOptions) {
	if opts.JSON {
		return
	}
	if opts.ListenSet || opts.PortSet {
		fmt.Fprintln(cliStderr, "scenery: warning: --listen/--port force a manual TCP app backend; this is a debugging escape hatch and can be less parallel-safe than the default agent Unix-socket backend")
	}
}

func applyTemporaryEnv(values map[string]string) func() {
	if len(values) == 0 {
		return func() {}
	}
	type previousValue struct {
		value string
		ok    bool
	}
	previous := make(map[string]previousValue, len(values))
	for key, value := range values {
		old, ok := envpolicy.Lookup(key)
		previous[key] = previousValue{value: old, ok: ok}
		_ = envpolicy.Set(key, value)
	}
	return func() {
		for key, old := range previous {
			if old.ok {
				_ = envpolicy.Set(key, old.value)
			} else {
				_ = envpolicy.Unset(key)
			}
		}
	}
}

func resolveListenAddr(listen string, port int) string {
	if listen == "" {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	if _, _, err := net.SplitHostPort(listen); err == nil {
		return listen
	}
	return net.JoinHostPort(listen, strconv.Itoa(port))
}

func resolveAppRoot(start string) (string, error) {
	if start == "" {
		return ".", nil
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	return abs, nil
}
