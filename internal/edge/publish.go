package edge

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// publishRetainReleases is the fixed number of past releases kept next to the
// current one. This is artifact safety for rollback of a failed reload, not a
// general release store.
const publishRetainReleases = 3

// PublishInput describes one production frontend build to publish into the
// Scenery-owned deploy artifact store.
type PublishInput struct {
	// ArtifactsRoot is the machine-level deploy artifact root, normally
	// agent Paths.DeployArtifactsDir.
	ArtifactsRoot string
	// AppID and Frontend name the publication target; both must be safe
	// identifiers because they become path segments.
	AppID    string
	Frontend string
	// SourceDir is the completed build output directory (for example dist/).
	SourceDir string
	// ReleaseID names the immutable release directory. Empty selects a
	// UTC-timestamp identifier.
	ReleaseID string
}

// PublishedFrontend is the typed record of one completed publication.
type PublishedFrontend struct {
	AppID       string `json:"app_id"`
	Frontend    string `json:"frontend"`
	ReleaseID   string `json:"release_id"`
	CurrentPath string `json:"current_path"`
	ReleaseDir  string `json:"release_dir"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
}

// PublishFrontendArtifact copies a validated production build into a new
// immutable release directory beneath ArtifactsRoot and atomically switches
// the frontend's `current` symlink to it. The previous release stays on disk
// (bounded retention) so a failed edge reload can be rolled back. Symlinks and
// special files in the build output are rejected; a partially copied staging
// directory is never observable through `current`.
func PublishFrontendArtifact(in PublishInput) (PublishedFrontend, error) {
	if err := validatePublishIdentifier("app id", in.AppID); err != nil {
		return PublishedFrontend{}, err
	}
	if err := validatePublishIdentifier("frontend name", in.Frontend); err != nil {
		return PublishedFrontend{}, err
	}
	if strings.TrimSpace(in.ArtifactsRoot) == "" {
		return PublishedFrontend{}, fmt.Errorf("publish artifacts root is empty")
	}
	sourceDir := filepath.Clean(strings.TrimSpace(in.SourceDir))
	if info, err := os.Stat(sourceDir); err != nil || !info.IsDir() {
		return PublishedFrontend{}, fmt.Errorf("build output %s is not a directory", sourceDir)
	}
	entry, err := os.Lstat(filepath.Join(sourceDir, "index.html"))
	if err != nil || !entry.Mode().IsRegular() {
		return PublishedFrontend{}, fmt.Errorf("build output %s has no regular index.html entry document", sourceDir)
	}
	releaseID := strings.TrimSpace(in.ReleaseID)
	if releaseID == "" {
		releaseID = time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	if err := validatePublishReleaseID(releaseID); err != nil {
		return PublishedFrontend{}, err
	}
	frontendDir := filepath.Join(filepath.Clean(in.ArtifactsRoot), in.AppID, in.Frontend)
	if err := os.MkdirAll(frontendDir, 0o755); err != nil {
		return PublishedFrontend{}, err
	}
	releaseDir := filepath.Join(frontendDir, releaseID)
	if _, err := os.Lstat(releaseDir); err == nil {
		return PublishedFrontend{}, fmt.Errorf("release %s already exists at %s", releaseID, releaseDir)
	}
	stagingDir := filepath.Join(frontendDir, ".staging-"+releaseID)
	_ = os.RemoveAll(stagingDir)
	files, bytes, err := copyPublishTree(sourceDir, stagingDir)
	if err != nil {
		_ = os.RemoveAll(stagingDir)
		return PublishedFrontend{}, err
	}
	if info, err := os.Lstat(filepath.Join(stagingDir, "index.html")); err != nil || !info.Mode().IsRegular() {
		_ = os.RemoveAll(stagingDir)
		return PublishedFrontend{}, fmt.Errorf("staged release lost its index.html entry document")
	}
	if err := os.Rename(stagingDir, releaseDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return PublishedFrontend{}, err
	}
	currentPath := filepath.Join(frontendDir, "current")
	if err := switchCurrentSymlink(currentPath, releaseID); err != nil {
		return PublishedFrontend{}, err
	}
	if err := prunePublishReleases(frontendDir, releaseID); err != nil {
		return PublishedFrontend{}, err
	}
	return PublishedFrontend{
		AppID:       in.AppID,
		Frontend:    in.Frontend,
		ReleaseID:   releaseID,
		CurrentPath: currentPath,
		ReleaseDir:  releaseDir,
		Files:       files,
		Bytes:       bytes,
	}, nil
}

// CurrentPublishedRelease resolves a frontend `current` symlink to its release
// directory and reports whether it holds a regular entry document.
func CurrentPublishedRelease(currentPath string) (releaseDir string, entryPresent bool, err error) {
	target, err := os.Readlink(currentPath)
	if err != nil {
		return "", false, err
	}
	if filepath.IsAbs(target) || target != filepath.Base(target) {
		return "", false, fmt.Errorf("current symlink %s must point at a sibling release directory, got %q", currentPath, target)
	}
	releaseDir = filepath.Join(filepath.Dir(currentPath), target)
	info, err := os.Stat(releaseDir)
	if err != nil || !info.IsDir() {
		return releaseDir, false, fmt.Errorf("current release %s is not a directory", releaseDir)
	}
	entry, err := os.Lstat(filepath.Join(releaseDir, "index.html"))
	entryPresent = err == nil && entry.Mode().IsRegular()
	return releaseDir, entryPresent, nil
}

// RollbackCurrentRelease atomically points `current` back at a previously
// retained sibling release, used when a Caddy validation, reload, or probe
// fails after a publication switched the pointer.
func RollbackCurrentRelease(currentPath, releaseID string) error {
	if err := validatePublishReleaseID(releaseID); err != nil {
		return err
	}
	releaseDir := filepath.Join(filepath.Dir(currentPath), releaseID)
	if info, err := os.Stat(releaseDir); err != nil || !info.IsDir() {
		return fmt.Errorf("rollback release %s is not a directory", releaseDir)
	}
	return switchCurrentSymlink(currentPath, releaseID)
}

// switchCurrentSymlink atomically replaces `current` with a relative symlink
// to the named release directory on the same filesystem.
func switchCurrentSymlink(currentPath, releaseID string) error {
	tmp := currentPath + ".next"
	_ = os.Remove(tmp)
	if err := os.Symlink(releaseID, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, currentPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// prunePublishReleases removes stale staging directories and old releases,
// keeping the current release plus a fixed retention window. It only ever
// deletes entries directly beneath the validated frontend directory.
func prunePublishReleases(frontendDir, currentReleaseID string) error {
	entries, err := os.ReadDir(frontendDir)
	if err != nil {
		return err
	}
	releases := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".staging-") && name != ".staging-"+currentReleaseID {
			if err := os.RemoveAll(filepath.Join(frontendDir, name)); err != nil {
				return err
			}
			continue
		}
		if !entry.IsDir() || name == currentReleaseID {
			continue
		}
		if strings.HasPrefix(name, ".") || name == "current" {
			continue
		}
		releases = append(releases, name)
	}
	sort.Strings(releases)
	excess := len(releases) - (publishRetainReleases - 1)
	for i := 0; i < excess; i++ {
		if err := os.RemoveAll(filepath.Join(frontendDir, releases[i])); err != nil {
			return err
		}
	}
	return nil
}

// copyPublishTree copies regular files and directories, rejecting symlinks and
// special files so a published release can never reference paths outside its
// own root. Files become world-readable for the unprivileged static server.
func copyPublishTree(sourceDir, destDir string) (int, int64, error) {
	files := 0
	var total int64
	err := filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("build output path %s escapes the build root", path)
		}
		dest := filepath.Join(destDir, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(dest, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			n, err := copyPublishFile(path, dest, info.Mode())
			if err != nil {
				return err
			}
			files++
			total += n
			return nil
		default:
			return fmt.Errorf("build output entry %s is not a regular file or directory; symlinks and special files are not published", path)
		}
	})
	return files, total, err
}

func copyPublishFile(source, dest string, mode fs.FileMode) (int64, error) {
	in, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	perm := (mode.Perm() | 0o444) & 0o755
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return n, err
}

func validatePublishIdentifier(label, value string) error {
	if value == "" {
		return fmt.Errorf("publish %s is empty", label)
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (index > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return fmt.Errorf("publish %s %q must start with a lowercase letter or number and use only lowercase letters, numbers, dots, underscores, or dashes", label, value)
	}
	return nil
}

func validatePublishReleaseID(value string) error {
	if value == "" || value == "current" || strings.HasPrefix(value, ".") {
		return fmt.Errorf("release id %q is reserved", value)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == 'T' || r == 'Z' {
			continue
		}
		return fmt.Errorf("release id %q contains unsupported characters", value)
	}
	return nil
}
