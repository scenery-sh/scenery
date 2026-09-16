//go:build scenery_build_cache_integration

package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Closing the owner of a recipe recording ends every process the recording
// started, including tools the Go command runs through its recorder, before
// Close returns and the recording's slot, lease and directory are released.
func TestRetainedRecordingCrossProcessCloseStopsItsToolTree(t *testing.T) {
	t.Setenv("SCENERY_DEV_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"go.mod":  "module example.com/recording\n\ngo 1.23\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The recorder stands in for Scenery's toolexec recorder: it reports its own
	// process and a tool it started, and blocks the tool, as a long compile
	// would.
	pids := filepath.Join(root, "pids")
	recorder := filepath.Join(root, "recorder")
	script := "#!/bin/sh\necho $$ >> " + strconv.Quote(pids) + "\nsleep 60 &\necho $! >> " + strconv.Quote(pids) + "\nwait\n"
	if err := os.WriteFile(recorder, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	targetRoot := filepath.Join(root, "processes", "targets", "recording")
	target := retainedProcessTarget{name: "recording", pattern: ".", output: filepath.Join(root, "recording")}

	work := NewBackgroundWork(context.Background())
	recorded := make(chan error, 1)
	if !startBackgroundWork(WithBackgroundWork(context.Background(), work), func(ctx context.Context) {
		recorded <- captureRetainedProcessRecipe(ctx, workspace, environment, nil, targetRoot, filepath.Join(root, "processes", "shared"), recorder, target)
	}) {
		t.Fatal("the recording was not admitted as owned background work")
	}
	var observed []int
	for deadline := time.Now().Add(time.Minute); len(observed) < 2; {
		if time.Now().After(deadline) {
			t.Fatalf("the recorder never started a tool; observed %v", observed)
		}
		data, _ := os.ReadFile(pids)
		observed = observed[:0]
		for _, line := range strings.Fields(string(data)) {
			if pid, err := strconv.Atoi(line); err == nil {
				observed = append(observed, pid)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	work.Close()
	if err := <-recorded; err == nil {
		t.Fatal("a closed recording reported success")
	}
	for _, pid := range observed {
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("recording process %d outlived its closed owner: %v", pid, err)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	entries, err := os.ReadDir(targetRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "recipe-") {
			t.Errorf("a closed recording left its directory %s", entry.Name())
		}
	}
}
