package build

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"
)

// observeWorkspaceVerification verifies the prepared workspace and records it
// as a build step.
func observeWorkspaceVerification(ctx context.Context, result *Result, reason string) error {
	started := time.Now()
	err := verifyPreparedWorkspace(result)
	RecordStep(ctx, Step{Name: "workspace.verify", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: reason, OK: err == nil})
	return err
}

// Reacquiring the workspace lock must not silently adopt another preparation's
// files. Check membership and content, not saved timestamps, before the fork
// and again before success publication: content observed earlier in this
// process is reused only while a file's stamp, including its status-change
// time, which any write changes, is unchanged. Tidy's optional go.sum is a
// known input.
func verifyPreparedWorkspace(result *Result) error {
	allowed := map[string]bool{"go.mod": true, "go.sum": true}
	for _, group := range [][]string{result.SourceFiles, result.GeneratedFiles} {
		for _, path := range group {
			allowed[filepath.ToSlash(path)] = true
		}
	}
	err := filepath.WalkDir(result.Dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(result.Dir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			// Linked development process executables are outputs of the same
			// preparation, owned like the application executable.
			if relative == developmentProcessBinaryDir {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("prepared workspace has a non-regular input: %s", relative)
		}
		if allowed[relative] || relative == buildStateFile || relative == ".scenery-workspace.lock" || relative == workspaceMarkerFile || relative == "scenery-app" || isFingerprintBinaryName(relative) {
			return nil
		}
		return fmt.Errorf("prepared workspace membership changed: %s", relative)
	})
	if err != nil {
		return err
	}
	fingerprint, err := workspaceBuildFingerprint(result.Dir, result.GoBuildFlags, result.SourceFiles, result.GeneratedFiles)
	if err != nil {
		return err
	}
	if fingerprint != result.BuildFingerprint {
		return fmt.Errorf("prepared workspace bytes changed; candidate was not published")
	}
	return nil
}
