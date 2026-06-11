package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/envpolicy"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/wiremodel"
)

type Result struct {
	AppRoot                   string
	AppName                   string
	AppID                     string
	Dir                       string
	Binary                    string
	NeedsTidy                 bool
	DependencyFingerprint     string
	SourceFingerprint         string
	SourceMetadataFingerprint string
	GeneratorFingerprint      string
	BuildFingerprint          string
	GraphFingerprint          string
	Metadata                  json.RawMessage
	APIEncoding               json.RawMessage
	SourceFiles               []string
	SourceStamps              map[string]SourceStamp
	GeneratedFiles            []string
	ReuseCompiled             bool
	Ephemeral                 bool
	GoBuildFlags              []string
}

// SourceStamp records the size/mtime/permissions of an app source file as
// observed immediately before its content was copied into the workspace. A
// stamp therefore proves the copy happened for that exact on-disk state; if
// the source file changes again afterwards, its stat no longer matches and
// the next sync rewrites it, regardless of what any file watcher reported.
type SourceStamp struct {
	Size        int64  `json:"size"`
	ModTimeNano int64  `json:"mtime_unix_nano"`
	Perm        uint32 `json:"perm"`
}

type buildState struct {
	Version                   string                 `json:"version,omitempty"`
	DependencyFingerprint     string                 `json:"dependency_fingerprint"`
	SourceFingerprint         string                 `json:"source_fingerprint,omitempty"`
	SourceMetadataFingerprint string                 `json:"source_metadata_fingerprint,omitempty"`
	GeneratorFingerprint      string                 `json:"generator_fingerprint,omitempty"`
	BuildFingerprint          string                 `json:"build_fingerprint,omitempty"`
	GraphFingerprint          string                 `json:"graph_fingerprint,omitempty"`
	Metadata                  []byte                 `json:"metadata,omitempty"`
	APIEncoding               []byte                 `json:"api_encoding,omitempty"`
	SourceStamps              map[string]SourceStamp `json:"source_file_stamps,omitempty"`
	GeneratedFiles            []string               `json:"generated_files,omitempty"`
	GoBuildFlags              []string               `json:"go_build_flags,omitempty"`
}

const (
	buildStateFile    = ".scenery-build-state.json"
	buildStateVersion = "4"
)

type CachedGraph struct {
	Result      *Result
	Metadata    json.RawMessage
	APIEncoding json.RawMessage
}

type GeneratedManifest struct {
	SchemaVersion string                  `json:"schema_version"`
	App           inspectdata.AppRef      `json:"app"`
	Counts        inspectdata.AppCounts   `json:"counts"`
	Artifacts     GeneratedManifestPaths  `json:"artifacts"`
	Schemas       GeneratedManifestSchema `json:"schemas"`
	Hashes        GeneratedManifestHashes `json:"hashes"`
}

type GeneratedManifestPaths struct {
	App              string `json:"app"`
	Routes           string `json:"routes"`
	Services         string `json:"services"`
	Endpoints        string `json:"endpoints"`
	WireCapabilities string `json:"wire_capabilities"`
	BuildLatest      string `json:"build_latest"`
}

type GeneratedManifestSchema struct {
	App              string `json:"app"`
	Routes           string `json:"routes"`
	Services         string `json:"services"`
	Endpoints        string `json:"endpoints"`
	WireCapabilities string `json:"wire_capabilities"`
	BuildLatest      string `json:"build_latest"`
}

type GeneratedManifestHashes struct {
	App              string `json:"app"`
	Routes           string `json:"routes"`
	Services         string `json:"services"`
	Endpoints        string `json:"endpoints"`
	WireCapabilities string `json:"wire_capabilities"`
}

type generatedInspectArtifacts struct {
	App                  inspectdata.AppResponse
	Routes               inspectdata.RoutesResponse
	Services             inspectdata.ServicesResponse
	Endpoints            inspectdata.EndpointsResponse
	WireCapabilities     any
	AppJSON              []byte
	RoutesJSON           []byte
	ServicesJSON         []byte
	EndpointsJSON        []byte
	WireCapabilitiesJSON []byte
}

type LatestBuildManifest struct {
	SchemaVersion string                    `json:"schema_version"`
	App           LatestBuildManifestApp    `json:"app"`
	Build         LatestBuildManifestRecord `json:"build"`
}

type LatestBuildManifestApp struct {
	Name       string `json:"name"`
	ID         string `json:"id,omitempty"`
	Root       string `json:"root"`
	ConfigPath string `json:"config_path"`
}

type LatestBuildManifestRecord struct {
	Phase                 string `json:"phase"`
	WorkspaceDir          string `json:"workspace_dir"`
	BinaryPath            string `json:"binary_path"`
	WorkspaceExists       bool   `json:"workspace_exists"`
	BinaryExists          bool   `json:"binary_exists"`
	BuildStatePath        string `json:"build_state_path"`
	BuildStateExists      bool   `json:"build_state_exists"`
	BuildStateVersion     string `json:"build_state_version,omitempty"`
	DependencyFingerprint string `json:"dependency_fingerprint,omitempty"`
	GraphFingerprint      string `json:"graph_fingerprint,omitempty"`
	MetadataPresent       bool   `json:"metadata_present"`
	APIEncodingPresent    bool   `json:"api_encoding_present"`
	SourceFileCount       int    `json:"source_file_count"`
	GeneratedFileCount    int    `json:"generated_file_count"`
}

func App(appRoot string, cfg app.Config) (*Result, error) {
	model, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return nil, err
	}
	result, err := Prepare(appRoot, model, cfg)
	if err != nil {
		return nil, err
	}
	if err := Compile(result); err != nil {
		if result.Ephemeral {
			_ = os.RemoveAll(result.Dir)
		}
		return nil, err
	}
	return result, nil
}

func LoadReusableBinary(appRoot string, cfg app.Config) (*Result, bool, error) {
	goBuildFlags := normalizeGoBuildFlags(cfg.Build.GoFlags)
	sourceFingerprint, err := currentAppSourceFingerprint(appRoot)
	if err != nil {
		return nil, false, err
	}
	generatorFingerprint, err := currentGeneratorFingerprint()
	if err != nil {
		return nil, false, err
	}
	workspaceDir, err := workspaceDir(appRoot, cfg.Name)
	if err != nil {
		return nil, false, err
	}
	unlock, err := lockWorkspace(workspaceDir)
	if err != nil {
		return nil, false, err
	}
	defer unlock()
	state, err := loadBuildState(workspaceDir)
	if err != nil {
		return nil, false, err
	}
	if state.Version != buildStateVersion ||
		state.SourceFingerprint == "" ||
		state.SourceFingerprint != sourceFingerprint ||
		state.GeneratorFingerprint == "" ||
		state.GeneratorFingerprint != generatorFingerprint ||
		state.BuildFingerprint == "" ||
		!slices.Equal(state.GoBuildFlags, goBuildFlags) {
		return nil, false, nil
	}
	binary := filepath.Join(workspaceDir, workspaceBinaryName(appRoot, state.BuildFingerprint))
	if !pathExists(binary) {
		return nil, false, nil
	}
	result := &Result{
		AppRoot:                   appRoot,
		AppName:                   cfg.Name,
		AppID:                     cfg.ID,
		Dir:                       workspaceDir,
		Binary:                    binary,
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
		ReuseCompiled:             true,
		GoBuildFlags:              append([]string(nil), goBuildFlags...),
	}
	return result, true, nil
}

func Prepare(appRoot string, model *model.App, cfg app.Config) (*Result, error) {
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
	sourceFingerprint, err := currentAppSourceFingerprint(appRoot)
	if err != nil {
		return nil, err
	}
	sourceFiles, sourceStamps, err := syncSourceFiles(workspaceDir, appRoot, state.SourceStamps, nil)
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
		App:              inspectdata.BuildAppResponse(appRoot, cfg, appModel),
		Routes:           inspectdata.BuildRoutesResponse(appRoot, cfg, appModel),
		Services:         inspectdata.BuildServicesResponse(appRoot, cfg, appModel),
		Endpoints:        inspectdata.BuildEndpointsResponse(appRoot, cfg, appModel),
		WireCapabilities: wiremodel.AppCapabilities(appModel),
	}
	genDir := filepath.Join(appRoot, ".scenery", "gen")
	files := map[string]*[]byte{
		"app.json":               &artifacts.AppJSON,
		"routes.json":            &artifacts.RoutesJSON,
		"services.json":          &artifacts.ServicesJSON,
		"endpoints.json":         &artifacts.EndpointsJSON,
		"wire/capabilities.json": &artifacts.WireCapabilitiesJSON,
	}
	payloads := map[string]any{
		"app.json":               artifacts.App,
		"routes.json":            artifacts.Routes,
		"services.json":          artifacts.Services,
		"endpoints.json":         artifacts.Endpoints,
		"wire/capabilities.json": artifacts.WireCapabilities,
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
			App:              ".scenery/gen/app.json",
			Routes:           ".scenery/gen/routes.json",
			Services:         ".scenery/gen/services.json",
			Endpoints:        ".scenery/gen/endpoints.json",
			WireCapabilities: ".scenery/gen/wire/capabilities.json",
			BuildLatest:      ".scenery/build/latest.json",
		},
		Schemas: GeneratedManifestSchema{
			App:              artifacts.App.SchemaVersion,
			Routes:           artifacts.Routes.SchemaVersion,
			Services:         artifacts.Services.SchemaVersion,
			Endpoints:        artifacts.Endpoints.SchemaVersion,
			WireCapabilities: "scenery.wire.capabilities.v1",
			BuildLatest:      "scenery.build.latest.v1",
		},
		Hashes: GeneratedManifestHashes{
			App:              sha256Hex(artifacts.AppJSON),
			Routes:           sha256Hex(artifacts.RoutesJSON),
			Services:         sha256Hex(artifacts.ServicesJSON),
			Endpoints:        sha256Hex(artifacts.EndpointsJSON),
			WireCapabilities: sha256Hex(artifacts.WireCapabilitiesJSON),
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileIfChanged(filepath.Join(appRoot, ".scenery", "gen"), "manifest.json", data)
}

func Compile(result *Result) error {
	return CompileContext(context.Background(), result)
}

func PrimeWorkspace(result *Result) error {
	return PrimeWorkspaceContext(context.Background(), result)
}

func PrimeWorkspaceContext(ctx context.Context, result *Result) error {
	if result == nil {
		return fmt.Errorf("nil build result")
	}
	if result.NeedsTidy {
		if err := tidyWorkspace(ctx, result); err != nil {
			return err
		}
	}
	return savePrimedWorkspace(result)
}

func tidyWorkspace(ctx context.Context, result *Result) error {
	if err := runGoContext(ctx, result.Dir, "mod", "tidy"); err != nil {
		return err
	}
	fingerprint, err := dependencyFingerprintFromWorkspace(result.Dir)
	if err != nil {
		return err
	}
	result.DependencyFingerprint = fingerprint
	result.NeedsTidy = false
	return nil
}

func savePrimedWorkspace(result *Result) error {
	if err := saveBuildState(result.Dir, buildState{
		Version:                   buildStateVersion,
		DependencyFingerprint:     result.DependencyFingerprint,
		SourceFingerprint:         result.SourceFingerprint,
		SourceMetadataFingerprint: result.SourceMetadataFingerprint,
		GeneratorFingerprint:      result.GeneratorFingerprint,
		BuildFingerprint:          result.BuildFingerprint,
		GraphFingerprint:          result.GraphFingerprint,
		Metadata:                  append([]byte(nil), result.Metadata...),
		APIEncoding:               append([]byte(nil), result.APIEncoding...),
		SourceStamps:              maps.Clone(result.SourceStamps),
		GeneratedFiles:            append([]string(nil), result.GeneratedFiles...),
		GoBuildFlags:              append([]string(nil), result.GoBuildFlags...),
	}); err != nil {
		return err
	}
	if err := WriteLatestBuildManifest(result, "primed"); err != nil {
		return err
	}
	return nil
}

func CompileContext(ctx context.Context, result *Result) error {
	if result == nil {
		return fmt.Errorf("nil build result")
	}
	unlock, err := lockWorkspace(result.Dir)
	if err != nil {
		return err
	}
	defer unlock()
	if result.ReuseCompiled {
		result.NeedsTidy = false
		if err := savePrimedWorkspace(result); err != nil {
			return err
		}
		return WriteLatestBuildManifest(result, "compiled")
	}
	if !result.NeedsTidy {
		if err := savePrimedWorkspace(result); err != nil {
			return err
		}
	}
	err = runGoContext(ctx, result.Dir, goBuildArgs(result.Binary, result.GoBuildFlags)...)
	if err != nil && (result.NeedsTidy || goBuildNeedsWorkspaceTidy(err)) {
		if tidyErr := tidyWorkspace(ctx, result); tidyErr != nil {
			return tidyErr
		}
		if saveErr := savePrimedWorkspace(result); saveErr != nil {
			return saveErr
		}
		err = runGoContext(ctx, result.Dir, goBuildArgs(result.Binary, result.GoBuildFlags)...)
	}
	if err != nil {
		return err
	}
	if result.NeedsTidy {
		fingerprint, fingerprintErr := dependencyFingerprintFromWorkspace(result.Dir)
		if fingerprintErr != nil {
			return fingerprintErr
		}
		result.DependencyFingerprint = fingerprint
		result.NeedsTidy = false
		if err := savePrimedWorkspace(result); err != nil {
			return err
		}
	}
	if err := WriteLatestBuildManifest(result, "compiled"); err != nil {
		return err
	}
	return nil
}

func goBuildNeedsWorkspaceTidy(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "missing go.sum entry") ||
		strings.Contains(text, "updates to go.mod needed") ||
		strings.Contains(text, "go.mod updates are needed")
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && (shouldSkipDir(rel) || shouldSkipRuntimeArtifactDir(rel)) {
			return filepath.SkipDir
		}
		if !d.IsDir() && shouldSkipFile(rel) {
			return nil
		}
		if shouldSkipSymlink(path, d) {
			return nil
		}
		if !d.IsDir() && shouldSkipNonRegularFile(path, d) {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

// syncSourceFiles mirrors the app's source files into the workspace and
// returns the synced file list plus the stamp observed for every file. A file
// is skipped only when its current stat still matches the stamp recorded by a
// previous sync and the workspace copy exists; anything else is re-read and
// rewritten. The sync never trusts external change notifications, so changes
// missed by a file watcher (git pulls, edits while stopped) are still picked
// up here. Stamps are captured before reading the content: if a file changes
// mid-read, the recorded stamp is older than its stat and the next sync
// rewrites it. Files in skip are tracked and stamped but never written; their
// workspace content is owned by generated-file sync.
func syncSourceFiles(root, appRoot string, prevStamps map[string]SourceStamp, skip map[string]struct{}) ([]string, map[string]SourceStamp, error) {
	currentFiles, err := listSourceFiles(appRoot)
	if err != nil {
		return nil, nil, err
	}
	stamps := make(map[string]SourceStamp, len(currentFiles))
	for _, rel := range currentFiles {
		src := filepath.Join(appRoot, filepath.FromSlash(rel))
		info, err := os.Stat(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, nil, err
		}
		stamp := sourceStampFromInfo(info)
		if _, ok := skip[rel]; ok {
			stamps[rel] = stamp
			continue
		}
		if prev, ok := prevStamps[rel]; ok && prev == stamp {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				stamps[rel] = stamp
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
		}
		data, err := sourceFileData(src, rel)
		if err != nil {
			return nil, nil, err
		}
		if err := writeFileIfChanged(root, rel, data); err != nil {
			return nil, nil, err
		}
		stamps[rel] = stamp
	}
	for rel := range prevStamps {
		if _, ok := stamps[rel]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
	}
	return sourceFilesFromStamps(stamps), stamps, nil
}

func sourceStampFromInfo(info os.FileInfo) SourceStamp {
	return SourceStamp{
		Size:        info.Size(),
		ModTimeNano: info.ModTime().UnixNano(),
		Perm:        uint32(info.Mode().Perm()),
	}
}

func sourceFilesFromStamps(stamps map[string]SourceStamp) []string {
	files := make([]string, 0, len(stamps))
	for rel := range stamps {
		files = append(files, filepath.ToSlash(rel))
	}
	sort.Strings(files)
	return files
}

// sourceStampsFingerprint hashes the stamps recorded while syncing, not a
// fresh stat pass over the app root. The distinction matters: a fresh stat
// pass can pick up changes made after the sync read its data, which would
// bless a workspace that does not actually contain them.
func sourceStampsFingerprint(stamps map[string]SourceStamp) string {
	h := sha256.New()
	for _, rel := range sourceFilesFromStamps(stamps) {
		stamp := stamps[rel]
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(fmt.Appendf(nil, "%d:%d:%o", stamp.Size, stamp.ModTimeNano, stamp.Perm))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func listSourceFiles(appRoot string) ([]string, error) {
	files := make(map[string]struct{})
	err := filepath.WalkDir(appRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(appRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && (shouldSkipDir(rel) || shouldSkipRuntimeArtifactDir(rel)) {
			return filepath.SkipDir
		}
		if d.IsDir() || !isGoWorkspaceSourceFile(rel) || shouldSkipFile(rel) || shouldSkipSymlink(path, d) || shouldSkipNonRegularFile(path, d) {
			return nil
		}
		rel = filepath.ToSlash(rel)
		files[rel] = struct{}{}
		if filepath.Ext(rel) == ".go" {
			if err := addAppEmbeddedFiles(appRoot, rel, files); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sortedKeys(files), nil
}

func isGoWorkspaceSourceFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	switch base {
	case "go.mod", "go.sum", "go.work", "go.work.sum":
		return true
	}
	switch filepath.Ext(rel) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".f", ".F", ".for", ".f90", ".m", ".mm", ".s", ".S", ".syso", ".swig", ".swigcxx":
		return true
	default:
		return false
	}
}

func addAppEmbeddedFiles(appRoot, goRel string, files map[string]struct{}) error {
	data, err := os.ReadFile(filepath.Join(appRoot, filepath.FromSlash(goRel)))
	if err != nil {
		return err
	}
	patterns := parseGeneratorGoEmbedPatterns(string(data))
	if len(patterns) == 0 {
		return nil
	}
	pkgDir := filepath.Dir(goRel)
	for _, pattern := range patterns {
		if err := addGeneratorEmbeddedPatternFiles(appRoot, pkgDir, pattern, files); err != nil {
			return err
		}
	}
	return nil
}

func currentAppSourceFingerprint(appRoot string) (string, error) {
	files, err := listSourceFiles(appRoot)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	configPath := filepath.Join(appRoot, ".scenery.json")
	if data, err := os.ReadFile(configPath); err == nil {
		_, _ = h.Write([]byte(".scenery.json"))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, rel := range files {
		data, err := sourceFileData(filepath.Join(appRoot, filepath.FromSlash(rel)), rel)
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var generatorFingerprint struct {
	once  sync.Once
	value string
	err   error
}

func currentGeneratorFingerprint() (string, error) {
	generatorFingerprint.once.Do(func() {
		generatorFingerprint.value, generatorFingerprint.err = cachedGeneratorFingerprint(app.RepoRoot())
	})
	return generatorFingerprint.value, generatorFingerprint.err
}

const generatorFingerprintCacheSchema = "scenery.generator-fingerprint.v1"

type generatorFingerprintCache struct {
	SchemaVersion       string `json:"schema_version"`
	RepoRoot            string `json:"repo_root"`
	MetadataFingerprint string `json:"metadata_fingerprint"`
	Fingerprint         string `json:"fingerprint"`
}

func cachedGeneratorFingerprint(repoRoot string) (string, error) {
	metadataFingerprint, err := generatorMetadataFingerprint(repoRoot)
	if err != nil {
		return "", err
	}
	cachePath, err := generatorFingerprintCachePath(repoRoot)
	if err != nil {
		return "", err
	}
	if cached, ok, err := loadGeneratorFingerprintCache(cachePath); err != nil {
		return "", err
	} else if ok &&
		cached.SchemaVersion == generatorFingerprintCacheSchema &&
		cached.RepoRoot == repoRoot &&
		cached.MetadataFingerprint == metadataFingerprint &&
		cached.Fingerprint != "" {
		return cached.Fingerprint, nil
	}
	fingerprint, err := computeGeneratorFingerprint(repoRoot)
	if err != nil {
		return "", err
	}
	if err := saveGeneratorFingerprintCache(cachePath, generatorFingerprintCache{
		SchemaVersion:       generatorFingerprintCacheSchema,
		RepoRoot:            repoRoot,
		MetadataFingerprint: metadataFingerprint,
		Fingerprint:         fingerprint,
	}); err != nil {
		return "", err
	}
	return fingerprint, nil
}

func generatorMetadataFingerprint(repoRoot string) (string, error) {
	h := sha256.New()
	paths := generatorFingerprintPaths()
	for _, rel := range paths {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := hashGeneratorMetadataPath(h, repoRoot, path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func computeGeneratorFingerprint(repoRoot string) (string, error) {
	h := sha256.New()
	for _, rel := range generatorFingerprintPaths() {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := hashGeneratorPath(h, repoRoot, path); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func generatorFingerprintPaths() []string {
	return []string{
		"go.mod",
		"go.sum",
		".",
		"auth",
		"cron",
		"errs",
		"internal/app",
		"internal/build",
		"internal/codegen",
		"internal/devreport",
		"internal/envfile",
		"internal/inspect",
		"internal/localproxy",
		"internal/model",
		"internal/parse",
		"internal/redact",
		"internal/runtimeapi",
		"internal/standardauthmeta",
		"internal/stdlog",
		"internal/termstyle",
		"internal/wire",
		"internal/wiremodel",
		"middleware",
		"pgxpool",
		"rlog",
		"runtime",
		"runtimeapp",
		"temporal",
	}
}

func generatorFingerprintCachePath(repoRoot string) (string, error) {
	cacheRoot, err := sceneryCacheRoot()
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absRoot))
	return filepath.Join(cacheRoot, "build", "generator-fingerprint-"+hex.EncodeToString(sum[:8])+".json"), nil
}

func loadGeneratorFingerprintCache(path string) (generatorFingerprintCache, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return generatorFingerprintCache{}, false, nil
		}
		return generatorFingerprintCache{}, false, err
	}
	var cached generatorFingerprintCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return generatorFingerprintCache{}, false, err
	}
	return cached, true, nil
}

func saveGeneratorFingerprintCache(path string, cached generatorFingerprintCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func hashGeneratorMetadataPath(h interface{ Write([]byte) (int, error) }, repoRoot, path string) error {
	files, err := generatorFingerprintFiles(repoRoot, path)
	if err != nil {
		return err
	}
	for _, rel := range files {
		child := filepath.Join(repoRoot, filepath.FromSlash(rel))
		info, err := os.Stat(child)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if err := hashGeneratorFileMetadata(h, repoRoot, child, info); err != nil {
			return err
		}
	}
	return nil
}

func hashGeneratorFileMetadata(h interface{ Write([]byte) (int, error) }, repoRoot, path string, info os.FileInfo) error {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return err
	}
	_, _ = h.Write([]byte(filepath.ToSlash(rel)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(fmt.Appendf(nil, "%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()))
	_, _ = h.Write([]byte{0})
	return nil
}

func hashGeneratorPath(h interface{ Write([]byte) (int, error) }, repoRoot, path string) error {
	files, err := generatorFingerprintFiles(repoRoot, path)
	if err != nil {
		return err
	}
	for _, rel := range files {
		child := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := hashGeneratorFile(h, repoRoot, child); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	return nil
}

func generatorFingerprintFiles(repoRoot, path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil, err
		}
		return []string{filepath.ToSlash(rel)}, nil
	}
	if rel, err := filepath.Rel(repoRoot, path); err != nil {
		return nil, err
	} else if rel == "." {
		return generatorRootPackageFiles(repoRoot)
	}
	files := map[string]struct{}{}
	err = filepath.WalkDir(path, func(child string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, child)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			switch filepath.Base(rel) {
			case "node_modules", "dist", "coverage":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if d.Type()&os.ModeSymlink != 0 || filepath.Ext(child) != ".go" || strings.HasSuffix(child, "_test.go") {
			return nil
		}
		repoRel, err := filepath.Rel(repoRoot, child)
		if err != nil {
			return err
		}
		repoRel = filepath.ToSlash(repoRel)
		files[repoRel] = struct{}{}
		if err := addGeneratorEmbeddedFiles(repoRoot, repoRel, files); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nil
}

func generatorRootPackageFiles(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, err
	}
	files := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files[name] = struct{}{}
		if err := addGeneratorEmbeddedFiles(repoRoot, name, files); err != nil {
			return nil, err
		}
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	return paths, nil
}

func hashGeneratorFile(h interface{ Write([]byte) (int, error) }, repoRoot, path string) error {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, _ = h.Write([]byte(filepath.ToSlash(rel)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(data)
	_, _ = h.Write([]byte{0})
	return nil
}

func addGeneratorEmbeddedFiles(repoRoot, goRel string, files map[string]struct{}) error {
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(goRel)))
	if err != nil {
		return err
	}
	patterns := parseGeneratorGoEmbedPatterns(string(data))
	if len(patterns) == 0 {
		return nil
	}
	pkgDir := filepath.Dir(goRel)
	for _, pattern := range patterns {
		if err := addGeneratorEmbeddedPatternFiles(repoRoot, pkgDir, pattern, files); err != nil {
			return err
		}
	}
	return nil
}

func parseGeneratorGoEmbedPatterns(src string) []string {
	var patterns []string
	for line := range strings.SplitSeq(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:embed") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "//go:embed"))
		for rest != "" {
			token, next, ok := nextGeneratorEmbedToken(rest)
			if !ok {
				break
			}
			if token != "" {
				patterns = append(patterns, token)
			}
			rest = next
		}
	}
	return patterns
}

func nextGeneratorEmbedToken(input string) (string, string, bool) {
	input = strings.TrimLeftFunc(input, unicode.IsSpace)
	if input == "" {
		return "", "", false
	}
	if quote, _ := utf8.DecodeRuneInString(input); quote == '"' || quote == '`' {
		for i := 1; i <= len(input); i++ {
			token, err := strconv.Unquote(input[:i])
			if err == nil {
				return token, input[i:], true
			}
		}
		return "", "", false
	}
	i := 0
	for i < len(input) {
		r, size := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(r) {
			break
		}
		i += size
	}
	return input[:i], input[i:], true
}

func addGeneratorEmbeddedPatternFiles(repoRoot, pkgDir, pattern string, files map[string]struct{}) error {
	includeHidden := false
	if strings.HasPrefix(pattern, "all:") {
		includeHidden = true
		pattern = strings.TrimPrefix(pattern, "all:")
	}
	if pattern == "" || filepath.IsAbs(pattern) || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
		return nil
	}
	search := filepath.Join(repoRoot, filepath.FromSlash(pkgDir), filepath.FromSlash(pattern))
	matches, err := filepath.Glob(search)
	if err != nil {
		return nil
	}
	for _, match := range matches {
		if err := addGeneratorEmbeddedPath(repoRoot, match, includeHidden, files); err != nil {
			return err
		}
	}
	return nil
}

func addGeneratorEmbeddedPath(repoRoot, path string, includeHidden bool, files map[string]struct{}) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		if includeHidden || !hasGeneratorHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	}
	return filepath.WalkDir(path, func(child string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, child)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if !includeHidden && hasGeneratorHiddenOrUnderscorePart(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if includeHidden || !hasGeneratorHiddenOrUnderscorePart(rel) {
			files[filepath.ToSlash(rel)] = struct{}{}
		}
		return nil
	})
}

func hasGeneratorHiddenOrUnderscorePart(rel string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}

func shouldSkipDir(rel string) bool {
	base := filepath.Base(rel)
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "scenery_internal_main", "__MACOSX", "coverage":
		return true
	default:
		return false
	}
}

func shouldSkipRuntimeArtifactDir(rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	switch {
	case rel == "var/browser", strings.HasPrefix(rel, "var/browser/"):
		return true
	case rel == "var/chrome", strings.HasPrefix(rel, "var/chrome/"):
		return true
	case rel == "var/playwright", strings.HasPrefix(rel, "var/playwright/"):
		return true
	default:
		return false
	}
}

func shouldSkipFile(rel string) bool {
	base := filepath.Base(rel)
	if base == ".DS_Store" {
		return true
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	return false
}

func shouldSkipNonRegularFile(path string, d os.DirEntry) bool {
	if d == nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
		return false
	}
	info, err := d.Info()
	if err != nil {
		return true
	}
	return !info.Mode().IsRegular()
}

func shouldSkipSymlink(path string, d os.DirEntry) bool {
	if d.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && info.IsDir()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if filepath.Ext(src) == ".go" {
		data, err = rewriteSceneryImports(src, data)
		if err != nil {
			return err
		}
	}
	return os.WriteFile(dst, data, 0o644)
}

func sourceFileData(path, rel string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	switch rel {
	case "go.mod":
		return patchGoModData(data, app.RepoRoot())
	}
	if filepath.Ext(rel) == ".go" {
		return rewriteSceneryImports(path, data)
	}
	return data, nil
}

func writeFileIfChanged(root, rel string, data []byte) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err == nil && string(current) == string(data) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func patchGoModData(data []byte, repoRoot string) ([]byte, error) {
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	if err := file.AddRequire("scenery.sh", "v0.0.0"); err != nil && !strings.Contains(err.Error(), "already exists") {
		return nil, err
	}
	_ = file.DropReplace("scenery.sh", "")
	if err := file.AddReplace("scenery.sh", "", repoRoot, ""); err != nil {
		return nil, err
	}
	formatted, err := file.Format()
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

func seedSceneryGoSum(workspaceDir, repoRoot string) error {
	repoSum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	workspaceSumPath := filepath.Join(workspaceDir, "go.sum")
	workspaceSum, err := os.ReadFile(workspaceSumPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := map[string]struct{}{}
	for _, data := range [][]byte{workspaceSum, repoSum} {
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines[line] = struct{}{}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	merged := make([]string, 0, len(lines))
	for line := range lines {
		merged = append(merged, line)
	}
	sort.Strings(merged)
	return writeFileIfChanged(workspaceDir, "go.sum", []byte(strings.Join(merged, "\n")+"\n"))
}

var runGo = runRealGo

func SetGoRunnerForTesting(runner func(context.Context, string, ...string) error) func() {
	old := runGo
	runGo = runner
	return func() {
		runGo = old
	}
}

func runRealGo(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go %s failed: %w\n%s", strings.Join(args, " "), err, output)
	}
	return nil
}

func runGoContext(ctx context.Context, dir string, args ...string) error {
	return runGo(ctx, dir, args...)
}

func normalizeGoBuildFlags(flags []string) []string {
	normalized := make([]string, 0, len(flags))
	for _, flag := range flags {
		flag = strings.TrimSpace(flag)
		if flag == "" {
			continue
		}
		normalized = append(normalized, flag)
	}
	return normalized
}

func goBuildArgs(binary string, flags []string) []string {
	args := make([]string, 0, 5+len(flags))
	args = append(args, "build")
	args = append(args, normalizeGoBuildFlags(flags)...)
	args = append(args, "-buildvcs=false", "-o", binary, "./scenery_internal_main")
	return args
}

func workspaceDir(appRoot, appName string) (string, error) {
	cacheRoot, err := sceneryCacheRoot()
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(appRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absRoot))
	name := sanitizeWorkspaceLabel(appName)
	if name == "" {
		name = "app"
	}
	return filepath.Join(cacheRoot, "build", name+"-"+hex.EncodeToString(sum[:8])), nil
}

func workspaceBinaryName(appRoot, buildFingerprint string) string {
	if buildFingerprint != "" {
		const prefixLength = 16
		if len(buildFingerprint) < prefixLength {
			return "scenery-app-" + buildFingerprint
		}
		return "scenery-app-" + buildFingerprint[:prefixLength]
	}
	absRoot, err := filepath.Abs(appRoot)
	if err != nil {
		absRoot = appRoot
	}
	sum := sha256.Sum256([]byte(absRoot))
	return "scenery-app-" + hex.EncodeToString(sum[:8])
}

func sceneryCacheRoot() (string, error) {
	if root := strings.TrimSpace(envpolicy.Get("SCENERY_DEV_CACHE_DIR")); root != "" {
		return root, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "scenery"), nil
}

func CacheRoot() (string, error) {
	return sceneryCacheRoot()
}

func WorkspaceDir(appRoot, appName string) (string, error) {
	return workspaceDir(appRoot, appName)
}

func BuildStatePath(appRoot, appName string) (string, error) {
	root, err := workspaceDir(appRoot, appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, buildStateFile), nil
}

func LatestBuildPath(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "build", "latest.json")
}

type StateInfo struct {
	Path                      string
	Exists                    bool
	Version                   string
	DependencyFingerprint     string
	SourceFingerprint         string
	SourceMetadataFingerprint string
	GeneratorFingerprint      string
	GraphFingerprint          string
	MetadataPresent           bool
	APIEncodingPresent        bool
	SourceFiles               []string
	GeneratedFiles            []string
	GoBuildFlags              []string
}

func ReadStateInfo(appRoot, appName string) (*StateInfo, error) {
	statePath, err := BuildStatePath(appRoot, appName)
	if err != nil {
		return nil, err
	}
	info := &StateInfo{Path: statePath}
	if _, err := os.Stat(statePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return info, nil
		}
		return nil, err
	}
	state, err := loadBuildState(filepath.Dir(statePath))
	if err != nil {
		return nil, err
	}
	info.Exists = true
	info.Version = state.Version
	info.DependencyFingerprint = state.DependencyFingerprint
	info.SourceFingerprint = state.SourceFingerprint
	info.SourceMetadataFingerprint = state.SourceMetadataFingerprint
	info.GeneratorFingerprint = state.GeneratorFingerprint
	info.GraphFingerprint = state.GraphFingerprint
	info.MetadataPresent = len(state.Metadata) > 0
	info.APIEncodingPresent = len(state.APIEncoding) > 0
	info.SourceFiles = sourceFilesFromStamps(state.SourceStamps)
	info.GeneratedFiles = append([]string(nil), state.GeneratedFiles...)
	info.GoBuildFlags = append([]string(nil), state.GoBuildFlags...)
	return info, nil
}

func ReadLatestBuildManifest(appRoot string) (*LatestBuildManifest, bool, error) {
	path := LatestBuildPath(appRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var manifest LatestBuildManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, false, err
	}
	return &manifest, true, nil
}

func WriteLatestBuildManifest(result *Result, phase string) error {
	if result == nil {
		return fmt.Errorf("nil build result")
	}
	if result.AppRoot == "" {
		return fmt.Errorf("missing app root for latest build manifest")
	}
	state, err := ReadStateInfo(result.AppRoot, result.AppName)
	if err != nil {
		return err
	}
	manifest := LatestBuildManifest{
		SchemaVersion: "scenery.build.latest.v1",
		App: LatestBuildManifestApp{
			Name:       result.AppName,
			ID:         result.AppID,
			Root:       result.AppRoot,
			ConfigPath: filepath.Join(result.AppRoot, ".scenery.json"),
		},
		Build: LatestBuildManifestRecord{
			Phase:                 phase,
			WorkspaceDir:          result.Dir,
			BinaryPath:            result.Binary,
			WorkspaceExists:       pathExists(result.Dir),
			BinaryExists:          pathExists(result.Binary),
			BuildStatePath:        state.Path,
			BuildStateExists:      state.Exists,
			BuildStateVersion:     state.Version,
			DependencyFingerprint: state.DependencyFingerprint,
			GraphFingerprint:      state.GraphFingerprint,
			MetadataPresent:       state.MetadataPresent,
			APIEncodingPresent:    state.APIEncodingPresent,
			SourceFileCount:       len(state.SourceFiles),
			GeneratedFileCount:    len(state.GeneratedFiles),
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileIfChanged(filepath.Dir(LatestBuildPath(result.AppRoot)), filepath.Base(LatestBuildPath(result.AppRoot)), data)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sanitizeWorkspaceLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func removeUnexpectedFilesFromLists(root string, sourceFiles, generatedFiles []string) error {
	keepFiles := make(map[string]struct{}, len(sourceFiles)+len(generatedFiles)+2)
	keepDirs := map[string]struct{}{
		".": {},
	}
	for _, rel := range append(append([]string(nil), sourceFiles...), generatedFiles...) {
		rel = filepath.ToSlash(rel)
		keepFiles[rel] = struct{}{}
		dir := filepath.Dir(rel)
		for dir != "." && dir != "/" {
			keepDirs[filepath.ToSlash(dir)] = struct{}{}
			dir = filepath.Dir(dir)
		}
	}
	keepFiles["scenery-app"] = struct{}{}
	keepFiles[".scenery-workspace.lock"] = struct{}{}
	keepFiles[buildStateFile] = struct{}{}
	keepFiles["go.sum"] = struct{}{}

	var files []string
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		if _, ok := keepFiles[rel]; ok || strings.HasPrefix(rel, "scenery-app-") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, path := range dirs {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := keepDirs[filepath.ToSlash(rel)]; ok {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, fs.ErrExist) {
			if pathErr, ok := errors.AsType[*fs.PathError](err); ok && errors.Is(pathErr.Err, fs.ErrExist) {
				continue
			}
			if strings.Contains(err.Error(), "directory not empty") {
				continue
			}
			return err
		}
	}
	return nil
}
