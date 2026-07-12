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
	if err == nil || !requestsVNextJSON(args) {
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

func requestsVNextJSON(args []string) bool {
	for index, argument := range args {
		if argument == "-o" {
			return index+1 < len(args) && args[index+1] == "json"
		}
		if argument == "-o=json" {
			return true
		}
	}
	return false
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
	normalized, apiVersion, err := normalizeCLIAPIVersion(args)
	if err != nil {
		return err
	}
	args = normalized
	if apiVersion == "scenery.cli.v0" && v1OnlyCommand(args) {
		return fmt.Errorf("invalid_request: SCN_LEGACY_CLI_UNREPRESENTABLE: %s has no scenery.cli.v0 representation", strings.Join(args[:minInt(2, len(args))], " "))
	}
	if apiVersion == "scenery.cli.v1" && args[0] == "check" && !hasVNextOutputSelector(args[1:]) {
		args = append(args, "-o", "human")
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

func normalizeCLIAPIVersion(args []string) ([]string, string, error) {
	clean := make([]string, 0, len(args))
	selected := ""
	for index := 0; index < len(args); index++ {
		argument := args[index]
		value := ""
		switch {
		case argument == "--api-version":
			if index+1 >= len(args) {
				return nil, "", fmt.Errorf("invalid_request: --api-version requires scenery.cli.v0 or scenery.cli.v1")
			}
			index++
			value = args[index]
		case strings.HasPrefix(argument, "--api-version="):
			value = strings.TrimPrefix(argument, "--api-version=")
		default:
			clean = append(clean, argument)
			continue
		}
		if value != "scenery.cli.v0" && value != "scenery.cli.v1" {
			return nil, "", fmt.Errorf("invalid_request: unknown CLI API version %q", value)
		}
		if selected != "" && selected != value {
			return nil, "", fmt.Errorf("invalid_request: conflicting CLI API versions")
		}
		selected = value
	}
	legacyJSON, currentOutput := hasCLIArg(clean, "--json"), hasVNextOutputSelector(clean)
	if legacyJSON && currentOutput {
		return nil, "", fmt.Errorf("invalid_request: conflicting scenery.cli.v0 --json and scenery.cli.v1 -o selectors")
	}
	if selected == "scenery.cli.v0" && currentOutput || selected == "scenery.cli.v1" && legacyJSON {
		return nil, "", fmt.Errorf("invalid_request: CLI API version conflicts with the output selector")
	}
	return clean, selected, nil
}

func hasVNextOutputSelector(args []string) bool {
	for index, argument := range args {
		value := ""
		switch {
		case argument == "-o" && index+1 < len(args):
			value = args[index+1]
		case strings.HasPrefix(argument, "-o="):
			value = strings.TrimPrefix(argument, "-o=")
		}
		switch value {
		case "human", "json", "jsonl":
			return true
		}
	}
	return false
}

func v1OnlyCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "compile", "schema", "list", "get", "explain", "diff", "graph", "changes":
		return true
	case "agent":
		return len(args) > 1 && args[1] == "serve"
	case "deploy":
		return len(args) > 1 && (args[1] == "plan" || args[1] == "apply")
	default:
		return false
	}
}

type silentCLIError struct {
	err  error
	code int
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
	flags.BoolVar(&opts.JSON, "json", false, "")
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
