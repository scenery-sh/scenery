package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveAppRoot(t *testing.T) {
	t.Parallel()

	if got, err := resolveAppRoot(""); err != nil || got != "." {
		t.Fatalf("resolveAppRoot(\"\") = %q, %v; want \".\", nil", got, err)
	}

	root := t.TempDir()
	got, err := resolveAppRoot(root)
	if err != nil {
		t.Fatalf("resolveAppRoot returned error: %v", err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("resolveAppRoot(%q) = %q, want %q", root, got, filepath.Clean(root))
	}
}

func TestDevLegacyProxySurfaceRejected(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--proxy", "--trust"} {
		if _, err := parseDevArgs([]string{flag}); err == nil || !strings.Contains(err.Error(), `unknown flag "`+flag+`"`) {
			t.Fatalf("parseDevArgs(%s) error = %v, want unknown flag", flag, err)
		}
	}
}

func TestLegacyLocalProxyEnvRemovedFromProductionSource(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	banned := "SCENERY_LOCAL_" + "PROXY"
	var hits []string
	for _, dir := range []string{"cmd/scenery", "internal"} {
		root := filepath.Join(repoRoot, dir)
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), banned) {
				rel, _ := filepath.Rel(repoRoot, path)
				hits = append(hits, filepath.ToSlash(rel))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("%s remains in production source: %s", banned, strings.Join(hits, ", "))
	}
}
