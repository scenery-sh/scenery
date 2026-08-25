package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/runtimeassets"
)

func TestAssistantAssetDescriptorKindMatchesRuntimeAssets(t *testing.T) {
	if AssistantAssetDescriptorKind != runtimeassets.AssistantAssetKind {
		t.Fatalf("AssistantAssetDescriptorKind = %q, want %q", AssistantAssetDescriptorKind, runtimeassets.AssistantAssetKind)
	}
}

func TestApplyImplementationCheckUsesInjectedCheckInProcess(t *testing.T) {
	t.Parallel()

	result := &compiler.Result{}
	called := false
	applyImplementationCheck(result, func(got *compiler.Result) CheckResult {
		called = true
		if got != result {
			t.Fatalf("check result pointer = %p, want %p", got, result)
		}
		return CheckResult{ImplementationStatus: "valid", ImplementationChecked: true}
	})
	if !called {
		t.Fatal("implementation checker was not called")
	}
	if result.ImplementationStatus != "valid" {
		t.Fatalf("implementation_status = %q diagnostics=%#v", result.ImplementationStatus, result.Diagnostics)
	}
}

func TestCheckPredictedGoContractsAcceptsNative(t *testing.T) {
	parallelIntegrationTest(t)
	root := t.TempDir()
	writeMinimalPredictedGoFixture(t, root)
	result, err := compiler.Compile(root)
	if err != nil || result == nil || !result.Valid() {
		t.Fatalf("compile native: %v diagnostics=%#v", err, diagnosticsOf(result))
	}
	if err := CheckPredictedGoContracts(result); err != nil {
		t.Fatal(err)
	}
	adapter, err := os.ReadFile(filepath.Join(root, "internal", "scenerygen", "house_house_adapter", "adapter.gen.go"))
	if err != nil {
		t.Fatalf("read predicted native adapter: %v", err)
	}
	if !strings.Contains(string(adapter), "return native.Inspect(ctx, input)") {
		t.Fatalf("predicted native adapter does not invoke the authored handler:\n%s", adapter)
	}
}

func TestCheckPredictedTypeScriptClientsAcceptHouse(t *testing.T) {
	parallelIntegrationTest(t)
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	result, err := compiler.Compile(root)
	if err != nil || result == nil || !result.Valid() {
		t.Fatalf("compile house: %v diagnostics=%#v", err, diagnosticsOf(result))
	}
	if err := CheckPredictedTypeScriptClients(result); err != nil {
		t.Fatal(err)
	}
}

func diagnosticsOf(result *compiler.Result) []compiler.Diagnostic {
	if result == nil {
		return nil
	}
	return result.Diagnostics
}
