package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// isolatedHarnessTimingSample accepts one complete uncached exact-root run.
// Invocation isolation is owned by the caller, which starts a new process for
// every sample; this boundary rejects incomplete or replayed result shapes.
func isolatedHarnessTimingSample(output []byte, packageName, testName string) (float64, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	clock := newGoTestRootClock()
	runs, passes, packages := 0, 0, 0
	var seconds float64
	for scanner.Scan() {
		var event goTestJSONEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return 0, fmt.Errorf("invalid Go JSON event: %w", err)
		}
		if event.Package != packageName {
			continue
		}
		if event.Action == "fail" || (event.Action == "skip" && (event.Test == "" || event.Test == testName)) || strings.Contains(event.Output, "(cached)") {
			return 0, fmt.Errorf("unusable %s evidence for %s", event.Action, testName)
		}
		if event.Test == "" && event.Action == "pass" {
			packages++
		}
		if isExactTopLevelGoTestRoot(event.Test) && event.Test != testName {
			return 0, fmt.Errorf("unexpected test root %s", event.Test)
		}
		if event.Test == testName && event.Action == "run" {
			runs++
		}
		if elapsed, finished := clock.observe(event); finished && event.Test == testName {
			passes++
			seconds = elapsed
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read Go JSON events: %w", err)
	}
	if runs != 1 || passes != 1 || packages != 1 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return 0, fmt.Errorf("incomplete exact-root evidence: runs=%d passes=%d packages=%d seconds=%v", runs, passes, packages, seconds)
	}
	return seconds, nil
}
