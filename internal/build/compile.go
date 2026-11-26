package build

import (
	"context"
	"maps"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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
	return refreshWorkspaceBuildIdentity(result)
}

// Tidy changes the consumed workspace, so its final bytes own the binary key
// and any linked runtime identity. A retry must prepare its bundle again.
func refreshWorkspaceBuildIdentity(result *Result) error {
	fingerprint, err := workspaceBuildFingerprint(result.Dir, result.GoBuildFlags, result.SourceFiles, result.GeneratedFiles)
	if err != nil {
		return err
	}
	if result.BuildFingerprint != fingerprint && result.BuildInput != nil {
		result.BuildInput = nil
		result.ImplementationRevisions = nil
	}
	result.BuildFingerprint = fingerprint
	result.Binary = filepath.Join(result.Dir, workspaceBinaryName(result.AppRoot, fingerprint))
	return nil
}

func savePrimedWorkspace(result *Result) error {
	if err := saveBuildState(result.Dir, buildState{
		Version:                    buildStateVersion,
		DependencyFingerprint:      result.DependencyFingerprint,
		SourceFingerprint:          result.SourceFingerprint,
		SourceMetadataFingerprint:  result.SourceMetadataFingerprint,
		FrameworkFingerprint:       result.FrameworkFingerprint,
		GeneratorFingerprint:       result.GeneratorFingerprint,
		PreparationFingerprint:     result.PreparationFingerprint,
		BuildFingerprint:           result.BuildFingerprint,
		GraphFingerprint:           result.GraphFingerprint,
		Metadata:                   append([]byte(nil), result.Metadata...),
		APIEncoding:                append([]byte(nil), result.APIEncoding...),
		SourceStamps:               maps.Clone(result.SourceStamps),
		GeneratedFiles:             append([]string(nil), result.GeneratedFiles...),
		GeneratedStamps:            maps.Clone(result.GeneratedStamps),
		PublicGeneratedStamps:      maps.Clone(result.PublicGeneratedStamps),
		CachedTypeScriptStamps:     maps.Clone(result.CachedTypeScriptStamps),
		ManagedGeneratedPaths:      append([]string(nil), result.ManagedGeneratedPaths...),
		GoBuildFlags:               append([]string(nil), result.GoBuildFlags...),
		OwnedGoModuleSources:       cloneOwnedGoModuleSources(result.OwnedGoModuleSources),
		DevelopmentProcessBinaries: append([]string(nil), result.DevelopmentProcessBinaries...),
		VerifiedImplementation:     result.verifiedImplementation,
	}); err != nil {
		return err
	}
	if err := WriteLatestBuildManifest(result, "primed"); err != nil {
		return err
	}
	return nil
}

var runGo = runRealGo

func runRealGo(ctx context.Context, dir string, environment []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	if environment != nil {
		cmd.Env = environment
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return goCommandFailure(ctx, args, output, err)
	}
	return nil
}

func runGoContextWithEnvironment(ctx context.Context, dir string, environment []string, args ...string) error {
	started := time.Now()
	err := runGo(ctx, dir, environment, args...)
	reason := "go_tool"
	if len(args) > 0 {
		reason = args[0]
	}
	step := Step{Name: "go.command", StartedAt: started, Duration: time.Since(started), Cache: "go_managed", Reason: reason, OK: err == nil, Actions: 1}
	if reason == "build" {
		// The stock Go command exposes no successful package rebuild inventory
		// without enabling its much more expensive verbose action trace.
		step.PackagesRebuiltAvailable = false
	}
	RecordStep(ctx, step)
	return err
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

func SetGoRunnerForTesting(runner func(context.Context, string, ...string) error) func() {
	old := runGo
	runGo = func(ctx context.Context, dir string, _ []string, args ...string) error {
		return runner(ctx, dir, args...)
	}
	return func() {
		runGo = old
	}
}
