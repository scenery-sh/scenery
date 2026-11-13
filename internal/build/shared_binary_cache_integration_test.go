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

type sharedBinaryNativeInput struct {
	path, unobservedPath  string
	original, replacement []byte
	before                os.FileInfo
}

func prepareSharedBinaryNativeInput(t *testing.T, dependency, selection string) sharedBinaryNativeInput {
	t.Helper()
	input := sharedBinaryNativeInput{
		path:        filepath.Join(dependency, "dep.go"),
		original:    []byte("package dependency\nconst Value = \"A\"\n"),
		replacement: []byte("package dependency\nconst Value = \"B\"\n"),
	}
	switch selection {
	case "tracked_go":
	case "ignored_go":
		writeBuildTestFile(t, dependency, "dep.go", "package dependency\nvar Value = \"A\"\n")
		input.path = filepath.Join(dependency, "optional.go")
		input.unobservedPath = input.path
		input.original = []byte("//go:build scenery_review_disabled\n\npackage dependency\nfunc init() { Value = \"B\" }\n")
		input.replacement = bytes.Replace(input.original, []byte("scenery_review_disabled"), []byte("!scenery_review_disabled"), 1)
	case "temporary_embed":
		writeBuildTestFile(t, dependency, "dep.go", `package dependency

import "embed"

//go:embed assets
var assets embed.FS

var Value = "A"

func init() {
	if data, err := assets.ReadFile("assets/empty/new.txt"); err == nil {
		Value = string(data)
	}
}
`)
		writeBuildTestFile(t, dependency, "assets/known.txt", "A")
		input.unobservedPath = filepath.Join(dependency, "assets", "empty")
		if err := os.MkdirAll(input.unobservedPath, 0o755); err != nil {
			t.Fatal(err)
		}
		input.path = filepath.Join(input.unobservedPath, "new.txt")
		input.original, input.replacement = nil, []byte("B")
		return input // The changing directory exists before initial discovery.
	default:
		t.Fatalf("unknown native input selection %q", selection)
	}
	if err := os.WriteFile(input.path, input.original, 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	input.before, err = buildInputLstat(input.path)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func (input sharedBinaryNativeInput) restore() error {
	if input.before == nil {
		return os.Remove(input.path)
	}
	if err := os.WriteFile(input.path, input.original, input.before.Mode()); err != nil {
		return err
	}
	return os.Chtimes(input.path, input.before.ModTime(), input.before.ModTime())
}

func (input sharedBinaryNativeInput) assertRestored(t *testing.T) {
	t.Helper()
	if input.before == nil {
		if _, err := os.Lstat(input.path); !os.IsNotExist(err) {
			t.Fatalf("temporary embedded input was not removed: %v", err)
		}
		if entries, err := os.ReadDir(input.unobservedPath); err != nil || len(entries) != 0 {
			t.Fatalf("initially empty embed directory was not preserved: entries=%v err=%v", entries, err)
		}
		return
	}
	after, err := buildInputLstat(input.path)
	if err != nil || buildInputStamp(input.before) != buildInputStamp(after) {
		t.Fatalf("native regression did not restore non-change-time metadata: %v", err)
	}
	if data, err := os.ReadFile(input.path); err != nil || !bytes.Equal(data, input.original) {
		t.Fatalf("native input A bytes were not restored: %v", err)
	}
}

type buildInputInfoWithoutChangeTime struct{ os.FileInfo }

func (info buildInputInfoWithoutChangeTime) Sys() any {
	device, inode := buildInputFileIdentity(info.FileInfo)
	return struct{ Dev, Ino uint64 }{device, inode}
}

func withoutBuildInputChangeTime(t *testing.T) {
	t.Helper()
	previous := buildInputLstat
	t.Cleanup(func() { buildInputLstat = previous })
	buildInputLstat = func(path string) (os.FileInfo, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		return buildInputInfoWithoutChangeTime{info}, nil
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
