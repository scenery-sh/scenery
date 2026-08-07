package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"scenery.sh/internal/assistantruntime"
	"scenery.sh/internal/assistanttoken"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpgateway"
)

func TestAssistantRuntimeConfigStrictAndPrivateFieldsNeverSerialize(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "assistants.json")
	encoded := `{"assistants":[{"assistant_address":"app/assistant/a","control_address":"http://127.0.0.1:4101","control_token":"private-token","runtime_revision":"runtime-a","capability_revision":"capability-a"},{"assistant_address":"app/assistant/b","control_address":"http://127.0.0.1:4102","control_token":"private-token-b","runtime_revision":"runtime-b","capability_revision":"capability-b","required":false}]}`
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadAssistantRuntimeConfig(path)
	if err != nil {
		t.Fatalf("LoadAssistantRuntimeConfig() = %v", err)
	}
	if len(config.Assistants) != 2 || !config.Assistants[0].Required || config.Assistants[1].Required {
		t.Fatalf("config assistants = %#v", config.Assistants)
	}
	marshaled, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	output := string(marshaled)
	for _, forbidden := range []string{"control_address", "control_token", "127.0.0.1:4101", "private-token"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("serialized runtime config leaked %q: %s", forbidden, output)
		}
	}
	for _, invalid := range []string{
		`{"assistants":[{"assistant_address":"app/assistant/a","control_address":"http://127.0.0.1:4101","control_token":"x","runtime_revision":"runtime-a","capability_revision":"capability-a","unexpected":true}]}`,
		`{"assistants":[{"assistant_address":"app/assistant/a","assistant_address":"app/assistant/b","control_address":"http://127.0.0.1:4101","control_token":"x","runtime_revision":"runtime-a","capability_revision":"capability-a"}]}`,
		`{"assistants":[]} {"assistants":[]}`,
	} {
		if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAssistantRuntimeConfig(path); err == nil {
			t.Fatalf("LoadAssistantRuntimeConfig(%s) accepted invalid input", invalid)
		}
	}
}

func TestWriteAssistantRuntimeConfigIncludesPrivateHandoffOnlyOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "assistants.json")
	config := AssistantRuntimeConfig{Assistants: []AssistantBootstrapDescriptor{{
		AssistantAddress: "app/assistant/support", ControlAddress: "http://127.0.0.1:4101", ControlToken: "private-token", RuntimeRevision: "runtime-a", CapabilityRevision: "capability-a",
	}}}
	if err := WriteAssistantRuntimeConfig(path, config); err != nil {
		t.Fatalf("WriteAssistantRuntimeConfig() = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("descriptor mode = %o, want 600", info.Mode().Perm())
	}
	loaded, err := LoadAssistantRuntimeConfig(path)
	if err != nil {
		t.Fatalf("LoadAssistantRuntimeConfig() = %v", err)
	}
	if loaded.Assistants[0].ControlAddress != config.Assistants[0].ControlAddress || loaded.Assistants[0].ControlToken != config.Assistants[0].ControlToken {
		t.Fatalf("private handoff changed on disk: %#v", loaded.Assistants[0])
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "private-token") {
		t.Fatalf("private descriptor bytes missing from handoff file: err=%v bytes=%s", err, data)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAssistantRuntimeConfig(path); err == nil {
		t.Fatal("LoadAssistantRuntimeConfig accepted group/world-readable descriptor")
	}
}

func TestAssistantBootstrapSupportsMultipleHelpersAndAtomicReplacement(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/a", "a")
	registerTestAssistant(t, "app/assistant/b", "b")

	helpers := map[string][]*assistantruntime.FakeHelper{}
	factory := func(descriptor AssistantBootstrapDescriptor) (AssistantClient, error) {
		helper := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{
			AssistantAddress:   descriptor.AssistantAddress,
			RuntimeRevision:    descriptor.RuntimeRevision,
			CapabilityRevision: descriptor.CapabilityRevision,
		})
		if err := helper.Start(context.Background()); err != nil {
			return nil, err
		}
		helpers[descriptor.AssistantAddress] = append(helpers[descriptor.AssistantAddress], helper)
		return helper, nil
	}
	bootstrap := NewAssistantBootstrap(AssistantBootstrapOptions{Factory: factory, ProbeTimeout: time.Second})
	config := AssistantRuntimeConfig{Assistants: []AssistantBootstrapDescriptor{
		{AssistantAddress: "app/assistant/a", ControlAddress: "http://127.0.0.1:4101", ControlToken: "token-a", RuntimeRevision: "runtime-a", CapabilityRevision: "capability-a"},
		{AssistantAddress: "app/assistant/b", ControlAddress: "http://127.0.0.1:4102", ControlToken: "token-b", RuntimeRevision: "runtime-b", CapabilityRevision: "capability-b"},
	}}
	if err := bootstrap.Apply(context.Background(), config); err != nil {
		t.Fatalf("initial Apply() = %v", err)
	}
	statuses := bootstrap.Statuses()
	if len(statuses) != 2 || statuses[0].State != string(assistantruntime.StateReady) || statuses[1].State != string(assistantruntime.StateReady) {
		t.Fatalf("initial statuses = %#v", statuses)
	}
	if global.assistantClients["app/assistant/a"] == nil || global.assistantClients["app/assistant/b"] == nil {
		t.Fatal("multiple helper clients were not registered")
	}
	firstA := helpers["app/assistant/a"][0]
	if err := bootstrap.Apply(context.Background(), config); err != nil {
		t.Fatalf("replacement Apply() = %v", err)
	}
	if firstA.State() != assistantruntime.StateStopped {
		t.Fatalf("old helper state = %q, want stopped after replacement", firstA.State())
	}
	if global.assistantClients["app/assistant/a"] == firstA {
		t.Fatal("replacement retained old helper client")
	}
	if err := bootstrap.Apply(context.Background(), AssistantRuntimeConfig{}); err != nil {
		t.Fatalf("cleanup Apply() = %v", err)
	}
	for _, address := range []string{"app/assistant/a", "app/assistant/b"} {
		if global.assistantClients[address] != nil {
			t.Fatalf("client %s survived cleanup", address)
		}
	}
	for _, status := range bootstrap.Statuses() {
		if status.State != string(assistantruntime.StateUnavailable) || status.ErrorCode != "unconfigured" {
			t.Fatalf("cleanup status = %#v", status)
		}
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantBootstrapReturnsPromptlyAndRetriesLateHelper(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	config := AssistantRuntimeConfig{Assistants: []AssistantBootstrapDescriptor{{
		AssistantAddress: "app/assistant/support", ControlAddress: "http://127.0.0.1:4199", ControlToken: "control-token", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1",
	}}}
	path := filepath.Join(t.TempDir(), "assistant-runtime.json")
	if err := WriteAssistantRuntimeConfig(path, config); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AssistantRuntimeConfigEnv, path)
	var available atomic.Bool
	bootstrap, err := bootstrapAssistantRuntime(context.Background(), AssistantBootstrapOptions{
		Factory: func(descriptor AssistantBootstrapDescriptor) (AssistantClient, error) {
			if !available.Load() {
				return assistantruntime.NewUnavailableFakeHelper(assistantruntime.FakeConfig{AssistantAddress: descriptor.AssistantAddress, RuntimeRevision: descriptor.RuntimeRevision, CapabilityRevision: descriptor.CapabilityRevision}), nil
			}
			helper := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{AssistantAddress: descriptor.AssistantAddress, RuntimeRevision: descriptor.RuntimeRevision, CapabilityRevision: descriptor.CapabilityRevision})
			if err := helper.Start(context.Background()); err != nil {
				return nil, err
			}
			return helper, nil
		},
		ProbeTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("bootstrapAssistantRuntime() = %v", err)
	}
	if bootstrap == nil {
		t.Fatal("bootstrapAssistantRuntime returned nil manager")
	}
	initial := bootstrap.Statuses()
	if len(initial) != 1 || initial[0].State != string(assistantruntime.StateUnavailable) {
		t.Fatalf("initial status = %#v", initial)
	}
	available.Store(true)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		statuses := bootstrap.Statuses()
		if len(statuses) == 1 && statuses[0].State == string(assistantruntime.StateReady) {
			returnAfterClose := bootstrap.Close()
			if returnAfterClose != nil {
				t.Fatalf("bootstrap.Close() = %v", returnAfterClose)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = bootstrap.Close()
	t.Fatalf("late helper never became ready: %#v", bootstrap.Statuses())
}

func TestAssistantBootstrapUnavailableAndRevisionMismatchLeavePublicSurfaceAlive(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	config := AssistantRuntimeConfig{Assistants: []AssistantBootstrapDescriptor{{
		AssistantAddress: "app/assistant/support", ControlAddress: "http://127.0.0.1:4101", ControlToken: "token", RuntimeRevision: "runtime-a", CapabilityRevision: "capability-a",
	}}}
	for _, test := range []struct {
		name      string
		factory   AssistantClientFactory
		wantError string
	}{
		{
			name: "unavailable",
			factory: func(AssistantBootstrapDescriptor) (AssistantClient, error) {
				return assistantruntime.NewUnavailableFakeHelper(assistantruntime.FakeConfig{AssistantAddress: "app/assistant/support", RuntimeRevision: "runtime-a", CapabilityRevision: "capability-a"}), nil
			},
			wantError: "unavailable",
		},
		{
			name: "revision mismatch",
			factory: func(AssistantBootstrapDescriptor) (AssistantClient, error) {
				helper := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{AssistantAddress: "app/assistant/support", RuntimeRevision: "runtime-other", CapabilityRevision: "capability-a"})
				if err := helper.Start(context.Background()); err != nil {
					return nil, err
				}
				return helper, nil
			},
			wantError: "revision_mismatch",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bootstrap := NewAssistantBootstrap(AssistantBootstrapOptions{Factory: test.factory, ProbeTimeout: time.Second})
			if err := bootstrap.Apply(context.Background(), config); err != nil {
				t.Fatalf("Apply() = %v", err)
			}
			statuses := bootstrap.Statuses()
			if len(statuses) != 1 || statuses[0].State != string(assistantruntime.StateUnavailable) || statuses[0].ErrorCode != test.wantError {
				t.Fatalf("statuses = %#v, want unavailable/%s", statuses, test.wantError)
			}
			if global.assistantClients["app/assistant/support"] != nil {
				t.Fatal("unavailable helper was registered")
			}
			if len(listEndpoints()) != 5 {
				t.Fatalf("public endpoint count = %d, want five", len(listEndpoints()))
			}
			_ = bootstrap.Close()
		})
	}
}

func TestRegisterAssistantInstallsBootstrapService(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	global.mu.RLock()
	_, ok := global.serviceInitializers[assistantRuntimeBootstrapService]
	global.mu.RUnlock()
	if !ok {
		t.Fatal("assistant registration did not install runtime bootstrap service")
	}
}

func TestAssistantMCPManifestRegistrationIsStrictAndDependsOnFederation(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	if err := RegisterNativeService(NativeServiceRegistration{Address: "app/mcp_server/support", Initialize: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	manifest := mcpcontract.Manifest{Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision, ProtocolVersion: mcpcontract.ProtocolVersion, ContractRevision: "capability-1", Capabilities: []mcpcontract.Capability{}, Connections: []mcpcontract.Connection{}}
	encoded, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterAssistantMCPManifestChecked("app/assistant/support", "app/mcp_server/support", encoded); err != nil {
		t.Fatalf("RegisterAssistantMCPManifestChecked() = %v", err)
	}
	if _, ok := global.assistantMCPManifests["app/assistant/support"]; !ok {
		t.Fatal("assistant MCP manifest was not retained")
	}
	serviceAddress := assistantMCPGatewayServiceAddress("app/assistant/support")
	global.mu.RLock()
	gatewayInitializer, gatewayOK := global.serviceInitializers[serviceAddress]
	bootstrapInitializer, bootstrapOK := global.serviceInitializers[assistantRuntimeBootstrapService]
	global.mu.RUnlock()
	if !gatewayOK || !bootstrapOK {
		t.Fatalf("gateway/bootstrap service registration missing: gateway=%v bootstrap=%v", gatewayOK, bootstrapOK)
	}
	if len(gatewayInitializer.dependencies) != 1 || gatewayInitializer.dependencies[0] != "app/mcp_server/support" {
		t.Fatalf("gateway dependencies = %#v", gatewayInitializer.dependencies)
	}
	if !containsString(bootstrapInitializer.dependencies, serviceAddress) {
		t.Fatalf("bootstrap dependencies = %#v", bootstrapInitializer.dependencies)
	}
	if err := RegisterAssistantMCPManifestChecked("app/assistant/support", "app/mcp_server/support", encoded); err == nil {
		t.Fatal("duplicate assistant MCP manifest registration was accepted")
	}
	for _, invalid := range []string{
		`{"kind":"scenery.mcp-capability-manifest","kind":"duplicate"}`,
		`{"kind":"scenery.mcp-capability-manifest","unknown":true}`,
	} {
		if err := RegisterAssistantMCPManifestChecked("app/assistant/other", "app/mcp_server/support", []byte(invalid)); err == nil {
			t.Fatalf("invalid assistant MCP manifest %s was accepted", invalid)
		}
	}
}

func TestAssistantMCPGatewayInitializesAfterFederationBeforeHelperBootstrap(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	if err := RegisterNativeService(NativeServiceRegistration{Address: "app/mcp_server/support", Initialize: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	manifest := mcpcontract.Manifest{Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision, ProtocolVersion: mcpcontract.ProtocolVersion, ContractRevision: "capability-1", Capabilities: []mcpcontract.Capability{}, Connections: []mcpcontract.Connection{}}
	encoded, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterAssistantMCPManifestChecked("app/assistant/support", "app/mcp_server/support", encoded); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AssistantRuntimeConfigEnv, "")
	if err := InitializeServices(); err != nil {
		t.Fatalf("InitializeServices() = %v", err)
	}
	global.mu.RLock()
	serverOrder := global.serviceInitOrder["app/mcp_server/support"]
	gatewayOrder := global.serviceInitOrder[assistantMCPGatewayServiceAddress("app/assistant/support")]
	bootstrapOrder := global.serviceInitOrder[assistantRuntimeBootstrapService]
	global.mu.RUnlock()
	if !(serverOrder > 0 && serverOrder < gatewayOrder && gatewayOrder < bootstrapOrder) {
		t.Fatalf("service initialization order server=%d gateway=%d bootstrap=%d", serverOrder, gatewayOrder, bootstrapOrder)
	}
	if err := ShutdownServices(context.Background()); err != nil {
		t.Fatalf("ShutdownServices() = %v", err)
	}
}

func TestAssistantMCPGatewayStartsAndStopsInAppChild(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	if err := RegisterNativeService(NativeServiceRegistration{Address: "app/mcp_server/support", Initialize: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	manifest := mcpcontract.Manifest{Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision, ProtocolVersion: mcpcontract.ProtocolVersion, ContractRevision: "capability-1", Capabilities: []mcpcontract.Capability{}, Connections: []mcpcontract.Connection{}}
	encoded, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterAssistantMCPManifestChecked("app/assistant/support", "app/mcp_server/support", encoded); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddress := listener.Addr().String()
	_ = listener.Close()
	path := filepath.Join(t.TempDir(), "assistant-runtime.json")
	if err := WriteAssistantRuntimeConfig(path, AssistantRuntimeConfig{Assistants: []AssistantBootstrapDescriptor{{
		AssistantAddress: "app/assistant/support", ControlAddress: "http://127.0.0.1:4101", ControlToken: "control-token", MCPListenAddress: listenAddress, MCPBridgeSecret: strings.Repeat("b", assistantMCPBridgeSecretMin), RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1",
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AssistantRuntimeConfigEnv, path)
	if err := initializeAssistantMCPGateway(context.Background(), "app/assistant/support"); err != nil {
		t.Fatalf("initializeAssistantMCPGateway() = %v", err)
	}
	activeAssistantMCPGateways.Lock()
	gateway := activeAssistantMCPGateways.values["app/assistant/support"]
	activeAssistantMCPGateways.Unlock()
	if gateway == nil || !strings.HasPrefix(gateway.URL, "http://127.0.0.1:") {
		t.Fatalf("gateway = %#v", gateway)
	}
	if err := shutdownAssistantMCPGateway(context.Background(), "app/assistant/support"); err != nil {
		t.Fatalf("shutdownAssistantMCPGateway() = %v", err)
	}
	activeAssistantMCPGateways.Lock()
	_, stillActive := activeAssistantMCPGateways.values["app/assistant/support"]
	activeAssistantMCPGateways.Unlock()
	if stillActive {
		t.Fatal("gateway survived shutdown")
	}
}

func TestAssistantMCPGatewayDispatchesRegisteredLocalToolWithSignedAssertion(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	if err := RegisterNativeService(NativeServiceRegistration{Address: "app/mcp_server/support", Initialize: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	manifest := mcpcontract.Manifest{
		Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision, ProtocolVersion: mcpcontract.ProtocolVersion, ContractRevision: "capability-1",
		Capabilities: []mcpcontract.Capability{{ID: "app/assistant/support#echo", Name: "echo", Title: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), OperationAddress: "app/operation/echo", ExecutionAddress: "app/execution/echo", Origin: mcpcontract.Origin{Kind: "local", Address: "app/service/echo"}, Limits: mcpcontract.Limits{MaxInputBytes: mcpcontract.MaximumInputBytes, MaxResultBytes: mcpcontract.MaximumResultBytes}, Approval: mcpcontract.ApprovalNever}},
	}
	encoded, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterAssistantMCPManifestChecked("app/assistant/support", "app/mcp_server/support", encoded); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMCPTool(MCPToolRegistration{
		ID: "app/assistant/support#echo", Name: "echo", AssistantAddress: "app/assistant/support", CapabilityRevision: "capability-1",
		DecodeInput: func(data []byte) (any, error) {
			var value map[string]any
			err := json.Unmarshal(data, &value)
			return value, err
		},
		EncodeOutput: func(value any) ([]byte, error) {
			return contract.MarshalContractOutcomeVariant("result", "ok", value, "json")
		},
		Invoke: func(_ context.Context, _ MCPToolCallContext, input any) (any, error) {
			return map[string]any{"echo": input}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddress := listener.Addr().String()
	_ = listener.Close()
	path := filepath.Join(t.TempDir(), "assistant-runtime.json")
	secret := strings.Repeat("b", assistantMCPBridgeSecretMin)
	if err := WriteAssistantRuntimeConfig(path, AssistantRuntimeConfig{Assistants: []AssistantBootstrapDescriptor{{
		AssistantAddress: "app/assistant/support", ControlAddress: "http://127.0.0.1:4101", ControlToken: "control-token", MCPListenAddress: listenAddress, MCPBridgeSecret: secret, RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1",
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AssistantRuntimeConfigEnv, path)
	if err := initializeAssistantMCPGateway(context.Background(), "app/assistant/support"); err != nil {
		t.Fatal(err)
	}
	activeAssistantMCPGateways.Lock()
	gateway := activeAssistantMCPGateways.values["app/assistant/support"]
	activeAssistantMCPGateways.Unlock()
	if gateway == nil {
		t.Fatal("app-owned MCP gateway did not start")
	}
	claims := mcpgateway.AssertionClaims{Audience: "scenery", AssistantAddress: "app/assistant/support", Principal: "alice", ConversationDigest: "conversation", CapabilityRevision: "capability-1", ExpiresAt: time.Now().Add(time.Minute).Unix(), Nonce: "nonce-1"}
	assertion, err := mcpgateway.SignAssertion([]byte(secret), claims)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "runtime-test-client", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: gateway.URL, HTTPClient: &http.Client{Transport: signedAssertionTransport{token: assertion}}, DisableStandaloneSSE: true, MaxRetries: -1}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		_ = shutdownAssistantMCPGateway(context.Background(), "app/assistant/support")
		t.Fatalf("connect app-owned gateway: %v", err)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"value": "ok"}})
	_ = session.Close()
	if err != nil {
		_ = shutdownAssistantMCPGateway(context.Background(), "app/assistant/support")
		t.Fatalf("call app-owned local tool: %v", err)
	}
	if result.IsError || len(result.Content) != 1 {
		_ = shutdownAssistantMCPGateway(context.Background(), "app/assistant/support")
		t.Fatalf("tool result = %#v", result)
	}
	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(textContent.Text, `"outcome":"ok"`) || !strings.Contains(textContent.Text, `"echo"`) {
		_ = shutdownAssistantMCPGateway(context.Background(), "app/assistant/support")
		t.Fatalf("tool payload = %#v", result.Content)
	}
	if err := shutdownAssistantMCPGateway(context.Background(), "app/assistant/support"); err != nil {
		t.Fatalf("shutdown app-owned gateway: %v", err)
	}
}

type signedAssertionTransport struct {
	token string
}

func (transport signedAssertionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set(mcpgateway.AssertionHeader, transport.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestAssistantMCPGatewayFailureLeavesAssistantNeutral(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/support", "support")
	if err := RegisterNativeService(NativeServiceRegistration{Address: "app/mcp_server/support", Initialize: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	manifest := mcpcontract.Manifest{Kind: mcpcontract.ManifestKind, SchemaRevision: mcpcontract.ManifestSchemaRevision, ProtocolVersion: mcpcontract.ProtocolVersion, ContractRevision: "capability-1", Capabilities: []mcpcontract.Capability{}, Connections: []mcpcontract.Connection{}}
	encoded, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterAssistantMCPManifestChecked("app/assistant/support", "app/mcp_server/support", encoded); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddress := listener.Addr().String()
	path := filepath.Join(t.TempDir(), "assistant-runtime.json")
	if err := WriteAssistantRuntimeConfig(path, AssistantRuntimeConfig{Assistants: []AssistantBootstrapDescriptor{{
		AssistantAddress: "app/assistant/support", ControlAddress: "http://127.0.0.1:4101", ControlToken: "control-token", MCPListenAddress: listenAddress, MCPBridgeSecret: strings.Repeat("b", assistantMCPBridgeSecretMin), RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1",
	}}}); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Setenv(AssistantRuntimeConfigEnv, path)
	if err := initializeAssistantMCPGateway(context.Background(), "app/assistant/support"); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	// The occupied address makes child gateway startup fail closed. The helper
	// factory itself is healthy, so this assertion proves bootstrap never
	// exposes a client while its private bridge is absent.
	bootstrap := NewAssistantBootstrap(AssistantBootstrapOptions{Factory: func(descriptor AssistantBootstrapDescriptor) (AssistantClient, error) {
		helper := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{AssistantAddress: descriptor.AssistantAddress, RuntimeRevision: descriptor.RuntimeRevision, CapabilityRevision: descriptor.CapabilityRevision})
		if err := helper.Start(context.Background()); err != nil {
			return nil, err
		}
		return helper, nil
	}, ProbeTimeout: time.Second})
	config, err := LoadAssistantRuntimeConfig(path)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	if err := bootstrap.Apply(context.Background(), config); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	status := bootstrap.Statuses()
	if len(status) != 1 || status[0].ErrorCode != "gateway_unavailable" || global.assistantClients["app/assistant/support"] != nil {
		t.Fatalf("gateway failure status = %#v, clients = %#v", status, global.assistantClients)
	}
	_ = bootstrap.Close()
	_ = listener.Close()
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSwapAssistantClientsChangesAllAssistantsAsOneGeneration(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	registerTestAssistant(t, "app/assistant/a", "a")
	registerTestAssistant(t, "app/assistant/b", "b")
	oldA := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{AssistantAddress: "app/assistant/a"})
	oldB := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{AssistantAddress: "app/assistant/b"})
	newA := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{AssistantAddress: "app/assistant/a"})
	newB := assistantruntime.NewFakeHelper(assistantruntime.FakeConfig{AssistantAddress: "app/assistant/b"})
	if _, err := swapAssistantClients(map[string]AssistantClient{"app/assistant/a": oldA, "app/assistant/b": oldB}); err != nil {
		t.Fatal(err)
	}
	previous, err := swapAssistantClients(map[string]AssistantClient{"app/assistant/a": newA, "app/assistant/b": newB})
	if err != nil {
		t.Fatal(err)
	}
	if previous["app/assistant/a"] != oldA || previous["app/assistant/b"] != oldB {
		t.Fatalf("previous generation = %#v", previous)
	}
	if global.assistantClients["app/assistant/a"] != newA || global.assistantClients["app/assistant/b"] != newB {
		t.Fatal("bulk swap did not install a single new generation")
	}
}

func registerTestAssistant(t *testing.T, address, name string) {
	t.Helper()
	if err := RegisterAssistantChecked(AssistantRegistration{Address: address, Name: name, Path: "/assistants/" + name, Access: Public, AssistantAddress: address, RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapConfigMissingFileIsStrict(t *testing.T) {
	_, err := LoadAssistantRuntimeConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !errors.Is(err, errAssistantRuntimeConfig) {
		t.Fatalf("missing config error = %v, want assistant runtime config error", err)
	}
}

func TestAssistantBootstrapDoesNotExposeProviderNames(t *testing.T) {
	data, err := os.ReadFile("assistant_bootstrap.go")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?i)(^|[^a-z0-9_])eve([^a-z0-9_]|$)`).Match(data) {
		t.Fatal("runtime bootstrap contains provider implementation name")
	}
	if strings.Contains(string(data), "SCENERY_ASSISTANT_CONTROL_TOKEN") {
		t.Fatal("runtime bootstrap introduced a singular helper token environment")
	}
}

func TestAssistantBootstrapPublicStatusJSONIsProviderNeutral(t *testing.T) {
	status := AssistantBootstrapStatus{AssistantAddress: "app/assistant/support", State: string(assistantruntime.StateReady), Required: true, RuntimeRevision: "runtime", CapabilityRevision: "capability"}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "control_") || strings.Contains(string(data), "token") {
		t.Fatal("status JSON contains private descriptor fields")
	}
	if got := http.StatusServiceUnavailable; got != 503 {
		t.Fatalf("unexpected status constant %d", got)
	}
}

func TestAssistantTokenKeyFilePersistsHandlesAcrossRegistrationRestart(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	keyPath := filepath.Join(t.TempDir(), "assistant-token.key")
	key := strings.Repeat("k", 32)
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AssistantTokenKeyEnv, "")
	t.Setenv(AssistantTokenKeyFileEnv, keyPath)
	first, _, err := normalizeAssistantRegistration(AssistantRegistration{Address: "app/assistant/support", Name: "support", Path: "/assistants/support", Access: Public, AssistantAddress: "app/assistant/support", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.TokenManager.Keys == nil || len(first.InitiatorSigner.Key) != 32 {
		t.Fatal("persistent token key was not loaded")
	}
	claims := assistanttoken.ConversationClaims{AssistantAddress: "app/assistant/support", OwnerDigest: assistanttoken.OwnerDigest("owner"), ConversationDigest: assistanttoken.ConversationDigest("conversation"), PrivateSessionID: "session", ContinuationToken: "continuation"}
	handle, err := first.TokenManager.SealConversation(claims)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := normalizeAssistantRegistration(AssistantRegistration{Address: "app/assistant/support", Name: "support", Path: "/assistants/support", Access: Public, AssistantAddress: "app/assistant/support", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.TokenManager.UnsealConversation(handle, assistanttoken.ConversationExpectation{AssistantAddress: claims.AssistantAddress, OwnerDigest: claims.OwnerDigest}); err != nil {
		t.Fatalf("handle did not survive registration restart: %v", err)
	}
}

func TestAssistantTokenKeyInvalidFileFailsClosedWithoutRandomFallback(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	keyPath := filepath.Join(t.TempDir(), "assistant-token.key")
	if err := os.WriteFile(keyPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AssistantTokenKeyEnv, "")
	t.Setenv(AssistantTokenKeyFileEnv, keyPath)
	registration, _, err := normalizeAssistantRegistration(AssistantRegistration{Address: "app/assistant/support", Name: "support", Path: "/assistants/support", Access: Public, AssistantAddress: "app/assistant/support", RuntimeRevision: "runtime-1", CapabilityRevision: "capability-1"})
	if err != nil {
		t.Fatal(err)
	}
	if registration.TokenManager.Keys != nil || len(registration.InitiatorSigner.Key) != 0 {
		t.Fatal("invalid configured key unexpectedly enabled a fallback key")
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if key := assistantTokenKeyFromRuntime(); key != nil {
		t.Fatal("insecure key file was accepted")
	}
}
