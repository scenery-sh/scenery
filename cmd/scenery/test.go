package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/parse"
)

type testOptions struct {
	AppRoot string
	GoArgs  []string
}

var execGoTestCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func testCommand(args []string) error {
	return runSceneryTest(context.Background(), args)
}

func runSceneryTest(ctx context.Context, args []string) error {
	return runSceneryTestOutput(ctx, args, os.Stdout)
}

func runSceneryTestOutput(ctx context.Context, args []string, stdout io.Writer) error {
	opts, err := parseTestArgs(args)
	if err != nil {
		return err
	}

	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}

	result, err := prepareTestWorkspace(ctx, appRoot, cfg)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	testDir, err := resolveTestWorkingDir(cwd, appRoot, result.Dir)
	if err != nil {
		return err
	}

	goArgs := append([]string{"test"}, result.GoBuildFlags...)
	goArgs = append(goArgs, opts.GoArgs...)
	err, output := runGeneratedWorkspaceGoTest(ctx, testDir, goArgs, false)
	if err == nil {
		_, _ = stdout.Write(output)
		return nil
	}
	_, _ = stdout.Write(output)
	return err
}

func prepareTestWorkspace(ctx context.Context, appRoot string, cfg app.Config) (*build.Result, error) {
	var result *build.Result
	graphFingerprint := ""
	if snapshot, err := scanWatchedFiles(appRoot); err == nil {
		graphFingerprint = snapshotFingerprint(snapshot)
		if cached, ok, err := build.LoadCachedGraph(appRoot, cfg, graphFingerprint); err != nil {
			return nil, err
		} else if ok {
			reused, err := build.RefreshCachedWorkspace(appRoot, cached.Result)
			if err != nil {
				return nil, err
			}
			if reused {
				result = cached.Result
			}
		}
	}
	if result == nil {
		model, err := parse.App(appRoot, cfg.Name)
		if err != nil {
			return nil, err
		}
		prepared, err := build.Prepare(appRoot, model, cfg)
		if err != nil {
			return nil, err
		}
		result = prepared
	}
	if graphFingerprint != "" {
		result.GraphFingerprint = graphFingerprint
	}
	if err := build.PrimeWorkspaceContext(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func runGeneratedWorkspaceGoTest(ctx context.Context, dir string, goArgs []string, stream bool) (error, []byte) {
	cmd := execGoTestCommand(ctx, "go", goArgs...)
	cmd.Dir = dir
	cmd.Env = append(envpolicy.Environ(), "SCENERY_RUNTIME_ENV=test")
	if stream {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run(), nil
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	return cmd.Run(), output.Bytes()
}

func parseTestArgs(args []string) (testOptions, error) {
	var opts testOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return testOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			opts.GoArgs = append(opts.GoArgs, args[i])
		}
	}
	return opts, nil
}

func resolveTestWorkingDir(cwd, appRoot, workspaceRoot string) (string, error) {
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	absAppRoot, err := filepath.Abs(appRoot)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absAppRoot, absCWD)
	if err != nil {
		return workspaceRoot, nil
	}
	if rel == "." {
		return workspaceRoot, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return workspaceRoot, nil
	}
	return filepath.Join(workspaceRoot, rel), nil
}
