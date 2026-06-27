package main

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/toolchain"
)

func freeLoopbackPort() (int, error) {
	ln, err := netListen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %s", ln.Addr())
	}
	return addr.Port, nil
}

func managedToolchainArtifactStatus(stateRoot, name string) (toolchain.ArtifactStatus, error) {
	return managedToolchainArtifactStatusInDir(toolchainStoreDirForStateRoot(stateRoot), name)
}

func managedToolchainArtifactStatusInDir(storeDir, name string) (toolchain.ArtifactStatus, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	store, err := toolchain.NewStore(storeDir, manifest)
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	return store.Path(context.Background(), name, toolchain.CurrentPlatform())
}

func syncManagedToolchainArtifact(ctx context.Context, stateRoot, name string) (toolchain.ArtifactStatus, error) {
	return syncManagedToolchainArtifactInDir(ctx, toolchainStoreDirForStateRoot(stateRoot), name)
}

func syncManagedToolchainArtifactInDir(ctx context.Context, storeDir, name string) (toolchain.ArtifactStatus, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return toolchain.ArtifactStatus{}, err
	}
	store, err := toolchain.NewStore(storeDir, manifest)
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
