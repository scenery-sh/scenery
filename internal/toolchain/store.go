package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

const (
	installMetadataKind             = "scenery.toolchain.install"
	installMetadataSchemaDescriptor = `{"installed_at":"datetime","kind":"scenery.toolchain.install","manifest_sha256":"digest","name":"string","platform":"platform","producer":"producer","schema_revision":"digest","source_kind":"optional_string","source_sha256":"digest","source_url":"string","spec_revision":"digest","version":"external_version"}`
	statusSchemaDescriptor          = `{"artifacts":"array<artifact-status>","kind":"scenery.toolchain.status","manifest_sha256":"digest","platform":"platform","schema_revision":"digest","source_locks":"array<source-lock-status>","store_dir":"path"}`
)

var StatusSchemaRevision = string(spec.SchemaRevision(statusSchemaDescriptor))

type Store struct {
	Dir            string
	RootDir        string
	Manifest       Manifest
	ManifestSHA256 string
	Platform       Platform
	Client         *http.Client
}

type Options struct {
	RootDir  string
	Platform Platform
	Tool     string
}

type Status struct {
	Kind           string             `json:"kind"`
	SchemaRevision string             `json:"schema_revision"`
	ManifestSHA256 string             `json:"manifest_sha256"`
	StoreDir       string             `json:"store_dir"`
	Platform       string             `json:"platform"`
	SourceLocks    []SourceLockStatus `json:"source_locks"`
	Artifacts      []ArtifactStatus   `json:"artifacts"`
}

type SourceLockStatus struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Manager  string `json:"manager,omitempty"`
	Manifest string `json:"manifest"`
	Lock     string `json:"lock,omitempty"`
	Status   string `json:"status"`
}

type ArtifactStatus struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Version     string `json:"version"`
	Status      string `json:"status"`
	Source      string `json:"source,omitempty"`
	ManagedPath string `json:"managed_path,omitempty"`
	HomePath    string `json:"home_path,omitempty"`
	Message     string `json:"message,omitempty"`
}

type InstallMetadata struct {
	machine.ArtifactIdentity
	Name           string `json:"name"`
	Version        string `json:"version"`
	Platform       string `json:"platform"`
	ManifestSHA256 string `json:"manifest_sha256"`
	SourceURL      string `json:"source_url"`
	SourceSHA256   string `json:"source_sha256"`
	SourceKind     string `json:"source_kind,omitempty"`
	InstalledAt    string `json:"installed_at"`
}

func DefaultStoreDir(appRoot string) string {
	if value := strings.TrimSpace(envpolicy.Get("SCENERY_TOOLCHAIN_DIR")); value != "" {
		return value
	}
	if strings.TrimSpace(appRoot) == "" {
		appRoot = "."
	}
	return filepath.Join(appRoot, ".scenery", "toolchain")
}

func NewStore(dir string, manifest Manifest) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("toolchain store dir is empty")
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return &Store{Dir: dir, RootDir: ".", Manifest: manifest, ManifestSHA256: ManifestSHA256(data), Platform: CurrentPlatform(), Client: http.DefaultClient}, nil
}

func (s *Store) List(ctx context.Context, opts Options) (Status, error) {
	return s.status(ctx, opts, false)
}

func (s *Store) Verify(ctx context.Context, opts Options) (Status, error) {
	return s.status(ctx, opts, true)
}

func (s *Store) Sync(ctx context.Context, opts Options) (Status, error) {
	if opts.Platform.String() == "" {
		opts.Platform = s.Platform
	}
	selected, err := s.selectedArtifacts(opts.Tool)
	if err != nil {
		return Status{}, err
	}
	for _, artifact := range selected {
		if artifact.Kind != "binary" {
			continue
		}
		status := s.artifactStatus(artifact, opts.Platform, true)
		if status.Status == "installed" {
			continue
		}
		if isFalseEnv(envpolicy.Get("SCENERY_TOOLCHAIN_DOWNLOAD")) {
			return Status{}, fmt.Errorf("toolchain downloads disabled by SCENERY_TOOLCHAIN_DOWNLOAD=0")
		}
		if err := s.installArtifact(ctx, artifact, opts.Platform); err != nil {
			return Status{}, err
		}
	}
	return s.status(ctx, opts, true)
}

func (s *Store) Path(ctx context.Context, artifactName string, platform Platform) (ArtifactStatus, error) {
	if platform.String() == "" {
		platform = s.Platform
	}
	artifact, ok := s.Manifest.Artifact(artifactName)
	if !ok {
		return ArtifactStatus{}, fmt.Errorf("unknown toolchain artifact %q", artifactName)
	}
	status := s.artifactStatus(artifact, platform, true)
	if status.ManagedPath == "" {
		return status, fmt.Errorf("toolchain artifact %q has no managed path for %s", artifactName, platform)
	}
	_ = ctx
	return status, nil
}

func (s *Store) status(ctx context.Context, opts Options, verify bool) (Status, error) {
	if opts.Platform.String() == "" {
		opts.Platform = s.Platform
	}
	root := opts.RootDir
	if root == "" {
		root = s.RootDir
	}
	status := Status{
		Kind:           StatusKind,
		SchemaRevision: StatusSchemaRevision,
		ManifestSHA256: s.manifestSHA256(),
		StoreDir:       filepath.Clean(s.Dir),
		Platform:       opts.Platform.String(),
	}
	for _, lock := range s.Manifest.SourceLocks {
		status.SourceLocks = append(status.SourceLocks, sourceLockStatus(root, lock))
	}
	selected, err := s.selectedArtifacts(opts.Tool)
	if err != nil {
		return Status{}, err
	}
	for _, artifact := range selected {
		status.Artifacts = append(status.Artifacts, s.artifactStatus(artifact, opts.Platform, verify))
	}
	return status, nil
}

func sourceLockStatus(root string, lock SourceLock) SourceLockStatus {
	status := SourceLockStatus{
		Name:     lock.Name,
		Kind:     lock.Kind,
		Manager:  lock.Manager,
		Manifest: lock.Manifest,
		Lock:     lock.Lock,
		Status:   "present",
	}
	if _, err := os.Stat(filepath.Join(root, lock.Manifest)); err != nil {
		status.Status = "missing"
		return status
	}
	if lock.Lock != "" {
		if _, err := os.Stat(filepath.Join(root, lock.Lock)); err != nil {
			status.Status = "missing-lock"
		}
	}
	return status
}

func (s *Store) selectedArtifacts(tool string) ([]Artifact, error) {
	if tool == "" {
		return s.Manifest.Artifacts, nil
	}
	if artifact, ok := s.Manifest.Artifact(tool); ok {
		return []Artifact{artifact}, nil
	}
	return nil, fmt.Errorf("unknown toolchain artifact %q", tool)
}

func (s *Store) artifactStatus(artifact Artifact, platform Platform, verify bool) ArtifactStatus {
	status := ArtifactStatus{Name: artifact.Name, Kind: artifact.Kind, Version: artifact.Version, Status: "missing"}
	if artifact.Kind != "binary" {
		status.Status = "declared"
		return status
	}
	entry, ok := artifact.PlatformArtifact(platform)
	if !ok {
		status.Status = "unsupported"
		status.Message = "no platform artifact for " + platform.String()
		return status
	}
	status.ManagedPath = s.managedBinaryPath(artifact, entry, platform)
	if entry.Home {
		status.HomePath = s.homePath(artifact, platform)
	}
	if !isExecutableFile(status.ManagedPath) {
		return status
	}
	status.Status = "installed"
	status.Source = "managed-store"
	if verify {
		if err := s.verifyInstall(artifact, entry, platform); err != nil {
			status.Status = "invalid"
			status.Message = err.Error()
		}
	}
	return status
}

func (s *Store) installArtifact(ctx context.Context, artifact Artifact, platform Platform) error {
	entry, ok := artifact.PlatformArtifact(platform)
	if !ok {
		return fmt.Errorf("toolchain artifact %s has no platform artifact for %s", artifact.Name, platform)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, entry.URL, nil)
	if err != nil {
		return err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", entry.URL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 768<<20))
	if err != nil {
		return err
	}
	if err := verifySHA256(data, entry.SHA256); err != nil {
		return fmt.Errorf("verify %s: %w", artifact.Name, err)
	}
	finalDir := s.artifactPlatformDir(artifact, platform)
	tmpDir := filepath.Join(filepath.Dir(finalDir), ".tmp-"+artifact.Name+"-"+time.Now().UTC().Format("20060102150405.000000000"))
	_ = os.RemoveAll(tmpDir)
	defer func() { _ = os.RemoveAll(tmpDir) }()
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	if len(entry.Build) > 0 {
		if err := s.buildArtifact(ctx, data, artifact, entry, tmpDir); err != nil {
			return err
		}
	} else {
		if err := s.extractArtifact(data, artifact, entry, tmpDir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(finalDir)
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return err
	}
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
	data, err = json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(finalDir, "install.json"), append(data, '\n'), 0o644)
}

func (s *Store) buildArtifact(ctx context.Context, data []byte, artifact Artifact, entry PlatformArtifact, dir string) error {
	srcDir := filepath.Join(dir, "src")
	installDir := filepath.Join(dir, "build-install")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return err
	}
	if err := extractTarArchive(data, entry.StripComponents, srcDir); err != nil {
		return err
	}
	for _, command := range entry.Build {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		command = expandBuildCommand(command, srcDir, installDir)
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Dir = srcDir
		cmd.Env = envpolicy.Environ()
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("build %s: %w: %s", artifact.Name, err, strings.TrimSpace(string(output)))
		}
	}
	outputPath := filepath.Join(installDir, cleanExtract(entry.BuildOutput))
	target := filepath.Join(dir, "bin", artifact.DefaultBinary)
	if err := copyExecutableFile(outputPath, target); err != nil {
		return fmt.Errorf("install built %s from %s: %w", artifact.Name, entry.BuildOutput, err)
	}
	return nil
}

func expandBuildCommand(command, sourceDir, installDir string) string {
	command = strings.ReplaceAll(command, "{source}", shellQuote(sourceDir))
	command = strings.ReplaceAll(command, "{install}", shellQuote(installDir))
	return command
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *Store) extractArtifact(data []byte, artifact Artifact, entry PlatformArtifact, dir string) error {
	if err := extractSelectedArtifact(data, artifact, entry, dir); err != nil {
		return err
	}
	for _, binary := range append([]string{artifact.DefaultBinary}, artifact.Binaries...) {
		path := filepath.Join(dir, "bin", binary)
		if isExecutableFile(path) {
			continue
		}
		homePath := filepath.Join(dir, "home", "bin", binary)
		if isExecutableFile(homePath) {
			continue
		}
	}
	return nil
}

func extractSelectedArtifact(data []byte, artifact Artifact, entry PlatformArtifact, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	found := false
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name, ok := cleanArchivePath(header.Name, entry.StripComponents)
		if !ok {
			continue
		}
		if entry.Home {
			homeDir := filepath.Join(dir, "home")
			if err := extractTarEntry(tr, header, filepath.Join(homeDir, name), homeDir); err != nil {
				return err
			}
			if filepath.ToSlash(name) == cleanExtract(entry.Extract) {
				found = true
			}
			continue
		}
		if filepath.Base(name) != filepath.Base(entry.Extract) {
			continue
		}
		target := filepath.Join(dir, "bin", artifact.DefaultBinary)
		if err := extractTarEntry(tr, header, target, filepath.Join(dir, "bin")); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return fmt.Errorf("archive did not contain expected path %s", entry.Extract)
	}
	return nil
}

func extractTarArchive(data []byte, stripComponents int, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, ok := cleanArchivePath(header.Name, stripComponents)
		if !ok {
			continue
		}
		if err := extractTarEntry(tr, header, filepath.Join(dir, name), dir); err != nil {
			return err
		}
	}
}

func copyExecutableFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("source is a directory")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode&0o111 == 0 {
		mode |= 0o755
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func extractTarEntry(r io.Reader, header *tar.Header, target, root string) error {
	cleanTarget := filepath.Clean(target)
	cleanRoot := filepath.Clean(root)
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(cleanTarget, header.FileInfo().Mode().Perm())
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
			return err
		}
		mode := header.FileInfo().Mode().Perm()
		if mode&0o111 == 0 {
			mode |= 0o755
		}
		out, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, r)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	case tar.TypeSymlink:
		linkName := filepath.FromSlash(strings.TrimSpace(header.Linkname))
		if linkName == "" || filepath.IsAbs(linkName) {
			return nil
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(cleanTarget), linkName))
		if resolved != cleanRoot && !strings.HasPrefix(resolved, cleanRoot+string(filepath.Separator)) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
			return err
		}
		return os.Symlink(header.Linkname, cleanTarget)
	default:
		return nil
	}
}

func cleanArchivePath(name string, strip int) (string, bool) {
	name = filepath.ToSlash(filepath.Clean(strings.TrimSpace(name)))
	if name == "." || name == "" || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
		return "", false
	}
	parts := strings.Split(name, "/")
	if strip > len(parts) {
		return "", false
	}
	parts = parts[strip:]
	if len(parts) == 0 {
		return "", false
	}
	cleaned := filepath.ToSlash(filepath.Clean(strings.Join(parts, "/")))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	return cleaned, true
}

func (s *Store) verifyInstall(artifact Artifact, entry PlatformArtifact, platform Platform) error {
	metaPath := filepath.Join(s.artifactPlatformDir(artifact, platform), "install.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	var meta InstallMetadata
	if err := machine.DecodeArtifact(data, &meta, &meta.ArtifactIdentity, installMetadataKind, installMetadataSchemaDescriptor, "reinstall the toolchain artifact"); err != nil {
		return err
	}
	if meta.Name != artifact.Name || meta.Version != artifact.Version || meta.Platform != platform.String() {
		return fmt.Errorf("install metadata does not match manifest")
	}
	if !strings.EqualFold(meta.SourceSHA256, entry.SHA256) {
		return fmt.Errorf("install metadata checksum does not match manifest")
	}
	return nil
}

func (s *Store) artifactPlatformDir(artifact Artifact, platform Platform) string {
	return filepath.Join(s.Dir, "artifacts", artifact.Name, artifact.Version, platform.DirName())
}

func (s *Store) manifestSHA256() string {
	if s.ManifestSHA256 != "" {
		return s.ManifestSHA256
	}
	data, err := json.Marshal(s.Manifest)
	if err != nil {
		return ""
	}
	return ManifestSHA256(data)
}

func (s *Store) homePath(artifact Artifact, platform Platform) string {
	return filepath.Join(s.artifactPlatformDir(artifact, platform), "home")
}

func (s *Store) managedBinaryPath(artifact Artifact, entry PlatformArtifact, platform Platform) string {
	if entry.Home {
		return filepath.Join(s.homePath(artifact, platform), cleanExtract(entry.Extract))
	}
	return filepath.Join(s.artifactPlatformDir(artifact, platform), "bin", artifact.DefaultBinary)
}

func verifySHA256(data []byte, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if !isSHA256(want) {
		return fmt.Errorf("invalid sha256")
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	return nil
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func isFalseEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}
