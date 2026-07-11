package vnext

import "testing"

func TestGoContractOwnershipRequiresServicesAndUniqueOwners(t *testing.T) {
	module := func(name, source, importPath string) Resource {
		metadata := map[string]any{"name": name}
		if importPath != "" {
			metadata["go_contract"] = map[string]any{"import_path": importPath}
		}
		return Resource{Address: "app/module/" + name, Module: "app", Kind: "scenery.module/v1", Name: name, Spec: map[string]any{"source": source, "workspace_package_root": source, "package": metadata}}
	}
	service := func(module string) Resource {
		return Resource{Address: module + "/service/api", Module: module, Kind: "scenery.service/v1", Name: "api", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"runtime": "go"}}
	}
	for _, test := range []struct {
		name      string
		resources []Resource
		code      string
	}{
		{name: "missing", resources: []Resource{module("missing", "missing", ""), service("missing")}, code: "SCN6120"},
		{name: "forbidden", resources: []Resource{module("types", "types", "example.test/types")}, code: "SCN6120"},
		{name: "duplicate", resources: []Resource{module("one", "one", "example.test/shared"), service("one"), module("two", "two", "example.test/shared"), service("two")}, code: "SCN6121"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if diagnostics := validateGoContractOwnership(nil, test.resources); !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("missing %s in %#v", test.code, diagnostics)
			}
		})
	}
	valid := []Resource{module("api", "api", "example.test/api"), service("api"), module("types", "types", "")}
	if diagnostics := validateGoContractOwnership(nil, valid); hasErrors(diagnostics) {
		t.Fatalf("valid ownership diagnostics = %#v", diagnostics)
	}
}
