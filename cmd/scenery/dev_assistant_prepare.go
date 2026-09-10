package main

// Assistant preparation projects the graph into stable private descriptors,
// materializes each isolated provider workspace, and reuses verified outputs.
// Process startup, readiness, restart, and shutdown stay in the supervisor.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/mcpprojection"
	"scenery.sh/internal/toolchain"
)

// assistantDefinitionsFromResult returns canonical assistant implementations
// in address order.  A missing implementation block is ignored: the compiler
// already reports that as invalid and the app build remains the source of the
// user-facing diagnostic.
func assistantDefinitionsFromResult(result *compiler.Result, root string) []assistantDefinition {
	if result == nil || result.Manifest == nil {
		return nil
	}
	capabilityRevision := strings.TrimSpace(result.Manifest.ContractRevision)
	definitions := make([]assistantDefinition, 0)
	for _, resource := range result.Manifest.Resources {
		if resource.Kind != "scenery.assistant" || strings.TrimSpace(resource.Address) == "" {
			continue
		}
		implementation, _ := resource.Spec["implementation"].(map[string]any)
		source := assistantString(implementation["source"])
		packagePath := assistantString(implementation["package"])
		lockPath := assistantString(implementation["package_lock"])
		if source == "" || packagePath == "" || lockPath == "" {
			continue
		}
		server := assistantRef(resource.Spec["mcp_server"])
		name := strings.TrimSpace(resource.Name)
		if name == "" {
			name = assistantNameFromAddress(resource.Address)
		}
		runtimeRevision := assistantRuntimeRevisionFor(result, resource.Address)
		required := true
		if value, ok := implementation["required"].(bool); ok {
			required = value
		}
		definition := assistantDefinition{
			Address:            strings.TrimSpace(resource.Address),
			Name:               name,
			SourceRoot:         filepath.Join(root, filepath.FromSlash(source)),
			PackagePath:        filepath.Join(root, filepath.FromSlash(packagePath)),
			PackageLockPath:    filepath.Join(root, filepath.FromSlash(lockPath)),
			MCPServer:          server,
			RuntimeRevision:    runtimeRevision,
			CapabilityRevision: capabilityRevision,
			Required:           required,
		}
		identity, _ := json.Marshal([]any{definition.Address, definition.Name, source, packagePath, lockPath, server, definition.RuntimeRevision, definition.CapabilityRevision, required})
		digest := sha256.Sum256(identity)
		definition.Identity = "sha256:" + hex.EncodeToString(digest[:])
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Address < definitions[j].Address })
	return definitions
}

func assistantRuntimeRevisionFor(result *compiler.Result, address string) string {
	if result != nil {
		if revision := strings.TrimSpace(result.ImplementationRevisions[address]); revision != "" {
			return revision
		}
		keys := make([]string, 0, len(result.ImplementationRevisions))
		for key := range result.ImplementationRevisions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if revision := strings.TrimSpace(result.ImplementationRevisions[key]); revision != "" {
				return revision
			}
		}
	}
	return assistantRuntimeRevision
}

func assistantString(value any) string {
	if stringValue, ok := value.(string); ok {
		return strings.TrimSpace(stringValue)
	}
	return ""
}

func assistantRef(value any) string {
	if ref, ok := value.(map[string]any); ok {
		value = ref["$ref"]
	}
	result := assistantString(value)
	if strings.HasPrefix(result, "mcp_server.") {
		return "app/mcp_server/" + strings.TrimPrefix(result, "mcp_server.")
	}
	return result
}

func assistantApprovalNeverTools(result *compiler.Result, server string) []string {
	if result == nil || !result.Valid() {
		return nil
	}
	expanded, err := result.ManifestForView("expanded")
	if err != nil {
		return nil
	}
	manifest, err := mcpprojection.ProjectManifest(expanded, result.WorkspaceRevision, server)
	if err != nil {
		// A missing or invalid projection must fail closed. The compiler and app
		// gateway report the underlying contract error on their normal surfaces.
		return nil
	}
	return eve.ApprovalNeverTools(manifest)
}

func assistantNameFromAddress(address string) string {
	address = strings.TrimSuffix(strings.TrimSpace(address), "/")
	if index := strings.LastIndexByte(address, '/'); index >= 0 {
		return address[index+1:]
	}
	return address
}

// Prepare applies a compiler snapshot and prepares private helper state before
// the app child starts. In production this includes the managed Node home,
// generated overlay, and dependency cache; the app-owned MCP gateway can then
// bind the preselected loopback address while helper probes are still pending.
func (s *assistantSupervisor) Prepare(ctx context.Context, result *compiler.Result) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = s.ctx
	}
	definitions := assistantDefinitionsFromResult(result, s.config.Root)
	byAddress := make(map[string]assistantDefinition, len(definitions))
	for _, definition := range definitions {
		byAddress[definition.Address] = definition
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.contract = result
	stale := make([]*assistantProcessInstance, 0)
	staleRoots := make([]string, 0)
	for address, instance := range s.instances {
		if _, ok := byAddress[address]; !ok {
			delete(s.instances, address)
			delete(s.prepared, address)
			if root := s.ownedRoots[address]; root != "" {
				staleRoots = append(staleRoots, root)
				delete(s.ownedRoots, address)
			}
			delete(s.statuses, address)
			stale = append(stale, instance)
		}
	}
	for address := range s.prepared {
		if _, ok := byAddress[address]; ok {
			continue
		}
		delete(s.prepared, address)
		if root := s.ownedRoots[address]; root != "" {
			staleRoots = append(staleRoots, root)
			delete(s.ownedRoots, address)
		}
		delete(s.statuses, address)
	}
	s.mu.Unlock()
	if len(stale) > 0 || len(definitions) == 0 {
		s.publishStatuses()
	}
	for _, instance := range stale {
		s.stopInstance(instance)
	}
	for _, root := range staleRoots {
		_ = os.RemoveAll(root)
	}
	for _, definition := range definitions {
		s.mu.Lock()
		current := s.instances[definition.Address]
		prepared, hasPrepared := s.prepared[definition.Address]
		unchanged := current != nil && current.definition.Identity == definition.Identity && current.process != nil && !current.stopping
		preparedUnchanged := hasPrepared && prepared.definition.Identity == definition.Identity
		s.mu.Unlock()
		if unchanged || preparedUnchanged {
			s.emitStep(ctx, definition, "assistant.prepare", time.Now(), "hit", "prepared_identity_unchanged", nil)
			continue
		}
		if current != nil {
			s.mu.Lock()
			delete(s.prepared, definition.Address)
			s.mu.Unlock()
			s.stopInstance(current)
			s.mu.Lock()
			if s.instances[definition.Address] == current {
				delete(s.instances, definition.Address)
			}
			s.mu.Unlock()
		}
		if current == nil {
			s.mu.Lock()
			// A crashed or unavailable helper can leave a prepared overlay
			// without a live process. If the graph identity changed, discard
			// that old manager-owned tree before installing the new slot; the
			// normal live-process branch above removes it through stopInstance.
			oldRoot := s.ownedRoots[definition.Address]
			delete(s.ownedRoots, definition.Address)
			delete(s.prepared, definition.Address)
			s.mu.Unlock()
			if oldRoot != "" {
				_ = os.RemoveAll(oldRoot)
			}
		}
		// Preparation records all setup failures as unavailable but does not
		// abort the unrelated Go app.
		_ = s.prepareDefinition(ctx, definition)
	}
	return nil
}

func (s *assistantSupervisor) prepareDefinition(ctx context.Context, definition assistantDefinition) error {
	if ctx == nil {
		ctx = s.ctx
	}
	state := AssistantStatusRecord{
		Address: definition.Address, Name: definition.Name, SourceID: "assistant:" + definition.Name,
		State: string(assistantruntime.StateStarting), Required: definition.Required,
		RuntimeRevision: definition.RuntimeRevision, CapabilityRevision: definition.CapabilityRevision,
		LogSource: "assistant:" + definition.Name,
	}
	s.mu.Lock()
	state.RestartCount = s.restarts[definition.Address]
	s.statuses[definition.Address] = state
	s.mu.Unlock()
	s.publishStatuses()
	s.emit(ctx, definition, "info", "assistant helper preparing", map[string]any{"address": definition.Address})
	if err := os.MkdirAll(s.config.StateRoot, 0o700); err != nil {
		return s.failDefinition(ctx, definition, fmt.Errorf("assistant state root: %w", err))
	}
	controlURL, err := assistantControlURLAllocator()
	if err != nil {
		return s.failDefinition(ctx, definition, err)
	}
	controlToken, err := randomToken()
	if err != nil {
		return s.failDefinition(ctx, definition, err)
	}
	mcpListenAddress, err := assistantMCPListenAddressAllocator()
	if err != nil {
		return s.failDefinition(ctx, definition, err)
	}
	bridgeSecret, err := randomSecret()
	if err != nil {
		return s.failDefinition(ctx, definition, err)
	}
	prepared := assistantPreparedRuntime{definition: definition, approvalNeverTools: assistantApprovalNeverTools(s.contract, definition.MCPServer), controlURL: controlURL, controlToken: controlToken, mcpListenAddress: mcpListenAddress, mcpURL: "http://" + mcpListenAddress, bridgeSecret: bridgeSecret}
	// Publish the stable private descriptor before any managed-Node or overlay
	// work. If preparation fails, the app child can still bind its MCP gateway
	// and expose the assistant as unavailable while a later helper retry repairs
	// the overlay.
	s.mu.Lock()
	s.prepared[definition.Address] = prepared
	state = s.statuses[definition.Address]
	state.ControlAddress = prepared.controlURL
	state.MCPAddress = prepared.mcpURL
	s.statuses[definition.Address] = state
	s.mu.Unlock()
	s.publishStatuses()
	if s.config.UseAppGateway {
		if err := s.prepareOverlay(ctx, &prepared); err != nil {
			return s.failDefinition(ctx, definition, err)
		}
	}
	s.mu.Lock()
	if current, ok := s.prepared[definition.Address]; ok && current.definition.Identity == definition.Identity {
		state = s.statuses[definition.Address]
		state.ControlAddress = current.controlURL
		state.MCPAddress = current.mcpURL
		state.OverlayPath = current.overlay.Root
		s.statuses[definition.Address] = state
	}
	s.mu.Unlock()
	s.publishStatuses()
	return nil
}

func (s *assistantSupervisor) prepareOverlay(ctx context.Context, prepared *assistantPreparedRuntime) error {
	if prepared == nil {
		return errors.New("assistant prepared runtime is nil")
	}
	if prepared.overlay.Root != "" {
		if _, err := os.Stat(prepared.overlay.Root); err == nil {
			return nil
		}
	}
	if err := os.MkdirAll(s.config.StateRoot, 0o700); err != nil {
		return fmt.Errorf("assistant state root: %w", err)
	}
	ownedRoot, err := os.MkdirTemp(s.config.StateRoot, "assistant-"+sanitizeRouteLabel(prepared.definition.Name)+"-")
	if err != nil {
		return fmt.Errorf("assistant overlay: %w", err)
	}
	prepared.ownedRoot = ownedRoot
	s.mu.Lock()
	s.ownedRoots[prepared.definition.Address] = ownedRoot
	s.mu.Unlock()
	nodePath, npmPath, nodeHome, err := s.config.NodeResolver(ctx)
	if err != nil {
		_ = os.RemoveAll(ownedRoot)
		return fmt.Errorf("assistant managed Node: %w", err)
	}
	prepared.nodePath, prepared.npmPath, prepared.nodeHome = nodePath, npmPath, nodeHome
	overlay, err := eve.Materialize(eve.OverlayRequest{
		SourceRoot: prepared.definition.SourceRoot, OverlayRoot: filepath.Join(ownedRoot, "overlay"),
		AssistantAddress: prepared.definition.Address, RuntimeRevision: prepared.definition.RuntimeRevision,
		CapabilityRevision: prepared.definition.CapabilityRevision, ApprovalNeverTools: prepared.approvalNeverTools,
		ControlURL: prepared.controlURL, MCPURL: prepared.mcpURL,
	})
	if err != nil {
		_ = os.RemoveAll(ownedRoot)
		return fmt.Errorf("assistant overlay materialize: %w", err)
	}
	started := time.Now()
	var cache *assistantOverlayCache
	if s.cacheOverlays {
		cache, err = openAssistantOverlayCache(s.config.Root, overlay.Root, nodePath, prepared.mcpURL)
		if err == nil {
			var hit bool
			hit, err = cache.restore(ctx, overlay.Root)
			if hit && err == nil {
				s.emitStep(ctx, prepared.definition, "assistant.dependencies", started, "hit", "verified_private_overlay_copy", nil)
				s.emitStep(ctx, prepared.definition, "assistant.build", time.Now(), "hit", "verified_relocated_build", nil)
				goto preparedOverlay
			}
		}
		if err != nil {
			_ = os.RemoveAll(ownedRoot)
			return fmt.Errorf("assistant prepared cache: %w", err)
		}
	}
	err = s.config.InstallDeps(ctx, overlay.Root, npmPath, nodeHome)
	s.emitStep(ctx, prepared.definition, "assistant.dependencies", started, "miss", "new_private_overlay", err)
	if err != nil {
		_ = os.RemoveAll(ownedRoot)
		return err
	}
	started = time.Now()
	err = s.config.BuildOverlay(ctx, overlay.Root, nodePath, nodeHome, prepared.mcpURL)
	s.emitStep(ctx, prepared.definition, "assistant.build", started, "miss", "new_private_overlay", err)
	if err != nil {
		_ = os.RemoveAll(ownedRoot)
		return fmt.Errorf("assistant helper build: %w", err)
	}
	if cache != nil {
		started = time.Now()
		err = cache.publish(ctx, overlay.Root)
		s.emitStep(ctx, prepared.definition, "assistant.cache_publish", started, "miss", "prepared_output_snapshot", err)
		if err != nil {
			_ = os.RemoveAll(ownedRoot)
			return fmt.Errorf("assistant prepared cache: %w", err)
		}
	}
preparedOverlay:
	prepared.overlay = overlay
	s.mu.Lock()
	if current, ok := s.prepared[prepared.definition.Address]; ok && current.definition.Identity == prepared.definition.Identity {
		current.nodePath, current.npmPath, current.nodeHome = prepared.nodePath, prepared.npmPath, prepared.nodeHome
		current.overlay, current.ownedRoot = prepared.overlay, prepared.ownedRoot
		s.prepared[prepared.definition.Address] = current
	}
	s.mu.Unlock()
	return nil
}

func resolveAssistantManagedNode(ctx context.Context, root string) (string, string, string, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return "", "", "", fmt.Errorf("assistant managed Node manifest: %w", err)
	}
	store, err := toolchain.NewStore(toolchain.DefaultStoreDir(root), manifest)
	if err != nil {
		return "", "", "", fmt.Errorf("assistant managed Node store: %w", err)
	}
	store.RootDir = root
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	store.Platform = toolchain.CurrentPlatform()
	status, err := store.Sync(ctx, toolchain.Options{RootDir: root, Platform: store.Platform, Tool: "node", Strict: true})
	if err != nil {
		return "", "", "", fmt.Errorf("assistant managed Node sync: %w", err)
	}
	for _, artifact := range status.Artifacts {
		if artifact.Name != "node" {
			continue
		}
		if artifact.Status != "installed" || artifact.ManagedPath == "" || artifact.HomePath == "" {
			return "", "", "", fmt.Errorf("assistant managed Node is unavailable: %s", artifact.Message)
		}
		npm := filepath.Join(artifact.HomePath, "bin", "npm")
		if _, err := os.Stat(npm); err != nil {
			return "", "", "", fmt.Errorf("assistant managed npm is unavailable: %w", err)
		}
		return artifact.ManagedPath, npm, artifact.HomePath, nil
	}
	return "", "", "", errors.New("assistant managed Node artifact is unavailable")
}

func installAssistantDependencies(ctx context.Context, overlay, npm, home string) error {
	command := execCommandContext(ctx, npm, "ci", "--ignore-scripts", "--no-audit", "--no-fund")
	command.Dir = overlay
	command.Env = []string{"PATH=" + filepath.Join(home, "bin"), "HOME=" + filepath.Join(overlay, ".home"), "NPM_CONFIG_UPDATE_NOTIFIER=false", "NPM_CONFIG_FUND=false", "NPM_CONFIG_AUDIT=false"}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("assistant dependency install: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// buildAssistantOverlay compiles the pinned provider workspace before the
// generated bootstrap invokes its start command. The managed Node executable
// is used directly and the build receives only the private loopback MCP URL
// plus an isolated HOME/PATH; no application environment is inherited.
func buildAssistantOverlay(ctx context.Context, overlay, nodePath, nodeHome, mcpURL string) error {
	eveCLI := filepath.Join(overlay, "node_modules", "eve", "bin", "eve.js")
	for label, path := range map[string]string{"managed Node": nodePath, "Eve CLI": eveCLI} {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("%s is unavailable: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular non-symlink file", label)
		}
	}
	home := filepath.Join(overlay, ".home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	command := execCommandContext(ctx, nodePath, eveCLI, "build", "--skip-sandbox-prewarm")
	command.Dir = overlay
	command.Env = []string{
		"PATH=" + filepath.Join(nodeHome, "bin"),
		"HOME=" + home,
		"SCENERY_MCP_URL=" + mcpURL,
		"NPM_CONFIG_UPDATE_NOTIFIER=false",
	}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}
