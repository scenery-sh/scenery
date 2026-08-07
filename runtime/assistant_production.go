package runtime

// This file owns the production-only handoff from generated, embedded
// assistant assets to the app-child runtime.  It deliberately knows only
// provider-neutral descriptors and archive bytes.  Development supervision
// remains in cmd/scenery and does not use this path.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/runtimeassets"
)

const (
	assistantProductionService    = "scenery/assistant-production"
	assistantProductionStateDir   = "assistant-runtime"
	assistantProductionConfigName = "runtime.json"
	assistantMCPURLEnv            = "SCENERY_MCP_URL"
	assistantControlAddrEnv       = "SCENERY_ASSISTANT_CONTROL_ADDR"
	assistantControlTokenEnv      = "SCENERY_ASSISTANT_CONTROL_TOKEN"
	assistantBridgeSecretEnv      = "SCENERY_MCP_BRIDGE_SECRET"
	assistantIDEnv                = "SCENERY_ASSISTANT_ID"
	assistantControlTokenBytes    = 32
	assistantBridgeSecretBytes    = 32
)

// AssistantAssetDescriptor is the provider-neutral descriptor emitted by the
// generated assets package. Tree descriptor JSON is carried separately so the
// runtime can verify an archive before installation without extracting it
// speculatively or trusting only a digest string.
type AssistantAssetDescriptor struct {
	Kind                 string `json:"kind"`
	SchemaRevision       string `json:"schema_revision"`
	AssistantAddress     string `json:"assistant_address"`
	Target               string `json:"target"`
	RuntimeRevision      string `json:"runtime_revision"`
	CapabilityRevision   string `json:"capability_revision"`
	NodeArchiveDigest    string `json:"node_archive_digest"`
	NodeTreeDigest       string `json:"node_tree_digest"`
	CapsuleArchiveDigest string `json:"capsule_archive_digest"`
	CapsuleTreeDigest    string `json:"capsule_tree_digest"`
	CapsuleEntry         string `json:"capsule_entry"`
	PackageLockDigest    string `json:"package_lock_digest"`
}

// AssistantEmbeddedAsset is the generated-code-safe production asset input.
// Generated packages should copy their immutable embedded byte slices before
// calling RegisterEmbeddedAssistantAssets; this function never mutates them.
type AssistantEmbeddedAsset struct {
	Descriptor            AssistantAssetDescriptor
	DescriptorJSON        []byte
	NodeArchive           []byte
	NodeDescriptorJSON    []byte
	CapsuleArchive        []byte
	CapsuleDescriptorJSON []byte
}

// AssistantProductionOptions controls the private production state root. An
// empty StateRoot uses SCENERY_APP_ROOT/.scenery/assistant-runtime, or a
// deterministic user-cache path keyed by ApplicationID when no app root is
// available, so no arbitrary working directory becomes runtime state.
type AssistantProductionOptions struct {
	StateRoot     string
	ApplicationID string
}

type assistantProductionAsset struct {
	input      AssistantEmbeddedAsset
	node       runtimeassets.InstallResult
	capsule    runtimeassets.InstallResult
	controlURL string
	mcpURL     string
	token      string
	bridge     string
	homePath   string
	process    *productionAssistantProcess
}

type assistantProductionRuntime struct {
	mu             sync.Mutex
	stateRoot      string
	configPath     string
	assets         map[string]*assistantProductionAsset
	previousConfig string
	closed         bool
}

var activeAssistantProduction struct {
	sync.Mutex
	runtime *assistantProductionRuntime
}

// RegisterEmbeddedAssistantAssets installs one deterministic production
// service. The generated composition calls this after assistant and MCP
// manifest registration; the service then installs verified assets, writes the
// strict runtime descriptor, and starts every helper before the private MCP
// gateway and bootstrap dependencies run.
func RegisterEmbeddedAssistantAssets(options AssistantProductionOptions, assets []AssistantEmbeddedAsset) error {
	if len(assets) == 0 {
		return nil
	}
	if err := validateEmbeddedAssistantAssets(assets); err != nil {
		return err
	}
	if key := assistantTokenKeyFromRuntime(); len(key) != 32 {
		return errors.New("runtime: assistant token key is unavailable")
	}
	if strings.TrimSpace(options.ApplicationID) == "" {
		options.ApplicationID = strings.TrimSpace(envpolicy.Get("SCENERY_BASE_APP_ID"))
	}
	if strings.TrimSpace(options.ApplicationID) == "" {
		options.ApplicationID = productionAssetIdentity(assets)
	}
	if err := validateProductionApplicationID(options.ApplicationID); err != nil {
		return err
	}
	stateRoot, err := assistantProductionStateRoot(options.StateRoot, options.ApplicationID)
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if _, ok := assistantRegistrationByAddress(asset.Descriptor.AssistantAddress); !ok {
			return fmt.Errorf("runtime: embedded assistant %s is not registered", asset.Descriptor.AssistantAddress)
		}
	}
	// Validate the generated MCP gateway registrations before mutating the
	// service registry.  A composition error must not leave a production
	// manager half-registered for a later retry.
	global.mu.RLock()
	for _, asset := range assets {
		service := assistantMCPGatewayServiceAddress(asset.Descriptor.AssistantAddress)
		if _, ok := global.serviceInitializers[service]; !ok {
			global.mu.RUnlock()
			return fmt.Errorf("runtime: assistant MCP gateway %s is not registered", service)
		}
	}
	global.mu.RUnlock()
	manager := &assistantProductionRuntime{stateRoot: stateRoot, configPath: filepath.Join(stateRoot, assistantProductionConfigName), assets: make(map[string]*assistantProductionAsset, len(assets))}
	for index := range assets {
		assetCopy := cloneEmbeddedAsset(assets[index])
		manager.assets[assetCopy.Descriptor.AssistantAddress] = &assistantProductionAsset{input: assetCopy}
	}
	if err := RegisterNativeService(NativeServiceRegistration{
		Address:    assistantProductionService,
		Initialize: func(ctx context.Context) error { return manager.initialize(ctx) },
		Shutdown:   func(ctx context.Context) error { return manager.close(ctx) },
	}); err != nil {
		return err
	}
	// The MCP gateway must observe this manager's config and helper process
	// before it allocates its listener. Bootstrap already depends on each MCP
	// gateway and therefore remains last in the initialization graph.
	global.mu.Lock()
	for address := range manager.assets {
		service := assistantMCPGatewayServiceAddress(address)
		initializer := global.serviceInitializers[service]
		if !slicesContains(initializer.dependencies, assistantProductionService) {
			initializer.dependencies = append(initializer.dependencies, assistantProductionService)
			initializer.dependencies = canonicalContractAddresses(initializer.dependencies)
			global.serviceInitializers[service] = initializer
		}
	}
	global.mu.Unlock()
	return nil
}

func cloneEmbeddedAsset(input AssistantEmbeddedAsset) AssistantEmbeddedAsset {
	input.DescriptorJSON = append([]byte(nil), input.DescriptorJSON...)
	input.NodeArchive = append([]byte(nil), input.NodeArchive...)
	input.NodeDescriptorJSON = append([]byte(nil), input.NodeDescriptorJSON...)
	input.CapsuleArchive = append([]byte(nil), input.CapsuleArchive...)
	input.CapsuleDescriptorJSON = append([]byte(nil), input.CapsuleDescriptorJSON...)
	return input
}

func assistantProductionStateRoot(value, applicationID string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		root := strings.TrimSpace(envpolicy.Get("SCENERY_APP_ROOT"))
		if root != "" {
			value = filepath.Join(root, ".scenery", assistantProductionStateDir)
		} else {
			cacheRoot, err := os.UserCacheDir()
			if err != nil || strings.TrimSpace(cacheRoot) == "" {
				return "", errors.New("runtime: assistant state root requires SCENERY_APP_ROOT or a user cache directory")
			}
			sum := sha256.Sum256([]byte(applicationID))
			value = filepath.Join(cacheRoot, "scenery", assistantProductionStateDir, hex.EncodeToString(sum[:]))
		}
	}
	value, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("runtime: resolve assistant state root: %w", err)
	}
	if value == string(filepath.Separator) || filepath.VolumeName(value) != "" && value == filepath.VolumeName(value)+string(filepath.Separator) {
		return "", errors.New("runtime: assistant state root may not be a filesystem root")
	}
	if err := os.MkdirAll(value, 0o700); err != nil {
		return "", fmt.Errorf("runtime: create assistant state root: %w", err)
	}
	if err := os.Chmod(value, 0o700); err != nil {
		return "", fmt.Errorf("runtime: protect assistant state root: %w", err)
	}
	return filepath.Clean(value), nil
}

func productionAssetIdentity(assets []AssistantEmbeddedAsset) string {
	addresses := make([]string, 0, len(assets))
	for _, asset := range assets {
		addresses = append(addresses, asset.Descriptor.AssistantAddress+"\x00"+asset.Descriptor.RuntimeRevision+"\x00"+asset.Descriptor.CapabilityRevision)
	}
	sort.Strings(addresses)
	sum := sha256.Sum256([]byte(strings.Join(addresses, "\x00")))
	return "embedded-" + hex.EncodeToString(sum[:])
}

func validateProductionApplicationID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > 256 || strings.IndexFunc(value, func(r rune) bool { return r <= 0x20 || r == '/' || r == '\\' || r == ':' }) >= 0 {
		return errors.New("runtime: assistant application identity is invalid")
	}
	return nil
}

func validateEmbeddedAssistantAssets(assets []AssistantEmbeddedAsset) error {
	seen := make(map[string]struct{}, len(assets))
	for index := range assets {
		d := assets[index].Descriptor
		if d.Kind != runtimeassets.AssistantAssetKind || d.SchemaRevision != runtimeassets.AssistantAssetSchemaRevision {
			return fmt.Errorf("runtime: assistant asset %d has unsupported descriptor identity", index)
		}
		for name, value := range map[string]string{
			"assistant_address": d.AssistantAddress, "target": d.Target, "runtime_revision": d.RuntimeRevision,
			"capability_revision": d.CapabilityRevision, "node_archive_digest": d.NodeArchiveDigest,
			"node_tree_digest": d.NodeTreeDigest, "capsule_archive_digest": d.CapsuleArchiveDigest,
			"capsule_tree_digest": d.CapsuleTreeDigest, "capsule_entry": d.CapsuleEntry,
			"package_lock_digest": d.PackageLockDigest,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("runtime: assistant asset %d %s is required", index, name)
			}
		}
		if d.Target != runtime.GOOS+"/"+runtime.GOARCH {
			return fmt.Errorf("runtime: assistant asset %s targets %s, want %s", d.AssistantAddress, d.Target, runtime.GOOS+"/"+runtime.GOARCH)
		}
		if d.CapsuleEntry != ".scenery/bootstrap.mjs" || strings.Contains(d.AssistantAddress, "\\") || strings.ContainsAny(d.AssistantAddress, "?#\x00") {
			return fmt.Errorf("runtime: assistant asset %s has invalid capsule entry or address", d.AssistantAddress)
		}
		key := d.AssistantAddress + "\x00" + d.Target
		if _, ok := seen[key]; ok {
			return fmt.Errorf("runtime: duplicate assistant asset %s", d.AssistantAddress)
		}
		seen[key] = struct{}{}
		if err := validateEmbeddedTreeDescriptor(assets[index].NodeDescriptorJSON, d.NodeTreeDigest); err != nil {
			return fmt.Errorf("runtime: assistant %s node descriptor: %w", d.AssistantAddress, err)
		}
		if err := validateEmbeddedTreeDescriptor(assets[index].CapsuleDescriptorJSON, d.CapsuleTreeDigest); err != nil {
			return fmt.Errorf("runtime: assistant %s capsule descriptor: %w", d.AssistantAddress, err)
		}
		if digestBytes(assets[index].NodeArchive) != d.NodeArchiveDigest || len(assets[index].NodeArchive) == 0 {
			return fmt.Errorf("runtime: assistant %s node archive digest mismatch", d.AssistantAddress)
		}
		if digestBytes(assets[index].CapsuleArchive) != d.CapsuleArchiveDigest || len(assets[index].CapsuleArchive) == 0 {
			return fmt.Errorf("runtime: assistant %s capsule archive digest mismatch", d.AssistantAddress)
		}
		if len(assets[index].DescriptorJSON) > 0 {
			var encoded AssistantAssetDescriptor
			if err := decodeStrictObject(assets[index].DescriptorJSON, &encoded); err != nil {
				return fmt.Errorf("runtime: assistant %s descriptor JSON: %w", d.AssistantAddress, err)
			}
			if encoded != d {
				return fmt.Errorf("runtime: assistant %s descriptor JSON does not match embedded descriptor", d.AssistantAddress)
			}
		}
	}
	return nil
}

func validateEmbeddedTreeDescriptor(data []byte, expected string) error {
	if len(data) == 0 {
		return errors.New("tree descriptor JSON is missing")
	}
	var descriptor runtimeassets.Descriptor
	if err := decodeStrictObject(data, &descriptor); err != nil {
		return err
	}
	if err := descriptor.Validate(); err != nil {
		return err
	}
	if descriptor.Digest != expected {
		return fmt.Errorf("tree digest mismatch: got %s want %s", descriptor.Digest, expected)
	}
	return nil
}

func decodeStrictObject(data []byte, target any) error {
	if _, err := contract.DecodeJSONObject(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func digestBytes(value []byte) string {
	// Archive digests in generated descriptors are SHA-256 strings. Keep this
	// helper local so no implementation package enters the generated/runtime
	// boundary.
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assistantRegistrationByAddress(address string) (AssistantRegistration, bool) {
	global.mu.RLock()
	registration, ok := global.assistants[address]
	global.mu.RUnlock()
	return registration, ok
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (manager *assistantProductionRuntime) initialize(ctx context.Context) error {
	if manager == nil {
		return errors.New("runtime: assistant production manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return errors.New("runtime: assistant production manager is stopped")
	}
	manager.mu.Unlock()

	addresses := make([]string, 0, len(manager.assets))
	for address := range manager.assets {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	config := AssistantRuntimeConfig{Assistants: make([]AssistantBootstrapDescriptor, 0, len(addresses))}
	manager.previousConfig = envpolicy.Get(AssistantRuntimeConfigEnv)
	started := make([]*assistantProductionAsset, 0, len(addresses))
	for _, address := range addresses {
		item := manager.assets[address]
		asset := item.input
		nodeDescriptor, err := parseTreeDescriptor(asset.NodeDescriptorJSON)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s node descriptor: %w", address, err)
		}
		capsuleDescriptor, err := parseTreeDescriptor(asset.CapsuleDescriptorJSON)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s capsule descriptor: %w", address, err)
		}
		nodeArchive, err := runtimeassets.NewArchive(asset.NodeArchive, nodeDescriptor)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s node archive: %w", address, err)
		}
		capsuleArchive, err := runtimeassets.NewArchive(asset.CapsuleArchive, capsuleDescriptor)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s capsule archive: %w", address, err)
		}
		nodeInstall, err := runtimeassets.InstallContext(ctx, manager.stateRoot, nodeArchive)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s node install: %w", address, err)
		}
		capsuleInstall, err := runtimeassets.InstallContext(ctx, manager.stateRoot, capsuleArchive)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s capsule install: %w", address, err)
		}
		nodePath := filepath.Join(nodeInstall.Path, "bin", "node")
		if err := validateExecutable(nodePath); err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s managed runtime executable: %w", address, err)
		}
		entry := filepath.Join(capsuleInstall.Path, filepath.FromSlash(asset.Descriptor.CapsuleEntry))
		if err := validateCapsuleEntry(entry); err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s capsule entry: %w", address, err)
		}
		controlURL, err := allocateLoopbackURL()
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return err
		}
		mcpURL, err := allocateLoopbackURL()
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return err
		}
		token, err := randomSecretHex(assistantControlTokenBytes)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return err
		}
		bridge, err := randomSecretHex(assistantBridgeSecretBytes)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			return err
		}
		item.node, item.capsule, item.controlURL, item.mcpURL, item.token, item.bridge = nodeInstall, capsuleInstall, controlURL, mcpURL, token, bridge
		homeDigest := sha256.Sum256([]byte(address))
		item.homePath = filepath.Join(manager.stateRoot, "assistant-homes", hex.EncodeToString(homeDigest[:]))
		if err := ensurePrivateDirectory(item.homePath); err != nil {
			manager.cleanupStarted(ctx, started)
			return fmt.Errorf("runtime: assistant %s private home: %w", address, err)
		}
		config.Assistants = append(config.Assistants, AssistantBootstrapDescriptor{
			AssistantAddress: address, ControlAddress: controlURL, ControlToken: token, MCPListenAddress: strings.TrimPrefix(mcpURL, "http://"), MCPBridgeSecret: bridge,
			RuntimeRevision: asset.Descriptor.RuntimeRevision, CapabilityRevision: asset.Descriptor.CapabilityRevision, Required: true,
		})
		started = append(started, item)
	}
	if err := WriteAssistantRuntimeConfig(manager.configPath, config); err != nil {
		manager.cleanupStarted(ctx, started)
		return fmt.Errorf("runtime: write assistant runtime config: %w", err)
	}
	if err := envpolicy.Set(AssistantRuntimeConfigEnv, manager.configPath); err != nil {
		manager.cleanupStarted(ctx, started)
		manager.rollbackConfig()
		return fmt.Errorf("runtime: publish assistant runtime config: %w", err)
	}
	for _, item := range started {
		entry := filepath.Join(item.capsule.Path, filepath.FromSlash(item.input.Descriptor.CapsuleEntry))
		nodePath := filepath.Join(item.node.Path, "bin", "node")
		process, err := startProductionAssistantProcess(ctx, nodePath, entry, item)
		if err != nil {
			manager.cleanupStarted(ctx, started)
			manager.rollbackConfig()
			return fmt.Errorf("runtime: start assistant %s: %w", item.input.Descriptor.AssistantAddress, err)
		}
		item.process = process
	}
	activeAssistantProduction.Lock()
	activeAssistantProduction.runtime = manager
	activeAssistantProduction.Unlock()
	return nil
}

func parseTreeDescriptor(data []byte) (runtimeassets.Descriptor, error) {
	var descriptor runtimeassets.Descriptor
	if err := decodeStrictObject(data, &descriptor); err != nil {
		return runtimeassets.Descriptor{}, err
	}
	if err := descriptor.Validate(); err != nil {
		return runtimeassets.Descriptor{}, err
	}
	return descriptor, nil
}

func validateExecutable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm() != 0o755 {
		return errors.New("path is not a private executable with mode 0755")
	}
	return nil
}

func validateCapsuleEntry(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || (info.Mode().Perm() != 0o644 && info.Mode().Perm() != 0o755) {
		return errors.New("path is not a regular capsule script with safe mode")
	}
	return nil
}

func allocateLoopbackURL() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("runtime: allocate private loopback port: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("runtime: release private loopback port: %w", err)
	}
	return "http://" + address, nil
}

func randomSecretHex(size int) (string, error) {
	if size <= 0 {
		return "", errors.New("runtime: secret size is invalid")
	}
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("runtime: generate private assistant secret: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func (manager *assistantProductionRuntime) cleanupStarted(ctx context.Context, started []*assistantProductionAsset) {
	for index := len(started) - 1; index >= 0; index-- {
		if started[index] != nil && started[index].process != nil {
			_ = started[index].process.Stop(ctx)
		}
	}
}

func (manager *assistantProductionRuntime) rollbackConfig() {
	if manager == nil {
		return
	}
	if current := envpolicy.Get(AssistantRuntimeConfigEnv); current == manager.configPath {
		if manager.previousConfig == "" {
			_ = envpolicy.Unset(AssistantRuntimeConfigEnv)
		} else {
			_ = envpolicy.Set(AssistantRuntimeConfigEnv, manager.previousConfig)
		}
	}
	_ = os.Remove(manager.configPath)
}

func (manager *assistantProductionRuntime) close(ctx context.Context) error {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	previousConfig := manager.previousConfig
	manager.mu.Unlock()
	for _, item := range manager.assets {
		if item != nil && item.process != nil {
			_ = item.process.Stop(ctx)
		}
	}
	if current := envpolicy.Get(AssistantRuntimeConfigEnv); current == manager.configPath {
		if previousConfig == "" {
			_ = envpolicy.Unset(AssistantRuntimeConfigEnv)
		} else {
			_ = envpolicy.Set(AssistantRuntimeConfigEnv, previousConfig)
		}
		_ = os.Remove(manager.configPath)
	}
	activeAssistantProduction.Lock()
	if activeAssistantProduction.runtime == manager {
		activeAssistantProduction.runtime = nil
	}
	activeAssistantProduction.Unlock()
	return nil
}

type productionAssistantProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	err  error
	mu   sync.Mutex
}

func startProductionAssistantProcess(_ context.Context, nodePath, entry string, item *assistantProductionAsset) (*productionAssistantProcess, error) {
	workingDirectory := strings.TrimSpace(item.homePath)
	if workingDirectory == "" {
		homeDigest := sha256.Sum256([]byte(item.input.Descriptor.AssistantAddress))
		workingDirectory = filepath.Join(filepath.Dir(item.capsule.Path), ".assistant-home-"+hex.EncodeToString(homeDigest[:]))
		item.homePath = workingDirectory
	}
	if err := ensurePrivateDirectory(workingDirectory); err != nil {
		return nil, fmt.Errorf("prepare assistant working directory: %w", err)
	}
	cmd := exec.Command(nodePath, entry)
	// The content-addressed capsule is immutable and is reverified on every
	// process start. Provider runtimes may write relative state, so they run
	// from the assistant's private home while importing the verified absolute
	// entry path from the capsule.
	cmd.Dir = workingDirectory
	cmd.Env = productionAssistantEnvironment(item)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &productionAssistantProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		process.once.Do(func() { close(process.done) })
	}()
	return process, nil
}

func productionAssistantEnvironment(item *assistantProductionAsset) []string {
	home := item.homePath
	if strings.TrimSpace(home) == "" {
		homeDigest := sha256.Sum256([]byte(item.input.Descriptor.AssistantAddress))
		home = filepath.Join(filepath.Dir(item.capsule.Path), ".assistant-home-"+hex.EncodeToString(homeDigest[:]))
	}
	_ = os.MkdirAll(home, 0o700)
	_ = os.Chmod(home, 0o700)
	values := []string{"PATH=" + filepath.Join(item.node.Path, "bin"), "HOME=" + home}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"} {
		if value, ok := envpolicy.Lookup(key); ok && value != "" {
			values = append(values, key+"="+value)
		}
	}
	values = append(values,
		assistantIDEnv+"="+item.input.Descriptor.AssistantAddress,
		assistantControlAddrEnv+"="+item.controlURL,
		assistantControlTokenEnv+"="+item.token,
		assistantMCPURLEnv+"="+item.mcpURL,
		assistantBridgeSecretEnv+"="+item.bridge,
		"SCENERY_CAPABILITY_REVISION="+item.input.Descriptor.CapabilityRevision,
		"SCENERY_RUNTIME_REVISION="+item.input.Descriptor.RuntimeRevision,
	)
	return values
}

func ensurePrivateDirectory(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("private directory path is empty")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("private directory is not a regular directory")
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	} else {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("private directory has unsafe mode")
	}
	return nil
}

func (process *productionAssistantProcess) PID() int {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return 0
	}
	return process.cmd.Process.Pid
}

func (process *productionAssistantProcess) Wait() error {
	if process == nil {
		return nil
	}
	<-process.done
	process.mu.Lock()
	err := process.err
	process.mu.Unlock()
	return err
}

func (process *productionAssistantProcess) Stop(ctx context.Context) error {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := process.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = process.cmd.Process.Kill()
	}
	select {
	case <-process.done:
		return nil
	case <-ctx.Done():
		_ = process.cmd.Process.Kill()
		<-process.done
		return ctx.Err()
	case <-time.After(5 * time.Second):
		_ = process.cmd.Process.Kill()
		<-process.done
		return nil
	}
}

var _ assistantruntime.Process = (*productionAssistantProcess)(nil)
