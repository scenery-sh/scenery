package main

import "testing"

// scenery up must rebuild on .scn-only edits: .scn is the singular app
// model, and until 2026-07-15 the watch set silently omitted it (verified
// live — an app contract comment edit produced no rebuild while Go edits did).
func TestIsWatchedFileIncludesScenerySources(t *testing.T) {
	t.Parallel()

	watched := []string{
		testAppFilename,
		"service/" + testPackageFilename,
		"service/extra.scn",
		"main.go",
		".scenery.json",
	}
	for _, rel := range watched {
		if !isWatchedFile(rel) {
			t.Errorf("isWatchedFile(%q) = false, want true", rel)
		}
	}
	unwatched := []string{
		testAppLockFilename,
		"nested/" + testAppLockFilename,
		"apps/web/src/App.tsx",
	}
	for _, rel := range unwatched {
		if isWatchedFile(rel) {
			t.Errorf("isWatchedFile(%q) = true, want false", rel)
		}
	}
}
