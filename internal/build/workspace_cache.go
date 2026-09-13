package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/gotarget"
)

// PreparationFingerprint identifies declaration/configuration work that is
// independent of ordinary implementation bytes. Producer/generator identity is
// checked separately when the persisted preparation is loaded.
func PreparationFingerprint(cfg app.Config, contract *compiler.Result) (string, error) {
	if contract == nil || !contract.Valid() || contract.Manifest == nil {
		return "", fmt.Errorf("preparation fingerprint requires a valid contract")
	}
	encoded, err := json.Marshal(struct {
		Config           app.Config `json:"config"`
		ContractRevision string     `json:"contract_revision"`
	}{Config: cfg, ContractRevision: contract.Manifest.ContractRevision})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

const absentArtifactStamp = "absent"

func artifactPathStamps(root string, paths []string) (map[string]string, error) {
	stamps := make(map[string]string, len(paths))
	for _, rel := range paths {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, fmt.Errorf("artifact path escapes root: %s", rel)
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			stamps[rel] = absentArtifactStamp
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("artifact path is not a regular file: %s", rel)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		stamps[rel] = hex.EncodeToString(sum[:])
	}
	return stamps, nil
}

func dependencyFingerprintFromWorkspace(root string) (string, error) {
	return dependencyFingerprintFromInventory(newWorkspaceInventory(root))
}

func dependencyFingerprintFromInventory(inventory *workspaceInventory) (string, error) {
	root := inventory.root
	h := sha256.New()
	if data, err := inventory.read("go.mod"); err == nil {
		_, _ = h.Write([]byte("go.mod\x00"))
		_, _ = h.Write(data)
	}
	if data, err := inventory.read("go.sum"); err == nil {
		_, _ = h.Write([]byte("go.sum\x00"))
		_, _ = h.Write(data)
	}
	var goFiles []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && shouldSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			goFiles = append(goFiles, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(goFiles)
	for _, rel := range goFiles {
		data, err := inventory.read(rel)
		if err != nil {
			return "", err
		}
		imports, err := goImports(data)
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		for _, imp := range imports {
			_, _ = h.Write([]byte(imp))
			_, _ = h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func goImports(src []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, imp := range file.Imports {
		imports = append(imports, strings.Trim(imp.Path.Value, `"`))
	}
	sort.Strings(imports)
	return imports, nil
}

func loadBuildState(root string) (buildState, error) {
	path := filepath.Join(root, buildStateFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return buildState{}, nil
		}
		return buildState{}, err
	}
	var state buildState
	if err := json.Unmarshal(data, &state); err != nil {
		return buildState{}, err
	}
	return state, nil
}

func LoadCachedGraph(appRoot string, cfg app.Config, graphFingerprint string) (*CachedGraph, bool, error) {
	return LoadCachedGraphContext(context.Background(), appRoot, cfg, graphFingerprint)
}

func LoadCachedGraphContext(ctx context.Context, appRoot string, cfg app.Config, graphFingerprint string) (cached *CachedGraph, hit bool, err error) {
	return loadCachedGraphContext(ctx, appRoot, cfg, graphFingerprint, "", nil)
}

// LoadCachedPreparationContext reuses declaration-derived preparation across
// implementation-only source changes. graphFingerprint remains the identity of
// the complete captured input set; preparationFingerprint deliberately omits
// implementation bytes and is accepted only with the caller's freshly
// compiled, matching contract.
func LoadCachedPreparationContext(ctx context.Context, appRoot string, cfg app.Config, graphFingerprint, preparationFingerprint string, contract *compiler.Result) (*CachedGraph, bool, error) {
	if contract == nil || !contract.Valid() || preparationFingerprint == "" {
		return nil, false, nil
	}
	return loadCachedGraphContext(ctx, appRoot, cfg, graphFingerprint, preparationFingerprint, contract)
}

func loadCachedGraphContext(ctx context.Context, appRoot string, cfg app.Config, graphFingerprint, preparationFingerprint string, contract *compiler.Result) (cached *CachedGraph, hit bool, err error) {
	started := time.Now()
	reason := "read_failed"
	defer func() {
		cache := "miss"
		if hit {
			cache = "hit"
		}
		finishStep(ctx, "graph.cache", started, cache, reason, err)
	}()
	goBuildFlags := normalizeGoBuildFlags(cfg.Build.GoFlags)
	root, err := workspaceDir(appRoot, cfg.Name)
	if err != nil {
		return nil, false, err
	}
	state, err := loadBuildState(root)
	if err != nil {
		return nil, false, err
	}
	if state.Version != buildStateVersion {
		reason = "build_state_missing_or_changed"
		return nil, false, nil
	}
	if preparationFingerprint == "" {
		if state.GraphFingerprint == "" || state.GraphFingerprint != graphFingerprint {
			reason = "source_snapshot_changed"
			return nil, false, nil
		}
	} else if state.PreparationFingerprint == "" || state.PreparationFingerprint != preparationFingerprint {
		reason = "preparation_inputs_changed"
		return nil, false, nil
	}
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		return nil, false, err
	}
	if state.GeneratorFingerprint == "" || state.GeneratorFingerprint != generatorFingerprint {
		reason = "generator_changed"
		return nil, false, nil
	}
	if !slices.Equal(state.GoBuildFlags, goBuildFlags) {
		reason = "go_flags_changed"
		return nil, false, nil
	}
	if _, err := os.Stat(filepath.Join(root, "scenery_internal_main", "main.go")); err != nil {
		reason = "generated_main_missing"
		return nil, false, nil
	}
	if state.BuildFingerprint == "" {
		reason = "build_fingerprint_missing"
		return nil, false, nil
	}
	if len(state.Metadata) == 0 || len(state.APIEncoding) == 0 {
		reason = "metadata_missing"
		return nil, false, nil
	}
	result := &Result{
		AppRoot:                   appRoot,
		AppName:                   cfg.Name,
		AppID:                     cfg.ID,
		Dir:                       root,
		Binary:                    filepath.Join(root, workspaceBinaryName(appRoot, state.BuildFingerprint)),
		NeedsTidy:                 false,
		DependencyFingerprint:     state.DependencyFingerprint,
		SourceFingerprint:         state.SourceFingerprint,
		SourceMetadataFingerprint: state.SourceMetadataFingerprint,
		FrameworkFingerprint:      state.FrameworkFingerprint,
		GeneratorFingerprint:      state.GeneratorFingerprint,
		PreparationFingerprint:    state.PreparationFingerprint,
		BuildFingerprint:          state.BuildFingerprint,
		GraphFingerprint:          state.GraphFingerprint,
		Metadata:                  append(json.RawMessage(nil), state.Metadata...),
		APIEncoding:               append(json.RawMessage(nil), state.APIEncoding...),
		SourceFiles:               sourceFilesFromStamps(state.SourceStamps),
		SourceStamps:              maps.Clone(state.SourceStamps),
		GeneratedFiles:            append([]string(nil), state.GeneratedFiles...),
		GeneratedStamps:           maps.Clone(state.GeneratedStamps),
		PublicGeneratedStamps:     maps.Clone(state.PublicGeneratedStamps),
		CachedTypeScriptStamps:    maps.Clone(state.CachedTypeScriptStamps),
		VerificationPatterns:      append([]string(nil), state.VerificationPatterns...),
		ManagedGeneratedPaths:     append([]string(nil), state.ManagedGeneratedPaths...),
		GoBuildFlags:              append([]string(nil), goBuildFlags...),
		Contract:                  contract,
	}
	if preparationFingerprint != "" {
		result.GraphFingerprint = graphFingerprint
	}
	reason = "source_and_generator_match"
	return &CachedGraph{
		Result:      result,
		Metadata:    append(json.RawMessage(nil), state.Metadata...),
		APIEncoding: append(json.RawMessage(nil), state.APIEncoding...),
	}, true, nil
}

func RefreshCachedWorkspace(appRoot string, result *Result) (bool, error) {
	return RefreshCachedWorkspaceWithSnapshot(appRoot, result, nil)
}

func RefreshCachedWorkspaceWithSnapshot(appRoot string, result *Result, snapshot *SourceSnapshot) (bool, error) {
	return RefreshCachedWorkspaceWithSnapshotContext(context.Background(), appRoot, result, snapshot)
}

func RefreshCachedWorkspaceWithSnapshotContext(ctx context.Context, appRoot string, result *Result, snapshot *SourceSnapshot) (reused bool, err error) {
	prepared, err := PrepareCachedWorkspaceWithSnapshotContext(ctx, appRoot, app.Config{}, result, snapshot)
	return prepared, err
}

// PrepareCachedWorkspaceWithSnapshotContext refreshes a declaration-equivalent
// private workspace for the current captured implementation. A true result
// means the workspace can proceed directly to CompileContext. Executable reuse
// belongs exclusively to the identity-bound shared binary cache; a bare old
// workspace executable has no generation-specific identity to authorize reuse.
func PrepareCachedWorkspaceWithSnapshotContext(ctx context.Context, appRoot string, cfg app.Config, result *Result, snapshot *SourceSnapshot) (prepared bool, err error) {
	started := time.Now()
	reason := "projection_changed_or_missing"
	defer func() {
		cache := "miss"
		if prepared {
			cache = "hit"
		}
		finishStep(ctx, "workspace.cache", started, cache, reason, err)
	}()
	if result == nil {
		return false, fmt.Errorf("nil build result")
	}
	contract := result.Contract
	var current bool
	if cfg.Name != "" {
		projectionStarted := time.Now()
		current, err = cachedProjectionCurrent(appRoot, result)
		cache, projectionReason := "miss", "artifact_missing_or_changed"
		if current {
			cache, projectionReason = "hit", "preparation_key_and_artifacts_match"
			result.verification = &preparedVerification{patterns: append([]string(nil), result.VerificationPatterns...)}
		}
		finishStep(ctx, "projection.go", projectionStarted, cache, projectionReason, err)
		finishStep(ctx, "projection.typescript", projectionStarted, cache, projectionReason, err)
	} else {
		current, err = refreshCachedGoProjection(appRoot, result, snapshot, contract)
	}
	if err != nil || !current {
		return false, err
	}
	contract = result.Contract
	target, err := compiler.ResolveGoBuildTarget(contract, "", "development")
	if err != nil {
		return false, err
	}
	goBuildFlags := append([]string(nil), target.Context.BuildFlags...)
	if len(target.Context.BuildTags) > 0 {
		goBuildFlags = append(goBuildFlags, "-tags="+strings.Join(target.Context.BuildTags, ","))
	}
	if cfg.Name != "" && cfg.Name != result.AppName {
		reason = "app_config_changed"
		return false, nil
	}
	if !slices.Equal(normalizeGoBuildFlags(goBuildFlags), normalizeGoBuildFlags(result.GoBuildFlags)) {
		reason = "target_flags_changed"
		return false, nil
	}
	result.Target = &target
	result.GoEnvironment = gotarget.Environment(target.Context)
	reason = "generated_file_missing"
	generated := make(map[string]struct{}, len(result.GeneratedFiles))
	for _, rel := range result.GeneratedFiles {
		rel = filepath.ToSlash(rel)
		generated[rel] = struct{}{}
		if _, err := os.Stat(filepath.Join(result.Dir, filepath.FromSlash(rel))); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
	}
	var mutation workspaceMutation
	materializeStarted := time.Now()
	materializeErr := func() error {
		sourceFiles, sourceStamps, syncErr := syncSourceFilesWithSnapshotObserved(result.Dir, appRoot, result.SourceStamps, generated, snapshot, &mutation)
		if syncErr != nil {
			return syncErr
		}
		result.SourceFiles = sourceFiles
		result.SourceStamps = sourceStamps
		result.SourceMetadataFingerprint = sourceStampsFingerprint(sourceStamps)
		return removeUnexpectedFilesFromListsObserved(result.Dir, result.SourceFiles, result.GeneratedFiles, &mutation)
	}()
	recordWorkspaceMaterialization(ctx, materializeStarted, mutation, materializeErr)
	if materializeErr != nil {
		return false, materializeErr
	}
	previousFrameworkFingerprint := result.FrameworkFingerprint
	frameworkFingerprint, _, err := currentFrameworkFingerprintFromWorkspace(result.Dir)
	if err != nil {
		return false, err
	}
	result.FrameworkFingerprint = frameworkFingerprint
	// A cached graph does not carry the resolved target needed to regenerate
	// runtime linker metadata. Re-prepare the full build when Scenery itself
	// changes instead of producing an unbound runtime binary.
	if previousFrameworkFingerprint != frameworkFingerprint {
		reason = "framework_changed"
		return false, nil
	}
	inventory := newWorkspaceInventory(result.Dir)
	depFingerprint, err := dependencyFingerprintFromInventory(inventory)
	if err != nil {
		return false, err
	}
	result.NeedsTidy = result.DependencyFingerprint != depFingerprint
	result.DependencyFingerprint = depFingerprint
	buildFingerprint, err := workspaceBuildFingerprintFromInventory(inventory, result.GoBuildFlags, result.SourceFiles, result.GeneratedFiles)
	if err != nil {
		return false, err
	}
	previousBuildFingerprint := result.BuildFingerprint
	result.BuildFingerprint = buildFingerprint
	result.Binary = filepath.Join(result.Dir, workspaceBinaryName(appRoot, buildFingerprint))
	reason = "workspace_binary_missing"
	if previousBuildFingerprint != buildFingerprint {
		reason = fmt.Sprintf("workspace_inputs_changed:%s:%s", previousBuildFingerprint, buildFingerprint)
	}
	if reason == "workspace_binary_missing" {
		reason = "prepared_workspace_requires_compile"
	}
	return true, nil
}

func cachedProjectionCurrent(appRoot string, result *Result) (bool, error) {
	if result == nil || result.Contract == nil || !result.Contract.Valid() || len(result.GeneratedFiles) == 0 || len(result.GeneratedStamps) == 0 {
		return false, nil
	}
	managed, err := compiler.GeneratedPaths(appRoot)
	if err != nil {
		return false, err
	}
	managedPaths := make([]string, 0, len(managed))
	for rel := range managed {
		managedPaths = append(managedPaths, filepath.ToSlash(rel))
	}
	sort.Strings(managedPaths)
	if !slices.Equal(managedPaths, result.ManagedGeneratedPaths) {
		return false, nil
	}
	private, err := artifactPathStamps(result.Dir, result.GeneratedFiles)
	if err != nil || !maps.Equal(private, result.GeneratedStamps) {
		return false, err
	}
	publicPaths := make([]string, 0, len(result.PublicGeneratedStamps))
	for rel := range result.PublicGeneratedStamps {
		publicPaths = append(publicPaths, rel)
	}
	public, err := artifactPathStamps(appRoot, publicPaths)
	if err != nil || !maps.Equal(public, result.PublicGeneratedStamps) {
		return false, err
	}
	typeScriptPaths := make([]string, 0, len(result.CachedTypeScriptStamps))
	for rel := range result.CachedTypeScriptStamps {
		typeScriptPaths = append(typeScriptPaths, rel)
	}
	typeScript, err := artifactPathStamps(appRoot, typeScriptPaths)
	if err != nil || !maps.Equal(typeScript, result.CachedTypeScriptStamps) {
		return false, err
	}
	return true, nil
}

// A cached executable is usable only after publishing the current public
// projection and proving its private workspace still contains those bytes.
// Cache metadata alone cannot establish this after deletion or a branch switch.
func refreshCachedGoProjection(appRoot string, result *Result, snapshot *SourceSnapshot, prepared ...*compiler.Result) (bool, error) {
	if err := requireGenerateHooks(); err != nil {
		return false, err
	}
	var contract *compiler.Result
	if len(prepared) > 0 {
		contract = prepared[0]
	}
	if contract == nil {
		var err error
		contract, err = compileWorkspaceContract(appRoot, snapshot)
		if err != nil {
			return false, err
		}
	}
	if !contract.Valid() {
		return false, nil
	}
	projection, err := generateHooks.PrepareBuildGoWorkspace(contract)
	if err != nil {
		return false, err
	}
	if _, err := generateHooks.SyncCachedTypeScript(contract); err != nil {
		return false, err
	}
	for rel, expected := range projection.Files {
		actual, err := os.ReadFile(filepath.Join(result.Dir, filepath.FromSlash(rel)))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !bytes.Equal(actual, expected) {
			return false, nil
		}
	}
	// Runtime setup needs current compiled requirements even when the executable
	// is reusable. Retain this verified snapshot, not persisted cache metadata.
	result.Contract = contract
	result.verification = &preparedVerification{patterns: append([]string(nil), projection.VerificationPatterns...)}
	return true, nil
}

func saveBuildState(root string, state buildState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, buildStateFile), data, 0o644)
}

func workspaceBuildFingerprint(root string, goBuildFlags []string, groups ...[]string) (string, error) {
	return workspaceBuildFingerprintFromInventory(newWorkspaceInventory(root), goBuildFlags, groups...)
}

func workspaceBuildFingerprintFromInventory(inventory *workspaceInventory, goBuildFlags []string, groups ...[]string) (string, error) {
	// Tidy can create go.sum even when it is absent from authored source lists.
	// Both module files are consumed workspace inputs, not source projections.
	files := map[string]struct{}{"go.mod": {}, "go.sum": {}}
	for _, group := range groups {
		for _, rel := range group {
			rel = filepath.ToSlash(rel)
			if rel == "" {
				continue
			}
			files[rel] = struct{}{}
		}
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	h := sha256.New()
	_, _ = h.Write([]byte("go_build_flags"))
	_, _ = h.Write([]byte{0})
	for _, flag := range normalizeGoBuildFlags(goBuildFlags) {
		_, _ = h.Write([]byte(flag))
		_, _ = h.Write([]byte{0})
	}
	for _, rel := range paths {
		data, err := inventory.read(rel)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func syncGeneratedFiles(root, appRoot string, gen *codegen.Output, prev, sourceFiles []string) ([]string, error) {
	return syncGeneratedFilesObserved(root, appRoot, gen, prev, sourceFiles, nil)
}

func syncGeneratedFilesObserved(root, appRoot string, gen *codegen.Output, prev, sourceFiles []string, mutation *workspaceMutation) ([]string, error) {
	next := make(map[string][]byte, len(gen.Generated))
	for rel, data := range gen.Generated {
		next[filepath.ToSlash(rel)] = data
	}
	for rel, data := range next {
		if _, err := writeFileIfChangedObserved(root, rel, data, mutation); err != nil {
			return nil, err
		}
	}
	for _, rel := range prev {
		rel = filepath.ToSlash(rel)
		if _, ok := next[rel]; ok {
			continue
		}
		if slices.Contains(sourceFiles, rel) {
			continue
		}
		if _, err := removeFileIfExistsObserved(root, rel, mutation); err != nil {
			return nil, err
		}
	}
	paths := make([]string, 0, len(next))
	for rel := range next {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nil
}

func sortedKeys(set map[string]struct{}) []string {
	paths := make([]string, 0, len(set))
	for rel := range set {
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths
}
