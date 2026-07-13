package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCompilerSeparatesAuthoredInputsAndDefaultsAcrossViews(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	path := filepath.Join(root, "scenery.scn")
	sourceBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sourceBytes = append(sourceBytes, []byte(`

authorization "member" {
  principal = std.type.authenticated_principal
}
`)...)
	if err := os.WriteFile(path, sourceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", "scenery.package.scn")
	packageBytes, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageBytes = append(packageBytes, []byte(`

enum "state" {
  value "ready" {
  }
}

union "choice" {
  discriminator = "kind"

  variant "text" {
    type = record.process_scene_input
  }
}
`)...)
	if err := os.WriteFile(packagePath, packageBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	source, _ := result.ManifestForView("source")
	effective, _ := result.ManifestForView("effective")
	sourceBinding := resourcesByAddress(source)["house/binding/process_scene_http"]
	effectiveBinding := resourcesByAddress(effective)["house/binding/process_scene_http"]
	if got := refString(sourceBinding.Spec["gateway"]); got != "var.gateway" {
		t.Fatalf("source gateway = %q, want authored var.gateway", got)
	}
	if got := refString(effectiveBinding.Spec["gateway"]); got != "http_gateway.public_api" {
		t.Fatalf("effective gateway = %q, want resolved input", got)
	}
	if field := effectiveBinding.Origin.FieldProvenance["/spec/gateway"]; field.Kind != "module_input" || field.Input != "var.gateway" || field.ProvidedBy != "app/http_gateway/public_api" {
		t.Fatalf("resource-valued module input provenance = %#v", field)
	}
	sourceModule := resourcesByAddress(source)["app/module/house"]
	effectiveModule := resourcesByAddress(effective)["app/module/house"]
	if got := refString(sourceModule.Spec["exports"].(map[string]any)["service"]); got != "service.house" {
		t.Fatalf("source export = %q, want authored service.house", got)
	}
	if got := refString(effectiveModule.Spec["exports"].(map[string]any)["service"]); got != "house/service/house" {
		t.Fatalf("effective export = %q, want canonical resource address", got)
	}
	if field := sourceModule.Origin.FieldProvenance["/spec/inputs/gateway"]; field.Input != "http_gateway.public_api" || field.ProvidedBy != "app/http_gateway/public_api" {
		t.Fatalf("source module input reference provenance = %#v", field)
	}
	if field := effectiveModule.Origin.FieldProvenance["/spec/exports/service"]; field.Input != "service.house" || field.ProvidedBy != "house/service/house" {
		t.Fatalf("effective export provenance = %#v", field)
	}
	for _, path := range []string{"/spec/interface_inputs/gateway/type", "/spec/export_metadata/service/value"} {
		field := sourceModule.Origin.FieldProvenance[path]
		if field.Kind != "authored" || field.DeclaredAt == nil {
			t.Fatalf("source package-interface provenance %s = %#v", path, field)
		}
	}
	operation := resourcesByAddress(effective)["house/operation/process_scene"]
	if field := operation.Origin.FieldProvenance["/spec/service"]; field.Input != "service.house" || field.ProvidedBy != "house/service/house" {
		t.Fatalf("direct reference provenance = %#v", field)
	}
	sourceAuthorization := resourcesByAddress(source)["app/authorization/member"]
	effectiveAuthorization := resourcesByAddress(effective)["app/authorization/member"]
	if sourceAuthorization.Spec["strategy"] != nil {
		t.Fatalf("source authorization contains default: %#v", sourceAuthorization.Spec["strategy"])
	}
	if effectiveAuthorization.Spec["strategy"] != "deny_unless_allowed" {
		t.Fatalf("effective authorization strategy = %#v", effectiveAuthorization.Spec["strategy"])
	}
	strategyOrigin := effectiveAuthorization.Origin.FieldProvenance["/spec/strategy"]
	if strategyOrigin.Kind != "default" || strategyOrigin.ProvidedBy != "spec" {
		t.Fatalf("authorization default provenance = %#v", strategyOrigin)
	}
	for address, field := range map[string]string{
		"house/record/process_scene_input": "unknown_fields",
		"house/enum/state":                 "open",
		"house/union/choice":               "open",
	} {
		if resourcesByAddress(source)[address].Spec[field] != nil {
			t.Fatalf("source %s contains default %s", address, field)
		}
		effectiveResource := resourcesByAddress(effective)[address]
		if effectiveResource.Spec[field] == nil {
			t.Fatalf("effective %s omits default %s", address, field)
		}
		origin := effectiveResource.Origin.FieldProvenance["/spec/"+field]
		if origin.Kind != "default" || origin.ProvidedBy != "spec" {
			t.Fatalf("%s default provenance = %#v", address, origin)
		}
	}
}

func TestCompilerExplainsPackageDefaultAndModuleInputFieldProvenance(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	rootPath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = bytes.ReplaceAll(rootSource, []byte("    \"scenery.go-implementation/v1\",\n"), nil)
	rootSource = bytes.ReplaceAll(rootSource, []byte("    \"scenery.runtime-http\",\n"), nil)
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", "scenery.package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = append(packageSource, []byte(`

input "roof_model_path" {
  type    = relative_path
  default = "models/default"
}
`)...)
	packageSource = bytes.Replace(packageSource, []byte("  implementation {\n    constructor = \"NewService\"\n  }"), []byte("  implementation {\n    constructor = \"NewService\"\n  }\n\n  config {\n    model_path = var.roof_model_path\n  }"), 1)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	source, _ := result.ManifestForView("source")
	effective, _ := result.ManifestForView("effective")
	if got := refString(resourcesByAddress(source)["house/service/house"].Spec["config"].(map[string]any)["model_path"]); got != "var.roof_model_path" {
		t.Fatalf("source config = %q", got)
	}
	field := resourcesByAddress(effective)["house/service/house"].Origin.FieldProvenance["/spec/config/model_path"]
	if field.Kind != "package_default" || field.Input != "var.roof_model_path" || field.ProvidedBy == "" {
		t.Fatalf("package-default provenance = %#v", field)
	}
	if !containsString(field.Transformations, "contextual_relative_path") {
		t.Fatalf("package-default transformations = %#v", field.Transformations)
	}
	rootSource = bytes.Replace(rootSource, []byte("    gateway = http_gateway.public_api\n"), []byte("    gateway         = http_gateway.public_api\n    roof_model_path = relative_path(\"models/custom\")\n"), 1)
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile caller input: %v diagnostics=%#v", err, result.Diagnostics)
	}
	effective, _ = result.ManifestForView("effective")
	field = resourcesByAddress(effective)["house/service/house"].Origin.FieldProvenance["/spec/config/model_path"]
	if field.Kind != "module_input" || field.Input != "var.roof_model_path" || field.ProvidedBy != "app/module/house" {
		t.Fatalf("module-input provenance = %#v", field)
	}
	var inputValueRange Range
	for _, source := range result.Sources {
		for _, block := range source.Blocks {
			if block.Type == "module" && len(block.Labels) == 1 && block.Labels[0] == "house" {
				inputValueRange = block.Attributes["inputs"].ValueRanges["/roof_model_path"]
			}
		}
	}
	if field.DeclaredAt == nil || *field.DeclaredAt != inputValueRange || inputValueRange.Start.ByteOffset == 0 {
		t.Fatalf("module-input declared range = %#v, want exact object value %#v", field.DeclaredAt, inputValueRange)
	}
}

func TestCompilerFieldProvenanceUsesExistingRFC6901Pointers(t *testing.T) {
	for _, fixture := range []string{"house", "native"} {
		t.Run(fixture, func(t *testing.T) {
			result, err := Compile(filepath.Join("testdata", fixture))
			if err != nil || !result.Valid() {
				t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
			}
			for _, view := range []string{"source", "effective", "expanded"} {
				manifest, viewErr := result.ManifestForView(view)
				if viewErr != nil {
					t.Fatal(viewErr)
				}
				for _, resource := range manifest.Resources {
					for path := range resource.Origin.FieldProvenance {
						if _, exists := resourcePointerValue(resource, path); !exists {
							t.Fatalf("%s %s provenance path %s does not address its graph value", view, resource.Address, path)
						}
					}
				}
			}
		})
	}
}

func TestSourceViewPreservesModuleExportExpressionsBeforeDependencyResolution(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"types", "consumer"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `application "module_views" {}
module "types" { source = "./types" }
module "consumer" {
  source = "./consumer"
  inputs = { shape = module.types.shape }
}
`)
	writeNestedModuleFile(t, filepath.Join(root, "types", "scenery.package.scn"), `package "types" {
}
record "shape" {
  field "id" {
    type = string
  }
}
export "shape" {
  value = record.shape
}
`)
	writeNestedModuleFile(t, filepath.Join(root, "consumer", "scenery.package.scn"), `package "consumer" {
}
input "shape" { type = resource_ref("record") }
`)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	source, _ := result.ManifestForView("source")
	effective, _ := result.ManifestForView("effective")
	sourceInput := resourcesByAddress(source)["app/module/consumer"].Spec["inputs"].(map[string]any)["shape"]
	effectiveInput := resourcesByAddress(effective)["app/module/consumer"].Spec["inputs"].(map[string]any)["shape"]
	if got := refString(sourceInput); got != "module.types.shape" {
		t.Fatalf("source module input = %q", got)
	}
	if got := refString(effectiveInput); got != "types/record/shape" {
		t.Fatalf("effective module input = %q", got)
	}
	field := resourcesByAddress(effective)["app/module/consumer"].Origin.FieldProvenance["/spec/inputs/shape"]
	if field.Kind != "module_export" || field.Input != "module.types.shape" || field.ProvidedBy != "app/module/types/export/shape" {
		t.Fatalf("resolved module-export provenance = %#v", field)
	}
}
