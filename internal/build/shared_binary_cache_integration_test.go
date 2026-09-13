//go:build scenery_build_cache_integration

package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	sharedBinaryIntegrationMode    = "SCENERY_SHARED_BINARY_INTEGRATION_MODE"
	sharedBinaryIntegrationRoot    = "SCENERY_SHARED_BINARY_INTEGRATION_ROOT"
	sharedBinaryIntegrationKey     = "SCENERY_SHARED_BINARY_INTEGRATION_KEY"
	sharedBinaryIntegrationReady   = "SCENERY_SHARED_BINARY_INTEGRATION_READY"
	sharedBinaryIntegrationRelease = "SCENERY_SHARED_BINARY_INTEGRATION_RELEASE"
)

// TestSharedBinaryIntegrationHelper is entered only by the explicit harness
// probe. It makes OS-lock ownership belong to a distinct process so the parent
// can test crash cleanup, cancellation, and fair admission without pretending
// goroutines prove cross-process behavior.
func TestSharedBinaryIntegrationHelper(t *testing.T) {
	mode := os.Getenv(sharedBinaryIntegrationMode)
	if mode == "" {
		t.Skip("helper process only")
	}
	root, key := os.Getenv(sharedBinaryIntegrationRoot), os.Getenv(sharedBinaryIntegrationKey)
	ready, releasePath := os.Getenv(sharedBinaryIntegrationReady), os.Getenv(sharedBinaryIntegrationRelease)
	if root == "" || key == "" || ready == "" {
		t.Fatal("shared binary integration helper is missing its exact scope")
	}
	switch mode {
	case "publisher":
		stage, release, err := createSharedBinaryStageIn(context.Background(), root, filepath.Join(root, "artifacts"), ".publish-")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		writeBuildTestFile(t, stage, "application", "partial executable")
		if err := os.Truncate(filepath.Join(stage, "application"), sharedBinaryCacheBytes+1); err != nil {
			t.Fatal(err)
		}
		writeBuildTestFile(t, filepath.Dir(ready), filepath.Base(ready), stage)
		for {
			time.Sleep(time.Hour)
		}
	case "workspace-writer":
		release, acquired, exists, err := trySharedBinaryExistingLock(filepath.Join(root, ".scenery-workspace.lock"))
		if err != nil || !exists || acquired {
			if acquired {
				release()
			}
			t.Fatalf("producer does not hold workspace: acquired=%t exists=%t err=%v", acquired, exists, err)
		}
		writeBuildTestFile(t, filepath.Dir(ready), filepath.Base(ready), "blocked")
		unlock, err := lockWorkspace(root)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		writeBuildTestFile(t, root, "svc/api.go", "package svc // next generation\n")
	case "subscriber":
		subscriber, err := subscribeSharedBinary(root, key)
		if err != nil {
			t.Fatal(err)
		}
		defer subscriber.Close()
		if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "slot":
		release, err := acquireSharedBinarySlot(context.Background(), root, key)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if err := os.WriteFile(ready, []byte("acquired\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := os.Stat(releasePath); err == nil {
				return
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func TestSharedBinaryCrossProcessLastSubscriberCancellation(t *testing.T) {
	root := t.TempDir()
	key := strings.Repeat("a", 64)
	ready := filepath.Join(root, "subscriber.ready")
	helper := startSharedBinaryIntegrationHelper(t, "subscriber", root, key, ready, "")
	waitSharedBinaryIntegrationPath(t, ready)

	directory := filepath.Join(root, "subscribers", key)
	active, err := activeSharedBinarySubscribers(directory, time.Now())
	if err != nil || active != 1 {
		t.Fatalf("cross-process subscribers = %d, err=%v", active, err)
	}
	producer, stop := sharedBinaryProducerContext(context.Background(), directory)
	defer stop()
	select {
	case <-producer.Done():
		t.Fatal("producer canceled while the helper subscriber held its lease")
	case <-time.After(30 * time.Millisecond):
	}
	killSharedBinaryIntegrationHelper(t, helper)
	select {
	case <-producer.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("producer was not canceled after the crashed last subscriber released its OS lock")
	}
	active, err = activeSharedBinarySubscribers(directory, time.Now())
	if err != nil || active != 0 {
		t.Fatalf("crashed subscriber cleanup = %d, err=%v", active, err)
	}
	if _, err := os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatalf("crashed subscriber directory remains: %v", err)
	}
}

func TestSharedBinaryCrossProcessSlotsAreBoundedAndFair(t *testing.T) {
	root := t.TempDir()
	key := strings.Repeat("b", 64)
	type contender struct {
		command  *sharedBinaryIntegrationCommand
		acquired string
		release  string
	}
	start := func(index int) contender {
		acquired := filepath.Join(root, fmt.Sprintf("slot-%d.acquired", index))
		release := filepath.Join(root, fmt.Sprintf("slot-%d.release", index))
		return contender{command: startSharedBinaryIntegrationHelper(t, "slot", root, key, acquired, release), acquired: acquired, release: release}
	}
	release := func(item contender) {
		if err := os.WriteFile(item.release, []byte("release\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		waitSharedBinaryIntegrationExit(t, item.command)
	}

	first, second := start(1), start(2)
	waitSharedBinaryIntegrationPath(t, first.acquired)
	waitSharedBinaryIntegrationPath(t, second.acquired)
	third := start(3)
	waitSharedBinaryIntegrationTicket(t, root, third.command.command.Process.Pid)
	fourth := start(4)
	waitSharedBinaryIntegrationTicket(t, root, fourth.command.command.Process.Pid)
	if pathExists(third.acquired) || pathExists(fourth.acquired) {
		t.Fatal("more than two cross-process link contenders acquired slots")
	}

	if err := os.WriteFile(first.release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitSharedBinaryIntegrationPath(t, third.acquired)
	if pathExists(fourth.acquired) {
		t.Fatal("newer contender overtook the oldest queued ticket")
	}
	waitSharedBinaryIntegrationExit(t, first.command)
	if err := os.WriteFile(second.release, []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitSharedBinaryIntegrationPath(t, fourth.acquired)
	waitSharedBinaryIntegrationExit(t, second.command)
	release(third)
	release(fourth)
}

func TestSharedBinaryCrossProcessPublicationCrashRecovery(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "publisher.ready")
	helper := startSharedBinaryIntegrationHelper(t, "publisher", root, strings.Repeat("c", 64), ready, "")
	waitSharedBinaryIntegrationPath(t, ready)
	data, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	stage := string(data)
	if filepath.Dir(stage) != filepath.Join(root, "artifacts") || !strings.HasPrefix(filepath.Base(stage), ".publish-") {
		t.Fatalf("publisher stage outside owned root: %q", stage)
	}
	if err := pruneSharedBinaries(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, "application")); err != nil {
		t.Fatalf("live publisher was reclaimed: %v", err)
	}
	killSharedBinaryIntegrationHelper(t, helper)
	if err := pruneSharedBinaries(root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !os.IsNotExist(err) {
		t.Fatalf("crashed publication escaped retention: %v", err)
	}
}

func TestSharedBinaryCrossProcessCanceledProducerInputLease(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	appRoot, result := newCachedBuildTestWorkspace(t, "process-input-lease")
	prepareSharedBinaryTestResult(appRoot, result)
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	key, expected, err := sharedBinaryKey(result)
	if err != nil {
		t.Fatal(err)
	}
	other, err := subscribeSharedBinary(cacheRoot, key)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	original, err := os.ReadFile(filepath.Join(result.Dir, "svc/api.go"))
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	restore := SetGoRunnerForTesting(func(_ context.Context, dir string, args ...string) error {
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go command: %v", args)
		}
		close(started)
		<-release
		data, err := os.ReadFile(filepath.Join(dir, "svc/api.go"))
		if err != nil {
			return err
		}
		return os.WriteFile(output, data, 0o755)
	})
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		unlock, err := lockWorkspace(result.Dir)
		if err == nil {
			err = runSharedBinaryTestBuild(ctx, result)
			unlock()
		}
		done <- err
	}()
	<-started
	cancel()
	// Wait for the subscriber to leave, not for the still-borrowing producer.
	deadline := time.Now().Add(time.Second)
	for {
		active, err := activeSharedBinarySubscribers(other.directory, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled subscriber remained registered")
		}
		time.Sleep(time.Millisecond)
	}
	ready := filepath.Join(t.TempDir(), "writer.ready")
	writer := startSharedBinaryIntegrationHelper(t, "workspace-writer", result.Dir, key, ready, "")
	waitSharedBinaryIntegrationPath(t, ready)
	releaseOnce.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled producer: %v", err)
	}
	waitSharedBinaryIntegrationExit(t, writer)
	artifact, data, hit, err := loadSharedBinary(cacheRoot, key)
	if err != nil || !hit || !sharedBinaryMetadataEqual(artifact, expected) || string(data) != string(original) {
		t.Fatalf("producer consumed replacement workspace: hit=%t data=%q err=%v", hit, data, err)
	}
	current, err := os.ReadFile(filepath.Join(result.Dir, "svc/api.go"))
	if err != nil || bytes.Equal(current, original) {
		t.Fatalf("next materializer never acquired released workspace: %v", err)
	}
}

func TestSharedBinaryCrossProcessRejectsChangedNativeInputs(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	appRoot, result := newCachedBuildTestWorkspace(t, "native-input-proof")
	dependency := t.TempDir()
	writeBuildTestFile(t, dependency, "go.mod", "module example.test/dependency\n\ngo 1.27.0\n")
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"A\"\n")
	writeBuildTestFile(t, result.Dir, "go.mod", "module example.com/buildtest\n\ngo 1.27.0\n\nrequire example.test/dependency v0.0.0\nreplace example.test/dependency => "+dependency+"\n")
	writeBuildTestFile(t, result.Dir, "scenery_internal_main/main.go", "package main\nimport (\"fmt\"; \"example.test/dependency\")\nfunc main() { fmt.Print(dependency.Value) }\n")
	prepareSharedBinaryTestResult(appRoot, result)
	if err := refreshWorkspaceBuildIdentity(result); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := prepareRuntimeBundle(ctx, result); err != nil {
		t.Fatal(err)
	}
	key, _, err := sharedBinaryKey(result)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := sharedBinaryRoot()
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	builds := 0
	oldGo := runGo
	defer func() { runGo = oldGo }()
	runGo = func(ctx context.Context, dir string, env []string, args ...string) error {
		builds++
		if builds == 1 {
			close(started)
			<-release
		}
		if err := runRealGo(ctx, dir, env, args...); err != nil {
			return err
		}
		output, ok := fakeGoBuildOutput(args)
		if !ok {
			return fmt.Errorf("unexpected Go invocation: %v", args)
		}
		data, err := exec.CommandContext(ctx, output).Output()
		want := "A"
		if builds == 1 {
			want = "B"
		}
		if err != nil || string(data) != want {
			return fmt.Errorf("native binary did not consume %s: output=%q err=%w", want, data, err)
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- runSharedGoBuildContext(ctx, result) }()
	<-started
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"B\"\n")
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err == nil || !strings.Contains(err.Error(), "go build inputs changed") {
		t.Fatalf("changed native inputs were not rejected: %v", err)
	}
	if _, _, hit, err := loadSharedBinary(cacheRoot, key); err != nil || hit {
		t.Fatalf("rejected native binary was published: hit=%t err=%v", hit, err)
	}
	writeBuildTestFile(t, dependency, "dep.go", "package dependency\nconst Value = \"A\"\n")
	for range 2 {
		if err := prepareRuntimeBundle(ctx, result); err != nil {
			t.Fatal(err)
		}
		if err := runSharedGoBuildContext(ctx, result); err != nil {
			t.Fatal(err)
		}
	}
	// A hit is not permission to skip live discovery. Newly appearing Go
	// membership in a local replacement must invalidate that caller's proof.
	writeBuildTestFile(t, dependency, "added.go", "package dependency\nconst Added = true\n")
	if err := runSharedGoBuildContext(ctx, result); err == nil || !strings.Contains(err.Error(), "go build inputs changed") {
		t.Fatalf("cache hit skipped current dependency membership: %v", err)
	}
	if builds != 2 {
		t.Fatalf("rejected retry/control build count = %d, want 2", builds)
	}
}

type sharedBinaryIntegrationCommand struct {
	command *exec.Cmd
	output  *bytes.Buffer
}

func startSharedBinaryIntegrationHelper(t *testing.T, mode, root, key, ready, release string) *sharedBinaryIntegrationCommand {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestSharedBinaryIntegrationHelper$", "-test.v")
	command.Env = append(os.Environ(),
		sharedBinaryIntegrationMode+"="+mode,
		sharedBinaryIntegrationRoot+"="+root,
		sharedBinaryIntegrationKey+"="+key,
		sharedBinaryIntegrationReady+"="+ready,
		sharedBinaryIntegrationRelease+"="+release,
	)
	output := &bytes.Buffer{}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	item := &sharedBinaryIntegrationCommand{command: command, output: output}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	return item
}

func killSharedBinaryIntegrationHelper(t *testing.T, item *sharedBinaryIntegrationCommand) {
	t.Helper()
	if err := item.command.Process.Kill(); err != nil {
		t.Fatalf("kill integration helper: %v: %s", err, item.output.String())
	}
	_ = item.command.Wait()
	item.command.Process = nil
}

func waitSharedBinaryIntegrationExit(t *testing.T, item *sharedBinaryIntegrationCommand) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- item.command.Wait() }()
	select {
	case err := <-done:
		item.command.Process = nil
		if err != nil {
			t.Fatalf("integration helper failed: %v: %s", err, item.output.String())
		}
	case <-time.After(2 * time.Second):
		_ = item.command.Process.Kill()
		<-done
		item.command.Process = nil
		t.Fatalf("integration helper did not exit: %s", item.output.String())
	}
}

func waitSharedBinaryIntegrationPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitSharedBinaryIntegrationTicket(t *testing.T, root string, pid int) {
	t.Helper()
	needle := fmt.Sprintf("-%010d-", pid)
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := os.ReadDir(filepath.Join(root, "queue"))
		if err == nil {
			for _, entry := range entries {
				if strings.Contains(entry.Name(), needle) {
					return
				}
			}
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for ticket owned by pid %d", pid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
