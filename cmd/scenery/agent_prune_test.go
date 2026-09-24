package main

import (
	"strings"
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
	if defaults.BuildCache || all.BuildCache || db.BuildCache || state.BuildCache {
		t.Fatal("build cache cleanup must stay explicit")
	}

	buildCache, err := parsePruneArgs([]string{"--older-than", "14d", "--build-cache"})
	if err != nil {
		t.Fatal(err)
	}
	if !buildCache.BuildCache || buildCache.DB || buildCache.State || buildCache.All {
		t.Fatalf("build cache prune options = %+v", buildCache)
	}
}

// A mistyped or missing age is the caller's mistake, so it must name what to
// write instead of an opaque internal failure report.
func TestParsePruneArgsReportsAnUnusableAgeAsAnInvalidRequest(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"--older-than", "0s"}, {"--older-than", "bogus"}, {"--older-than", "0d"}} {
		_, err := parsePruneArgs(args)
		if err == nil {
			t.Fatalf("prune %v was accepted", args)
		}
		diagnostic := cliErrorDiagnostic(err)
		if cliExitCode(err) != 2 || diagnostic.ReportToken != "" || !strings.HasPrefix(diagnostic.Code, "SCN8") {
			t.Fatalf("prune %v: exit=%d diagnostic=%+v", args, cliExitCode(err), diagnostic)
		}
		if !strings.Contains(diagnostic.Message, "older-than") {
			t.Fatalf("prune %v does not name the flag: %q", args, diagnostic.Message)
		}
	}
}
