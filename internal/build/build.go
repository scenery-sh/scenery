package build

import (
	"encoding/json"
	"fmt"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
	"scenery.sh/internal/machine"
)

type Result struct {
	AppRoot                   string
	AppName                   string
	AppID                     string
	Dir                       string
	Binary                    string
	ArtifactDigest            string
	ExecutableBytes           int64
	NeedsTidy                 bool
	DependencyFingerprint     string
	SourceFingerprint         string
	SourceMetadataFingerprint string
	FrameworkFingerprint      string
	FrameworkSourceRoot       string
	FrameworkSourceDigest     string
	GeneratorFingerprint      string
	PreparationFingerprint    string
	BuildFingerprint          string
	GraphFingerprint          string
	Metadata                  json.RawMessage
	APIEncoding               json.RawMessage
	SourceFiles               []string
	SourceStamps              map[string]SourceStamp
	GeneratedFiles            []string
	GeneratedStamps           map[string]string
	PublicGeneratedStamps     map[string]string
	CachedTypeScriptStamps    map[string]string
	ManagedGeneratedPaths     []string
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
	AssistantAssets         []generateapi.AssistantAssetDescriptor
	ProductionAssets        bool
	OwnedGoModuleSources    []OwnedGoModuleSource
	// DevelopmentProcessBinaries names, relative to Dir, the executables of the
	// process-model generation this build produced instead of one application
	// executable.
	DevelopmentProcessBinaries []string
	verification               *preparedVerification
	// workspaceHold is the workspace lock a held preparation kept after it
	// established the workspace's membership and bytes; the next process build
	// consumes it instead of locking and verifying them again.
	workspaceHold func()
}

// ReleaseWorkspace releases a workspace lock that a held preparation kept and
// no compilation consumed. It is safe to call more than once.
func (r *Result) ReleaseWorkspace() {
	if r == nil || r.workspaceHold == nil {
		return
	}
	release := r.workspaceHold
	r.workspaceHold = nil
	release()
}

func (r *Result) takeWorkspaceHold() func() {
	if r == nil {
		return nil
	}
	hold := r.workspaceHold
	r.workspaceHold = nil
	return hold
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
	Hash        string `json:"sha256,omitempty"`
}

type SourceSnapshot struct {
	Files         map[string]SourceSnapshotFile
	CompilerFiles map[string]SourceSnapshotFile
	// CompilerAbsent records relevant missing resolver alternatives and
	// optional revision inputs. Their later appearance changes membership even
	// though there were no bytes to capture in this snapshot.
	CompilerAbsent map[string]bool
	// ContractFiles and ContractCompilerFiles are the non-implementation
	// inputs captured with Contract. They let the ordinary handler-edit path
	// prove graph equivalence without asking the compiler to reread appRoot.
	ContractFiles          map[string]SourceSnapshotFile
	ContractCompilerFiles  map[string]SourceSnapshotFile
	ContractCompilerAbsent map[string]bool
	CompilerCaptureValid   bool
	// Contract is an optional already-compiled startup snapshot. Consumers
	// verify captured graph inputs before reusing its pure graph.
	Contract *compiler.Result
	// Generated is the set of managed generated paths the capture excluded
	// from Files (compiler.GeneratedPaths at capture time), or nil when the
	// capture did not record it and consumers discover it from disk.
	Generated map[string]bool
}

// generatedPaths returns the managed generated paths of appRoot for a
// preparation that consumes snapshot: the captured set, or the set discovered
// from disk when the snapshot has none.
func (snapshot *SourceSnapshot) generatedPaths(appRoot string) (map[string]bool, error) {
	if snapshot != nil && snapshot.Generated != nil {
		return snapshot.Generated, nil
	}
	return compiler.GeneratedPaths(appRoot)
}

type SourceSnapshotFile struct {
	Size           int64
	ModTimeNano    int64
	Perm           uint32
	Hash           string
	Embedded       bool
	Implementation bool
	Data           []byte
}

type buildState struct {
	Version                   string                 `json:"version,omitempty"`
	DependencyFingerprint     string                 `json:"dependency_fingerprint"`
	SourceFingerprint         string                 `json:"source_fingerprint,omitempty"`
	SourceMetadataFingerprint string                 `json:"source_metadata_fingerprint,omitempty"`
	FrameworkFingerprint      string                 `json:"framework_fingerprint,omitempty"`
	GeneratorFingerprint      string                 `json:"generator_fingerprint,omitempty"`
	PreparationFingerprint    string                 `json:"preparation_fingerprint,omitempty"`
	BuildFingerprint          string                 `json:"build_fingerprint,omitempty"`
	GraphFingerprint          string                 `json:"graph_fingerprint,omitempty"`
	Metadata                  []byte                 `json:"metadata,omitempty"`
	APIEncoding               []byte                 `json:"api_encoding,omitempty"`
	SourceStamps              map[string]SourceStamp `json:"source_file_stamps,omitempty"`
	GeneratedFiles            []string               `json:"generated_files,omitempty"`
	GeneratedStamps           map[string]string      `json:"generated_file_sha256,omitempty"`
	PublicGeneratedStamps     map[string]string      `json:"public_generated_sha256,omitempty"`
	CachedTypeScriptStamps    map[string]string      `json:"cached_typescript_sha256,omitempty"`
	ManagedGeneratedPaths     []string               `json:"managed_generated_paths,omitempty"`
	GoBuildFlags              []string               `json:"go_build_flags,omitempty"`
	OwnedGoModuleSources      []OwnedGoModuleSource  `json:"owned_go_module_sources,omitempty"`
	// DevelopmentProcessBinaries are the executables of a process-model build.
	DevelopmentProcessBinaries []string `json:"development_process_binaries,omitempty"`
}

const (
	buildStateFile    = ".scenery-build-state.json"
	buildStateVersion = "10"
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

// AppForTarget prepares and compiles one host-executable app binary from the
// named go_target, or from the target with role "development" when targetName
// is empty. Runtime process roles such as SCENERY_ROLE=worker select behavior
// inside that binary; they are not go_target roles.
func AppForTarget(appRoot string, cfg app.Config, targetName string) (*Result, error) {
	return appForTarget(appRoot, cfg, targetName, "development", false)
}

// BuildArtifactForTarget prepares and compiles one production app binary from
// the named go_target, or from the target with role "artifact" when targetName
// is empty. Unlike AppForTarget, it materializes the platform-matched
// assistant runtime assets before linking the Go binary.
func BuildArtifactForTarget(appRoot string, cfg app.Config, targetName string) (*Result, error) {
	return appForTarget(appRoot, cfg, targetName, "artifact", true)
}

func appForTarget(appRoot string, cfg app.Config, targetName, defaultRole string, productionAssets bool) (*Result, error) {
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
		return nil, fmt.Errorf("go contract target %s does not produce a runtime binary", target.Name)
	}
	result, err := prepareWithContractTarget(appRoot, cfg, nil, contract, target)
	if err != nil {
		return nil, err
	}
	result.ProductionAssets = productionAssets
	if err := Compile(result); err != nil {
		return nil, err
	}
	return result, nil
}
