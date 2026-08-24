package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"scenery.sh/internal/runtimeassets"
)

func TestProductionMissingTokenKeyFailsClosed(t *testing.T) {
	t.Setenv(AssistantTokenKeyEnv, "")
	t.Setenv(AssistantTokenKeyFileEnv, "")
	asset := testProductionEmbeddedAsset(t, "app/assistant/support")
	if err := RegisterEmbeddedAssistantAssets(AssistantProductionOptions{StateRoot: t.TempDir(), ApplicationID: "production-key-test"}, []AssistantEmbeddedAsset{asset}); err == nil {
		t.Fatal("RegisterEmbeddedAssistantAssets accepted a missing production token key")
	}
}

func TestProductionRegistrationRejectsMissingGatewayBeforeMutation(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	t.Setenv(AssistantTokenKeyEnv, strings.Repeat("k", 32))
	t.Setenv(AssistantTokenKeyFileEnv, "")
	asset := testProductionEmbeddedAsset(t, "app/assistant/support")
	if err := RegisterEmbeddedAssistantAssets(AssistantProductionOptions{StateRoot: t.TempDir(), ApplicationID: "missing-gateway-test"}, []AssistantEmbeddedAsset{asset}); err == nil {
		t.Fatal("RegisterEmbeddedAssistantAssets accepted an unregistered MCP gateway")
	}
	global.mu.RLock()
	_, registered := global.serviceInitializers[assistantProductionService]
	global.mu.RUnlock()
	if registered {
		t.Fatal("production service remained registered after missing gateway rejection")
	}
}

func TestProductionAssistantEnvironmentUsesStrictAllowlist(t *testing.T) {
	t.Setenv("SCENERY_DATABASE_URL", "postgres://ambient-secret")
	t.Setenv("SCENERY_PROVIDER_TOKEN", "provider-secret")
	t.Setenv("LANG", "en_US.UTF-8")
	asset := testProductionEmbeddedAsset(t, "app/assistant/support")
	item := &assistantProductionAsset{
		input:      asset,
		node:       runtimeassets.InstallResult{Path: filepath.Join(t.TempDir(), "node")},
		capsule:    runtimeassets.InstallResult{Path: filepath.Join(t.TempDir(), "capsule")},
		homePath:   filepath.Join(t.TempDir(), "home"),
		controlURL: "http://127.0.0.1:4101",
		mcpURL:     "http://127.0.0.1:4102",
		token:      "control-token",
		bridge:     "bridge-secret",
	}
	env := productionAssistantEnvironment(item)
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"SCENERY_DATABASE_URL", "postgres://ambient-secret", "SCENERY_PROVIDER_TOKEN", "provider-secret"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("production helper environment leaked %q: %v", forbidden, env)
		}
	}
	for _, required := range []string{
		"PATH=" + filepath.Join(item.node.Path, "bin"),
		"HOME=" + item.homePath,
		"SCENERY_ASSISTANT_ID=" + asset.Descriptor.AssistantAddress,
		"SCENERY_ASSISTANT_CONTROL_ADDR=" + item.controlURL,
		"SCENERY_ASSISTANT_CONTROL_TOKEN=" + item.token,
		"SCENERY_MCP_URL=" + item.mcpURL,
		"SCENERY_MCP_BRIDGE_SECRET=" + item.bridge,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("production helper environment missing %q: %v", required, env)
		}
	}
}

func TestProductionCapsuleEntryAllowsPrivateRegular0644(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.mjs")
	if err := os.WriteFile(path, []byte("// bootstrap"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateCapsuleEntry(path); err != nil {
		t.Fatalf("validateCapsuleEntry(0644) = %v", err)
	}
}

func TestProductionStateRootUsesIdentityCacheWithoutAppRoot(t *testing.T) {
	t.Setenv("SCENERY_APP_ROOT", "")
	first, err := assistantProductionStateRoot("", "production-cache-test-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := assistantProductionStateRoot("", "production-cache-test-b")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := assistantProductionStateRoot("", "production-cache-test-a")
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(cacheRoot, "scenery", assistantProductionStateDir) + string(filepath.Separator)
	if !strings.HasPrefix(first, prefix) || !strings.HasPrefix(second, prefix) {
		t.Fatalf("state roots escaped the deterministic cache root: %q %q", first, second)
	}
	if first != repeated || first == second {
		t.Fatalf("state root identity is not deterministic: first=%q repeated=%q second=%q", first, repeated, second)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(first)
		_ = os.RemoveAll(second)
	})
}

func TestProductionInstallsStartsReusesAndRejectsTamperedAssets(t *testing.T) {
	asset := testProductionEmbeddedAsset(t, "app/assistant/support")
	stateRoot := t.TempDir()
	configPath := filepath.Join(stateRoot, "runtime.json")
	manager := &assistantProductionRuntime{
		stateRoot:  stateRoot,
		configPath: configPath,
		assets: map[string]*assistantProductionAsset{
			asset.Descriptor.AssistantAddress: &assistantProductionAsset{input: asset},
		},
	}
	t.Setenv(AssistantRuntimeConfigEnv, "")
	if err := manager.initialize(context.Background()); err != nil {
		t.Fatalf("initialize() = %v", err)
	}
	item := manager.assets[asset.Descriptor.AssistantAddress]
	if item.process == nil || item.process.PID() <= 0 {
		t.Fatalf("managed assistant process was not started: %#v", item.process)
	}
	if item.process.cmd.Dir != item.homePath || item.process.cmd.Dir == item.capsule.Path {
		t.Fatalf("managed assistant working directory = %q, home=%q capsule=%q", item.process.cmd.Dir, item.homePath, item.capsule.Path)
	}
	if err := manager.close(context.Background()); err != nil {
		t.Fatalf("close() = %v", err)
	}
	if _, err := os.Stat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime config after close: err=%v", err)
	}
	if _, err := runtimeassets.InstallContext(context.Background(), stateRoot, mustProductionArchive(t, asset.NodeArchive, asset.NodeDescriptorJSON)); err != nil {
		t.Fatalf("verified asset reuse = %v", err)
	}
	if _, err := runtimeassets.InstallContext(context.Background(), stateRoot, mustProductionArchive(t, asset.CapsuleArchive, asset.CapsuleDescriptorJSON)); err != nil {
		t.Fatalf("verified capsule reuse after helper execution = %v", err)
	}
	if err := os.WriteFile(filepath.Join(item.node.Path, "bin", "node"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	nodeArchive := mustProductionArchive(t, asset.NodeArchive, asset.NodeDescriptorJSON)
	if _, err := runtimeassets.InstallContext(context.Background(), stateRoot, nodeArchive); !errors.Is(err, runtimeassets.ErrExistingInstallTampered) {
		t.Fatalf("tampered asset error = %v, want ErrExistingInstallTampered", err)
	}
	manager = &assistantProductionRuntime{
		stateRoot:  stateRoot,
		configPath: configPath,
		assets: map[string]*assistantProductionAsset{
			asset.Descriptor.AssistantAddress: &assistantProductionAsset{input: asset},
		},
	}
	if err := manager.initialize(context.Background()); !errors.Is(err, runtimeassets.ErrExistingInstallTampered) {
		t.Fatalf("initialize() with tampered asset = %v, want ErrExistingInstallTampered", err)
	}
	if err := os.RemoveAll(item.node.Path); err != nil {
		t.Fatal(err)
	}
	if err := manager.initialize(context.Background()); err != nil {
		t.Fatalf("initialize() after tamper recovery = %v", err)
	}
	if err := manager.close(context.Background()); err != nil {
		t.Fatalf("recovered close() = %v", err)
	}
}

func TestProductionConcurrentAssetInstallReusesVerifiedTree(t *testing.T) {
	asset := testProductionEmbeddedAsset(t, "app/assistant/concurrent")
	archive := mustProductionArchive(t, asset.CapsuleArchive, asset.CapsuleDescriptorJSON)
	stateRoot := t.TempDir()
	// Two contenders are sufficient to exercise the install lock and reuse
	// path; additional identical workers only repeat archive verification.
	const workers = 2
	results := make([]runtimeassets.InstallResult, workers)
	errs := make([]error, workers)
	var group sync.WaitGroup
	for i := range workers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			results[index], errs[index] = runtimeassets.InstallContext(context.Background(), stateRoot, archive)
		}(i)
	}
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent install %d = %v", index, err)
		}
		if results[index].Path == "" || results[index].Digest != archive.Descriptor.Digest {
			t.Fatalf("concurrent install %d result = %#v", index, results[index])
		}
	}
	if _, err := os.Stat(results[0].Path); err != nil {
		t.Fatal(err)
	}
}

func testProductionEmbeddedAsset(t *testing.T, address string) AssistantEmbeddedAsset {
	t.Helper()
	root := t.TempDir()
	nodeRoot := filepath.Join(root, "node")
	if err := os.MkdirAll(filepath.Join(nodeRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	nodeScript := []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n")
	if err := os.WriteFile(filepath.Join(nodeRoot, "bin", "node"), nodeScript, 0o755); err != nil {
		t.Fatal(err)
	}
	capsuleRoot := filepath.Join(root, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleRoot, ".scenery"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capsuleRoot, ".scenery", "bootstrap.mjs"), []byte("// bootstrap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodeArchive, err := runtimeassets.BuildArchive(nodeRoot)
	if err != nil {
		t.Fatalf("build node archive: %v", err)
	}
	capsuleArchive, err := runtimeassets.BuildArchive(capsuleRoot)
	if err != nil {
		t.Fatalf("build capsule archive: %v", err)
	}
	descriptor := AssistantAssetDescriptor{
		Kind:                 runtimeassets.AssistantAssetKind,
		SchemaRevision:       runtimeassets.AssistantAssetSchemaRevision,
		AssistantAddress:     address,
		Target:               runtime.GOOS + "/" + runtime.GOARCH,
		RuntimeRevision:      "runtime-test",
		CapabilityRevision:   "capability-test",
		NodeArchiveDigest:    nodeArchive.ArchiveDigest,
		NodeTreeDigest:       nodeArchive.Descriptor.Digest,
		CapsuleArchiveDigest: capsuleArchive.ArchiveDigest,
		CapsuleTreeDigest:    capsuleArchive.Descriptor.Digest,
		CapsuleEntry:         ".scenery/bootstrap.mjs",
		PackageLockDigest:    "sha256:" + strings.Repeat("a", 64),
	}
	return AssistantEmbeddedAsset{
		Descriptor:            descriptor,
		DescriptorJSON:        mustJSON(t, descriptor),
		NodeArchive:           nodeArchive.Data,
		NodeDescriptorJSON:    mustJSON(t, nodeArchive.Descriptor),
		CapsuleArchive:        capsuleArchive.Data,
		CapsuleDescriptorJSON: mustJSON(t, capsuleArchive.Descriptor),
	}
}

func mustProductionArchive(t *testing.T, data, descriptorJSON []byte) runtimeassets.Archive {
	t.Helper()
	var descriptor runtimeassets.Descriptor
	if err := json.Unmarshal(descriptorJSON, &descriptor); err != nil {
		t.Fatal(err)
	}
	archive, err := runtimeassets.NewArchive(data, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
