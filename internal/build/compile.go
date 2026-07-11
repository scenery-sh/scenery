package build

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
)

func Compile(result *Result) error {
	return CompileContext(context.Background(), result)
}

func PrimeWorkspaceContext(ctx context.Context, result *Result) error {
	if result == nil {
		return fmt.Errorf("nil build result")
	}
	if result.NeedsTidy {
		if err := tidyWorkspace(ctx, result); err != nil {
			return err
		}
	}
	return savePrimedWorkspace(result)
}

func tidyWorkspace(ctx context.Context, result *Result) error {
	if err := runGoContextWithEnvironment(ctx, result.Dir, result.GoEnvironment, "mod", "tidy"); err != nil {
		return err
	}
	fingerprint, err := dependencyFingerprintFromWorkspace(result.Dir)
	if err != nil {
		return err
	}
	result.DependencyFingerprint = fingerprint
	result.NeedsTidy = false
	return nil
}

func savePrimedWorkspace(result *Result) error {
	if err := saveBuildState(result.Dir, buildState{
		Version:                   buildStateVersion,
		DependencyFingerprint:     result.DependencyFingerprint,
		SourceFingerprint:         result.SourceFingerprint,
		SourceMetadataFingerprint: result.SourceMetadataFingerprint,
		FrameworkFingerprint:      result.FrameworkFingerprint,
		GeneratorFingerprint:      result.GeneratorFingerprint,
		BuildFingerprint:          result.BuildFingerprint,
		GraphFingerprint:          result.GraphFingerprint,
		Metadata:                  append([]byte(nil), result.Metadata...),
		APIEncoding:               append([]byte(nil), result.APIEncoding...),
		SourceStamps:              maps.Clone(result.SourceStamps),
		GeneratedFiles:            append([]string(nil), result.GeneratedFiles...),
		GoBuildFlags:              append([]string(nil), result.GoBuildFlags...),
	}); err != nil {
		return err
	}
	if err := WriteLatestBuildManifest(result, "primed"); err != nil {
		return err
	}
	return nil
}

func CompileContext(ctx context.Context, result *Result) error {
	if result == nil {
		return fmt.Errorf("nil build result")
	}
	unlock, err := lockWorkspace(result.Dir)
	if err != nil {
		return err
	}
	defer unlock()
	if result.ReuseCompiled {
		result.NeedsTidy = false
		if err := savePrimedWorkspace(result); err != nil {
			return err
		}
		return WriteLatestBuildManifest(result, "compiled")
	}
	if result.VNextTarget != nil && result.NeedsTidy {
		if err := tidyWorkspace(ctx, result); err != nil {
			return err
		}
	}
	if err := prepareVNextRuntimeBundle(ctx, result); err != nil {
		return err
	}
	if !result.NeedsTidy {
		if err := savePrimedWorkspace(result); err != nil {
			return err
		}
	}
	err = runGoBuildContext(ctx, result)
	if err != nil && (result.NeedsTidy || goBuildNeedsWorkspaceTidy(err)) {
		if tidyErr := tidyWorkspace(ctx, result); tidyErr != nil {
			return tidyErr
		}
		if saveErr := savePrimedWorkspace(result); saveErr != nil {
			return saveErr
		}
		err = runGoBuildContext(ctx, result)
	}
	if err != nil {
		return err
	}
	if err := writeVNextRuntimeBundle(result); err != nil {
		return err
	}
	if result.NeedsTidy {
		fingerprint, fingerprintErr := dependencyFingerprintFromWorkspace(result.Dir)
		if fingerprintErr != nil {
			return fingerprintErr
		}
		result.DependencyFingerprint = fingerprint
		result.NeedsTidy = false
		if err := savePrimedWorkspace(result); err != nil {
			return err
		}
	}
	if err := WriteLatestBuildManifest(result, "compiled"); err != nil {
		return err
	}
	return nil
}

func runGoBuildContext(ctx context.Context, result *Result) error {
	if err := os.Remove(result.Binary); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale build output %s: %w", result.Binary, err)
	}
	return runGoContextWithEnvironment(ctx, result.Dir, result.GoEnvironment, goBuildArgs(result.Binary, result.GoBuildFlags)...)
}

func goBuildNeedsWorkspaceTidy(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "missing go.sum entry") ||
		strings.Contains(text, "updates to go.mod needed") ||
		strings.Contains(text, "go.mod updates are needed")
}

var runGo = runRealGo

func SetGoRunnerForTesting(runner func(context.Context, string, ...string) error) func() {
	old := runGo
	runGo = func(ctx context.Context, dir string, _ []string, args ...string) error {
		return runner(ctx, dir, args...)
	}
	return func() {
		runGo = old
	}
}

func runRealGo(ctx context.Context, dir string, environment []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	if environment != nil {
		cmd.Env = environment
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go %s failed: %w\n%s", strings.Join(args, " "), err, output)
	}
	return nil
}

func runGoContextWithEnvironment(ctx context.Context, dir string, environment []string, args ...string) error {
	return runGo(ctx, dir, environment, args...)
}

func normalizeGoBuildFlags(flags []string) []string {
	normalized := make([]string, 0, len(flags))
	for _, flag := range flags {
		flag = strings.TrimSpace(flag)
		if flag == "" {
			continue
		}
		normalized = append(normalized, flag)
	}
	return normalized
}

func goBuildArgs(binary string, flags []string) []string {
	args := make([]string, 0, 5+len(flags))
	args = append(args, "build")
	args = append(args, normalizeGoBuildFlags(flags)...)
	args = append(args, "-buildvcs=false", "-o", binary, "./scenery_internal_main")
	return args
}
