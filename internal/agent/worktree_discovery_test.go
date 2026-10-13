package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverWorktreesPreservesCanonicalInvalidEntries(t *testing.T) {
	t.Parallel()
	home, root := t.TempDir(), t.TempDir()
	paths, err := PathsForWorktree(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(NewWorktreeRecord(paths, "discovery-test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Record, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	// Distinct letters keep these fixtures distinct on case-insensitive hosts.
	names := []string{strings.Repeat("b", 64), strings.Repeat("A", 64), strings.Repeat("C", 63) + "d", "not-a-key"}
	before := map[string][]byte{paths.Record: encoded}
	for _, name := range names {
		path := filepath.Join(home, "worktrees", name)
		data := []byte("invalid owned locator\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		before[path] = data
	}
	for _, corrupt := range []bool{false, true} {
		if corrupt {
			before[paths.Record] = []byte("corrupt ownership\n")
			if err := os.WriteFile(paths.Record, before[paths.Record], 0o600); err != nil {
				t.Fatal(err)
			}
		}
		found, err := DiscoverWorktrees(home)
		if err != nil || len(found) != 2 {
			t.Fatalf("discovery = %+v, %v; want two canonical entries", found, err)
		}
		byKey := map[string]WorktreeDiscovery{}
		for _, item := range found {
			byKey[item.Key] = item
		}
		if byKey[names[0]].Status != "incompatible-or-invalid" {
			t.Fatalf("canonical invalid locator was hidden: %+v", found)
		}
		want := "retained"
		if corrupt {
			want = "incompatible-or-invalid"
		}
		if byKey[paths.Key].Status != want {
			t.Fatalf("canonical authority status = %+v, want %s", byKey[paths.Key], want)
		}
		for path, data := range before {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, after) {
				t.Fatalf("discovery changed %s: %v", path, err)
			}
		}
		if _, err := os.Lstat(paths.LiveLock); !os.IsNotExist(err) {
			t.Fatalf("discovery allocated owner lock: %v", err)
		}
	}
}
