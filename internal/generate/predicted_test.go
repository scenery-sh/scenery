package generate

import (
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

func diagnosticsOf(result *compiler.Result) []compiler.Diagnostic {
	if result == nil {
		return nil
	}
	return result.Diagnostics
}
