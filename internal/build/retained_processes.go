package build

import (
	"os"
	"path/filepath"
	"sync"
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
func retireRetainedProcessState(workspace string) {
	root, err := retainedNativeRoot(workspace)
	if err != nil {
		return
	}
	if _, retired := retiredRetainedProcessWorkspaces.LoadOrStore(root, true); retired {
		return
	}
	_ = os.RemoveAll(filepath.Join(root, retiredRetainedProcessRoot))
}
