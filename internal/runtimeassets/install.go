package runtimeassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/atomicfile"
)

// InstallResult describes the verified content-addressed directory returned by
// Install.  Reused is true when an existing directory passed every descriptor
// and tree check and no extraction was needed.
type InstallResult struct {
	Path       string
	Digest     string
	Descriptor Descriptor
	Reused     bool
}

// ErrExistingInstallTampered is returned when the content-addressed final
// directory already exists but its manifest or tree no longer matches the
// requested artifact.  The directory is intentionally never removed.
var ErrExistingInstallTampered = errors.New("existing runtime asset install is tampered")

type installManifest struct {
	SchemaRevision string     `json:"schema_revision"`
	ArchiveDigest  string     `json:"archive_digest"`
	Descriptor     Descriptor `json:"descriptor"`
}

// Install writes archive to stateRoot/runtime-assets/<tree-digest> using a
// per-digest interprocess lock, an isolated staging directory, complete tree
// verification, and an atomic rename.  Existing verified content is reused;
// existing invalid content is rejected and never replaced or deleted.
func Install(stateRoot string, archive Archive) (InstallResult, error) {
	return InstallContext(context.Background(), stateRoot, archive)
}

// InstallContext is Install with cancellation while waiting for the digest
// lock.  Extraction itself is bounded by the archive bytes supplied by the
// caller and runs only in an unreferenced staging directory.
func InstallContext(ctx context.Context, stateRoot string, archive Archive) (InstallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := archive.Validate(); err != nil {
		return InstallResult{}, err
	}
	if err := validateStateRoot(stateRoot); err != nil {
		return InstallResult{}, err
	}
	base := filepath.Join(stateRoot, AssetDirectory)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("create runtime asset state: %w", err)
	}
	digestHex := strings.TrimPrefix(archive.Descriptor.Digest, "sha256:")
	if len(digestHex) != 64 {
		return InstallResult{}, fmt.Errorf("invalid tree digest %q", archive.Descriptor.Digest)
	}
	finalPath := filepath.Join(base, digestHex)
	lock, err := acquireAssetLock(ctx, filepath.Join(base, "."+digestHex+".lock"))
	if err != nil {
		return InstallResult{}, err
	}
	defer lock()
	if err := cleanupAbandonedStages(base, digestHex); err != nil {
		return InstallResult{}, err
	}
	if info, statErr := os.Lstat(finalPath); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return InstallResult{}, fmt.Errorf("%w: final path is not a directory", ErrExistingInstallTampered)
		}
		if err := verifyInstalled(finalPath, archive); err != nil {
			return InstallResult{}, fmt.Errorf("%w: %v", ErrExistingInstallTampered, err)
		}
		return InstallResult{Path: finalPath, Digest: archive.Descriptor.Digest, Descriptor: cloneDescriptor(archive.Descriptor), Reused: true}, nil
	} else if !os.IsNotExist(statErr) {
		return InstallResult{}, statErr
	}

	stage, err := os.MkdirTemp(base, ".stage-"+digestHex+"-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("create runtime asset stage: %w", err)
	}
	stageOwned := true
	defer func() {
		if stageOwned {
			_ = os.RemoveAll(stage)
		}
	}()
	if _, err := ExtractArchive(archive.Data, stage); err != nil {
		return InstallResult{}, fmt.Errorf("extract runtime asset: %w", err)
	}
	manifest := installManifest{SchemaRevision: SchemaRevision, ArchiveDigest: archive.ArchiveDigest, Descriptor: cloneDescriptor(archive.Descriptor)}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return InstallResult{}, fmt.Errorf("marshal runtime asset manifest: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(stage, DescriptorFilename), manifestBytes, 0o644, atomicfile.Options{SyncFile: true}); err != nil {
		return InstallResult{}, fmt.Errorf("write runtime asset manifest: %w", err)
	}
	if err := verifyInstalled(stage, archive); err != nil {
		return InstallResult{}, fmt.Errorf("verify staged runtime asset: %w", err)
	}
	if err := syncTreeDirectories(stage); err != nil {
		return InstallResult{}, fmt.Errorf("sync staged runtime asset: %w", err)
	}
	if err := os.Rename(stage, finalPath); err != nil {
		return InstallResult{}, fmt.Errorf("commit runtime asset: %w", err)
	}
	stageOwned = false
	if err := syncDirectory(base); err != nil {
		return InstallResult{}, fmt.Errorf("sync runtime asset state: %w", err)
	}
	// Verify after the rename as well.  If an unexpected filesystem mutation
	// occurred, leave the final directory intact for diagnosis and fail closed.
	if err := verifyInstalled(finalPath, archive); err != nil {
		return InstallResult{}, fmt.Errorf("verify committed runtime asset: %w", err)
	}
	return InstallResult{Path: finalPath, Digest: archive.Descriptor.Digest, Descriptor: cloneDescriptor(archive.Descriptor)}, nil
}

// InstalledPath returns the deterministic final path for a tree digest.
func InstalledPath(stateRoot, digest string) (string, error) {
	if err := validateStateRoot(stateRoot); err != nil {
		return "", err
	}
	if !validDigest(digest) {
		return "", fmt.Errorf("invalid tree digest %q", digest)
	}
	return filepath.Join(stateRoot, AssetDirectory, strings.TrimPrefix(digest, "sha256:")), nil
}

func (a Archive) Validate() error {
	if err := a.Descriptor.Validate(); err != nil {
		return fmt.Errorf("descriptor: %w", err)
	}
	if len(a.Data) == 0 || !validDigest(a.ArchiveDigest) {
		return fmt.Errorf("archive digest is missing")
	}
	if got := digestBytes(a.Data); got != a.ArchiveDigest {
		return fmt.Errorf("archive digest mismatch: got %q want %q", got, a.ArchiveDigest)
	}
	return nil
}

func verifyInstalled(path string, archive Archive) error {
	data, err := os.ReadFile(filepath.Join(path, DescriptorFilename))
	if err != nil {
		return fmt.Errorf("read install manifest: %w", err)
	}
	var manifest installManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode install manifest: %w", err)
	}
	if manifest.SchemaRevision != SchemaRevision || manifest.ArchiveDigest != archive.ArchiveDigest {
		return fmt.Errorf("install manifest identity mismatch")
	}
	if err := manifest.Descriptor.Validate(); err != nil {
		return fmt.Errorf("install manifest descriptor: %w", err)
	}
	if manifest.Descriptor.Digest != archive.Descriptor.Digest {
		return fmt.Errorf("install manifest tree digest mismatch")
	}
	actual, err := describeTree(path, true)
	if err != nil {
		return fmt.Errorf("describe installed tree: %w", err)
	}
	if actual.Digest != archive.Descriptor.Digest || !sameEntries(actual, archive.Descriptor) {
		return fmt.Errorf("installed tree digest mismatch: got %q want %q", actual.Digest, archive.Descriptor.Digest)
	}
	return nil
}

func sameEntries(a, b Descriptor) bool {
	if len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Entries {
		if a.Entries[i] != b.Entries[i] {
			return false
		}
	}
	return true
}

func validateStateRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("state root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	if absolute == string(filepath.Separator) || (volume != "" && absolute == volume+string(filepath.Separator)) {
		return fmt.Errorf("state root may not be a filesystem root")
	}
	return nil
}

func cleanupAbandonedStages(base, digest string) error {
	prefix := ".stage-" + digest + "-"
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		candidate := filepath.Join(base, entry.Name())
		info, err := os.Lstat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("refusing to remove non-directory abandoned stage %q", entry.Name())
		}
		if err := os.RemoveAll(candidate); err != nil {
			return fmt.Errorf("remove abandoned stage %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func syncDirectory(name string) error {
	dir, err := os.Open(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// syncTreeDirectories persists every directory touched by extraction before
// the staging directory is renamed into its content-addressed final path.
// Children are synced before their parents so directory entries and files are
// durable bottom-up; the final root sync covers the rename-visible tree.
func syncTreeDirectories(root string) error {
	directories := make([]string, 0)
	if err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, name)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool {
		if len(directories[i]) != len(directories[j]) {
			return len(directories[i]) > len(directories[j])
		}
		return directories[i] > directories[j]
	})
	for _, directory := range directories {
		if err := syncDirectory(directory); err != nil {
			return fmt.Errorf("sync directory %q: %w", directory, err)
		}
	}
	return nil
}
