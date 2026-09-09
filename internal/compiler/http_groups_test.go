package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func httpGroupModule(instance, root string) Resource {
	return Resource{Address: moduleResourceAddress(instance), Kind: "scenery.module", Spec: map[string]any{"workspace_package_root": root}}
}

func httpGroupBinding(instance, route string) Resource {
	return Resource{Address: instance + "/binding/get", Module: instance, Kind: "scenery.binding", Spec: map[string]any{
		"protocol": "http", "gateway": map[string]any{"$ref": "app/http_gateway/public"},
		"http": map[string]any{"method": "GET", "path": route},
	}}
}

func TestHTTPDirectoryGroupsComposeLiteralPackageParents(t *testing.T) {
	for _, tc := range []struct{ root, route, base, want string }{
		{"maps", "/maps/list", "/v1", "/v1/maps/list"},
		{"group1/maps", "/maps/list", "/", "/group1/maps/list"},
		{"group1/maps", "/maps/list", "/v1", "/v1/group1/maps/list"},
		{"group1/admin/maps", "/maps/list", "/v1", "/v1/group1/admin/maps/list"},
		{"group1/maps", "/group1/maps/list", "/group1", "/group1/group1/group1/maps/list"},
		{"group1/maps", "/", "/v1", "/v1/group1"},
		{"group1/maps", "/files/{path...}", "/", "/group1/files/{path...}"},
	} {
		resources := []Resource{httpGroupModule("alias", tc.root), httpGroupBinding("alias", tc.route)}
		if diagnostics := applyHTTPDirectoryGroups(resources); len(diagnostics) != 0 {
			t.Fatalf("%s: %#v", tc.root, diagnostics)
		}
		got := joinHTTPPath(tc.base, stringValue(resources[1].Spec["http"].(map[string]any)["path"]))
		if got != tc.want {
			t.Errorf("root=%s route=%s base=%s: got %s, want %s", tc.root, tc.route, tc.base, got, tc.want)
		}
	}
}

func TestHTTPDirectoryGroupsRejectURLSyntaxAndDoNotRepairPaths(t *testing.T) {
	for _, group := range []string{"{tenant}", "a%2fb", "a?b", "a#b", "a b", "../outside", "a/*", "a/../b"} {
		resources := []Resource{httpGroupModule("maps", group+"/maps"), httpGroupBinding("maps", "/maps")}
		if diagnostics := applyHTTPDirectoryGroups(resources); !hasDiagnostic(diagnostics, "SCN2102") {
			t.Errorf("group=%q: %#v", group, diagnostics)
		}
	}
	for _, route := range []string{"maps", "/maps//list", "/maps/../list"} {
		resources := []Resource{httpGroupModule("maps", "group1/maps"), httpGroupBinding("maps", route)}
		if diagnostics := applyHTTPDirectoryGroups(resources); !hasDiagnostic(diagnostics, "SCN2102") {
			t.Errorf("route=%q: %#v", route, diagnostics)
		}
	}
}

func TestHTTPDirectoryGroupsExcludeRegistryAncestryAndNonHTTPBindings(t *testing.T) {
	parent := httpGroupModule("registry", "")
	parent.Spec["locked_integrity"] = "sha256:locked"
	resources := []Resource{parent, httpGroupModule("registry/maps", ".scenery/modules/cache/maps"), httpGroupBinding("registry/maps", "/maps")}
	if diagnostics := applyHTTPDirectoryGroups(resources); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if got := resources[2].Spec["http"].(map[string]any)["path"]; got != "/maps" {
		t.Fatalf("registry cache affected path: %v", got)
	}
	resources = []Resource{httpGroupModule("maps", "group1/maps"), httpGroupBinding("maps", "/maps")}
	resources[1].Spec["protocol"] = "internal"
	applyHTTPDirectoryGroups(resources)
	if got := resources[1].Spec["http"].(map[string]any)["path"]; got != "/maps" {
		t.Fatalf("non-HTTP binding changed: %v", got)
	}
}

func TestHTTPDirectoryGroupsFeedCRUDAndCollisionValidation(t *testing.T) {
	crud := httpGroupBinding("maps", "/items")
	crud.Kind = "scenery.crud"
	resources := []Resource{httpGroupModule("maps", "group1/maps"), crud}
	applyHTTPDirectoryGroups(resources)
	httpSpec := resources[1].Spec["http"].(map[string]any)
	generated := expandCRUDHTTPBinding(resources[1], Resource{}, Resource{}, "list", "list", "direct", nil, nil, httpSpec, func(string, string) Origin { return Origin{} })
	if got := generated.Spec["http"].(map[string]any)["path"]; got != "/group1/items" {
		t.Fatalf("CRUD path = %v", got)
	}
	resources = []Resource{
		{Address: "app/http_gateway/public", Kind: "scenery.http-gateway", Spec: map[string]any{"base_path": "/v1"}},
		httpGroupModule("maps", "group1/maps"), httpGroupBinding("maps", "/items"),
		httpGroupModule("geometry", "group2/geometry"), httpGroupBinding("geometry", "/items"),
	}
	applyHTTPDirectoryGroups(resources)
	if diagnostics := validateHTTPResources(resources); hasDiagnostic(diagnostics, "SCN2002") {
		t.Fatalf("different groups collided: %#v", diagnostics)
	}
	resources = append(resources, httpGroupBinding("other", "/group1/items"))
	if diagnostics := validateHTTPResources(resources); !hasDiagnostic(diagnostics, "SCN2002") {
		t.Fatalf("effective grouped collision was missed: %#v", diagnostics)
	}
}

func TestCompilerHTTPDirectoryGroupsPreserveSourceAndTrackMoves(t *testing.T) {
	root := t.TempDir()
	for relative, source := range map[string]string{
		"app.scn": `application "groups" {}
http_gateway "public" {
  exposure = "internal"
  base_path = "/v1"
  cors = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded = std.forwarded_headers.reject
}
module "parent" {
  source = "./group1"
  inputs = { gateway = http_gateway.public }
}
`,
		"group1/package.scn": `package "parent" {}
input "gateway" { type = resource_ref("http_gateway") }
module "alias" {
  source = "./nested/maps"
  inputs = { gateway = var.gateway }
}
`,
		"group1/nested/maps/package.scn": `package "maps" {
  go_contract { import_path = "example.test/app/group1/nested/maps" }
}
input "gateway" { type = resource_ref("http_gateway") }
service "maps" {
  runtime = "go"
  implementation { constructor = "NewService" }
}
operation "get" {
  service = service.maps
  input = std.type.unit
  handler { method = "Get" }
  result "ok" { type = string }
}
execution "direct" {
  operation = operation.get
  mode = "direct"
}
binding "get" {
  gateway = var.gateway
  operation = operation.get
  execution = execution.direct
  protocol = "http"
  delivery = "call"
  authentication = std.authentication.none
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  http {
    method = "GET"
    path = "/maps/list"
    codec_profile = std.codec.http_json_v1
    response "ok" {
      when = result.ok
      status = 200
      body {
        codec = "json"
        from = result.ok
      }
    }
  }
}
`,
	} {
		filename := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		writeNestedModuleFile(t, filename, source)
	}
	before, err := Compile(root)
	if err != nil || !before.Valid() {
		t.Fatalf("compile: %v, %#v", err, before.Diagnostics)
	}
	for view, want := range map[string]string{"source": "/maps/list", "effective": "/group1/nested/maps/list", "expanded": "/group1/nested/maps/list"} {
		manifest, _ := before.ManifestForView(view)
		binding := resourcesByAddress(manifest)["parent/alias/binding/get"]
		if got := binding.Spec["http"].(map[string]any)["path"]; got != want {
			t.Fatalf("%s path=%v want %s", view, got, want)
		}
		if view != "source" {
			field := binding.Origin.FieldProvenance["/spec/http/path"]
			if field.Kind != "derived" || field.DeclaredAt == nil || !containsString(field.Transformations, "directory_group_prefix") {
				t.Fatalf("group provenance = %#v", field)
			}
		}
	}
	if err := os.Rename(filepath.Join(root, "group1"), filepath.Join(root, "group2")); err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(filepath.Join(root, "app.scn"))
	if err != nil {
		t.Fatal(err)
	}
	writeNestedModuleFile(t, filepath.Join(root, "app.scn"), strings.ReplaceAll(string(app), "./group1", "./group2"))
	after, err := Compile(root)
	if err != nil || !after.Valid() {
		t.Fatalf("moved compile: %v, %#v", err, after.Diagnostics)
	}
	if before.Manifest.ContractRevision == after.Manifest.ContractRevision || before.HTTPSurfaceRevisions["app/http_gateway/public"] == after.HTTPSurfaceRevisions["app/http_gateway/public"] {
		t.Fatal("directory move did not change contract/HTTP revisions")
	}
}
