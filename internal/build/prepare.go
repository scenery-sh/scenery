package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

func Prepare(appRoot string, model *model.App, cfg app.Config) (*Result, error) {
	return PrepareWithSnapshot(appRoot, model, cfg, nil)
}

func PrepareWithSnapshot(appRoot string, model *model.App, cfg app.Config, snapshot *SourceSnapshot) (*Result, error) {
	if err := requireGenerateHooks(); err != nil {
		return nil, err
	}
	contract, err := compiler.Check(appRoot)
	if err != nil {
		return nil, err
	}
	if contract.ContractStatus == "valid" {
		if err := generateHooks.SyncGoPackages(contract); err != nil {
			return nil, err
		}
	}
	generateHooks.ApplyImplementationCheck(contract)
	if !contract.Valid() {
		for _, diagnostic := range contract.Diagnostics {
			if diagnostic.Severity == "error" {
				return nil, &ContractError{Diagnostic: diagnostic}
			}
		}
		return nil, fmt.Errorf("build preparation failed: invalid contract has no error diagnostic")
	}
	target, err := compiler.ResolveGoBuildTarget(contract, "", "development")
	if err != nil {
		return nil, err
	}
	return prepareWithContractTarget(appRoot, model, cfg, snapshot, contract, target)
}

// ContractError retains compiler diagnostics across build and detached-startup
// orchestration; user-fixable generated drift must not become SCN9000.
type ContractError struct{ Diagnostic compiler.Diagnostic }

func (e *ContractError) Error() string {
	message := "build preparation failed: " + e.Diagnostic.Code + ": " + e.Diagnostic.Message
	if len(e.Diagnostic.Suggestions) > 0 {
		message += " (" + e.Diagnostic.Suggestions[0] + ")"
	}
	return message
}

func (e *ContractError) ExitCode() int {
	if strings.HasPrefix(e.Diagnostic.Code, "SCN9") {
		return 10
	}
	return 3
}

func prepareWithContractTarget(appRoot string, model *model.App, cfg app.Config, snapshot *SourceSnapshot, contract *compiler.Result, target compiler.GoBuildTarget) (*Result, error) {
	if err := generateHooks.SyncGoPackages(contract); err != nil {
		return nil, err
	}
	if err := generateHooks.SyncCachedTypeScript(contract); err != nil {
		return nil, err
	}
	renderedGo, err := generateHooks.RenderGoWorkspaceFiles(contract)
	if err != nil {
		return nil, err
	}
	if model == nil {
		overlay, overlayErr := generateHooks.GoVerificationOverlay(contract)
		if overlayErr != nil {
			return nil, overlayErr
		}
		patterns, patternsErr := generateHooks.GoVerificationPatterns(contract)
		if patternsErr != nil {
			return nil, patternsErr
		}
		target.Context.Patterns = append(target.Context.Patterns, patterns...)
		model, err = parse.AnalyzeTarget(appRoot, cfg.Name, overlay, target.Context)
		if err != nil {
			return nil, err
		}
	}
	runtimePlan, err := generateHooks.RuntimeIntegrationPlan(contract)
	if err != nil {
		return nil, err
	}
	goBuildFlags := append([]string(nil), target.Context.BuildFlags...)
	if len(target.Context.BuildTags) > 0 {
		goBuildFlags = append(goBuildFlags, "-tags="+strings.Join(target.Context.BuildTags, ","))
	}
	gen, err := codegen.Generate(model, cfg, runtimePlan.CompositionImport)
	if err != nil {
		return nil, err
	}
	for relative, contents := range renderedGo {
		if _, exists := gen.Generated[relative]; exists {
			return nil, fmt.Errorf("generated artifact path collision: %s", relative)
		}
		gen.Generated[relative] = contents
	}
	workspaceDir, err := workspaceDir(appRoot, cfg.Name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, err
	}
	unlock, err := lockWorkspace(workspaceDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := loadBuildState(workspaceDir)
	if err != nil {
		return nil, err
	}
	// Hash the app source before syncing so a file that changes mid-prepare
	// invalidates this fingerprint instead of blessing a workspace that may
	// not contain the change.
	sourceFingerprint, err := currentAppSourceFingerprintWithSnapshot(appRoot, snapshot)
	if err != nil {
		return nil, err
	}
	generatedPaths := make(map[string]struct{}, len(gen.Generated))
	for relative := range gen.Generated {
		generatedPaths[filepath.ToSlash(relative)] = struct{}{}
	}
	sourceFiles, sourceStamps, err := syncSourceFilesWithSnapshot(workspaceDir, appRoot, state.SourceStamps, generatedPaths, snapshot)
	if err != nil {
		return nil, err
	}
	generatedFiles, err := syncGeneratedFiles(workspaceDir, appRoot, gen, state.GeneratedFiles, sourceFiles)
	if err != nil {
		return nil, err
	}
	if err := removeUnexpectedFilesFromLists(workspaceDir, sourceFiles, generatedFiles); err != nil {
		return nil, err
	}
	if err := seedSceneryGoSum(workspaceDir, app.RepoRoot()); err != nil {
		return nil, err
	}
	sourceMetadataFingerprint := sourceStampsFingerprint(sourceStamps)
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		return nil, err
	}
	depFingerprint, err := dependencyFingerprintFromWorkspace(workspaceDir)
	if err != nil {
		return nil, err
	}
	frameworkFingerprint, _, err := currentFrameworkFingerprintFromWorkspace(workspaceDir)
	if err != nil {
		return nil, err
	}
	needsTidy := state.DependencyFingerprint != depFingerprint
	buildFingerprint, err := workspaceBuildFingerprint(workspaceDir, goBuildFlags, sourceFiles, generatedFiles)
	if err != nil {
		return nil, err
	}
	binary := filepath.Join(workspaceDir, workspaceBinaryName(appRoot, buildFingerprint))
	result := &Result{
		AppRoot:                   appRoot,
		AppName:                   cfg.Name,
		AppID:                     cfg.ID,
		Dir:                       workspaceDir,
		Binary:                    binary,
		NeedsTidy:                 needsTidy,
		DependencyFingerprint:     depFingerprint,
		SourceFingerprint:         sourceFingerprint,
		SourceMetadataFingerprint: sourceMetadataFingerprint,
		FrameworkFingerprint:      frameworkFingerprint,
		GeneratorFingerprint:      generatorFingerprint,
		BuildFingerprint:          buildFingerprint,
		ReuseCompiled:             buildFingerprint != "" && pathExists(binary) && state.FrameworkFingerprint == frameworkFingerprint,
		SourceFiles:               sourceFiles,
		SourceStamps:              sourceStamps,
		GeneratedFiles:            generatedFiles,
		GoBuildFlags:              append([]string(nil), goBuildFlags...),
		Contract:                  contract,
		Target:                    &target,
	}
	result.GoEnvironment = gotarget.Environment(target.Context)
	// Runtime bundles are target-specific, so an unbound workspace binary is
	// never reused across build targets.
	result.ReuseCompiled = false
	if err := WriteLatestBuildManifest(result, "prepared"); err != nil {
		return nil, err
	}
	return result, nil
}
