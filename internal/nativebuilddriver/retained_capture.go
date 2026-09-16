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
	"runtime"
	"sort"
	"strings"
	"sync"
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
	current.GoToolDigest, err = toolDigest(goPath)
	if err != nil {
		return current, err
	}
	current.PackageLoadingMS = elapsedMS(started)
	directoryStarted := time.Now()
	directoryPaths := sortedMapKeys(baseline.Directories)
	directories := make([]struct {
		digest string
		err    error
	}, len(directoryPaths))
	if err := forEachIndex(ctx, len(directoryPaths), func(index int) {
		directories[index].digest, directories[index].err = directoryDigest(directoryPaths[index])
	}); err != nil {
		return current, err
	}
	for index, path := range directoryPaths {
		if err := directories[index].err; err != nil {
			if errors.Is(err, os.ErrNotExist) {
				current.Reason = "package_selection_changed"
				continue
			}
			return current, err
		}
		current.Directories[path] = directories[index].digest
	}
	current.DirectoryValidationMS = elapsedMS(directoryStarted)
	inputStarted := time.Now()
	filePaths := sortedMapKeys(baseline.Files)
	// Inputs are stamped and hashed concurrently; their results are applied in
	// path order, so the capture and its failure are what a sequential scan
	// would produce.
	inputs := make([]retainedInput, len(filePaths))
	members := newWorkspaceMembers(workspace)
	if err := forEachIndex(ctx, len(filePaths), func(index int) {
		path := filePaths[index]
		inputs[index] = hashRetainedInput(members, path, baseline.Files[path], baseline.FileStamps[path])
	}); err != nil {
		return current, err
	}
	var snapshotDuration time.Duration
	for index, path := range filePaths {
		expected := baseline.Files[path]
		input := inputs[index]
		if input.missing {
			continue
		}
		if input.err != nil {
			return current, input.err
		}
		if !input.regular {
			current.Reason = "input_not_regular"
			continue
		}
		current.FileStamps[path] = input.stamp
		digest := input.digest
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

// retainedInput is the observed identity of one input of a retained capture.
type retainedInput struct {
	missing, regular bool
	stamp            FileStamp
	digest           string
	err              error
}

// hashRetainedInput stamps one input and hashes it unless it lies outside the
// workspace and its stable change time proves the recorded digest.
func hashRetainedInput(members *workspaceMembers, path, expected string, baselineStamp FileStamp) retainedInput {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return retainedInput{missing: true}
	}
	if err != nil {
		return retainedInput{err: err}
	}
	if !info.Mode().IsRegular() {
		return retainedInput{}
	}
	input := retainedInput{regular: true, stamp: fileStamp(info), digest: expected}
	if members.regularFile(path) || baselineStamp.ChangeTimeNano == 0 || input.stamp != baselineStamp {
		input.digest, _, input.err = FileDigest(path)
		if input.err != nil {
			return input
		}
		after, statErr := os.Lstat(path)
		if statErr != nil {
			input.err = fmt.Errorf("stat retained input after hashing %s: %w", path, statErr)
		} else if fileStamp(after) != input.stamp {
			input.err = fmt.Errorf("input changed while hashing: %s", path)
		}
	}
	return input
}

// workspaceMembers decides workspace membership of many regular files. The
// workspace is canonicalized once and each directory once, because resolving
// every component of every input path dominated a capture of a large domain.
type workspaceMembers struct {
	root        string
	directories sync.Map
}

func newWorkspaceMembers(workspace string) *workspaceMembers {
	return &workspaceMembers{root: canonicalRetainedPath(workspace)}
}

// regularFile reports whether a path that names a regular file, not a
// symbolic link, lies within the workspace.
func (members *workspaceMembers) regularFile(path string) bool {
	directory := filepath.Dir(path)
	canonical, ok := members.directories.Load(directory)
	if !ok {
		canonical, _ = members.directories.LoadOrStore(directory, canonicalRetainedPath(directory))
	}
	relative, err := filepath.Rel(members.root, filepath.Join(canonical.(string), filepath.Base(path)))
	return err == nil && relative != ".." && relative != "." && relative != "" && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// forEachIndex runs work for every index on up to GOMAXPROCS goroutines and
// reports the context's error when it ends first.
func forEachIndex(ctx context.Context, count int, work func(int)) error {
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), count) {
		wg.Go(func() {
			for index := range next {
				work(index)
			}
		})
	}
	var err error
	for index := range count {
		if err = ctx.Err(); err != nil {
			break
		}
		next <- index
	}
	close(next)
	wg.Wait()
	return err
}

var toolDigests sync.Map

// toolDigest hashes a Go tool once per stamp; a replaced tool has a new stamp.
func toolDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	type key struct {
		path  string
		stamp FileStamp
	}
	identity := key{path, fileStamp(info)}
	if digest, ok := toolDigests.Load(identity); ok {
		return digest.(string), nil
	}
	digest, _, err := FileDigest(path)
	if err != nil {
		return "", err
	}
	toolDigests.Store(identity, digest)
	return digest, nil
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
