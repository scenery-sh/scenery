package build

import (
	"os"
	"path/filepath"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestCachedGraphRequiresCompleteOutputDigests(t *testing.T) {
	t.Parallel()
	root, result := newCachedBuildTestWorkspace(t, "graph")
	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.GeneratedFiles) == 0 {
		t.Fatal("fixture has no generated output")
	}
	path := filepath.Join(result.Dir, state.GeneratedFiles[0])
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cached, hit, err := LoadCachedGraph(root, appcfg.Config{Name: "buildtest"}, "graph"); err != nil || hit || cached != nil {
		t.Fatalf("tampered output was a hit: %v %v", hit, err)
	}
}

func TestMalformedDisposableBuildStateIsAMiss(t *testing.T) {
	t.Parallel()
	root, result := newCachedBuildTestWorkspace(t, "graph")
	if err := os.WriteFile(filepath.Join(result.Dir, buildStateFile), []byte(`{"version":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cached, hit, err := LoadCachedGraph(root, appcfg.Config{Name: "buildtest"}, "graph"); err != nil || hit || cached != nil {
		t.Fatalf("corrupt cache prevented rebuilding: %v %v", hit, err)
	}
}

func TestCachedArtifactSetAcceptsVerifiedAbsence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	paths := []string{"retired.ts"}
	expected := map[string]string{"retired.ts": absentArtifactStamp}
	check := func(want bool) {
		t.Helper()
		got, err := cachedArtifactSetCurrent(root, paths, expected)
		if err != nil || got != want {
			t.Fatalf("current = %v, want %v: %v", got, want, err)
		}
	}
	check(true)
	path := filepath.Join(root, paths[0])
	if err := os.WriteFile(path, []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(true)
	expected[paths[0]] = "invalid"
	check(false)
}
