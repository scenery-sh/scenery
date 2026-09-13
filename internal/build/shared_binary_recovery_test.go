package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestSharedBinaryRejectsChangedFrameworkBeforePublication(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "framework")
}

func TestSharedBinaryRejectsChangedLocalReplaceBeforePublication(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "local_replace")
}

func TestSharedBinaryRejectsChangedWorkspaceBeforePublication(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "workspace")
}

func TestSharedBinaryRejectsRestoredInputsBeforePublication(t *testing.T) {
	testSharedBinaryRejectsChangedInputs(t, "restored")
}

func testSharedBinaryRejectsChangedInputs(t *testing.T, kind string) {
	t.Helper()
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, result := newCachedBuildTestWorkspace(t, "input-proof")
	prepareSharedBinaryTestResult(root, result)
	dependency := t.TempDir()
	writeBuildTestFile(t, dependency, "go.mod", "module scenery.sh\n")
	writeBuildTestFile(t, dependency, "dep.go", "package dep\nconst Value = \"A\"\n")
	inputPath := filepath.Join(dependency, "dep.go")
	if kind == "workspace" {
		inputPath = filepath.Join(result.Dir, "svc/api.go")
	}
	original, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "framework" {
		source, err := FrameworkSourceManifest(dependency)
		if err != nil {
			t.Fatal(err)
		}
		result.FrameworkSourceRoot, result.FrameworkSourceDigest = dependency, source.Digest
	}
	// Inject only Go's package discovery. Actual file hashes, module
	// metadata, workspace and framework checks remain production code.
	discover := func(_ context.Context, candidate *Result) (*BuildInputManifest, error) {
		data, err := json.Marshal(goListPackage{Dir: dependency, ImportPath: "example.test/local", GoFiles: []string{"dep.go"},
			Module: &goListModule{Path: "example.test/local", Replace: &goListModule{Dir: dependency, GoMod: filepath.Join(dependency, "go.mod")}}})
		if err != nil {
			return nil, err
		}
		return buildInputManifestFromGoList(candidate, data)
	}
	result.BuildInput, err = discover(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	bindSharedBinaryTestIdentity(result)
	key, expected, err := sharedBinaryKey(result)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	builds := 0
	restore := SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go invocation: %v", args)
		}
		builds++
		if builds == 1 {
			close(started)
			<-release
		}
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return err
		}
		if kind == "restored" && builds == 1 {
			if err := os.WriteFile(inputPath, original, 0o644); err != nil {
				return err
			}
		}
		return os.WriteFile(output, data, 0o755)
	})
	t.Cleanup(restore)
	build := func() error {
		return runSharedGoBuildWithInputCheck(context.Background(), result, func(ctx context.Context) error {
			return verifySharedBinaryInputs(ctx, result, discover)
		})
	}
	done := make(chan error, 1)
	go func() { done <- build() }()
	<-started
	if err := os.WriteFile(inputPath, append([]byte("// B\n"), original...), 0o644); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed input was not rejected: %v", err)
	}
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, hit, err := loadSharedBinary(cacheRoot, key); err != nil || hit {
		t.Fatalf("rejected action left reusable output: hit=%t err=%v", hit, err)
	}
	if err := os.WriteFile(inputPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	// Identical bytes still derive the original key, but the live external
	// dependency keeps this retry outside the shared executable reuse domain.
	result.BuildInput, err = discover(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	if err := build(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Binary)
	if err != nil || string(data) != string(original) || builds != 2 {
		t.Fatalf("A retry restored rejected B: builds=%d data=%q err=%v", builds, data, err)
	}
	if hit, err := restoreSharedBinary(cacheRoot, key, expected, result.Binary); err != nil || hit {
		t.Fatalf("external-input retry became reusable: hit=%t err=%v", hit, err)
	}
}

func TestSharedBinaryCanceledProducerRetainsInputLease(t *testing.T) {
	synctest.Test(t, testSharedBinaryCanceledProducerRetainsInputLease)
}

func testSharedBinaryCanceledProducerRetainsInputLease(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, result := newCachedBuildTestWorkspace(t, "input-lease")
	prepareSharedBinaryTestResult(root, result)
	key, _, err := sharedBinaryKey(result)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	other, err := subscribeSharedBinary(cacheRoot, key)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	original, err := os.ReadFile(filepath.Join(result.Dir, "svc/api.go"))
	if err != nil {
		t.Fatal(err)
	}
	restore := SetGoRunnerForTesting(func(_ context.Context, dir string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go invocation: %v", args)
		}
		close(started)
		<-release
		data, err := os.ReadFile(filepath.Join(dir, "svc/api.go"))
		if err != nil {
			return err
		}
		if string(data) != string(original) {
			return fmt.Errorf("producer read next generation's input")
		}
		return os.WriteFile(output, data, 0o755)
	})
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		unlock, err := lockWorkspace(result.Dir)
		if err != nil {
			done <- err
			return
		}
		err = runSharedBinaryTestBuild(ctx, result)
		unlock()
		done <- err
	}()
	<-started
	cancel()
	synctest.Wait()
	deadline := time.Now().Add(50 * time.Millisecond)
	for {
		active, err := activeSharedBinarySubscribers(other.directory, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled subscriber did not leave")
		}
		time.Sleep(time.Millisecond)
	}
	unlockNext, acquired, _, err := trySharedBinaryExistingLock(filepath.Join(result.Dir, ".scenery-workspace.lock"))
	if acquired {
		// This is the next materializer's write, under the actual workspace lock.
		writeBuildTestFile(t, result.Dir, "svc/api.go", "package svc // next generation\n")
		unlockNext()
	}
	releaseOnce.Do(func() { close(release) })
	if got := <-done; !errors.Is(got, context.Canceled) {
		t.Fatalf("canceled owner result: %v", got)
	}
	if err != nil || acquired {
		t.Fatalf("next materializer acquired a workspace still read by the producer: acquired=%t err=%v", acquired, err)
	}
	unlockNext, acquired, _, err = trySharedBinaryExistingLock(filepath.Join(result.Dir, ".scenery-workspace.lock"))
	if err != nil || !acquired {
		t.Fatalf("completed producer retained workspace: acquired=%t err=%v", acquired, err)
	}
	unlockNext()
}

func TestSharedBinaryRepairsSemanticManifestCorruption(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, result := newCachedBuildTestWorkspace(t, "semantic-corruption")
	prepareSharedBinaryTestResult(root, result)
	builds := 0
	restore := SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go invocation: %v", args)
		}
		builds++
		return os.WriteFile(output, []byte("valid binary"), 0o755)
	})
	defer restore()
	if err := runSharedBinaryTestBuild(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	key, _, _ := sharedBinaryKey(result)
	cacheRoot, _ := sharedBinaryRoot()
	for _, field := range []string{"contract_revision", "implementation_revision", "build_input_digest", "framework_source_digest"} {
		manifest := filepath.Join(cacheRoot, "artifacts", key, "manifest.json")
		data, err := os.ReadFile(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := json.Unmarshal(data, &values); err != nil {
			t.Fatal(err)
		}
		values[field] = "sha256:" + strings.Repeat("f", 64)
		data, err = json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifest, data, 0o644); err != nil {
			t.Fatal(err)
		}
		before := builds
		if err := runSharedBinaryTestBuild(context.Background(), result); err != nil {
			t.Fatalf("repair %s: %v", field, err)
		}
		if err := runSharedBinaryTestBuild(context.Background(), result); err != nil || builds != before+1 {
			t.Fatalf("repair did not restore reuse for %s: builds=%d err=%v", field, builds, err)
		}
	}
}

func TestSharedBinaryRepairsDestinationExecutionMode(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, result := newCachedBuildTestWorkspace(t, "execute-mode")
	prepareSharedBinaryTestResult(root, result)
	cacheRoot, _ := sharedBinaryRoot()
	key, expected, _ := sharedBinaryKey(result)
	writeBuildTestFile(t, root, "binary", "valid executable")
	if err := publishSharedBinary(cacheRoot, key, expected, filepath.Join(root, "binary")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o644, 0o600} {
		if _, err := restoreSharedBinary(cacheRoot, key, expected, result.Binary); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(result.Binary, mode); err != nil {
			t.Fatal(err)
		}
		if hit, err := restoreSharedBinary(cacheRoot, key, expected, result.Binary); err != nil || !hit {
			t.Fatalf("mode repair: hit=%t err=%v", hit, err)
		}
		info, err := os.Stat(result.Binary)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("destination remained non-executable: info=%v err=%v", info, err)
		}
	}
}

func TestSharedBinaryPruneReclaimsAbandonedPublications(t *testing.T) {
	root := t.TempDir()
	artifacts := filepath.Join(root, "artifacts")
	active := filepath.Join(artifacts, ".publish-active")
	orphan := filepath.Join(artifacts, ".publish-abandoned")
	unknown := filepath.Join(artifacts, ".unrelated")
	for _, path := range []string{active, orphan, unknown} {
		writeBuildTestFile(t, path, "application", "partial")
	}
	release, acquired, err := trySharedBinaryLock(filepath.Join(active, ".lease"))
	if err != nil || !acquired {
		t.Fatalf("publisher lease: %t %v", acquired, err)
	}
	defer release()
	// Sparse length exercises accounting without allocating the cache budget.
	if err := os.Truncate(filepath.Join(orphan, "application"), sharedBinaryCacheBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := pruneSharedBinaries(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("abandoned publication escaped pruning: %v", err)
	}
	for _, retained := range []string{active, unknown} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("pruned unowned or active path %s: %v", retained, err)
		}
	}
}
