package main

import (
	"fmt"
	"os"
	"os/exec"

	appcfg "onlava.com/internal/app"
)

type psqlOptions struct {
	AppRoot string
	Args    []string
}

func psqlCommand(args []string) error {
	opts, err := parsePSQLArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, _, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	invocation, err := buildPSQLInvocation(appRoot, os.Environ(), opts)
	if err != nil {
		return err
	}
	cmd := exec.Command(invocation.Program, invocation.Args...)
	cmd.Dir = invocation.Dir
	cmd.Env = invocation.Env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type psqlInvocation struct {
	Program string
	Args    []string
	Dir     string
	Env     []string
}

func parsePSQLArgs(args []string) (psqlOptions, error) {
	var opts psqlOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return psqlOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			opts.Args = append(opts.Args, args[i:]...)
			return opts, nil
		}
	}
	return opts, nil
}

func buildPSQLInvocation(appRoot string, baseEnv []string, opts psqlOptions) (psqlInvocation, error) {
	program, err := exec.LookPath("psql")
	if err != nil {
		return psqlInvocation{}, fmt.Errorf("psql not found in PATH")
	}
	dsn, _, err := discoverDatabaseURL(appRoot)
	if err != nil {
		return psqlInvocation{}, err
	}
	env, err := appEnvWithDotEnv(baseEnv, appRoot)
	if err != nil {
		return psqlInvocation{}, err
	}
	return psqlInvocation{
		Program: program,
		Args:    append([]string{dsn}, opts.Args...),
		Dir:     appRoot,
		Env:     env,
	}, nil
}
