package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
	"scenery.sh/internal/gotarget"
)

func Prepare(appRoot string, cfg app.Config) (*Result, error) {
	ctx := context.Background()
	result, err := PrepareForCompileWithSnapshotContext(ctx, appRoot, cfg, nil)
	if err != nil {
		return nil, err
	}
	unlock, err := lockWorkspace(result.Dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := verifyPreparedWorkspace(result); err != nil {
		return nil, err
	}
	if result.NeedsTidy {
		if err := tidyWorkspace(ctx, result); err != nil {
			return nil, err
		}
	}
	if err := completePreparedVerification(ctx, result); err != nil {
		return nil, err
	}
	if err := verifyPreparedWorkspace(result); err != nil {
		return nil, err
	}
	return result, nil
}

// PrepareForCompileWithSnapshotContext owns all source and generated workspace
// bytes. CompileContext performs its pending full verification and joins it
// before publishing any successful build evidence.
func PrepareForCompileWithSnapshotContext(ctx context.Context, appRoot string, cfg app.Config, snapshot *SourceSnapshot) (*Result, error) {
	if err := requireGenerateHooks(); err != nil {
		return nil, err
	}
	contract, err := observeBuild(ctx, "contract.check", func() (*compiler.Result, error) { return compileWorkspaceContract(appRoot, snapshot) })
	if err != nil {
		return nil, err
	}
	if err := preparedContractError(contract); err != nil {
		return nil, err
	}
	return PrepareForCompileWithContractSnapshotContext(ctx, appRoot, cfg, snapshot, contract)
}

// PrepareForCompileWithContractSnapshotContext continues preparation from the
// exact graph already computed by the development engine. It prevents a cache
// miss from recompiling the same graph while retaining the ordinary public
// generation transaction and target checks.
func PrepareForCompileWithContractSnapshotContext(ctx context.Context, appRoot string, cfg app.Config, snapshot *SourceSnapshot, contract *compiler.Result) (*Result, error) {
	if err := requireGenerateHooks(); err != nil {
		return nil, err
	}
	if err := preparedContractError(contract); err != nil {
		return nil, err
	}
	projection, err := observeBuild(ctx, "projection.go", func() (generateapi.GoWorkspaceProjection, error) {
		return generateHooks.PrepareBuildGoWorkspace(contract)
	})
	if err != nil {
		return nil, err
	}
	target, err := compiler.ResolveGoBuildTarget(contract, "", "development")
	if err != nil {
		return nil, err
	}
	return prepareWithContractTargetContext(ctx, appRoot, cfg, snapshot, contract, target, projection)
}

func preparedContractError(contract *compiler.Result) error {
	if contract.Valid() {
		return nil
	}
	if contract != nil {
		for _, diagnostic := range contract.Diagnostics {
			if diagnostic.Severity == "error" {
				return &ContractError{Diagnostic: diagnostic}
			}
		}
	}
	return fmt.Errorf("build preparation failed: invalid contract has no error diagnostic")
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

func prepareWithContractTarget(appRoot string, cfg app.Config, snapshot *SourceSnapshot, contract *compiler.Result, target compiler.GoBuildTarget) (*Result, error) {
	if err := requireGenerateHooks(); err != nil {
		return nil, err
	}
	projection, err := generateHooks.PrepareBuildGoWorkspace(contract)
	if err != nil {
		return nil, err
	}
	return prepareWithContractTargetContext(context.Background(), appRoot, cfg, snapshot, contract, target, projection)
}

// The caller publishes public Go projections before implementation checking or
// target preparation. Keep that transaction outside the shared workspace phase
// so ordinary development does not publish the same projection twice.
func prepareWithContractTargetContext(ctx context.Context, appRoot string, cfg app.Config, snapshot *SourceSnapshot, contract *compiler.Result, target compiler.GoBuildTarget, projection generateapi.GoWorkspaceProjection) (*Result, error) {
	typeScriptPaths, err := observeBuild(ctx, "projection.typescript", func() ([]string, error) { return generateHooks.SyncCachedTypeScript(contract) })
	if err != nil {
		return nil, err
	}
	runtimePlan, err := generateHooks.RuntimeIntegrationPlan(contract)
	if err != nil {
		return nil, err
	}
	goBuildFlags := append([]string(nil), target.Context.BuildFlags...)
	if len(target.Context.BuildTags) > 0 {
		goBuildFlags = append(goBuildFlags, "-tags="+strings.Join(target.Context.BuildTags, ","))
	}
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		return nil, err
	}
	gen, err := renderSharedCompositionContext(ctx, cfg.Name, cfg, runtimePlan.CompositionImport, contract.SQLRequirements, generatorFingerprint)
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
	var (
		sourceFiles    []string
		sourceStamps   map[string]SourceStamp
		generatedFiles []string
		mutation       workspaceMutation
	)
	materializeStarted := time.Now()
	materializeErr := func() error {
		var syncErr error
		sourceFiles, sourceStamps, syncErr = syncSourceFilesWithSnapshotObserved(workspaceDir, appRoot, state.SourceStamps, generatedPaths, snapshot, &mutation)
		if syncErr != nil {
			return syncErr
		}
		generatedFiles, syncErr = syncGeneratedFilesObserved(workspaceDir, appRoot, gen, state.GeneratedFiles, sourceFiles, &mutation)
		if syncErr != nil {
			return syncErr
		}
		if syncErr = removeUnexpectedFilesFromListsObserved(workspaceDir, sourceFiles, generatedFiles, &mutation); syncErr != nil {
			return syncErr
		}
		return seedWorkspaceSceneryGoSumObserved(workspaceDir, &mutation)
	}()
	recordWorkspaceMaterialization(ctx, materializeStarted, mutation, materializeErr)
	if materializeErr != nil {
		return nil, materializeErr
	}
	generatedStamps, err := artifactPathStamps(workspaceDir, generatedFiles)
	if err != nil {
		return nil, err
	}
	publicGeneratedPaths := make([]string, 0, len(projection.Files))
	for rel := range projection.Files {
		if _, statErr := os.Lstat(filepath.Join(appRoot, filepath.FromSlash(rel))); statErr == nil {
			publicGeneratedPaths = append(publicGeneratedPaths, rel)
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
	}
	publicGeneratedStamps, err := artifactPathStamps(appRoot, publicGeneratedPaths)
	if err != nil {
		return nil, err
	}
	cachedTypeScriptStamps, err := artifactPathStamps(appRoot, typeScriptPaths)
	if err != nil {
		return nil, err
	}
	managedGenerated, err := compiler.GeneratedPaths(appRoot)
	if err != nil {
		return nil, err
	}
	managedGeneratedPaths := make([]string, 0, len(managedGenerated))
	for rel := range managedGenerated {
		managedGeneratedPaths = append(managedGeneratedPaths, filepath.ToSlash(rel))
	}
	sort.Strings(managedGeneratedPaths)
	sourceMetadataFingerprint := sourceStampsFingerprint(sourceStamps)
	preparationFingerprint, err := PreparationFingerprint(cfg, contract)
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
		PreparationFingerprint:    preparationFingerprint,
		BuildFingerprint:          buildFingerprint,
		SourceFiles:               sourceFiles,
		SourceStamps:              sourceStamps,
		GeneratedFiles:            generatedFiles,
		GeneratedStamps:           generatedStamps,
		PublicGeneratedStamps:     publicGeneratedStamps,
		CachedTypeScriptStamps:    cachedTypeScriptStamps,
		VerificationPatterns:      append([]string(nil), projection.VerificationPatterns...),
		ManagedGeneratedPaths:     managedGeneratedPaths,
		GoBuildFlags:              append([]string(nil), goBuildFlags...),
		Contract:                  contract,
		Target:                    &target,
		verification:              &preparedVerification{patterns: append([]string(nil), projection.VerificationPatterns...)},
	}
	result.GoEnvironment = gotarget.Environment(target.Context)
	if err := WriteLatestBuildManifest(result, "prepared"); err != nil {
		return nil, err
	}
	return result, nil
}
