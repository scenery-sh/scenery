package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func WriteManifest(session Session) error {
	if session.StateRoot == "" {
		return nil
	}
	if err := os.MkdirAll(session.StateRoot, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(filepath.Join(session.StateRoot, "manifest.json"), data, 0o644)
}
