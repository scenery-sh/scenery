package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/runtimeassets"
)

func TestRestoredAssistantTreeIsVerifiedAndIndependent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "cache", "tree")
	write := func(relative, contents string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(source, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("node_modules/pkg/index.js", "module.exports = 1\n", 0o644)
	write("node_modules/pkg/bin/tool", "#!/bin/sh\n", 0o755)
	write(".output/server/index.mjs", "export {}\n", 0o644)
	if err := os.Symlink("../pkg/bin/tool", filepath.Join(source, "node_modules", "pkg", "linked")); err != nil {
		t.Fatal(err)
	}
	descriptor, err := runtimeassets.DescribeTree(source)
	if err != nil {
		t.Fatal(err)
	}
	restore := func(name string) (string, assistantCopyStats, error) {
		destination := filepath.Join(root, name)
		if err := os.Mkdir(destination, 0o755); err != nil {
			t.Fatal(err)
		}
		stats := assistantCopyStats{Started: time.Now()}
		err := restoreAssistantPreparedTree(t.Context(), source, destination, &descriptor, &stats)
		return destination, stats, err
	}
	destination, stats, err := restore("first")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := runtimeassets.DescribeTree(destination)
	if err != nil || restored.Digest != descriptor.Digest || stats.Files != 3 || stats.Entries != len(descriptor.Entries) {
		t.Fatalf("restored digest %q, want %q; %d files, %d entries (%v)", restored.Digest, descriptor.Digest, stats.Files, stats.Entries, err)
	}
	// A write to the restored copy must not reach the cache.
	if err := os.WriteFile(filepath.Join(destination, "node_modules", "pkg", "index.js"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if kept, err := os.ReadFile(filepath.Join(source, "node_modules", "pkg", "index.js")); err != nil || string(kept) != "module.exports = 1\n" {
		t.Fatalf("the cache follows its restored copy: %q, %v", kept, err)
	}
	// Bytes that are not the recorded ones are refused however they are restored.
	write("node_modules/pkg/index.js", "module.exports = 2\n", 0o644)
	if _, _, err := restore("tampered"); err == nil || !strings.Contains(err.Error(), "content mismatch") {
		t.Fatalf("tampered cache bytes were restored: %v", err)
	}
	write("node_modules/pkg/index.js", "module.exports = 1\n", 0o644)
	write("node_modules/pkg/extra.js", "extra\n", 0o644)
	if _, _, err := restore("extra"); err == nil {
		t.Fatal("a file the record does not name was restored")
	}
	if err := os.Remove(filepath.Join(source, "node_modules", "pkg", "extra.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, ".output", "server", "index.mjs")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := restore("missing"); err == nil {
		t.Fatal("a tree without a recorded file was restored")
	}
}

func TestClonedAssistantTreeReportsWhatItCreated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source, destination := filepath.Join(root, "tree"), filepath.Join(root, "overlay")
	for _, dir := range []string{filepath.Join(source, "node_modules", "pkg"), destination} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "node_modules", "pkg", "index.js"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor, err := runtimeassets.DescribeTree(source)
	if err != nil {
		t.Fatal(err)
	}
	stats := assistantCopyStats{}
	cloned, err := cloneAssistantPreparedTree(t.Context(), source, destination, &descriptor, &stats)
	if errors.Is(err, errors.ErrUnsupported) {
		// No directory clone here: the name it tried is reported for removal.
		if len(cloned) != 1 || cloned[0] != "node_modules" {
			t.Fatalf("unsupported clone reported %v", cloned)
		}
		return
	}
	if err != nil || len(cloned) != 1 || cloned[0] != "node_modules" || stats.Files != 1 {
		t.Fatalf("clone = %v, %v, %d files", cloned, err, stats.Files)
	}
	// An overlay that already has the path is never merged into.
	if _, err := cloneAssistantPreparedTree(t.Context(), source, destination, &descriptor, &assistantCopyStats{}); err == nil {
		t.Fatal("a second clone merged into an existing tree")
	}
}

func TestWorktreeAdoptsASharedAssistantCacheEntryAndStillVerifiesIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	built := filepath.Join(root, "first", "prepared", "key")
	tree := filepath.Join(built, "tree", "node_modules", "pkg")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor, err := runtimeassets.DescribeTree(filepath.Join(built, "tree"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(assistantOverlayCacheRecord{Key: "key", Tree: descriptor})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(built, "record.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "machine", "assistant-prepared", "key")
	first := &assistantOverlayCache{path: built, key: "key", shared: shared}
	first.share(t.Context())
	if _, err := os.Stat(filepath.Join(shared, "tree", "node_modules", "pkg", "index.js")); err != nil {
		t.Fatalf("the built entry was not shared: %v", err)
	}
	// A second worktree has no entry of its own and restores the shared one.
	second := &assistantOverlayCache{path: filepath.Join(root, "second", "prepared", "key"), key: "key", shared: shared}
	overlay := filepath.Join(root, "overlay")
	if err := os.Mkdir(overlay, 0o755); err != nil {
		t.Fatal(err)
	}
	hit, err := second.restore(t.Context(), overlay)
	if err != nil || !hit {
		t.Fatalf("restore from the shared entry = %t, %v", hit, err)
	}
	if contents, err := os.ReadFile(filepath.Join(overlay, "node_modules", "pkg", "index.js")); err != nil || string(contents) != "module.exports = 1\n" {
		t.Fatalf("restored file = %q, %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(second.path, "record.json")); err != nil {
		t.Fatalf("the adopted entry is not in the worktree's cache: %v", err)
	}
	// Sharing never replaces an entry, and a shared entry of other bytes or of
	// another key is refused by the same verification as a local one.
	if err := os.WriteFile(filepath.Join(shared, "tree", "node_modules", "pkg", "index.js"), []byte("module.exports = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first.share(t.Context())
	tampered := &assistantOverlayCache{path: filepath.Join(root, "third", "prepared", "key"), key: "key", shared: shared}
	other := filepath.Join(root, "overlay-tampered")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if hit, err := tampered.restore(t.Context(), other); err == nil || hit {
		t.Fatalf("tampered shared bytes were restored: %t, %v", hit, err)
	}
	foreign := &assistantOverlayCache{path: filepath.Join(root, "fourth", "prepared", "other"), key: "other", shared: shared}
	third := filepath.Join(root, "overlay-foreign")
	if err := os.Mkdir(third, 0o755); err != nil {
		t.Fatal(err)
	}
	if hit, err := foreign.restore(t.Context(), third); err == nil || hit {
		t.Fatalf("a shared entry of another key was restored: %t, %v", hit, err)
	}
	// Without a shared entry a miss stays a miss.
	absent := &assistantOverlayCache{path: filepath.Join(root, "fifth", "prepared", "key"), key: "key", shared: filepath.Join(root, "machine", "assistant-prepared", "absent")}
	if hit, err := absent.restore(t.Context(), third); err != nil || hit {
		t.Fatalf("a miss without a shared entry = %t, %v", hit, err)
	}
}
