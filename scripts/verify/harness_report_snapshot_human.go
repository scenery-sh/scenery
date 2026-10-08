package main

import (
	"errors"
	"fmt"
	"strings"
)

// Bind each fixture's physical coverage and status to its owning source line.
// These exact fixture expectations are not a general human-output parser.
func validateHarnessReportHuman(output, name, path string, read, size int64, rowSize int) error {
	var source, outcome string
	prefix := false
	switch name {
	case "active-cli-append", "archived-cli-append":
		n := size / int64(rowSize)
		source = fmt.Sprintf("  CLI %s (complete, archived=%t): read %d / captured %d bytes; retained %d, in window %d, duplicate invocations 0, invalid 0; ", path, name == "archived-cli-append", read, size, n, n)
		prefix = true // first/last wall timestamps are fixture-specific
		outcome = fmt.Sprintf("help %d failed 0 timed successes n=%d p50 0ms p95 0ms", n, n)
		if !strings.Contains(output, fmt.Sprintf("\nCLI commands (%d records, 0 failed)\n", n)) {
			return errors.New("human original CLI command totals differ")
		}
	case "supervisor-truncate", "writer-rotate":
		status, segments := "partial", 1
		if name == "writer-rotate" {
			status, segments = "complete", 4
		}
		n := read / int64(rowSize)
		source = fmt.Sprintf("  Supervisor %s (%s): read %d / captured %d bytes across %d segments; retained %d, in window %d, invalid 0.", path, status, read, size, segments, n, n)
		outcome = fmt.Sprintf("rebuilds %d failed 0 timed successes n=%d p50 0ms p95 0ms", n, n)
	case "claude-append", "codex-truncate":
		complete, partial, p50 := 1, 0, "0ms"
		if name == "codex-truncate" {
			complete, partial, p50 = 0, 1, "-"
		}
		source = fmt.Sprintf("  Transcripts: %d read, %d read in part, 0 unreadable; read %d / captured %d bytes; 0 invalid and 0 oversized records skipped, 0 results without a call, 0 calls without a result.", complete, partial, read, size)
		outcome = "help 1 attributable 1 failed 0 waited 0ms p50 " + p50
		if !strings.Contains(output, "Agents: 1 tool calls, 0 errors; 1 shell commands ran Scenery (1 invocations): 1 recorded Scenery's own outcome (0 failed); as shell commands 0 failed and 0 recorded no outcome\n") {
			return errors.New("human original transcript command/outcome evidence differs")
		}
	default:
		return errors.New("unknown human snapshot fixture")
	}
	foundSource, foundOutcome := false, outcome == ""
	for _, line := range strings.Split(output, "\n") {
		foundSource = foundSource || line == source || (prefix && strings.HasPrefix(line, source))
		foundOutcome = foundOutcome || strings.Join(strings.Fields(line), " ") == outcome
	}
	if !foundSource || !foundOutcome {
		return errors.New("human owning-source coverage or retained outcome differs")
	}
	return nil
}
