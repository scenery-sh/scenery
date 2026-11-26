package generate

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
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
		{Address: "consumer/service/second", Kind: "scenery.service", Name: "second", Module: "consumer", Spec: map[string]any{
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
	first, err := serviceGoClients(idx, resources[1])
	if err != nil || len(first) != 1 || first[0].operationName() != "ProjectedEchoClient" {
		t.Fatalf("first clients = %+v, err = %v", first, err)
	}
	second, err := serviceGoClients(idx, resources[2])
	if err != nil || len(second) != 1 || second[0].operationName() != "Projected2EchoClient" {
		t.Fatalf("second clients = %+v, err = %v", second, err)
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
	if !strings.Contains(contractSrc, "ProjectedEchoClientInput") || !strings.Contains(contractSrc, "Projected2EchoClientInput") || !strings.Contains(contractSrc, "UnmarshalEchoClientInput") {
		t.Fatalf("expected all disambiguated and local operations:\n%s", contractSrc)
	}
}

func TestProjectedGoClientReservesAllGeneratedOperationIdentifiers(t *testing.T) {
	resources := []Resource{
		{Address: "app/module/consumer", Kind: "scenery.module", Name: "consumer", Module: "app", Spec: map[string]any{
			"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/consumer"}},
		}},
		{Address: "consumer/service/consumer", Kind: "scenery.service", Name: "consumer", Module: "consumer", Spec: map[string]any{
			"client": []any{map[string]any{"name": "echo", "binding": map[string]any{"$ref": "provider/binding/echo_internal"}}},
		}},
		{Address: "consumer/record/echo_client_input", Kind: "scenery.record", Name: "echo_client_input", Module: "consumer"},
		{Address: "consumer/record/projected_echo_client_ok", Kind: "scenery.record", Name: "projected_echo_client_ok", Module: "consumer"},
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
	if err != nil || len(clients) != 1 || clients[0].operationName() != "Projected2EchoClient" {
		t.Fatalf("clients = %+v, err = %v; want Projected2EchoClient", clients, err)
	}
	projected, err := projectedGoClientOperations(resources, idx)
	if err != nil {
		t.Fatal(err)
	}
	moduleResources := idx.moduleResources("consumer")
	typeResolver := newGoContractTypeResolver("consumer", resources, map[string]bool{})
	contractSrc, err := renderContractAPI("consumer", "example.test/consumer", "abi-1", moduleResources, idx, typeResolver)
	if err != nil {
		t.Fatal(err)
	}
	typesSrc := renderContractTypesResolved(append(slices.Clone(moduleResources), projected...), typeResolver)
	fset := token.NewFileSet()
	contractFile, err := parser.ParseFile(fset, "contract.gen.go", contractSrc, 0)
	if err != nil {
		t.Fatalf("generated contract failed to parse: %v\n%s", err, contractSrc)
	}
	typesFile, err := parser.ParseFile(fset, "types.gen.go", typesSrc, 0)
	if err != nil {
		t.Fatalf("generated types failed to parse: %v\n%s", err, typesSrc)
	}
	decls := map[string]string{}
	for name, file := range map[string]*ast.File{"contract.gen.go": contractFile, "types.gen.go": typesFile} {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						if prev, exists := decls[ts.Name.Name]; exists {
							t.Fatalf("duplicate type identifier %s in %s and %s", ts.Name.Name, prev, name)
						}
						decls[ts.Name.Name] = name
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil {
					if prev, exists := decls[d.Name.Name]; exists {
						t.Fatalf("duplicate function identifier %s in %s and %s", d.Name.Name, prev, name)
					}
					decls[d.Name.Name] = name
				}
			}
		}
	}
	if !strings.Contains(contractSrc, "Projected2EchoClientInput") || !strings.Contains(contractSrc, "Projected2EchoClientOk") {
		t.Fatalf("expected Projected2EchoClient symbols:\n%s", contractSrc)
	}
}
