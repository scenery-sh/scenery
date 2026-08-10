package main

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

func TestAssistantWatchLanesRemainIndependentFromGoBuild(t *testing.T) {
	root := t.TempDir()
	setAssistantImplementationWatch(root, []assistantDefinition{{
		Address: "app/assistant/support", SourceRoot: filepath.Join(root, "assistants", "support"),
	}})
	tests := []struct {
		path string
		want string
	}{
		{"assistants/support/instructions.md", assistantWatchHelperOnly},
		{"assistants/support/skills/search.md", assistantWatchHelperOnly},
		{"assistants/support/tools/local.ts", assistantWatchHelperOnly},
		{"assistants/support/agent/channels/scenery.ts", assistantWatchHelperOnly},
		{"assistants/support/agent/connections/scenery.ts", assistantWatchHelperOnly},
		{"assistants/support/.scenery/runtime-manifest.json", assistantWatchApp},
		{"assistants/support/package.json", assistantWatchDependency},
		{"assistants/support/package-lock.json", assistantWatchDependency},
		{"assistants/support/native.go", assistantWatchApp},
	}
	for _, test := range tests {
		if got := classifyAssistantWatchPath(root, test.path); got != test.want {
			t.Errorf("classifyAssistantWatchPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	assistant, app := splitAssistantWatchPaths(root, []string{
		"assistants/support/instructions.md", "assistants/support/agent/channels/scenery.ts", "assistants/support/.scenery/runtime-manifest.json", "assistants/support/package.json", "assistants/support/native.go", "main.go",
	})
	if len(assistant) != 3 || len(app) != 3 {
		t.Fatalf("split assistant=%v app=%v", assistant, app)
	}
	if got, want := assistant, []string{"assistants/support/instructions.md", "assistants/support/agent/channels/scenery.ts", "assistants/support/package.json"}; !slices.Equal(got, want) {
		t.Fatalf("assistant helper paths = %v, want %v", got, want)
	}
	if got, want := app, []string{"assistants/support/.scenery/runtime-manifest.json", "assistants/support/native.go", "main.go"}; !slices.Equal(got, want) {
		t.Fatalf("app rebuild paths = %v, want %v", got, want)
	}
}

func TestAssistantDefinitionsUseCanonicalGraphRevisions(t *testing.T) {
	root := t.TempDir()
	result := &compiler.Result{WorkspaceRevision: "workspace-1", ImplementationRevisions: map[string]string{"app/assistant/support": "runtime-2"}, Manifest: &graph.Manifest{
		ContractRevision: "capability-2",
		Resources: []graph.Resource{{Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server":     map[string]any{"$ref": "mcp_server.support"},
			"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
		}}},
	}}
	defs := assistantDefinitionsFromResult(result, root)
	if len(defs) != 1 {
		t.Fatalf("definitions = %+v", defs)
	}
	if defs[0].MCPServer != "app/mcp_server/support" || defs[0].RuntimeRevision != "runtime-2" || defs[0].CapabilityRevision != "capability-2" {
		t.Fatalf("definition = %+v", defs[0])
	}
	if defs[0].SourceRoot != filepath.Join(root, "assistants", "support") {
		t.Fatalf("source root = %q", defs[0].SourceRoot)
	}
}

func TestAssistantUnavailableDoesNotAbortReconcile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assistants", "support"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "capability-1", Resources: []graph.Resource{{
		Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server":     "mcp_server.support",
			"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
		},
	}}}}
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: root, StateRoot: filepath.Join(root, "state")})
	if err := supervisor.Reconcile(context.Background(), result); err != nil {
		t.Fatalf("Reconcile returned helper outage: %v", err)
	}
	status := supervisor.Status()
	if len(status) != 1 || status[0].State != "unavailable" || status[0].Ready || !assistantStatusCode(status[0].LastFailure) {
		t.Fatalf("status = %+v", status)
	}
	if err := supervisor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantCloseStopsOwnedInstance(t *testing.T) {
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: t.TempDir()})
	done := make(chan struct{})
	process := &devManagedProcess{PID: 42, done: done, outputDone: make(chan struct{}), Cmd: nil}
	definition := assistantDefinition{Address: "app/assistant/support", Name: "support"}
	supervisor.mu.Lock()
	supervisor.instances[definition.Address] = &assistantProcessInstance{definition: definition, process: process}
	supervisor.mu.Unlock()
	close(done)
	if err := supervisor.Close(); err != nil {
		t.Fatal(err)
	}
	if !process.stoppedForTest() {
		t.Fatal("close did not observe process stop")
	}
}

func TestSessionProcessesReplaceAssistantSnapshot(t *testing.T) {
	assistants := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: t.TempDir()})
	process := &devManagedProcess{PID: 42, done: make(chan struct{}), outputDone: make(chan struct{})}
	close(process.done)
	assistants.mu.Lock()
	assistants.instances["app/assistant/current"] = &assistantProcessInstance{
		definition: assistantDefinition{Address: "app/assistant/current", Name: "current"},
		process:    process,
	}
	assistants.mu.Unlock()
	t.Cleanup(func() { _ = assistants.Close() })

	supervisor := &devSupervisor{assistants: assistants}
	processes := supervisor.sessionProcessesFor(&localagent.Session{Processes: map[string]localagent.Process{
		"assistant-stale": {PID: 41},
		"frontend-app":    {PID: 40},
	}}, "43")

	if _, ok := processes["assistant-stale"]; ok {
		t.Fatalf("stale assistant process was retained: %+v", processes)
	}
	if got := processes["assistant-current"].PID; got != 42 {
		t.Fatalf("current assistant PID = %d, want 42", got)
	}
	if got := processes[localagent.RouteAPI].PID; got != 43 {
		t.Fatalf("API PID = %d, want 43", got)
	}
	if got := processes["frontend-app"].PID; got != 40 {
		t.Fatalf("frontend PID = %d, want 40", got)
	}
}

// stoppedForTest avoids requiring a real exec.Cmd in the ownership test. A
// nil Cmd is already a stopped process from the runner's perspective.
func (p *devManagedProcess) stoppedForTest() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func TestAssistantRestartBackoffIsBounded(t *testing.T) {
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: t.TempDir(), Now: func() time.Time { return time.Unix(100, 0) }})
	definition := assistantDefinition{Address: "app/assistant/support", Name: "support"}
	for i := 0; i < assistantRestartLimit; i++ {
		supervisor.scheduleRestart(definition)
	}
	supervisor.mu.Lock()
	count := supervisor.restarts[definition.Address]
	supervisor.mu.Unlock()
	if count != assistantRestartLimit {
		t.Fatalf("restart count = %d, want %d", count, assistantRestartLimit)
	}
	_ = supervisor.Close()
}

func TestEnsureAssistantTokenKeyIsStableAndPrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	path, err := ensureAssistantTokenKey(root)
	if err != nil {
		t.Fatalf("ensureAssistantTokenKey() = %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(first))) != 64 {
		t.Fatalf("token key file length = %d, want 64 hex characters", len(strings.TrimSpace(string(first))))
	}
	if _, err := hex.DecodeString(strings.TrimSpace(string(first))); err != nil {
		t.Fatalf("token key file is not hex: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token key mode = %o, contains group/world permissions", info.Mode().Perm())
	}
	secondPath, err := ensureAssistantTokenKey(root)
	if err != nil {
		t.Fatalf("second ensureAssistantTokenKey() = %v", err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if path != secondPath || string(first) != string(second) {
		t.Fatalf("token key was not stable: first=%q second=%q", first, second)
	}
}

func TestAssistantHelperEnvUsesAllowlist(t *testing.T) {
	t.Setenv("SCENERY_DATABASE_URL", "postgres://should-not-cross")
	t.Setenv("LANG", "en_US.UTF-8")
	env := assistantHelperEnv("/managed/node", filepath.Join(t.TempDir(), "overlay"), "SCENERY_ASSISTANT_ID=app/assistant/support")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "SCENERY_DATABASE_URL") || strings.Contains(joined, "postgres://should-not-cross") {
		t.Fatalf("helper environment leaked ambient app secret: %v", env)
	}
	if !strings.Contains(joined, "PATH=/managed/node/bin") || !strings.Contains(joined, "HOME=") || !strings.Contains(joined, "SCENERY_ASSISTANT_ID=app/assistant/support") {
		t.Fatalf("helper environment missing managed/private values: %v", env)
	}
}

func TestAssistantProviderEnvAllowsOnlyOpenAIKey(t *testing.T) {
	providerEnv := assistantProviderEnv([]string{
		"OPENAI_API_KEY=test-openai-key",
		"SCENERY_DATABASE_URL=postgres://must-not-cross",
		"GOOGLE_OAUTH_CLIENT_SECRET=must-not-cross",
	})
	if len(providerEnv) != 1 || providerEnv[0] != "OPENAI_API_KEY=test-openai-key" {
		t.Fatalf("assistantProviderEnv() = %v", providerEnv)
	}
	helperEnv := assistantHelperEnv("/managed/node", filepath.Join(t.TempDir(), "overlay"), providerEnv...)
	joined := strings.Join(helperEnv, "\n")
	if !strings.Contains(joined, "OPENAI_API_KEY=test-openai-key") {
		t.Fatalf("helper environment missing OpenAI credential: %v", helperEnv)
	}
	if strings.Contains(joined, "SCENERY_DATABASE_URL") || strings.Contains(joined, "GOOGLE_OAUTH_CLIENT_SECRET") {
		t.Fatalf("helper environment leaked unrelated credentials: %v", helperEnv)
	}
}

func TestAssistantRuntimeConfigUsesControlTokenNotBridgeSecret(t *testing.T) {
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: t.TempDir()})
	definition := assistantDefinition{Address: "app/assistant/support", Name: "support", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"}
	process := &devManagedProcess{PID: 42, done: make(chan struct{}), outputDone: make(chan struct{})}
	close(process.done)
	bridgeSecret := "bridge-secret"
	client, err := assistantruntime.NewHTTPClient(assistantruntime.HTTPClientConfig{ControlBase: "http://127.0.0.1:4101", ControlToken: "control-token", AssistantAddress: definition.Address, RuntimeRevision: definition.RuntimeRevision, CapabilityRevision: definition.CapabilityRevision})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.mu.Lock()
	supervisor.instances[definition.Address] = &assistantProcessInstance{definition: definition, process: process, client: client, controlURL: "http://127.0.0.1:4101", controlToken: "control-token", secret: []byte(bridgeSecret)}
	supervisor.prepared[definition.Address] = assistantPreparedRuntime{definition: definition, controlURL: "http://127.0.0.1:4101", controlToken: "control-token", mcpListenAddress: "127.0.0.1:4102", mcpURL: "http://127.0.0.1:4102", bridgeSecret: []byte(bridgeSecret)}
	supervisor.mu.Unlock()
	config := supervisor.RuntimeConfig()
	if len(config.Assistants) != 1 || config.Assistants[0].ControlToken != "control-token" {
		t.Fatalf("runtime config = %+v, control token was not preserved", config)
	}
	if config.Assistants[0].ControlToken == bridgeSecret {
		t.Fatal("runtime config reused MCP bridge secret as control token")
	}
	_ = supervisor.Close()
}

func TestAssistantPreparedRuntimeSlotsStayStableAcrossReconcile(t *testing.T) {
	root := t.TempDir()
	result := &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "capability-1", Resources: []graph.Resource{{
		Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server":     "mcp_server.support",
			"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
		},
	}}}}
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: root, StateRoot: filepath.Join(root, "state")})
	if err := supervisor.Prepare(context.Background(), result); err != nil {
		t.Fatalf("first Prepare() = %v", err)
	}
	first := supervisor.RuntimeConfig()
	if err := supervisor.Prepare(context.Background(), result); err != nil {
		t.Fatalf("second Prepare() = %v", err)
	}
	second := supervisor.RuntimeConfig()
	if len(first.Assistants) != 1 || len(second.Assistants) != 1 {
		t.Fatalf("runtime configs = %#v / %#v", first, second)
	}
	if first.Assistants[0].ControlAddress != second.Assistants[0].ControlAddress || first.Assistants[0].ControlToken != second.Assistants[0].ControlToken || first.Assistants[0].MCPListenAddress != second.Assistants[0].MCPListenAddress || first.Assistants[0].MCPBridgeSecret != second.Assistants[0].MCPBridgeSecret {
		t.Fatalf("prepared runtime slot rotated across unchanged reconcile: %#v / %#v", first.Assistants[0], second.Assistants[0])
	}
	_ = supervisor.Close()
}

func TestAssistantPrepareRemovesOrphanedOverlayOnRevisionChange(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	oldRoot := filepath.Join(stateRoot, "old-overlay")
	if err := os.MkdirAll(oldRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	address := "app/assistant/support"
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: root, StateRoot: stateRoot})
	oldDefinition := assistantDefinition{Address: address, Name: "support", Identity: "old", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"}
	supervisor.mu.Lock()
	supervisor.prepared[address] = assistantPreparedRuntime{definition: oldDefinition, ownedRoot: oldRoot}
	supervisor.ownedRoots[address] = oldRoot
	supervisor.mu.Unlock()
	result := &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "capability-2", Resources: []graph.Resource{{
		Address: address, Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server":     "mcp_server.support",
			"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
		},
	}}}}
	if err := supervisor.Prepare(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old overlay still exists after revision change: err=%v", err)
	}
	_ = supervisor.Close()
}

func TestAssistantStartPreparedSkipsLiveInstance(t *testing.T) {
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: t.TempDir(), ProcessFactory: func(context.Context, devProcessStartRequest) (*devManagedProcess, error) {
		t.Fatal("StartPrepared attempted to duplicate a live helper")
		return nil, nil
	}})
	definition := assistantDefinition{Address: "app/assistant/support", Name: "support", Identity: "identity", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"}
	process := &devManagedProcess{PID: 42, done: make(chan struct{}), outputDone: make(chan struct{})}
	close(process.done)
	client, err := assistantruntime.NewHTTPClient(assistantruntime.HTTPClientConfig{ControlBase: "http://127.0.0.1:4101", ControlToken: "control-token", AssistantAddress: definition.Address, RuntimeRevision: definition.RuntimeRevision, CapabilityRevision: definition.CapabilityRevision})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.mu.Lock()
	supervisor.prepared[definition.Address] = assistantPreparedRuntime{definition: definition, controlURL: "http://127.0.0.1:4101", controlToken: "control-token", mcpListenAddress: "127.0.0.1:4102", mcpURL: "http://127.0.0.1:4102", bridgeSecret: []byte(strings.Repeat("b", 32))}
	supervisor.instances[definition.Address] = &assistantProcessInstance{definition: definition, process: process, client: client}
	supervisor.mu.Unlock()
	if err := supervisor.StartPrepared(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantPreparePublishesDescriptorBeforeHelperStart(t *testing.T) {
	root := t.TempDir()
	started := false
	result := &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "capability-1", Resources: []graph.Resource{{
		Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server":     "mcp_server.support",
			"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
		},
	}}}}
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{Root: root, StateRoot: filepath.Join(root, "state"), ProcessFactory: func(context.Context, devProcessStartRequest) (*devManagedProcess, error) {
		started = true
		return nil, errors.New("test process should not start during Prepare")
	}})
	if err := supervisor.Prepare(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	config := supervisor.RuntimeConfig()
	if started || len(config.Assistants) != 1 || config.Assistants[0].MCPListenAddress == "" || config.Assistants[0].ControlToken == "" {
		t.Fatalf("prepare started helper or omitted descriptor: started=%v config=%#v", started, config)
	}
	_ = supervisor.Close()
}

func TestAssistantPrepareBuildsOverlayBeforeHelperStart(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "assistants", "support")
	if err := os.MkdirAll(filepath.Join(source, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"package.json":      "{}\n",
		"package-lock.json": "{}\n",
		"index.ts":          "export {};\n",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "capability-1", Resources: []graph.Resource{{
		Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server": "mcp_server.support",
			"implementation": map[string]any{
				"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json",
			},
		},
	}}}}
	installed, built := false, false
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{
		Root: root, StateRoot: filepath.Join(root, "state"), UseAppGateway: true,
		NodeResolver: func(context.Context) (string, string, string, error) {
			return "/managed/node", "/managed/npm", "/managed/home", nil
		},
		InstallDeps: func(_ context.Context, overlay, _, _ string) error {
			installed = true
			if _, err := os.Stat(filepath.Join(overlay, ".scenery", "bootstrap.mjs")); err != nil {
				t.Fatalf("bootstrap was not materialized before install: %v", err)
			}
			return nil
		},
		BuildOverlay: func(_ context.Context, overlay, node, home, mcpURL string) error {
			if !installed || node != "/managed/node" || home != "/managed/home" || !strings.HasPrefix(mcpURL, "http://127.0.0.1:") {
				t.Fatalf("build inputs installed=%v node=%q home=%q mcp=%q", installed, node, home, mcpURL)
			}
			if _, err := os.Stat(filepath.Join(overlay, ".scenery", "bootstrap.mjs")); err != nil {
				t.Fatalf("bootstrap disappeared before build: %v", err)
			}
			built = true
			return nil
		},
	})
	if err := supervisor.Prepare(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Fatal("assistant overlay was not built during Prepare")
	}
	_ = supervisor.Close()
}

func TestAssistantPrepareKeepsDescriptorWhenOverlayPreparationFails(t *testing.T) {
	root := t.TempDir()
	result := &compiler.Result{Manifest: &graph.Manifest{ContractRevision: "capability-1", Resources: []graph.Resource{{
		Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
			"mcp_server":     "mcp_server.support",
			"implementation": map[string]any{"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json"},
		},
	}}}}
	supervisor := newAssistantSupervisor(context.Background(), assistantSupervisorConfig{
		Root: root, StateRoot: filepath.Join(root, "state"), UseAppGateway: true,
		NodeResolver: func(context.Context) (string, string, string, error) {
			return "", "", "", errors.New("managed node unavailable")
		},
	})
	if err := supervisor.Prepare(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	config := supervisor.RuntimeConfig()
	if len(config.Assistants) != 1 || config.Assistants[0].MCPListenAddress == "" || config.Assistants[0].MCPBridgeSecret == "" {
		t.Fatalf("descriptor was lost after overlay failure: %#v", config)
	}
	status := supervisor.Status()
	if len(status) != 1 || status[0].State != string(assistantruntime.StateUnavailable) {
		t.Fatalf("status = %#v", status)
	}
	_ = supervisor.Close()
}
