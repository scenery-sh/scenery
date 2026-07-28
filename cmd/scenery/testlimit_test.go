package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/testlimit"
)

// TestMain keeps the testlimit GOMAXPROCS cap (set by the import's init) but
// raises -test.parallel: this package's parallel tests mostly wait on
// subprocesses, so more in-flight tests shorten the run without adding
// scheduler threads.
//
// It also isolates the test binary from the developer's real machine state.
// SCENERY_AGENT_HOME pins DefaultPaths to a temp home, and the edge-helper
// plist introspection is stubbed to "not installed": on a machine with the
// privileged helper installed, the real plist points HelperTargetState at the
// real ~/.scenery/run/edge-target.json, and any test walking the deploy/edge
// status paths would migrate that live file in place — mutating machine state
// and invalidating this package's Go test cache entry on every following run.
// Tests that need specific helper state already override these func vars.
func TestMain(m *testing.M) {
	testlimit.RaiseTestParallelism(8)
	home, err := os.MkdirTemp("", "scenery-cmd-test-agent-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("SCENERY_AGENT_HOME", filepath.Join(home, ".scenery")); err != nil {
		panic(err)
	}
	edgeHelperPlistOptionsFunc = func() (edgeHelperOptions, error) {
		return edgeHelperOptions{}, errors.New("edge helper introspection is disabled in tests; override edgeHelperPlistOptionsFunc for specific helper state")
	}
	dashboardConsoleDistDirFunc = func() (string, bool) { return "", false }
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
