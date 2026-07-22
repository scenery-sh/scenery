package main

import (
	"testing"
	"time"
)

func TestParsePruneArgsKeepsDestructiveCleanupExplicit(t *testing.T) {
	t.Parallel()

	defaults, err := parsePruneArgs([]string{"--older-than", "14d"})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.DB || defaults.State || defaults.All {
		t.Fatalf("default prune options = %+v, want registry-only cleanup", defaults)
	}
	if defaults.OlderThan != 14*24*time.Hour {
		t.Fatalf("older than = %s", defaults.OlderThan)
	}

	all, err := parsePruneArgs([]string{"--older-than=336h", "--all"})
	if err != nil {
		t.Fatal(err)
	}
	if !all.All {
		t.Fatalf("all prune options = %+v", all)
	}

	db, err := parsePruneArgs([]string{"--older-than", "2h", "--db"})
	if err != nil {
		t.Fatal(err)
	}
	if !db.DB || db.State || db.All {
		t.Fatalf("db prune options = %+v", db)
	}

	state, err := parsePruneArgs([]string{"--older-than", "2h", "--state"})
	if err != nil {
		t.Fatal(err)
	}
	if !state.State || state.DB || state.All {
		t.Fatalf("state prune options = %+v", state)
	}
}
