package build

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWorkspaceAdoptsAProcessExecutableAnotherWorkspaceLinked(t *testing.T) {
	previous := sharedDevelopmentProcessRoot
	t.Cleanup(func() { sharedDevelopmentProcessRoot = previous })
	root := t.TempDir()
	shared := filepath.Join(root, "machine", "process-executables")
	sharedDevelopmentProcessRoot = func() string { return shared }
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linked := &DevelopmentProcess{Name: "echo", Binary: filepath.Join(first, "echo-0123456789abcdef")}
	if err := os.WriteFile(linked.Binary, []byte("linked executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest, _, err := fileDigest(linked.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishDevelopmentProcessDigest(linked.Binary, digest); err != nil {
		t.Fatal(err)
	}
	shareDevelopmentProcesses([]*DevelopmentProcess{linked})

	same := &DevelopmentProcess{Name: "echo", Binary: filepath.Join(second, "echo-0123456789abcdef")}
	other := &DevelopmentProcess{Name: "echo", Binary: filepath.Join(second, "echo-fedcba9876543210")}
	remaining := adoptSharedDevelopmentProcesses(t.Context(), []*DevelopmentProcess{same, other})
	if len(remaining) != 1 || remaining[0] != other || same.ArtifactDigest != digest || other.ArtifactDigest != "" {
		t.Fatalf("remaining %d, adopted digest %q, other digest %q", len(remaining), same.ArtifactDigest, other.ArtifactDigest)
	}
	if contents, err := os.ReadFile(same.Binary); err != nil || string(contents) != "linked executable" {
		t.Fatalf("adopted executable = %q, %v", contents, err)
	}
	if info, err := os.Lstat(same.Binary); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o755 {
		t.Fatalf("adopted executable is not an independent executable file: %v, %v", info, err)
	}
	if reused, ok, err := retainedDevelopmentProcessDigest(same.Binary); err != nil || !ok || reused != digest {
		t.Fatalf("adopted executable is not reusable: %q, %t, %v", reused, ok, err)
	}
	// The adopting workspace owns its bytes: the store and the first
	// workspace keep theirs when it changes them.
	if err := os.WriteFile(same.Binary, []byte("changed here"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{linked.Binary, filepath.Join(shared, filepath.Base(linked.Binary))} {
		if contents, err := os.ReadFile(path); err != nil || string(contents) != "linked executable" {
			t.Fatalf("%s follows the adopting workspace: %q, %v", path, contents, err)
		}
	}
	// Bytes that are not the recorded ones, or an entry without a record,
	// are linked again instead of adopted.
	stored := filepath.Join(shared, filepath.Base(linked.Binary))
	if err := os.WriteFile(stored, []byte("tampered executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	third := &DevelopmentProcess{Name: "echo", Binary: filepath.Join(root, "third", "echo-0123456789abcdef")}
	if err := os.MkdirAll(filepath.Dir(third.Binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if remaining := adoptSharedDevelopmentProcesses(t.Context(), []*DevelopmentProcess{third}); len(remaining) != 1 || third.ArtifactDigest != "" {
		t.Fatal("tampered shared bytes were adopted")
	}
	if entries, err := os.ReadDir(filepath.Dir(third.Binary)); err != nil || len(entries) != 0 {
		t.Fatalf("a refused adoption left %d files (%v)", len(entries), err)
	}
	if err := os.Remove(stored + developmentProcessDigestSuffix); err != nil {
		t.Fatal(err)
	}
	if remaining := adoptSharedDevelopmentProcesses(t.Context(), []*DevelopmentProcess{third}); len(remaining) != 1 {
		t.Fatal("an executable without a digest record was adopted")
	}
	// Sharing never replaces an entry and keeps the most recently used.
	shareDevelopmentProcesses([]*DevelopmentProcess{linked})
	if contents, err := os.ReadFile(stored); err != nil || string(contents) != "tampered executable" {
		t.Fatalf("sharing replaced an existing entry: %q, %v", contents, err)
	}
	for index := range developmentProcessShareLimit + 3 {
		name := filepath.Join(shared, "old-"+strconv.Itoa(index))
		if err := os.WriteFile(name, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name+developmentProcessDigestSuffix, []byte("sha256:x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().Add(-time.Duration(index+1) * time.Hour)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	pruneSharedDevelopmentProcesses(shared)
	entries, err := os.ReadDir(shared)
	if err != nil {
		t.Fatal(err)
	}
	executables := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != developmentProcessDigestSuffix {
			executables++
		}
	}
	if _, err := os.Lstat(filepath.Join(shared, "old-"+strconv.Itoa(developmentProcessShareLimit+2))); executables != developmentProcessShareLimit || err == nil {
		t.Fatalf("the store keeps %d executables, limit %d; the oldest removed: %t", executables, developmentProcessShareLimit, err != nil)
	}
	// Without a store nothing is adopted or shared.
	sharedDevelopmentProcessRoot = func() string { return "" }
	if remaining := adoptSharedDevelopmentProcesses(t.Context(), []*DevelopmentProcess{third}); len(remaining) != 1 {
		t.Fatal("a process was adopted without a store")
	}
	shareDevelopmentProcesses([]*DevelopmentProcess{linked})
}
