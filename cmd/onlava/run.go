package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
)

type runOptions struct {
	Listen    string
	Port      int
	AppRoot   string
	Env       string
	LogFormat string
}

func runCommand(args []string) error {
	opts, err := parseRunArgs(args)
	if err != nil {
		return err
	}
	addr := resolveListenAddr(opts.Listen, opts.Port)
	return runHeadlessFunc(addr, opts)
}

var (
	runWithWatchFunc = runWithWatch
	runHeadlessFunc  = runHeadless
)

func parseRunArgs(args []string) (runOptions, error) {
	opts := runOptions{Port: 4000, LogFormat: "text"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port", "-p":
			i++
			if i >= len(args) {
				return runOptions{}, fmt.Errorf("missing value for --port")
			}
			value, err := parsePort(args[i])
			if err != nil {
				return runOptions{}, err
			}
			opts.Port = value
		case "--listen":
			i++
			if i >= len(args) {
				return runOptions{}, fmt.Errorf("missing value for --listen")
			}
			opts.Listen = args[i]
		case "--app-root":
			i++
			if i >= len(args) {
				return runOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--env":
			i++
			if i >= len(args) {
				return runOptions{}, fmt.Errorf("missing value for --env")
			}
			opts.Env = strings.TrimSpace(args[i])
			if opts.Env == "" {
				return runOptions{}, fmt.Errorf("--env must not be empty")
			}
		case "--log-format":
			i++
			if i >= len(args) {
				return runOptions{}, fmt.Errorf("missing value for --log-format")
			}
			switch args[i] {
			case "text", "json":
				opts.LogFormat = args[i]
			default:
				return runOptions{}, fmt.Errorf("invalid --log-format %q", args[i])
			}
		case "--verbose", "-v", "--json", "--dashboard", "--watch", "--db-studio", "--proxy":
			return runOptions{}, fmt.Errorf("%s is a development flag; use `onlava dev`", args[i])
		default:
			return runOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}

func runHeadless(addr string, opts runOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	cfg.EnableDBStudio = false

	result, err := build.App(root, cfg)
	if err != nil {
		return err
	}
	return startHeadlessApp(root, cfg, result.Binary, addr, opts)
}

func startHeadlessApp(root string, cfg app.Config, binary, addr string, opts runOptions) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	cmd := commandTreeContext(ctx, binary)
	cmd.Dir = root
	env, err := appProcessEnv(root, cfg, opts.LogFormat, opts.Env, "ONLAVA_LISTEN_ADDR="+addr, "ONLAVA_ROLE="+headlessRuntimeRole(cfg))
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
		return fmt.Errorf("onlava app exited: %w", err)
	}
	return nil
}

func headlessRuntimeRole(cfg app.Config) string {
	_ = cfg
	return "api"
}

func appProcessEnv(root string, cfg app.Config, logFormat string, envName string, extra ...string) ([]string, error) {
	baseEnv, err := appEnvWithDotEnv(os.Environ(), root)
	if err != nil {
		return nil, err
	}
	overrides := []string{
		"ONLAVA_APP_ID=" + cfg.AppID(),
		"ONLAVA_APP_ROOT=" + root,
		"ONLAVA_LOCAL_PROXY=0",
		"ONLAVA_LOG_FORMAT=" + logFormat,
		"ONLAVA_PARENT_MONITOR=1",
		fmt.Sprintf("ONLAVA_PARENT_MONITOR_PID=%d", os.Getpid()),
	}
	overrides = append(overrides, extra...)
	if envName != "" {
		overrides = append(overrides, "ONLAVA_ENV="+envName, "ONLAVA_RUNTIME_ENV="+envName)
	}
	return envWithOverrides(baseEnv, overrides...), nil
}

func envWithOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, item := range overrides {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			keys[key] = struct{}{}
		}
	}
	env := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replace := keys[key]; replace {
				continue
			}
		}
		env = append(env, item)
	}
	return append(env, overrides...)
}
