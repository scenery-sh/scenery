package main

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

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
