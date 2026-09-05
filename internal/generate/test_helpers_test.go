package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/compiler"
)

func parallelIntegrationTest(t *testing.T) {
	t.Helper()
	t.Parallel()
}

func hasDiagnostic(diagnostics []compiler.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && appwalk.SkipDir(source, path) {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, contents, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCopyTreeSkipsNonSourceDirectories(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()

	write := func(rel string) {
		t.Helper()
		path := filepath.Join(source, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.scn")
	write(".scenery/cache/runtime.bin")
	write("node_modules/dependency/index.js")

	copyTree(t, source, target)

	if contents, err := os.ReadFile(filepath.Join(target, "app.scn")); err != nil {
		t.Fatalf("read copied authored source: %v", err)
	} else if string(contents) != "app.scn" {
		t.Fatalf("copied authored source = %q, want %q", contents, "app.scn")
	}
	for _, rel := range []string{".scenery", "node_modules"} {
		if _, err := os.Stat(filepath.Join(target, rel)); !os.IsNotExist(err) {
			t.Fatalf("ignored directory %q was copied: %v", rel, err)
		}
	}
}

func rewriteFixtureSceneryReplace(t *testing.T, root string) {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(repositoryRoot), 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
