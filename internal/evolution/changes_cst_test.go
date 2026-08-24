package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestCSTMutationUpdatesNestedBlockAndObjectLeavesWithoutLosingComments(t *testing.T) {
	root := t.TempDir()
	rootPath := filepath.Join(root, testAppFilename)
	writeNestedModuleFile(t, rootPath, `application "change_test" {}
http_gateway "public" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
http_gateway "secondary" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
module "house" {
  source = "./house"
  inputs = {
    # gateway stays typed
    gateway = http_gateway.public
  }
}
`)
	packagePath := filepath.Join(root, "house", testPackageFilename)
	writeNestedModuleFile(t, packagePath, `package "house" {}
input "gateway" {
  type = resource_ref("http_gateway")
}
service "house" {
  runtime = "test"
  implementation {
    constructor = "NewService"
  }
}
operation "process_scene" {
  service = service.house
  input   = std.type.unit
  handler {
    method = "ProcessScene"
  }
}
execution "process_scene_direct" {
  operation = operation.process_scene
  mode      = "direct"
}
binding "process_scene_http" {
  gateway        = var.gateway
  operation      = operation.process_scene
  execution      = execution.process_scene_direct
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "GET"
    # route comment survives
    path          = "/house/process"
    codec_profile = std.codec.http_json_v1
  }
}
`)
	base, err := compiler.Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	module := resourcesByAddress(base.Manifest)["app/module/house"]
	if err := mutateResourceValue(root, base, module, SemanticOperation{Op: "value.set", Path: "/spec/inputs/gateway", Value: map[string]any{"$ref": "http_gateway.secondary"}}); err != nil {
		t.Fatal(err)
	}
	afterModule, err := compiler.Compile(root)
	if err != nil || !afterModule.Valid() {
		t.Fatalf("compile after module edit: %v diagnostics=%#v", err, afterModule.Diagnostics)
	}
	binding := resourcesByAddress(afterModule.Manifest)["house/binding/process_scene_http"]
	if refString(binding.Spec["gateway"]) != "http_gateway.secondary" {
		t.Fatalf("binding gateway = %#v", binding.Spec["gateway"])
	}
	if err := mutateResourceValue(root, afterModule, binding, SemanticOperation{Op: "value.set", Path: "/spec/http/path", Value: "/house/process-v2"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# route comment survives", `"/house/process-v2"`} {
		if !strings.Contains(string(after), want) {
			t.Errorf("package source missing %q:\n%s", want, after)
		}
	}
	rootAfter, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# gateway stays typed", "gateway = http_gateway.secondary"} {
		if !strings.Contains(string(rootAfter), want) {
			t.Errorf("root source missing %q:\n%s", want, rootAfter)
		}
	}
}

func TestSemanticPointerAddressesNamedChildren(t *testing.T) {
	resource := Resource{Spec: map[string]any{"result": []any{
		map[string]any{"name": "created", "type": map[string]any{"$ref": "record.created"}},
		map[string]any{"name": "accepted", "type": map[string]any{"$ref": "record.accepted"}},
	}}}
	value, ok := compiler.ResourcePointerValue(resource, "/spec/result/accepted/type")
	if !ok || refString(value) != "record.accepted" {
		t.Fatalf("value=%#v ok=%t", value, ok)
	}
}

func TestCSTMutationRecognizesAssistantAndMCPSingletonBlocks(t *testing.T) {
	tests := []struct {
		kind, field string
	}{
		{"scenery.binding", "mcp"},
		{"scenery.mcp-connection", "auth"},
		{"scenery.mcp-connection", "tools"},
		{"scenery.assistant", "implementation"},
		{"scenery.assistant", "surface"},
	}
	for _, test := range tests {
		if !semanticSingularBlockField(test.kind, test.field) {
			t.Errorf("semantic block %s.%s was not recognized", test.kind, test.field)
		}
	}
	if semanticSingularBlockField("scenery.mcp-server", "capability") {
		t.Error("repeatable MCP capability block was treated as singleton")
	}
}

func TestChangePlanAppliesNestedBlockEditAtomically(t *testing.T) {
	t.Parallel()

	root, base := newHouseChangeFixture(t, `service "house" {
  runtime = "test"
  implementation {
    constructor = "NewService"
  }
}
`)
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: new(base.Manifest.ContractRevision),
		Caller: "test", Operations: []SemanticOperation{{
			Op: "value.set", Address: "house/service/house", Path: "/spec/implementation/constructor", Value: "NewServiceV2",
			Precondition: &ChangePrecondition{Equals: "NewService"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 1 || plan.Edits[0].Path != filepath.ToSlash(filepath.Join("house", testPackageFilename)) {
		t.Fatalf("nested block edits = %#v", plan.Edits)
	}
	rollback, finalize, err := commitPlannedEdits(root, plan.Edits, "")
	if err != nil {
		t.Fatal(err)
	}
	defer rollback()
	source, err := os.ReadFile(filepath.Join(root, "house", testPackageFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `constructor = "NewServiceV2"`) {
		t.Fatalf("nested block source:\n%s", source)
	}
	finalize()
}
