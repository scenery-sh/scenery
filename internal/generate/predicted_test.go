package generate

import (
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/runtimeassets"
)

func TestAssistantAssetDescriptorKindMatchesRuntimeAssets(t *testing.T) {
	if AssistantAssetDescriptorKind != runtimeassets.AssistantAssetKind {
		t.Fatalf("AssistantAssetDescriptorKind = %q, want %q", AssistantAssetDescriptorKind, runtimeassets.AssistantAssetKind)
	}
}

func TestApplyImplementationCheckReportsValidNative(t *testing.T) {
	parallelIntegrationTest(t)

	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	result, err := compiler.Compile(root)
	if err != nil || result == nil || !result.Valid() {
		t.Fatalf("compile native: %v diagnostics=%#v", err, diagnosticsOf(result))
	}
	ApplyImplementationCheck(result)
	if result.ImplementationStatus != "valid" {
		t.Fatalf("implementation_status = %q diagnostics=%#v", result.ImplementationStatus, result.Diagnostics)
	}
}

func TestCheckPredictedArtifactsAcceptNativeAndHouse(t *testing.T) {
	t.Run("go native", func(t *testing.T) {
		parallelIntegrationTest(t)
		root := t.TempDir()
		copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
		rewriteFixtureSceneryReplace(t, root)
		result, err := compiler.Compile(root)
		if err != nil || result == nil || !result.Valid() {
			t.Fatalf("compile native: %v diagnostics=%#v", err, diagnosticsOf(result))
		}
		if err := CheckPredictedGoContracts(result); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("typescript house", func(t *testing.T) {
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
	})
}

func diagnosticsOf(result *compiler.Result) []compiler.Diagnostic {
	if result == nil {
		return nil
	}
	return result.Diagnostics
}
