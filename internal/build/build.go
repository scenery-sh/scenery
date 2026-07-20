package build

import (
	"encoding/json"
	"fmt"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
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
	FrameworkFingerprint      string
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
	// RuntimeLinkerMetadata holds the -X linker values injected at go build
	// time. It stays out of GoBuildFlags so persisted build state keeps only
	// the configured flags and warm-start cache comparison remains stable.
	RuntimeLinkerMetadata   map[string]string
	GoEnvironment           []string
	Contract                *compiler.Result
	Target                  *compiler.GoBuildTarget
	BuildInput              *BuildInputManifest
	ImplementationRevisions map[string]string
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
	FrameworkFingerprint      string                 `json:"framework_fingerprint,omitempty"`
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
	buildStateVersion = "5"
)

type CachedGraph struct {
	Result      *Result
	Metadata    json.RawMessage
	APIEncoding json.RawMessage
}

type LatestBuildManifest struct {
	machine.ArtifactIdentity
	App   LatestBuildManifestApp    `json:"app"`
	Build LatestBuildManifestRecord `json:"build"`
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
	FrameworkFingerprint  string `json:"framework_fingerprint,omitempty"`
	GraphFingerprint      string `json:"graph_fingerprint,omitempty"`
	MetadataPresent       bool   `json:"metadata_present"`
	APIEncodingPresent    bool   `json:"api_encoding_present"`
	SourceFileCount       int    `json:"source_file_count"`
	GeneratedFileCount    int    `json:"generated_file_count"`
}

func AppForTarget(appRoot string, cfg app.Config, targetName, defaultRole string) (*Result, error) {
	contract, err := compiler.Check(appRoot)
	if err != nil {
		return nil, err
	}
	if !contract.Valid() {
		return nil, fmt.Errorf("contract or generated artifacts are invalid")
	}
	target, err := compiler.ResolveGoBuildTarget(contract, targetName, defaultRole)
	if err != nil {
		return nil, err
	}
	if target.Role == "contract" {
		return nil, fmt.Errorf("Go contract target %s does not produce a runtime binary", target.Name)
	}
	result, err := prepareWithContractTarget(appRoot, nil, cfg, nil, contract, target)
	if err != nil {
		return nil, err
	}
	if err := Compile(result); err != nil {
		return nil, err
	}
	return result, nil
}
