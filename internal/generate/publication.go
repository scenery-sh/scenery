package generate

import (
	"fmt"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/workspacetx"
)

type artifactRenderer func(*compiler.Result) ([]generatedFile, error)

// Generate under one ownership boundary, including the source snapshot and
// stale-file inspection. Check-only rendering never acquires a writing lock.
func generateFromRoot(root string, check bool, message string, render artifactRenderer) (GenerateResult, error) {
	return publishGenerated(root, check, message, func() ([]generatedFile, error) {
		compile := compiler.Compile
		if !check {
			compile = compiler.CompileDuringChangeTransaction
		}
		result, err := compile(root)
		if err != nil {
			return nil, err
		}
		if result.ContractStatus != "valid" || result.Manifest == nil {
			return nil, fmt.Errorf("cannot generate from invalid contract: %s", firstError(result.Diagnostics))
		}
		return render(result)
	})
}

func generateFromResult(result *compiler.Result, check bool, message string, render artifactRenderer) (GenerateResult, error) {
	if result == nil || result.ContractStatus != "valid" || result.Manifest == nil {
		return GenerateResult{}, fmt.Errorf("cannot generate from invalid contract")
	}
	if !check {
		// Unchanged outputs need validation, not a durable write transaction.
		// A real update still repeats preparation under the publication lock.
		unchanged, err := compiler.SnapshotUnchanged(result)
		if err != nil {
			return GenerateResult{}, err
		}
		if !unchanged {
			return GenerateResult{}, fmt.Errorf("revision_conflict: application changed after the generation snapshot; retry generation")
		}
		files, err := render(result)
		if err != nil {
			return GenerateResult{}, err
		}
		inspected, err := inspectGeneratedFiles(result.Root, files)
		if err != nil || len(inspected.Changed) == 0 {
			return inspected, err
		}
	}
	return publishGenerated(result.Root, check, message, func() ([]generatedFile, error) {
		if !check {
			current, err := compiler.CompileDuringChangeTransaction(result.Root)
			if err != nil {
				return nil, err
			}
			if err := requireGenerationSnapshot(result, current); err != nil {
				return nil, err
			}
		}
		return render(result)
	})
}

func requireGenerationSnapshot(expected, current *compiler.Result) error {
	if current.Manifest == nil || current.WorkspaceRevision != expected.WorkspaceRevision || current.Manifest.ContractRevision != expected.Manifest.ContractRevision {
		return fmt.Errorf("revision_conflict: application changed after the generation snapshot; retry generation")
	}
	return nil
}

func publishGenerated(root string, check bool, message string, render func() ([]generatedFile, error)) (GenerateResult, error) {
	result := GenerateResult{Changed: []string{}, Checked: []string{}}
	prepare := func() ([]workspacetx.File, error) {
		files, err := render()
		if err != nil {
			return nil, err
		}
		result, err = inspectGeneratedFiles(root, files)
		if err != nil {
			return nil, err
		}
		if check && len(result.Changed) > 0 {
			return nil, fmt.Errorf("%s: %s", message, strings.Join(result.Changed, ", "))
		}
		changed := make(map[string]bool, len(result.Changed))
		for _, relative := range result.Changed {
			changed[relative] = true
		}
		var updates []generatedFile
		for _, file := range files {
			relative, _ := filepath.Rel(root, file.Path)
			if changed[filepath.ToSlash(relative)] {
				updates = append(updates, file)
			}
		}
		return transactionFiles(root, updates)
	}
	var err error
	if check {
		_, err = prepare()
	} else {
		err = workspacetx.Publish(root, prepare)
	}
	return result, err
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
