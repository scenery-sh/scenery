package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLegacyRecoveryStateFindsEveryUpgradeHazard(t *testing.T) {
	for _, test := range []struct {
		name, relative, apiVersion string
	}{
		{name: "change lock", relative: filepath.Join("transactions", "change.lock"), apiVersion: "scenery.change-transaction-lock/v1"},
		{name: "change journal", relative: filepath.Join("transactions", "change-apply.json"), apiVersion: "scenery.change-transaction/v1"},
		{name: "deployment lock", relative: filepath.Join("deployments", "apply.lock"), apiVersion: "scenery.deployment-apply-lock/v1"},
		{name: "deployment journal", relative: filepath.Join("deployments", "journal", "legacy.json"), apiVersion: "scenery.deployment-apply-journal/v1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".scenery", test.relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			legacy := []byte(`{"api_version":"` + test.apiVersion + `","sentinel":"keep"}`)
			if err := os.WriteFile(path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}

			err := checkLegacyRecoveryState(root)
			if err == nil || !strings.Contains(err.Error(), "previous Scenery binary") || !strings.Contains(err.Error(), "no state was modified") {
				t.Fatalf("CheckLegacyRecoveryState() error = %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != string(legacy) {
				t.Fatalf("legacy state changed: got %q, err %v", got, readErr)
			}
		})
	}
}
