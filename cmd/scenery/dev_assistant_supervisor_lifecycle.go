package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime"
)

// assistantHelperEnv is deliberately an allowlist. Only credentials selected
// by assistantProviderEnv, the managed Node home, a private overlay HOME,
// harmless locale/temp settings, and explicit Scenery bridge values cross into
// a provider helper.
func assistantHelperEnv(nodeHome, overlayRoot string, values ...string) []string {
	home := filepath.Join(overlayRoot, ".home")
	_ = os.MkdirAll(home, 0o700)
	nodeBin := filepath.Join(nodeHome, "bin")
	path := nodeBin
	if dir := hostCodexBinDir(); dir != "" && dir != nodeBin {
		path = nodeBin + string(os.PathListSeparator) + dir
	}
	env := []string{"PATH=" + path, "HOME=" + home}
	if !envHasKey(values, "CODEX_HOME") {
		if dest := hostCodexHome(); dest != "" {
			env = append(env, "CODEX_HOME="+dest)
		}
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"} {
		if value, ok := envpolicy.Lookup(key); ok && value != "" {
			env = append(env, key+"="+value)
		}
	}
	return append(env, values...)
}

// lookExecutable and userHomeDir are seams so helper-env tests can prove the
// Codex CLI/login projection without depending on the host install.
var lookExecutable = exec.LookPath
var userHomeDir = os.UserHomeDir

func hostCodexBinDir() string {
	path, err := lookExecutable("codex")
	if err != nil || strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Dir(path)
}

func hostCodexHome() string {
	if value, ok := envpolicy.Lookup("CODEX_HOME"); ok && strings.TrimSpace(value) != "" {
		return value
	}
	home, err := userHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func envHasKey(values []string, key string) bool {
	prefix := key + "="
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// assistantProviderEnv projects the smallest supported provider credential
// surface from the selected app environment. Additions require an explicit
// provider integration and focused non-leakage tests.
func assistantProviderEnv(value []byte) []string {
	if len(value) == 0 {
		return nil
	}
	return []string{"OPENAI_API_KEY=" + string(value)}
}

// assistantProviderEnvFromConfig resolves assistant.openai_api_key from the
// environment the runtime runs; an unconfigured key leaves helpers without a
// provider credential.
func assistantProviderEnvFromConfig(ctx context.Context, cfg app.Config, env app.ResolvedEnv) ([]string, error) {
	store, err := devConfigStore(cfg)
	if err != nil || store == nil {
		return nil, err
	}
	document, err := devConfigDocument(store, cfg, env)
	if err != nil {
		return nil, err
	}
	version, ok := document.Secrets[appconfig.AssistantProviderKey]
	if !ok {
		return nil, nil
	}
	backend, err := appconfig.DefaultSecretBackend(store)
	if configSecretBackendOverride != nil {
		backend, err = configSecretBackendOverride(store)
	}
	if err != nil {
		return nil, err
	}
	value, err := backend.Resolve(ctx, env.Name, appconfig.AssistantProviderKey, version)
	if err != nil {
		return nil, err
	}
	return assistantProviderEnv([]byte(strings.TrimSpace(string(value)))), nil
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
	case <-instance.process.Done:
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
	s.reportProcess(instance.definition.Name, 0)
	s.emit(context.Background(), instance.definition, "error", "assistant helper exited", map[string]any{"pid": instance.process.PID})
	s.scheduleRestart(instance.definition)
}

func (s *assistantSupervisor) scheduleRestart(definition assistantDefinition) {
	s.mu.Lock()
	if s.closed || s.instances[definition.Address] != nil {
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
		_ = s.failDefinition(context.Background(), definition, errors.New("assistant helper restart rate limit exceeded"))
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
			s.lifecycle.Lock()
			defer s.lifecycle.Unlock()
			_ = s.startDefinition(s.ctx, definition)
		}
	}()
}

func (s *assistantSupervisor) stopInstance(instance *assistantProcessInstance) error {
	if instance == nil {
		return nil
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
		if err := instance.process.Stop(stopTimeout); err != nil {
			return err
		}
	}
	if instance.gateway != nil {
		_ = instance.gateway.Close()
	}
	s.reportProcess(instance.definition.Name, 0)
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
	return nil
}

// Close stops assistants before callers tear down the ordinary app process.
// It only removes manager-owned overlay directories.
func (s *assistantSupervisor) Close() error {
	if s == nil {
		return nil
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	if s.closed && len(s.instances) == 0 {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for address := range s.prepared {
		delete(s.prepared, address)
	}
	instances := make([]*assistantProcessInstance, 0, len(s.instances))
	for _, instance := range s.instances {
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
	var stopErrors []error
	for _, instance := range instances {
		if err := s.stopInstance(instance); err != nil {
			stopErrors = append(stopErrors, err)
			continue
		}
		s.mu.Lock()
		delete(s.instances, instance.definition.Address)
		s.mu.Unlock()
	}
	if len(stopErrors) != 0 {
		return errors.Join(stopErrors...)
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
		if !prepared.hasDescriptor() {
			continue
		}
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
	s.callbacks.Lock()
	defer s.callbacks.Unlock()
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

func (s *assistantSupervisor) ProcessSnapshot() map[string]localagent.Process {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]localagent.Process)
	for _, instance := range s.instances {
		if instance != nil && instance.process != nil && instance.process.PID > 0 {
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
	s.callbacks.Lock()
	defer s.callbacks.Unlock()
	s.config.OnEvent(ctx, devdash.DevSource{ID: "assistant:" + definition.Name, Kind: "assistant", Name: definition.Name, Role: "assistant-helper", Status: level}, level, message, fields)
}

// emitStep publishes one preparation step. A failed step names its cause and
// the private record that keeps it, with the provider's own output, after the
// failed overlay is gone; only the step's event and record change, not the
// assistant's status or the retry that follows.
func (s *assistantSupervisor) emitStep(ctx context.Context, definition assistantDefinition, name string, started time.Time, cache, reason string, err error) {
	fields := map[string]any{
		"name": name, "assistant": definition.Address, "started_at": started.UTC().Format(time.RFC3339Nano),
		"duration_ms": float64(time.Since(started).Microseconds()) / 1000,
		"cache":       cache, "reason": reason, "ok": err == nil,
	}
	level := "info"
	detail := recordAssistantPreparationStep(s.config.StateRoot, definition, name, err, s.config.Now())
	if err != nil {
		level = "error"
		fields["error"] = assistantStepErrorText(err)
		if detail != "" {
			fields["detail_path"] = detail
		}
	}
	s.emit(ctx, definition, level, "assistant.step", fields)
}
