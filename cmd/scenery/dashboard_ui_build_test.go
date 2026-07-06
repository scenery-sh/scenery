package main

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func TestDashboardUIBuildStaleWhenDistMissing(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

func TestDashboardBundleHashUsesAssetNames(t *testing.T) {
	t.Parallel()

	first, err := dashboardBundleHash(fstest.MapFS{
		"index.html":              {Data: []byte("first")},
		"assets/index-a1b2c3.js":  {Data: []byte("console.log(1)")},
		"assets/index-d4e5f6.css": {Data: []byte("body{}")},
		"placeholder.txt":         {Data: []byte("ignored")},
	})
	if err != nil {
		t.Fatalf("dashboardBundleHash first: %v", err)
	}
	sameNames, err := dashboardBundleHash(fstest.MapFS{
		"assets/index-d4e5f6.css": {Data: []byte("different")},
		"assets/index-a1b2c3.js":  {Data: []byte("different")},
		"index.html":              {Data: []byte("different")},
	})
	if err != nil {
		t.Fatalf("dashboardBundleHash same names: %v", err)
	}
	if first != sameNames {
		t.Fatalf("hash changed for the same asset names: %s != %s", first, sameNames)
	}

	differentName, err := dashboardBundleHash(fstest.MapFS{
		"index.html":              {Data: []byte("first")},
		"assets/index-a1b2c3.js":  {Data: []byte("console.log(1)")},
		"assets/index-x7y8z9.css": {Data: []byte("body{}")},
	})
	if err != nil {
		t.Fatalf("dashboardBundleHash different name: %v", err)
	}
	if first == differentName {
		t.Fatal("expected hash to change when a content-hashed asset filename changes")
	}
}

func TestDashboardBundleStatusForDistComparesRunningAndDiskHashes(t *testing.T) {
	t.Parallel()

	embedded := fstest.MapFS{
		"index.html":               {Data: []byte("<!doctype html>")},
		"assets/index-embedded.js": {Data: []byte("console.log(1)")},
	}
	uiRoot := t.TempDir()
	writeDashboardUITestFile(t, uiRoot, "dist/index.html", "<!doctype html>")
	writeDashboardUITestFile(t, uiRoot, "dist/assets/index-disk.js", "console.log(2)")

	status, err := dashboardBundleStatusForDist(embedded, filepath.Join(uiRoot, "dist"))
	if err != nil {
		t.Fatalf("dashboardBundleStatusForDist mismatch: %v", err)
	}
	if !status.Stale || status.RunningHash == "" || status.DiskHash == "" || status.Warning == "" {
		t.Fatalf("status = %+v, want stale with hashes and warning", status)
	}

	matchingRoot := t.TempDir()
	writeDashboardUITestFile(t, matchingRoot, "dist/index.html", "<!doctype html>")
	writeDashboardUITestFile(t, matchingRoot, "dist/assets/index-embedded.js", "console.log(2)")
	status, err = dashboardBundleStatusForDist(embedded, filepath.Join(matchingRoot, "dist"))
	if err != nil {
		t.Fatalf("dashboardBundleStatusForDist match: %v", err)
	}
	if status.Stale || status.RunningHash == "" || status.RunningHash != status.DiskHash {
		t.Fatalf("status = %+v, want fresh matching hashes", status)
	}
}

func TestDashboardBundleStatusForDistMissingIsStale(t *testing.T) {
	t.Parallel()

	status, err := dashboardBundleStatusForDist(fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>")},
	}, filepath.Join(t.TempDir(), "dist"))
	if err != nil {
		t.Fatalf("dashboardBundleStatusForDist missing: %v", err)
	}
	if !status.Stale {
		t.Fatalf("status = %+v, want stale for missing explicit dist", status)
	}
}

func TestDashboardUIDepsStaleWhenPackageManifestIsNewer(t *testing.T) {
	t.Parallel()

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
