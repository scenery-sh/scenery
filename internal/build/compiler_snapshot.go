package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/workspacetx"
)

// Startup discovers watch inputs from a complete graph before build
// preparation. Reuse that graph only when the current captured non-
// implementation inputs still equal its baseline; stale snapshots compile anew.
func compileWorkspaceContract(root string, snapshot *SourceSnapshot) (*compiler.Result, error) {
	if snapshot != nil && snapshot.Contract != nil && filepath.Clean(snapshot.Contract.Root) == filepath.Clean(root) {
		unchanged := snapshot.CompilerCaptureValid && capturedContractInputsUnchanged(snapshot)
		for _, source := range snapshot.Contract.Sources {
			if source != nil && source.External {
				unchanged = false
				break
			}
		}
		if unchanged {
			// Implementation checking adds diagnostics/status to the build's
			// result. Do not mutate the source snapshot's diagnostic ownership.
			result := *snapshot.Contract
			manifest := *result.Manifest
			result.Manifest = &manifest
			result.Diagnostics = slices.Clone(result.Diagnostics)
			manifest.Diagnostics = slices.Clone(manifest.Diagnostics)
			captured := make(map[string][]byte, len(snapshot.CompilerFiles))
			for rel, file := range snapshot.CompilerFiles {
				data, err := exactCapturedBytes(rel, file)
				if err != nil {
					return nil, err
				}
				captured[rel] = data
			}
			if err := compiler.BindCapturedWorkspaceRevision(&result, captured); err != nil {
				return nil, err
			}
			return &result, nil
		}
	}
	if snapshot == nil {
		return compiler.Check(root)
	}
	return compileCapturedWorkspaceContract(root, snapshot)
}

// compileCapturedWorkspaceContract gives graph compilation the same immutable
// bytes consumed by later preparation stages. The temporary root is private to
// this computation; the returned graph is rebound to the authored root before
// it can become a watcher baseline or appear in diagnostics.
func compileCapturedWorkspaceContract(root string, snapshot *SourceSnapshot) (*compiler.Result, error) {
	if snapshot == nil || !snapshot.CompilerCaptureValid {
		return nil, fmt.Errorf("captured compiler input set is unavailable")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := workspacetx.RecoverOrReject(root, workspacetx.NormalRead); err != nil {
		return nil, err
	}
	cacheRoot, err := CacheRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return nil, err
	}
	stagedRoot, err := os.MkdirTemp(cacheRoot, ".compiler-snapshot-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stagedRoot) }()

	present := make(map[string]SourceSnapshotFile, len(snapshot.Files)+len(snapshot.CompilerFiles))
	for rel, file := range snapshot.Files {
		present[rel] = file
	}
	for rel, file := range snapshot.CompilerFiles {
		if current, ok := present[rel]; ok && !sameCapturedCompilerFile(current, file) {
			return nil, fmt.Errorf("captured compiler input has conflicting bytes: %s", rel)
		}
		present[rel] = file
	}
	for rel := range snapshot.CompilerAbsent {
		if _, ok := present[rel]; ok {
			return nil, fmt.Errorf("captured compiler input is both present and absent: %s", rel)
		}
	}
	for rel, file := range present {
		clean, err := safeCapturedCompilerPath(rel)
		if err != nil {
			return nil, err
		}
		data, err := exactCapturedBytes(clean, file)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(stagedRoot, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		perm := os.FileMode(file.Perm)
		if perm == 0 {
			perm = 0o644
		}
		if err := os.WriteFile(path, data, perm); err != nil {
			return nil, err
		}
	}
	for rel := range snapshot.CompilerAbsent {
		clean, err := safeCapturedCompilerPath(rel)
		if err != nil {
			return nil, err
		}
		if _, err := os.Lstat(filepath.Join(stagedRoot, filepath.FromSlash(clean))); !os.IsNotExist(err) {
			return nil, fmt.Errorf("captured compiler absence is not exact for %s", clean)
		}
	}

	result, err := compiler.Check(stagedRoot)
	if err != nil {
		return nil, err
	}
	result.Root = root
	for _, source := range result.Sources {
		if source == nil || source.External {
			continue
		}
		source.Path = filepath.Join(root, filepath.FromSlash(source.Relative))
	}
	return result, nil
}

func safeCapturedCompilerPath(rel string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if rel == "" || clean != filepath.ToSlash(rel) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("captured compiler input escapes the workspace: %s", rel)
	}
	return clean, nil
}

func sameCapturedCompilerFile(a, b SourceSnapshotFile) bool {
	return capturedFileIdentityValid(a) && capturedFileIdentityValid(b) && a.Size == b.Size && a.Perm == b.Perm && a.Hash == b.Hash
}

func capturedContractInputsUnchanged(snapshot *SourceSnapshot) bool {
	currentFiles := make(map[string]SourceSnapshotFile)
	for rel, file := range snapshot.Files {
		if !file.Implementation {
			currentFiles[rel] = file
		}
	}
	currentCompiler := make(map[string]SourceSnapshotFile)
	for rel, file := range snapshot.CompilerFiles {
		if !file.Implementation {
			currentCompiler[rel] = file
		}
	}
	currentAbsent := make(map[string]bool)
	for rel, implementation := range snapshot.CompilerAbsent {
		if !implementation {
			currentAbsent[rel] = false
		}
	}
	return capturedFileMapsEqual(currentFiles, snapshot.ContractFiles) &&
		capturedFileMapsEqual(currentCompiler, snapshot.ContractCompilerFiles) &&
		boolMapsEqual(currentAbsent, snapshot.ContractCompilerAbsent)
}

func boolMapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func capturedFileMapsEqual(a, b map[string]SourceSnapshotFile) bool {
	if len(a) != len(b) {
		return false
	}
	for rel, file := range a {
		other, ok := b[rel]
		if !ok || !capturedFileIdentityValid(file) || file.Size != other.Size || file.Perm != other.Perm || file.Hash != other.Hash || file.Embedded != other.Embedded {
			return false
		}
	}
	return true
}

func exactCapturedBytes(rel string, file SourceSnapshotFile) ([]byte, error) {
	if file.Data == nil {
		return nil, fmt.Errorf("captured compiler bytes are unavailable for %s", rel)
	}
	if !capturedFileIdentityValid(file) {
		return nil, fmt.Errorf("captured compiler identity does not match bytes for %s", rel)
	}
	return append([]byte(nil), file.Data...), nil
}

func capturedFileIdentityValid(file SourceSnapshotFile) bool {
	if file.Data == nil {
		return false
	}
	digest := sha256.Sum256(file.Data)
	return int64(len(file.Data)) == file.Size && hex.EncodeToString(digest[:]) == file.Hash
}

// CompileContractWithSnapshot returns the canonical graph used by the build.
// The optional startup result is reused only after complete captured-input
// verification; callers still perform their supersession gate before candidate
// publication.
func CompileContractWithSnapshot(root string, snapshot *SourceSnapshot) (*compiler.Result, error) {
	return compileWorkspaceContract(root, snapshot)
}

func CompileContractWithSnapshotContext(ctx context.Context, root string, snapshot *SourceSnapshot) (*compiler.Result, error) {
	return observeBuild(ctx, "contract.check", func() (*compiler.Result, error) {
		return compileWorkspaceContract(root, snapshot)
	})
}
