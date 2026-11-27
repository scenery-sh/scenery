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
			if err := validateHarnessReportEvidenceHuman(output, name, "/owned", 512); (err == nil) != (change == "") {
				t.Errorf("%s %q: %v", name, change, err)
			}
		}
		if validateHarnessReportEvidenceHuman(strings.ReplaceAll(malformed, "rebuild failure 1× owned expected failure\n", ""), name, "/owned", 512) == nil {
			t.Fatal("missing cause row accepted")
		}
	}
	const native = "Sources: 1 CLI records, 0 supervisor logs.\nCLI commands (1 records, 0 failed, 0 uptime)\n  caller claude: 1\n"
	for change, want := range map[string]string{"": "", "caller claude: 1": "caller codex: 1", "1 CLI records": "2 CLI records"} {
		got := native
		if change != "" {
			got = strings.Replace(got, change, want, 1)
		}
		if err := validateHarnessReportEvidenceHuman(got, "bare-native", "/owned", 128); (err == nil) != (change == "") {
			t.Errorf("bare-native %q: %v", change, err)
		}
	}
}
