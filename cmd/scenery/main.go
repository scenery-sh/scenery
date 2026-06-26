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

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/stdlog"
)

var cliStderr io.Writer = os.Stderr

func main() {
	if err := run(os.Args[1:]); err != nil {
		if _, ok := errors.AsType[*silentCLIError](err); !ok {
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
		writeRootHelp(os.Stdout)
		return nil
	}
	switch args[0] {
	case "help":
		return helpCommand(args[1:])
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
	case "worktree":
		return worktreeCommand(args[1:])
	case "generate":
		return generateCommand(args[1:])
	case "task":
		return taskCommand(args[1:])
	case "validate":
		return validateCommand(args[1:])
	case "storage":
		return storageCommand(args[1:])
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
		return fmt.Errorf("unknown command %q; use `scenery help`", args[0])
	}
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
		return legacyDevProxyError("SCENERY_LOCAL_PROXY")
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
	Listen       string
	Port         int
	ListenSet    bool
	PortSet      bool
	Verbose      bool
	JSON         bool
	AppRoot      string
	Detach       bool
	ClaimAliases bool
	Trust        bool
}

type devListenRequest struct {
	Network      string
	Addr         string
	Explicit     bool
	PreferTCP    bool
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
			return devOptions{}, fmt.Errorf("--trust moved to `scenery system trust`")
		case "--detach":
			opts.Detach = true
		case "--app-root":
			i++
			if i >= len(args) {
				return devOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--session":
			return devOptions{}, fmt.Errorf("scenery up no longer accepts --session; one app root has one live dev runtime, so use --app-root or a separate Git worktree")
		case "--new-session":
			return devOptions{}, fmt.Errorf("scenery up no longer accepts --new-session; use a separate Git worktree for another live code copy")
		case "--claim-aliases":
			opts.ClaimAliases = true
		default:
			return devOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func legacyDevProxyError(source string) error {
	return fmt.Errorf("%s no longer enables the legacy local proxy in `scenery up`; use the default agent-routed app URLs, or run `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, then `scenery system edge trust` to prepare trusted local HTTPS", source)
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

func devProxyEnabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(envpolicy.Get("SCENERY_LOCAL_PROXY"))) {
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
