package devtools

import (
	"fmt"
	"strings"
	"sync"

	"scenery.sh/internal/toolchain"
)

const pinnedVersionsKind = "scenery.internal.devtools.versions"

const pinnedVersionsSchemaRevision = "sha256:cb117e0934975289d6f6047f449e4e281c4f0f93e6a7cecd4ab1a9d6936c7044"

type PinnedVersionsConfig struct {
	Kind           string           `json:"kind"`
	SchemaRevision string           `json:"schema_revision"`
	Victoria       VictoriaVersions `json:"victoria"`
}

type VictoriaVersions struct {
	Metrics VersionPin `json:"metrics"`
	Logs    VersionPin `json:"logs"`
	Traces  VersionPin `json:"traces"`
}

type VersionPin struct {
	Version string `json:"version"`
}

var (
	pinnedVersionsOnce sync.Once
	pinnedVersions     PinnedVersionsConfig
	pinnedVersionsErr  error
)

func PinnedVersions() PinnedVersionsConfig {
	pinnedVersionsOnce.Do(func() {
		pinnedVersions, pinnedVersionsErr = pinnedVersionsFromToolchainManifest()
	})
	if pinnedVersionsErr != nil {
		panic(pinnedVersionsErr)
	}
	return pinnedVersions
}

func pinnedVersionsFromToolchainManifest() (PinnedVersionsConfig, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return PinnedVersionsConfig{}, err
	}
	cfg := PinnedVersionsConfig{
		Kind: pinnedVersionsKind, SchemaRevision: pinnedVersionsSchemaRevision,
	}
	for _, artifact := range manifest.Artifacts {
		switch artifact.Name {
		case "victoria-metrics":
			cfg.Victoria.Metrics.Version = artifact.Version
		case "victoria-logs":
			cfg.Victoria.Logs.Version = artifact.Version
		case "victoria-traces":
			cfg.Victoria.Traces.Version = artifact.Version
		}
	}
	if err := validatePinnedVersions(cfg); err != nil {
		return PinnedVersionsConfig{}, err
	}
	return cfg, nil
}

func validatePinnedVersions(cfg PinnedVersionsConfig) error {
	if cfg.Kind != pinnedVersionsKind || cfg.SchemaRevision != pinnedVersionsSchemaRevision {
		return fmt.Errorf("unsupported internal devtool versions identity %q at %q", cfg.Kind, cfg.SchemaRevision)
	}
	if strings.TrimSpace(cfg.Victoria.Metrics.Version) == "" {
		return fmt.Errorf("internal devtool versions missing victoria.metrics.version")
	}
	if strings.TrimSpace(cfg.Victoria.Logs.Version) == "" {
		return fmt.Errorf("internal devtool versions missing victoria.logs.version")
	}
	if strings.TrimSpace(cfg.Victoria.Traces.Version) == "" {
		return fmt.Errorf("internal devtool versions missing victoria.traces.version")
	}
	return nil
}
