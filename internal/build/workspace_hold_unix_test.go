//go:build unix

package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	appcfg "scenery.sh/internal/app"
)

// A held refresh keeps the workspace lock under which it established the
// workspace, so no other preparation can change it before the process build
// consumes the hold; releasing an unconsumed hold frees the workspace.
func TestHeldRefreshKeepsTheWorkspaceLockUntilReleased(t *testing.T) {
	original := currentGeneratorFingerprint
	currentGeneratorFingerprint = func() (string, error) { return "fixture-generator", nil }
	t.Cleanup(func() { currentGeneratorFingerprint = original })

	appDir, _ := newCachedBuildTestWorkspace(t, "graph-1")
	writeBuildTestFile(t, appDir, "svc/helper.go", "package svc\n\nfunc helper() {}\n")
	cached, ok, err := LoadCachedGraph(appDir, appcfg.Config{Name: "buildtest"}, "graph-1")
	if err != nil || !ok {
		t.Fatalf("LoadCachedGraph() = %v, %v", ok, err)
	}
	locked := func() bool {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(cached.Result.Dir, ".scenery-workspace.lock"), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			return false
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			t.Fatal(err)
		}
		return true
	}
	prepared, err := PrepareCachedWorkspaceHeldContext(context.Background(), appDir, appcfg.Config{}, cached.Result, nil)
	if err != nil || !prepared {
		t.Fatalf("PrepareCachedWorkspaceHeldContext() = %v, %v", prepared, err)
	}
	if !locked() {
		t.Fatal("a prepared held refresh released the workspace lock")
	}
	cached.Result.ReleaseWorkspace()
	cached.Result.ReleaseWorkspace()
	if locked() {
		t.Fatal("ReleaseWorkspace left the workspace locked")
	}
	if hold := cached.Result.takeWorkspaceHold(); hold != nil {
		t.Fatal("a released hold was handed over again")
	}
}
