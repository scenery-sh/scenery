package scenery_test

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

func installedSceneryBinaryMatchesRepo(path, repo string) bool {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return false
	}
	if info.Main.Path != "scenery.sh" {
		return false
	}
	revision, ok := buildInfoSetting(info, "vcs.revision")
	if !ok || strings.TrimSpace(revision) == "" {
		return false
	}
	if modified, ok := buildInfoSetting(info, "vcs.modified"); ok && modified == "true" {
		return false
	}
	repoRevision, err := currentRepoRevision(repo)
	if err != nil || repoRevision == "" {
		return false
	}
	return revision == repoRevision
}

func buildInfoSetting(info *debug.BuildInfo, key string) (string, bool) {
	if info == nil {
		return "", false
	}
	for _, setting := range info.Settings {
		if setting.Key == key {
			return setting.Value, true
		}
	}
	return "", false
}

func currentRepoRevision(repo string) (string, error) {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func latestIntegrationSourceModTime(repo string) (time.Time, bool, error) {
	paths := []string{
		"go.mod",
		"go.sum",
		"auth",
		"cmd",
		"cron",
		"data",
		"errs",
		"internal",
		"middleware",
		"rlog",
		"runtime",
	}
	var latest time.Time
	found := false
	for _, rel := range paths {
		modTime, ok, err := latestPathModTime(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			return time.Time{}, false, err
		}
		if ok && (!found || modTime.After(latest)) {
			latest = modTime
			found = true
		}
	}
	return latest, found, nil
}

func latestPathModTime(root string) (time.Time, bool, error) {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	if !info.IsDir() {
		return info.ModTime(), true, nil
	}
	var latest time.Time
	found := false
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if integrationBinaryInputSkipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !integrationBinaryInputFile(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !found || info.ModTime().After(latest) {
			latest = info.ModTime()
			found = true
		}
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return latest, found, nil
}

func integrationSourceFingerprint(repo string) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("repo-root"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(filepath.Clean(repo)))
	_, _ = h.Write([]byte{0})
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel == ".":
				return nil
			case integrationBinaryInputSkipDirName(d.Name()):
				return filepath.SkipDir
			case rel == "cmd" || rel == "scripts":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !integrationSourceFingerprintFile(rel, path) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func integrationSourceFingerprintFile(rel, path string) bool {
	if rel == "go.mod" || rel == "go.sum" {
		return true
	}
	if strings.HasPrefix(rel, "testdata/apps/") {
		base := filepath.Base(path)
		return base != "" && base != ".DS_Store"
	}
	for _, prefix := range []string{"auth/", "cron/", "errs/", "internal/", "middleware/", "rlog/", "runtime/"} {
		if strings.HasPrefix(rel, prefix) {
			return integrationBinaryInputFile(path)
		}
	}
	return false
}

func integrationBinaryInputFile(path string) bool {
	base := filepath.Base(path)
	if base == "" || base == ".DS_Store" || strings.HasPrefix(base, ".env") || strings.HasPrefix(base, ".") {
		return false
	}
	if strings.HasSuffix(base, "_test.go") {
		return false
	}
	return true
}

func integrationBinaryInputSkipDirName(name string) bool {
	switch name {
	case ".git", ".scenery", "node_modules", "dist", "coverage":
		return true
	default:
		return false
	}
}
