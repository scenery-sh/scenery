package vnext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoServiceConfigCompilesFromAuthoredSource(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)

	rootPath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"scenery.runtime-http/v1",`, `"scenery.runtime-http/v1",
    "scenery.deployment/v1",`, 1))
	rootSource = []byte(strings.Replace(string(rootSource), `gateway = http_gateway.public_api`, `gateway              = http_gateway.public_api
    roof_model_path      = "models/roof"
    process_concurrency  = 4
    provider_token       = secret.provider_token`, 1))
	rootSource = append(rootSource, []byte(`

provider "vault" {
  source  = "registry.scenery.dev/core/vault"
  version = ">= 1.0.0, < 2.0.0"
}

secret_store "production" {
  provider  = provider.vault
  lifecycle = "external"
  require_capabilities = ["secrets.resolve/v1"]
  config = { address = "https://vault.example.test" }
}

secret "provider_token" {
  store = secret_store.production
  key   = "house/provider-token"
}
`)...)
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}

	packagePath := filepath.Join(root, "house", "scenery.package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = []byte(strings.Replace(string(packageSource), `input "gateway" {
  type = resource_ref("http_gateway")
}`, `input "gateway" {
  type = resource_ref("http_gateway")
}

input "roof_model_path" {
  type  = relative_path
  phase = "deployment"
}

input "process_concurrency" {
  type  = uint32
  phase = "deployment"
}

input "provider_token" {
  type      = resource_ref("secret")
  phase     = "deployment"
  sensitive = true
}`, 1))
	packageSource = []byte(strings.Replace(string(packageSource), `  implementation {
    constructor = "NewService"
  }`, `  implementation {
    constructor = "NewService"
  }

  config {
    roof_model_path     = var.roof_model_path
    process_concurrency = var.process_concurrency
    provider_token      = var.provider_token
  }`, 1))
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}

	version, integrity, ok := BuiltinProviderLock("registry.scenery.dev/core/vault")
	if !ok {
		t.Fatal("builtin vault provider unavailable")
	}
	lock := fmt.Sprintf(`lock { schema = %q }
provider "vault" {
  source                    = "registry.scenery.dev/core/vault"
  version                   = %q
  integrity                 = %q
  compile_descriptor_digest = %q
  runtime_abi               = "scenery.secrets-runtime/v1"
  deployment_abi            = %q
}
`, LockfileSchema, version, integrity, integrity, deploymentProviderABI)
	if err := os.WriteFile(filepath.Join(root, "scenery.lock.scn"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile authored Go config: %v diagnostics=%#v", err, result.Diagnostics)
	}
	service := resourcesByAddress(result.Manifest)["house/service/house"]
	schema := namedChildren(service.Spec, "config_schema")
	if len(schema) != 3 || schema[0]["name"] != "process_concurrency" || schema[0]["phase"] != "deployment" || schema[1]["name"] != "provider_token" || schema[1]["sensitive"] != true || schema[2]["name"] != "roof_model_path" {
		t.Fatalf("config schema = %#v", schema)
	}
}

func TestGoServiceConfigRejectsInvalidDynamicAttributes(t *testing.T) {
	sources := []*Source{{Blocks: []*Block{
		{Type: "input", Labels: []string{"ProcessConcurrency"}, Attributes: map[string]Expression{
			"type":  {Raw: "uint32"},
			"phase": {Kind: "literal", Value: "runtime"},
		}},
	}}}
	service := Resource{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Spec: map[string]any{
		"runtime": "go", "config": map[string]any{
			"ProcessConcurrency": map[string]any{"$ref": "var.ProcessConcurrency"},
		},
	}}
	resources, diagnostics := enrichPackageGoServiceSchemas([]Resource{service}, sources)
	if !diagnosticsContain(diagnostics, "SCN3405") || !diagnosticsContain(diagnostics, "SCN3406") {
		t.Fatalf("dynamic config diagnostics = %#v resources=%#v", diagnostics, resources)
	}

	resources[0].Spec["config"] = map[string]any{"count": "four"}
	resources[0].Spec["config_schema"] = []any{map[string]any{"name": "count", "type": "uint32", "phase": "deployment"}}
	if diagnostics := validateGoServiceConfiguration(resources); !diagnosticsContain(diagnostics, "SCN3407") {
		t.Fatalf("resolved value diagnostics = %#v", diagnostics)
	}
}

func TestGoServiceConfigSourceRejectsNonLowerSnakeKey(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "house"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte(`language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1"]
}
application "config_test" { version = "1.0.0" }
module "house" { source = "./house" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "house", "scenery.package.scn"), []byte(`package "house" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
input "process_concurrency" { type = uint32 }
service "house" {
  runtime = "go"
  implementation { constructor = "NewService" }
  config { ProcessConcurrency = var.process_concurrency }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnosticsContain(result.Diagnostics, "SCN1017") {
		t.Fatalf("dynamic source diagnostics = %#v", result.Diagnostics)
	}
}

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
