package main

import (
	"fmt"
	"go/version"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// checkArchitectureFixtureModules compares each nested module that replaces
// scenery.sh with this repository against the root go.mod. Such a module
// inherits the root requirements, so Go selects the root's newer versions and
// refuses every read-only command in it with "updates to go.mod needed" once
// its own go.mod lists an older requirement or go version. A root dependency
// bump would otherwise break the fixtures silently until a probe starts them.
// It returns how many fixture modules it compared.
func checkArchitectureFixtureModules(repoRoot string, candidates []string) (int, []checkDiagnostic) {
	root, err := parseArchitectureGoMod(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		// checkArchitectureDependencies reports an unusable root go.mod.
		return 0, nil
	}
	selected := map[string]string{}
	for _, requirement := range root.Require {
		selected[requirement.Mod.Path] = requirement.Mod.Version
	}
	checked := 0
	var diagnostics []checkDiagnostic
	for _, rel := range candidates {
		module, err := parseArchitectureGoMod(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil || !replacesSceneryWithRepository(repoRoot, rel, module) {
			continue
		}
		checked++
		var older []string
		if module.Go != nil && root.Go != nil && version.Compare("go"+module.Go.Version, "go"+root.Go.Version) < 0 {
			older = append(older, fmt.Sprintf("go %s < %s", module.Go.Version, root.Go.Version))
		}
		for _, requirement := range module.Require {
			if want, ok := selected[requirement.Mod.Path]; ok && semver.Compare(requirement.Mod.Version, want) < 0 {
				older = append(older, fmt.Sprintf("%s %s < %s", requirement.Mod.Path, requirement.Mod.Version, want))
			}
		}
		if len(older) == 0 {
			continue
		}
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "architecture checks",
			Severity:        "error",
			File:            rel,
			Message:         "fixture module is older than the root go.mod, so read-only go commands in it fail with \"updates to go.mod needed\": " + strings.Join(older, ", "),
			SuggestedAction: "Tidy it in a disposable copy: point `replace scenery.sh` at the absolute repository path, run `.scenery/harness/bin/scenery generate --app-root <copy>`, run `go mod tidy`, then copy go.mod and go.sum back with the relative replace restored (docs/harness-engineering.md#architecture-checks).",
		})
	}
	return checked, diagnostics
}

func parseArchitectureGoMod(path string) (*modfile.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(path, data, nil)
}

// replacesSceneryWithRepository reports whether the module at rel resolves
// scenery.sh to the repository root, as every committed fixture does.
func replacesSceneryWithRepository(repoRoot, rel string, module *modfile.File) bool {
	for _, replace := range module.Replace {
		if replace.Old.Path != "scenery.sh" || replace.New.Version != "" {
			continue
		}
		target := filepath.FromSlash(replace.New.Path)
		if !filepath.IsAbs(target) {
			target = filepath.Join(repoRoot, filepath.Dir(filepath.FromSlash(rel)), target)
		}
		return filepath.Clean(target) == filepath.Clean(repoRoot)
	}
	return false
}
