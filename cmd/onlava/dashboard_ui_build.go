package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"onlava.com/internal/app"
)

type uiBuildSpec struct {
	envVar       string
	root         string
	installTitle string
	buildTitle   string
	sourcePaths  []string
}

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

func prepareDashboardUIDir(ctx context.Context, console *runConsole) (string, error) {
	return prepareUIDir(ctx, console, uiBuildSpec{
		envVar:       "ONLAVA_DEV_DASHBOARD_UI_DIR",
		root:         filepath.Join(app.RepoRoot(), "ui"),
		installTitle: "Installing Onlava dashboard UI packages",
		buildTitle:   "Building Onlava dashboard UI",
		sourcePaths:  dashboardUISourcePaths,
	})
}

func dashboardUIDepsStale(uiRoot string) (bool, error) {
	return uiDepsStale(uiRoot)
}

func dashboardUIBuildStale(uiRoot string) (bool, error) {
	return uiBuildStale(uiRoot, dashboardUISourcePaths)
}

func prepareUIDir(ctx context.Context, console *runConsole, spec uiBuildSpec) (string, error) {
	if dir := strings.TrimSpace(os.Getenv(spec.envVar)); dir != "" {
		return dir, nil
	}
	if _, err := os.Stat(filepath.Join(spec.root, "package.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	depsStale, err := uiDepsStale(spec.root)
	if err != nil {
		return "", err
	}
	if depsStale {
		installFn := func() error { return installUIDeps(ctx, spec.root) }
		if console != nil {
			if err := console.Phase(spec.installTitle, installFn); err != nil {
				return "", err
			}
		} else if err := installFn(); err != nil {
			return "", err
		}
	}

	stale, err := uiBuildStale(spec.root, spec.sourcePaths)
	if err != nil {
		return "", err
	}
	if stale {
		buildFn := func() error { return buildUI(ctx, spec.root) }
		if console != nil {
			if err := console.Phase(spec.buildTitle, buildFn); err != nil {
				return "", err
			}
		} else if err := buildFn(); err != nil {
			return "", err
		}
	}

	distDir := filepath.Join(spec.root, "dist")
	if _, err := os.Stat(filepath.Join(distDir, "index.html")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return distDir, nil
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

func buildUI(ctx context.Context, uiRoot string) error {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		return fmt.Errorf("UI build requires bun: %w", err)
	}
	cmd := commandTreeContext(ctx, bunPath, "run", "build")
	cmd.Dir = uiRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return fmt.Errorf("UI build failed: %w", err)
		}
		return fmt.Errorf("UI build failed: %w\n%s", err, msg)
	}
	return nil
}

func installUIDeps(ctx context.Context, uiRoot string) error {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		return fmt.Errorf("UI install requires bun: %w", err)
	}
	cmd := commandTreeContext(ctx, bunPath, "install")
	cmd.Dir = uiRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return fmt.Errorf("UI install failed: %w", err)
		}
		return fmt.Errorf("UI install failed: %w\n%s", err, msg)
	}
	return nil
}
