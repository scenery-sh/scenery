package nativebuilddriver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func rewriteEmbedCfg(source, target string, capture Capture) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	if _, ok := config["Patterns"]; !ok {
		return fmt.Errorf("embed configuration has no Patterns map")
	}
	var files map[string]string
	if raw, ok := config["Files"]; !ok {
		return fmt.Errorf("embed configuration has no Files map")
	} else if err := json.Unmarshal(raw, &files); err != nil {
		return fmt.Errorf("decode embedded files: %w", err)
	}
	if files == nil {
		return fmt.Errorf("embed configuration has a null Files map")
	}
	for name, original := range files {
		snapshot, digest := capturedSnapshot(capture, original)
		if snapshot == "" || digest == "" {
			return fmt.Errorf("embedded input is absent from the captured generation: %s", original)
		}
		info, err := os.Lstat(snapshot)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("embedded snapshot is unavailable: %s", snapshot)
		}
		files[name] = snapshot
	}
	encodedFiles, err := json.Marshal(files)
	if err != nil {
		return err
	}
	config["Files"] = encodedFiles
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, append(encoded, '\n'), 0o600)
}

func capturedSnapshot(capture Capture, original string) (string, string) {
	if snapshot := capture.SnapshotFiles[original]; snapshot != "" {
		return snapshot, capture.Files[original]
	}
	clean := filepath.Clean(original)
	for path, snapshot := range capture.SnapshotFiles {
		if filepath.Clean(path) == clean && snapshot != "" {
			return snapshot, capture.Files[path]
		}
	}
	return "", ""
}
