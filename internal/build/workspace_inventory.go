package build

import (
	"os"
	"path/filepath"
)

// workspaceInventory shares actual bytes within one preparation operation,
// after synchronization. It never survives a write, tidy, or another build.
type workspaceInventory struct {
	root  string
	files map[string]workspaceRead
}

type workspaceRead struct {
	data []byte
	err  error
}

func newWorkspaceInventory(root string) *workspaceInventory {
	return &workspaceInventory{root: root, files: make(map[string]workspaceRead)}
}

func (inventory *workspaceInventory) read(rel string) ([]byte, error) {
	rel = filepath.ToSlash(rel)
	if previous, ok := inventory.files[rel]; ok {
		return previous.data, previous.err
	}
	data, err := os.ReadFile(filepath.Join(inventory.root, filepath.FromSlash(rel)))
	inventory.files[rel] = workspaceRead{data, err}
	return data, err
}
