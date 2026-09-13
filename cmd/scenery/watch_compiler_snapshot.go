package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
)

// captureCompilerRevisionFiles adds the complete non-declaration membership
// selected by the last verified graph. It is separate from files because test
// and explicit revision inputs contribute identity without becoming ordinary
// runtime source synchronization policy.
func (snapshot *fileSnapshot) captureCompilerRevisionFiles(root string, previous fileSnapshot) {
	if snapshot.contract == nil {
		return
	}
	inputs, err := compiler.WorkspaceRevisionInputsWithGenerated(snapshot.contract, snapshot.generated)
	if err != nil {
		snapshot.compilerValid = false
		return
	}
	snapshot.compilerFiles = make(map[string]fileStamp, len(inputs))
	snapshot.compilerImpl = make(map[string]bool, len(inputs))
	snapshot.compilerAbsent = make(map[string]bool)
	for _, input := range inputs {
		rel := filepath.ToSlash(input.Path)
		if !input.Present {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
				snapshot.compilerValid = false
				continue
			}
			snapshot.compilerAbsent[rel] = input.Implementation
			continue
		}
		stamp, ok := snapshot.files[rel]
		if !ok {
			path := filepath.Join(root, filepath.FromSlash(rel))
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				snapshot.compilerValid = false
				continue
			}
			var reused bool
			stamp, reused = reusableStamp(previous.compilerFiles, rel, info, false)
			if !reused {
				stamp, _, err = stampWatchedFile(path, info, false)
				if err != nil {
					snapshot.compilerValid = false
					continue
				}
			}
		}
		snapshot.compilerFiles[rel] = stamp
		snapshot.compilerImpl[rel] = input.Implementation
	}
}

func snapshotFingerprint(snapshot fileSnapshot) string {
	paths := make([]string, 0, len(snapshot.files)+len(snapshot.compilerFiles)+len(snapshot.compilerAbsent))
	for path := range snapshot.files {
		paths = append(paths, "source\x00"+path)
	}
	for path := range snapshot.compilerFiles {
		if compilerFileAffectsRuntime(snapshot, snapshot, path) {
			paths = append(paths, "compiler\x00"+path)
		}
	}
	for path := range snapshot.compilerAbsent {
		if compilerFileAffectsRuntime(snapshot, snapshot, path) {
			paths = append(paths, "absent\x00"+path)
		}
	}
	sort.Strings(paths)
	h := sha256.New()
	var scratch []byte
	for _, namespacedPath := range paths {
		namespace, path, _ := strings.Cut(namespacedPath, "\x00")
		stamp := snapshot.files[path]
		if namespace == "compiler" {
			stamp = snapshot.compilerFiles[path]
		}
		scratch = append(scratch[:0], path...)
		scratch = append(scratch, 0)
		if namespace == "absent" {
			scratch = append(scratch, "absent"...)
			scratch = append(scratch, 0)
			_, _ = h.Write(scratch)
			continue
		}
		scratch = append(scratch, stamp.hash...)
		scratch = append(scratch, 0)
		scratch = strconv.AppendInt(scratch, stamp.size, 10)
		scratch = append(scratch, ':')
		scratch = strconv.AppendUint(scratch, uint64(stamp.mode), 8)
		scratch = append(scratch, ':')
		scratch = strconv.AppendBool(scratch, stamp.embed)
		scratch = append(scratch, 0)
		_, _ = h.Write(scratch)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func buildSourceSnapshot(snapshot fileSnapshot) *build.SourceSnapshot {
	convert := func(stamps map[string]fileStamp, implementation map[string]bool) map[string]build.SourceSnapshotFile {
		files := make(map[string]build.SourceSnapshotFile, len(stamps))
		for rel, stamp := range stamps {
			files[rel] = build.SourceSnapshotFile{
				Size:           stamp.size,
				ModTimeNano:    stamp.modTime.UnixNano(),
				Perm:           stamp.mode,
				Hash:           stamp.hash,
				Embedded:       stamp.embed,
				Implementation: implementation[rel],
				Data:           append([]byte(nil), stamp.data...),
			}
		}
		return files
	}
	implementation := make(map[string]bool, len(snapshot.files))
	for rel, stamp := range snapshot.files {
		implementation[rel] = implementationSnapshotFile(rel, stamp)
	}
	return &build.SourceSnapshot{
		Files:                  convert(snapshot.files, implementation),
		CompilerFiles:          convert(snapshot.compilerFiles, snapshot.compilerImpl),
		CompilerAbsent:         cloneBoolMap(snapshot.compilerAbsent),
		ContractFiles:          convert(snapshot.contractFiles, nil),
		ContractCompilerFiles:  convert(snapshot.contractCompiler, nil),
		ContractCompilerAbsent: cloneBoolMap(snapshot.contractCompilerAbsent),
		CompilerCaptureValid:   snapshot.compilerValid,
		Contract:               snapshot.contract,
	}
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
