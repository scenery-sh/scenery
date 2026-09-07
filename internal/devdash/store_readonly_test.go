package devdash

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyStoreNeverCreatesOrRewritesCache(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	if _, err := OpenReadOnlyStore(missing); !os.IsNotExist(err) {
		t.Fatalf("missing cache: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("inspection created a cache directory")
	}
	path := filepath.Join(root, "devdash.json")
	original := []byte("{\n  \"version\": 1, \"apps\": {}\n}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenReadOnlyStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertApp(t.Context(), AppRecord{ID: "not-authorized"}); err == nil {
		t.Fatal("read-only store accepted a mutation")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, actual) {
		t.Fatal("read-only open/close rewrote the cache")
	}
}
