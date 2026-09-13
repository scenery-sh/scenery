package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"scenery.sh/internal/build"
)

func TestWatchCapturePreservesEmptyFilesOnRescan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, ".gitignore", "")
	writeWatchFile(t, root, "svc/embed.go", "package svc\nimport _ \"embed\"\n//go:embed empty.txt\nvar asset []byte\n")
	writeWatchFile(t, root, "svc/empty.txt", "")
	previous := fileSnapshot{}
	for _, phase := range []string{"fresh", "cached"} {
		current, err := scanWatchedFilesReusing(root, previous)
		if err != nil {
			t.Fatal(err)
		}
		// Cover both source and compiler/baseline conversion, independently of
		// whether the graph selected either path as a workspace revision input.
		current.compilerFiles, current.contractFiles, current.contractCompiler = current.files, current.files, current.files
		captured := buildSourceSnapshot(current)
		for _, files := range []map[string]build.SourceSnapshotFile{captured.Files, captured.CompilerFiles, captured.ContractFiles, captured.ContractCompilerFiles} {
			for _, path := range []string{".gitignore", "svc/empty.txt"} {
				file, ok := files[path]
				emptyHash := sha256.Sum256(nil)
				if !ok || file.Data == nil || len(file.Data) != 0 || file.Size != 0 || file.Hash != hex.EncodeToString(emptyHash[:]) {
					t.Fatalf("%s %s lost present empty bytes: present=%t nil=%t size=%d hash=%s", phase, path, ok, file.Data == nil, file.Size, file.Hash)
				}
			}
		}
		previous = current
	}
}
