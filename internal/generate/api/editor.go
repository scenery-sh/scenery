package generateapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InspectEditorWorkspace reports whether the app root's generated Go editor
// workspace is present, current, and unconflicted.
func InspectEditorWorkspace(appRoot string) EditorWorkspaceStatus {
	root, err := filepath.Abs(appRoot)
	if err != nil {
		return EditorWorkspaceStatus{Conflict: true, Message: err.Error()}
	}
	status := EditorWorkspaceStatus{
		WorkFile:       filepath.Join(root, "go.work"),
		OwnerFile:      filepath.Join(root, ".scenery", "editor", "go-work-owner.json"),
		ParentWorkFile: findParentWorkFile(root),
	}
	owner, exists, err := ReadEditorWorkOwner(status.OwnerFile)
	if err != nil {
		status.Conflict, status.Message = true, err.Error()
		return status
	}
	if !exists {
		if pathExists(status.WorkFile) {
			status.Conflict, status.Message = true, "go.work is user-owned"
		}
		return status
	}
	data, fileExists, err := readOptionalFile(status.WorkFile)
	if err != nil {
		status.Conflict, status.Message = true, err.Error()
		return status
	}
	if !fileExists {
		status.Conflict, status.Message = true, "managed go.work is missing"
		return status
	}
	digest := contentDigest(data)
	if owner.Path != "go.work" || owner.Generator != EditorWorkspaceGenerator || digest != owner.Digest && digest != owner.PreviousDigest {
		status.Conflict, status.Message = true, "go.work ownership digest does not match"
		return status
	}
	status.Managed = true
	status.SpecRevision = owner.SpecRevision
	status.ContractRevision = owner.ContractRevision
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, string(filepath.Separator)+"generations"+string(filepath.Separator)) || strings.Contains(line, "/generations/") {
			status.Generation = strings.Trim(line, `"`)
			break
		}
	}
	return status
}

// IsManagedEditorWorkFile reports whether relative is a Scenery-owned root
// go.work or go.work.sum under appRoot.
func IsManagedEditorWorkFile(appRoot, relative string) bool {
	base := filepath.Base(filepath.ToSlash(relative))
	if base != "go.work" && base != "go.work.sum" {
		return false
	}
	return InspectEditorWorkspace(appRoot).Managed
}

// ReadEditorWorkOwner decodes a managed go.work ownership record.
func ReadEditorWorkOwner(path string) (EditorWorkOwner, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return EditorWorkOwner{}, false, nil
	}
	if err != nil {
		return EditorWorkOwner{}, false, err
	}
	var owner EditorWorkOwner
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&owner); err != nil {
		return EditorWorkOwner{}, false, fmt.Errorf("decode editor workspace owner: %w", err)
	}
	return owner, true, nil
}

func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readOptionalFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func findParentWorkFile(root string) string {
	for parent := filepath.Dir(root); parent != root; parent, root = filepath.Dir(parent), parent {
		candidate := filepath.Join(parent, "go.work")
		if pathExists(candidate) {
			return candidate
		}
	}
	return ""
}
