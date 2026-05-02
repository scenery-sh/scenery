package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseTestArgs(t *testing.T) {
	opts, err := parseTestArgs([]string{"--app-root", "/tmp/app", "-run", "TestFoo", "./svc"})
	if err != nil {
		t.Fatalf("parseTestArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("AppRoot = %q, want %q", opts.AppRoot, "/tmp/app")
	}
	got := strings.Join(opts.GoArgs, "\x00")
	want := strings.Join([]string{"-run", "TestFoo", "./svc"}, "\x00")
	if got != want {
		t.Fatalf("GoArgs = %q, want %q", opts.GoArgs, []string{"-run", "TestFoo", "./svc"})
	}
}

func TestResolveTestWorkingDir(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	appRoot := filepath.Join(t.TempDir(), "app")

	got, err := resolveTestWorkingDir(filepath.Join(appRoot, "svc"), appRoot, workspace)
	if err != nil {
		t.Fatalf("resolveTestWorkingDir returned error: %v", err)
	}
	want := filepath.Join(workspace, "svc")
	if got != want {
		t.Fatalf("resolveTestWorkingDir inside app = %q, want %q", got, want)
	}

	got, err = resolveTestWorkingDir(t.TempDir(), appRoot, workspace)
	if err != nil {
		t.Fatalf("resolveTestWorkingDir returned error: %v", err)
	}
	if got != workspace {
		t.Fatalf("resolveTestWorkingDir outside app = %q, want %q", got, workspace)
	}
}

func TestOnlavaTestRunsGoTestInGeneratedWorkspace(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"testapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/testapp\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return nil }\n")
	writeTestAppFile(t, root, "svc/api_test.go", "package svc\n\nimport (\n\t\"testing\"\n\n\tonlava \"onlava.com\"\n)\n\nfunc TestOnlavaMetaUsesTestEnv(t *testing.T) {\n\tif onlava.Meta().Environment.Type != onlava.EnvTest {\n\t\tt.Fatalf(\"env type = %q, want %q\", onlava.Meta().Environment.Type, onlava.EnvTest)\n\t}\n}\n")

	restore := chdirForTest(t, filepath.Join(root, "svc"))
	defer restore()

	if err := runOnlavaTest(context.Background(), []string{"-run", "TestOnlavaMetaUsesTestEnv"}); err != nil {
		t.Fatalf("runOnlavaTest returned error: %v", err)
	}
}

func writeTestAppFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func chdirForTest(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	return func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore Chdir(%q): %v", prev, err)
		}
	}
}

func TestOnlavaTestPassesThroughGoTestFlags(t *testing.T) {
	if testing.Short() && runtime.GOOS == "windows" {
		t.Skip("slow process test on windows")
	}
	root := t.TempDir()
	writeTestAppFile(t, root, ".onlava.json", `{"name":"flagapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/flagapp\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return nil }\n")
	writeTestAppFile(t, root, "svc/api_test.go", "package svc\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) { t.Fatal(\"should not run\") }\n")

	restore := chdirForTest(t, root)
	defer restore()

	if err := runOnlavaTest(context.Background(), []string{"./svc", "-run", "TestOne"}); err != nil {
		t.Fatalf("runOnlavaTest returned error: %v", err)
	}
}
