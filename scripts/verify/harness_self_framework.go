package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"scenery.sh/internal/build"
)

// Only this probe-owned origin is later mutated; no running developer checkout
// is edited to establish isolation from concurrent framework co-development.
func prepareHarnessSelectedFramework(ctx context.Context, repoRoot, root, appRoot, bootstrap string, env []string) (build.FrameworkSelection, error) {
	var empty build.FrameworkSelection
	origin := filepath.Join(root, "framework-origin")
	if err := copyHarnessFrameworkSource(repoRoot, origin); err != nil {
		return empty, err
	}
	command := exec.CommandContext(ctx, bootstrap, "framework", "use", "--source", origin, "--app-root", appRoot, "-o", "json")
	command.Env, command.Dir = env, appRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return empty, fmt.Errorf("prepare public framework selection: %w: %s", err, output)
	}
	selection, err := build.ReadFrameworkSelection(appRoot)
	if err != nil {
		return empty, err
	}
	if err := build.VerifyFrameworkSelection(ctx, selection); err != nil {
		return empty, err
	}
	return selection, nil
}

func copyHarnessFrameworkSource(repoRoot, origin string) error {
	source, err := build.FrameworkSourceManifest(repoRoot)
	if err != nil {
		return err
	}
	for _, input := range source.Inputs.Entries {
		relative := strings.TrimPrefix(input.Identity, "framework/source/")
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relative)))
		if err != nil {
			return err
		}
		target := filepath.Join(origin, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func verifyHarnessFrameworkBuildInputs(manifest *build.BuildInputManifest, selected build.FrameworkSelection) error {
	if manifest == nil {
		return fmt.Errorf("runtime has no actual build inputs")
	}
	expected := map[string]string{
		"framework/scenery.sh/source":     selected.Source.Digest,
		"producer/scenery-cli/executable": selected.ExecutableDigest,
	}
	for _, input := range manifest.Entries {
		if want, ok := expected[input.Identity]; ok {
			if input.Digest != want {
				return fmt.Errorf("runtime %s is %s, selected %s", input.Identity, input.Digest, want)
			}
			delete(expected, input.Identity)
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("runtime build inputs omit selected producer evidence: %v", expected)
	}
	return nil
}
