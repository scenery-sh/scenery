package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
	"github.com/pbrazdil/onlava/internal/workers"
)

type workerOptions struct {
	AppRoot    string
	Env        string
	LogFormat  string
	TaskQueues []string
}

type workerBindingsOptions struct {
	AppRoot string
	OutDir  string
	JSON    bool
}

func workerCommand(args []string) error {
	if len(args) > 0 && args[0] == "bindings" {
		opts, err := parseWorkerBindingsArgs(args[1:])
		if err != nil {
			return err
		}
		return runWorkerBindingsFunc(opts, os.Stdout)
	}
	opts, err := parseWorkerArgs(args)
	if err != nil {
		return err
	}
	return runWorkerFunc(opts)
}

var runWorkerFunc = runWorker
var runWorkerBindingsFunc = runWorkerBindings

func parseWorkerArgs(args []string) (workerOptions, error) {
	opts := workerOptions{LogFormat: "text"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return workerOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--env":
			i++
			if i >= len(args) {
				return workerOptions{}, fmt.Errorf("missing value for --env")
			}
			opts.Env = strings.TrimSpace(args[i])
			if opts.Env == "" {
				return workerOptions{}, fmt.Errorf("--env must not be empty")
			}
		case "--log-format":
			i++
			if i >= len(args) {
				return workerOptions{}, fmt.Errorf("missing value for --log-format")
			}
			switch args[i] {
			case "text", "json":
				opts.LogFormat = args[i]
			default:
				return workerOptions{}, fmt.Errorf("invalid --log-format %q", args[i])
			}
		case "--task-queue":
			i++
			if i >= len(args) {
				return workerOptions{}, fmt.Errorf("missing value for --task-queue")
			}
			queues := splitWorkerTaskQueues(args[i])
			if len(queues) == 0 {
				return workerOptions{}, fmt.Errorf("--task-queue must not be empty")
			}
			opts.TaskQueues = append(opts.TaskQueues, queues...)
		case "--port", "-p", "--listen", "--verbose", "-v", "--json", "--dashboard", "--watch", "--db-studio", "--proxy":
			return workerOptions{}, fmt.Errorf("%s is not supported by `onlava worker`", args[i])
		default:
			return workerOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parseWorkerBindingsArgs(args []string) (workerBindingsOptions, error) {
	var opts workerBindingsOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return workerBindingsOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--out":
			i++
			if i >= len(args) {
				return workerBindingsOptions{}, fmt.Errorf("missing value for --out")
			}
			opts.OutDir = strings.TrimSpace(args[i])
			if opts.OutDir == "" {
				return workerBindingsOptions{}, fmt.Errorf("--out must not be empty")
			}
		case "--json":
			opts.JSON = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return workerBindingsOptions{}, fmt.Errorf("unknown flag %q", args[i])
			}
			return workerBindingsOptions{}, fmt.Errorf("unexpected argument %q", args[i])
		}
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
	cfg.EnableDBStudio = false

	result, err := build.App(root, cfg)
	if err != nil {
		return err
	}
	return startWorkerApp(root, cfg, result.Binary, opts)
}

func runWorkerBindings(opts workerBindingsOptions, stdout io.Writer) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}
	result, err := workers.GenerateBindingsWithKnownActivities(root, cfg.Name, opts.OutDir, knownTemporalActivityNamesFromRoot(root, cfg.Name))
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if encodeErr := enc.Encode(result); encodeErr != nil {
			return encodeErr
		}
	}
	if err != nil {
		return err
	}
	if !opts.JSON {
		for _, file := range result.Files {
			_, _ = fmt.Fprintf(stdout, "wrote %s\n", file.Path)
		}
	}
	return nil
}

func startWorkerApp(root string, cfg app.Config, binary string, opts workerOptions) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	cmd := commandTreeContext(ctx, binary)
	cmd.Dir = root
	extra := []string{"ONLAVA_ROLE=worker"}
	if len(opts.TaskQueues) > 0 {
		extra = append(extra, "ONLAVA_TEMPORAL_TASK_QUEUE="+strings.Join(uniqueWorkerTaskQueues(opts.TaskQueues), ","))
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
		return fmt.Errorf("onlava worker exited: %w", err)
	}
	return nil
}

func splitWorkerTaskQueues(value string) []string {
	parts := strings.Split(value, ",")
	queues := make([]string, 0, len(parts))
	for _, part := range parts {
		queue := strings.TrimSpace(part)
		if queue == "" {
			continue
		}
		queues = append(queues, queue)
	}
	return queues
}

func uniqueWorkerTaskQueues(queues []string) []string {
	seen := make(map[string]struct{}, len(queues))
	out := make([]string, 0, len(queues))
	for _, queue := range queues {
		queue = strings.TrimSpace(queue)
		if queue == "" {
			continue
		}
		if _, ok := seen[queue]; ok {
			continue
		}
		seen[queue] = struct{}{}
		out = append(out, queue)
	}
	return out
}
