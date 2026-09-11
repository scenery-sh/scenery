package generate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/scn"
)

func TestProjectionCacheInputsAndOwnership(t *testing.T) {
	result := &compiler.Result{Root: t.TempDir(), Manifest: &Manifest{}, Sources: []*scn.Source{{Bytes: []byte("declaration")}}}
	calls := 0
	render := func() ([]generatedFile, error) {
		calls++
		return []generatedFile{{Path: "contract.go", Bytes: []byte("original")}}, nil
	}
	read := func(extra string) []generatedFile {
		t.Helper()
		files, err := cachedProjection(newProjectionInput(result), "test", extra, render)
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	read("catalog")[0].Bytes[0] = 'x'
	result.WorkspaceRevision = "implementation-edit"
	result.ImplementationRevisions = map[string]string{"handler": "changed"}
	files := read("catalog")
	if calls != 1 || string(files[0].Bytes) != "original" {
		t.Fatalf("reuse/ownership: calls=%d files=%v", calls, files)
	}
	files[0].Path = "changed"
	files[0].Bytes[0] = 'x'
	if next := read("catalog"); next[0].Path != "contract.go" || string(next[0].Bytes) != "original" {
		t.Fatal("cache hit leaked mutable storage")
	}
	result.Sources[0].Bytes = []byte("new declaration")
	read("catalog")
	result.Sources[0].Blocks = []*scn.Block{{Type: "synthetic"}}
	read("catalog")
	result.Manifest.ContractRevision = "new-contract"
	read("catalog")
	result.HTTPSurfaceRevisions = map[string]string{"gateway": "new-http"}
	read("catalog")
	read("edited-live-catalog")
	if calls != 6 {
		t.Fatalf("input changes did not invalidate: %d renders", calls)
	}
}

func TestCachedProjectionStillChecksLiveModuleAndArtifacts(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	files, err := renderExpectedGoPackageFiles(result)
	if err != nil {
		t.Fatal(err)
	}
	writeRenderedFixture(t, files)
	generated, err := GenerateGoContractsFromResult(result, false)
	if err != nil || len(generated.Changed) != 0 {
		t.Fatalf("unchanged publication: %#v %v", generated, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".scenery", "transactions")); !os.IsNotExist(err) {
		t.Fatalf("unchanged output created a transaction: %v", err)
	}
	if err := os.Remove(files[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContractsFromResult(result, true); err == nil {
		t.Fatal("cache hid a deleted generated file")
	}
	if _, err := os.Stat(files[0].Path); !os.IsNotExist(err) {
		t.Fatal("check repaired deleted output")
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/wrong\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := renderExpectedGoPackageFiles(result); err == nil {
		t.Fatal("cache bypassed current module ownership")
	}
}

func TestProjectionCacheDoesNotRetainFailures(t *testing.T) {
	result := &compiler.Result{Root: t.TempDir()}
	want := errors.New("render failed")
	calls := 0
	for range 2 {
		_, err := cachedProjection(newProjectionInput(result), "failure", nil, func() ([]generatedFile, error) {
			calls++
			return nil, want
		})
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("failed rendering was cached")
	}
}

func TestProjectionInputSeparatesSelectionsAndFreshOperations(t *testing.T) {
	result := &compiler.Result{Root: "workspace", Sources: []*scn.Source{{Bytes: []byte("declaration")}}}
	input := newProjectionInput(result)
	first, err := input.key("public", "catalog")
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range []struct{ kind, extra string }{{"private", "catalog"}, {"public", "changed-catalog"}} {
		key, err := input.key(selection.kind, selection.extra)
		if err != nil || key == first {
			t.Fatalf("projection selection was not isolated: %v", err)
		}
	}
	result.Sources[0].Bytes = []byte("changed declaration")
	fresh, err := newProjectionInput(result).key("public", "catalog")
	if err != nil || fresh == first {
		t.Fatalf("a fresh operation reused changed source identity: %v", err)
	}
	result.Sources[0].Blocks = []*scn.Block{{Attributes: map[string]scn.Expression{"unsupported": {Value: make(chan int)}}}}
	invalid := newProjectionInput(result)
	calls := 0
	for range 2 {
		_, err := cachedProjection(invalid, "invalid", nil, func() ([]generatedFile, error) {
			calls++
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("unserializable common input was cached")
	}
}
