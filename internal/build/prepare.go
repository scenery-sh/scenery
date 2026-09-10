package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
	"scenery.sh/internal/gotarget"
	gomodel "scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

func Prepare(appRoot string, model *gomodel.App, cfg app.Config) (*Result, error) {
	return PrepareWithSnapshot(appRoot, model, cfg, nil)
}

func PrepareWithSnapshot(appRoot string, model *gomodel.App, cfg app.Config, snapshot *SourceSnapshot) (*Result, error) {
	return PrepareWithSnapshotContext(context.Background(), appRoot, model, cfg, snapshot)
}

func PrepareWithSnapshotContext(ctx context.Context, appRoot string, model *gomodel.App, cfg app.Config, snapshot *SourceSnapshot) (*Result, error) {
	if err := requireGenerateHooks(); err != nil {
		return nil, err
	}
	contract, err := observeBuild(ctx, "contract.check", func() (*compiler.Result, error) { return compiler.Check(appRoot) })
	if err != nil {
		return nil, err
	}
	if contract.ContractStatus == "valid" {
		if err := observeBuildAction(ctx, "projection.public_go", func() error { return generateHooks.SyncGoPackages(contract) }); err != nil {
			return nil, err
		}
	}
	var analyses []verifiedGoAnalysis
	var projection *generateapi.GoWorkspaceProjection
	if err := observeBuildAction(ctx, "implementation.check", func() error {
		projection = generateHooks.ApplyImplementationCheck(contract, func(target gotarget.Context, app *gomodel.App) {
			analyses = append(analyses, verifiedGoAnalysis{target: target, app: app})
		})
		return nil
	}); err != nil {
		return nil, err
	}
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
	return prepareWithContractTargetContext(ctx, appRoot, model, cfg, snapshot, contract, target, analyses, projection)
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

func prepareWithContractTarget(appRoot string, model *gomodel.App, cfg app.Config, snapshot *SourceSnapshot, contract *compiler.Result, target compiler.GoBuildTarget) (*Result, error) {
	if err := generateHooks.SyncGoPackages(contract); err != nil {
		return nil, err
	}
	return prepareWithContractTargetContext(context.Background(), appRoot, model, cfg, snapshot, contract, target, nil, nil)
}

// The caller publishes public Go projections before implementation checking or
// target preparation. Keep that transaction outside the shared workspace phase
// so ordinary development does not publish the same projection twice.
func prepareWithContractTargetContext(ctx context.Context, appRoot string, model *gomodel.App, cfg app.Config, snapshot *SourceSnapshot, contract *compiler.Result, target compiler.GoBuildTarget, analyses []verifiedGoAnalysis, projection *generateapi.GoWorkspaceProjection) (*Result, error) {
	if err := observeBuildAction(ctx, "projection.typescript", func() error { return generateHooks.SyncCachedTypeScript(contract) }); err != nil {
		return nil, err
	}
	var err error
	if projection == nil {
		rendered, renderErr := observeBuild(ctx, "projection.private_go", func() (generateapi.GoWorkspaceProjection, error) { return generateHooks.PrepareGoWorkspace(contract) })
		if renderErr != nil {
			return nil, renderErr
		}
		projection = &rendered
	} else {
		finishStep(ctx, "projection.private_go", time.Now(), "hit", "same_preparation_verified_projection", nil)
	}
	if model == nil {
		target.Context.Patterns = append(target.Context.Patterns, projection.VerificationPatterns...)
		started := time.Now()
		model = matchingGoAnalysis(analyses, appRoot, cfg.Name, target.Context)
		if model != nil {
			finishStep(ctx, "go.analysis", started, "hit", "same_preparation_exact_verified_target", nil)
		}
	}
	if model == nil {
		err = observeBuildAction(ctx, "go.analysis", func() error {
			model, err = parse.AnalyzeTarget(appRoot, cfg.Name, projection.VerificationOverlay, target.Context)
			return err
		})
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
	gen, err := observeBuild(ctx, "workspace.render", func() (*codegen.Output, error) {
		return codegen.Generate(model, cfg, runtimePlan.CompositionImport, contract.SQLRequirements)
	})
	if err != nil {
		return nil, err
	}
	for relative, contents := range projection.Files {
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
	if err := seedWorkspaceSceneryGoSum(workspaceDir); err != nil {
		return nil, err
	}
	sourceMetadataFingerprint := sourceStampsFingerprint(sourceStamps)
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		return nil, err
	}
	inventory := newWorkspaceInventory(workspaceDir)
	depFingerprint, err := dependencyFingerprintFromInventory(inventory)
	if err != nil {
		return nil, err
	}
	frameworkFingerprint, err := observeBuild(ctx, "framework.workspace_fingerprint", func() (string, error) {
		fingerprint, _, err := currentFrameworkFingerprintFromWorkspace(workspaceDir)
		return fingerprint, err
	})
	if err != nil {
		return nil, err
	}
	needsTidy := state.DependencyFingerprint != depFingerprint
	buildFingerprint, err := workspaceBuildFingerprintFromInventory(inventory, goBuildFlags, sourceFiles, generatedFiles)
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
