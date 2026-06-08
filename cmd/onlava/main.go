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

	"github.com/pbrazdil/onlava/internal/envpolicy"
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
	case "up":
		return upCommand(args[1:])
	case "ps":
		return statusCommand(args[1:])
	case "console":
		return consoleCommand(args[1:])
	case "serve":
		return serveCommand(args[1:])
	case "down":
		return downCommand(args[1:])
	case "prune":
		return pruneCommand(args[1:])
	case "db":
		return dbCommand(args[1:])
	case "generate":
		return generateCommand(args[1:])
	case "task":
		return taskCommand(args[1:])
	case "worker":
		return workerCommand(args[1:])
	case "version":
		return versionCommand(args[1:])
	case "doctor":
		return doctorCommand(args[1:])
	case "build":
		return buildCommand(args[1:])
	case "check":
		return checkCommand(args[1:])
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
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usageError() error {
	return fmt.Errorf(`usage:
  commands:
    onlava up [--port <n>] [--listen <addr>] [--app-root <path>] [--session <id>|--new-session] [--claim-aliases] [-v|--verbose] [--json] [--detach]
    onlava ps --json [--app-root <path>] [--session <id>] [--watch]
    onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [-f|--follow] [--jsonl|--json]
    onlava logs query [--app-root <path>] [--session current|<id>] --query <logsql> [--since <duration>] [--start <time>] [--end <time>] [--limit <n>] [--timeout <duration>] [--fields <csv>] [--json|--jsonl]
    onlava logs tail [--app-root <path>] [--session current|<id>] --query <logsql> [--since <duration>] [--timeout <duration>] [--fields <csv>] [--jsonl]
    onlava console [--app-root <path>] [--session current|<id>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]
    onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all]
    onlava prune --older-than <duration> [--app-root <path>] [--json]
    onlava db psql [--app-root <path>] [psql args...]
    onlava db apply [--app-root <path>] [--json]
    onlava db seed [--app-root <path>] [--dry-run] [--json]
    onlava db setup [--app-root <path>] [--json]
    onlava db reset [--app-root <path>]
    onlava db drop [--app-root <path>]
    onlava db snapshot create|restore <name> [--app-root <path>]
    onlava generate [--app-root <path>] [--dry-run] [--json]
    onlava generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
    onlava generate sqlc [--app-root <path>] [--dry-run] [--json]
    onlava task list [--app-root <path>] [--json]
    onlava task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
    onlava task run <target> [--app-root <path>] [--env <name>] [--lang go|typescript] [-- script args...]
    onlava task graph --json [--app-root <path>]
    onlava serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
    onlava worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]
    onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
    onlava worker typescript [--task-queue <name>[,<name>]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
    onlava worker deployment set-current --build-id <id> [--deployment <name>] [--app-root <path>] [--json]
    onlava worker deployment ramp --build-id <id> --percentage <n> [--deployment <name>] [--app-root <path>] [--json]
    onlava worker deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
    onlava doctor [--app-root <path>] [--json]
    onlava version [--json]
    onlava build [--app-root <path>] [-o <path>]
    onlava check [--app-root <path>] [--json]
    onlava harness [--app-root <path>] [--json] [--write]
    onlava harness self [--repo-root <path>] [--json] [--write] [--quick|--race|--release]
    onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
    onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|observability --json [--app-root <path>]
    onlava inspect docs --json [--repo-root <path>]
    onlava inspect harness --json [--app-root <path>] [--repo-root <path>]
    onlava traces list [--json] [--session current|<id>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest] [--app-root <path>]
    onlava traces clear --json [--app-root <path>]
    onlava metrics list [--json] [--session current|<id>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>] [--app-root <path>]
    onlava metrics query [--json] [--app-root <path>] [--session current|<id>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]
    onlava metrics labels [--json] [--app-root <path>] [--session current|<id>] [--match <selector>] [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
    onlava metrics series [--json] [--app-root <path>] [--session current|<id>] --match <selector> [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
    onlava test [--app-root <path>] [go test flags/packages...]
    onlava system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
    onlava system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
    onlava system edge install|trust|status|restart|uninstall|dns|privileged [--json]
    onlava system toolchain list|sync|verify|path [--json]
    onlava system trust [--json]`)
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

func upCommand(args []string) error {
	opts, err := parseDevArgs(args)
	if err != nil {
		return err
	}
	if devProxyEnabledByEnv() {
		return legacyDevProxyError("ONLAVA_LOCAL_PROXY")
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

func devCommand(args []string) error {
	return upCommand(args)
}

type devOptions struct {
	Listen       string
	Port         int
	ListenSet    bool
	PortSet      bool
	Verbose      bool
	JSON         bool
	AppRoot      string
	SessionID    string
	NewSession   bool
	Detach       bool
	ClaimAliases bool
	Trust        bool
}

type devListenRequest struct {
	Network      string
	Addr         string
	Explicit     bool
	PreferTCP    bool
	SessionID    string
	NewSession   bool
	ClaimAliases bool
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
			return devOptions{}, legacyDevProxyError("--proxy")
		case "--trust":
			return devOptions{}, fmt.Errorf("--trust moved to `onlava system trust`")
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
		case "--claim-aliases":
			opts.ClaimAliases = true
		default:
			return devOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if strings.TrimSpace(opts.SessionID) != "" && opts.NewSession {
		return devOptions{}, fmt.Errorf("--session and --new-session cannot be used together")
	}
	return opts, nil
}

func legacyDevProxyError(source string) error {
	return fmt.Errorf("%s no longer enables the legacy local proxy in `onlava up`; use the default agent-routed session URLs, or run `onlava system edge dns install`, `onlava system edge privileged install`, `onlava system edge install`, then `onlava system edge trust` to prepare trusted local HTTPS", source)
}

func resolveDevListenRequest(opts devOptions) devListenRequest {
	if opts.ListenSet || opts.PortSet {
		return devListenRequest{
			Network:      "tcp",
			Addr:         resolveListenAddr(opts.Listen, opts.Port),
			Explicit:     true,
			SessionID:    opts.SessionID,
			NewSession:   opts.NewSession,
			ClaimAliases: opts.ClaimAliases,
		}
	}
	return devListenRequest{
		SessionID:    opts.SessionID,
		NewSession:   opts.NewSession,
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
		fmt.Fprintln(cliStderr, "onlava: warning: --listen/--port force a manual TCP app backend; this is a debugging escape hatch and can be less parallel-safe than the default agent Unix-socket backend")
	}
}

func devProxyEnabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(envpolicy.Get("ONLAVA_LOCAL_PROXY"))) {
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
