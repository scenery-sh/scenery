package runtime

import (
	"context"
	"errors"
	"testing"
)

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
	three, ok := LookupAssistantMCPFederation("app/assistant/c")
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

func TestMCPFederationLifecycleAndSealRollback(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer func() { _ = ShutdownServices(context.Background()); restore() }()
	registration := MCPFederationRegistration{Address: "app/mcp_server/support", AssistantAddresses: []string{"app/assistant/support"}, CapabilityRevision: "sha256:contract"}
	contract, err := NewContractRegistry(ContractRegistryOptions{ContractRevisions: map[string]string{"federation": "sha256:contract", "failing": "sha256:contract"}, RequiredAddresses: []string{"app/mcp_server/support", "app/failing"}})
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
	global.mu.RLock()
	_, registered := global.mcpFederationSpecs[registration.Address]
	global.mu.RUnlock()
	if registered {
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
	if _, ok := LookupAssistantMCPFederation("app/assistant/support"); !ok {
		t.Fatal("federation missing after initialization")
	}
	if err := ShutdownServices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupAssistantMCPFederation("app/assistant/support"); ok {
		t.Fatal("federation survived shutdown")
	}
}
