package build

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// workspaceInventory shares what one preparation operation observed of the
// private workspace, after synchronization. It never survives a write, tidy,
// or another build.
type workspaceInventory struct {
	root    string
	files   map[string]workspaceRead
	digests map[string]workspaceDigest
}

type workspaceRead struct {
	data []byte
	err  error
}

type workspaceDigest struct {
	digest string
	exists bool
	err    error
}

func newWorkspaceInventory(root string) *workspaceInventory {
	return &workspaceInventory{root: root, files: make(map[string]workspaceRead), digests: make(map[string]workspaceDigest)}
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

// digest returns the content digest of the regular file at rel and whether it
// exists. Content observed earlier in this process is reused only while the
// file's stamp, including its status-change time, is unchanged
// (cachedBuildInputFileDigest), so a write by any process is read again.
func (inventory *workspaceInventory) digest(rel string) (string, bool, error) {
	rel = filepath.ToSlash(rel)
	if previous, ok := inventory.digests[rel]; ok {
		return previous.digest, previous.exists, previous.err
	}
	result := workspaceDigest{}
	path := filepath.Join(inventory.root, filepath.FromSlash(rel))
	info, err := buildInputLstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		result.err = err
	case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
		result.err = fmt.Errorf("prepared workspace input is not a regular file: %s", rel)
	default:
		result.digest, _, result.err = cachedBuildInputFileDigest(path, info, os.ReadFile)
		result.exists = result.err == nil
	}
	inventory.digests[rel] = result
	return result.digest, result.exists, result.err
}

// goImports returns the sorted import paths of the Go file at rel. Imports
// parsed earlier in this process are reused only while the file's stamp,
// including its status-change time, is unchanged.
func (inventory *workspaceInventory) goImports(rel string) ([]string, error) {
	path := filepath.Join(inventory.root, filepath.FromSlash(rel))
	before, err := buildInputLstat(path)
	if err != nil {
		return nil, err
	}
	stamp := buildInputStamp(before)
	key := filepath.Clean(path)
	if stamp.ChangeTimeNano != 0 {
		retainedGoImports.Lock()
		entry, ok := retainedGoImports.entries[key]
		retainedGoImports.Unlock()
		if ok && entry.stamp == stamp {
			return slices.Clone(entry.imports), nil
		}
	}
	data, err := inventory.read(rel)
	if err != nil {
		return nil, err
	}
	imports, err := goImports(data)
	if err != nil {
		return nil, err
	}
	if after, err := buildInputLstat(path); err == nil && stamp.ChangeTimeNano != 0 && buildInputStamp(after) == stamp {
		retainedGoImports.Lock()
		if retainedGoImports.entries == nil || len(retainedGoImports.entries) >= retainedGoImportLimit {
			retainedGoImports.entries = map[string]retainedGoImportEntry{}
		}
		retainedGoImports.entries[key] = retainedGoImportEntry{stamp: stamp, imports: slices.Clone(imports)}
		retainedGoImports.Unlock()
	}
	return imports, nil
}

var retainedGoImports struct {
	sync.Mutex
	entries map[string]retainedGoImportEntry
}

type retainedGoImportEntry struct {
	stamp   buildInputFileStamp
	imports []string
}

// retainedGoImportLimit bounds the retained import lists; exceeding it
// discards them all.
const retainedGoImportLimit = 32_768
