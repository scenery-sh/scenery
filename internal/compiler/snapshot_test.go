package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotUnchangedVerifiesMembershipAndBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, appFilename)
	original := []byte("application \"snapshot\" {}\n")
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(path, original)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	check := func(want bool) {
		t.Helper()
		got, err := SnapshotUnchanged(result)
		if err != nil || got != want {
			t.Fatalf("snapshot unchanged=%v, want %v: %v", got, want, err)
		}
	}
	check(true)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	write(path, []byte("application \"modified\" {}\n"))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	check(false)
	write(path, original)
	check(true)
	added := filepath.Join(root, "added.scn")
	write(added, []byte("// additional source\n"))
	check(false)
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestSnapshotUnchangedIncludesImplementationBytes(t *testing.T) {
	root := t.TempDir()
	source := "application \"snapshot\" {}\nworkspace {\n implementation_root \"go\" {\n path = \".\"\n revision_include = [\"*.go\", \"go.mod\"]\n revision_exclude = []\n }\n}\n"
	for path, data := range map[string]string{appFilename: source, "handler.go": "package app\n", "go.mod": "module example.test/app\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v %#v", err, result.Diagnostics)
	}
	if ok, err := SnapshotUnchanged(result); err != nil || !ok {
		t.Fatalf("initial snapshot: %v %v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(root, "handler.go"), []byte("package app\nfunc Edited() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := SnapshotUnchanged(result); err != nil || ok {
		t.Fatalf("edited implementation: %v %v", ok, err)
	}
}
