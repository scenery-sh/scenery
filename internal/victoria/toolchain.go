package victoria

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/toolchain"
)

func managedToolchainArtifactStatus(stateRoot, name string) (toolchain.ArtifactStatus, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	store, err := toolchain.NewStore(toolchainStoreDirForStateRoot(stateRoot), manifest)
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	return store.Path(context.Background(), name, toolchain.CurrentPlatform())
}

func syncManagedToolchainArtifact(ctx context.Context, stateRoot, name string) (toolchain.ArtifactStatus, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	store, err := toolchain.NewStore(toolchainStoreDirForStateRoot(stateRoot), manifest)
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	status, err := store.Sync(ctx, toolchain.Options{Tool: name})
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	for _, artifact := range status.Artifacts {
		if artifact.Name == name {
			return artifact, nil
		}
	}
	return toolchain.ArtifactStatus{}, fmt.Errorf("toolchain artifact %s was not reported after sync", name)
}

func toolchainStoreDirForStateRoot(stateRoot string) string {
	if strings.TrimSpace(envpolicy.Get("SCENERY_TOOLCHAIN_DIR")) != "" {
		return toolchain.DefaultStoreDir("")
	}
	if stateRoot == "" {
		return toolchain.DefaultStoreDir(".")
	}
	return filepath.Join(filepath.Dir(filepath.Clean(stateRoot)), "toolchain")
}
