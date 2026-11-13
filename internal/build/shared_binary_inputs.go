package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Live freshness checks remain necessary for private builds. They cannot
// prove which external bytes a compiler read during A/B/A.
func verifySharedBinaryInputs(ctx context.Context, result *Result, discover func(context.Context, *Result) (*BuildInputManifest, error)) error {
	if err := verifyPreparedWorkspace(result); err != nil {
		return err
	}
	if result.FrameworkSourceRoot != "" {
		source, err := FrameworkSourceManifest(result.FrameworkSourceRoot)
		if err != nil {
			return err
		}
		if source.Digest != result.FrameworkSourceDigest {
			return fmt.Errorf("framework source changed during compilation")
		}
	}
	if result.BuildInput == nil {
		return fmt.Errorf("go build input identity is unavailable after compilation")
	}
	directoryChanged, err := verifyObservedBuildInputStamps(result.BuildInput.observed)
	if err != nil {
		return err
	}
	if !directoryChanged {
		return ctx.Err()
	}
	// A directory stamp change can mean package or embed membership changed.
	// Only that case needs another Go graph query; stable file and directory
	// stamps prove the exact discovered input set remained current.
	current := *result
	inputs, err := discover(ctx, &current)
	if err != nil {
		return err
	}
	if inputs.Digest != result.BuildInput.Digest {
		return fmt.Errorf("go build inputs changed during compilation")
	}
	// Retain existing detectable-mutation rejection for private candidates too.
	// This is not an immutability proof: all stamps may match after A/B/A.
	for path, before := range result.BuildInput.observed {
		if after, ok := inputs.observed[path]; !ok || before != after {
			return fmt.Errorf("go build input changed during compilation, including a possible restore: %s", path)
		}
	}
	return ctx.Err()
}

func verifyObservedBuildInputStamps(observed map[string]buildInputFileStamp) (bool, error) {
	if len(observed) == 0 {
		return true, nil
	}
	directoryChanged := false
	for path, before := range observed {
		after, err := buildInputLstat(path)
		if err != nil {
			return false, fmt.Errorf("go build input changed during compilation: %s: %w", path, err)
		}
		if after.Mode()&os.ModeSymlink != 0 || (!after.IsDir() && !after.Mode().IsRegular()) {
			return false, fmt.Errorf("go build input changed type during compilation: %s", path)
		}
		if buildInputStamp(after) == before {
			continue
		}
		if after.IsDir() {
			directoryChanged = true
			continue
		}
		return false, fmt.Errorf("go build input changed during compilation, including a possible restore: %s", path)
	}
	return directoryChanged, nil
}

func observeBuildInputPath(observed map[string]buildInputFileStamp, path string) error {
	info, err := buildInputLstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fmt.Errorf("go build input is not a regular file or directory: %s", path)
	}
	stamp := buildInputStamp(info)
	path = filepath.Clean(path)
	if before, ok := observed[path]; ok && before != stamp {
		return fmt.Errorf("go build input changed during discovery: %s", path)
	}
	observed[path] = stamp
	return nil
}

// observeBuildInputDirectories records the stamps of directory and its parents
// up to packageRoot. A directory this discovery already observed was observed
// with its parents, so the walk stops there.
func observeBuildInputDirectories(observed map[string]buildInputFileStamp, directory, packageRoot string) error {
	for {
		if _, seen := observed[filepath.Clean(directory)]; seen {
			return nil
		}
		if err := observeBuildInputPath(observed, directory); err != nil {
			return err
		}
		if directory == packageRoot || filepath.Dir(directory) == directory {
			return nil
		}
		directory = filepath.Dir(directory)
	}
}
