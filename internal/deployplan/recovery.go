package deployplan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasLegacyAPIVersion(encoded []byte, want string) bool {
	var identity struct {
		APIVersion string `json:"api_version"`
	}
	return json.Unmarshal(encoded, &identity) == nil && identity.APIVersion == want
}

func legacyRecoveryStateError(family, path string) error {
	return fmt.Errorf("failed_precondition: legacy %s recovery state at %s must be recovered with the previous Scenery binary before using this binary; no state was modified", family, filepath.ToSlash(path))
}

// CheckLegacyRecoveryState refuses deployment work when interrupted state
// requires the previous recovery implementation. It never mutates state.
func CheckLegacyRecoveryState(root string) error {
	if err := checkLegacyRecoveryFile(filepath.Join(root, ".scenery", "deployments", "apply.lock"), "deployment", "scenery.deployment-apply-lock/v1"); err != nil {
		return err
	}
	directory := filepath.Join(root, ".scenery", "deployments", "journal")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed_precondition: inspect deployment recovery state before replacing Scenery: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := checkLegacyRecoveryFile(filepath.Join(directory, entry.Name()), "deployment", "scenery.deployment-apply-journal/v1"); err != nil {
			return err
		}
	}
	return nil
}

func checkLegacyRecoveryFile(path, family, apiVersion string) error {
	encoded, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed_precondition: inspect %s recovery state before replacing Scenery: %w", family, err)
	}
	if hasLegacyAPIVersion(encoded, apiVersion) {
		return legacyRecoveryStateError(family, path)
	}
	return nil
}
