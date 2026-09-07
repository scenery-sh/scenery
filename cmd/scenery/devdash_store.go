package main

import (
	"os"
	"path/filepath"

	"scenery.sh/internal/devdash"
)

func openWorktreeDevdashStore(appRoot string) (*devdash.Store, error) {
	paths, err := commandWorktreePaths(appRoot)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(paths.ControlPaths().AgentDir, "dashboard")
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	return devdash.OpenReadOnlyStore(root)
}
