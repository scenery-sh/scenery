package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Reasons a prune reports for a development-cache workspace.
const (
	// WorkspacePruneAppRootMissing removed a workspace whose recorded app
	// root no longer exists.
	WorkspacePruneAppRootMissing = "app_root_missing"
	// WorkspacePruneStale removed a workspace whose build state is older than
	// the cutoff.
	WorkspacePruneStale = "stale"
	// WorkspacePruneLocked kept a workspace another process is preparing or
	// building.
	WorkspacePruneLocked = "locked"
	// WorkspacePruneActive kept the workspace of a protected app root, such as
	// one with a running runtime.
	WorkspacePruneActive = "active"
	// WorkspacePruneRecent kept a workspace whose build state is newer than
	// the cutoff.
	WorkspacePruneRecent = "recent"
	// WorkspacePruneKept kept a workspace no removal rule selected.
	WorkspacePruneKept = "kept"
)

// WorkspacePruneEntry reports one development-cache workspace a prune
// considered.
type WorkspacePruneEntry struct {
	Path      string `json:"path"`
	AppRoot   string `json:"app_root,omitempty"`
	AppName   string `json:"app_name,omitempty"`
	UpdatedAt string `json:"updated_at"`
	Bytes     int64  `json:"bytes"`
	Removed   bool   `json:"removed"`
	Reason    string `json:"reason"`
}

// WorkspacePruneOptions selects which private workspaces of the development
// cache a prune may remove. A workspace whose recorded app root no longer
// exists is removed unless OrphanFilter rejects that root; a workspace whose
// build state is older than Cutoff is removed; every other workspace is kept.
// A workspace another process holds locked is always kept.
type WorkspacePruneOptions struct {
	// Cutoff removes workspaces whose build state was last written before
	// it. Zero disables age-based removal.
	Cutoff time.Time
	// AppRoots restricts the prune to the workspaces of these app roots.
	// Empty selects every workspace of the cache.
	AppRoots []string
	// ProtectedAppRoots names app roots whose workspaces are kept regardless
	// of age, such as those with a running runtime.
	ProtectedAppRoots []string
	// OrphanFilter, when set, limits orphan removal to recorded app roots it
	// accepts. Nil removes every orphaned workspace.
	OrphanFilter func(appRoot string) bool
}

// PruneWorkspaces removes the selected private workspaces beneath the
// development cache root and reports every workspace it considered, sorted by
// path. Shared content-addressed caches beside the workspaces are untouched.
func PruneWorkspaces(ctx context.Context, opts WorkspacePruneOptions) (string, []WorkspacePruneEntry, error) {
	cacheRoot, err := CacheRoot()
	if err != nil {
		return "", nil, err
	}
	buildRoot := filepath.Join(cacheRoot, "build")
	entries, err := os.ReadDir(buildRoot)
	if errors.Is(err, os.ErrNotExist) {
		return cacheRoot, []WorkspacePruneEntry{}, nil
	}
	if err != nil {
		return cacheRoot, nil, err
	}
	selected, err := workspaceSuffixSet(opts.AppRoots)
	if err != nil {
		return cacheRoot, nil, err
	}
	protected, err := workspaceSuffixSet(opts.ProtectedAppRoots)
	if err != nil {
		return cacheRoot, nil, err
	}
	report := []WorkspacePruneEntry{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return cacheRoot, report, err
		}
		suffix, ok := workspaceDirectorySuffix(entry)
		if !ok || (len(selected) > 0 && !selected[suffix]) {
			continue
		}
		path := filepath.Join(buildRoot, entry.Name())
		if !isWorkspaceDirectory(path) {
			continue
		}
		item, err := pruneWorkspace(path, protected[suffix], opts)
		if err != nil {
			return cacheRoot, report, err
		}
		report = append(report, item)
	}
	sort.Slice(report, func(i, j int) bool { return report[i].Path < report[j].Path })
	return cacheRoot, report, nil
}

func pruneWorkspace(path string, protectedRoot bool, opts WorkspacePruneOptions) (WorkspacePruneEntry, error) {
	item := WorkspacePruneEntry{Path: path, Reason: WorkspacePruneKept}
	unlock, held, err := tryLockWorkspace(path)
	if err != nil {
		return item, err
	}
	if held {
		item.Reason = WorkspacePruneLocked
		return item, nil
	}
	defer unlock()
	marker, markerErr := ReadWorkspaceMarker(path)
	if markerErr == nil {
		item.AppRoot, item.AppName = marker.AppRoot, marker.AppName
	}
	updatedAt := workspaceUpdatedAt(path)
	item.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	switch {
	case protectedRoot:
		item.Reason = WorkspacePruneActive
	case markerErr == nil && workspaceAppRootMissing(marker.AppRoot) && (opts.OrphanFilter == nil || opts.OrphanFilter(marker.AppRoot)):
		item.Reason = WorkspacePruneAppRootMissing
		item.Removed = true
	case !opts.Cutoff.IsZero() && updatedAt.Before(opts.Cutoff):
		item.Reason = WorkspacePruneStale
		item.Removed = true
	case !opts.Cutoff.IsZero():
		item.Reason = WorkspacePruneRecent
	}
	item.Bytes = directoryBytes(path)
	if !item.Removed {
		return item, nil
	}
	if err := os.RemoveAll(path); err != nil {
		return item, err
	}
	roots, err := retiredCompilerStateRoots(path)
	if err != nil {
		return item, err
	}
	for _, root := range roots {
		if err := os.RemoveAll(root); err != nil {
			return item, err
		}
	}
	return item, nil
}

// workspaceDirectorySuffix returns the app-root hash of a workspace directory
// name, `<label>-<16 hex>`, and false for every other cache entry.
func workspaceDirectorySuffix(entry fs.DirEntry) (string, bool) {
	if !entry.IsDir() {
		return "", false
	}
	name := entry.Name()
	dash := strings.LastIndexByte(name, '-')
	if dash <= 0 || len(name)-dash-1 != 16 {
		return "", false
	}
	suffix := name[dash+1:]
	if decoded, err := hex.DecodeString(suffix); err != nil || len(decoded) != 8 {
		return "", false
	}
	return suffix, true
}

// isWorkspaceDirectory requires the state a preparation leaves behind; a
// directory that merely looks like a workspace is never removed.
func isWorkspaceDirectory(path string) bool {
	for _, name := range []string{buildStateFile, workspaceMarkerFile, ".scenery-workspace.lock"} {
		if info, err := os.Lstat(filepath.Join(path, name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// workspaceSuffix is the app-root hash WorkspaceDirAt puts in a workspace
// directory name.
func workspaceSuffix(appRoot string) (string, error) {
	absRoot, err := filepath.Abs(appRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absRoot))
	return hex.EncodeToString(sum[:8]), nil
}

// workspaceSuffixSet hashes each app root as written and, when it exists,
// with its symbolic links resolved, so either spelling selects the workspace.
func workspaceSuffixSet(appRoots []string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, appRoot := range appRoots {
		if strings.TrimSpace(appRoot) == "" {
			continue
		}
		suffix, err := workspaceSuffix(appRoot)
		if err != nil {
			return nil, err
		}
		set[suffix] = true
		if canonical, err := filepath.EvalSymlinks(appRoot); err == nil {
			suffix, err := workspaceSuffix(canonical)
			if err != nil {
				return nil, err
			}
			set[suffix] = true
		}
	}
	return set, nil
}

// workspaceAppRootMissing reports a recorded app root that no longer exists.
// Any other failure to read it keeps the workspace.
func workspaceAppRootMissing(appRoot string) bool {
	_, err := os.Stat(appRoot)
	return errors.Is(err, os.ErrNotExist)
}

// workspaceUpdatedAt is the time the workspace's build state was last
// written, falling back to its marker and then the directory itself.
func workspaceUpdatedAt(path string) time.Time {
	for _, name := range []string{buildStateFile, workspaceMarkerFile, ""} {
		if info, err := os.Lstat(filepath.Join(path, name)); err == nil {
			return info.ModTime()
		}
	}
	return time.Time{}
}

// directoryBytes sums the sizes of the regular files beneath path. Unreadable
// entries count as zero: the size is evidence, not a removal condition.
func directoryBytes(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
