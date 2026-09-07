package main

import (
	"bytes"
	"os"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestWorktreeStatusIsReadOnlyForStoppedAndInvalidRecords(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())
	root := t.TempDir()
	paths, err := commandWorktreePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	op, err := paths.BeginOperation()
	if err != nil {
		t.Fatal(err)
	}
	if err := op.SaveRecord(localagent.NewWorktreeRecord(paths, "books")); err != nil {
		t.Fatal(err)
	}
	if err := op.Close(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"stopped", "incompatible-or-invalid"} {
		if expected == "incompatible-or-invalid" {
			if err := os.WriteFile(paths.Record, []byte("not current ownership"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		before, err := os.ReadFile(paths.Record)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := inspectWorktreeOwners(t.Context(), root)
		if err != nil || len(entries) != 1 || entries[0].Status != expected {
			t.Fatalf("status = %+v, %v", entries, err)
		}
		after, err := os.ReadFile(paths.Record)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("status rewrote ownership")
		}
		if _, err := os.Stat(paths.LiveLock); !os.IsNotExist(err) {
			t.Fatalf("status created a live lock: %v", err)
		}
	}
}
