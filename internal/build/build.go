package build

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/parse"
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

type SourceSnapshot struct {
	Files map[string]SourceSnapshotFile
}

type SourceSnapshotFile struct {
	Size        int64
	ModTimeNano int64
	Perm        uint32
	Hash        string
	Embedded    bool
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
	App         string `json:"app"`
	Routes      string `json:"routes"`
	Services    string `json:"services"`
	Endpoints   string `json:"endpoints"`
	Models      string `json:"models,omitempty"`
	Views       string `json:"views,omitempty"`
	BuildLatest string `json:"build_latest"`
}

type GeneratedManifestSchema struct {
	App         string `json:"app"`
	Routes      string `json:"routes"`
	Services    string `json:"services"`
	Endpoints   string `json:"endpoints"`
	Models      string `json:"models,omitempty"`
	Views       string `json:"views,omitempty"`
	BuildLatest string `json:"build_latest"`
}

type GeneratedManifestHashes struct {
	App       string `json:"app"`
	Routes    string `json:"routes"`
	Services  string `json:"services"`
	Endpoints string `json:"endpoints"`
	Models    string `json:"models,omitempty"`
	Views     string `json:"views,omitempty"`
}

type generatedInspectArtifacts struct {
	App           inspectdata.AppResponse
	Routes        inspectdata.RoutesResponse
	Services      inspectdata.ServicesResponse
	Endpoints     inspectdata.EndpointsResponse
	Models        inspectdata.ModelsResponse
	Views         inspectdata.ViewsResponse
	AppJSON       []byte
	RoutesJSON    []byte
	ServicesJSON  []byte
	EndpointsJSON []byte
	ModelsJSON    []byte
	ViewsJSON     []byte
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
	return LoadReusableBinaryWithSnapshot(appRoot, cfg, nil)
}

func LoadReusableBinaryWithSnapshot(appRoot string, cfg app.Config, snapshot *SourceSnapshot) (*Result, bool, error) {
	goBuildFlags := normalizeGoBuildFlags(cfg.Build.GoFlags)
	sourceFingerprint, err := currentAppSourceFingerprintWithSnapshot(appRoot, snapshot)
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
	if ok, err := latestBuildManifestMatchesReusableBinary(appRoot, cfg, workspaceDir, binary, state); err != nil || !ok {
		return nil, false, err
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

func latestBuildManifestMatchesReusableBinary(appRoot string, cfg app.Config, workspaceDir, binary string, state buildState) (bool, error) {
	manifest, ok, err := ReadLatestBuildManifest(appRoot)
	if err != nil || !ok {
		return false, err
	}
	if manifest.SchemaVersion != "scenery.build.latest.v1" ||
		manifest.App.Root != appRoot ||
		manifest.App.Name != cfg.Name ||
		manifest.App.ID != cfg.ID ||
		manifest.Build.Phase != "compiled" ||
		manifest.Build.WorkspaceDir != workspaceDir ||
		manifest.Build.BinaryPath != binary ||
		!manifest.Build.WorkspaceExists ||
		!manifest.Build.BinaryExists ||
		manifest.Build.BuildStateVersion != buildStateVersion ||
		manifest.Build.DependencyFingerprint != state.DependencyFingerprint ||
		manifest.Build.GraphFingerprint != state.GraphFingerprint {
		return false, nil
	}
	return true, nil
}
