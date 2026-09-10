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
)

func dependencyFingerprintFromWorkspace(root string) (string, error) {
	h := sha256.New()
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		_, _ = h.Write([]byte("go.mod\x00"))
		_, _ = h.Write(data)
	}
	if data, err := os.ReadFile(filepath.Join(root, "go.sum")); err == nil {
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
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
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
	if state.GraphFingerprint == "" || state.GraphFingerprint != graphFingerprint {
		reason = "source_snapshot_changed"
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
		BuildFingerprint:          state.BuildFingerprint,
		GraphFingerprint:          state.GraphFingerprint,
		Metadata:                  append(json.RawMessage(nil), state.Metadata...),
		APIEncoding:               append(json.RawMessage(nil), state.APIEncoding...),
		SourceFiles:               sourceFilesFromStamps(state.SourceStamps),
		SourceStamps:              maps.Clone(state.SourceStamps),
		GeneratedFiles:            append([]string(nil), state.GeneratedFiles...),
		GoBuildFlags:              append([]string(nil), goBuildFlags...),
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
	started := time.Now()
	reason := "projection_changed_or_missing"
	defer func() {
		cache := "miss"
		if reused {
			cache = "hit"
		}
		finishStep(ctx, "workspace.cache", started, cache, reason, err)
	}()
	if result == nil {
		return false, fmt.Errorf("nil build result")
	}
	current, err := refreshCachedGoProjection(appRoot, result)
	if err != nil || !current {
		return false, err
	}
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
	sourceFiles, sourceStamps, err := syncSourceFilesWithSnapshot(result.Dir, appRoot, result.SourceStamps, generated, snapshot)
	if err != nil {
		return false, err
	}
	result.SourceFiles = sourceFiles
	result.SourceStamps = sourceStamps
	result.SourceMetadataFingerprint = sourceStampsFingerprint(sourceStamps)
	if err := removeUnexpectedFilesFromLists(result.Dir, result.SourceFiles, result.GeneratedFiles); err != nil {
		return false, err
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
	depFingerprint, err := dependencyFingerprintFromWorkspace(result.Dir)
	if err != nil {
		return false, err
	}
	result.NeedsTidy = result.DependencyFingerprint != depFingerprint
	result.DependencyFingerprint = depFingerprint
	buildFingerprint, err := workspaceBuildFingerprint(result.Dir, result.GoBuildFlags, result.SourceFiles, result.GeneratedFiles)
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
	result.ReuseCompiled = pathExists(result.Binary) && previousFrameworkFingerprint == frameworkFingerprint
	if result.ReuseCompiled && !restoreCachedRuntimeIdentity(result) {
		// A binary cache hit without its current bound identity is not a runtime
		// candidate. Re-prepare normally instead of publishing an unbound result.
		result.ReuseCompiled = false
		reason = "runtime_identity_not_reusable"
	}
	if result.ReuseCompiled {
		reason = "verified_workspace_and_runtime_identity"
	}
	return result.ReuseCompiled, nil
}

// A cached executable is usable only after publishing the current public
// projection and proving its private workspace still contains those bytes.
// Cache metadata alone cannot establish this after deletion or a branch switch.
func refreshCachedGoProjection(appRoot string, result *Result) (bool, error) {
	if err := requireGenerateHooks(); err != nil {
		return false, err
	}
	contract, err := compiler.Compile(appRoot)
	if err != nil {
		return false, err
	}
	if !contract.Valid() {
		return false, nil
	}
	if err := generateHooks.SyncGoPackages(contract); err != nil {
		return false, err
	}
	if err := generateHooks.SyncCachedTypeScript(contract); err != nil {
		return false, err
	}
	rendered, err := generateHooks.RenderGoWorkspaceFiles(contract)
	if err != nil {
		return false, err
	}
	for rel, expected := range rendered {
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
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
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
	next := make(map[string][]byte, len(gen.Generated))
	for rel, data := range gen.Generated {
		next[filepath.ToSlash(rel)] = data
	}
	for rel, data := range next {
		if err := writeFileIfChanged(root, rel, data); err != nil {
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
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
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
