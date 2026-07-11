package vnext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCSTMutationUpdatesNestedBlockAndObjectLeavesWithoutLosingComments(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	rootPath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), "  inputs = {\n    gateway = http_gateway.public_api", "  inputs = {\n    # gateway stays typed\n    gateway = http_gateway.public_api", 1))
	rootSource = append(rootSource, []byte(`

http_gateway "secondary" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
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
	packageSource = []byte(strings.Replace(string(packageSource), "    path          = \"/house/process\"", "    # route comment survives\n    path          = \"/house/process\"", 1))
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	module := resourcesByAddress(base.Manifest)["app/module/house"]
	if err := mutateResourceValue(root, base, module, SemanticOperation{Op: "value.set", Path: "/spec/inputs/gateway", Value: map[string]any{"$ref": "http_gateway.secondary"}}); err != nil {
		t.Fatal(err)
	}
	afterModule, err := Compile(root)
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
	value, ok := resourcePointerValue(resource, "/spec/result/accepted/type")
	if !ok || refString(value) != "record.accepted" {
		t.Fatalf("value=%#v ok=%t", value, ok)
	}
}

func TestChangePlanAppliesNestedBlockEditAtomically(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision),
		Caller: "test", Operations: []SemanticOperation{{
			Op: "value.set", Address: "house/binding/process_scene_http", Path: "/spec/http/path", Value: "/house/process-v2",
			Precondition: &ChangePrecondition{Equals: "/house/process"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ApplyChangePlanWithOptions(root, plan, ApplyOptions{
		ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: stringPointer(base.Manifest.ContractRevision), Caller: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.WorkspaceRevision != plan.PredictedWorkspaceRevision || receipt.ContractRevision != plan.PredictedContractRevision {
		t.Fatalf("receipt=%#v plan=%#v", receipt, plan)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile after apply: %v diagnostics=%#v", err, result.Diagnostics)
	}
	httpSpec := resourcesByAddress(result.Manifest)["house/binding/process_scene_http"].Spec["http"].(map[string]any)
	if stringValue(httpSpec["path"]) != "/house/process-v2" {
		t.Fatalf("HTTP path = %#v", httpSpec["path"])
	}
}
