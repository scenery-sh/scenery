package build

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Service entrypoints of the process model were once linked from recorded
// per-entrypoint recipes kept beside the application's retained compiler
// state. Measured on ONLV, a ready recipe did not shorten an edit and
// recording one slowed the edits made meanwhile, so entrypoints are linked by
// stock Go only. Workspaces still hold that recorded state; it is removed once
// per workspace, and nothing reads it.

const retiredRetainedProcessRoot = "processes"

var retiredRetainedProcessWorkspaces sync.Map

// retireRetainedProcessState removes the recorded entrypoint recipes and their
// shared store from the workspace's retained compiler root. The application
// entrypoint's retained state beside it and the Go build cache are untouched.
// A workspace counts as retired only once the removal succeeded; a failure is
// reported as a failed build step, never fails the build, and is retried by
// the next process build.
func retireRetainedProcessState(ctx context.Context, workspace string) {
	root, err := retainedNativeRoot(workspace)
	if err != nil {
		return
	}
	if _, retired := retiredRetainedProcessWorkspaces.Load(root); retired {
		return
	}
	started := time.Now()
	if err := os.RemoveAll(filepath.Join(root, retiredRetainedProcessRoot)); err != nil {
		RecordStep(ctx, Step{Name: "build.retained_state_retirement", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "retired_entrypoint_recipes: " + err.Error(), OK: false})
		return
	}
	retiredRetainedProcessWorkspaces.Store(root, true)
}
