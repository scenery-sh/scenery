package compiler

import (
	"bytes"
	"os"
	"path/filepath"

	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

// SnapshotUnchanged verifies a local immutable result against current source
// membership, declaration bytes and the complete workspace revision. It does
// not re-expand an identical graph. Registry-backed results retain full lock
// and package-cache validation through the ordinary compiler.
func SnapshotUnchanged(result *Result) (bool, error) {
	if result == nil || !result.Valid() {
		return false, nil
	}
	if err := workspacetx.RecoverOrReject(result.Root, workspacetx.NormalRead); err != nil {
		return false, err
	}
	sources := make(map[string][]byte)
	directories := make(map[string]bool)
	for _, source := range result.Sources {
		if source.External {
			current, err := Compile(result.Root)
			return err == nil && current.Manifest != nil && current.WorkspaceRevision == result.WorkspaceRevision && current.Manifest.ContractRevision == result.Manifest.ContractRevision, err
		}
		sources[source.Path] = source.Bytes
		directories[filepath.Dir(source.Path)] = true
	}
	for directory := range directories {
		if err := scn.RejectPathSymlinks(result.Root, directory); err != nil {
			return false, err
		}
		paths, err := scn.SourceFiles(directory, directory == result.Root)
		if err != nil {
			return false, err
		}
		for _, path := range paths {
			expected, ok := sources[path]
			if !ok {
				return false, nil
			}
			current, err := os.ReadFile(path)
			if err != nil {
				return false, err
			}
			if !bytes.Equal(current, expected) {
				return false, nil
			}
			delete(sources, path)
		}
	}
	if len(sources) != 0 {
		return false, nil
	}
	revision, err := computeWorkspaceRevision(result.Root, result.Sources)
	return err == nil && revision == result.WorkspaceRevision, err
}
