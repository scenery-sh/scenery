package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/model"
)

func Prepare(appRoot string, model *model.App, cfg app.Config) (*Result, error) {
	return PrepareWithSnapshot(appRoot, model, cfg, nil)
}

func PrepareWithSnapshot(appRoot string, model *model.App, cfg app.Config, snapshot *SourceSnapshot) (*Result, error) {
	goBuildFlags := normalizeGoBuildFlags(cfg.Build.GoFlags)
	artifacts, err := writeGeneratedInspectArtifacts(appRoot, cfg, model)
	if err != nil {
		return nil, err
	}
	gen, err := codegen.GenerateWithConfig(model, cfg)
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
		GeneratorFingerprint:      generatorFingerprint,
		BuildFingerprint:          buildFingerprint,
		ReuseCompiled:             buildFingerprint != "" && pathExists(binary),
		SourceFiles:               sourceFiles,
		SourceStamps:              sourceStamps,
		GeneratedFiles:            generatedFiles,
		GoBuildFlags:              append([]string(nil), goBuildFlags...),
	}
	if err := WriteLatestBuildManifest(result, "prepared"); err != nil {
		return nil, err
	}
	if err := writeGeneratedManifest(appRoot, artifacts); err != nil {
		return nil, err
	}
	return result, nil
}

func writeGeneratedInspectArtifacts(appRoot string, cfg app.Config, appModel *model.App) (*generatedInspectArtifacts, error) {
	artifacts := &generatedInspectArtifacts{
		App:       inspectdata.BuildAppResponse(appRoot, cfg, appModel),
		Routes:    inspectdata.BuildRoutesResponse(appRoot, cfg, appModel),
		Services:  inspectdata.BuildServicesResponse(appRoot, cfg, appModel),
		Endpoints: inspectdata.BuildEndpointsResponse(appRoot, cfg, appModel),
		Models:    inspectdata.BuildModelsResponse(appRoot, cfg, appModel),
		Views:     inspectdata.BuildViewsResponse(appRoot, cfg, appModel),
	}
	genDir := filepath.Join(appRoot, ".scenery", "gen")
	files := map[string]*[]byte{
		"app.json":       &artifacts.AppJSON,
		"routes.json":    &artifacts.RoutesJSON,
		"services.json":  &artifacts.ServicesJSON,
		"endpoints.json": &artifacts.EndpointsJSON,
		"models.json":    &artifacts.ModelsJSON,
		"views.json":     &artifacts.ViewsJSON,
	}
	payloads := map[string]any{
		"app.json":       artifacts.App,
		"routes.json":    artifacts.Routes,
		"services.json":  artifacts.Services,
		"endpoints.json": artifacts.Endpoints,
		"models.json":    artifacts.Models,
		"views.json":     artifacts.Views,
	}
	for name, target := range files {
		data, err := json.MarshalIndent(payloads[name], "", "  ")
		if err != nil {
			return nil, err
		}
		data = append(data, '\n')
		if err := writeFileIfChanged(genDir, name, data); err != nil {
			return nil, err
		}
		*target = data
	}
	return artifacts, nil
}

func writeGeneratedManifest(appRoot string, artifacts *generatedInspectArtifacts) error {
	if artifacts == nil {
		return fmt.Errorf("nil generated inspect artifacts")
	}
	manifest := GeneratedManifest{
		SchemaVersion: "scenery.gen.manifest.v1",
		App:           artifacts.App.App,
		Counts:        artifacts.App.Counts,
		Artifacts: GeneratedManifestPaths{
			App:         ".scenery/gen/app.json",
			Routes:      ".scenery/gen/routes.json",
			Services:    ".scenery/gen/services.json",
			Endpoints:   ".scenery/gen/endpoints.json",
			Models:      ".scenery/gen/models.json",
			Views:       ".scenery/gen/views.json",
			BuildLatest: ".scenery/build/latest.json",
		},
		Schemas: GeneratedManifestSchema{
			App:         artifacts.App.SchemaVersion,
			Routes:      artifacts.Routes.SchemaVersion,
			Services:    artifacts.Services.SchemaVersion,
			Endpoints:   artifacts.Endpoints.SchemaVersion,
			Models:      artifacts.Models.SchemaVersion,
			Views:       artifacts.Views.SchemaVersion,
			BuildLatest: "scenery.build.latest.v1",
		},
		Hashes: GeneratedManifestHashes{
			App:       sha256Hex(artifacts.AppJSON),
			Routes:    sha256Hex(artifacts.RoutesJSON),
			Services:  sha256Hex(artifacts.ServicesJSON),
			Endpoints: sha256Hex(artifacts.EndpointsJSON),
			Models:    sha256Hex(artifacts.ModelsJSON),
			Views:     sha256Hex(artifacts.ViewsJSON),
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileIfChanged(filepath.Join(appRoot, ".scenery", "gen"), "manifest.json", data)
}
