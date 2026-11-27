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
		if !strings.Contains(output, fmt.Sprintf("\nCLI commands (%d records, 0 failed, 0 uptime)\n", n)) {
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
