package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/build"
)

const harnessBuildCacheSweepName = "probe build-cache sweep"

// runHarnessBuildCacheSweepStep removes the development-cache workspaces the
// probes' temporary app roots left behind. Every probe builds its disposable
// app in the shared development cache, whose workspaces are named by the app
// root hash; once the temporary root is gone nothing can reuse them. Only
// workspaces whose recorded app root lies beneath the temporary directory and
// no longer exists are removed: every other workspace, including a retained
// probe root kept for diagnosis, is left to `scenery prune --build-cache`.
func runHarnessBuildCacheSweepStep(ctx context.Context, repoRoot string, probes []string) harnessStep {
	started := time.Now()
	command := []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot}
	for _, id := range probes {
		command = append(command, "--probe", id)
	}
	command = append(command, "--summary", "--write")
	step := harnessStep{Name: harnessBuildCacheSweepName, Command: command}
	cacheRoot, entries, err := build.PruneWorkspaces(ctx, build.WorkspacePruneOptions{OrphanFilter: harnessTemporaryAppRoot})
	removed, bytes := 0, int64(0)
	for _, entry := range entries {
		if entry.Removed {
			removed++
			bytes += entry.Bytes
		}
	}
	step.Summary = map[string]any{"cache_root": cacheRoot, "workspaces": len(entries), "removed": removed, "bytes_reclaimed": bytes}
	step.OK = err == nil
	if err != nil {
		step.Error = err.Error()
		step.Diagnostics = []checkDiagnostic{{
			Stage:           harnessBuildCacheSweepName,
			Severity:        "warning",
			Message:         "probe build workspaces were not swept: " + err.Error(),
			SuggestedAction: "Run `scenery prune --build-cache --older-than 1h` after the probes finish, or fix the development cache root.",
		}}
	}
	step.DurationMS = time.Since(started).Milliseconds()
	return step
}

// harnessTemporaryAppRoot reports an app root beneath the temporary directory
// in either its written or symlink-resolved spelling, as probes create them.
func harnessTemporaryAppRoot(appRoot string) bool {
	for _, base := range []string{os.TempDir(), "/tmp"} {
		for _, spelling := range []string{filepath.Clean(base), canonicalHarnessPath(base)} {
			if spelling != "" && spelling != string(filepath.Separator) && strings.HasPrefix(appRoot, spelling+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

func canonicalHarnessPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}
