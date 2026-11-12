package compiler

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestRootPackagesLoadedAheadEqualPackagesLoadedInOrder(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "apps", "multiservice"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile fixture: %v, %+v", err, result.Diagnostics)
	}
	var modules []*Block
	for _, source := range result.Sources {
		if source.Relative != "app.scn" {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type == "module" {
				modules = append(modules, block)
			}
		}
	}
	if len(modules) < 2 {
		t.Fatalf("fixture declares %d root modules, want several", len(modules))
	}
	lockfile, _, err := loadLockfile(root)
	if err != nil {
		t.Fatal(err)
	}
	ahead := precompileRootPackages(root, modules, lockfile)
	if len(ahead) != len(modules) {
		t.Fatalf("loaded %d packages ahead for %d modules", len(ahead), len(modules))
	}
	for key := range ahead {
		wantResources, wantSources, wantDiagnostics := compilePackageLogical(root, key.directory, key.module, key.logicalBase)
		resources, sources, diagnostics := ahead.compile(root, key.directory, key.module, key.logicalBase)
		addresses := func(values []Resource) []string {
			out := make([]string, len(values))
			for index, value := range values {
				out[index] = value.Address
			}
			return out
		}
		if !slices.Equal(addresses(resources), addresses(wantResources)) || len(sources) != len(wantSources) || len(diagnostics) != len(wantDiagnostics) {
			t.Fatalf("package %s loaded ahead differs from the one loaded in order", key.module)
		}
		for index := range sources {
			if sources[index].Relative != wantSources[index].Relative || string(sources[index].Bytes) != string(wantSources[index].Bytes) {
				t.Fatalf("package %s source %d differs", key.module, index)
			}
		}
		// A package is handed out once; a second instance loads its own.
		if _, held := ahead[key]; held {
			t.Fatalf("package %s stayed available after it was taken", key.module)
		}
		if again, _, _ := ahead.compile(root, key.directory, key.module, key.logicalBase); !slices.Equal(addresses(again), addresses(wantResources)) {
			t.Fatalf("package %s was not loaded again after it was taken", key.module)
		}
	}
	// One module needs no workers, and unresolvable sources are left to the loop.
	if precompileRootPackages(root, modules[:1], lockfile) != nil {
		t.Fatal("a single module was loaded ahead")
	}
}

func TestImplementationRootsWalkedAheadEqualTheWalkInOrder(t *testing.T) {
	t.Parallel()
	for _, fixture := range []string{"multiservice", "worktree-postgres", "basic"} {
		root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "apps", fixture))
		if err != nil {
			t.Fatal(err)
		}
		result, err := Compile(root)
		if err != nil || !result.Valid() {
			t.Fatalf("compile %s: %v", fixture, err)
		}
		direct, err := computeWorkspaceRevision(root, result.Sources)
		if err != nil {
			t.Fatal(err)
		}
		walk := beginImplementationWalk(root, result.Sources)
		ahead, err := computeWorkspaceRevisionWalked(root, result.Sources, walk)
		if err != nil || ahead != direct || direct != result.WorkspaceRevision {
			t.Fatalf("%s: revision walked ahead %q, in order %q, compiled %q (%v)", fixture, ahead, direct, result.WorkspaceRevision, err)
		}
		if (walk == nil) != (rootWorkspaceBlock(result.Sources) == nil) {
			t.Fatalf("%s: a walk started without a workspace block, or not with one", fixture)
		}
		// A walk of another workspace block is never used for this one.
		other := &implementationWalk{workspace: &Block{}, done: make(chan struct{})}
		if viaOther, err := computeWorkspaceRevisionWalked(root, result.Sources, other); err != nil || viaOther != direct {
			t.Fatalf("%s: a foreign walk changed the revision: %q, %v", fixture, viaOther, err)
		}
	}
}
