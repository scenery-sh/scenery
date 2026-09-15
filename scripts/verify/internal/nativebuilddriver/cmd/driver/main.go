package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/nativebuilddriver"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("expected recipe, build, or serve")
	}
	switch os.Args[1] {
	case "recipe":
		flags := flag.NewFlagSet("recipe", flag.ContinueOnError)
		recordRoot := flags.String("record-root", "", "record root")
		workspace := flags.String("workspace", "", "workspace")
		goTool := flags.String("go", "", "Go command")
		output := flags.String("output", "", "recipe path")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		capture, err := nativebuilddriver.FullCapture(ctx, *goTool, *workspace, filepath.Join(*recordRoot, "bootstrap-snapshot"), envpolicy.Environ(), nil)
		if err != nil {
			return err
		}
		recipe, err := nativebuilddriver.LoadRecordedRecipe(*recordRoot, *workspace, capture)
		if err != nil {
			return err
		}
		return writeJSON(*output, recipe)
	case "build":
		flags := flag.NewFlagSet("build", flag.ContinueOnError)
		recipePath := flags.String("recipe", "", "recipe path")
		workspace := flags.String("workspace", "", "workspace")
		output := flags.String("output", "", "output executable")
		generation := flags.String("generation", "", "owned generation root")
		argsPath := flags.String("build-args", "", "JSON build argv")
		resultPath := flags.String("result", "", "result path")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		var recipe nativebuilddriver.Recipe
		if err := readJSON(*recipePath, &recipe); err != nil {
			return err
		}
		var args []string
		if err := readJSON(*argsPath, &args); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		absoluteOutput, err := filepath.Abs(*output)
		if err != nil {
			return err
		}
		absoluteGeneration, err := filepath.Abs(*generation)
		if err != nil {
			return err
		}
		result, err := recipe.Build(ctx, nativebuilddriver.BuildRequest{Workspace: *workspace, Output: absoluteOutput,
			GenerationRoot: absoluteGeneration, BuildArgv: args, Environment: envpolicy.Environ()})
		writeErr := writeJSON(*resultPath, result)
		if err != nil {
			return fmt.Errorf("build: %w (result write: %v)", err, writeErr)
		}
		if writeErr != nil {
			return writeErr
		}
		if result.Status != "supported_and_rebuilt" {
			return fmt.Errorf("%s: %s", result.Status, result.Reason)
		}
		return nil
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		recipePath := flags.String("recipe", "", "recipe path")
		socket := flags.String("socket", "", "owned Unix socket")
		session := flags.String("session", "", "benchmark session identity")
		workspace := flags.String("workspace", "", "owned workload workspace")
		ready := flags.String("ready", "", "ready record path")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		started := time.Now()
		var recipe nativebuilddriver.Recipe
		if err := readJSON(*recipePath, &recipe); err != nil {
			return err
		}
		owner := &nativebuilddriver.Owner{Recipe: &recipe, Session: *session, Workspace: *workspace, StateRoot: filepath.Join(filepath.Dir(*recipePath), "retained")}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			for {
				if _, err := os.Stat(*socket); err == nil {
					_ = writeJSON(*ready, map[string]any{"protocol": nativebuilddriver.ProtocolVersion, "pid": os.Getpid(), "session": *session, "workspace": *workspace, "recipe_load_ms": float64(time.Since(started).Nanoseconds()) / 1e6})
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		return owner.Serve(ctx, *socket)
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
