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
	"scenery.sh/internal/postgresdb"
)

type snapshotPostgresRunner interface {
	Dump(context.Context, postgresdb.Database, io.Writer) error
	Restore(context.Context, postgresdb.Database, []string, io.Reader) error
}

type dockerSnapshotPostgresRunner struct {
	state postgresServerState
}

type hostSnapshotPostgresRunner struct{}

var snapshotPostgresRunnerFor = newSnapshotPostgresRunner

func newSnapshotPostgresRunner(database postgresdb.Database) (snapshotPostgresRunner, error) {
	if database.Source == postgresdb.SourceExternal {
		return hostSnapshotPostgresRunner{}, nil
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return nil, err
	}
	state, err := loadPostgresServerState(postgresServerStatePath(paths))
	if err != nil {
		return nil, fmt.Errorf("load managed postgres state: %w", err)
	}
	return dockerSnapshotPostgresRunner{state: *state}, nil
}

func (r dockerSnapshotPostgresRunner) Dump(ctx context.Context, database postgresdb.Database, out io.Writer) error {
	url, err := snapshotContainerDatabaseURL(r.state, database.Database)
	if err != nil {
		return err
	}
	return runSnapshotPostgresTool(ctx, "docker", []string{"exec", "-i", r.state.Container, "pg_dump", "-Fc", "--no-owner", "--no-privileges", "--dbname", url}, nil, out, "pg_dump")
}

func (r dockerSnapshotPostgresRunner) Restore(ctx context.Context, database postgresdb.Database, flags []string, in io.Reader) error {
	url, err := snapshotContainerDatabaseURL(r.state, database.Database)
	if err != nil {
		return err
	}
	args := []string{"exec", "-i", r.state.Container, "pg_restore", "--no-owner", "--no-privileges"}
	args = append(args, flags...)
	args = append(args, "--dbname", url)
	return runSnapshotPostgresTool(ctx, "docker", args, in, io.Discard, "pg_restore")
}

func (hostSnapshotPostgresRunner) Dump(ctx context.Context, database postgresdb.Database, out io.Writer) error {
	return runSnapshotPostgresTool(ctx, "pg_dump", []string{"-Fc", "--no-owner", "--no-privileges", "--dbname", database.URL}, nil, out, "pg_dump")
}

func (hostSnapshotPostgresRunner) Restore(ctx context.Context, database postgresdb.Database, flags []string, in io.Reader) error {
	args := []string{"--no-owner", "--no-privileges"}
	args = append(args, flags...)
	args = append(args, "--dbname", database.URL)
	return runSnapshotPostgresTool(ctx, "pg_restore", args, in, io.Discard, "pg_restore")
}

func snapshotContainerDatabaseURL(state postgresServerState, database string) (string, error) {
	parsed, err := url.Parse(state.databaseURL(database))
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
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("%s failed: %w: %s", label, err, detail)
		}
		return fmt.Errorf("%s failed: %w", label, err)
	}
	return nil
}
