package generate

import (
	"fmt"
	"path/filepath"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/workspacetx"
)

type artifactRenderer func(*compiler.Result) ([]generatedFile, error)

func validateGenerationSnapshot(result *compiler.Result, owner bool) error {
	verify := compiler.SnapshotUnchanged
	if owner {
		verify = compiler.SnapshotUnchangedDuringChangeTransaction
	}
	unchanged, err := verify(result)
	if err != nil {
		return err
	}
	if !unchanged {
		return fmt.Errorf("revision_conflict: application changed after the generation snapshot; retry generation")
	}
	return nil
}

func transactionFiles(root string, files []generatedFile) ([]workspacetx.File, error) {
	transaction := make([]workspacetx.File, 0, len(files))
	for _, file := range files {
		if err := rejectGeneratedPathSymlinks(root, file.Path); err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, file.Path)
		if err != nil {
			return nil, err
		}
		transaction = append(transaction, workspacetx.File{Path: filepath.ToSlash(relative), Bytes: file.Bytes, Remove: file.Remove})
	}
	return transaction, nil
}
