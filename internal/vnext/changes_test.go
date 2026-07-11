package vnext

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2/hclwrite"
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

func TestChangePlanNormalizesPresentationEquivalentOperations(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	request := func(value any) ChangeRequest {
		return ChangeRequest{
			BaseWorkspaceRevision: base.WorkspaceRevision,
			BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
			Operations:            []SemanticOperation{{Op: "value.set", Address: "house/execution/process_scene_direct", Path: "/spec/timeout", Value: value}},
		}
	}
	contextual, err := PlanChanges(root, request("45m"))
	if err != nil {
		t.Fatal(err)
	}
	tagged, err := PlanChanges(root, request(map[string]any{"$scalar": "duration", "nanoseconds": "2700000000000"}))
	if err != nil {
		t.Fatal(err)
	}
	if contextual.OperationsDigest != tagged.OperationsDigest || !semanticEqual(contextual.Operations, tagged.Operations) {
		t.Fatalf("normalized operations differ:\ncontextual=%#v %s\ntagged=%#v %s", contextual.Operations, contextual.OperationsDigest, tagged.Operations, tagged.OperationsDigest)
	}
	operation := contextual.Operations[0]
	if operation.ExpectedKind != "scenery.execution/v1" || operation.ExpectedSchemaRevision != resourceSchemaRevision(operation.ExpectedKind) || operation.View != "source" {
		t.Fatalf("normalized operation identity = %#v", operation)
	}
	mismatched := request("45m")
	mismatched.Operations[0].ExpectedKind = "scenery.record/v1"
	if _, err := PlanChanges(root, mismatched); err == nil || !strings.Contains(err.Error(), "expected kind") {
		t.Fatalf("mismatched expected kind error = %v", err)
	}
	readOnly := request("45m")
	readOnly.Operations[0].View = "expanded"
	if _, err := PlanChanges(root, readOnly); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expanded-view mutation error = %v", err)
	}
	referenceRequest := func(reference string) ChangeRequest {
		return ChangeRequest{
			BaseWorkspaceRevision: base.WorkspaceRevision,
			BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
			Operations: []SemanticOperation{{
				Op: "value.set", Address: "house/execution/process_scene_direct", Path: "/spec/operation", Value: map[string]any{"$ref": reference},
			}},
		}
	}
	localReference, err := PlanChanges(root, referenceRequest("operation.process_scene"))
	if err != nil {
		t.Fatal(err)
	}
	canonicalReference, err := PlanChanges(root, referenceRequest("house/operation/process_scene"))
	if err != nil {
		t.Fatal(err)
	}
	if localReference.OperationsDigest != canonicalReference.OperationsDigest || !semanticEqual(localReference.Operations, canonicalReference.Operations) {
		t.Fatalf("reference normalization differs: %#v %#v", localReference.Operations, canonicalReference.Operations)
	}
}

func TestChangePlanNormalizesOperationsAcrossTemporarilyInvalidGraph(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{
			{Op: "value.unset", Address: "house/service/house", Path: "/spec/runtime"},
			{Op: "value.set", Address: "house/service/house", Path: "/spec/runtime", Value: "go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, operation := range plan.Operations {
		if operation.ExpectedKind != "scenery.service/v1" || operation.ExpectedSchemaRevision != resourceSchemaRevision("scenery.service/v1") || operation.View != "source" {
			t.Fatalf("operation %d was not normalized: %#v", index, operation)
		}
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

func TestChangeRenameUpdatesNestedAndCompositeReferencesAndRecordsEvidence(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"parent", "geometry", "consumer"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1", "scenery.compatibility-core/v1"]
}
application "nested_rename" { version = "1.0.0" }
module "parent" { source = "./parent" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "parent", "scenery.package.scn"), `package "parent" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
module "geometry" { source = "../geometry" }
`)
	geometryPath := filepath.Join(root, "geometry", "scenery.package.scn")
	writeNestedModuleFile(t, geometryPath, `package "geometry" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "point" {
  field "x" { type = float64 }
}
module "consumer" {
  source = "../consumer"
  inputs = { point = record.point }
}
export "shapes" {
  value = {
    primary = record.point
    all = [record.point]
  }
}
`)
	writeNestedModuleFile(t, filepath.Join(root, "consumer", "scenery.package.scn"), `package "consumer" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
input "point" { type = resource_ref("record") }
`)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{
			Op: "resource.rename", Address: "parent/geometry/record/point", Value: "vector",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Renames) != 1 || plan.Renames[0].From != "parent/geometry/record/point" || plan.Renames[0].To != "parent/geometry/record/vector" || plan.Renames[0].Digest == "" {
		t.Fatalf("rename receipts = %#v", plan.Renames)
	}
	foundRename := false
	for _, change := range plan.SemanticDiff.Changes {
		if change.Operation == "add" || change.Operation == "remove" {
			t.Fatalf("rename degraded to %s: %#v", change.Operation, plan.SemanticDiff.Changes)
		}
		foundRename = foundRename || change.Operation == "rename"
	}
	if !foundRename {
		t.Fatalf("semantic diff changes = %#v", plan.SemanticDiff.Changes)
	}
	encodedPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrippedPlan ChangePlan
	if err := json.Unmarshal(encodedPlan, &roundTrippedPlan); err != nil {
		t.Fatal(err)
	}
	plan = roundTrippedPlan
	receipt, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Renames) != 1 || receipt.Renames[0] != plan.Renames[0] {
		t.Fatalf("receipt renames = %#v; plan = %#v", receipt.Renames, plan.Renames)
	}
	persistedReceipt, err := os.ReadFile(appliedPlanPath(root, plan.PlanID))
	if err != nil || !strings.Contains(string(persistedReceipt), `"rename_receipts"`) {
		t.Fatalf("persisted receipt = %s, %v", persistedReceipt, err)
	}
	applied, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	persistedRenames, err := LoadAppliedRenameReceipts(root, base.Manifest, applied.Manifest)
	if err != nil || len(persistedRenames) != 1 {
		t.Fatalf("loaded applied renames = %#v, %v", persistedRenames, err)
	}
	persistedDiff := CompareManifests(base.Manifest, applied.Manifest, CompareOptions{Renames: persistedRenames})
	foundPersistedRename := false
	for _, change := range persistedDiff.Changes {
		foundPersistedRename = foundPersistedRename || change.Operation == "rename"
	}
	if !foundPersistedRename {
		t.Fatalf("future diff did not recover rename evidence: %#v", persistedDiff.Changes)
	}
	written, err := os.ReadFile(geometryPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "record.point") || strings.Count(string(written), "record.vector") != 3 || !strings.Contains(string(written), `record "vector"`) {
		t.Fatalf("renamed source:\n%s", written)
	}
}

func TestChangeRenameRejectsSourceSharedByMultipleModuleInstances(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "geometry"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1", "scenery.compatibility-core/v1"]
}
application "shared" { version = "1.0.0" }
module "first" { source = "./geometry" }
module "second" { source = "./geometry" }
`)
	packagePath := filepath.Join(root, "geometry", "scenery.package.scn")
	writeNestedModuleFile(t, packagePath, `package "geometry" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "point" {
  field "x" { type = float64 }
}
`)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	before, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations:            []SemanticOperation{{Op: "resource.rename", Address: "first/record/point", Value: "vector"}},
	})
	if err == nil || !strings.Contains(err.Error(), "source is shared by module instances") {
		t.Fatalf("shared-source rename error = %v", err)
	}
	after, readErr := os.ReadFile(packagePath)
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("shared package changed: %v\n%s", readErr, after)
	}
}

func TestChangeCreateThenRenameUsesRefreshedGraph(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision), Operations: []SemanticOperation{
		{Op: "resource.create", Address: "app/authentication/test", Value: map[string]any{"provider": map[string]any{"$ref": "std.provider.standard_auth"}, "scheme": "session"}},
		{Op: "resource.rename", Address: "app/authentication/test", Value: "renamed"},
	}})
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
	resources := resourcesByAddress(result.Manifest)
	if _, ok := resources["app/authentication/renamed"]; !ok {
		t.Fatal("created and renamed resource missing")
	}
	if _, ok := resources["app/authentication/test"]; ok {
		t.Fatal("pre-rename resource remains")
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

func TestStructuredResourceRendererUsesHTTPWireLabelPolicies(t *testing.T) {
	schema, ok := authoredResourceSourceSchema("binding")
	if !ok {
		t.Fatal("binding source schema is unavailable")
	}
	block, err := renderAuthoredResourceBlock("binding", []string{"lookup_http"}, map[string]any{
		"gateway": map[string]any{"$ref": "var.gateway"}, "operation": map[string]any{"$ref": "house/operation/lookup"},
		"execution": map[string]any{"$ref": "house/execution/lookup_direct"}, "protocol": "http", "delivery": "call",
		"authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
		"http": map[string]any{
			"method": "POST", "path": "/lookup", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"},
			"header":          map[string]any{"name": "x-request-id", "to": map[string]any{"$ref": "operation.lookup.input.request_id"}},
			"query_parameter": map[string]any{"name": "filter-name", "to": map[string]any{"$ref": "operation.lookup.input.filter_name"}},
			"body": map[string]any{"codec": "multipart", "to": map[string]any{"$ref": "operation.lookup.input"}, "part": map[string]any{
				"name": "asset-file", "to": map[string]any{"$ref": "operation.lookup.input.asset"}, "kind": "file",
			}},
		},
	}, schema, "house")
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(hclwrite.Format(block.BuildTokens(nil).Bytes()))
	for _, fragment := range []string{`header "x-request-id"`, `query_parameter "filter-name"`, `part "asset-file"`} {
		if !strings.Contains(rendered, fragment) {
			t.Fatalf("rendered binding missing %q:\n%s", fragment, rendered)
		}
	}
	for _, child := range []string{"header", "query_parameter"} {
		public := publicAuthoredBlockSchema(httpSourceSchema.Children[child].Schema)
		pattern := stringValue(public["label_pattern"])
		if matched, compileErr := regexp.MatchString(pattern, map[string]string{"header": "x-request-id", "query_parameter": "filter-name"}[child]); compileErr != nil || !matched {
			t.Errorf("%s label_pattern %q does not advertise its valid wire label", child, pattern)
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "scenery.package.scn")
	if err := os.WriteFile(path, hclwrite.Format(block.BuildTokens(nil).Bytes()), 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
	diagnostics = append(diagnostics, validateAuthoredBlockSchemas([]*Source{source}, true)...)
	if hasErrors(diagnostics) {
		t.Fatalf("rendered wire labels do not pass source validation: %#v", diagnostics)
	}
	invalid := bytes.Replace(hclwrite.Format(block.BuildTokens(nil).Bytes()), []byte(`header "x-request-id"`), []byte(`header "bad header"`), 1)
	if _, err := canonicalFormatSource(invalid, path); err == nil || !strings.Contains(err.Error(), "http_field_name") {
		t.Fatalf("formatter accepted an invalid wire label: %v", err)
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
	if err := retainIssuedPlan(root, issuedChangePlan, plan.PlanID, plan); err != nil {
		t.Fatal(err)
	}
	options := ApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: stringPointer(base.Manifest.ContractRevision), Caller: plan.Caller}
	tampered := plan
	tampered.RequiredApprovals = nil
	tampered.PlanID = changePlanID(tampered)
	if _, err := ApplyChangePlanWithOptions(root, tampered, options); err == nil || !strings.Contains(err.Error(), "issued plan") {
		t.Fatalf("caller-recomputed approval stripping error = %v", err)
	}
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
