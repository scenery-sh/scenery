package generate

import (
	"errors"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"scenery.sh/internal/scn"
)

func TestAdapterProjectionSeparatesBuildIdentityAndOwnsValues(t *testing.T) {
	result := &Result{Root: t.TempDir(), Manifest: &Manifest{}, Sources: []*scn.Source{{Bytes: []byte("contract")}}}
	calls := 0
	render := func() ([]applicationAdapter, error) {
		calls++
		return []applicationAdapter{{Source: []byte("source"), Covered: []string{"operation"}}}, nil
	}
	read := func(importPath string) []applicationAdapter {
		t.Helper()
		adapters, err := cachedApplicationAdapters(newProjectionInput(result), importPath, render)
		if err != nil {
			t.Fatal(err)
		}
		return adapters
	}
	first := read("app/internal/scenerygen")
	first[0].Source[0] = 'x'
	first[0].Covered[0] = "changed"
	result.WorkspaceRevision = "next-workspace"
	result.ImplementationRevisions = map[string]string{"development": "next-implementation"}
	second := read("app/internal/scenerygen")
	if calls != 1 || string(second[0].Source) != "source" || second[0].Covered[0] != "operation" {
		t.Fatalf("build identity prevented reuse or mutable values leaked: calls=%d adapters=%v", calls, second)
	}
	second[0].Source[0] = 'x'
	second[0].Covered[0] = "changed"
	if third := read("app/internal/scenerygen"); string(third[0].Source) != "source" || third[0].Covered[0] != "operation" {
		t.Fatal("cache hit did not own its values")
	}
	result.Manifest.ContractRevision = "new-contract"
	read("app/internal/scenerygen")
	result.Sources[0].Blocks = []*scn.Block{{Type: "synthetic"}}
	read("app/internal/scenerygen")
	read("other/internal/scenerygen")
	if calls != 4 {
		t.Fatalf("declaration/import invalidation: %d renders", calls)
	}
	result.Root = t.TempDir()
	want := errors.New("invalid adapter")
	for range 2 {
		_, err := cachedApplicationAdapters(newProjectionInput(result), "app", func() ([]applicationAdapter, error) {
			calls++
			return nil, want
		})
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	if calls != 6 {
		t.Fatal("failed rendering was cached")
	}
}

func TestProjectedGoClientDisambiguatesCollisionWithLocalOperation(t *testing.T) {
	resources := []Resource{
		{Address: "app/module/consumer", Kind: "scenery.module", Name: "consumer", Module: "app", Spec: map[string]any{
			"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/consumer"}},
		}},
		{Address: "consumer/service/consumer", Kind: "scenery.service", Name: "consumer", Module: "consumer", Spec: map[string]any{
			"client": []any{map[string]any{"name": "echo", "binding": map[string]any{"$ref": "provider/binding/echo_internal"}}},
		}},
		{Address: "consumer/operation/echo_client", Kind: "scenery.operation", Name: "echo_client", Module: "consumer", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.consumer"}, "input": "string",
			"result":  []any{map[string]any{"name": "ok", "type": "string"}},
			"handler": map[string]any{"method": "EchoClient"},
		}},
		{Address: "provider/service/provider", Kind: "scenery.service", Name: "provider", Module: "provider"},
		{Address: "provider/operation/echo", Kind: "scenery.operation", Name: "echo", Module: "provider", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.provider"}, "input": "string",
			"result": []any{map[string]any{"name": "ok", "type": "string"}},
		}},
		{Address: "provider/binding/echo_internal", Kind: "scenery.binding", Name: "echo_internal", Module: "provider", Spec: map[string]any{
			"operation": map[string]any{"$ref": "operation.echo"}, "protocol": "internal", "delivery": "call",
			"internal": map[string]any{"visibility": "application"},
		}},
	}
	idx := newResourceIndex(resources)
	clients, err := serviceGoClients(idx, resources[1])
	if err != nil || len(clients) != 1 || clients[0].operationName() != "ProjectedEchoClient" {
		t.Fatalf("clients = %+v, err = %v", clients, err)
	}
	projected, err := projectedGoClientOperations(resources, idx)
	if err != nil {
		t.Fatal(err)
	}
	typeResolver := newGoContractTypeResolver("consumer", resources, map[string]bool{})
	contractSrc, err := renderContractAPI("consumer", "example.test/consumer", "abi-1", append(idx.moduleResources("consumer"), projected...), idx, typeResolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "contract.gen.go", contractSrc, 0); err != nil {
		t.Fatalf("generated contract failed to parse: %v\n%s", err, contractSrc)
	}
	if !strings.Contains(contractSrc, "ProjectedEchoClientInput") || !strings.Contains(contractSrc, "UnmarshalEchoClientInput") {
		t.Fatalf("expected both disambiguated and local operations:\n%s", contractSrc)
	}
}
