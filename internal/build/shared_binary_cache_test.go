package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
)

func TestSharedDevelopmentBinaryReusesExactArtifactAndRepairsCorruption(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	firstRoot, first := newCachedBuildTestWorkspace(t, "first")
	prepareSharedBinaryTestResult(firstRoot, first)
	second := cloneSharedBinaryTestResult(first, filepath.Join(t.TempDir(), "second"))
	var builds atomic.Int32
	restore := SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected go command: %s", strings.Join(args, " "))
		}
		builds.Add(1)
		return os.WriteFile(output, []byte("shared-executable"), 0o755)
	})
	t.Cleanup(restore)
	if err := runSharedGoBuildContext(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := runSharedGoBuildContext(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 {
		t.Fatalf("exact artifact linked %d times, want one", builds.Load())
	}
	data, err := os.ReadFile(second.Binary)
	if err != nil || string(data) != "shared-executable" {
		t.Fatalf("restored binary = %q, err=%v", data, err)
	}
	before, err := os.Stat(second.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runSharedGoBuildContext(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(second.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("exact destination was replaced or relinked: builds=%d same_file=%t before=%s after=%s", builds.Load(), os.SameFile(before, after), before.ModTime(), after.ModTime())
	}
	key, _, err := sharedBinaryKey(first)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cacheRoot, "artifacts", key)); err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{first.Binary, second.Binary} {
		data, err := os.ReadFile(retained)
		if err != nil || string(data) != "shared-executable" {
			t.Fatalf("cache reclamation invalidated retained executable %s: %q err=%v", retained, data, err)
		}
	}
	third := cloneSharedBinaryTestResult(first, filepath.Join(t.TempDir(), "third"))
	if err := runSharedGoBuildContext(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 2 {
		t.Fatalf("reclaimed artifact did not rebuild exactly once: builds=%d", builds.Load())
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "artifacts", key, "application"), []byte("corrupt"), 0o755); err != nil {
		t.Fatal(err)
	}
	fourth := cloneSharedBinaryTestResult(first, filepath.Join(t.TempDir(), "fourth"))
	if err := runSharedGoBuildContext(context.Background(), fourth); err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 3 {
		t.Fatalf("corrupt artifact did not rebuild exactly once: builds=%d", builds.Load())
	}
}

func TestSharedDevelopmentBinaryDeduplicatesInflightAndDetachesCanceledWaiter(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, first := newCachedBuildTestWorkspace(t, "inflight")
	prepareSharedBinaryTestResult(root, first)
	second := cloneSharedBinaryTestResult(first, filepath.Join(t.TempDir(), "second"))
	third := cloneSharedBinaryTestResult(first, filepath.Join(t.TempDir(), "third"))
	started, releaseBuild := make(chan struct{}), make(chan struct{})
	var builds atomic.Int32
	restore := SetGoRunnerForTesting(func(ctx context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected go command: %s", strings.Join(args, " "))
		}
		if builds.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-releaseBuild:
		}
		return os.WriteFile(output, []byte("inflight-executable"), 0o755)
	})
	t.Cleanup(restore)
	firstDone := make(chan error, 1)
	go func() { firstDone <- runSharedGoBuildContext(context.Background(), first) }()
	<-started
	secondDone := make(chan error, 1)
	go func() { secondDone <- runSharedGoBuildContext(context.Background(), second) }()
	canceledCtx, cancel := context.WithCancel(context.Background())
	thirdDone := make(chan error, 1)
	go func() { thirdDone <- runSharedGoBuildContext(canceledCtx, third) }()
	cancel()
	if err := <-thirdDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter = %v, want context canceled", err)
	}
	close(releaseBuild)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 {
		t.Fatalf("identical inflight artifact linked %d times", builds.Load())
	}
}

func TestSharedDevelopmentBinaryProducerCancellationKeepsSubscribedBuild(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, first := newCachedBuildTestWorkspace(t, "producer-cancel")
	prepareSharedBinaryTestResult(root, first)
	second := cloneSharedBinaryTestResult(first, filepath.Join(t.TempDir(), "second"))
	started, releaseBuild, buildCanceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var builds atomic.Int32
	restore := SetGoRunnerForTesting(func(ctx context.Context, _ string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected go command: %s", strings.Join(args, " "))
		}
		if builds.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			close(buildCanceled)
			return ctx.Err()
		case <-releaseBuild:
		}
		return os.WriteFile(output, []byte("retained-subscriber-executable"), 0o755)
	})
	t.Cleanup(restore)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- runSharedGoBuildContext(firstCtx, first) }()
	<-started
	secondDone := make(chan error, 1)
	go func() { secondDone <- runSharedGoBuildContext(context.Background(), second) }()

	key, _, err := sharedBinaryKey(first)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	subscriberDirectory := filepath.Join(cacheRoot, "subscribers", key)
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		active, activeErr := activeSharedBinarySubscribers(subscriberDirectory, time.Now())
		if activeErr != nil {
			t.Fatal(activeErr)
		}
		if active == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("active subscribers = %d, want 2", active)
		}
		time.Sleep(time.Millisecond)
	}

	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("producer caller cancellation = %v", err)
	}
	select {
	case <-buildCanceled:
		t.Fatal("producer work was canceled while another subscriber remained")
	default:
	}
	close(releaseBuild)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 {
		t.Fatalf("retained subscriber caused %d builds, want one", builds.Load())
	}
	data, err := os.ReadFile(second.Binary)
	if err != nil || string(data) != "retained-subscriber-executable" {
		t.Fatalf("subscriber binary = %q, err=%v", data, err)
	}
}

func TestSharedDevelopmentBinaryLastSubscriberCancelsProducer(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root, result := newCachedBuildTestWorkspace(t, "last-subscriber")
	prepareSharedBinaryTestResult(root, result)
	started, buildCanceled := make(chan struct{}), make(chan struct{})
	restore := SetGoRunnerForTesting(func(ctx context.Context, _ string, args ...string) error {
		if _, ok := fakeGoBuildOutput(args); !ok {
			return fmt.Errorf("unexpected go command: %s", strings.Join(args, " "))
		}
		close(started)
		<-ctx.Done()
		close(buildCanceled)
		return ctx.Err()
	})
	t.Cleanup(restore)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runSharedGoBuildContext(ctx, result) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("last subscriber cancellation = %v", err)
	}
	select {
	case <-buildCanceled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("producer was not canceled after its last subscriber left")
	}
	key, _, err := sharedBinaryKey(result)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := loadSharedBinary(cacheRoot, key); err != nil || ok {
		t.Fatalf("canceled producer artifact: ok=%v err=%v", ok, err)
	}
}

func TestSharedBinaryKeyRetainsWorktreeSensitiveModuleRoot(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	firstRoot, first := newCachedBuildTestWorkspace(t, "first-root")
	secondRoot, second := newCachedBuildTestWorkspace(t, "second-root")
	prepareSharedBinaryTestResult(firstRoot, first)
	prepareSharedBinaryTestResult(secondRoot, second)
	firstKey, _, err := sharedBinaryKey(first)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, _, err := sharedBinaryKey(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey == secondKey {
		t.Fatal("path-sensitive worktrees received one executable cache key")
	}
}

func TestSharedBinaryWaitIsContextCancelable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "action.lock")
	release, acquired, err := trySharedBinaryLock(path)
	if err != nil || !acquired {
		t.Fatalf("hold lock: acquired=%v err=%v", acquired, err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := acquireSharedBinaryLock(ctx, path); err == nil || ctx.Err() == nil {
		t.Fatalf("blocked subscriber was not canceled: %v", err)
	}
}

func TestSharedBinaryExistingLockDoesNotRecreateMissingLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vanished.subscriber")
	release, acquired, exists, err := trySharedBinaryExistingLock(path)
	if err != nil || exists || acquired || release != nil {
		t.Fatalf("missing lease: exists=%v acquired=%v release=%v err=%v", exists, acquired, release != nil, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing lease was recreated: %v", err)
	}
}

func TestSharedBinarySubscriberScanReclaimsUnlockedLease(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "subscribers")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "stale.subscriber")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	active, err := activeSharedBinarySubscribers(directory, time.Now())
	if err != nil || active != 0 {
		t.Fatalf("active subscribers = %d, err=%v", active, err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale subscriber remains: %v", err)
	}
}

func TestSharedBinaryTicketQueueOrdersActiveAndReclaimsStale(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "queue")
	firstPath, releaseFirst, err := createSharedBinaryLease(directory, "00000000000000000001-0000000001-00000000000000000001.ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		releaseFirst()
		_ = os.Remove(firstPath)
	}()
	secondPath, releaseSecond, err := createSharedBinaryLease(directory, "00000000000000000002-0000000001-00000000000000000002.ticket")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		releaseSecond()
		_ = os.Remove(secondPath)
	}()
	stale := filepath.Join(directory, "00000000000000000000-0000000001-00000000000000000000.ticket")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	position, err := sharedBinaryTicketPosition(directory, filepath.Base(secondPath))
	if err != nil || position != 1 {
		t.Fatalf("second ticket position = %d, err=%v", position, err)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale ticket remains: %v", err)
	}
}

func TestSharedBinaryStageCleanupPreservesActiveAndRemovesCrashed(t *testing.T) {
	root := t.TempDir()
	active, releaseActive, err := createSharedBinaryStage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		releaseActive()
		_ = os.RemoveAll(active)
	}()
	stale := filepath.Join(root, "staging", ".build-crashed")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, ".lease"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cleanupSharedBinaryStages(filepath.Join(root, "staging")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active stage removed: %v", err)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("crashed stage remains: %v", err)
	}
}

func TestSharedBinaryPruneBoundsEntriesAndPreservesCurrentArtifact(t *testing.T) {
	root := t.TempDir()
	artifacts := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := strings.Repeat("f", 64)
	for index := 0; index < sharedBinaryCacheEntries+2; index++ {
		key := fmt.Sprintf("%064x", index)
		if index == 0 {
			key = keep
		}
		directory := filepath.Join(artifacts, key)
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "application"), []byte{byte(index)}, 0o755); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(index+1), 0)
		if err := os.Chtimes(directory, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneSharedBinaries(root, keep); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != sharedBinaryCacheEntries {
		t.Fatalf("retained entries = %d, want %d", len(entries), sharedBinaryCacheEntries)
	}
	if _, err := os.Stat(filepath.Join(artifacts, keep, "application")); err != nil {
		t.Fatalf("current artifact was pruned: %v", err)
	}
}

func prepareSharedBinaryTestResult(root string, result *Result) {
	result.AppRoot = root
	result.Target.Context.ModuleRoot = root
	result.BuildInput = newBuildInputManifest(result.Target.Name, map[string]string{"fixture": "sha256:" + strings.Repeat("b", 64)})
	result.ImplementationRevisions, _ = compiler.ComputeImplementationRevisions(result.Contract, map[string]string{result.Target.Name: result.BuildInput.Digest})
	result.RuntimeLinkerMetadata = map[string]string{
		"scenery.sh/runtime.linkedContractRevision":       result.Contract.Manifest.ContractRevision,
		"scenery.sh/runtime.linkedImplementationRevision": result.ImplementationRevisions[result.Target.Name],
		"scenery.sh/runtime.linkedBuildInputDigest":       result.BuildInput.Digest,
		"scenery.sh/runtime.linkedGoTarget":               result.Target.Name,
	}
}

func cloneSharedBinaryTestResult(source *Result, binary string) *Result {
	clone := *source
	target := *source.Target
	clone.Target = &target
	clone.Binary = binary
	clone.RuntimeLinkerMetadata = map[string]string{}
	for key, value := range source.RuntimeLinkerMetadata {
		clone.RuntimeLinkerMetadata[key] = value
	}
	return &clone
}
