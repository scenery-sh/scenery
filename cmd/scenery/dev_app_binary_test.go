package main

import (
	"os"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestSessionAppBinaryRetainsExactBytesAcrossCacheReplacement(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cached-app")
	session := &localagent.Session{StateRoot: filepath.Join(root, "session")}
	if err := os.WriteFile(source, []byte("previous executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	previous, err := prepareSessionAppBinary(session, source)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise an in-place overwrite too, not only go build's atomic rename.
	if err := os.WriteFile(source, []byte("candidate executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareSessionAppBinary(session, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if previous == candidate {
		t.Fatal("different executable bytes reused the retained generation path")
	}
	for path, want := range map[string]string{previous: "previous executable", candidate: "candidate executable"} {
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != want {
			t.Fatalf("retained %s = %q: %v", path, contents, err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("retained executable is not independent regular bytes: %v", err)
		}
	}
}
