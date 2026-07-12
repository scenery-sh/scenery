package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/vnext"
)

func Prepare(appRoot string, model *model.App, cfg app.Config) (*Result, error) {
	return PrepareWithSnapshot(appRoot, model, cfg, nil)
}

func PrepareWithSnapshot(appRoot string, model *model.App, cfg app.Config, snapshot *SourceSnapshot) (*Result, error) {
	contract, err := vnext.Check(appRoot)
	if err != nil {
		return nil, err
	}
	if !contract.Valid() {
		message := "edition-2027 contract or generated artifacts are invalid"
		for _, diagnostic := range contract.Diagnostics {
			if diagnostic.Severity == "error" {
				message = diagnostic.Code + ": " + diagnostic.Message
				break
			}
		}
		return nil, fmt.Errorf("vNext build preparation failed: %s", message)
	}
	target, err := vnext.ResolveGoBuildTarget(contract, "", "development")
	if err != nil {
		return nil, err
	}
	return prepareWithContractTarget(appRoot, model, cfg, snapshot, contract, target)
}

func prepareWithContractTarget(appRoot string, model *model.App, cfg app.Config, snapshot *SourceSnapshot, contract *vnext.Result, target vnext.GoBuildTarget) (*Result, error) {
	runtimePlan, err := vnext.BuildRuntimeIntegrationPlan(contract)
	if err != nil {
		return nil, err
	}
	codegenOptions := codegen.Options{CompositionImport: runtimePlan.CompositionImport}
	goBuildFlags := append([]string(nil), target.Context.BuildFlags...)
	if len(target.Context.BuildTags) > 0 {
		goBuildFlags = append(goBuildFlags, "-tags="+strings.Join(target.Context.BuildTags, ","))
	}
	gen, err := codegen.GenerateWithOptions(model, cfg, codegenOptions)
	if err != nil {
		return nil, err
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
	sourceFiles, sourceStamps, err := syncSourceFilesWithSnapshot(workspaceDir, appRoot, state.SourceStamps, nil, snapshot)
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
		VNextContract:             contract,
		VNextTarget:               &target,
	}
	result.GoEnvironment = parse.GoTargetEnvironment(target.Context)
	// Runtime bundles are target-specific, so an unbound workspace binary is
	// never reused across build targets.
	result.ReuseCompiled = false
	if err := WriteLatestBuildManifest(result, "prepared"); err != nil {
		return nil, err
	}
	return result, nil
}
