package build

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// Reacquiring the workspace lock must not silently adopt another preparation's
// files. Check membership and bytes, not saved timestamps, before the fork and
// again before success publication. Tidy's optional go.sum is a known input.
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
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(result.Dir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("prepared workspace has a non-regular input: %s", relative)
		}
		if allowed[relative] || relative == buildStateFile || relative == ".scenery-workspace.lock" || relative == "scenery-app" || isFingerprintBinaryName(relative) {
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
