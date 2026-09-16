package nativebuilddriver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RetainedCapture validates the complete bootstrap input domain without asking
// cmd/go to rediscover the package graph. Workspace bytes are always hashed;
// unchanged external inputs may reuse a digest only when the platform exposes
// a stable change time. Only changed workspace Go files need a new snapshot.
func (recipe *Recipe) RetainedCapture(ctx context.Context, goTool, snapshotRoot string, env []string, buildFlags []string) (Capture, error) {
	return retainedCapture(ctx, goTool, recipe.Workspace, recipe.currentCapture(), snapshotRoot, env, buildFlags)
}

func retainedCapture(ctx context.Context, goTool, workspace string, baseline Capture, snapshotRoot string, env []string, buildFlags []string) (Capture, error) {
	started := time.Now()
	current := Capture{
		Protocol: ProtocolVersion, Workspace: workspace, StartedAt: started.UTC(),
		Packages: clonePackages(baseline.Packages), Files: map[string]string{}, FileStamps: map[string]FileStamp{}, Syntax: cloneStrings(baseline.Syntax),
		Directories: map[string]string{}, SnapshotFiles: cloneStrings(baseline.SnapshotFiles),
		GoVersion: baseline.GoVersion, BuildFlags: append([]string(nil), buildFlags...), Pattern: baseline.Pattern, Entrypoint: baseline.Entrypoint,
		Environment: cloneStrings(baseline.Environment), RequestEnv: relevantRequestEnvironment(env),
	}
	if err := ctx.Err(); err != nil {
		return current, err
	}
	goPath, err := exec.LookPath(goTool)
	if err != nil {
		return current, err
	}
	current.GoToolDigest, _, err = FileDigest(goPath)
	if err != nil {
		return current, err
	}
	current.PackageLoadingMS = elapsedMS(started)
	directoryStarted := time.Now()
	directoryPaths := sortedMapKeys(baseline.Directories)
	for _, path := range directoryPaths {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		digest, err := directoryDigest(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				current.Reason = "package_selection_changed"
				continue
			}
			return current, err
		}
		current.Directories[path] = digest
	}
	current.DirectoryValidationMS = elapsedMS(directoryStarted)
	inputStarted := time.Now()
	filePaths := sortedMapKeys(baseline.Files)
	var snapshotDuration time.Duration
	for _, path := range filePaths {
		expected := baseline.Files[path]
		if err := ctx.Err(); err != nil {
			return current, err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return current, err
		}
		if !info.Mode().IsRegular() {
			current.Reason = "input_not_regular"
			continue
		}
		stamp := fileStamp(info)
		current.FileStamps[path] = stamp
		digest := expected
		baselineStamp := baseline.FileStamps[path]
		if withinWorkspace(workspace, path) || baselineStamp.ChangeTimeNano == 0 || stamp != baselineStamp {
			digest, _, err = FileDigest(path)
			if err != nil {
				return current, err
			}
			after, statErr := os.Lstat(path)
			if statErr != nil {
				return current, fmt.Errorf("stat retained input after hashing %s: %w", path, statErr)
			}
			if fileStamp(after) != stamp {
				return current, fmt.Errorf("input changed while hashing: %s", path)
			}
		}
		current.Files[path] = digest
		if digest == expected {
			continue
		}
		if filepath.Ext(path) == ".go" {
			syntax, syntaxErr := sourceSelectionIdentity(path)
			if syntaxErr != nil {
				current.Reason = "package_selection_changed"
				continue
			}
			current.Syntax[path] = syntax
		}
		if !withinWorkspace(workspace, path) {
			continue
		}
		rel, ok := workspaceRelative(workspace, path)
		if !ok {
			return current, fmt.Errorf("retained source escaped workspace: %s", path)
		}
		copyStarted := time.Now()
		copy, err := CopyRegular(path, filepath.Join(snapshotRoot, "workspace", rel))
		snapshotDuration += time.Since(copyStarted)
		if err != nil {
			return current, err
		}
		if copy.Digest != digest {
			return current, fmt.Errorf("source changed during retained capture: %s", path)
		}
		current.SnapshotFiles[path] = copy.Copy
	}
	current.SnapshotMS = float64(snapshotDuration.Nanoseconds()) / 1e6
	current.InputHashMS = elapsedMS(inputStarted) - current.SnapshotMS
	encoded, err := canonicalCapture(current)
	if err != nil {
		return current, err
	}
	digest := sha256.Sum256(encoded)
	current.Digest = "sha256:" + hex.EncodeToString(digest[:])
	current.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	return current, nil
}

func sortedMapKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func elapsedMS(started time.Time) float64 {
	return float64(time.Since(started).Nanoseconds()) / 1e6
}

func withinWorkspace(workspace, path string) bool {
	_, ok := workspaceRelative(workspace, path)
	return ok
}

func workspaceRelative(workspace, path string) (string, bool) {
	relative, err := filepath.Rel(canonicalRetainedPath(workspace), canonicalRetainedPath(path))
	return relative, err == nil && relative != ".." && relative != "." && relative != "" && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func cloneStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func clonePackages(source map[string]Package) map[string]Package {
	result := make(map[string]Package, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
