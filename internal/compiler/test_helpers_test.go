package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/spec"
)

func diagnosticsContain(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func deploymentPlanFixture(t *testing.T, lifecycle string) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sceneryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(sceneryRoot), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, appFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(`

provider "postgres" {
  source = "registry.scenery.dev/core/postgres"
}
data_source "database" {
  provider = provider.postgres
  lifecycle = "`+lifecycle+`"
  config = { database = "nativeapp" }
}
`)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	integrity, ok := BuiltinProviderLock("registry.scenery.dev/core/postgres")
	if !ok {
		t.Fatal("builtin postgres descriptor is unavailable")
	}
	lockfile := fmt.Sprintf(`lock {}
provider "postgres" {
  source = "registry.scenery.dev/core/postgres"
  integrity = %q
  compile_descriptor_digest = %q
  runtime_abi = "scenery.data-runtime/v1"
  deployment_abi = %q
}
`, integrity, integrity, deploymentProviderABI)
	if err := os.WriteFile(filepath.Join(root, appLockFilename), []byte(lockfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeNestedModuleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func DiagnosticDefinitions() []DiagnosticDefinition {
	return spec.DiagnosticDefinitions()
}

func diagnosticCategory(code string) string {
	return spec.DiagnosticCategory(code)
}

func parseDiagnosticDefinitions(rows string) []DiagnosticDefinition {
	return spec.ParseDiagnosticDefinitions(rows)
}

func workspaceRevisionInputPathsWithGenerated(root string, sources []*Source, generatedPaths map[string]bool) ([]workspaceRevisionInputPath, error) {
	return workspaceRevisionInputPathsReading(root, sources, generatedPaths, nil)
}

func resolveModuleInputValuesWithProvenance(rootResources, packageResources []Resource, packageSources []*Source, module *Block, callerModule string) (map[string]any, map[string]FieldProvenance, []Diagnostic) {
	return resolveModuleInputValuesWithSourceProvenance(rootResources, packageResources, packageSources, module, module, callerModule)
}

func resolveModuleInstanceInputsInScope(rootResources, packageResources []Resource, packageSources []*Source, module *Block, callerModule string) ([]Resource, []Diagnostic) {
	values, provenance, diagnostics := resolveModuleInputValuesWithProvenance(rootResources, packageResources, packageSources, module, callerModule)
	resolved, substitutionDiagnostics := substituteResolvedModuleInputsWithProvenance(packageResources, values, provenance, deferredConfigurationInputs(packageSources, values))
	diagnostics = append(diagnostics, substitutionDiagnostics...)
	return resolved, diagnostics
}

func authoredAttributeDefinition(revision, name string) authoredAttributeSchema {
	return spec.AuthoredAttributeDefinition(revision, name)
}

func authoredRevisionDomain(revision, name string) string {
	return spec.AuthoredRevisionDomain(revision, name)
}

func resourceSchemaAllowedFields(kind string) []string {
	return spec.ResourceSchemaAllowedFields(kind)
}

func BuiltinProviderLock(source string) (integrity string, ok bool) {
	descriptor, ok := builtinProviderDescriptors()[source]
	if !ok {
		return "", false
	}
	return providerDescriptorDigest(descriptor), true
}
