package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDashboardUIBuildStaleWhenDistMissing(t *testing.T) {
	uiRoot := t.TempDir()
	writeDashboardUITestFile(t, uiRoot, "package.json", "{}")
	writeDashboardUITestFile(t, uiRoot, "src/main.tsx", "export {}")

	stale, err := dashboardUIBuildStale(uiRoot)
	if err != nil {
		t.Fatalf("dashboardUIBuildStale returned error: %v", err)
	}
	if !stale {
		t.Fatal("expected UI build to be stale when dist/index.html is missing")
	}
}

func TestDashboardUIBuildStaleWhenSourceIsNewer(t *testing.T) {
	uiRoot := t.TempDir()
	writeDashboardUITestFile(t, uiRoot, "package.json", "{}")
	distPath := writeDashboardUITestFile(t, uiRoot, "dist/index.html", "<!doctype html>")
	srcPath := writeDashboardUITestFile(t, uiRoot, "src/main.tsx", "export {}")

	now := time.Now()
	if err := os.Chtimes(distPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("set dist times: %v", err)
	}
	if err := os.Chtimes(srcPath, now, now); err != nil {
		t.Fatalf("set src times: %v", err)
	}

	stale, err := dashboardUIBuildStale(uiRoot)
	if err != nil {
		t.Fatalf("dashboardUIBuildStale returned error: %v", err)
	}
	if !stale {
		t.Fatal("expected UI build to be stale when source is newer than dist")
	}
}

func TestDashboardUIBuildNotStaleWhenDistIsNewer(t *testing.T) {
	uiRoot := t.TempDir()
	writeDashboardUITestFile(t, uiRoot, "package.json", "{}")
	distPath := writeDashboardUITestFile(t, uiRoot, "dist/index.html", "<!doctype html>")
	srcPath := writeDashboardUITestFile(t, uiRoot, "src/main.tsx", "export {}")

	now := time.Now()
	if err := os.Chtimes(srcPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("set src times: %v", err)
	}
	if err := os.Chtimes(distPath, now, now); err != nil {
		t.Fatalf("set dist times: %v", err)
	}

	stale, err := dashboardUIBuildStale(uiRoot)
	if err != nil {
		t.Fatalf("dashboardUIBuildStale returned error: %v", err)
	}
	if stale {
		t.Fatal("expected UI build to be fresh when dist is newer than source")
	}
}

func TestDashboardUIDepsStaleWhenPackageManifestIsNewer(t *testing.T) {
	uiRoot := t.TempDir()
	nodeModulesPath := filepath.Join(uiRoot, "node_modules")
	if err := os.MkdirAll(nodeModulesPath, 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	pkgPath := writeDashboardUITestFile(t, uiRoot, "package.json", "{}")

	now := time.Now()
	if err := os.Chtimes(nodeModulesPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("set node_modules times: %v", err)
	}
	if err := os.Chtimes(pkgPath, now, now); err != nil {
		t.Fatalf("set package times: %v", err)
	}

	stale, err := dashboardUIDepsStale(uiRoot)
	if err != nil {
		t.Fatalf("dashboardUIDepsStale returned error: %v", err)
	}
	if !stale {
		t.Fatal("expected UI deps to be stale when package.json is newer than node_modules")
	}
}

func writeDashboardUITestFile(t *testing.T, root, rel, data string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
