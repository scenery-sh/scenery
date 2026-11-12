package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	previous, err := prepareSessionAppBinary(session, source, "")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise an in-place overwrite too, not only go build's atomic rename.
	if err := os.WriteFile(source, []byte("candidate executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareSessionAppBinary(session, source, "")
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

func TestSessionAppBinaryUsesValidatedProducerDigest(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "cached-app")
	contents := []byte("candidate executable")
	if err := os.WriteFile(source, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	path, err := prepareSessionAppBinary(&localagent.Session{StateRoot: filepath.Join(root, "session")}, source, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(contents) {
		t.Fatalf("retained executable = %q: %v", got, err)
	}
	if _, err := prepareSessionAppBinary(&localagent.Session{StateRoot: filepath.Join(root, "other")}, source, "not-a-digest"); err == nil {
		t.Fatal("invalid producer digest accepted")
	}
	// Retained bytes of the digest are verified and used without another copy.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	again, err := prepareSessionAppBinary(&localagent.Session{StateRoot: filepath.Join(root, "session")}, source, hex.EncodeToString(sum[:]))
	if err != nil || again != path {
		t.Fatalf("retained executable of the same digest = %q, %v; want %q", again, err, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := path + ".replacement"
	changed := append([]byte(nil), contents...)
	changed[0] ^= 1
	if err := os.WriteFile(replacement, changed, info.Mode()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSessionAppBinary(&localagent.Session{StateRoot: filepath.Join(root, "session")}, source, hex.EncodeToString(sum[:])); err == nil {
		t.Fatal("changed retained bytes were used")
	}
}

func TestClosingSessionKeepsOnlyItsLastGenerationExecutables(t *testing.T) {
	session := &localagent.Session{StateRoot: filepath.Join(t.TempDir(), "session")}
	dir := filepath.Join(session.StateRoot, "run", "app")
	if err := os.MkdirAll(filepath.Join(dir, "scenery-app-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	served, replaced, foreign := filepath.Join(dir, "scenery-app-served"), filepath.Join(dir, "scenery-app-replaced"), filepath.Join(dir, "operator-notes")
	for _, path := range []string{served, replaced, foreign} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	releaseSessionAppBinariesExcept(session, map[string]bool{served: true})
	for path, want := range map[string]bool{served: true, replaced: false, foreign: true, filepath.Join(dir, "scenery-app-directory"): true} {
		if _, err := os.Lstat(path); (err == nil) != want {
			t.Fatalf("%s kept = %t, want %t (%v)", filepath.Base(path), err == nil, want, err)
		}
	}
	// A session without retained state has nothing to release.
	releaseSessionAppBinariesExcept(nil, nil)
	releaseSessionAppBinariesExcept(&localagent.Session{StateRoot: filepath.Join(t.TempDir(), "absent")}, nil)
}
