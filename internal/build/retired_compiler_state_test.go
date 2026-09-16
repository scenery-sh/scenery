package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/devcache"
)

// A workspace that holds state of the removed retained compiler loses it, in
// every cache version, while another workspace keeps its own. A removal that
// fails leaves the workspace unretired, so a later build tries again.
func TestRetiredCompilerStateIsRemovedForItsWorkspaceOnly(t *testing.T) {
	cache := t.TempDir()
	t.Cleanup(devcache.SetRoot(cache))
	workspace, other := filepath.Join(t.TempDir(), "app"), filepath.Join(t.TempDir(), "other")
	state := func(workspace, version string) string {
		t.Helper()
		name, err := retiredCompilerStateName(workspace)
		if err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(cache, "build", "retained-go-compiler", version, name)
		if err := os.MkdirAll(filepath.Join(root, "recipes"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "current.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	owned := []string{state(workspace, "v1"), state(workspace, "v2")}
	kept := state(other, "v2")
	version := filepath.Dir(owned[1])
	if err := os.Chmod(version, 0o500); err != nil {
		t.Fatal(err)
	}
	retireCompilerState(context.Background(), workspace)
	if err := os.Chmod(version, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(owned[1]); err != nil {
		t.Fatalf("retained compiler state vanished although its removal failed: %v", err)
	}
	retireCompilerState(context.Background(), workspace)
	for _, root := range owned {
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retired compiler state %s remains: %v", root, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(kept, "current.json")); err != nil {
		t.Fatalf("another workspace's state was removed: %v", err)
	}
}
