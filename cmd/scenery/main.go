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
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
	"scenery.sh/internal/stdlog"
)

var cliStderr io.Writer = os.Stderr

func main() {
	os.Exit(executeCLI(os.Args[1:]))
}

func executeCLI(args []string) int {
	started := time.Now()
	err := renderMachineError(os.Stdout, args, run(args))
	exitCode := cliExitCode(err)
	recordCLITelemetry(cliTelemetryRecord{
		At:         started.UTC(),
		Command:    telemetryCommand(args),
		DurationMS: time.Since(started).Milliseconds(),
		ExitCode:   exitCode,
		Version:    sceneryVersion,
		Mode:       telemetryMode(args),
	})
	if err != nil {
		if _, silent := errors.AsType[*silentCLIError](err); !silent {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	return exitCode
}

func renderMachineError(stdout io.Writer, args []string, err error) error {
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
	diagnostic := compiler.TransportDiagnostic(kind, err.Error())
	if output == "jsonl" {
		writer := newCLIEventWriter(stdout)
		if encodeErr := writer.write("summary", true, map[string]any{"event_count": 0, "ok": false, "diagnostic": diagnostic}); encodeErr != nil {
			return &silentCLIError{err: fmt.Errorf("internal: encode CLI error event: %w", encodeErr), code: 10}
		}
		return &silentCLIError{err: err, code: code}
	}
	envelope := newCLIEnvelope(false, nil, []graph.Diagnostic{diagnostic})
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
	return json.NewEncoder(w).Encode(newCLIEnvelope(cliDataOK(data), data, nil))
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
	return machine.DecodeData[graph.Diagnostic](encoded, currentMachineSpecRevision(), target)
}

func newCLIEnvelope(ok bool, data any, diagnostics []graph.Diagnostic) machine.Envelope[graph.Diagnostic] {
	return machine.NewEnvelope(currentMachineSpecRevision(), cliProducer(), ok, data, diagnostics)
}

func currentMachineSpecRevision() string { return string(spec.CurrentRevision()) }

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
		return runBindingCompletion(os.Stdout, args[1:])
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
		if len(args) > 1 && args[1] == "sqlc" {
			return generateCommand(args[1:])
		}
		return runContractGenerate(os.Stdout, args[1:])
	case "task":
		return taskCommand(args[1:])
	case "validate":
		return validateCommand(args[1:])
	case "storage":
		return storageCommand(args[1:])
	case "snapshot":
		return snapshotCommand(args[1:])
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
		if handled, err := runBindingCLI(os.Stdout, os.Stderr, args); handled {
			return err
		}
		return fmt.Errorf("unknown command %q; use `scenery help`", args[0])
	}
}

type silentCLIError struct {
	err  error
	code int
}

type cliEventWriter struct {
	w            io.Writer
	sequence     uint64
	specRevision string
	producer     machine.Producer
}

func newCLIEventWriter(w io.Writer) *cliEventWriter {
	return &cliEventWriter{w: w, specRevision: currentMachineSpecRevision(), producer: cliProducer()}
}

func (w *cliEventWriter) write(event string, terminal bool, data any) error {
	w.sequence++
	return json.NewEncoder(w.w).Encode(machine.NewEventEnvelope(
		w.specRevision, w.producer, w.sequence, event, terminal, data, []graph.Diagnostic{},
	))
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
	case "capability_unavailable", "feature_unavailable":
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
	if err := validateRuntimePlan(opts.AppRoot); err != nil {
		return err
	}
	warnDevEscapeHatches(opts)
	if opts.Detach && !detachedDevChildMode() {
		return runDetachedDevFunc(args, opts)
	}
	listen := resolveDevListenRequest(opts)
	return runWithWatchFunc(listen, opts.Verbose, opts.JSON, opts.AppRoot)
}

func validateRuntimePlan(appRootOption string) error {
	start, err := resolveAppRoot(appRootOption)
	if err != nil {
		return err
	}
	appRoot, _, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	result, err := checkCompiledContract(appRoot)
	if err != nil {
		return err
	}
	if !result.Valid() {
		return fmt.Errorf("runtime plan is invalid: %s", firstCompilerDiagnostic(result.Diagnostics))
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
	flags.StringVar(&opts.Listen, "listen", "", "")
	flags.BoolVar(&opts.Verbose, "verbose", false, "")
	flags.StringVar(&opts.Output, "o", "human", "")
	flags.BoolVar(&opts.Detach, "detach", false, "")
	flags.StringVar(&opts.Wait, "wait", opts.Wait, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.ClaimAliases, "claim-aliases", false, "")
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

func warnDevEscapeHatches(opts devOptions) {
	if opts.JSON {
		return
	}
	if opts.ListenSet || opts.PortSet {
		fmt.Fprintln(cliStderr, "scenery: warning: --listen/--port force a manual TCP app backend; this is a debugging escape hatch and can be less parallel-safe than the default agent Unix-socket backend")
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
