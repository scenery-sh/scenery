package main

import (
	"strings"
	"testing"
)

func TestReportEvidenceHumanSemantics(t *testing.T) {
	t.Parallel()
	const malformed = "Supervisor /owned (partial): read 512 / captured 512 bytes across 1 segments; retained 3, in window 3, invalid 1.\n0 supervisor logs unreadable or read in part; 1 event records with invalid evidence (known outcomes retained).\nBuilds of detached scenery up sessions (1 sessions)\nrebuilds 1 failed 1 timed successes n=0 p50 - p95 -\nrebuild failure 1× owned expected failure\n"
	for _, name := range []string{"malformed-error", "null-error", "null-data"} {
		for change, want := range map[string]string{"": "", "rebuilds 1": "rebuilds 2", "failed 1": "failed 0", "timed successes n=0": "timed successes n=1", "1×": "2×", "rebuild failure 1×": "unstarted/rejected work 1×", "(partial)": "(complete)", "invalid 1": "invalid 0"} {
			output := malformed
			if change != "" {
				output = strings.Replace(output, change, want, 1)
			}
			if err := validateHarnessReportEvidenceHuman(output, name, "/owned", 512, 0); (err == nil) != (change == "") {
				t.Errorf("%s %q: %v", name, change, err)
			}
		}
		if validateHarnessReportEvidenceHuman(strings.ReplaceAll(malformed, "rebuild failure 1× owned expected failure\n", ""), name, "/owned", 512, 0) == nil {
			t.Fatal("missing cause row accepted")
		}
	}
	const coverage = "Transcripts: 1 read, 0 read in part, 0 unreadable; read 512 / captured 512 bytes; 0 invalid and 0 oversized records skipped, 0 results without a call, 0 calls without a result.\nAgents: 1 tool calls, 0 errors; 1 shell commands ran Scenery (1 invocations): 1 recorded Scenery's own outcome (0 failed); as shell commands 0 failed and 0 recorded no outcome\n"
	for _, fixture := range []struct {
		name, sessions, waited, p50 string
		elapsed                     int64
	}{
		{"bare-claude", "1 Claude Code and 0 Codex", "0ms", "0ms", 0},
		{"bare-claude", "1 Claude Code and 0 Codex", "25ms", "25ms", 25},
		{"bare-claude", "1 Claude Code and 0 Codex", "1.25s", "1.25s", 1250},
		{"bare-claude", "1 Claude Code and 0 Codex", "10.0s", "10.0s", 10000},
		{"bare-codex", "0 Claude Code and 1 Codex", "0ms", "-", 25},
	} {
		output := "Sources: 1 CLI records, 0 supervisor logs, " + fixture.sessions + " sessions that ran Scenery.\n" + coverage + "help 1 attributable 1 failed 0 waited " + fixture.waited + " p50 " + fixture.p50 + "\n"
		for change, want := range map[string]string{"": "", "waited " + fixture.waited: "waited unavailable", "p50 " + fixture.p50: "p50 other", "attributable 1": "attributable 0", "recorded no outcome": "lost outcome", "read 512 / captured 512": "read 511 / captured 512"} {
			got := output
			if change != "" {
				got = strings.Replace(got, change, want, 1)
			}
			if err := validateHarnessReportEvidenceHuman(got, fixture.name, "/owned", 512, fixture.elapsed); (err == nil) != (change == "") {
				t.Errorf("%s %d %q: %v", fixture.name, fixture.elapsed, change, err)
			}
		}
	}
}
