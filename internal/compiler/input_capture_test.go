package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeclaredInputCaptureTracksBytesMembershipAndExactExclusions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCompilerSnapshotFile(t, root, "queries/first.sql", "select 1;\n")
	writeCompilerSnapshotFile(t, root, "queries/authored_gen.go", "package authored\n")
	writeCompilerSnapshotFile(t, root, "queries/generated.go", "package generated\n")
	selections := []InputSelection{{Path: "queries"}, {Path: "missing.sql"}}
	read := func() *InputCapture {
		t.Helper()
		captured, err := CaptureInputPaths(root, selections, map[string]bool{"queries/generated.go": true})
		if err != nil {
			t.Fatal(err)
		}
		return captured
	}
	before := read()
	if _, ok := before.Files["queries/authored_gen.go"]; !ok {
		t.Fatal("suffix exclusion hid authored code")
	}
	info, err := os.Stat(filepath.Join(root, "queries/first.sql"))
	if err != nil {
		t.Fatal(err)
	}
	writeCompilerSnapshotFile(t, root, "queries/first.sql", "select 2;\n")
	if err := os.Chtimes(filepath.Join(root, "queries/first.sql"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if next := read(); next.Digest == before.Digest {
		t.Fatal("restored-mtime edit did not invalidate")
	}
	before = read()
	writeCompilerSnapshotFile(t, root, "missing.sql", "select 3;\n")
	if next := read(); next.Digest == before.Digest {
		t.Fatal("absent input appearance did not invalidate")
	}
	before = read()
	writeCompilerSnapshotFile(t, root, "queries/added.sql", "select 4;\n")
	if next := read(); next.Digest == before.Digest {
		t.Fatal("directory membership change did not invalidate")
	}
	before = read()
	writeCompilerSnapshotFile(t, root, "queries/output/new.go", "package output\n")
	after, err := CaptureInputPaths(root, selections, map[string]bool{"queries/generated.go": true, "queries/output/new.go": true})
	if err != nil || after.Digest != before.Digest {
		t.Fatalf("owned output/parent directory invalidated authored inputs: %v", err)
	}
}
