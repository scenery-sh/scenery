package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A cache entry is native output, not a saved implementation-check verdict.
// Prove its input identity independently before making that output reusable.
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
			return fmt.Errorf("framework source changed during compilation; shared candidate was not published")
		}
	}
	// Discovery also reads live local replacements, embedded/native inputs and
	// module metadata. Do not let it overwrite the identity being checked.
	current := *result
	inputs, err := discover(ctx, &current)
	if err != nil {
		return err
	}
	if result.BuildInput == nil || inputs.Digest != result.BuildInput.Digest {
		return fmt.Errorf("go build inputs changed during compilation; shared candidate was not published")
	}
	for path, before := range result.BuildInput.observed {
		if after, ok := inputs.observed[path]; !ok || before != after {
			return fmt.Errorf("go build input changed during compilation, including a possible restore: %s; shared candidate was not published", path)
		}
	}
	return ctx.Err()
}

func observeBuildInputPath(observed map[string]buildInputFileStamp, path string) error {
	info, err := os.Lstat(path)
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

func observeExternalBuildInputDirectories(observed map[string]buildInputFileStamp, workspace, directory, packageRoot string) error {
	relative, err := filepath.Rel(workspace, directory)
	if err != nil {
		return err
	}
	if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	for {
		if err := observeBuildInputPath(observed, directory); err != nil {
			return err
		}
		if directory == packageRoot || filepath.Dir(directory) == directory {
			return nil
		}
		directory = filepath.Dir(directory)
	}
}
