package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	appcfg "github.com/pbrazdil/onlava/internal/app"
)

type psqlOptions struct {
	AppRoot    string
	Args       []string
	UseManaged bool
}

func dbCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: onlava db psql|reset|drop|snapshot [--app-root <path>]")
	}
	switch args[0] {
	case "psql":
		return psqlCommandWithOptions(args[1:], true)
	case "reset":
		return dbResetCommand(args[1:])
	case "drop":
		return dbDropCommand(args[1:])
	case "snapshot":
		return dbSnapshotCommand(args[1:])
	default:
		return fmt.Errorf("unknown db command %q", args[0])
	}
}

func dbDropCommand(args []string) error {
	opts, err := parseDBResetArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), appRoot)
	if err != nil {
		return err
	}
	session, err := currentAgentSessionForAppRoot(context.Background(), appRoot)
	if err != nil {
		return err
	}
	plan, err := managedPostgresPlanForCurrentSession(context.Background(), appRoot, cfg, baseEnv)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("dev.services.postgres is not configured")
	}
	if err := dropManagedPostgresDatabase(context.Background(), plan.AdminURL, plan.DatabaseName); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "dropped onlava managed database %s for session %s\n", plan.DatabaseName, session.SessionID)
	return nil
}

func psqlCommand(args []string) error {
	return psqlCommandWithOptions(args, false)
}

func psqlCommandWithOptions(args []string, useManaged bool) error {
	opts, err := parsePSQLArgs(args)
	if err != nil {
		return err
	}
	opts.UseManaged = useManaged
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), appRoot)
	if err != nil {
		return err
	}
	invocation, err := buildPSQLInvocationForConfig(context.Background(), appRoot, cfg, baseEnv, opts)
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

func dbResetCommand(args []string) error {
	opts, err := parseDBResetArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), appRoot)
	if err != nil {
		return err
	}
	plan, err := managedPostgresPlanForCurrentSession(context.Background(), appRoot, cfg, baseEnv)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("dev.services.postgres is not configured")
	}
	if err := resetManagedPostgresDatabase(context.Background(), plan.AdminURL, plan.DatabaseName); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "reset onlava managed database %s\n", plan.DatabaseName)
	return nil
}

func dbSnapshotCommand(args []string) error {
	opts, err := parseDBSnapshotArgs(args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), appRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	session, err := currentAgentSessionForAppRoot(ctx, appRoot)
	if err != nil {
		return err
	}
	plan, err := managedPostgresPlanForCurrentSession(ctx, appRoot, cfg, baseEnv)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("dev.services.postgres is not configured")
	}
	path, err := managedPostgresSnapshotPath(appRoot, session.SessionID, opts.Name)
	if err != nil {
		return err
	}
	switch opts.Action {
	case "create":
		if err := ensureManagedPostgresDatabase(ctx, plan.AdminURL, plan.DatabaseName); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		program, err := exec.LookPath("pg_dump")
		if err != nil {
			return fmt.Errorf("pg_dump not found in PATH")
		}
		cmd := exec.Command(program, "--file", path, plan.DatabaseURL)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "created onlava database snapshot %s at %s\n", opts.Name, path)
		return nil
	case "restore":
		if _, err := os.Stat(path); err != nil {
			return err
		}
		if err := resetManagedPostgresDatabase(ctx, plan.AdminURL, plan.DatabaseName); err != nil {
			return err
		}
		program, err := exec.LookPath("psql")
		if err != nil {
			return fmt.Errorf("psql not found in PATH")
		}
		cmd := exec.Command(program, plan.DatabaseURL, "-v", "ON_ERROR_STOP=1", "-f", path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "restored onlava database snapshot %s from %s\n", opts.Name, path)
		return nil
	default:
		return fmt.Errorf("unknown db snapshot action %q", opts.Action)
	}
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
	return buildPSQLInvocationForConfig(context.Background(), appRoot, appcfg.Config{}, baseEnv, opts)
}

func buildPSQLInvocationForConfig(ctx context.Context, appRoot string, cfg appcfg.Config, baseEnv []string, opts psqlOptions) (psqlInvocation, error) {
	program, err := exec.LookPath("psql")
	if err != nil {
		return psqlInvocation{}, fmt.Errorf("psql not found in PATH")
	}
	var dsn string
	if opts.UseManaged {
		plan, err := managedPostgresPlanForCurrentSession(ctx, appRoot, cfg, baseEnv)
		if err != nil {
			return psqlInvocation{}, err
		}
		if plan != nil {
			if err := ensureManagedPostgresDatabase(ctx, plan.AdminURL, plan.DatabaseName); err != nil {
				return psqlInvocation{}, err
			}
			dsn = plan.DatabaseURL
		}
	}
	if dsn == "" {
		var err error
		dsn, _, err = discoverDatabaseURL(appRoot)
		if err != nil {
			return psqlInvocation{}, err
		}
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

type dbResetOptions struct {
	AppRoot string
}

type dbSnapshotOptions struct {
	Action  string
	Name    string
	AppRoot string
}

func parseDBResetArgs(args []string) (dbResetOptions, error) {
	var opts dbResetOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return dbResetOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			return dbResetOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func parseDBSnapshotArgs(args []string) (dbSnapshotOptions, error) {
	var opts dbSnapshotOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "create", "restore":
			if opts.Action != "" {
				return dbSnapshotOptions{}, fmt.Errorf("db snapshot action already set")
			}
			opts.Action = args[i]
			i++
			if i >= len(args) {
				return dbSnapshotOptions{}, fmt.Errorf("missing snapshot name")
			}
			opts.Name = args[i]
		case "--app-root":
			i++
			if i >= len(args) {
				return dbSnapshotOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		default:
			return dbSnapshotOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if opts.Action == "" {
		return dbSnapshotOptions{}, fmt.Errorf("missing db snapshot action create|restore")
	}
	if opts.Name == "" {
		return dbSnapshotOptions{}, fmt.Errorf("missing snapshot name")
	}
	return opts, nil
}

func managedPostgresSnapshotPath(appRoot, sessionID, name string) (string, error) {
	label := localagentLabel(name)
	if label == "" {
		return "", fmt.Errorf("snapshot name must contain at least one letter or digit")
	}
	return filepath.Join(appRoot, ".onlava", "sessions", sessionID, "db", "snapshots", label+".sql"), nil
}
