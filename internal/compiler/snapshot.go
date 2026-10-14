package compiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

// SnapshotUnchanged verifies a local immutable result against current source
// membership, declaration bytes and the complete workspace revision. It does
// not re-expand a valid local graph. Invalid and registry-backed results
// re-read the compiler's complete diagnostic/lock/package validation.
func SnapshotUnchanged(result *Result) (bool, error) {
	return snapshotUnchanged(result, workspacetx.NormalRead)
}

// SnapshotUnchangedDuringChangeTransaction validates the captured revision
// while this process owns source/generated publication. It never reacquires
// the normal-reader gate against its own transaction.
func SnapshotUnchangedDuringChangeTransaction(result *Result) (bool, error) {
	return snapshotUnchanged(result, workspacetx.CurrentOwnerRead)
}

func snapshotUnchanged(result *Result, mode workspacetx.ReadMode) (unchanged bool, returnErr error) {
	if result == nil {
		return false, nil
	}
	if err := workspacetx.RecoverOrReject(result.Root, mode); err != nil {
		return false, err
	}
	// A captured file or directory disappearing is a changed snapshot. Preserve
	// other I/O failures so callers do not mislabel them as concurrent edits.
	defer func() {
		if errors.Is(returnErr, os.ErrNotExist) {
			unchanged, returnErr = false, nil
		}
	}()
	if result.ContractStatus != "valid" || result.Manifest == nil {
		// Invalid graphs may stop before loading every module. Re-run the raw
		// reader so diagnostic-only results also verify their captured inputs.
		current, err := compileResult(result.Root)
		if err != nil || current.Valid() || current.WorkspaceRevision != result.WorkspaceRevision || len(current.Sources) != len(result.Sources) {
			return false, err
		}
		for i, source := range result.Sources {
			if current.Sources[i].Path != source.Path || !bytes.Equal(current.Sources[i].Bytes, source.Bytes) {
				return false, nil
			}
		}
		before, err := canonicalDiagnosticSet(result.Diagnostics)
		if err != nil {
			return false, err
		}
		after, err := canonicalDiagnosticSet(current.Diagnostics)
		return err == nil && bytes.Equal(before, after), err
	}
	if unchanged, err := consumedResourceBytesUnchanged(result); err != nil || !unchanged {
		return false, err
	}
	sources := make(map[string][]byte)
	directories := make(map[string]bool)
	for _, source := range result.Sources {
		if source.External {
			// The gate above already admitted this read. Revalidate registry locks
			// without recursively invoking this same snapshot verification.
			current, err := compileResult(result.Root)
			if err != nil || !current.Valid() || current.WorkspaceRevision != result.WorkspaceRevision || current.Manifest.ContractRevision != result.Manifest.ContractRevision {
				return false, err
			}
			if len(current.Sources) != len(result.Sources) {
				return false, nil
			}
			for i := range result.Sources {
				if current.Sources[i].Path != result.Sources[i].Path || !bytes.Equal(current.Sources[i].Bytes, result.Sources[i].Bytes) {
					return false, nil
				}
			}
			return consumedResourceBytesUnchanged(current)
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

// Schema validation may enumerate independent errors in a different order.
// Their order cannot turn identical captured inputs into a revision conflict.
func canonicalDiagnosticSet(diagnostics []Diagnostic) ([]byte, error) {
	items := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		data, err := json.Marshal(diagnostic)
		if err != nil {
			return nil, err
		}
		items = append(items, string(data))
	}
	slices.Sort(items)
	return json.Marshal(items)
}

// SQL and renderer digests describe bytes consumed before expansion, rather
// than later bytes present when workspace hashing ran.
func consumedResourceBytesUnchanged(result *Result) (bool, error) {
	manifests := []*Manifest{result.Manifest}
	for _, view := range result.ViewManifests {
		manifests = append(manifests, view)
	}
	digests := map[string]string{}
	for _, manifest := range manifests {
		if manifest == nil {
			continue
		}
		resources := resourcesByAddress(manifest)
		for _, resource := range manifest.Resources {
			expected := stringValue(resource.Spec["implementation_digest"])
			if expected == "" {
				continue
			}
			var path string
			var err error
			switch resource.Kind {
			case "scenery.view":
				implementation, _ := resource.Spec["implementation"].(map[string]any)
				path, err = dataImplementationPath(result.Root, resources, resource, stringValue(implementation["file"]))
			case "scenery.renderer":
				path, err = rendererModulePath(result.Root, resources, resource)
			default:
				continue
			}
			if err != nil {
				return false, err
			}
			actual, ok := digests[path]
			if !ok {
				contents, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				sum := sha256.Sum256(contents)
				actual = "sha256:" + hex.EncodeToString(sum[:])
				digests[path] = actual
			}
			if actual != expected {
				return false, nil
			}
		}
	}
	return true, nil
}
