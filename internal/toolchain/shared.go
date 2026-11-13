package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/filemeta"
	"scenery.sh/internal/machine"
)

// sharedArtifactRoot holds installed archive artifacts that every store of
// this user may clone instead of downloading and extracting them again. Empty
// disables sharing; only the command wires it.
var sharedArtifactRoot string

// SetSharedArtifactStore selects the directory whose installed artifacts
// stores clone from and publish to. Sharing needs a cloning filesystem: a
// store that cannot clone installs from its declared source as before.
func SetSharedArtifactStore(root string) {
	sharedArtifactRoot = strings.TrimSpace(root)
}

// sharedArtifactDir names one installed artifact by everything that decides
// its content: the artifact, its platform and the whole declared platform
// entry, which carries the archive checksum and the extraction settings.
func sharedArtifactDir(artifact Artifact, entry PlatformArtifact, platform Platform) string {
	if sharedArtifactRoot == "" || len(entry.Build) > 0 {
		// A built artifact also depends on the host compiler, which no key names.
		return ""
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return filepath.Join(sharedArtifactRoot, artifact.Name, artifact.Version, platform.DirName(), hex.EncodeToString(sum[:]))
}

// adoptSharedArtifact installs the artifact as a clone of its shared copy and
// reports whether it did. The clone shares its blocks with the shared copy, so
// another worktree's toolchain costs neither a download nor disk space.
func (s *Store) adoptSharedArtifact(artifact Artifact, entry PlatformArtifact, platform Platform) bool {
	shared := sharedArtifactDir(artifact, entry, platform)
	if shared == "" {
		return false
	}
	if meta, err := readInstallMetadata(shared); err != nil || !meta.matches(artifact, entry, platform) {
		return false
	}
	finalDir := s.artifactPlatformDir(artifact, platform)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return false
	}
	stage := filepath.Join(filepath.Dir(finalDir), ".tmp-"+artifact.Name+"-"+time.Now().UTC().Format("20060102150405.000000000"))
	_ = os.RemoveAll(stage)
	defer func() { _ = os.RemoveAll(stage) }()
	if filemeta.Clone(shared, stage) != nil {
		return false
	}
	if err := s.writeInstallMetadata(stage, artifact, entry, platform); err != nil {
		return false
	}
	_ = os.RemoveAll(finalDir)
	if os.Rename(stage, finalDir) != nil {
		return false
	}
	return isExecutableFile(s.managedBinaryPath(artifact, entry, platform)) && s.verifyInstall(artifact, entry, platform) == nil
}

// shareArtifact publishes a freshly installed artifact for other stores. A
// failure only costs the next store its own installation.
func (s *Store) shareArtifact(artifact Artifact, entry PlatformArtifact, platform Platform) {
	shared := sharedArtifactDir(artifact, entry, platform)
	if shared == "" {
		return
	}
	if _, err := os.Lstat(shared); err == nil {
		return
	}
	if os.MkdirAll(filepath.Dir(shared), 0o700) != nil {
		return
	}
	stage := filepath.Join(filepath.Dir(shared), ".tmp-"+time.Now().UTC().Format("20060102150405.000000000"))
	_ = os.RemoveAll(stage)
	defer func() { _ = os.RemoveAll(stage) }()
	if filemeta.Clone(s.artifactPlatformDir(artifact, platform), stage) != nil {
		return
	}
	_ = os.Rename(stage, shared)
}

func readInstallMetadata(dir string) (InstallMetadata, error) {
	var meta InstallMetadata
	data, err := os.ReadFile(filepath.Join(dir, "install.json"))
	if err != nil {
		return meta, err
	}
	err = machine.DecodeArtifact(data, &meta, &meta.ArtifactIdentity, installMetadataKind, installMetadataSchemaDescriptor, "reinstall the toolchain artifact")
	return meta, err
}

func (m InstallMetadata) matches(artifact Artifact, entry PlatformArtifact, platform Platform) bool {
	return m.Name == artifact.Name && m.Version == artifact.Version && m.Platform == platform.String() && strings.EqualFold(m.SourceSHA256, entry.SHA256)
}

func (s *Store) writeInstallMetadata(dir string, artifact Artifact, entry PlatformArtifact, platform Platform) error {
	meta := InstallMetadata{
		ArtifactIdentity: machine.NewArtifactIdentity(installMetadataKind, installMetadataSchemaDescriptor),
		Name:             artifact.Name,
		Version:          artifact.Version,
		Platform:         platform.String(),
		ManifestSHA256:   s.manifestSHA256(),
		SourceURL:        entry.URL,
		SourceSHA256:     strings.ToLower(entry.SHA256),
		InstalledAt:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "install.json")
	// The cloned record shares its blocks with the shared copy; replace it.
	_ = os.Remove(path)
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
