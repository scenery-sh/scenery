package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMCPFederationRegistrationKeepsSecretsSymbolic(t *testing.T) {
	registration := MCPFederationRegistration{
		Address:            "app/mcp_server/support",
		AssistantAddresses: []string{"app/assistant/support"},
		CapabilityRevision: "sha256:contract",
		Connections: []MCPConnectionSpec{{
			Address: "app/mcp_connection/docs", Namespace: "docs", URL: "http://127.0.0.1:1",
			AuthScheme: "bearer", Secret: MCPSecretReference{ResourceAddress: "app/secret/docs", StoreAddress: "app/secret_store/local", Key: "token"},
		}},
	}
	if _, err := normalizeMCPFederationRegistration(registration); err != nil {
		t.Fatalf("normalize symbolic registration: %v", err)
	}
	if strings.Contains(strings.Join([]string{registration.Address, registration.Connections[0].URL, registration.Connections[0].Secret.Key}, "\n"), "super-secret") {
		t.Fatal("test setup unexpectedly contains a plaintext secret")
	}
}

func TestMCPFederationSecretResolverAndSafeErrors(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer func() { _ = ShutdownServices(context.Background()); restore() }()
	const secret = "very-secret-value"
	if err := RegisterMCPSecretResolver("app/secret_store/local", func(context.Context, MCPSecretReference) ([]byte, error) {
		return []byte(secret), errors.New("resolver backend contains " + secret)
	}); err != nil {
		t.Fatal(err)
	}
	registration := MCPFederationRegistration{
		Address: "app/mcp_server/support", CapabilityRevision: "sha256:contract",
		Connections: []MCPConnectionSpec{{
			Address: "app/mcp_connection/docs", Namespace: "docs", URL: "http://127.0.0.1:1", Required: true,
			AuthScheme: "bearer", Secret: MCPSecretReference{ResourceAddress: "app/secret/docs", StoreAddress: "app/secret_store/local", Key: "token"},
			ConnectTimeout: 20 * time.Millisecond, CallTimeout: 20 * time.Millisecond,
		}},
	}
	if err := RegisterMCPFederationChecked(registration); err != nil {
		t.Fatal(err)
	}
	err := InitializeServices()
	if err == nil || !strings.Contains(err.Error(), "secret could not be resolved") {
		t.Fatalf("resolver error = %v, want sanitized resolution failure", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("resolver error leaked secret or URL: %v", err)
	}
}

func TestMCPFederationSecretResolverBufferIsCleared(t *testing.T) {
	value := []byte("temporary-secret")
	_, err := resolveMCPConnectionAuth(context.Background(), MCPConnectionSpec{
		AuthScheme: "bearer",
		Secret:     MCPSecretReference{ResourceAddress: "app/secret/token", StoreAddress: "app/secret_store/local", Key: "token"},
	}, map[string]MCPSecretResolver{
		"app/secret_store/local": func(context.Context, MCPSecretReference) ([]byte, error) { return value, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Trim(string(value), "\x00") != "" {
		t.Fatalf("resolver buffer was not cleared: %q", value)
	}
}

func TestMCPFederationMissingResolverFailsClosed(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer func() { _ = ShutdownServices(context.Background()); restore() }()
	registration := MCPFederationRegistration{
		Address: "app/mcp_server/support", CapabilityRevision: "sha256:contract",
		Connections: []MCPConnectionSpec{{
			Address: "app/mcp_connection/docs", Namespace: "docs", URL: "https://example.invalid/mcp", Required: true,
			AuthScheme: "header", AuthHeader: "X-API-Key", Secret: MCPSecretReference{ResourceAddress: "app/secret/docs", StoreAddress: "app/secret_store/missing", Key: "token"},
		}},
	}
	if err := RegisterMCPFederationChecked(registration); err != nil {
		t.Fatal(err)
	}
	err := InitializeServices()
	if err == nil || !strings.Contains(err.Error(), "secret resolver is unavailable") {
		t.Fatalf("InitializeServices() = %v, want missing resolver error", err)
	}
	if strings.Contains(err.Error(), "example.invalid") {
		t.Fatalf("missing resolver error leaked URL: %v", err)
	}
}

func TestMCPFederationSharingAndAssistantIsolation(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer func() { _ = ShutdownServices(context.Background()); restore() }()
	first := MCPFederationRegistration{Address: "app/mcp_server/support", AssistantAddresses: []string{"app/assistant/a", "app/assistant/b"}, CapabilityRevision: "sha256:a"}
	second := MCPFederationRegistration{Address: "app/mcp_server/billing", AssistantAddresses: []string{"app/assistant/c"}, CapabilityRevision: "sha256:b"}
	if err := RegisterMCPFederationChecked(first); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMCPFederationChecked(second); err != nil {
		t.Fatal(err)
	}
	if err := InitializeServices(); err != nil {
		t.Fatal(err)
	}
	one, ok := LookupAssistantMCPFederation("app/assistant/a")
	if !ok {
		t.Fatal("assistant a federation missing")
	}
	two, ok := LookupAssistantMCPFederation("app/assistant/b")
	if !ok || one != two {
		t.Fatal("assistants sharing one server did not share federation")
	}
	three, ok := LookupMCPFederationForAssistant("app/assistant/c")
	if !ok || three == one {
		t.Fatal("assistant c unexpectedly shares another server federation")
	}
}

func TestMCPFederationDuplicatesAndFilterCanonicalization(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	base := MCPFederationRegistration{Address: "app/mcp_server/support", AssistantAddresses: []string{"app/assistant/a", "app/assistant/a"}, CapabilityRevision: "sha256:a", LocalToolNames: []string{"zeta", "alpha", "alpha"}}
	if err := RegisterMCPFederationChecked(base); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMCPFederationChecked(base); err == nil {
		t.Fatal("duplicate federation registration was accepted")
	}
	if err := RegisterMCPFederationChecked(MCPFederationRegistration{Address: "app/mcp_server/billing", AssistantAddresses: []string{"app/assistant/a"}, CapabilityRevision: "sha256:b"}); err == nil {
		t.Fatal("assistant federation collision was accepted")
	}
	normalized, err := normalizeMCPFederationRegistration(base)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalized.LocalToolNames, []string{"alpha", "zeta"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("local tool ordering = %v, want %v", got, want)
	}
}

func TestMCPFederationRequiredAndOptionalReadiness(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer func() { _ = ShutdownServices(context.Background()); restore() }()
	required := MCPFederationRegistration{
		Address: "app/mcp_server/required", CapabilityRevision: "sha256:required",
		Connections: []MCPConnectionSpec{{Address: "app/mcp_connection/down", Namespace: "down", URL: "http://127.0.0.1:1", Required: true, ConnectTimeout: 20 * time.Millisecond, CallTimeout: 20 * time.Millisecond}},
	}
	optional := MCPFederationRegistration{
		Address: "app/mcp_server/optional", CapabilityRevision: "sha256:optional",
		Connections: []MCPConnectionSpec{{Address: "app/mcp_connection/down_optional", Namespace: "down_optional", URL: "http://127.0.0.1:1", ConnectTimeout: 20 * time.Millisecond, CallTimeout: 20 * time.Millisecond}},
	}
	if err := RegisterMCPFederationChecked(required); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMCPFederationChecked(optional); err != nil {
		t.Fatal(err)
	}
	if err := InitializeServices(); err != nil {
		t.Fatalf("required outage should be readiness state, got %v", err)
	}
	requiredFederation, ok := LookupMCPFederation(required.Address)
	if !ok || requiredFederation.Ready() {
		t.Fatal("required federation unexpectedly ready")
	}
	if err := MCPFederationReadiness(required.Address); !errors.Is(err, ErrMCPFederationNotReady) {
		t.Fatalf("required readiness = %v, want typed not-ready", err)
	}
	optionalFederation, ok := LookupMCPFederation(optional.Address)
	if !ok || !optionalFederation.Ready() || len(optionalFederation.Capabilities()) != 0 {
		t.Fatal("optional outage should be ready with omitted tools")
	}
}

func TestMCPFederationLifecycleAndSealRollback(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer func() { _ = ShutdownServices(context.Background()); restore() }()
	registration := MCPFederationRegistration{Address: "app/mcp_server/support", AssistantAddresses: []string{"app/assistant/support"}, CapabilityRevision: "sha256:contract"}
	contract, err := NewContractRegistry(ContractRegistryOptions{ContractRevision: "sha256:contract", RequiredAddresses: []string{"app/mcp_server/support", "app/failing"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Register("federation", ContractRegistration{ContractRevision: "sha256:contract", PackageContractABIRevision: "pkg", RuntimeABI: ContractRuntimeABI, CoveredAddresses: []string{"app/mcp_server/support"}, Apply: func() error {
		return RegisterMCPFederationChecked(registration)
	}}); err != nil {
		t.Fatal(err)
	}
	if err := contract.Register("failing", ContractRegistration{ContractRevision: "sha256:contract", PackageContractABIRevision: "pkg", RuntimeABI: ContractRuntimeABI, CoveredAddresses: []string{"app/failing"}, Apply: func() error {
		return errors.New("intentional adapter failure")
	}}); err != nil {
		t.Fatal(err)
	}
	if err := contract.Seal(); err == nil {
		t.Fatal("contract seal unexpectedly succeeded")
	}
	if _, ok := LookupMCPFederation(registration.Address); ok {
		t.Fatal("federation survived contract rollback")
	}
	if _, ok := LookupAssistantMCPFederation("app/assistant/support"); ok {
		t.Fatal("assistant mapping survived contract rollback")
	}

	if err := RegisterMCPFederationChecked(registration); err != nil {
		t.Fatal(err)
	}
	if err := InitializeServices(); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupMCPFederation(registration.Address); !ok {
		t.Fatal("federation missing after initialization")
	}
	if err := ShutdownServices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupMCPFederation(registration.Address); ok {
		t.Fatal("federation survived shutdown")
	}
}
