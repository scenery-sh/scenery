package devtools

import (
	"fmt"
	"strings"
	"sync"

	"scenery.sh/internal/toolchain"
)

type PinnedVersionsConfig struct {
	SchemaVersion string           `json:"schema_version"`
	Victoria      VictoriaVersions `json:"victoria"`
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
		SchemaVersion: "scenery.internal.devtools.versions.v1",
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
	if cfg.SchemaVersion != "scenery.internal.devtools.versions.v1" {
		return fmt.Errorf("unsupported internal devtool versions schema %q", cfg.SchemaVersion)
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
