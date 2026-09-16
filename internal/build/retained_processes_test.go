package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A workspace that recorded service entrypoint recipes before they were
// retired loses that state on its next process build, and keeps the retained
// compiler state of its application entrypoint.
func TestRetiredProcessRecipeStateIsRemovedBesideApplicationState(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	workspace := filepath.Join(t.TempDir(), "app")
	root, err := retainedNativeRoot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	retired := filepath.Join(root, retiredRetainedProcessRoot)
	application := []string{filepath.Join(root, "current.json"), filepath.Join(root, "recipes", "recipe-1", "recipe.json")}
	for _, path := range append(application, filepath.Join(retired, "targets", "echo_echo", "current.json"), filepath.Join(retired, "shared", "artifacts", "a.a")) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A removal that fails leaves the workspace unretired, so a later process
	// build tries again.
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	retireRetainedProcessState(context.Background(), workspace)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(retired); err != nil {
		t.Fatalf("retired state vanished although its removal failed: %v", err)
	}
	retireRetainedProcessState(context.Background(), workspace)
	if _, err := os.Lstat(retired); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired entrypoint recipes remain: %v", err)
	}
	for _, path := range application {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("application retained state %s was removed: %v", path, err)
		}
	}
}
