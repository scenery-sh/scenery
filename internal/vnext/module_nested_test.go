package vnext

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNestedLocalModuleInstantiatesNamespacedExports(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"parent", "geometry"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1"]
}
application "nested_app" { version = "1.0.0" }
module "parent" { source = "./parent" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "parent", "scenery.package.scn"), `package "parent" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
module "geometry" { source = "../geometry" }
record "shape" {
  field "point" { type = module.geometry.point }
}
export "shape" { value = record.shape }
`)
	writeNestedModuleFile(t, filepath.Join(root, "geometry", "scenery.package.scn"), `package "geometry" {
  version         = "2.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "point" {
  field "x" { type = float64 }
  field "y" { type = float64 }
}
export "point" { value = record.point }
`)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	resources := resourcesByAddress(result.Manifest)
	for _, address := range []string{"app/module/parent", "parent/module/geometry", "parent/geometry/record/point", "parent/record/shape"} {
		if resources[address].Address == "" {
			t.Errorf("missing %s", address)
		}
	}
	shape := namedChildren(resources["parent/record/shape"].Spec, "field")[0]
	if refString(shape["type"]) != "parent/geometry/record/point" {
		t.Fatalf("nested exported type = %#v", shape["type"])
	}
	chain := resources["parent/geometry/record/point"].Origin.ModuleChain
	if len(chain) != 2 || chain[0] != "app/module/parent" || chain[1] != "parent/module/geometry" {
		t.Fatalf("nested module chain = %#v", chain)
	}
	parentExports := resources["app/module/parent"].Spec["exports"].(map[string]any)
	if refString(parentExports["shape"]) != "parent/record/shape" {
		t.Fatalf("parent exports = %#v", parentExports)
	}
}

func TestNestedExportedTypeGeneratesCompilableGoContractClosure(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"parent", "geometry"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	writeNestedModuleFile(t, filepath.Join(root, "go.mod"), "module example.test/cross\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\nreplace scenery.sh => "+filepath.ToSlash(repositoryRoot)+"\n")
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.go-implementation/v1"]
}
workspace {
  managed_generated_roots = ["parent/scenerycontract", "internal/scenerygen"]
}
go_module "application" {
  root = "."
  import_path = "example.test/cross"
}
go_toolchain "application" {
  version = "1.26.3"
  experiments = []
}
go_target "development" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.application
  module = go_module.application
  packages = ["./..."]
  cgo = "disabled"
}
application "cross_module" { version = "1.0.0" }
module "parent" { source = "./parent" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "parent", "scenery.package.scn"), `package "parent" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
  go_contract { import_path = "example.test/cross/parent" }
}
module "geometry" {
  source = "../geometry"
}
service "parent" {
  runtime = "go"
  implementation {
    constructor = "NewService"
  }
}
record "shape" {
  field "point" {
    type = module.geometry.point
  }
}
operation "inspect" {
  service = service.parent
  input = record.shape
  handler {
    method = "Inspect"
  }
  result "ok" {
    type = module.geometry.point
  }
}
export "shape" { value = record.shape }
`)
	writeNestedModuleFile(t, filepath.Join(root, "parent", "service.go"), `package parent

import (
  "context"
  contract "example.test/cross/parent/scenerycontract"
)

//scenery:service
type Service struct{}

func NewService(context.Context, contract.ParentConstructorInput) (*Service, error) {
  return &Service{}, nil
}

func (*Service) Inspect(context.Context, contract.InspectInput) (contract.InspectOutcome, error) {
  return contract.InspectOk{Value: contract.Point{}}, nil
}
`)
	writeNestedModuleFile(t, filepath.Join(root, "geometry", "scenery.package.scn"), `package "geometry" {
  version = "2.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "point" {
  field "x" { type = float64 }
  field "y" { type = float64 }
}
export "point" { value = record.point }
`)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	parent := resourcesByAddress(result.Manifest)["app/module/parent"]
	files, err := generateModuleContract(result, parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteSet(root, files); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./parent/scenerycontract")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated cross-module contract does not compile: %v\n%s", err, output)
	}
}

func TestPackageABIIsStableAcrossModuleAliasesWithTypeOnlyDependency(t *testing.T) {
	resourcesFor := func(instance string) []Resource {
		return []Resource{
			{Address: "app/module/" + instance, Module: "app", Kind: "scenery.module/v1", Name: instance, Spec: map[string]any{"package": map[string]any{"name": "parent", "go_contract": map[string]any{"import_path": "example.test/cross/parent"}}}},
			{Address: instance + "/module/geometry", Module: instance, Kind: "scenery.module/v1", Name: "geometry", Spec: map[string]any{"package": map[string]any{"name": "geometry"}}},
			{Address: instance + "/geometry/record/point", Module: instance + "/geometry", Kind: "scenery.record/v1", Name: "point", Spec: map[string]any{"field": map[string]any{"name": "x", "type": map[string]any{"$ref": "float64"}}}},
			{Address: instance + "/record/shape", Module: instance, Kind: "scenery.record/v1", Name: "shape", Spec: map[string]any{"field": map[string]any{"name": "point", "type": map[string]any{"$ref": instance + "/geometry/record/point"}}}},
			{Address: instance + "/operation/inspect", Module: instance, Kind: "scenery.operation/v1", Name: "inspect", Spec: map[string]any{"input": map[string]any{"$ref": instance + "/record/shape"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": instance + "/geometry/record/point"}}, "handler": map[string]any{"method": "Inspect"}}},
		}
	}
	first, second := resourcesFor("first"), resourcesFor("second")
	firstABI, err := packageABIRevision("example.test/cross/parent", moduleResources(first, "first"), first)
	if err != nil {
		t.Fatal(err)
	}
	secondABI, err := packageABIRevision("example.test/cross/parent", moduleResources(second, "second"), second)
	if err != nil {
		t.Fatal(err)
	}
	if firstABI != secondABI {
		t.Fatalf("package ABI changed across aliases: first=%s second=%s", firstABI, secondABI)
	}
}

func TestNestedModuleDependencyCycleFails(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cycle"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language { edition = "2027" }
application "cycle_app" { version = "1.0.0" }
module "cycle" { source = "./cycle" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "cycle", "scenery.package.scn"), `package "cycle" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
module "again" { source = "." }
`)
	result, err := Compile(root)
	if err != nil || !hasDiagnostic(result.Diagnostics, "SCN3009") {
		t.Fatalf("err=%v diagnostics=%#v", err, result.Diagnostics)
	}
}

func TestRootModulesResolveExportDependenciesTopologically(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"types", "consumer"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language { edition = "2027" }
application "root_modules" { version = "1.0.0" }
module "consumer" {
  source = "./consumer"
  inputs = { point = module.types.point }
}
module "types" { source = "./types" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "types", "scenery.package.scn"), `package "types" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "point" {
  field "x" {
    type = float64
  }
}
export "point" { value = record.point }
`)
	writeNestedModuleFile(t, filepath.Join(root, "consumer", "scenery.package.scn"), `package "consumer" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
input "point" { type = resource_ref("record") }
export "point" { value = var.point }
`)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	exports := resourcesByAddress(result.Manifest)["app/module/consumer"].Spec["exports"].(map[string]any)
	if refString(exports["point"]) != "types/record/point" {
		t.Fatalf("consumer exports = %#v", exports)
	}
}

func writeNestedModuleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
