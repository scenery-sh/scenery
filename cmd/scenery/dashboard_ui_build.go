package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const dashboardUIRootRel = "apps/consolenext"

var dashboardUISourcePaths = []string{
	"package.json",
	"bun.lock",
	"bunfig.toml",
	"tsconfig.json",
	"vite.config.ts",
	"vite.config.js",
	"vite.config.mts",
	"vite.config.mjs",
	"index.html",
	"src",
	"public",
}

func dashboardUIDepsStale(uiRoot string) (bool, error) {
	return uiDepsStale(uiRoot)
}

func dashboardUIBuildStale(uiRoot string) (bool, error) {
	return uiBuildStale(uiRoot, dashboardUISourcePaths)
}

func uiDepsStale(uiRoot string) (bool, error) {
	nodeModulesInfo, err := os.Stat(filepath.Join(uiRoot, "node_modules"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	cutoff := nodeModulesInfo.ModTime()
	for _, rel := range []string{"package.json", "bun.lock", "bunfig.toml"} {
		path := filepath.Join(uiRoot, rel)
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false, err
		}
		if info.ModTime().After(cutoff) {
			return true, nil
		}
	}
	return false, nil
}

func uiBuildStale(uiRoot string, sourcePaths []string) (bool, error) {
	distIndexPath := filepath.Join(uiRoot, "dist", "index.html")
	distInfo, err := os.Stat(distIndexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	cutoff := distInfo.ModTime()
	for _, rel := range sourcePaths {
		path := filepath.Join(uiRoot, rel)
		modTime, ok, err := latestDashboardUIModTime(path)
		if err != nil {
			return false, err
		}
		if ok && modTime.After(cutoff) {
			return true, nil
		}
	}
	return false, nil
}

func latestDashboardUIModTime(path string) (time.Time, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	if !info.IsDir() {
		return info.ModTime(), true, nil
	}

	latest := info.ModTime()
	found := false
	err = filepath.WalkDir(path, func(walkPath string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		entryInfo, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		found = true
		if entryInfo.ModTime().After(latest) {
			latest = entryInfo.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return latest, found, nil
}
