package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/gotarget"
)

// Live freshness checks remain necessary for both private builds and cache
// hits. They cannot prove which external bytes a compiler read during A/B/A;
// sharedBinaryInputsSupported excludes that domain before lookup or joining.
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
		return fmt.Errorf("go build inputs changed during compilation; shared candidate was not published")
	}
	// Retain existing detectable-mutation rejection for private candidates too.
	// This is not an immutability proof: all stamps may match after A/B/A.
	for path, before := range result.BuildInput.observed {
		if after, ok := inputs.observed[path]; !ok || before != after {
			return fmt.Errorf("go build input changed during compilation, including a possible restore: %s; shared candidate was not published", path)
		}
	}
	current.BuildInput = inputs
	if sharedBinaryInputsSupported(result) && !sharedBinaryInputsSupported(&current) {
		return fmt.Errorf("go build input ownership changed during compilation; shared candidate was not published")
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
		return false, fmt.Errorf("go build input changed during compilation, including a possible restore: %s; shared candidate was not published", path)
	}
	return directoryChanged, nil
}

func sharedBinaryWorkspacePath(workspace, path string) bool {
	if !filepath.IsAbs(workspace) || !filepath.IsAbs(path) {
		return false
	}
	relative, err := filepath.Rel(workspace, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
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

func observeBuildInputDirectories(observed map[string]buildInputFileStamp, directory, packageRoot string) error {
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

// Even dependencies omitted from a package listing can supply module resolver
// inputs. Support only a standalone module, not an inferred "unused" replace.
func sharedBinaryStandaloneModule(workspace string) bool {
	data, err := os.ReadFile(filepath.Join(workspace, "go.mod"))
	if err != nil {
		return false
	}
	module, err := modfile.Parse("go.mod", data, nil)
	return err == nil && module.Module != nil && len(module.Require)+len(module.Replace)+len(module.Exclude) == 0
}

// Fail closed unless current discovery proved all non-toolchain inputs belong
// to the caller's locked workspace. Read-only permissions, module sums and
// content-addressed directory names do not make external source immutable.
func sharedBinaryInputsSupported(result *Result) bool {
	if result == nil || result.Target == nil || result.BuildInput == nil || result.BuildInput.sharedWorkspace == "" ||
		result.BuildInput.sharedWorkspace != result.Dir || runtime.GOOS == "windows" {
		// Windows does not implement the private workspace lock yet.
		return false
	}
	target := result.Target.Context
	if target.CGOEnabled || len(target.NativeToolEnv)+len(target.NativeToolIdentities)+len(target.BuildFlags) != 0 ||
		len(stringValuesForBuild(result.Target.Effective["native_inputs"]))+len(stringValuesForBuild(result.Target.Effective["native_input"])) != 0 ||
		(result.FrameworkSourceRoot != "" && !sharedBinaryWorkspacePath(result.Dir, result.FrameworkSourceRoot)) {
		return false
	}
	// Overlays, toolexec, linker flags and other configured tool reads are not
	// admitted by a package file listing. Only the generated build-tag flag is
	// allowed; private runtime -X metadata is appended separately by the owner.
	var flags []string
	if len(target.BuildTags) > 0 {
		flags = []string{"-tags=" + strings.Join(target.BuildTags, ",")}
	}
	return slices.Equal(normalizeGoBuildFlags(result.GoBuildFlags), flags) &&
		slices.Equal(result.GoEnvironment, gotarget.Environment(target))
}
