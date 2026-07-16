package main

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestUICatalogSnapshotDetectsChangesAndSkipsBuildDirs(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, contents string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("components/Button.tsx", "one")
	write("node_modules/pkg/index.js", "ignored")
	write("dist/bundle.js", "ignored")

	first, err := uiCatalogSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first["components/Button.tsx"]; !ok {
		t.Fatalf("snapshot missing catalog file: %#v", first)
	}
	for path := range first {
		if filepath.ToSlash(path) != "components/Button.tsx" {
			t.Fatalf("snapshot includes skipped path %q", path)
		}
	}

	second, err := uiCatalogSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(first, second) {
		t.Fatal("unchanged tree produced different snapshots")
	}

	write("components/Button.tsx", "one two three")
	changed, err := uiCatalogSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if maps.Equal(first, changed) {
		t.Fatal("changed file not reflected in snapshot")
	}
}
