package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

const dashboardUIRootRel = "apps/consolenext"

func dashboardUIBundleStale(uiRoot string) (bool, error) {
	status, err := dashboardBundleStatusForDist(embeddedDashboardAssetFS(), filepath.Join(uiRoot, "dist"))
	if err != nil {
		return false, err
	}
	return status.Stale, nil
}

func dashboardBundleStatusForCurrentRepo() (devdash.DashboardBundle, error) {
	embedded := embeddedDashboardAssetFS()
	status := devdash.DashboardBundle{}
	if hash, err := dashboardBundleHash(embedded); err == nil {
		status.RunningHash = hash
	} else if !errors.Is(err, fs.ErrNotExist) {
		return status, err
	}
	distDir, ok := dashboardConsoleNextDistDir()
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

func dashboardBundleHashDir(dir string) (string, bool, error) {
	if strings.TrimSpace(dir) == "" {
		return "", false, nil
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	hash, err := dashboardBundleHash(os.DirFS(dir))
	return hash, true, err
}

func dashboardBundleHash(fsys fs.FS) (string, error) {
	if fsys == nil {
		return "", fs.ErrNotExist
	}
	names := []string{}
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == "." || path == "placeholder.txt" {
			return nil
		}
		names = append(names, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fs.ErrNotExist
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		_, _ = h.Write([]byte(name))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func dashboardConsoleNextDistDir() (string, bool) {
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
