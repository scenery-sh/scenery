package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/atomicfile"
)

const (
	workspaceMarkerFile = ".scenery-workspace.json"
	workspaceMarkerKind = "scenery.build.workspace"
)

// WorkspaceMarker records which application a private development workspace
// serves. The workspace directory name only hashes the app root, so without
// the marker nothing can tell whether that root still exists. The marker is
// disposable cache state, not an API; prune reads it, nothing else does.
type WorkspaceMarker struct {
	Kind    string `json:"kind"`
	AppRoot string `json:"app_root"`
	AppName string `json:"app_name"`
}

// WorkspaceMarkerPath is the marker of the workspace directory.
func WorkspaceMarkerPath(workspace string) string {
	return filepath.Join(workspace, workspaceMarkerFile)
}

// WriteWorkspaceMarker records appRoot and appName in the workspace. The
// caller holds the workspace lock. An unchanged marker is left untouched so
// its modification time stays meaningful.
func WriteWorkspaceMarker(workspace, appRoot, appName string) error {
	absRoot, err := filepath.Abs(appRoot)
	if err != nil {
		return err
	}
	marker := WorkspaceMarker{Kind: workspaceMarkerKind, AppRoot: absRoot, AppName: strings.TrimSpace(appName)}
	if current, err := ReadWorkspaceMarker(workspace); err == nil && current == marker {
		return nil
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(WorkspaceMarkerPath(workspace), append(data, '\n'), 0o644, atomicfile.Options{})
}

// ReadWorkspaceMarker returns the marker of the workspace directory. A
// missing marker reports os.ErrNotExist; any other shape is an error.
func ReadWorkspaceMarker(workspace string) (WorkspaceMarker, error) {
	data, err := os.ReadFile(WorkspaceMarkerPath(workspace))
	if err != nil {
		return WorkspaceMarker{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var marker WorkspaceMarker
	if err := decoder.Decode(&marker); err != nil {
		return WorkspaceMarker{}, fmt.Errorf("decode workspace marker %s: %w", WorkspaceMarkerPath(workspace), err)
	}
	if marker.Kind != workspaceMarkerKind || strings.TrimSpace(marker.AppRoot) == "" || !filepath.IsAbs(marker.AppRoot) {
		return WorkspaceMarker{}, errors.New("workspace marker does not record an absolute app root: " + WorkspaceMarkerPath(workspace))
	}
	return marker, nil
}
