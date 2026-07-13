package evolution

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func atomicWrite(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return atomicWriteSynced(path, data, mode)
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

// CheckLegacyRecoveryState refuses mutation work when interrupted state still
// requires the previous recovery implementation. It never mutates state.
func CheckLegacyRecoveryState(root string) error {
	for _, file := range []struct{ path, version string }{
		{filepath.Join(root, ".scenery", "transactions", "change.lock"), "scenery.change-transaction-lock/v1"},
		{filepath.Join(root, ".scenery", "transactions", "change-apply.json"), "scenery.change-transaction/v1"},
	} {
		encoded, err := os.ReadFile(file.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed_precondition: inspect change transaction recovery state before replacing Scenery: %w", err)
		}
		if hasLegacyAPIVersion(encoded, file.version) {
			return legacyRecoveryStateError("change transaction", file.path)
		}
	}
	return nil
}
