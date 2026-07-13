package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/machine"
)

const (
	latestBuildKind             = "scenery.build.latest"
	latestBuildSchemaDescriptor = machine.ExactSchemaRevision("sha256:9cf0d64062da541ff614904f6499eef61a602ff64bd501aa92cd36ec99ee7e79")
)

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
	FrameworkFingerprint      string
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
	info.FrameworkFingerprint = state.FrameworkFingerprint
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
	if err := machine.DecodeArtifact(data, &manifest, &manifest.ArtifactIdentity, latestBuildKind, latestBuildSchemaDescriptor, "rebuild the application"); err != nil {
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
	configPath, err := app.ResolveConfigPath(result.AppRoot)
	if err != nil {
		return err
	}
	manifest := LatestBuildManifest{
		ArtifactIdentity: machine.NewArtifactIdentity(latestBuildKind, latestBuildSchemaDescriptor),
		App: LatestBuildManifestApp{
			Name:       result.AppName,
			ID:         result.AppID,
			Root:       result.AppRoot,
			ConfigPath: configPath,
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
			FrameworkFingerprint:  state.FrameworkFingerprint,
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
