//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package vnext

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSnapshotWorkspaceFilesRejectsNonRegularEntry(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "unexpected.scn"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := snapshotWorkspaceFiles(root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("snapshot error = %v, want non-regular entry rejection", err)
	}
}
