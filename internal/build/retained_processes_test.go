package build

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"scenery.sh/internal/nativebuilddriver"
)

func TestBackgroundWorkEndsWithItsOwner(t *testing.T) {
	if startBackgroundWork(context.Background(), func(context.Context) { t.Error("work without an owner ran") }) {
		t.Fatal("a build without an owner scheduled background work")
	}
	work := NewBackgroundWork(context.Background())
	traced := WithTraceOperation(context.Background(), "operation", func(Step) {})
	build, cancelBuild := context.WithCancel(WithBackgroundWork(traced, work))
	running, stopped := make(chan struct{}), make(chan struct{})
	if !startBackgroundWork(build, func(ctx context.Context) {
		close(running)
		<-ctx.Done()
		if _, ok := ctx.Value(traceKey{}).(*traceEmitter); !ok {
			t.Error("background work lost the trace of the build that scheduled it")
		}
		close(stopped)
	}) {
		t.Fatal("an open owner refused background work")
	}
	<-running
	// The build that scheduled the work ends; the work does not.
	cancelBuild()
	select {
	case <-stopped:
		t.Fatal("background work ended with the build that scheduled it")
	case <-time.After(time.Millisecond):
	}
	work.Close()
	select {
	case <-stopped:
	default:
		t.Fatal("Close returned before the owned work stopped")
	}
	if startBackgroundWork(WithBackgroundWork(context.Background(), work), func(context.Context) {}) {
		t.Fatal("a closed owner accepted background work")
	}
	(*BackgroundWork)(nil).Close()
}

func TestRejectedRetainedProcessRecipeIsReplaced(t *testing.T) {
	targetRoot := filepath.Join(t.TempDir(), "processes", "targets", "echo_echo")
	committed := filepath.Join(targetRoot, "recipe-old", "recipe.json")
	load := func() (*retainedNativeLoaded, error) { return &retainedNativeLoaded{recipePath: committed}, nil }
	if !retainedProcessNeedsRecipe(targetRoot, func() (*retainedNativeLoaded, error) { return nil, os.ErrNotExist }) {
		t.Fatal("a target without a usable recipe did not need one")
	}
	if retainedProcessNeedsRecipe(targetRoot, load) {
		t.Fatal("a usable committed recipe was replaced")
	}
	// A retained build found the committed recipe incompatible: its pointer
	// still exists, but it must not suppress the replacement.
	retainedProcessRejected.Store(targetRoot, committed)
	t.Cleanup(func() { retainedProcessRejected.Delete(targetRoot) })
	if !retainedProcessNeedsRecipe(targetRoot, load) {
		t.Fatal("a rejected recipe suppressed its replacement")
	}

	for _, directory := range []string{filepath.Dir(committed), filepath.Join(targetRoot, "recipe-new")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Dir(committed), old, old); err != nil {
		t.Fatal(err)
	}
	recorder := "/fixture/scenery"
	key := targetRoot + "\x00" + recorder
	entry := &retainedNativeCacheEntry{loaded: &retainedNativeLoaded{recipePath: committed}}
	retainedProcessRecipes.Store(key, entry)
	t.Cleanup(func() { retainedProcessRecipes.Delete(key) })
	replacement := filepath.Join(targetRoot, "recipe-new", "recipe.json")
	store := retainedProcessStoreFor(filepath.Dir(filepath.Dir(targetRoot)))
	if err := publishRetainedProcessRecipe(targetRoot, filepath.Dir(replacement), retainedNativeCurrent{RecipePath: replacement, RecorderExecutable: recorder}); err != nil {
		t.Fatal(err)
	}
	var current retainedNativeCurrent
	data, err := os.ReadFile(filepath.Join(targetRoot, "current.json"))
	if err != nil || decodeRetainedNativeJSON(data, &current) != nil || current.RecipePath != replacement {
		t.Fatalf("published pointer = %s (%v)", data, err)
	}
	if entry.loaded != nil {
		t.Fatal("the next retained build would keep using the rejected recipe")
	}
	if _, rejected := retainedProcessRejected.Load(targetRoot); rejected {
		t.Fatal("the published replacement is still rejected")
	}
	if _, err := os.Lstat(filepath.Dir(committed)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the superseded recipe was kept: %v", err)
	}
	if store.publications != 1 {
		t.Fatalf("publications = %d, want 1", store.publications)
	}
}

// writeRetainedProcessFixture commits a recipe of one target that references
// exactly the named shared artifacts.
func writeRetainedProcessFixture(t *testing.T, root, target string, artifacts []string) {
	t.Helper()
	targetRoot := retainedProcessTargetRoot(root, target)
	recipe := &nativebuilddriver.Recipe{Retained: map[string]nativebuilddriver.RetainedFile{}}
	for _, artifact := range artifacts {
		recipe.Retained[artifact] = nativebuilddriver.RetainedFile{Digest: "sha256:" + filepath.Base(artifact)}
	}
	recipePath := filepath.Join(targetRoot, "recipe", "recipe.json")
	if err := os.MkdirAll(filepath.Dir(recipePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRetainedNativeRecipe(recipePath, recipe); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(retainedNativeCurrent{RecipePath: recipePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetRoot, "current.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSharedRetainedProcessStoreStaysBoundedAcrossALongSession(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	workspace := filepath.Join(t.TempDir(), "app")
	root, shared, err := retainedProcessRoots(workspace)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := filepath.Join(shared, "artifacts")
	if err := os.MkdirAll(artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	store := retainedProcessStoreFor(root)
	count := func() int {
		entries, err := os.ReadDir(artifacts)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	services := []string{"echo_echo", "greeter_greeter", "maps_maps"}
	// A target whose first capture never completed has no pointer yet.
	if err := os.MkdirAll(retainedProcessTargetRoot(root, "pending_pending"), 0o700); err != nil {
		t.Fatal(err)
	}
	peak := 0
	for edit := range 120 {
		// Each retained edit of one service advances its recipe to a new
		// archive and supersedes the previous one.
		service := services[edit%len(services)]
		artifact := filepath.Join(artifacts, strconv.Itoa(edit)+".a")
		if err := os.WriteFile(artifact, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeRetainedProcessFixture(t, root, service, []string{artifact})
		store.published()
		pruneRetainedProcessState(workspace)
		peak = max(peak, count())
	}
	if limit := retainedProcessCollectInterval + len(services); peak > limit {
		t.Fatalf("shared store peaked at %d archives across 120 edits, want at most %d", peak, limit)
	}

	// A capture adopts archives before its recipe names them: no collection
	// may remove them while it holds its lease.
	release := store.lease()
	adopted := filepath.Join(artifacts, "adopted.a")
	if err := os.WriteFile(adopted, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range retainedProcessCollectInterval {
		store.published()
	}
	pruneRetainedProcessState(workspace)
	if _, err := os.Lstat(adopted); err != nil {
		t.Fatalf("a collection removed an archive a running capture adopted: %v", err)
	}
	release()
	release()
	if store.captures != 0 {
		t.Fatalf("captures = %d after releasing one lease twice", store.captures)
	}
	pruneRetainedProcessState(workspace)
	if _, err := os.Lstat(adopted); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the next collection kept an archive no recipe references: %v", err)
	}
}
