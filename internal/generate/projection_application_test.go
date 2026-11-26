package generate

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestPrivateGoProjectionPreservesGenerationIdentity(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	read := func() []generatedFile {
		t.Helper()
		files, err := renderExpectedGoApplicationFiles(result, newProjectionInput(result))
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	first := read()
	if len(first) == 0 || !reflect.DeepEqual(first, read()) {
		t.Fatal("unchanged private projection differs")
	}
	first[0].Bytes[0] ^= 1
	if reflect.DeepEqual(first, read()) {
		t.Fatal("caller mutation leaked into the projection cache")
	}
	result.ImplementationRevisions = map[string]string{"development": "sha256:" + strings.Repeat("a", 64)}
	implementation := read()
	descriptor := generatedSourceWithSuffix(implementation, "scenery.generated.json")
	if !strings.Contains(descriptor, result.ImplementationRevisions["development"]) {
		t.Fatal("private cache hid a changed implementation identity")
	}
	want, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil || !reflect.DeepEqual(implementation, want) {
		t.Fatalf("cached projection differs from fresh rendering: %v", err)
	}
}

func TestRuntimeIntegrationPlanListsEveryRenderedServiceAdapter(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v", err)
	}
	plan, err := BuildRuntimeIntegrationPlan(result)
	if err != nil {
		t.Fatal(err)
	}
	_, generatedImport, err := resolveApplicationGeneratedRoot(result)
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := renderApplicationAdapters(result, newResourceIndex(result.Manifest.Resources), generatedImport)
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) == 0 || len(plan.Services) != len(adapters) {
		t.Fatalf("plan services = %d, rendered adapters = %d", len(plan.Services), len(adapters))
	}
	for index, adapter := range adapters {
		service := plan.Services[index]
		if service.Address != adapter.Address || service.AdapterImport != adapter.ImportPath || service.Name != strings.TrimSuffix(adapter.RelativeDir, "_adapter") || !slices.Equal(service.RequiredAddresses, adapter.Covered) {
			t.Fatalf("service process plan %#v differs from rendered adapter %s %s %v", service, adapter.Address, adapter.ImportPath, adapter.Covered)
		}
		// Each worker registers a typed adapter for every operation of its
		// service; the host owns the bindings.
		source := string(adapter.Source)
		for _, operation := range compiler.ServiceOperations(result.Manifest.Resources, findResource(result.Manifest.Resources, service.Address)) {
			if !strings.Contains(source, fmt.Sprintf("sceneryruntime.RegisterWorkerOperation(%q,", operation.Address)) {
				t.Fatalf("service process %s does not register operation %s", service.Name, operation.Address)
			}
		}
	}
	if plan.ContractRevision != result.Manifest.ContractRevision {
		t.Fatalf("plan contract revision = %q", plan.ContractRevision)
	}
}

func findResource(resources []Resource, address string) Resource {
	for _, resource := range resources {
		if resource.Address == address {
			return resource
		}
	}
	return Resource{}
}
