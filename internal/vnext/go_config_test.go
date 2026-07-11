package vnext

import "testing"

func TestGoServiceConfigSchemaComesFromTypedPackageInputs(t *testing.T) {
	sources := []*Source{{Blocks: []*Block{
		{Type: "input", Labels: []string{"model_path"}, Attributes: map[string]Expression{
			"type": {Raw: "relative_path"},
		}},
		{Type: "input", Labels: []string{"token"}, Attributes: map[string]Expression{
			"type":      {Raw: `resource_ref("secret")`},
			"sensitive": {Kind: "literal", Value: true},
		}},
	}}}
	service := Resource{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Spec: map[string]any{
		"runtime": "go", "config": map[string]any{
			"model_path": map[string]any{"$ref": "var.model_path"},
			"token":      map[string]any{"$ref": "var.token"},
		},
	}}
	resources, diagnostics := enrichPackageGoServiceSchemas([]Resource{service}, sources)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	schema := namedChildren(resources[0].Spec, "config_schema")
	if len(schema) != 2 || schema[0]["name"] != "model_path" || schema[0]["type"] != "relative_path" || schema[1]["name"] != "token" || schema[1]["sensitive"] != true {
		t.Fatalf("config schema = %#v", schema)
	}

	resources[0].Spec["config"] = map[string]any{
		"model_path": map[string]any{"$scalar": "relative_path", "value": "models/roof"},
		"token":      map[string]any{"$ref": "secret.provider_token"},
	}
	resources = append(resources, Resource{Address: "app/secret/provider_token", Module: "app", Kind: "scenery.secret/v1", Name: "provider_token"})
	if diagnostics := validateGoServiceConfiguration(resources); hasErrors(diagnostics) {
		t.Fatalf("resolved config diagnostics = %#v", diagnostics)
	}

	resources[0].Spec["config"].(map[string]any)["token"] = "plaintext"
	if diagnostics := validateGoServiceConfiguration(resources); !diagnosticsContain(diagnostics, "SCN4001") {
		t.Fatalf("plaintext secret diagnostics = %#v", diagnostics)
	}
}
