package vnext

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChangePlanDoesNotWriteAndApplyIsRevisionBound(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "house", "scenery.package.scn")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations:            []SemanticOperation{{Op: "value.set", Address: "house/execution/process_scene_direct", Path: "/spec/timeout", Value: "45m", Precondition: &ChangePrecondition{Equals: "40m"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	afterPlan, _ := os.ReadFile(path)
	if string(before) != string(afterPlan) {
		t.Fatal("planning wrote source")
	}
	receipt, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.WorkspaceRevision != plan.PredictedWorkspaceRevision || receipt.ContractRevision != plan.PredictedContractRevision {
		t.Fatalf("receipt = %#v plan = %#v", receipt, plan)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err == nil {
		t.Fatal("stale plan applied twice")
	}
}

func TestChangeRenameUpdatesTypedReferences(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision), Operations: []SemanticOperation{{Op: "resource.rename", Address: "house/operation/process_scene", Value: "process_roof_scene"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "house", "scenery.package.scn"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsJSONText(b, "operation \"process_roof_scene\"") || !containsJSONText(b, "operation.process_roof_scene") {
		t.Fatalf("source:\n%s", b)
	}
}

func TestChangeCreateAddsTypedResource(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision), Operations: []SemanticOperation{{Op: "resource.create", Address: "app/authentication/test", Value: map[string]any{"provider": map[string]any{"$ref": "std.provider.standard_auth"}, "scheme": "session"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resourcesByAddress(result.Manifest)["app/authentication/test"]; !ok {
		t.Fatal("created resource missing")
	}
}

func TestChangeCreateAddsStructuredRecord(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{
			Op:      "resource.create",
			Address: "house/record/audit_entry",
			Value: map[string]any{
				"field": []any{
					map[string]any{"name": "message", "type": map[string]any{"$ref": "string"}, "min_length": 1},
					map[string]any{"name": "id", "type": map[string]any{"$ref": "uuid"}},
				},
				"unknown_fields": "reject",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	record := resourcesByAddress(result.Manifest)["house/record/audit_entry"]
	fields := namedChildren(record.Spec, "field")
	if len(fields) != 2 || stringValue(fields[0]["name"]) != "id" || stringValue(fields[1]["name"]) != "message" {
		t.Fatalf("created record fields = %#v", fields)
	}
}

func TestChangeCreateAddsStructuredOperation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1"]
}
application "catalog" { version = "1.0.0" }
workspace {
  managed_generated_roots = ["catalog/scenerycontract", "internal/scenerygen"]
}
go_module "application" {
  root = "."
  import_path = "example.test/catalog"
}
module "catalog" { source = "./catalog" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "catalog", "scenery.package.scn"), `package "catalog" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
  go_contract {
    import_path = "example.test/catalog/catalog"
  }
}
service "catalog" {
  runtime = "test"
  implementation {
    constructor = "NewService"
  }
}
record "lookup_input" {
  field "id" {
    type = uuid
  }
  field "region" {
    type = string
  }
}
record "lookup_result" {
  field "name" {
    type = string
  }
}
`)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{
			Op:      "resource.create",
			Address: "catalog/operation/lookup",
			Value: map[string]any{
				"service": map[string]any{"$ref": "catalog/service/catalog"},
				"input":   map[string]any{"$ref": "catalog/record/lookup_input"},
				"handler": map[string]any{"method": "Lookup"},
				"idempotency": map[string]any{
					"mode": "keyed",
					"key": []any{
						map[string]any{"$expression": "input.id"},
						map[string]any{"$expression": "input.region"},
					},
				},
				"result": map[string]any{"name": "found", "type": map[string]any{"$ref": "catalog/record/lookup_result"}},
				"error":  map[string]any{"name": "invalid", "type": map[string]any{"$ref": "std.type.problem"}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		written, _ := os.ReadFile(filepath.Join(root, "catalog", "scenery.package.scn"))
		t.Fatalf("compile: %v diagnostics=%#v\nsource:\n%s", err, result.Diagnostics, written)
	}
	operation := resourcesByAddress(result.Manifest)["catalog/operation/lookup"]
	keys, _ := operation.Spec["idempotency"].(map[string]any)["key"].([]any)
	if stringValue(operation.Spec["handler"].(map[string]any)["method"]) != "Lookup" || len(namedChildren(operation.Spec, "result")) != 1 || len(namedChildren(operation.Spec, "error")) != 1 ||
		len(keys) != 2 || expressionText(keys[0]) != "input.id" || expressionText(keys[1]) != "input.region" {
		t.Fatalf("created operation = %#v", operation.Spec)
	}
}

func TestChangeCreateRejectsScalarIdempotencyKey(t *testing.T) {
	tests := []struct {
		name, message string
		key           any
	}{
		{name: "scalar", key: map[string]any{"$expression": "input.id"}, message: "key: must be a list"},
		{name: "computed", key: []any{map[string]any{"$expression": "input.id + input.region"}}, message: "must reference a direct input field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := renderAuthoredResourceBlock("idempotency", nil, map[string]any{
				"mode": "keyed",
				"key":  test.key,
			}, operationIdempotencySourceSchema, "house")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("invalid idempotency key error = %v", err)
			}
		})
	}
}

func TestChangeCreateLowersCanonicalReferenceAndDurationScalar(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{
			Op:      "resource.create",
			Address: "house/execution/process_scene_copy",
			Value: map[string]any{
				"operation": map[string]any{"$ref": "house/operation/process_scene"},
				"mode":      "direct",
				"timeout":   map[string]any{"$scalar": "duration", "nanoseconds": "2400000000000"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	execution := resourcesByAddress(result.Manifest)["house/execution/process_scene_copy"]
	timeout, _ := execution.Spec["timeout"].(map[string]any)
	written, err := os.ReadFile(filepath.Join(root, "house", "scenery.package.scn"))
	if err != nil {
		t.Fatal(err)
	}
	if refString(execution.Spec["operation"]) != "operation.process_scene" || timeout["nanoseconds"] != "2400000000000" || strings.Count(string(written), `"40m"`) != 2 {
		t.Fatalf("created execution = %#v\nsource:\n%s", execution.Spec, written)
	}
}

func TestChangeCreateAddsStructuredHTTPBinding(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{
			Op:      "resource.create",
			Address: "house/binding/process_scene_copy",
			Value: map[string]any{
				"gateway":        map[string]any{"$ref": "var.gateway"},
				"operation":      map[string]any{"$ref": "operation.process_scene"},
				"execution":      map[string]any{"$ref": "execution.process_scene_direct"},
				"protocol":       "http",
				"delivery":       "call",
				"authentication": map[string]any{"$ref": "std.authentication.none"},
				"authorization":  map[string]any{"$ref": "std.authorization.public"},
				"pipeline":       map[string]any{"$ref": "std.pipeline.empty"},
				"http": map[string]any{
					"method":        "POST",
					"path":          "/house/process-copy",
					"codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"},
					"body":          map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.process_scene.input"}},
					"response": map[string]any{
						"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": 200,
						"body": map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.processed"}},
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	source, err := result.ManifestForView("source")
	if err != nil {
		t.Fatal(err)
	}
	binding := resourcesByAddress(source)["house/binding/process_scene_copy"]
	httpSpec, _ := binding.Spec["http"].(map[string]any)
	responses := namedChildren(httpSpec, "response")
	if stringValue(httpSpec["path"]) != "/house/process-copy" || len(responses) != 1 || responses[0]["body"] == nil {
		t.Fatalf("created HTTP binding = %#v", binding.Spec)
	}
}

func TestChangeCreateAddsServiceDynamicConfigBlock(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	rootPath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = bytes.Replace(rootSource, []byte(`    "scenery.go-implementation/v1",
    "scenery.runtime-http/v1",
`), nil, 1)
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", "scenery.package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = append(packageSource, []byte(`
input "model_path" {
  type = relative_path
  default = relative_path("models/default")
}
`)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{
			Op:      "resource.create",
			Address: "house/service/audit",
			Value: map[string]any{
				"runtime":        "go",
				"implementation": map[string]any{"constructor": "NewAuditService"},
				"config":         map[string]any{"model_path": map[string]any{"$ref": "var.model_path"}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	service := resourcesByAddress(result.Manifest)["house/service/audit"]
	configSchema := namedChildren(service.Spec, "config_schema")
	written, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(configSchema) != 1 || stringValue(configSchema[0]["name"]) != "model_path" || !strings.Contains(string(written), "config {\n    model_path = var.model_path\n  }") {
		t.Fatalf("created service config = %#v\nsource:\n%s", service.Spec, written)
	}
}

func TestChangeCreateResolvesNestedModuleSource(t *testing.T) {
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
workspace {
  managed_generated_roots = ["parent/scenerycontract", "geometry/scenerycontract", "internal/scenerygen"]
}
go_module "application" {
  root = "."
  import_path = "example.test/nested"
}
application "nested" { version = "1.0.0" }
module "parent" { source = "./parent" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "parent", "scenery.package.scn"), `package "parent" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
  go_contract { import_path = "example.test/nested/parent" }
}
module "geometry" { source = "../geometry" }
`)
	geometryPath := filepath.Join(root, "geometry", "scenery.package.scn")
	writeNestedModuleFile(t, geometryPath, `package "geometry" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
  go_contract { import_path = "example.test/nested/geometry" }
}
record "point" {
  field "x" { type = float64 }
}
`)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{
			Op:      "resource.create",
			Address: "parent/geometry/record/vector",
			Value: map[string]any{"field": []any{
				map[string]any{"name": "x", "type": map[string]any{"$ref": "float64"}},
				map[string]any{"name": "y", "type": map[string]any{"$ref": "float64"}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	written, err := os.ReadFile(geometryPath)
	if err != nil {
		t.Fatal(err)
	}
	if resourcesByAddress(result.Manifest)["parent/geometry/record/vector"].Address == "" || !strings.Contains(string(written), `record "vector"`) {
		t.Fatalf("nested resource destination:\n%s", written)
	}
}

func TestChangeApplyRequiresBoundApprovalAndRejectsReplay(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v %#v", err, base.Diagnostics)
	}
	plan := ChangePlan{
		APIVersion: "scenery.change-plan/v1", Application: base.Manifest.Application.Name,
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision),
		PredictedWorkspaceRevision: base.WorkspaceRevision, PredictedContractRevision: base.Manifest.ContractRevision,
		ImplementationStatus: "unchanged", DeploymentStatus: "unchanged", Caller: "agent:test",
		Capabilities: []string{"scenery.agent-mutation/v1"}, Operations: []SemanticOperation{}, Edits: []SourceEdit{},
		RequiredApprovals: []string{"risk_critical"}, RequiredCapabilities: []string{}, ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	plan.OperationsDigest = semanticOperationsDigest(plan.Operations)
	plan.PlanID = changePlanID(plan)
	options := ApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: stringPointer(base.Manifest.ContractRevision), Caller: plan.Caller}
	if _, err := ApplyChangePlanWithOptions(root, plan, options); err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("missing approval error = %v", err)
	}
	signature := "ed25519:test:" + base64.RawStdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	token := ApprovalToken{PlanID: plan.PlanID, Caller: plan.Caller, RiskScopes: []string{"risk_critical"}, ExpiresAt: time.Now().UTC().Add(time.Minute), Signature: signature}
	options.ApprovalTokens = []ApprovalToken{token}
	options.VerifyApproval = func(token ApprovalToken, payload []byte) error {
		if token.Signature != signature || len(payload) == 0 {
			return os.ErrPermission
		}
		return nil
	}
	if _, err := ApplyChangePlanWithOptions(root, plan, options); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlanWithOptions(root, plan, options); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("replay error = %v", err)
	}
}

func TestRepairPlanUsesNullContractRevisionAndEstablishesContract(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	path := filepath.Join(root, "house", "scenery.package.scn")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.Replace(source, []byte("\n  implementation {\n    constructor = \"NewService\"\n  }\n"), nil, 1)
	if bytes.Equal(source, broken) {
		t.Fatal("fixture implementation block not found")
	}
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if base.Manifest != nil || base.PartialGraph == nil || base.WorkspaceRevision == "" {
		t.Fatalf("invalid base = %#v", base)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  nil,
		Caller:                "agent:repair",
		Operations: []SemanticOperation{{
			Op: "value.set", Address: "house/service/house", Path: "/spec/implementation",
			Value: map[string]any{"constructor": "NewService"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BaseContractRevision != nil || plan.PredictedContractRevision == "" {
		t.Fatalf("repair plan revisions = %#v", plan)
	}
	receipt, err := ApplyChangePlanWithOptions(root, plan, ApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: nil, Caller: plan.Caller})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContractRevision == "" {
		t.Fatalf("repair receipt = %#v", receipt)
	}
	result, err := Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("repaired check: %v %#v", err, result.Diagnostics)
	}
}

func TestChangePlanRejectsSecretPlaintext(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{Op: "value.set", Address: "house/service/house", Path: "/spec/provider_token", Value: "top-secret"}},
	})
	if err == nil || !strings.Contains(err.Error(), "secret plaintext") {
		t.Fatalf("secret mutation error = %v", err)
	}
}

func TestModuleUpgradeResolvesLocalPackageAndRejectsUnavailableRegistryProfile(t *testing.T) {
	local := Resource{Kind: "scenery.module/v1", Spec: map[string]any{
		"source": "./house", "workspace_package_root": "house", "package": map[string]any{"version": "2.1.0"},
	}}
	if err := validateLocalModuleUpgrade(local, ">= 2.0.0, < 3.0.0"); err != nil {
		t.Fatalf("local upgrade constraint was not resolved: %v", err)
	}
	if err := validateLocalModuleUpgrade(local, ">= 3.0.0"); err == nil || !strings.Contains(err.Error(), "failed_precondition") {
		t.Fatalf("incompatible local upgrade = %v", err)
	}
	registry := Resource{Kind: "scenery.module/v1", Spec: map[string]any{"source": "registry.scenery.dev/example/house", "package": map[string]any{"version": "2.1.0"}}}
	if err := validateLocalModuleUpgrade(registry, ">= 2.2.0"); err == nil || !strings.Contains(err.Error(), "capability_unavailable") {
		t.Fatalf("registry upgrade without registry profile = %v", err)
	}
}
