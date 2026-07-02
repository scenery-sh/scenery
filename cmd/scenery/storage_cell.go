package main

import (
	"path/filepath"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

// storageCellPlan describes the Scenery-owned local storage cell directories for
// an app's declared stores. Storage is served from a plain directory tree
// (internal/storage.LocalStore): atomic temp-file + rename writes, checked
// fsync, and sidecar metadata. There is no managed storage process; offsite
// durability is an operator concern (replicate CellRoot with rclone/restic).
type storageCellPlan struct {
	StorageCellID string
	CellRoot      string
	ObjectsDir    string
}

// resolveStorageCellPlan resolves the storage cell directories for the app's
// configured stores. It returns (nil, nil) when no stores are declared. When
// agentHome is empty the default agent home is used.
func resolveStorageCellPlan(cfg app.Config, agentHome string) (*storageCellPlan, error) {
	if len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	paths, err := localagent.DefaultPaths()
	if agentHome != "" {
		paths = localagent.PathsForHome(agentHome)
		err = nil
	}
	if err != nil {
		return nil, err
	}
	cellID := cfg.StorageCellID()
	cellRoot := filepath.Join(paths.AgentDir, "storage", cellID)
	return &storageCellPlan{
		StorageCellID: cellID,
		CellRoot:      cellRoot,
		ObjectsDir:    filepath.Join(cellRoot, "objects"),
	}, nil
}

// storageStoreObjectsDir returns the on-disk directory that backs a single
// named store within the cell.
func (p *storageCellPlan) storageStoreObjectsDir(name string) string {
	if p == nil {
		return ""
	}
	return filepath.Join(p.ObjectsDir, name)
}
