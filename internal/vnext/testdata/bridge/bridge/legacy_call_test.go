package bridge_test

import (
	"context"
	"testing"

	"example.test/bridgeapp/bridge"
	"example.test/bridgeapp/internal/scenerygen/composition"
	sceneryruntime "scenery.sh/runtime"
)

func TestLegacyGeneratedCallReachesNativeOwnedOperation(t *testing.T) {
	registry, err := sceneryruntime.NewContractRegistry(sceneryruntime.ContractRegistryOptions{
		ContractRevision:  composition.ContractRevision,
		RequiredAddresses: composition.RequiredAddresses,
		ProviderABIs:      composition.RequiredProviderABIs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := composition.Register(registry); err != nil {
		t.Fatal(err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatal(err)
	}
	if err := sceneryruntime.InitializeServices(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sceneryruntime.ShutdownServices(context.Background()) })

	value, err := sceneryruntime.CallEndpoint(context.Background(), "bridge", "Echo", nil, &bridge.EchoParams{Message: "roof"})
	if err != nil {
		t.Fatal(err)
	}
	response, ok := value.(*bridge.EchoResponse)
	if !ok || response.Message != "legacy:roof" {
		t.Fatalf("legacy call response = %#v", value)
	}
}
