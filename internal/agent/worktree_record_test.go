package agent

import (
	"bytes"
	"os"
	"testing"
)

func TestWorktreeRecordPreservesOwnership(t *testing.T) {
	p, err := PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	op, err := p.BeginOperation()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.Close() })
	record := NewWorktreeRecord(p, "books")
	if err := op.SaveRecord(record); err != nil {
		t.Fatal(err)
	}
	loaded, err := p.LoadRecord("books")
	if err != nil || loaded.AppRoot != p.AppRoot {
		t.Fatalf("read ownership: %v", err)
	}
	loaded.RouterAddress = "127.0.0.1:12345"
	if err := op.SaveRecord(loaded); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p.Record)
	if err != nil {
		t.Fatal(err)
	}
	loaded.AppID = "other"
	if err := op.SaveRecord(loaded); err == nil {
		t.Fatal("application identity replacement accepted")
	}
	after, err := os.ReadFile(p.Record)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected replacement changed retained state")
	}
}

func TestWorktreeRecordDecodeFailureIsReadOnly(t *testing.T) {
	p, err := PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	op, err := p.BeginOperation()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.Close() })
	broken := []byte(`{"kind":"scenery.worktree","spec_revision":"incompatible"}`)
	if err := os.WriteFile(p.Record, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadRecord("books"); err == nil {
		t.Fatal("incompatible record accepted")
	}
	if err := op.SaveRecord(NewWorktreeRecord(p, "books")); err == nil {
		t.Fatal("incompatible record replaced")
	}
	got, err := os.ReadFile(p.Record)
	if err != nil || !bytes.Equal(got, broken) {
		t.Fatal("failed decode modified state")
	}
}
