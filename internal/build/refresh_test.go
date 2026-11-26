package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
)

func TestSyncSourceFilesDetectsNewFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nimport \"embed\"\n\n//go:embed templates/*\nvar _ embed.FS\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}

	const asset = "svc/templates/cv_classic.css"
	writeBuildTestFile(t, appRoot, asset, "body { color: black; }\n")

	got, _, err := syncSourceFiles(root, appRoot, prevStamps, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(asset))); err != nil {
		t.Fatalf("expected new asset to be synced into workspace: %v", err)
	}
	found := slices.Contains(got, asset)
	if !found {
		t.Fatalf("expected source files to include %s, got %v", asset, got)
	}
}

func TestSyncSourceFilesResyncsFilesChangedOnDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nfunc oldSymbol() {}\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}

	// Simulate a change the file watcher never reported, e.g. a git pull
	// rewriting the file between a watch snapshot and the workspace sync.
	const updated = "package svc\n\nfunc replacementSymbol() {}\n"
	writeBuildTestFile(t, appRoot, "svc/api.go", updated)

	if _, _, err := syncSourceFiles(root, appRoot, prevStamps, nil); err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "svc", "api.go"))
	if err != nil {
		t.Fatalf("read workspace copy: %v", err)
	}
	if string(data) != updated {
		t.Fatalf("workspace copy = %q, want resynced %q", data, updated)
	}
}

func TestSyncSourceFilesRejectsPreservedMetadataAsContentIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()
	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	const rel = "svc/api.go"
	writeBuildTestFile(t, appRoot, rel, "package svc\nconst value = 1\n")
	_, previous, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(appRoot, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeBuildTestFile(t, appRoot, rel, "package svc\nconst value = 2\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	_, current, err := syncSourceFiles(root, appRoot, previous, nil)
	if err != nil {
		t.Fatal(err)
	}
	if current[rel].Hash == previous[rel].Hash {
		t.Fatal("same-size same-mtime source edit retained the previous content identity")
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || !strings.Contains(string(data), "value = 2") {
		t.Fatalf("workspace did not capture changed bytes: %q %v", data, err)
	}
}

func TestSyncSourceFilesUsesCapturedBytes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()
	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	const rel = "svc/api.go"
	captured := []byte("package svc\nconst value = 1\n")
	writeBuildTestFile(t, appRoot, rel, string(captured))
	info, err := os.Stat(filepath.Join(appRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(captured)
	snapshot := &SourceSnapshot{Files: map[string]SourceSnapshotFile{
		rel: {Size: info.Size(), ModTimeNano: info.ModTime().UnixNano(), Perm: uint32(info.Mode().Perm()), Hash: hex.EncodeToString(digest[:]), Data: captured},
	}}
	writeBuildTestFile(t, appRoot, rel, "package svc\nconst value = 2\n")
	if _, _, err := syncSourceFilesWithSnapshot(root, appRoot, nil, nil, snapshot); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || string(data) != string(captured) {
		t.Fatalf("workspace bytes = %q, want captured %q: %v", data, captured, err)
	}
}

func TestSyncSourceFilesRestoresDeletedWorkspaceCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()

	writeBuildTestFile(t, appRoot, "go.mod", "module example.com/test\n\ngo 1.25.0\n")
	writeBuildTestFile(t, appRoot, "svc/api.go", "package svc\n\nfunc helper() {}\n")

	_, prevStamps, err := syncSourceFiles(root, appRoot, nil, nil)
	if err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "svc", "api.go")); err != nil {
		t.Fatalf("remove workspace copy: %v", err)
	}

	if _, _, err := syncSourceFiles(root, appRoot, prevStamps, nil); err != nil {
		t.Fatalf("syncSourceFiles() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "svc", "api.go")); err != nil {
		t.Fatalf("expected deleted workspace copy to be restored: %v", err)
	}
}

func TestSyncGeneratedFilesKeepsPathsThatAreNowRegularSourceFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := t.TempDir()
	rel := "house/rooftopology_api.go"
	writeBuildTestFile(t, appRoot, rel, "package house\n\nfunc helper() {}\n")
	writeBuildTestFile(t, root, rel, "package house\n\nfunc oldGenerated() {}\n")

	got, err := syncGeneratedFiles(root, appRoot, &codegen.Output{
		Generated: map[string][]byte{},
	}, []string{rel}, []string{rel})
	if err != nil {
		t.Fatalf("syncGeneratedFiles() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("generated files = %v, want empty", got)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("expected source-backed file to remain: %v", err)
	}
	if string(data) != "package house\n\nfunc oldGenerated() {}\n" {
		t.Fatalf("unexpected file contents after syncGeneratedFiles: %q", data)
	}
}

// Regression: the refreshed workspace is the same private resource another
// process materializes and compiles under the exclusive workspace lock. An
// unlocked mutation phase can delete that process's in-flight build output or
// change the bytes its post-compile verification checks.
func TestPrepareCachedWorkspaceMutatesUnderWorkspaceLock(t *testing.T) {
	t.Parallel()

	appDir, result := newCachedBuildTestWorkspace(t, "graph-1")
	const edited = `package svc

import "context"

func Hello(ctx context.Context) error { return nil }

func editedDuringAnotherBuild() {}
`
	writeBuildTestFile(t, appDir, "svc/api.go", edited)

	lockPath := filepath.Join(result.Dir, ".scenery-workspace.lock")
	mutationSteps := map[string]bool{"source.local_modules": true, "workspace.materialize": true}
	observed := map[string]bool{}
	var unlocked []string
	ctx := WithTrace(context.Background(), func(step Step) {
		if !mutationSteps[step.Name] {
			return
		}
		observed[step.Name] = true
		release, acquired, existing, err := trySharedBinaryExistingLock(lockPath)
		if err != nil {
			t.Errorf("probe workspace lock during %s: %v", step.Name, err)
			return
		}
		if acquired {
			release()
		}
		if acquired || !existing {
			unlocked = append(unlocked, step.Name)
		}
	})

	prepared, err := prepareCachedWorkspace(ctx, appDir, appcfg.Config{}, result, nil, false)
	if err != nil {
		t.Fatalf("prepareCachedWorkspace() error = %v", err)
	}
	if !prepared {
		t.Fatal("changed source should retain the declaration-equivalent prepared workspace")
	}
	data, err := os.ReadFile(filepath.Join(result.Dir, "svc", "api.go"))
	if err != nil || string(data) != edited {
		t.Fatalf("workspace copy = %q err=%v, want the refreshed implementation", data, err)
	}
	if len(observed) != len(mutationSteps) {
		t.Fatalf("observed mutation steps %v, want %v", observed, mutationSteps)
	}
	if len(unlocked) > 0 {
		t.Fatalf("cached workspace refresh mutated %v without the workspace lock", unlocked)
	}
	// The caller reacquires the same lock in CompileContext, so the refresh
	// must not retain it.
	release, acquired, _, err := trySharedBinaryExistingLock(lockPath)
	if err != nil || !acquired {
		t.Fatalf("refresh retained the workspace lock: acquired=%t err=%v", acquired, err)
	}
	release()
}
