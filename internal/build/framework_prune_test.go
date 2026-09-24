package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/machine"
)

func frameworkPruneTestSelection(t *testing.T, root, sourceDigest, executableDigest string) FrameworkSelection {
	t.Helper()
	binary := filepath.Join(root, ".scenery/framework/bin", sourceDigest, "test-platform", executableDigest, "scenery")
	writeBuildTestFile(t, filepath.Dir(binary), "scenery", "executable "+executableDigest)
	writeBuildTestFile(t, filepath.Join(root, ".scenery/framework/source", sourceDigest), "go.mod", "module scenery.sh\n")
	return FrameworkSelection{
		ArtifactIdentity: machine.NewArtifactIdentity(frameworkSelectionKind, frameworkSelectionSchema),
		AppRoot:          root,
		Source:           FrameworkSource{Root: filepath.Join(root, ".scenery/framework/source", sourceDigest), Digest: "sha256:" + sourceDigest},
		Version:          "dev",
		Executable:       binary,
		ExecutableDigest: "sha256:" + executableDigest,
	}
}

func frameworkPruneDigest(seed byte) string {
	return strings.Repeat(string(rune('a'+seed)), 64)
}

// Only the selected and the retained runtime's snapshots and producers stay;
// every other snapshot, producer and interrupted staging directory goes.
func TestPruneFrameworkStateKeepsSelectionAndRuntimeOnly(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	selected := frameworkPruneTestSelection(t, root, frameworkPruneDigest(0), frameworkPruneDigest(1))
	runtime := frameworkPruneTestSelection(t, root, frameworkPruneDigest(2), frameworkPruneDigest(3))
	if err := writeFrameworkSelectionAt(selected, FrameworkSelectionPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := writeFrameworkSelectionAt(runtime, RuntimeFrameworkPath(root)); err != nil {
		t.Fatal(err)
	}
	// An older producer of the selected source and its retired snapshot.
	sibling := frameworkPruneTestSelection(t, root, frameworkPruneDigest(0), frameworkPruneDigest(4))
	unselected := frameworkPruneTestSelection(t, root, frameworkPruneDigest(5), frameworkPruneDigest(6))
	writeBuildTestFile(t, root, ".scenery/framework/source/.source-123/go.mod", "module scenery.sh\n")
	writeBuildTestFile(t, root, ".scenery/framework/bin/"+frameworkPruneDigest(0)+"/test-platform/.build-123/scenery", "partial")
	writeBuildTestFile(t, root, ".scenery/framework/source/notes.txt", "not a snapshot")

	entries, err := PruneFrameworkState(root)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FrameworkPruneEntry{}
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	for path, want := range map[string]FrameworkPruneEntry{
		selected.Source.Root:              {Kind: FrameworkPruneSource, Reason: FrameworkPruneSelected},
		filepath.Dir(selected.Executable): {Kind: FrameworkPruneProducer, Reason: FrameworkPruneSelected},
		runtime.Source.Root:               {Kind: FrameworkPruneSource, Reason: FrameworkPruneRuntime},
		filepath.Dir(runtime.Executable):  {Kind: FrameworkPruneProducer, Reason: FrameworkPruneRuntime},
		filepath.Dir(sibling.Executable):  {Kind: FrameworkPruneProducer, Reason: FrameworkPruneUnselected, Removed: true},
		unselected.Source.Root:            {Kind: FrameworkPruneSource, Reason: FrameworkPruneUnselected, Removed: true},
		filepath.Join(root, ".scenery/framework/bin", frameworkPruneDigest(5)):                                {Kind: FrameworkPruneProducer, Reason: FrameworkPruneUnselected, Removed: true},
		filepath.Join(root, ".scenery/framework/source/.source-123"):                                          {Kind: FrameworkPruneStaging, Reason: FrameworkPruneInterrupted, Removed: true},
		filepath.Join(root, ".scenery/framework/bin", frameworkPruneDigest(0), "test-platform", ".build-123"): {Kind: FrameworkPruneStaging, Reason: FrameworkPruneInterrupted, Removed: true},
	} {
		got, ok := byPath[path]
		if !ok || got.Kind != want.Kind || got.Reason != want.Reason || got.Removed != want.Removed || got.Bytes <= 0 {
			t.Fatalf("%s: entry = %+v, want %+v", path, got, want)
		}
		if _, err := os.Stat(path); (err == nil) == want.Removed {
			t.Fatalf("%s: exists=%v, want removed=%v", path, err == nil, want.Removed)
		}
	}
	if len(entries) != 9 {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, ".scenery/framework/source/notes.txt")); err != nil {
		t.Fatalf("an unrecognized entry was touched: %v", err)
	}
	for _, path := range []string{selected.Executable, runtime.Executable, filepath.Join(selected.Source.Root, "go.mod")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("kept producer content changed: %v", err)
		}
	}
}

// A selection that exists but cannot be read means the kept set is unknown,
// so nothing is removed; an absent framework state root is simply empty.
func TestPruneFrameworkStateFailsClosedOnAnUnreadableSelection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unselected := frameworkPruneTestSelection(t, root, frameworkPruneDigest(7), frameworkPruneDigest(8))
	writeBuildTestFile(t, root, ".scenery/build/framework.json", "{}")
	if _, err := PruneFrameworkState(root); err == nil {
		t.Fatal("an unreadable selection did not fail the prune")
	}
	if _, err := os.Stat(unselected.Source.Root); err != nil {
		t.Fatalf("a failed prune removed state: %v", err)
	}
	entries, err := PruneFrameworkState(filepath.Join(root, "missing"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing app root prune = %+v, %v", entries, err)
	}
	entries, err = PruneFrameworkState(t.TempDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("app root without framework state prune = %+v, %v", entries, err)
	}
}
