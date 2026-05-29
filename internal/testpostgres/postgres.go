package testpostgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const EnvDatabaseURL = "ONLAVA_TEST_DATABASE_URL"

type Database struct {
	URL      string
	reusable bool
}

func Start(ctx context.Context) (*Database, error) {
	if dsn := strings.TrimSpace(os.Getenv(EnvDatabaseURL)); dsn != "" {
		url, err := ensurePackageDatabase(ctx, dsn)
		if err != nil {
			return nil, err
		}
		return &Database{URL: url}, nil
	}
	if adminURL, ok := readCachedReusablePostgresURL(); ok {
		url, err := ensurePackageDatabase(ctx, adminURL)
		if err == nil {
			return &Database{URL: url, reusable: true}, nil
		}
		_ = os.Remove(reusablePostgresURLCachePath())
	}
	unlock, err := lockReusablePostgres(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if adminURL, ok := readCachedReusablePostgresURL(); ok {
		url, err := ensurePackageDatabase(ctx, adminURL)
		if err == nil {
			return &Database{URL: url, reusable: true}, nil
		}
		_ = os.Remove(reusablePostgresURLCachePath())
	}
	url, err := startDockerPostgres(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeCachedReusablePostgresURL(url); err != nil {
		return nil, err
	}
	url, err = ensurePackageDatabase(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Database{URL: url, reusable: true}, nil
}

func (db *Database) Terminate(ctx context.Context) error {
	return nil
}

func startDockerPostgres(ctx context.Context) (string, error) {
	name := reusablePostgresContainerName()
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("%s is not set and docker CLI was not found in PATH: %w", EnvDatabaseURL, err)
	}
	exists, running, err := inspectDockerContainer(ctx, name)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := runDockerPostgres(ctx, name); err != nil {
			return "", err
		}
	} else if !running {
		if err := removeDockerContainer(ctx, name); err != nil {
			return "", err
		}
		if err := runDockerPostgres(ctx, name); err != nil {
			return "", err
		}
	}
	host, port, err := dockerPostgresHostPort(ctx, name)
	if err != nil {
		return "", err
	}
	adminURL := (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword("postgres", "postgres"),
		Host:   net.JoinHostPort(host, port),
		Path:   "/onlava_test",
	}).String() + "?sslmode=disable"
	if err := waitForPostgres(ctx, adminURL); err != nil {
		return "", err
	}
	return adminURL, nil
}

func inspectDockerContainer(ctx context.Context, name string) (exists bool, running bool, err error) {
	output, err := runCommand(ctx, "docker", "inspect", "-f", "{{.State.Running}}", name)
	if err == nil {
		return true, strings.TrimSpace(string(output)) == "true", nil
	}
	if strings.Contains(string(output), "No such object") || strings.Contains(err.Error(), "exit status 1") {
		return false, false, nil
	}
	return false, false, fmt.Errorf("inspect PostgreSQL docker container %s: %w\n%s", name, err, output)
}

func runDockerPostgres(ctx context.Context, name string) error {
	output, err := runCommand(ctx,
		"docker", "run", "-d",
		"--name", name,
		"-e", "POSTGRES_DB=onlava_test",
		"-e", "POSTGRES_USER=postgres",
		"-e", "POSTGRES_PASSWORD=postgres",
		"-p", "127.0.0.1::5432",
		"postgres:17-alpine",
	)
	if err != nil {
		return fmt.Errorf("start PostgreSQL docker container %s: %w\n%s", name, err, output)
	}
	return nil
}

func removeDockerContainer(ctx context.Context, name string) error {
	output, err := runCommand(ctx, "docker", "rm", "-f", name)
	if err != nil {
		return fmt.Errorf("remove stale PostgreSQL docker container %s: %w\n%s", name, err, output)
	}
	return nil
}

func dockerPostgresHostPort(ctx context.Context, name string) (string, string, error) {
	output, err := runCommand(ctx, "docker", "port", name, "5432/tcp")
	if err != nil {
		return "", "", fmt.Errorf("read PostgreSQL docker container port %s: %w\n%s", name, err, output)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) == 0 {
		return "", "", fmt.Errorf("PostgreSQL docker container %s has no published 5432/tcp port", name)
	}
	host, port, err := net.SplitHostPort(fields[0])
	if err != nil {
		return "", "", fmt.Errorf("parse PostgreSQL docker container port %q: %w", fields[0], err)
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	return host, port, nil
}

func waitForPostgres(ctx context.Context, dsn string) error {
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, time.Second)
		pool, err := pgxpool.New(pingCtx, dsn)
		if err == nil {
			err = pool.Ping(pingCtx)
			pool.Close()
		}
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("PostgreSQL docker container did not become ready: %w", lastErr)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.Bytes(), err
}

func reusablePostgresContainerName() string {
	sum := sha256.Sum256([]byte(repoRootForContainerName()))
	return "onlava-test-postgres-" + hex.EncodeToString(sum[:6])
}

func reusablePostgresCacheDir() (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheRoot, "onlava", "test-postgres"), nil
}

func reusablePostgresURLCachePath() string {
	dir, err := reusablePostgresCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, reusablePostgresContainerName()+".url")
}

func readCachedReusablePostgresURL() (string, bool) {
	path := reusablePostgresURLCachePath()
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	adminURL := strings.TrimSpace(string(data))
	if adminURL == "" {
		return "", false
	}
	return adminURL, true
}

func writeCachedReusablePostgresURL(adminURL string) error {
	dir, err := reusablePostgresCacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, reusablePostgresContainerName()+".url")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(adminURL+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func repoRootForContainerName() string {
	wd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	for {
		data, err := os.ReadFile(filepath.Join(wd, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/pbrazdil/onlava") {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return wd
		}
		wd = parent
	}
}

func lockReusablePostgres(ctx context.Context) (func(), error) {
	cacheRoot, err := reusablePostgresCacheDir()
	if err != nil {
		return nil, err
	}
	lockDir := filepath.Join(cacheRoot, reusablePostgresContainerName()+".lock")
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			return func() { _ = os.Remove(lockDir) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(lockDir); statErr == nil && time.Since(info.ModTime()) > 2*time.Minute {
			_ = os.Remove(lockDir)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for reusable PostgreSQL test lock %s", lockDir)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func ensurePackageDatabase(ctx context.Context, adminURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	dbName := packageDatabaseName()
	pool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return "", fmt.Errorf("connect PostgreSQL admin database: %w", err)
	}
	defer pool.Close()
	var exists bool
	if err := pool.QueryRow(ctx, `select exists (select 1 from pg_database where datname = $1)`, dbName).Scan(&exists); err != nil {
		return "", fmt.Errorf("inspect PostgreSQL package database: %w", err)
	}
	if !exists {
		if _, err := pool.Exec(ctx, `create database `+quoteIdent(dbName)); err != nil {
			if inspectErr := pool.QueryRow(ctx, `select exists (select 1 from pg_database where datname = $1)`, dbName).Scan(&exists); inspectErr != nil {
				return "", fmt.Errorf("create PostgreSQL package database %s: %w", dbName, err)
			}
			if !exists {
				return "", fmt.Errorf("create PostgreSQL package database %s: %w", dbName, err)
			}
		}
	}
	parsed, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + dbName
	query := parsed.Query()
	if query.Get("pool_max_conns") == "" {
		query.Set("pool_max_conns", "4")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func packageDatabaseName() string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "unknown"
	}
	root := repoRootForContainerName()
	rel, err := filepath.Rel(root, wd)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = wd
	}
	sum := sha256.Sum256([]byte(rel))
	return "onlava_test_" + hex.EncodeToString(sum[:6])
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
