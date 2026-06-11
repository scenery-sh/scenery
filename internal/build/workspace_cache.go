package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
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
		return nil, false, nil
	}
	if state.GraphFingerprint == "" || state.GraphFingerprint != graphFingerprint {
		return nil, false, nil
	}
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		return nil, false, err
	}
	if state.GeneratorFingerprint == "" || state.GeneratorFingerprint != generatorFingerprint {
		return nil, false, nil
	}
	if !slices.Equal(state.GoBuildFlags, goBuildFlags) {
		return nil, false, nil
	}
	if _, err := os.Stat(filepath.Join(root, "scenery_internal_main", "main.go")); err != nil {
		return nil, false, nil
	}
	if state.BuildFingerprint == "" {
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
	return &CachedGraph{
		Result:      result,
		Metadata:    append(json.RawMessage(nil), state.Metadata...),
		APIEncoding: append(json.RawMessage(nil), state.APIEncoding...),
	}, true, nil
}

func RefreshCachedWorkspace(appRoot string, result *Result) (bool, error) {
	if result == nil {
		return false, fmt.Errorf("nil build result")
	}
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
	sourceFiles, sourceStamps, err := syncSourceFiles(result.Dir, appRoot, result.SourceStamps, generated)
	if err != nil {
		return false, err
	}
	result.SourceFiles = sourceFiles
	result.SourceStamps = sourceStamps
	result.SourceMetadataFingerprint = sourceStampsFingerprint(sourceStamps)
	if err := removeUnexpectedFilesFromLists(result.Dir, result.SourceFiles, result.GeneratedFiles); err != nil {
		return false, err
	}
	if err := seedSceneryGoSum(result.Dir, app.RepoRoot()); err != nil {
		return false, err
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
	result.BuildFingerprint = buildFingerprint
	result.Binary = filepath.Join(result.Dir, workspaceBinaryName(appRoot, buildFingerprint))
	result.ReuseCompiled = pathExists(result.Binary)
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
	files := map[string]struct{}{}
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
	next := make(map[string][]byte, len(gen.Rewritten)+len(gen.Generated))
	for rel, data := range gen.Rewritten {
		rel = filepath.ToSlash(rel)
		if filepath.Ext(rel) == ".go" {
			var err error
			data, err = rewriteSceneryImports(filepath.Join(appRoot, rel), data)
			if err != nil {
				return nil, err
			}
		}
		next[rel] = data
	}
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

func rewriteSceneryImports(path string, src []byte) ([]byte, error) {
	text := string(src)
	needsPGXPoolRewrite := strings.Contains(text, "github.com/jackc/pgx/v5/pgxpool")
	if !needsPGXPoolRewrite {
		return src, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	changed := false
	if rewriteImportPath(file, "github.com/jackc/pgx/v5/pgxpool", "scenery.sh/pgxpool", "") {
		changed = true
	}

	if !changed {
		return src, nil
	}

	out, err := format.Source(renderAST(fset, file))
	if err != nil {
		return nil, err
	}
	return out, nil
}

func rewriteImportPath(file *ast.File, oldPath, newPath, alias string) bool {
	changed := false
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, "\"") != oldPath {
			continue
		}
		imp.Path.Value = fmt.Sprintf("%q", newPath)
		if alias != "" && imp.Name == nil {
			imp.Name = ast.NewIdent(alias)
		}
		changed = true
	}
	return changed
}

func renderAST(fset *token.FileSet, file *ast.File) []byte {
	var buf strings.Builder
	_ = format.Node(&buf, fset, file)
	return []byte(buf.String())
}
