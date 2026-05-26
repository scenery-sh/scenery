package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pbrazdil/onlava/internal/stdlog"
)

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
	case "dev":
		return devCommand(args[1:])
	case "run":
		return runCommand(args[1:])
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
    onlava dev [--port <n>] [--listen <addr>] [--app-root <path>] [-v|--verbose] [--json] [--proxy] [--trust]
    onlava run [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
    onlava worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]
    onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
    onlava temporal deployment set-current --build-id <id> [--deployment <name>] [--app-root <path>] [--json]
    onlava temporal deployment ramp --build-id <id> --percentage <n> [--deployment <name>] [--app-root <path>] [--json]
    onlava temporal deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
    onlava version [--json]
    onlava build [--app-root <path>] [-o <path>] [--db-studio]
    onlava check [--app-root <path>] [--json]
    onlava harness [--app-root <path>] [--json] [--write]
    onlava harness self [--repo-root <path>] [--json] [--write]
    onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
    onlava inspect app|routes|services|endpoints|wire|build|paths|temporal|traces|metrics --json [--app-root <path>]
    onlava inspect docs --json [--repo-root <path>]
    onlava inspect data --json --database-url <postgres-url> [--tenant <key>] [--object <name>]
    onlava inspect traces --json [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
    onlava inspect metrics --json [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
    onlava admin traces clear --json [--app-root <path>]
    onlava logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [-f|--follow] [--jsonl|--json]
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
	addr := resolveListenAddr(opts.Listen, opts.Port)
	return runWithWatchFunc(addr, opts.Verbose, opts.JSON, opts.AppRoot)
}

type devOptions struct {
	Listen  string
	Port    int
	Verbose bool
	JSON    bool
	AppRoot string
	Proxy   bool
	Trust   bool
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
		case "--listen":
			i++
			if i >= len(args) {
				return devOptions{}, fmt.Errorf("missing value for --listen")
			}
			opts.Listen = args[i]
		case "--verbose", "-v":
			opts.Verbose = true
		case "--json":
			opts.JSON = true
		case "--proxy":
			opts.Proxy = true
		case "--trust":
			opts.Trust = true
			opts.Proxy = true
		case "--app-root":
			i++
			if i >= len(args) {
				return devOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			return devOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func configureDevProcessEnv(opts devOptions) func() {
	changes := map[string]string{}
	if opts.Proxy || !devProxyDisabledByEnv() {
		changes["ONLAVA_LOCAL_PROXY"] = "1"
		if opts.Trust {
			changes["ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"] = "0"
		} else if _, ok := os.LookupEnv("ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"); !ok {
			changes["ONLAVA_LOCAL_PROXY_SKIP_TRUST_INSTALL"] = "0"
		}
	}
	return applyTemporaryEnv(changes)
}

func devProxyDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ONLAVA_LOCAL_PROXY"))) {
	case "0", "false", "no", "off":
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
