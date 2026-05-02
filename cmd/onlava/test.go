package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"onlava.com/internal/app"
	"onlava.com/internal/build"
	"onlava.com/internal/parse"
)

type testOptions struct {
	AppRoot string
	GoArgs  []string
}

var execGoTestCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func testCommand(args []string) error {
	return runOnlavaTest(context.Background(), args)
}

func runOnlavaTest(ctx context.Context, args []string) error {
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

	model, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return err
	}
	result, err := build.Prepare(appRoot, model, cfg, build.PrepareOptions{})
	if err != nil {
		return err
	}
	if err := build.PrimeWorkspaceContext(ctx, result); err != nil {
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

	goArgs := append([]string{"test"}, opts.GoArgs...)
	cmd := execGoTestCommand(ctx, "go", goArgs...)
	cmd.Dir = testDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "ONLAVA_RUNTIME_ENV=test")
	return cmd.Run()
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
