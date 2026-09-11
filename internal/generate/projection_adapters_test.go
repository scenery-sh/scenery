package generate

import (
	"errors"
	"testing"

	"scenery.sh/internal/scn"
)

func TestAdapterProjectionSeparatesBuildIdentityAndOwnsValues(t *testing.T) {
	result := &Result{Root: t.TempDir(), Manifest: &Manifest{}, Sources: []*scn.Source{{Bytes: []byte("contract")}}}
	calls := 0
	render := func() ([]applicationAdapter, error) {
		calls++
		return []applicationAdapter{{Source: []byte("source"), applicationAdapterMetadata: applicationAdapterMetadata{Covered: []string{"operation"}}}}, nil
	}
	read := func(importPath string) []applicationAdapter {
		t.Helper()
		adapters, err := cachedApplicationAdapters(newProjectionInput(result), importPath, render)
		if err != nil {
			t.Fatal(err)
		}
		return adapters
	}
	first := read("app/internal/scenerygen")
	first[0].Source[0] = 'x'
	first[0].Covered[0] = "changed"
	result.WorkspaceRevision = "next-workspace"
	result.ImplementationRevisions = map[string]string{"development": "next-implementation"}
	second := read("app/internal/scenerygen")
	if calls != 1 || string(second[0].Source) != "source" || second[0].Covered[0] != "operation" {
		t.Fatalf("build identity prevented reuse or mutable values leaked: calls=%d adapters=%v", calls, second)
	}
	second[0].Source[0] = 'x'
	second[0].Covered[0] = "changed"
	if third := read("app/internal/scenerygen"); string(third[0].Source) != "source" || third[0].Covered[0] != "operation" {
		t.Fatal("cache hit did not own its values")
	}
	result.Manifest.ContractRevision = "new-contract"
	read("app/internal/scenerygen")
	result.Sources[0].Blocks = []*scn.Block{{Type: "synthetic"}}
	read("app/internal/scenerygen")
	read("other/internal/scenerygen")
	if calls != 4 {
		t.Fatalf("declaration/import invalidation: %d renders", calls)
	}
	result.Root = t.TempDir()
	want := errors.New("invalid adapter")
	for range 2 {
		_, err := cachedApplicationAdapters(newProjectionInput(result), "app", func() ([]applicationAdapter, error) {
			calls++
			return nil, want
		})
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	if calls != 6 {
		t.Fatal("failed rendering was cached")
	}
}
