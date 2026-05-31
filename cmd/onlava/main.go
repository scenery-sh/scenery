package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pbrazdil/onlava/internal/stdlog"
)

var cliStderr io.Writer = os.Stderr

func main() {
	if err := run(os.Args[1:]); err != nil {
		var silent *silentCLIError
		if !errors.As(err, &silent) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func init() {
	stdlog.Install(os.Stderr)
	log.SetFlags(log.LstdFlags)
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "agent":
		return agentCommand(args[1:])
	case "dev":
		return devCommand(args[1:])
	case "attach":
		return attachCommand(args[1:])
	case "console":
		return consoleCommand(args[1:])
	case "run":
		return runCommand(args[1:])
	case "status":
		return statusCommand(args[1:])
	case "down":
		return downCommand(args[1:])
	case "prune":
		return pruneCommand(args[1:])
	case "db":
		return dbCommand(args[1:])
	case "worker":
		return workerCommand(args[1:])
	case "temporal":
		return temporalCommand(args[1:])
	case "version":
		return versionCommand(args[1:])
	case "build":
		return buildCommand(args[1:])
	case "psql":
		return psqlCommand(args[1:])
	case "check":
		return checkCommand(args[1:])
	case "harness":
		return harnessCommand(args[1:])
	case "inspect":
		return inspectCommand(args[1:])
	case "admin":
		return adminCommand(args[1:])
	case "logs":
		return logsCommand(args[1:])
	case "test":
		return testCommand(args[1:])
	case "gen":
		return genCommand(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usageError() error {
	return fmt.Errorf(`usage:
  stable/dev commands:
    onlava dev [--port <n>] [--listen <addr>] [--app-root <path>] [--session <id>|--new-session] [-v|--verbose] [--json] [--proxy] [--trust] [--detach]
    onlava attach [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria|sqlite] [--jsonl|--json] [--tui]
    onlava console [--app-root <path>] [--session current|<id>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>]
    onlava agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
    onlava agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
    onlava status --json [--app-root <path>] [--session <id>] [--watch]
    onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all]
    onlava prune --older-than <duration> [--app-root <path>] [--json]
    onlava db psql [--app-root <path>] [psql args...]
    onlava db reset [--app-root <path>]
    onlava db drop [--app-root <path>]
    onlava db snapshot create|restore <name> [--app-root <path>]
    onlava run [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
    onlava worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]
    onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
    onlava worker typescript [--task-queue <name>[,<name>]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
    onlava temporal deployment set-current --build-id <id> [--deployment <name>] [--app-root <path>] [--json]
    onlava temporal deployment ramp --build-id <id> --percentage <n> [--deployment <name>] [--app-root <path>] [--json]
    onlava temporal deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
    onlava version [--json]
    onlava build [--app-root <path>] [-o <path>]
    onlava check [--app-root <path>] [--json]
    onlava harness [--app-root <path>] [--json] [--write]
    onlava harness self [--repo-root <path>] [--json] [--write] [--quick|--race|--release]
    onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
    onlava inspect app|routes|services|endpoints|wire|build|paths|temporal|traces|metrics --json [--app-root <path>]
    onlava inspect docs --json [--repo-root <path>]
    onlava inspect traces --json [--session current|<id>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
    onlava inspect metrics --json [--session current|<id>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
    onlava admin traces clear --json [--app-root <path>]
    onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria|sqlite] [-f|--follow] [--jsonl|--json]
    onlava test [--app-root <path>] [go test flags/packages...]
    onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]

  beta/dev helpers:
    onlava psql [--app-root <path>] [psql args...]`)
}

type silentCLIError struct {
	err error
}

func (e *silentCLIError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func devCommand(args []string) error {
	opts, err := parseDevArgs(args)
	if err != nil {
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

type devOptions struct {
	Listen     string
	Port       int
	ListenSet  bool
	PortSet    bool
	Verbose    bool
	JSON       bool
	AppRoot    string
	SessionID  string
	NewSession bool
	Proxy      bool
	Trust      bool
	Detach     bool
}

type devListenRequest struct {
	Network    string
	Addr       string
	Explicit   bool
	PreferTCP  bool
	SessionID  string
	NewSession bool
}

func parseDevArgs(args []string) (devOptions, error) {
	opts := devOptions{Port: 4000}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port", "-p":
			i++
			if i >= len(args) {
				return devOptions{}, fmt.Errorf("missing value for --port")
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return devOptions{}, fmt.Errorf("invalid port %q", args[i])
			}
			opts.Port = value
			opts.PortSet = true
		case "--listen":
			i++
			if i >= len(args) {
				return devOptions{}, fmt.Errorf("missing value for --listen")
			}
			opts.Listen = args[i]
			opts.ListenSet = true
		case "--verbose", "-v":
			opts.Verbose = true
		case "--json":
			opts.JSON = true
		case "--proxy":
			opts.Proxy = true
		case "--trust":
			opts.Trust = true
			opts.Proxy = true
		case "--detach":
			opts.Detach = true
		case "--app-root":
			i++
			if i >= len(args) {
				return devOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--session":
			i++
			if i >= len(args) {
				return devOptions{}, fmt.Errorf("missing value for --session")
			}
			opts.SessionID = args[i]
		case "--new-session":
			opts.NewSession = true
		default:
			return devOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if strings.TrimSpace(opts.SessionID) != "" && opts.NewSession {
		return devOptions{}, fmt.Errorf("--session and --new-session cannot be used together")
	}
	return opts, nil
}

func resolveDevListenRequest(opts devOptions) devListenRequest {
	if opts.ListenSet || opts.PortSet {
		return devListenRequest{
			Network:    "tcp",
			Addr:       resolveListenAddr(opts.Listen, opts.Port),
			Explicit:   true,
			SessionID:  opts.SessionID,
			NewSession: opts.NewSession,
		}
	}
	return devListenRequest{
		PreferTCP:  opts.Proxy || opts.Trust || devProxyEnabledByEnv(),
		SessionID:  opts.SessionID,
		NewSession: opts.NewSession,
	}
}

func configureDevProcessEnv(opts devOptions) func() {
	changes := map[string]string{}
	if opts.Proxy || opts.Trust {
		changes["ONLAVA_LOCAL_PROXY"] = "1"
		if opts.Trust {
			changes["ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"] = "0"
		} else if _, ok := os.LookupEnv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); !ok {
			changes["ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"] = "0"
		}
	} else if devProxyEnabledByEnv() {
		if _, ok := os.LookupEnv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); !ok {
			changes["ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"] = "0"
		}
	}
	return applyTemporaryEnv(changes)
}

func warnDevEscapeHatches(opts devOptions) {
	if opts.JSON {
		return
	}
	if opts.ListenSet || opts.PortSet {
		fmt.Fprintln(cliStderr, "onlava: warning: --listen/--port force a manual TCP app backend; this is a debugging escape hatch and can be less parallel-safe than the default agent Unix-socket backend")
	}
	if opts.Proxy || opts.Trust || devProxyEnabledByEnv() {
		fmt.Fprintln(cliStderr, "onlava: warning: local proxy mode uses legacy machine-global proxy ports; prefer default agent-routed session URLs for parallel worktrees")
	}
}

func devProxyEnabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ONLAVA_LOCAL_PROXY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
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
		old, ok := os.LookupEnv(key)
		previous[key] = previousValue{value: old, ok: ok}
		_ = os.Setenv(key, value)
	}
	return func() {
		for key, old := range previous {
			if old.ok {
				_ = os.Setenv(key, old.value)
			} else {
				_ = os.Unsetenv(key)
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
