package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePSQLArgs(t *testing.T) {
	opts, err := parsePSQLArgs([]string{"--app-root", "/tmp/app", "-c", "select 1"})
	if err != nil {
		t.Fatalf("parsePSQLArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("app root = %q", opts.AppRoot)
	}
	if got := strings.Join(opts.Args, " "); got != "-c select 1" {
		t.Fatalf("args = %q", got)
	}
}

func TestParsePSQLArgsRequiresAppRootValue(t *testing.T) {
	_, err := parsePSQLArgs([]string{"--app-root"})
	if err == nil || err.Error() != "missing value for --app-root" {
		t.Fatalf("parsePSQLArgs() error = %v", err)
	}
}

func TestBuildPSQLInvocationUsesDatabaseURLFromDotEnv(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".env", "DatabaseURL=postgres://localhost/from-file\nOTHER=two\n")
	invocation, err := buildPSQLInvocation(root, []string{"PATH=" + os.Getenv("PATH")}, psqlOptions{Args: []string{"-c", "select 1"}})
	if err != nil {
		t.Fatalf("buildPSQLInvocation returned error: %v", err)
	}
	if filepath.Base(invocation.Program) != "psql" {
		t.Fatalf("program = %q", invocation.Program)
	}
	if invocation.Dir != root {
		t.Fatalf("dir = %q, want %q", invocation.Dir, root)
	}
	if len(invocation.Args) < 3 {
		t.Fatalf("args = %+v", invocation.Args)
	}
	if invocation.Args[0] != "postgres://localhost/from-file" {
		t.Fatalf("dsn arg = %q", invocation.Args[0])
	}
	if got := strings.Join(invocation.Args[1:], " "); got != "-c select 1" {
		t.Fatalf("forwarded args = %q", got)
	}
	if !containsEnv(invocation.Env, "OTHER=two") {
		t.Fatalf("env missing .env value: %+v", invocation.Env)
	}
}

func TestBuildPSQLInvocationPrefersProcessEnv(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".env", "DATABASE_URL=postgres://localhost/from-file\n")
	t.Setenv("DATABASE_URL", "postgres://localhost/from-env")
	invocation, err := buildPSQLInvocation(root, os.Environ(), psqlOptions{})
	if err != nil {
		t.Fatalf("buildPSQLInvocation returned error: %v", err)
	}
	if invocation.Args[0] != "postgres://localhost/from-env" {
		t.Fatalf("dsn arg = %q", invocation.Args[0])
	}
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
