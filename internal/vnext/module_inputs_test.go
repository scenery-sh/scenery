package vnext

import "testing"

func TestResolveModuleInstanceInputsSubstitutesTypedResources(t *testing.T) {
	rootResources := []Resource{{
		Address: "app/data_source/database", Module: "app", Kind: "scenery.data-source/v1", Name: "database",
		Spec: map[string]any{"require_capabilities": []any{"sql.query/v1"}, "effective_capabilities": []any{"sql.query/v1"}},
	}}
	packageResources := []Resource{{
		Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Name: "house",
		Spec: map[string]any{"dependency": map[string]any{"name": "database", "instance": map[string]any{"$ref": "var.database"}}},
	}}
	sources := []*Source{{Blocks: []*Block{{
		Type: "input", Labels: []string{"database"}, Attributes: map[string]Expression{
			"type":     {Raw: `resource_ref("data_source")`},
			"requires": {Kind: "literal", Value: []any{"sql.query/v1"}},
		},
	}}}}
	module := &Block{Type: "module", Labels: []string{"house"}, Attributes: map[string]Expression{
		"inputs": {Kind: "literal", Value: map[string]any{"database": map[string]any{"$ref": "data_source.database"}}},
	}}
	resolved, diagnostics := resolveModuleInstanceInputs(rootResources, packageResources, sources, module)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
	dependency := namedChildren(resolved[0].Spec, "dependency")[0]
	if refString(dependency["instance"]) != "data_source.database" {
		t.Fatalf("resolved dependency = %#v", dependency)
	}
}

func TestResolveModuleInstanceInputsRejectsMissingAndWrongKind(t *testing.T) {
	sources := []*Source{{Blocks: []*Block{{Type: "input", Labels: []string{"database"}, Attributes: map[string]Expression{"type": {Raw: `resource_ref("data_source")`}}}}}}
	packageResources := []Resource{{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Spec: map[string]any{"dependency": map[string]any{"name": "database", "instance": map[string]any{"$ref": "var.database"}}}}}
	module := &Block{Type: "module", Labels: []string{"house"}, Attributes: map[string]Expression{}}
	if _, diagnostics := resolveModuleInstanceInputs(nil, packageResources, sources, module); !hasDiagnostic(diagnostics, "SCN3007") {
		t.Fatalf("missing input diagnostics: %#v", diagnostics)
	}
	module.Attributes["inputs"] = Expression{Kind: "literal", Value: map[string]any{"database": map[string]any{"$ref": "http_gateway.public"}}}
	root := []Resource{{Address: "app/http_gateway/public", Module: "app", Kind: "scenery.http-gateway/v1", Name: "public"}}
	if _, diagnostics := resolveModuleInstanceInputs(root, packageResources, sources, module); !hasDiagnostic(diagnostics, "SCN3008") {
		t.Fatalf("wrong-kind input diagnostics: %#v", diagnostics)
	}
}
