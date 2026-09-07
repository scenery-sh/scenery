package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

type snapshotPostgresRunner interface {
	Dump(context.Context, postgresdb.Database, io.Writer) error
	Restore(context.Context, postgresdb.Database, []string, io.Reader) error
}

type dockerSnapshotPostgresRunner struct{}

type hostSnapshotPostgresRunner struct{}

var snapshotPostgresRunnerFor = newSnapshotPostgresRunner

func newSnapshotPostgresRunner(database postgresdb.Database) (snapshotPostgresRunner, error) {
	if database.Source == postgresdb.SourceExternal {
		return hostSnapshotPostgresRunner{}, nil
	}
	if database.Source != postgresdb.SourceManaged || database.AppRoot == "" || database.ResourceID == "" {
		return nil, worktreePostgresPrecondition("snapshot database has no managed worktree authority")
	}
	return dockerSnapshotPostgresRunner{}, nil
}

func (r dockerSnapshotPostgresRunner) Dump(ctx context.Context, database postgresdb.Database, out io.Writer) error {
	args, err := managedSnapshotToolArgs(ctx, database, "pg_dump")
	if err != nil {
		return err
	}
	return runSnapshotPostgresTool(ctx, "docker", append(args, "-Fc"), nil, out, "pg_dump")
}

func (r dockerSnapshotPostgresRunner) Restore(ctx context.Context, database postgresdb.Database, flags []string, in io.Reader) error {
	args, err := managedSnapshotToolArgs(ctx, database, "pg_restore")
	if err != nil {
		return err
	}
	args = append(args, flags...)
	return runSnapshotPostgresTool(ctx, "docker", args, in, io.Discard, "pg_restore")
}

func managedSnapshotToolArgs(ctx context.Context, database postgresdb.Database, tool string) ([]string, error) {
	resolver, err := newWorktreePostgresResolver(ctx, database.AppRoot, "")
	if err != nil {
		return nil, err
	}
	record, err := resolver.load()
	if err != nil {
		return nil, err
	}
	p := record.Postgres
	if database.Source != postgresdb.SourceManaged || p.InstanceID != database.ResourceID || database.Database != postgresname.DatabaseNameFor(record.AppID, record.AppRoot) {
		return nil, worktreePostgresPrecondition("snapshot target does not match retained worktree ownership")
	}
	_, container, err := resolver.inspect(ctx, record)
	if err != nil {
		return nil, err
	}
	if container == nil || !container.Running {
		return nil, worktreePostgresPrecondition("snapshot target container is not running")
	}
	observed := *p
	observed.Port = container.Port
	systemID, err := resolver.probe(ctx, &observed)
	if err != nil {
		return nil, err
	}
	if p.SystemID == "" || systemID != p.SystemID || p.Phase == "deleting" || (tool == "pg_dump" && (p.Restore != nil || p.Phase == "restoring" || p.Phase == "restore-failed")) {
		return nil, worktreePostgresPrecondition("snapshot target identity or operation state is incompatible")
	}
	databaseURL, err := snapshotContainerDatabaseURL(observed, database.Database)
	if err != nil {
		return nil, err
	}
	return []string{"--host", p.DaemonEndpoint, "exec", "-i", container.ID, tool, "--dbname", databaseURL}, nil
}

func (hostSnapshotPostgresRunner) Dump(ctx context.Context, database postgresdb.Database, out io.Writer) error {
	return runSnapshotPostgresTool(ctx, "pg_dump", []string{"-Fc", "--dbname", database.URL}, nil, out, "pg_dump")
}

func (hostSnapshotPostgresRunner) Restore(ctx context.Context, database postgresdb.Database, flags []string, in io.Reader) error {
	args := append([]string(nil), flags...)
	args = append(args, "--dbname", database.URL)
	return runSnapshotPostgresTool(ctx, "pg_restore", args, in, io.Discard, "pg_restore")
}

func snapshotContainerDatabaseURL(state localagent.WorktreePostgres, database string) (string, error) {
	parsed, err := url.Parse(worktreePostgresURL(&state, database))
	if err != nil {
		return "", err
	}
	parsed.Host = net.JoinHostPort("127.0.0.1", "5432")
	return parsed.String(), nil
}

func runSnapshotPostgresTool(ctx context.Context, program string, args []string, stdin io.Reader, stdout io.Writer, label string) error {
	path, err := exec.LookPath(program)
	if err != nil {
		if program == "docker" {
			return fmt.Errorf("docker not found in PATH")
		}
		return fmt.Errorf("%s not found in PATH; install a client matching the target Postgres server", label)
	}
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, path, args...)
	if program == "docker" {
		command.Env = envWithoutKeys(envpolicy.Environ(), "DOCKER_HOST", "DOCKER_CONTEXT")
	}
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			// Client errors can repeat a connection URI, including its password.
			for _, arg := range args {
				if parsed, parseErr := url.Parse(arg); parseErr == nil && parsed.User != nil {
					detail = strings.ReplaceAll(detail, arg, postgresdb.RedactURL(arg))
					if password, ok := parsed.User.Password(); ok && password != "" {
						detail = strings.ReplaceAll(detail, password, "[REDACTED]")
					}
				}
			}
			return fmt.Errorf("%s failed: %w: %s", label, err, detail)
		}
		return fmt.Errorf("%s failed: %w", label, err)
	}
	return nil
}
