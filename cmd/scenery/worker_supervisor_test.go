package main

import (
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/storageconfig"
)

// Go services keep ambient access to the app's configured stores; every other
// worker reaches storage only through declared Host capabilities.
func TestWorkerLaunchKeepsAmbientStorageOnlyForGo(t *testing.T) {
	t.Setenv(storageconfig.RuntimeConfigEnv, `{"kind":"scenery.storage.runtime"}`)
	for runtime, want := range map[string]bool{"go": true, "rust": false} {
		launch, err := projectWorkerLaunch(workerProgram{Service: "app/service/files", Runtime: runtime})
		if err != nil {
			t.Fatal(err)
		}
		got := slices.ContainsFunc(launch.Environment, func(entry string) bool {
			return strings.HasPrefix(entry, storageconfig.RuntimeConfigEnv+"=")
		})
		if got != want {
			t.Errorf("%s worker storage configuration = %t, want %t", runtime, got, want)
		}
	}
}
