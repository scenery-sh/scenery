package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/storagefs"
)

func writeStorageDownload(ctx context.Context, plan *storageNamespacePlan, output string, body io.Reader, size int64) error {
	output, err := storageOutputPath(plan, output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	r, err := os.OpenRoot(filepath.Dir(output))
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return atomicfile.CopyRoot(r, filepath.Base(output), storageDownloadReader{ctx: ctx, body: body}, size, 0o600, atomicfile.Options{SyncFile: true, SyncDir: true})
}

func storageOutputPath(plan *storageNamespacePlan, output string) (string, error) {
	if info, err := os.Lstat(output); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%w: output must be a regular file, not a symlink or special entry", storagefs.ErrInvalid)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return "", err
	}
	// Reuse canonical existing-ancestor resolution even for a not-yet-created
	// destination. Validation precedes mkdir or temporary-file creation.
	canonical, err := localagent.PathsForWorktree(paths.Home, output)
	if err != nil {
		return "", err
	}
	for _, protected := range []string{paths.Home, filepath.Join(plan.Binding.AppRoot, ".scenery")} {
		selected, err := localagent.PathsForWorktree(paths.Home, protected)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(selected.AppRoot, canonical.AppRoot)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: output cannot be inside managed storage or control state", storagefs.ErrInvalid)
		}
	}
	return canonical.AppRoot, nil
}

type storageDownloadReader struct {
	ctx  context.Context
	body io.Reader
}

func (r storageDownloadReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.body.Read(p)
}
