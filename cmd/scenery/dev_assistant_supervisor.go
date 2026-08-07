package main

// Development assistant supervision lives in the CLI process.  The helper is
// deliberately treated like any other managed child: authored assistant
// files are copied into a private overlay, generated adapter files are added
// there, and only the pinned Node executable is used to start the provider
// runtime.  Nothing in this file exposes Eve (or another provider) through the
// public status values.

import (
	"context"
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
	"sort"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/mcpprojection"
	"scenery.sh/internal/toolchain"
	"scenery.sh/runtime"
)

const (
	assistantRuntimeRevision = "runtime-1"
	assistantStartupTimeout  = 30 * time.Second
	assistantProbeInterval   = 100 * time.Millisecond
	assistantRestartBase     = 250 * time.Millisecond
	assistantRestartMax      = 10 * time.Second
	assistantRestartWindow   = time.Minute
	assistantRestartLimit    = 5
)

// AssistantStatusRecord is the provider-neutral live status consumed by
// inspect/status clients.  Implementation details are intentionally omitted;
// developer-only inspection can layer those values over this record.
type AssistantStatusRecord struct {
	Address                  string    `json:"address"`
	Name                     string    `json:"name"`
	SourceID                 string    `json:"source_id"`
	State                    string    `json:"state"`
	Required                 bool      `json:"required"`
	Ready                    bool      `json:"ready"`
	PID                      int       `json:"pid,omitempty"`
	ControlAddress           string    `json:"control_address,omitempty"`
	MCPAddress               string    `json:"mcp_address,omitempty"`
	RuntimeRevision          string    `json:"runtime_revision"`
	ActualRuntimeRevision    string    `json:"actual_runtime_revision,omitempty"`
	CapabilityRevision       string    `json:"capability_revision"`
	ActualCapabilityRevision string    `json:"actual_capability_revision,omitempty"`
	OverlayPath              string    `json:"overlay_path,omitempty"`
	RestartCount             int       `json:"restart_count"`
	LastFailure              string    `json:"last_failure,omitempty"`
	LastFailureAt            time.Time `json:"last_failure_at,omitempty"`
	LogSource                string    `json:"log_source"`
}

// assistantDefinition is extracted from the immutable compiler graph.  The
// source tree, package files, and MCP server reference are all authored
// values; the derived Identity binds restart decisions to the exact graph.
type assistantDefinition struct {
	Address            string
	Name               string
	SourceRoot         string
	PackagePath        string
	PackageLockPath    string
	MCPServer          string
	RuntimeRevision    string
	CapabilityRevision string
	Required           bool
	Identity           string
}

type assistantGateway interface {
	URL() string
	Close() error
}

type assistantGatewayRequest struct {
	Definition assistantDefinition
	Contract   *compiler.Result
	Secret     []byte
}

type assistantGatewayFactory func(context.Context, assistantGatewayRequest) (assistantGateway, error)
type assistantProcessFactory func(context.Context, devProcessStartRequest) (*devManagedProcess, error)

type assistantSupervisorConfig struct {
	Root      string
	StateRoot string
	// ProviderEnv contains the explicitly allowlisted provider credentials
	// resolved from the selected app environment. Arbitrary app dotenv values
	// must never cross into the assistant helper.
	ProviderEnv []string
	// UseAppGateway selects the production path: the generated app child owns
	// the MCP listener and dispatch registrations. Tests may leave it false to
	// use an injected parent gateway factory.
	UseAppGateway bool

	// GatewayFactory is injectable for deterministic lifecycle tests. Production
	// supervision uses the generated app-child gateway instead.
	GatewayFactory assistantGatewayFactory
	ProcessFactory assistantProcessFactory
	NodeResolver   func(context.Context) (nodePath, npmPath, nodeHome string, err error)
	InstallDeps    func(context.Context, string, string, string) error
	BuildOverlay   func(context.Context, string, string, string, string) error

	OnProcess func(name string, pid int)
	OnEvent   func(context.Context, devdash.DevSource, string, string, map[string]any)
	OnStatus  func([]AssistantStatusRecord)
	Output    io.Writer
	ErrOutput io.Writer
	Now       func() time.Time
}

type assistantProcessInstance struct {
	definition assistantDefinition
	overlay    eve.Overlay
	gateway    assistantGateway
	process    *devManagedProcess
	client     *assistantruntime.HTTPClient
	controlURL string
	// controlToken authenticates the helper control client and is handed to
	// the app bootstrap through the private runtime descriptor. secret is the
	// separate MCP assertion key used by the helper connection.
	controlToken string
	secret       []byte

	stopping bool
}

type assistantPreparedRuntime struct {
	definition         assistantDefinition
	approvalNeverTools []string
	controlURL         string
	controlToken       string
	mcpListenAddress   string
	mcpURL             string
	bridgeSecret       []byte
	nodePath           string
	npmPath            string
	nodeHome           string
	overlay            eve.Overlay
	ownedRoot          string
}

type assistantSupervisor struct {
	ctx    context.Context
	cancel context.CancelFunc
	config assistantSupervisorConfig

	mu          sync.Mutex
	contract    *compiler.Result
	instances   map[string]*assistantProcessInstance
	prepared    map[string]assistantPreparedRuntime
	statuses    map[string]AssistantStatusRecord
	restartFrom map[string]time.Time
	restarts    map[string]int
	ownedRoots  map[string]string
	closed      bool
}

func newAssistantSupervisor(parent context.Context, config assistantSupervisorConfig) *assistantSupervisor {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if config.ProcessFactory == nil {
		config.ProcessFactory = startDevManagedProcess
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.StateRoot == "" {
		config.StateRoot = filepath.Join(config.Root, ".scenery", "assistants")
	}
	if config.NodeResolver == nil {
		config.NodeResolver = func(ctx context.Context) (string, string, string, error) {
			return resolveAssistantManagedNode(ctx, config.Root)
		}
	}
	if config.InstallDeps == nil {
		config.InstallDeps = installAssistantDependencies
	}
	if config.BuildOverlay == nil {
		config.BuildOverlay = buildAssistantOverlay
	}
	return &assistantSupervisor{
		ctx:         ctx,
		cancel:      cancel,
		config:      config,
		instances:   map[string]*assistantProcessInstance{},
		prepared:    map[string]assistantPreparedRuntime{},
		statuses:    map[string]AssistantStatusRecord{},
		restartFrom: map[string]time.Time{},
		restarts:    map[string]int{},
		ownedRoots:  map[string]string{},
	}
}

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
	manifest, err := mcpprojection.Project(result, server)
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

// StartPrepared starts every helper whose private state was prepared by
// Prepare. It is called after the app child owns the MCP listeners. A direct
// Reconcile call (used by tests and non-supervisor callers) still gets the
// complete lifecycle by invoking both phases.
func (s *assistantSupervisor) StartPrepared(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	addresses := make([]string, 0, len(s.prepared))
	for address := range s.prepared {
		addresses = append(addresses, address)
	}
	s.mu.Unlock()
	sort.Strings(addresses)
	for _, address := range addresses {
		s.mu.Lock()
		prepared, ok := s.prepared[address]
		instance := s.instances[address]
		s.mu.Unlock()
		if !ok {
			continue
		}
		if instance != nil && instance.process != nil && !instance.stopping && instance.definition.Identity == prepared.definition.Identity {
			continue
		}
		if err := s.startPreparedDefinition(ctx, prepared); err != nil {
			// The helper failure is represented in status; keep starting other
			// assistants and leave the app process alive.
			s.scheduleRestart(prepared.definition)
			continue
		}
	}
	return nil
}

// Reconcile preserves the original one-call lifecycle for tests and callers
// that do not own an app child. The dev supervisor uses the two phases above.
func (s *assistantSupervisor) Reconcile(ctx context.Context, result *compiler.Result) error {
	if err := s.Prepare(ctx, result); err != nil {
		return err
	}
	return s.StartPrepared(ctx)
}

// HandleChanges applies an independent assistant watch lane without asking
// the Go build pipeline to run.  The caller has already classified paths.
func (s *assistantSupervisor) HandleChanges(ctx context.Context, paths []string) {
	if s == nil || len(paths) == 0 {
		return
	}
	addresses := map[string]bool{}
	s.mu.Lock()
	definitions := make(map[string]assistantDefinition, len(s.instances)+len(s.prepared))
	for address, prepared := range s.prepared {
		definitions[address] = prepared.definition
	}
	for address, instance := range s.instances {
		definitions[address] = instance.definition
	}
	s.mu.Unlock()
	for _, path := range paths {
		for address, definition := range definitions {
			if assistantPathWithin(definition.SourceRoot, filepath.Join(s.config.Root, filepath.FromSlash(path))) {
				addresses[address] = true
			}
		}
	}
	for address := range addresses {
		s.mu.Lock()
		instance := s.instances[address]
		definition := definitions[address]
		s.mu.Unlock()
		if instance != nil {
			s.stopInstance(instance)
			s.mu.Lock()
			if s.instances[address] == instance {
				delete(s.instances, address)
			}
			s.mu.Unlock()
		}
		s.invalidatePreparedOverlay(address)
		_ = s.startDefinition(ctx, definition)
	}
}

// invalidatePreparedOverlay forces a helper-only watch restart to copy the
// latest authored tree and refresh its dependency cache while retaining the
// stable control/MCP descriptor slot consumed by the app child.
func (s *assistantSupervisor) invalidatePreparedOverlay(address string) {
	s.mu.Lock()
	prepared, ok := s.prepared[address]
	if !ok {
		s.mu.Unlock()
		return
	}
	root := s.ownedRoots[address]
	delete(s.ownedRoots, address)
	prepared.overlay = eve.Overlay{}
	prepared.ownedRoot = ""
	s.prepared[address] = prepared
	s.mu.Unlock()
	if root != "" {
		_ = os.RemoveAll(root)
	}
}

func assistantPathWithin(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
	if err := s.config.InstallDeps(ctx, overlay.Root, npmPath, nodeHome); err != nil {
		_ = os.RemoveAll(ownedRoot)
		return err
	}
	if err := s.config.BuildOverlay(ctx, overlay.Root, nodePath, nodeHome, prepared.mcpURL); err != nil {
		_ = os.RemoveAll(ownedRoot)
		return fmt.Errorf("assistant helper build: %w", err)
	}
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

func (s *assistantSupervisor) startDefinition(ctx context.Context, definition assistantDefinition) error {
	s.mu.Lock()
	prepared, ok := s.prepared[definition.Address]
	s.mu.Unlock()
	if !ok || prepared.definition.Identity != definition.Identity {
		if err := s.prepareDefinition(ctx, definition); err != nil {
			return err
		}
		s.mu.Lock()
		prepared = s.prepared[definition.Address]
		s.mu.Unlock()
	}
	err := s.startPreparedDefinition(ctx, prepared)
	if err != nil {
		s.scheduleRestart(definition)
	}
	return err
}

func (s *assistantSupervisor) startPreparedDefinition(ctx context.Context, prepared assistantPreparedRuntime) error {
	if ctx == nil {
		ctx = s.ctx
	}
	definition := prepared.definition
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
	s.emit(ctx, definition, "info", "assistant helper starting", map[string]any{"address": definition.Address})

	var gateway assistantGateway
	nodePath, npmPath, nodeHome := prepared.nodePath, prepared.npmPath, prepared.nodeHome
	overlay := prepared.overlay
	if !s.config.UseAppGateway {
		if s.config.GatewayFactory == nil {
			return s.failDefinition(ctx, definition, errors.New("assistant private MCP gateway factory is unavailable"))
		}
		if err := os.MkdirAll(s.config.StateRoot, 0o700); err != nil {
			return s.failDefinition(ctx, definition, fmt.Errorf("assistant state root: %w", err))
		}
		ownedRoot, err := os.MkdirTemp(s.config.StateRoot, "assistant-"+sanitizeRouteLabel(definition.Name)+"-")
		if err != nil {
			return s.failDefinition(ctx, definition, fmt.Errorf("assistant overlay: %w", err))
		}
		overlayRoot := filepath.Join(ownedRoot, "overlay")
		s.mu.Lock()
		s.ownedRoots[definition.Address] = ownedRoot
		s.mu.Unlock()
		gateway, err = s.config.GatewayFactory(ctx, assistantGatewayRequest{Definition: definition, Contract: s.contract, Secret: prepared.bridgeSecret})
		if err != nil {
			return s.failDefinition(ctx, definition, fmt.Errorf("assistant private MCP gateway: %w", err))
		}
		if gateway == nil || strings.TrimSpace(gateway.URL()) == "" {
			if gateway != nil {
				_ = gateway.Close()
			}
			return s.failDefinition(ctx, definition, errors.New("assistant private MCP gateway returned no URL"))
		}
		mcpURL := gateway.URL()
		prepared.mcpURL = mcpURL
		s.mu.Lock()
		if current, ok := s.prepared[definition.Address]; ok && current.definition.Identity == definition.Identity {
			current.mcpURL = mcpURL
			s.prepared[definition.Address] = current
		}
		s.mu.Unlock()
		if nodePath, npmPath, nodeHome, err = s.config.NodeResolver(ctx); err != nil {
			_ = gateway.Close()
			return s.failDefinition(ctx, definition, err)
		}
		s.mu.Lock()
		if current, ok := s.prepared[definition.Address]; ok && current.definition.Identity == definition.Identity {
			current.nodePath, current.npmPath, current.nodeHome = nodePath, npmPath, nodeHome
			s.prepared[definition.Address] = current
		}
		s.mu.Unlock()
		overlay, err = eve.Materialize(eve.OverlayRequest{SourceRoot: definition.SourceRoot, OverlayRoot: overlayRoot, AssistantAddress: definition.Address, RuntimeRevision: definition.RuntimeRevision, CapabilityRevision: definition.CapabilityRevision, ApprovalNeverTools: prepared.approvalNeverTools, ControlURL: prepared.controlURL, MCPURL: mcpURL})
		if err != nil {
			_ = gateway.Close()
			return s.failDefinition(ctx, definition, fmt.Errorf("assistant overlay materialize: %w", err))
		}
		if err := s.config.InstallDeps(ctx, overlay.Root, npmPath, nodeHome); err != nil {
			_ = gateway.Close()
			return s.failDefinition(ctx, definition, err)
		}
		if err := s.config.BuildOverlay(ctx, overlay.Root, nodePath, nodeHome, mcpURL); err != nil {
			_ = gateway.Close()
			return s.failDefinition(ctx, definition, fmt.Errorf("assistant helper build: %w", err))
		}
	} else {
		if err := s.prepareOverlay(ctx, &prepared); err != nil {
			return s.failDefinition(ctx, definition, err)
		}
		overlay = prepared.overlay
		nodePath, npmPath, nodeHome = prepared.nodePath, prepared.npmPath, prepared.nodeHome
	}
	if overlay.Root == "" || overlay.BootstrapPath == "" || nodePath == "" {
		if gateway != nil {
			_ = gateway.Close()
		}
		return s.failDefinition(ctx, definition, errors.New("assistant helper overlay is incomplete"))
	}
	helperValues := append([]string(nil), s.config.ProviderEnv...)
	helperValues = append(helperValues,
		"SCENERY_ASSISTANT_ID="+definition.Address,
		"SCENERY_ASSISTANT_CONTROL_TOKEN="+prepared.controlToken,
		"SCENERY_ASSISTANT_CONTROL_ADDR="+prepared.controlURL,
		"SCENERY_MCP_URL="+prepared.mcpURL,
		"SCENERY_MCP_BRIDGE_SECRET="+string(prepared.bridgeSecret),
		"SCENERY_CAPABILITY_REVISION="+definition.CapabilityRevision,
		"SCENERY_RUNTIME_REVISION="+definition.RuntimeRevision,
	)
	env := assistantHelperEnv(nodeHome, overlay.Root, helperValues...)
	process, err := s.config.ProcessFactory(ctx, devProcessStartRequest{Name: "assistant:" + definition.Name, Kind: "assistant", Role: "assistant-helper", Dir: overlay.Root, Command: nodePath, Args: []string{overlay.BootstrapPath}, Env: env, Stdout: s.config.Output, Stderr: s.config.ErrOutput, TailLines: 120, OnOutput: func(pid int, stream string, data []byte) {
		s.emit(ctx, definition, "info", "assistant helper output", map[string]any{"pid": pid, "stream": stream, "bytes": len(data)})
	}})
	if err != nil {
		if gateway != nil {
			_ = gateway.Close()
		}
		return s.failDefinition(ctx, definition, fmt.Errorf("assistant helper start: %w", err))
	}
	client, err := assistantruntime.NewHTTPClient(assistantruntime.HTTPClientConfig{ControlBase: prepared.controlURL, ControlToken: prepared.controlToken, AssistantAddress: definition.Address, RuntimeRevision: definition.RuntimeRevision, CapabilityRevision: definition.CapabilityRevision, ControlTimeout: assistantStartupTimeout, StreamTimeout: assistantStartupTimeout})
	if err != nil {
		_ = process.Stop(stopTimeout)
		if gateway != nil {
			_ = gateway.Close()
		}
		return s.failDefinition(ctx, definition, fmt.Errorf("assistant helper client: %w", err))
	}
	probeCtx, cancel := context.WithTimeout(ctx, assistantStartupTimeout)
	probeErr := process.WaitReady(probeCtx, devProcessReadyRequest{Timeout: assistantStartupTimeout, Interval: assistantProbeInterval, Probe: func(probe context.Context) error {
		health, err := client.Health(probe)
		if err != nil {
			return err
		}
		if !health.Ready {
			return assistantruntime.ErrUnavailable
		}
		info, err := client.Info(probe)
		if err != nil {
			return err
		}
		if info.RuntimeRevision != definition.RuntimeRevision || info.CapabilityRevision != definition.CapabilityRevision {
			return assistantruntime.ErrRevisionMismatch
		}
		return nil
	}})
	cancel()
	if probeErr != nil {
		_ = client.Close()
		_ = process.Stop(stopTimeout)
		if gateway != nil {
			_ = gateway.Close()
		}
		return s.failDefinition(ctx, definition, fmt.Errorf("assistant helper readiness: %w", probeErr))
	}
	actualRuntimeRevision, actualCapabilityRevision := definition.RuntimeRevision, definition.CapabilityRevision
	if health, healthErr := client.Health(ctx); healthErr == nil {
		actualRuntimeRevision, actualCapabilityRevision = health.RuntimeRevision, health.CapabilityRevision
	}
	instance := &assistantProcessInstance{definition: definition, overlay: overlay, gateway: gateway, process: process, client: client, controlURL: prepared.controlURL, controlToken: prepared.controlToken, secret: append([]byte(nil), prepared.bridgeSecret...)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.stopInstance(instance)
		return nil
	}
	s.instances[definition.Address] = instance
	status := s.statuses[definition.Address]
	status.State = string(assistantruntime.StateReady)
	status.Ready = true
	status.PID = process.PID
	status.ControlAddress = prepared.controlURL
	status.MCPAddress = prepared.mcpURL
	status.OverlayPath = overlay.Root
	status.ActualRuntimeRevision = actualRuntimeRevision
	status.ActualCapabilityRevision = actualCapabilityRevision
	status.RestartCount = s.restarts[definition.Address]
	s.statuses[definition.Address] = status
	s.mu.Unlock()
	s.publishStatuses()
	if s.config.OnProcess != nil {
		s.config.OnProcess(definition.Name, process.PID)
	}
	s.emit(ctx, definition, "info", "assistant helper ready", map[string]any{"pid": process.PID})
	go s.monitorInstance(instance)
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

// assistantHelperEnv is deliberately an allowlist. Only credentials selected
// by assistantProviderEnv, the managed Node home, a private overlay HOME,
// harmless locale/temp settings, and explicit Scenery bridge values cross into
// a provider helper.
func assistantHelperEnv(nodeHome, overlayRoot string, values ...string) []string {
	home := filepath.Join(overlayRoot, ".home")
	_ = os.MkdirAll(home, 0o700)
	env := []string{"PATH=" + filepath.Join(nodeHome, "bin"), "HOME=" + home}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"} {
		if value, ok := envpolicy.Lookup(key); ok && value != "" {
			env = append(env, key+"="+value)
		}
	}
	return append(env, values...)
}

// assistantProviderEnv projects the smallest supported provider credential
// surface from the selected app environment. Additions require an explicit
// provider integration and focused non-leakage tests.
func assistantProviderEnv(appEnv []string) []string {
	value, _ := lookupEnvValue(appEnv, "OPENAI_API_KEY")
	if value == "" {
		return nil
	}
	return []string{"OPENAI_API_KEY=" + value}
}

// execCommandContext is a seam for tests that need to assert the managed npm
// command without executing a real package manager.  Production uses the
// standard os/exec implementation.
var execCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, command, args...)
}

var assistantControlURLAllocator = allocateAssistantControlURL
var assistantMCPListenAddressAllocator = allocateAssistantMCPListenAddress

func allocateAssistantControlURL() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("assistant control listener: %w", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("assistant control listener close: %w", err)
	}
	return "http://" + addr, nil
}

func allocateAssistantMCPListenAddress() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("assistant MCP listener: %w", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("assistant MCP listener close: %w", err)
	}
	return addr, nil
}

func randomSecret() ([]byte, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	return []byte(token), nil
}

func (s *assistantSupervisor) failDefinition(ctx context.Context, definition assistantDefinition, err error) error {
	if err == nil {
		err = errors.New("assistant helper unavailable")
	}
	now := s.config.Now().UTC()
	s.mu.Lock()
	ownedRoot := s.ownedRoots[definition.Address]
	delete(s.ownedRoots, definition.Address)
	if prepared, ok := s.prepared[definition.Address]; ok && prepared.definition.Identity == definition.Identity {
		prepared.overlay = eve.Overlay{}
		prepared.ownedRoot = ""
		s.prepared[definition.Address] = prepared
	}
	status := s.statuses[definition.Address]
	status.Address = definition.Address
	status.Name = definition.Name
	status.SourceID = "assistant:" + definition.Name
	status.State = string(assistantruntime.StateUnavailable)
	status.Ready = false
	status.Required = definition.Required
	status.RuntimeRevision = definition.RuntimeRevision
	status.CapabilityRevision = definition.CapabilityRevision
	status.RestartCount = s.restarts[definition.Address]
	failureCode := assistantFailureCode(err)
	status.LastFailure = failureCode
	status.LastFailureAt = now
	status.LogSource = "assistant:" + definition.Name
	s.statuses[definition.Address] = status
	s.mu.Unlock()
	s.publishStatuses()
	if ownedRoot != "" {
		_ = os.RemoveAll(ownedRoot)
	}
	s.emit(ctx, definition, "error", "assistant helper unavailable", map[string]any{"error_code": failureCode, "required": definition.Required})
	return err
}

func assistantFailureCode(err error) string {
	if err == nil {
		return "assistant_helper_unavailable"
	}
	switch {
	case errors.Is(err, assistantruntime.ErrRevisionMismatch):
		return "revision_mismatch"
	case errors.Is(err, assistantruntime.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return "unavailable"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "assistant_helper_unavailable"
	}
}

func (s *assistantSupervisor) monitorInstance(instance *assistantProcessInstance) {
	if instance == nil || instance.process == nil {
		return
	}
	select {
	case <-s.ctx.Done():
		return
	case <-instance.process.done:
	}
	s.mu.Lock()
	if s.closed || instance.stopping || s.instances[instance.definition.Address] != instance {
		s.mu.Unlock()
		return
	}
	delete(s.instances, instance.definition.Address)
	status := s.statuses[instance.definition.Address]
	status.State = string(assistantruntime.StateCrashed)
	status.Ready = false
	status.PID = 0
	status.LastFailure = "assistant_helper_crashed"
	status.LastFailureAt = s.config.Now().UTC()
	status.RestartCount = s.restarts[instance.definition.Address]
	s.statuses[instance.definition.Address] = status
	s.mu.Unlock()
	s.publishStatuses()
	if instance.client != nil {
		_ = instance.client.Close()
	}
	if instance.gateway != nil {
		_ = instance.gateway.Close()
	}
	if s.config.OnProcess != nil {
		s.config.OnProcess(instance.definition.Name, 0)
	}
	s.emit(context.Background(), instance.definition, "error", "assistant helper exited", map[string]any{"pid": instance.process.PID})
	s.scheduleRestart(instance.definition)
}

func (s *assistantSupervisor) scheduleRestart(definition assistantDefinition) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	now := s.config.Now().UTC()
	start := s.restartFrom[definition.Address]
	if start.IsZero() || now.Sub(start) >= assistantRestartWindow {
		start = now
		s.restartFrom[definition.Address] = start
		s.restarts[definition.Address] = 0
	}
	count := s.restarts[definition.Address]
	if count >= assistantRestartLimit {
		s.mu.Unlock()
		s.failDefinition(context.Background(), definition, errors.New("assistant helper restart rate limit exceeded"))
		return
	}
	s.restarts[definition.Address] = count + 1
	delay := assistantRestartBase * time.Duration(1<<minInt(count, 6))
	if delay > assistantRestartMax {
		delay = assistantRestartMax
	}
	s.mu.Unlock()
	s.emit(context.Background(), definition, "warn", "assistant helper restarting", map[string]any{"delay_ms": delay.Milliseconds(), "restart_count": count + 1})
	timer := time.NewTimer(delay)
	go func() {
		defer timer.Stop()
		select {
		case <-s.ctx.Done():
		case <-timer.C:
			_ = s.startDefinition(s.ctx, definition)
		}
	}()
}

func (s *assistantSupervisor) stopInstance(instance *assistantProcessInstance) {
	if instance == nil {
		return
	}
	s.mu.Lock()
	instance.stopping = true
	preservePrepared := false
	if !s.closed {
		if prepared, ok := s.prepared[instance.definition.Address]; ok && prepared.definition.Identity == instance.definition.Identity {
			preservePrepared = true
		}
	}
	s.mu.Unlock()
	if instance.client != nil {
		_ = instance.client.Close()
	}
	if instance.process != nil {
		_ = instance.process.Stop(stopTimeout)
	}
	if instance.gateway != nil {
		_ = instance.gateway.Close()
	}
	if s.config.OnProcess != nil {
		s.config.OnProcess(instance.definition.Name, 0)
	}
	if instance.overlay.Root != "" && !preservePrepared {
		s.mu.Lock()
		ownedRoot := s.ownedRoots[instance.definition.Address]
		delete(s.ownedRoots, instance.definition.Address)
		s.mu.Unlock()
		if ownedRoot == "" {
			ownedRoot = instance.overlay.Root
		}
		_ = os.RemoveAll(ownedRoot)
	}
}

// Close stops assistants before callers tear down the ordinary app process.
// It only removes manager-owned overlay directories.
func (s *assistantSupervisor) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for address := range s.prepared {
		delete(s.prepared, address)
	}
	instances := make([]*assistantProcessInstance, 0, len(s.instances))
	for address, instance := range s.instances {
		delete(s.instances, address)
		instances = append(instances, instance)
	}
	roots := make([]string, 0, len(s.ownedRoots))
	for _, root := range s.ownedRoots {
		roots = append(roots, root)
	}
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	for _, instance := range instances {
		s.stopInstance(instance)
	}
	for _, root := range roots {
		_ = os.RemoveAll(root)
	}
	return nil
}

func (s *assistantSupervisor) Status() []AssistantStatusRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]AssistantStatusRecord, 0, len(s.statuses))
	for _, status := range s.statuses {
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func (s *assistantSupervisor) Contract() *compiler.Result {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contract
}

// RuntimeConfig returns the stable private descriptor slots prepared for the
// current graph. The app child receives these before helpers start, so its MCP
// gateway can bind first and its bootstrap can retry helper probes.
func (s *assistantSupervisor) RuntimeConfig() runtime.AssistantRuntimeConfig {
	if s == nil {
		return runtime.AssistantRuntimeConfig{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	addresses := make([]string, 0, len(s.prepared))
	for address := range s.prepared {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	config := runtime.AssistantRuntimeConfig{Assistants: make([]runtime.AssistantBootstrapDescriptor, 0, len(addresses))}
	for _, address := range addresses {
		prepared := s.prepared[address]
		config.Assistants = append(config.Assistants, runtime.AssistantBootstrapDescriptor{
			AssistantAddress: prepared.definition.Address, ControlAddress: prepared.controlURL,
			ControlToken: prepared.controlToken, MCPListenAddress: prepared.mcpListenAddress,
			MCPBridgeSecret: string(prepared.bridgeSecret), RuntimeRevision: prepared.definition.RuntimeRevision,
			CapabilityRevision: prepared.definition.CapabilityRevision, Required: prepared.definition.Required,
		})
	}
	return config
}

func (s *assistantSupervisor) publishStatuses() {
	if s == nil || s.config.OnStatus == nil {
		return
	}
	s.config.OnStatus(s.Status())
}

func (s *devSupervisor) persistAssistantStatusSnapshot(statuses []AssistantStatusRecord) {
	if s == nil {
		return
	}
	result := (*compiler.Result)(nil)
	if s.assistants != nil {
		result = s.assistants.Contract()
	}
	if result == nil || result.Manifest == nil {
		return
	}
	// Keep the persisted record graph-backed and provider-neutral by default,
	// while retaining live process details under the explicit implementation
	// projection. The CLI drops that member unless --implementation is set.
	_ = writeAssistantLiveStatusSnapshot(s.root, result, statuses)
}

func assistantStringOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func (s *assistantSupervisor) ProcessSnapshot() map[string]localagent.Process {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]localagent.Process)
	for _, instance := range s.instances {
		if instance != nil && instance.process != nil && instance.process.PID > 0 && !instance.stopping {
			result["assistant-"+instance.definition.Name] = localagent.Process{PID: instance.process.PID}
		}
	}
	return result
}

func (s *assistantSupervisor) emit(ctx context.Context, definition assistantDefinition, level, message string, fields map[string]any) {
	if s == nil || s.config.OnEvent == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.config.OnEvent(ctx, devdash.DevSource{ID: "assistant:" + definition.Name, Kind: "assistant", Name: definition.Name, Role: "assistant-helper", Status: level}, level, message, fields)
}
