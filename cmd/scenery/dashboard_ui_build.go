package main

import (
	"errors"

	"io/fs"
	"os"
	"path/filepath"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

const dashboardUIRootRel = "apps/console"

var dashboardBundleHash = devdash.DashboardBundleHash
var dashboardBundleHashDir = devdash.DashboardBundleHashDir

func dashboardBundleStatusForCurrentRepo() (devdash.DashboardBundle, error) {
	embedded := embeddedDashboardAssetFS()
	status := devdash.DashboardBundle{}
	if hash, err := dashboardBundleHash(embedded); err == nil {
		status.RunningHash = hash
	} else if !errors.Is(err, fs.ErrNotExist) {
		return status, err
	}
	distDir, ok := dashboardConsoleDistDirFunc()
	if !ok {
		return status, nil
	}
	return dashboardBundleStatusForDist(embedded, distDir)
}

func dashboardBundleStatusForDist(embedded fs.FS, distDir string) (devdash.DashboardBundle, error) {
	status := devdash.DashboardBundle{}
	if hash, err := dashboardBundleHash(embedded); err == nil {
		status.RunningHash = hash
	} else if !errors.Is(err, fs.ErrNotExist) {
		return status, err
	}
	diskHash, exists, err := dashboardBundleHashDir(distDir)
	if err != nil {
		return status, err
	}
	if !exists {
		status.Stale = true
		status.Warning = "Dashboard UI bundle is stale; run ./scripts/build-dashboard-ui-embed.sh, rebuild the scenery binary, then restart scenery."
		return status, nil
	}
	status.DiskHash = diskHash
	status.DiskPath = filepath.ToSlash(distDir)
	status.Stale = status.RunningHash == "" || status.RunningHash != status.DiskHash
	if status.Stale {
		status.Warning = "Dashboard UI bundle is stale; run ./scripts/build-dashboard-ui-embed.sh, rebuild the scenery binary, then restart scenery."
	}
	return status, nil
}

// dashboardConsoleDistDirFunc resolves the working repository's built
// dashboard bundle. Tests replace it so the dashboard never reads the live
// `apps/console/dist`: that directory is rewritten by every `bun run build`
// (including the self-harness's own dashboard build step), and reading it
// records it as a Go test-cache input, so each dashboard build invalidated
// this package's cached test result.
var dashboardConsoleDistDirFunc = dashboardConsoleDistDir

func dashboardConsoleDistDir() (string, bool) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	candidates = append(candidates, appcfg.RepoRoot())
	for _, start := range candidates {
		repoRoot, ok := findSceneryRepoRoot(start)
		if !ok {
			continue
		}
		distDir := filepath.Join(repoRoot, filepath.FromSlash(dashboardUIRootRel), "dist")
		if _, err := os.Stat(filepath.Join(distDir, "index.html")); err == nil {
			return distDir, true
		}
	}
	return "", false
}
