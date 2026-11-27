package main

import (
	"errors"
	"fmt"
	"strings"
)

// Require whole semantic rows for these owned fixtures. Table padding may vary;
// counts, cause identity and measured versus unavailable timing may not.
func validateHarnessReportEvidenceHuman(output, name, path string, size int64) error {
	var expected []string
	switch name {
	case "malformed-error", "null-error", "null-data":
		expected = []string{
			fmt.Sprintf("Supervisor %s (partial): read %d / captured %d bytes across 1 segments; retained 3, in window 3, invalid 1.", path, size, size),
			"0 supervisor logs unreadable or read in part; 1 event records with invalid evidence (known outcomes retained).",
			"Builds of detached scenery up sessions (1 sessions)",
			"rebuilds 1 failed 1 timed successes n=0 p50 - p95 -",
			"rebuild failure 1× owned expected failure",
		}
	case "bare-native":
		expected = []string{
			"Sources: 1 CLI records, 0 supervisor logs.",
			"CLI commands (1 records, 0 failed, 0 uptime)",
			"caller claude: 1",
		}
	default:
		return errors.New("unknown report evidence human fixture")
	}
	lines := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		lines[strings.Join(strings.Fields(line), " ")] = true
	}
	for _, want := range expected {
		if !lines[strings.Join(strings.Fields(want), " ")] {
			return errors.New("human report source, outcome, cause or timing differs")
		}
	}
	return nil
}
