package vnext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCapabilitiesAdvertiseOpenDraftSurfacesAsUnsupported(t *testing.T) {
	result, err := Compile("testdata/house")
	if err != nil {
		t.Fatal(err)
	}
	response := HandleAgentRequest(result, AgentRequest{ID: json.RawMessage(`1`), Method: "capabilities"})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	capabilities := response.Result.(map[string]any)
	surfaces, ok := capabilities["unsupported_draft_surfaces"].([]string)
	if !ok {
		t.Fatalf("unsupported draft surfaces = %#v", capabilities["unsupported_draft_surfaces"])
	}
	want := []string{
		"compatibility_source_and_wire_classification",
		"declarative_extensions",
		"entity_evolution_migration",
		"native_toolchain_identity",
		"patch_authorization_and_review_policy",
		"platform_listener_and_certificate_schemas",
		"provider_capability_vocabulary",
		"provider_deployment_plan_and_target_vocabulary",
		"registry_trust_and_revocation",
		"standard_library_catalog",
		"streaming_and_websockets",
		"workflow_runtime",
	}
	if len(surfaces) != len(want) {
		t.Fatalf("unsupported draft surfaces = %#v, want %#v", surfaces, want)
	}
	for _, item := range want {
		if !containsString(surfaces, item) {
			t.Errorf("missing unsupported draft surface %q in %#v", item, surfaces)
		}
	}
}

func TestDeclarativeExtensionSyntaxIsKnownButUnsupported(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	path := filepath.Join(root, "scenery.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(`

extension "maps" {
  source  = "registry.scenery.dev/geo/maps"
  version = ">= 1.4.0, < 2.0.0"
}

resource "maps.roof_model" "production" {
  config = { model_path = "models/roofmapnet" }
}
`)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, "SCN7001") {
		t.Fatalf("extension syntax did not produce unsupported_profile: %#v", result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "SCN7001" && diagnostic.Details["profile"] != "scenery.declarative-extensions/v1" {
			t.Fatalf("extension diagnostic details = %#v", diagnostic.Details)
		}
	}
	if hasDiagnostic(result.Diagnostics, "SCN1002") {
		t.Fatalf("extension syntax was classified as unknown: %#v", result.Diagnostics)
	}
}

func TestPlatformListenerDraftFieldsAreDescribedAndRejected(t *testing.T) {
	schema, ok := AgentSchema("scenery.deployment.http-listener/v1")
	if !ok {
		t.Fatal("listener authoring schema is missing")
	}
	fields, _ := schema["fields"].(map[string]any)
	for _, name := range []string{"certificate", "platform_identity"} {
		field, _ := fields[name].(map[string]any)
		if field["support_status"] != "unsupported_draft" || field["unsupported_capability"] != "platform_listener_and_certificate_schemas" {
			t.Errorf("listener field %s = %#v", name, field)
		}
	}

	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	path := filepath.Join(root, "scenery.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `    "scenery.inspection-core/v1",`, "    \"scenery.inspection-core/v1\",\n    \"scenery.deployment/v1\",", 1) + `
deployment "production" {
  environment = "production"
  http_gateway {
    target = http_gateway.public_api
    listener {
      host              = "api.example.test"
      port              = 443
      tls               = "required"
      certificate       = "manual"
      platform_identity = "edge-1"
    }
  }
}
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "SCN7009" {
			count++
		}
	}
	if result.Valid() || count < 2 {
		t.Fatalf("draft listener diagnostics = %#v", result.Diagnostics)
	}
	canonical := Resource{Address: "app/deployment/production", Kind: "scenery.deployment/v1", Spec: map[string]any{
		"http_gateway": map[string]any{"listener": map[string]any{"certificate": "manual", "platform_identity": "edge-1"}},
	}}
	if diagnostics := validateDeploymentDraftSurfaces(canonical); len(diagnostics) != 2 || diagnostics[0].Code != "SCN7009" || diagnostics[1].Code != "SCN7009" {
		t.Fatalf("canonical draft listener diagnostics = %#v", diagnostics)
	}

	deploymentSchema, _ := authoredResourceSourceSchema("deployment")
	_, err = renderAuthoredResourceBlock("deployment", []string{"production"}, map[string]any{
		"environment": "production",
		"http_gateway": map[string]any{"target": map[string]any{"$ref": "http_gateway.public_api"}, "listener": map[string]any{
			"host": "api.example.test", "port": 443, "certificate": "manual",
		}},
	}, deploymentSchema, "app")
	if err == nil || !strings.Contains(err.Error(), "capability_unavailable") {
		t.Fatalf("resource.create draft field error = %v", err)
	}
}
