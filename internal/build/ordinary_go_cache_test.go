package build

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

const cachedContractPath = "svc/scenerycontract/types.gen.go"
const cachedContractBytes = "package scenerycontract\n"

func TestCachedGoProjectionRestoresMissingPublicPackage(t *testing.T) {
	root, result := ordinaryGoCacheFixture(t)
	writeBuildTestFile(t, result.Dir, cachedContractPath, cachedContractBytes)
	current, err := refreshCachedGoProjection(root, result, nil)
	if err != nil || !current {
		t.Fatalf("cache preparation: current=%v err=%v", current, err)
	}
	if _, err := os.Stat(filepath.Join(root, cachedContractPath)); err != nil {
		t.Fatalf("cache hit did not prepare public package: %v", err)
	}
}

func TestCachedGoProjectionRejectsMissingPrivateBytes(t *testing.T) {
	root, result := ordinaryGoCacheFixture(t)
	current, err := refreshCachedGoProjection(root, result, nil)
	if err != nil || current {
		t.Fatalf("missing private projection: current=%v err=%v", current, err)
	}
}

func TestCachedGoProjectionRejectsStalePrivateBytes(t *testing.T) {
	root, result := ordinaryGoCacheFixture(t)
	writeBuildTestFile(t, result.Dir, cachedContractPath, "package scenerycontract\n// stale cache\n")
	current, err := refreshCachedGoProjection(root, result, nil)
	if err != nil || current {
		t.Fatalf("stale private projection: current=%v err=%v", current, err)
	}
}

func ordinaryGoCacheFixture(t *testing.T) (string, *Result) {
	t.Helper()
	root, result := newCachedBuildTestWorkspace(t, "graph-1")
	previous := generateHooks
	t.Cleanup(func() { generateHooks = previous })
	// Test the build-owned hook ordering and byte comparison here. Actual
	// generator publication through a cached candidate CLI is a release proof.
	generateHooks.PrepareBuildGoWorkspace = func(contract *compiler.Result) (generateapi.GoWorkspaceProjection, error) {
		writeBuildTestFile(t, contract.Root, cachedContractPath, cachedContractBytes)
		return generateapi.GoWorkspaceProjection{Files: map[string][]byte{cachedContractPath: []byte(cachedContractBytes)}}, nil
	}
	generateHooks.SyncCachedTypeScript = func(*compiler.Result) error { return nil }
	return root, result
}
