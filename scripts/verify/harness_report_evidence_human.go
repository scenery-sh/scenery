package main

import (
	"errors"
	"fmt"
	"strings"
)

// Require whole semantic rows for these owned fixtures. Table padding may vary;
// counts, cause identity and measured versus unavailable timing may not.
func validateHarnessReportEvidenceHuman(output, name, path string, size, elapsed int64) error {
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
	case "bare-claude", "bare-codex":
		claude, codex, waited, percentile := 0, 1, "0ms", "-"
		if name == "bare-claude" {
			claude, codex = 1, 0
			waited = harnessReportHumanDuration(elapsed)
			percentile = waited
		}
		expected = []string{
			fmt.Sprintf("Sources: 1 CLI records, 0 supervisor logs, %d Claude Code and %d Codex sessions that ran Scenery.", claude, codex),
			fmt.Sprintf("Transcripts: 1 read, 0 read in part, 0 unreadable; read %d / captured %d bytes; 0 invalid and 0 oversized records skipped, 0 results without a call, 0 calls without a result.", size, size),
			"Agents: 1 tool calls, 0 errors; 1 shell commands ran Scenery (1 invocations): 1 recorded Scenery's own outcome (0 failed); as shell commands 0 failed and 0 recorded no outcome",
			fmt.Sprintf("help 1 attributable 1 failed 0 waited %s p50 %s", waited, percentile),
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

func harnessReportHumanDuration(ms int64) string {
	if ms >= 10000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dms", ms)
}
