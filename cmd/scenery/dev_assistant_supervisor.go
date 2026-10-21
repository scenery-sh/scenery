package main

// Development assistant supervision lives in the CLI process.  The helper is
// deliberately treated like any other managed child: authored assistant
// files are copied into a private overlay, generated adapter files are added
// there, and only the pinned Node executable is used to start the provider
// runtime.  Nothing in this file exposes Eve (or another provider) through the
// public status values.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devdash"
)

const (
	assistantStartupTimeout = 30 * time.Second
	assistantProbeInterval  = 100 * time.Millisecond
	assistantRestartBase    = 250 * time.Millisecond
	assistantRestartMax     = 10 * time.Second
	assistantRestartWindow  = time.Minute
	assistantRestartLimit   = 5
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
	lifecycle     sync.Mutex
	callbacks     sync.Mutex
	output        sync.Mutex
	cacheOverlays bool
	ctx           context.Context
	cancel        context.CancelFunc
	config        assistantSupervisorConfig

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
	cacheOverlays := config.InstallDeps == nil && config.BuildOverlay == nil && config.NodeResolver == nil
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
		cacheOverlays: cacheOverlays,
		ctx:           ctx,
		cancel:        cancel,
		config:        config,
		instances:     map[string]*assistantProcessInstance{},
		prepared:      map[string]assistantPreparedRuntime{},
		statuses:      map[string]AssistantStatusRecord{},
		restartFrom:   map[string]time.Time{},
		restarts:      map[string]int{},
		ownedRoots:    map[string]string{},
	}
}

// Reconcile preserves the original one-call lifecycle for tests and callers
// that do not own an app child. The dev supervisor uses the two phases above.
func (s *assistantSupervisor) Reconcile(ctx context.Context, result *compiler.Result) error {
	if s == nil {
		return nil
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
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
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
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
			if err := s.stopInstance(instance); err != nil {
				s.emit(ctx, definition, "error", "assistant shutdown unconfirmed; replacement refused", map[string]any{"error_code": "assistant_helper_unavailable"})
				continue
			}
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

func (s *assistantSupervisor) startDefinition(ctx context.Context, definition assistantDefinition) error {
	s.mu.Lock()
	prepared, ok := s.prepared[definition.Address]
	current := !s.closed && ok && prepared.definition.Identity == definition.Identity
	s.mu.Unlock()
	if !current {
		return nil
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
	s.mu.Lock()
	closed, existing := s.closed, s.instances[definition.Address]
	s.mu.Unlock()
	if closed {
		return errors.New("assistant supervisor is closed")
	}
	if existing != nil {
		if existing.stopping {
			return errors.New("assistant shutdown is unconfirmed; replacement refused")
		}
		return nil
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
	s.emit(ctx, definition, "info", "assistant helper starting", map[string]any{"address": definition.Address})

	var gateway assistantGateway
	var nodePath, npmPath, nodeHome string
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
		nodePath, nodeHome = prepared.nodePath, prepared.nodeHome
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
	process, err := s.config.ProcessFactory(ctx, devProcessStartRequest{Name: "assistant:" + definition.Name, Kind: "assistant", Role: "assistant-helper", Dir: overlay.Root, Command: nodePath, Args: []string{overlay.BootstrapPath}, Env: env, Stdout: s.outputWriter(s.config.Output), Stderr: s.outputWriter(s.config.ErrOutput), TailLines: 120, OnOutput: func(pid int, stream string, data []byte) {
		s.emit(ctx, definition, "info", "assistant helper output", map[string]any{"pid": pid, "stream": stream, "bytes": len(data)})
	}})
	if err != nil {
		if gateway != nil {
			_ = gateway.Close()
		}
		return s.failDefinition(ctx, definition, fmt.Errorf("assistant helper start: %w", err))
	}
	instance := &assistantProcessInstance{definition: definition, overlay: overlay, gateway: gateway, process: process, controlURL: prepared.controlURL, controlToken: prepared.controlToken, secret: append([]byte(nil), prepared.bridgeSecret...)}
	client, err := assistantruntime.NewHTTPClient(assistantruntime.HTTPClientConfig{ControlBase: prepared.controlURL, ControlToken: prepared.controlToken, AssistantAddress: definition.Address, RuntimeRevision: definition.RuntimeRevision, CapabilityRevision: definition.CapabilityRevision, ControlTimeout: assistantStartupTimeout, StreamTimeout: assistantStartupTimeout})
	if err != nil {
		return s.failStartedDefinition(ctx, instance, fmt.Errorf("assistant helper client: %w", err))
	}
	instance.client = client
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
		return s.failStartedDefinition(ctx, instance, fmt.Errorf("assistant helper readiness: %w", probeErr))
	}
	actualRuntimeRevision, actualCapabilityRevision := definition.RuntimeRevision, definition.CapabilityRevision
	if health, healthErr := client.Health(ctx); healthErr == nil {
		actualRuntimeRevision, actualCapabilityRevision = health.RuntimeRevision, health.CapabilityRevision
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.failStartedDefinition(ctx, instance, errors.New("assistant supervisor closed during startup"))
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
	s.reportProcess(definition.Name, process.PID)
	s.emit(ctx, definition, "info", "assistant helper ready", map[string]any{"pid": process.PID})
	go s.monitorInstance(instance)
	return nil
}

