package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesParentAndReplacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if err := Write(path, []byte("old"), 0o600, Options{}); err != nil {
		t.Fatalf("initial Write() error = %v", err)
	}

	if err := Write(path, []byte("new contents"), 0o640, Options{}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new contents" {
		t.Fatalf("content = %q, want %q", got, "new contents")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o640); got != want {
		t.Fatalf("mode = %o, want %o", got, want)
	}
}

func TestWriteSyncEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, []byte("durable"), 0o600, Options{SyncFile: true, SyncDir: true}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "durable" {
		t.Fatalf("content = %q, want %q", got, "durable")
	}
}

func TestWriteRenameFailureCleansUpTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Write(path, []byte("contents"), 0o600, Options{})
	if err == nil {
		t.Fatal("Write() error = nil, want rename failure")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".state.json.tmp-") {
			t.Fatalf("temporary file %q remained after failure", entry.Name())
		}
	}
}
