package toolchain

//go:generate go run ../cmd/gentoolchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ManifestKind             = "scenery.toolchain"
	ManifestSchemaRevision   = "sha256:02fd89902b530fb84a9fc13269b5ed93f1a01d8981430ba4f263963f40d86bc6"
	manifestSchemaDescriptor = `{"artifacts":"array<artifact>","kind":"scenery.toolchain","schema_revision":"digest","source_locks":"array<source-lock>"}`
	StatusKind               = "scenery.toolchain.status"
)

type Manifest struct {
	Kind           string       `json:"kind"`
	SchemaRevision string       `json:"schema_revision"`
	SourceLocks    []SourceLock `json:"source_locks"`
	Artifacts      []Artifact   `json:"artifacts"`
}

type SourceLock struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Manager  string `json:"manager,omitempty"`
	Manifest string `json:"manifest"`
	Lock     string `json:"lock,omitempty"`
}

type Artifact struct {
	Name          string                      `json:"name"`
	Kind          string                      `json:"kind"`
	Version       string                      `json:"version"`
	License       string                      `json:"license,omitempty"`
	DefaultBinary string                      `json:"default_binary,omitempty"`
	Binaries      []string                    `json:"binaries,omitempty"`
	Platforms     map[string]PlatformArtifact `json:"platforms,omitempty"`
	SourceBuild   *SourceBuildArtifact        `json:"source_build,omitempty"`
	Images        []ImageArtifact             `json:"images,omitempty"`
}

type SourceBuildArtifact struct {
	Kind    string `json:"kind"`
	Package string `json:"package"`
}

type PlatformArtifact struct {
	Archive         string   `json:"archive"`
	URL             string   `json:"url"`
	SHA256          string   `json:"sha256"`
	Extract         string   `json:"extract"`
	Home            bool     `json:"home,omitempty"`
	StripComponents int      `json:"strip_components,omitempty"`
	Build           []string `json:"build,omitempty"`
	BuildOutput     string   `json:"build_output,omitempty"`
}

type ImageArtifact struct {
	Ref       string `json:"ref"`
	Digest    string `json:"digest,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
	Usage     string `json:"usage,omitempty"`
	Stability string `json:"stability,omitempty"`
}

type Platform struct {
	GOOS   string
	GOARCH string
}

func CurrentPlatform() Platform {
	return Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func ParsePlatform(value string) (Platform, error) {
	goos, goarch, ok := strings.Cut(strings.TrimSpace(value), "/")
	if !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
		return Platform{}, fmt.Errorf("invalid platform %q, want goos/goarch", value)
	}
	return Platform{GOOS: goos, GOARCH: goarch}, nil
}

func (p Platform) String() string {
	if p.GOOS == "" || p.GOARCH == "" {
		return ""
	}
	return p.GOOS + "/" + p.GOARCH
}

func (p Platform) DirName() string {
	return strings.ReplaceAll(p.String(), "/", "-")
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("parse toolchain manifest: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return manifest, fmt.Errorf("parse toolchain manifest: trailing JSON")
	}
	if err := manifest.Validate(); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.Kind != ManifestKind || m.SchemaRevision != ManifestSchemaRevision {
		return fmt.Errorf("unsupported toolchain manifest identity %q %q", m.Kind, m.SchemaRevision)
	}
	seenLocks := map[string]bool{}
	for i, lock := range m.SourceLocks {
		if strings.TrimSpace(lock.Name) == "" {
			return fmt.Errorf("source_locks[%d] missing name", i)
		}
		if seenLocks[lock.Name] {
			return fmt.Errorf("duplicate source lock %q", lock.Name)
		}
		seenLocks[lock.Name] = true
		switch lock.Kind {
		case "go-modules", "package-manager":
		default:
			return fmt.Errorf("source_locks[%d] has invalid kind %q", i, lock.Kind)
		}
		if strings.TrimSpace(lock.Manifest) == "" {
			return fmt.Errorf("source_locks[%d] missing manifest", i)
		}
	}
	seenArtifacts := map[string]bool{}
	for i, artifact := range m.Artifacts {
		if strings.TrimSpace(artifact.Name) == "" {
			return fmt.Errorf("artifacts[%d] missing name", i)
		}
		if seenArtifacts[artifact.Name] {
			return fmt.Errorf("duplicate artifact %q", artifact.Name)
		}
		seenArtifacts[artifact.Name] = true
		switch artifact.Kind {
		case "binary", "image", "plugin":
		default:
			return fmt.Errorf("artifact %q has invalid kind %q", artifact.Name, artifact.Kind)
		}
		if strings.TrimSpace(artifact.Version) == "" {
			return fmt.Errorf("artifact %q missing version", artifact.Name)
		}
		if artifact.Kind == "binary" && strings.TrimSpace(artifact.DefaultBinary) == "" {
			return fmt.Errorf("binary artifact %q missing default_binary", artifact.Name)
		}
		if artifact.SourceBuild != nil {
			if artifact.Kind != "binary" {
				return fmt.Errorf("artifact %q has source_build but kind %q", artifact.Name, artifact.Kind)
			}
			if artifact.SourceBuild.Kind != "go" {
				return fmt.Errorf("artifact %q has unsupported source_build kind %q", artifact.Name, artifact.SourceBuild.Kind)
			}
			if cleanSourceBuildPackage(artifact.SourceBuild.Package) == "" {
				return fmt.Errorf("artifact %q source_build missing valid package", artifact.Name)
			}
		}
		for key, platform := range artifact.Platforms {
			if _, err := ParsePlatform(key); err != nil {
				return fmt.Errorf("artifact %q: %w", artifact.Name, err)
			}
			if platform.Archive != "tar.gz" {
				return fmt.Errorf("artifact %q platform %s has unsupported archive %q", artifact.Name, key, platform.Archive)
			}
			if strings.TrimSpace(platform.URL) == "" {
				return fmt.Errorf("artifact %q platform %s missing url", artifact.Name, key)
			}
			if !isSHA256(platform.SHA256) {
				return fmt.Errorf("artifact %q platform %s missing valid sha256", artifact.Name, key)
			}
			if cleanExtract(platform.Extract) == "" {
				return fmt.Errorf("artifact %q platform %s missing valid extract path", artifact.Name, key)
			}
			if len(platform.Build) > 0 && cleanExtract(platform.BuildOutput) == "" {
				return fmt.Errorf("artifact %q platform %s missing valid build_output", artifact.Name, key)
			}
		}
		for j, image := range artifact.Images {
			if strings.TrimSpace(image.Ref) == "" {
				return fmt.Errorf("artifact %q images[%d] missing ref", artifact.Name, j)
			}
			if image.Digest != "" && !strings.HasPrefix(image.Digest, "sha256:") {
				return fmt.Errorf("artifact %q images[%d] has invalid digest", artifact.Name, j)
			}
		}
	}
	return nil
}

func (m Manifest) Artifact(name string) (Artifact, bool) {
	for _, artifact := range m.Artifacts {
		if artifact.Name == name {
			return artifact, true
		}
	}
	return Artifact{}, false
}

func (a Artifact) PlatformArtifact(platform Platform) (PlatformArtifact, bool) {
	if a.Platforms == nil {
		return PlatformArtifact{}, false
	}
	entry, ok := a.Platforms[platform.String()]
	return entry, ok
}

func ManifestSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func BundledManifestBytes() []byte {
	out := make([]byte, len(bundledManifestBytes))
	copy(out, bundledManifestBytes)
	return out
}

func LoadBundledManifest() (Manifest, error) {
	return ParseManifest(bundledManifestBytes)
}

func BundledManifestSHA256() string {
	return ManifestSHA256(bundledManifestBytes)
}

func cleanExtract(value string) string {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	if value == "." || value == "" || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") {
		return ""
	}
	return value
}

func cleanSourceBuildPackage(value string) string {
	raw := filepath.ToSlash(strings.TrimSpace(value))
	if !strings.HasPrefix(raw, "./") {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(raw))
	if value == "." || value == "" || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") {
		return ""
	}
	if !strings.HasPrefix(value, ".") {
		value = "./" + value
	}
	return value
}

func isSHA256(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
