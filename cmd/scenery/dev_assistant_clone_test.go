package main

import (
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
