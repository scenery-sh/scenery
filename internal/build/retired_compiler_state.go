package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Development builds of a single application executable once used a retained
// Go compiler, which kept recorded action graphs, archives and entrypoint
// recipes for each workspace under the build cache. The single application
// development model and that compiler were removed; process-model entrypoints
// are linked by stock Go. Workspaces may still own that state, and nothing
// reads it, so each workspace's copy is removed once.

var retiredCompilerStateWorkspaces sync.Map

// retiredCompilerStateName is the directory name of a workspace's retained
// compiler state in each cache version.
func retiredCompilerStateName(workspace string) (string, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(absWorkspace); canonicalErr == nil {
		absWorkspace = canonical
	}
	digest := sha256.Sum256([]byte(filepath.Clean(absWorkspace)))
	return hex.EncodeToString(digest[:16]), nil
}

// retiredCompilerStateRoots returns the retained compiler directories of every
// cache version that belong to workspace.
func retiredCompilerStateRoots(workspace string) ([]string, error) {
	cacheRoot, err := CacheRoot()
	if err != nil {
		return nil, err
	}
	name, err := retiredCompilerStateName(workspace)
	if err != nil {
		return nil, err
	}
	return filepath.Glob(filepath.Join(cacheRoot, "build", "retained-go-compiler", "*", name))
}

// retireCompilerState removes the workspace's retained compiler state. Other
// workspaces' state and the Go build cache are untouched. A workspace counts as
// retired only once the removal succeeded; a failure is reported as a failed
// build step, never fails the build, and is retried by the next build.
func retireCompilerState(ctx context.Context, workspace string) {
	key := filepath.Clean(workspace)
	if _, retired := retiredCompilerStateWorkspaces.Load(key); retired {
		return
	}
	started := time.Now()
	roots, err := retiredCompilerStateRoots(workspace)
	for _, root := range roots {
		if err == nil {
			err = os.RemoveAll(root)
		}
	}
	if err != nil {
		RecordStep(ctx, Step{Name: "build.retained_state_retirement", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "retired_compiler_state: " + err.Error(), OK: false})
		return
	}
	retiredCompilerStateWorkspaces.Store(key, true)
}
