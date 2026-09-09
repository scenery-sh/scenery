package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// Export only authored disposable legacy data, then import through the current
// public CLI. Never discover or inspect a developer's legacy cells.
func storageProbeLegacyExport(ctx context.Context, repo, binary, home, base, root string, run func(string, ...string) (string, error)) error {
	physical := "__scenery/tenants/" + base64.RawURLEncoding.EncodeToString([]byte("legacy-tenant")) + "/legacy.txt"
	source := filepath.Join(base, "legacy-source")
	files := map[string][]byte{
		filepath.Join(source, "objects", "app", filepath.FromSlash(physical)):                                  []byte("legacy payload"),
		filepath.Join(source, "objects", "app", "__scenery", "metadata", filepath.FromSlash(physical)+".json"): []byte(`{"content_type":"text/plain","metadata":{"Origin":"legacy"}}`),
	}
	before := make(map[string]os.FileInfo)
	for name, data := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(name, data, 0o640); err != nil {
			return err
		}
		info, err := os.Stat(name)
		if err != nil {
			return err
		}
		before[name] = info
	}
	exporter := filepath.Join(base, "legacy-exporter")
	build := commandTreeContext(ctx, "go", "build", "-o", exporter, "./scripts/storage-export-legacy-src")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build legacy exporter: %w: %s", err, tailString(string(out), 4096))
	}
	archive := filepath.Join(base, "legacy-converted.zip")
	common := []string{"--source", source, "--app-id", "storage-basic", "--tenant-store", "app"}
	for _, extra := range [][]string{{"--dry-run"}, {"--confirm-source-quiesced", "--output", archive}} {
		cmd := commandTreeContext(ctx, exporter, append(append([]string(nil), common...), extra...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("legacy export %v: %w: %s", extra, err, tailString(string(out), 4096))
		}
	}
	if out, stderr, err := runHarnessStorageProbeCommand(ctx, repo, home, []string{binary, "snapshot", "verify", "--input", archive, "-o", "json"}); err != nil {
		return fmt.Errorf("verify converted archive: %w: %s", err, tailString(firstNonEmpty(stderr, out), 4096))
	}
	if _, err := run(root, "snapshot", "load", "--input", archive, "--storage", "--mode", "overwrite", "--yes"); err != nil {
		return err
	}
	output := filepath.Join(base, "legacy-download")
	if _, err := run(root, "storage", "get", "app", "legacy.txt", "--tenant", "legacy-tenant", "--output", output); err != nil {
		return err
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "legacy payload" {
		return fmt.Errorf("converted payload mismatch: %q: %w", data, err)
	}
	stat, err := run(root, "storage", "stat", "app", "legacy.txt", "--tenant", "legacy-tenant")
	if err != nil {
		return err
	}
	if !bytes.Contains([]byte(stat), []byte(`"Origin":"legacy"`)) {
		return fmt.Errorf("converted metadata missing: %s", stat)
	}
	for name, want := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		after, err := os.Stat(name)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, want) || !os.SameFile(before[name], after) || before[name].Mode() != after.Mode() || !before[name].ModTime().Equal(after.ModTime()) {
			return fmt.Errorf("legacy export changed source %s", name)
		}
	}
	return nil
}
