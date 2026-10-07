package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

func TestSourceLossWaitsOutAtomicSaveAndPreservesTypedStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		if actualRoot, source := worktreeSourceStatus(root); actualRoot != "present" || source != "missing" {
			t.Fatalf("source-less root = %s/%s", actualRoot, source)
		}
		go func() {
			time.Sleep(watchBackupPollInterval / 2)
			if err := os.WriteFile(filepath.Join(root, "app.scn"), []byte("source"), 0o600); err != nil {
				t.Error(err)
			}
		}()
		if err := requireWatchSource(t.Context(), root); err != nil {
			t.Fatalf("atomic save stopped owner: %v", err)
		}
		if err := os.Remove(filepath.Join(root, "app.scn")); err != nil {
			t.Fatal(err)
		}
		err := requireWatchSource(t.Context(), root)
		var lost *watchSourceLostError
		if !errors.As(err, &lost) || cliExitCode(err) != 3 || cliErrorDiagnostic(err).ReportToken != "" {
			t.Fatalf("permanent source loss = %v", err)
		}
	})
}

func TestPruneInventoryMeasuresFilesWithoutFollowingSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	file := filepath.Join(root, "evidence")
	if err := os.WriteFile(file, []byte("123456"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bytes, complete := worktreeInventoryBytes(root); bytes != 6 || !complete {
		t.Fatalf("inventory = %d/%t", bytes, complete)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if bytes, complete := worktreeInventoryBytes(root); bytes != 6 || complete {
		t.Fatalf("symlink inventory = %d/%t", bytes, complete)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "123456" {
		t.Fatalf("preview changed retained bytes: %q: %v", data, err)
	}
	if opts, err := parsePruneArgs([]string{"--older-than", "1h", "--preview"}); err != nil || !opts.Preview || opts.DB || opts.State {
		t.Fatalf("preview options = %+v: %v", opts, err)
	}
}
