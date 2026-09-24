package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"scenery.sh/internal/build"
)

// pruneBuildCacheReport is the disposable build state one prune considered:
// the development cache's private workspaces and, for a selected app root,
// its app-local framework snapshots and producers.
type pruneBuildCacheReport struct {
	CacheRoot      string                      `json:"cache_root"`
	Workspaces     []build.WorkspacePruneEntry `json:"workspaces"`
	Framework      []build.FrameworkPruneEntry `json:"framework"`
	BytesReclaimed int64                       `json:"bytes_reclaimed"`
}

// pruneBuildCache removes the workspaces whose recorded app root no longer
// exists or whose build state is older than cutoff, keeping those of running
// or unavailable worktree runtimes, and with an explicit app root also that
// root's unselected framework state.
func pruneBuildCache(ctx context.Context, entries []worktreeStatusEntry, appRoot string, cutoff time.Time) (*pruneBuildCacheReport, error) {
	options := build.WorkspacePruneOptions{Cutoff: cutoff}
	for _, entry := range entries {
		if entry.AppRoot != "" && (entry.Status == "running" || entry.Status == "unavailable") {
			options.ProtectedAppRoots = append(options.ProtectedAppRoots, entry.AppRoot)
		}
	}
	if appRoot != "" {
		options.AppRoots = []string{appRoot}
	}
	cacheRoot, workspaces, err := build.PruneWorkspaces(ctx, options)
	if err != nil {
		return nil, err
	}
	report := &pruneBuildCacheReport{CacheRoot: cacheRoot, Workspaces: workspaces, Framework: []build.FrameworkPruneEntry{}}
	for _, entry := range workspaces {
		if entry.Removed {
			report.BytesReclaimed += entry.Bytes
		}
	}
	if appRoot == "" {
		return report, nil
	}
	framework, err := build.PruneFrameworkState(appRoot)
	if err != nil {
		return nil, &codedCLIError{err: err, code: 3}
	}
	report.Framework = framework
	for _, entry := range framework {
		if entry.Removed {
			report.BytesReclaimed += entry.Bytes
		}
	}
	return report, nil
}

func writePruneBuildCacheSummary(stdout io.Writer, report *pruneBuildCacheReport) error {
	if report == nil {
		return nil
	}
	removed, kept := 0, 0
	for _, entry := range report.Workspaces {
		if !entry.Removed {
			kept++
			continue
		}
		removed++
		if _, err := fmt.Fprintf(stdout, "removed build workspace %s (%s, %s)\n", entry.Path, entry.Reason, formatPruneBytes(entry.Bytes)); err != nil {
			return err
		}
	}
	for _, entry := range report.Framework {
		if !entry.Removed {
			continue
		}
		if _, err := fmt.Fprintf(stdout, "removed framework %s %s (%s, %s)\n", entry.Kind, entry.Path, entry.Reason, formatPruneBytes(entry.Bytes)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(stdout, "build cache %s: removed %d workspaces, kept %d; reclaimed %s\n", report.CacheRoot, removed, kept, formatPruneBytes(report.BytesReclaimed))
	return err
}

func formatPruneBytes(bytes int64) string {
	const unit = 1024
	switch {
	case bytes >= unit*unit*unit:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(unit*unit*unit))
	case bytes >= unit*unit:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(unit*unit))
	case bytes >= unit:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/float64(unit))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
