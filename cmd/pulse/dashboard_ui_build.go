package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"pulse.dev/internal/app"
)

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

func prepareDashboardUIDir(console *runConsole) (string, error) {
	if dir := strings.TrimSpace(os.Getenv("PULSE_DEV_DASHBOARD_UI_DIR")); dir != "" {
		return dir, nil
	}

	uiRoot := filepath.Join(app.RepoRoot(), "ui")
	if _, err := os.Stat(filepath.Join(uiRoot, "package.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	depsStale, err := dashboardUIDepsStale(uiRoot)
	if err != nil {
		return "", err
	}
	if depsStale {
		installFn := func() error { return installDashboardUIDeps(uiRoot) }
		if console != nil {
			if err := console.Phase("Installing Pulse dashboard UI packages", installFn); err != nil {
				return "", err
			}
		} else if err := installFn(); err != nil {
			return "", err
		}
	}

	stale, err := dashboardUIBuildStale(uiRoot)
	if err != nil {
		return "", err
	}
	if stale {
		buildFn := func() error { return buildDashboardUI(uiRoot) }
		if console != nil {
			if err := console.Phase("Building Pulse dashboard UI", buildFn); err != nil {
				return "", err
			}
		} else if err := buildFn(); err != nil {
			return "", err
		}
	}

	distDir := filepath.Join(uiRoot, "dist")
	if _, err := os.Stat(filepath.Join(distDir, "index.html")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return distDir, nil
}

func dashboardUIDepsStale(uiRoot string) (bool, error) {
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

func dashboardUIBuildStale(uiRoot string) (bool, error) {
	distIndexPath := filepath.Join(uiRoot, "dist", "index.html")
	distInfo, err := os.Stat(distIndexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	cutoff := distInfo.ModTime()
	for _, rel := range dashboardUISourcePaths {
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

func buildDashboardUI(uiRoot string) error {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		return fmt.Errorf("dashboard UI build requires bun: %w", err)
	}
	cmd := exec.Command(bunPath, "run", "build")
	cmd.Dir = uiRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return fmt.Errorf("dashboard UI build failed: %w", err)
		}
		return fmt.Errorf("dashboard UI build failed: %w\n%s", err, msg)
	}
	return nil
}

func installDashboardUIDeps(uiRoot string) error {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		return fmt.Errorf("dashboard UI install requires bun: %w", err)
	}
	cmd := exec.Command(bunPath, "install")
	cmd.Dir = uiRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return fmt.Errorf("dashboard UI install failed: %w", err)
		}
		return fmt.Errorf("dashboard UI install failed: %w\n%s", err, msg)
	}
	return nil
}
