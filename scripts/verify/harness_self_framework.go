package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"scenery.sh/internal/build"
	"scenery.sh/internal/spec"
)

// The bootstrap cannot decode B's protocol. Inspect only bounded routing and
// evidence fields here; B's public inspect must validate its own full receipt.
func proveHarnessCrossSpecFramework(ctx context.Context, origin, appRoot, bootstrap string, env []string) (map[string]any, error) {
	path := filepath.Join(origin, "internal/spec/catalog.go")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	old := []byte(spec.CurrentSemanticRevisions().Defaults)
	changed := bytes.Replace(data, old, []byte("sha256:"+strings.Repeat("a", 64)), 1)
	if bytes.Equal(data, changed) {
		return nil, fmt.Errorf("cross-spec fixture did not change a semantic revision")
	}
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, bootstrap, "framework", "use", "--source", origin, "--app-root", appRoot, "-o", "json")
	command.Env, command.Dir = env, appRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cross-spec prepare: %w: %s", err, output)
	}
	var result struct {
		SpecRevision string `json:"spec_revision"`
		Data         struct {
			Executable string `json:"executable"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, err
	}
	if result.SpecRevision == "" || result.SpecRevision == string(spec.CurrentRevision()) {
		return nil, fmt.Errorf("candidate did not emit its different specification: %s", output)
	}
	canonical, err := filepath.EvalSymlinks(appRoot)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(result.Data.Executable, filepath.Join(canonical, ".scenery/framework/bin")+string(filepath.Separator)) {
		return nil, fmt.Errorf("candidate executable escaped its private root")
	}
	inspect := exec.CommandContext(ctx, result.Data.Executable, "framework", "inspect", "--app-root", appRoot, "-o", "json")
	inspect.Env, inspect.Dir = env, appRoot
	if output, err := inspect.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("candidate rejected its own receipt: %w: %s", err, output)
	}
	receipt, err := os.ReadFile(build.FrameworkSelectionPath(appRoot))
	if err != nil {
		return nil, err
	}
	var identity struct {
		SpecRevision string `json:"spec_revision"`
		Source       struct {
			Inputs struct {
				SpecRevision string `json:"spec_revision"`
			} `json:"build_input_manifest"`
		} `json:"source"`
	}
	if err := json.Unmarshal(receipt, &identity); err != nil {
		return nil, err
	}
	if identity.SpecRevision != result.SpecRevision || identity.Source.Inputs.SpecRevision != result.SpecRevision {
		return nil, fmt.Errorf("candidate receipt or nested manifest is stamped by another producer")
	}
	if _, err := build.ReadFrameworkSelection(appRoot); err == nil {
		return nil, fmt.Errorf("bootstrap unexpectedly accepted another specification's receipt")
	}
	return map[string]any{"bootstrap_spec": string(spec.CurrentRevision()), "candidate_spec": result.SpecRevision, "candidate_inspect_passed": true, "nested_manifest_owned": true, "bootstrap_strict_rejection": true}, nil
}

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
