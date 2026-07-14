package generate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestEditorWorkspaceSupportsRawGoWithoutMaterializedGeneratedGo(t *testing.T) {
	parallelIntegrationTest(t)

	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	if err := os.RemoveAll(filepath.Join(root, "house", "scenerycontract")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "internal", "scenerygen")); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncEditorWorkspace(result); err != nil {
		t.Fatal(err)
	}
	command := boundedGoCommand("test", "./...")
	command.Dir = root
	command.Env = withoutEnvironment(command.Env, "GOWORK")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("raw go test failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "house", "scenerycontract")); !os.IsNotExist(err) {
		t.Fatal("editor sync materialized package contracts in the checkout")
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "scenerygen")); !os.IsNotExist(err) {
		t.Fatal("editor sync materialized application generation in the checkout")
	}
}

func TestEditorWorkspaceConcurrentSyncCreatesOneGeneration(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- SyncEditorWorkspace(result)
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	cacheRoot, err := editorCacheRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(cacheRoot, "generations"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("generations = %d, want 1", len(entries))
	}
}

func TestEditorWorkspaceNeverReplacesUnverifiedWorkFile(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "go.work")
	const authored = "go 1.26.3\n"
	if err := os.WriteFile(path, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncEditorWorkspace(result); err == nil || !strings.Contains(err.Error(), "user-owned") {
		t.Fatalf("sync error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != authored {
		t.Fatalf("user workfile changed: %q, %v", data, err)
	}
}

func TestEditorWorkspaceStopsAfterOwnedWorkFileDiverges(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncEditorWorkspace(result); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "go.work")
	const changed = "go 1.26.3\n// user changed this\n"
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncEditorWorkspace(result); err == nil || !strings.Contains(err.Error(), "changed after Scenery created it") {
		t.Fatalf("sync error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != changed {
		t.Fatalf("diverged workfile changed: %q, %v", data, err)
	}
}

func TestEditorWorkspaceExplicitMergePreservesUserWorkFile(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	if err := os.RemoveAll(filepath.Join(root, "house", "scenerycontract")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "internal", "scenerygen")); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "go.work")
	authored := []byte("go 1.26.3\n\nuse .\n\n// user-owned sentinel\n")
	if err := os.WriteFile(path, authored, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncEditorWorkspaceMerge(result); err != nil {
		t.Fatal(err)
	}
	merged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(merged, authored) || !bytes.Contains(merged, []byte(editorMergeBegin)) {
		t.Fatalf("merged workfile lost user bytes:\n%s", merged)
	}
	command := boundedGoCommand("test", "./...")
	command.Dir = root
	command.Env = withoutEnvironment(command.Env, "GOWORK")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("raw go test failed: %v\n%s", err, output)
	}
	changed := bytes.Replace(merged, []byte("scenery:begin"), []byte("scenery:changed"), 1)
	if err := os.WriteFile(path, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncEditorWorkspace(result); err == nil || !strings.Contains(err.Error(), "managed block") {
		t.Fatalf("sync error = %v", err)
	}
}

func withoutEnvironment(environment []string, name string) []string {
	prefix := name + "="
	filtered := environment[:0]
	for _, value := range environment {
		if !strings.HasPrefix(value, prefix) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}
