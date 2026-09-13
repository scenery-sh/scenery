//go:build scenery_build_cache_integration

package build

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
