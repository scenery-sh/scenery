package deployplan

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/appwalk"
)

func parallelVNextIntegrationTest(t *testing.T) { t.Helper() }

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && appwalk.SkipDir(source, path) {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCopyTreeSkipsSceneryState(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	authoredPath := filepath.Join(source, "app.scn")
	if err := os.WriteFile(authoredPath, []byte("app {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(source, ".scenery", "cache", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	copyTree(t, source, target)

	if got, err := os.ReadFile(filepath.Join(target, "app.scn")); err != nil || string(got) != "app {}\n" {
		t.Fatalf("authored file = %q, %v; want copied contents", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, ".scenery")); !os.IsNotExist(err) {
		t.Fatalf("copied .scenery state: %v", err)
	}
}
