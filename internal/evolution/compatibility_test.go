package evolution

import (
	"reflect"
	"strings"
	"testing"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
)

const ManifestKind = graph.ManifestKind

var ManifestSchemaRevision = graph.ManifestSchemaRevision

func TestSemanticDiffClassifiesHTTPRouteAndSecurityChanges(t *testing.T) {
	base := &Manifest{ContractRevision: "sha256:base", Resources: []Resource{{
		Address: "house/binding/process_scene_http",
		Kind:    "scenery.binding",
		Spec: map[string]any{
			"authentication": map[string]any{"$ref": "app/authentication/standard"},
			"authorization":  map[string]any{"$ref": "app/authorization/member"},
			"http":           map[string]any{"method": "POST", "path": "/house/process"},
		},
	}}}
	target := &Manifest{ContractRevision: "sha256:target", Resources: []Resource{{
		Address: "house/binding/process_scene_http",
		Kind:    "scenery.binding",
		Spec: map[string]any{
			"authentication": map[string]any{"$ref": "std.authentication.none"},
			"authorization":  map[string]any{"$ref": "std.authorization.public"},
			"http":           map[string]any{"method": "POST", "path": "/house/scenes/process"},
		},
	}}}

	diff := CompareManifests(base, target, CompareOptions{View: "expanded"})
	if len(diff.Changes) != 3 {
		t.Fatalf("changes = %#v", diff.Changes)
	}
	if got := diff.Changes[0].Path; got != "/spec/authentication" {
		t.Fatalf("first path = %q", got)
	}
	if got := diff.Changes[2].Classifications["request_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("route request_wire = %q", got)
	}
	if got := diff.Changes[0].Classifications["security"].Relation; got != SecurityWeaker {
		t.Fatalf("authentication relation = %q", got)
	}
	if diff.Digest == "" || diff.Summary.Breaking != 3 {
		t.Fatalf("diff = %#v", diff)
	}
	if len(diff.RiskRecords) != 2 {
		t.Fatalf("risk records = %#v", diff.RiskRecords)
	}
}

func TestHTTPPathTailChangesRequireSecurityReview(t *testing.T) {
	classification := ClassifySecurityChange("replace", "/spec/http/path", "/drive/{path}", "/drive/{path...}")
	if classification.Result != CompatibilityUnknown || classification.Relation != SecurityUnknown {
		t.Fatalf("path-tail security classification = %#v", classification)
	}
}

func TestSemanticDiffIsDeterministicAcrossResourceOrder(t *testing.T) {
	resources := []Resource{
		{Address: "app/http_gateway/public", Kind: "scenery.http-gateway", Spec: map[string]any{"exposure": "internet"}},
		{Address: "house/operation/get", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "string"}}},
	}
	reversed := []Resource{resources[1], resources[0]}
	a := CompareManifests(&Manifest{Resources: resources}, &Manifest{Resources: nil}, CompareOptions{})
	b := CompareManifests(&Manifest{Resources: reversed}, &Manifest{Resources: nil}, CompareOptions{})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("diffs differ:\n%#v\n%#v", a, b)
	}
}

func TestSemanticDiffClassifiesRecordFieldsDirectionally(t *testing.T) {
	required := map[string]any{"name": "tenant_id", "type": map[string]any{"$ref": "string"}}
	optional := map[string]any{"name": "note", "type": map[string]any{"$expression": "optional(string)"}}
	base := manifestWithWireType(recordForDiff("closed", nil))
	target := manifestWithWireType(recordForDiff("closed", []any{required, optional}))

	diff := CompareManifests(base, target, CompareOptions{})
	requiredChange := findSemanticChange(t, diff, "/spec/field/tenant_id")
	if got := requiredChange.Classifications["request_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("required input add = %q", got)
	}
	if got := requiredChange.Classifications["response_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("required closed output add = %q", got)
	}
	optionalChange := findSemanticChange(t, diff, "/spec/field/note")
	if got := optionalChange.Classifications["request_wire"].Result; got != CompatibilityCompatible {
		t.Fatalf("optional input add = %q", got)
	}
	if got := optionalChange.Classifications["response_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("optional closed output add = %q", got)
	}

	preservingBase := manifestWithWireType(recordForDiff("preserve", nil))
	preserving := CompareManifests(preservingBase, manifestWithWireType(recordForDiff("preserve", []any{optional})), CompareOptions{})
	if got := findSemanticChange(t, preserving, "/spec/field/note").Classifications["response_wire"].Result; got != CompatibilityCompatible {
		t.Fatalf("preserving output add = %q", got)
	}
}

func TestSemanticDiffClassifiesOptionalNullableAndNumericTransitions(t *testing.T) {
	tests := []struct {
		name            string
		baseType        string
		targetType      string
		request, output string
	}{
		{"required_to_optional", "string", "optional(string)", CompatibilityCompatible, CompatibilityBreaking},
		{"optional_to_required", "optional(string)", "string", CompatibilityBreaking, CompatibilityCompatible},
		{"nonnullable_to_nullable", "string", "nullable(string)", CompatibilityCompatible, CompatibilityBreaking},
		{"nullable_to_nonnullable", "nullable(string)", "string", CompatibilityBreaking, CompatibilityBreaking},
		{"numeric_widen", "int32", "int64", CompatibilityCompatible, CompatibilityBreaking},
		{"numeric_narrow", "int64", "int32", CompatibilityBreaking, CompatibilityCompatible},
		{"wire_boundary", "int64", "float64", CompatibilityBreaking, CompatibilityBreaking},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := manifestWithWireType(recordForDiff("closed", []any{fieldForDiff("value", test.baseType)}))
			target := manifestWithWireType(recordForDiff("closed", []any{fieldForDiff("value", test.targetType)}))
			diff := CompareManifests(base, target, CompareOptions{})
			change := findSemanticChangeContaining(t, diff, "/spec/field/value/type")
			if got := change.Classifications["request_wire"].Result; got != test.request {
				t.Fatalf("request = %q, want %q", got, test.request)
			}
			if got := change.Classifications["response_wire"].Result; got != test.output {
				t.Fatalf("response = %q, want %q", got, test.output)
			}
		})
	}
}

func TestSemanticDiffClassifiesConstraintDirection(t *testing.T) {
	baseField := fieldForDiff("value", "string")
	baseField["min_length"] = 1
	targetField := fieldForDiff("value", "string")
	targetField["min_length"] = 2
	diff := CompareManifests(
		manifestWithWireType(recordForDiff("closed", []any{baseField})),
		manifestWithWireType(recordForDiff("closed", []any{targetField})),
		CompareOptions{},
	)
	change := findSemanticChange(t, diff, "/spec/field/value/min_length")
	if got := change.Classifications["request_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("tightened input = %q", got)
	}
	if got := change.Classifications["response_wire"].Result; got != CompatibilityCompatible {
		t.Fatalf("tightened output guarantee = %q", got)
	}

	targetField["pattern"] = "^[a-z]+$"
	pattern := CompareManifests(manifestWithWireType(recordForDiff("closed", []any{baseField})), manifestWithWireType(recordForDiff("closed", []any{targetField})), CompareOptions{})
	if got := findSemanticChange(t, pattern, "/spec/field/value/pattern").Classifications["request_wire"].Result; got != CompatibilityUnknown {
		t.Fatalf("pattern result = %q", got)
	}
}

func TestSemanticDiffClassifiesOpenAndClosedVariants(t *testing.T) {
	baseClosed := Resource{Address: "house/enum/state", Kind: "scenery.enum", Name: "state", Module: "house", Spec: map[string]any{"value": map[string]any{"name": "ready"}}}
	targetClosed := baseClosed
	targetClosed.Spec = map[string]any{"value": []any{map[string]any{"name": "ready"}, map[string]any{"name": "failed"}}}
	diff := CompareManifests(manifestWithWireType(baseClosed), manifestWithWireType(targetClosed), CompareOptions{})
	change := findSemanticChange(t, diff, "/spec/value/failed")
	if got := change.Classifications["request_wire"].Result; got != CompatibilityCompatible {
		t.Fatalf("closed enum input addition = %q", got)
	}
	if got := change.Classifications["response_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("closed enum output addition = %q", got)
	}

	baseOpen := baseClosed
	baseOpen.Spec = map[string]any{"open": true, "value": map[string]any{"name": "ready"}}
	targetOpen := targetClosed
	targetOpen.Spec = map[string]any{"open": true, "value": targetClosed.Spec["value"]}
	openDiff := CompareManifests(manifestWithWireType(baseOpen), manifestWithWireType(targetOpen), CompareOptions{})
	if got := findSemanticChange(t, openDiff, "/spec/value/failed").Classifications["response_wire"].Result; got != CompatibilityCompatible {
		t.Fatalf("open enum output addition = %q", got)
	}
}

func TestSemanticDiffReportsNonApplicableDimensionsAndMigrations(t *testing.T) {
	base := Resource{Address: "house/execution/process", Kind: "scenery.execution", Name: "process", Module: "house", Spec: map[string]any{"mode": "durable", "revision": "1"}}
	target := base
	target.Spec = map[string]any{"mode": "durable", "revision": "2"}
	diff := CompareManifests(manifestWith(base), manifestWith(target), CompareOptions{})
	change := findSemanticChange(t, diff, "/spec/revision")
	if change.Classifications["request_wire"].Applicable {
		t.Fatalf("request wire should not apply: %#v", change.Classifications["request_wire"])
	}
	if got := change.Classifications["runtime"].Result; got != CompatibilityMigrationRequired {
		t.Fatalf("runtime = %q", got)
	}
	if len(diff.RequiredMigrations) != 2 {
		t.Fatalf("migration metadata = %#v", diff.RequiredMigrations)
	}
}

func TestSemanticDiffUsesExplicitRenameEvidence(t *testing.T) {
	before := Resource{Address: "house/operation/old", Kind: "scenery.operation", Name: "old", Module: "house", Spec: map[string]any{"input": map[string]any{"$ref": "string"}}}
	after := before
	after.Address, after.Name = "house/operation/new", "new"
	base, target := manifestWith(before), manifestWith(after)
	base.ContractRevision, target.ContractRevision = "sha256:base", "sha256:target"
	receipt := RenameReceipt{From: before.Address, To: after.Address, BaseContractRevision: base.ContractRevision, TargetContractRevision: target.ContractRevision}
	receipt.Digest = renameReceiptDigest(receipt)
	diff := CompareManifests(base, target, CompareOptions{Renames: []RenameReceipt{receipt}})
	if len(diff.Changes) != 1 || diff.Changes[0].Operation != "rename" {
		t.Fatalf("rename changes = %#v", diff.Changes)
	}
	if got := diff.Changes[0].Classifications["source"].Result; got != CompatibilityBreaking {
		t.Fatalf("rename source = %q", got)
	}
	if got := diff.Changes[0].Classifications["request_wire"].Result; got != CompatibilityCompatible {
		t.Fatalf("rename request wire = %q", got)
	}
	if len(diff.Changes[0].Evidence) != 1 {
		t.Fatalf("rename evidence = %#v", diff.Changes[0].Evidence)
	}
	forged := receipt
	forged.Digest = byteDigest([]byte("forged"))
	diff = CompareManifests(base, target, CompareOptions{Renames: []RenameReceipt{forged}})
	if len(diff.Changes) != 2 || diff.Changes[0].Operation == "rename" || diff.Changes[1].Operation == "rename" {
		t.Fatalf("forged rename evidence was trusted: %#v", diff.Changes)
	}
}

func TestSemanticDiffMachineMetadata(t *testing.T) {
	operation := Resource{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Module: "house", Spec: map[string]any{"input": map[string]any{"$ref": "string"}}}
	diff := CompareManifests(&Manifest{ContractRevision: "sha256:base"}, &Manifest{ContractRevision: "sha256:target", Resources: []Resource{operation}}, CompareOptions{Dimensions: []string{"runtime", "source", "runtime", "bogus"}})
	if !reflect.DeepEqual(diff.Dimensions, []string{"source", "runtime"}) {
		t.Fatalf("dimensions = %#v", diff.Dimensions)
	}
	if diff.CatalogDigest == "" || diff.Digest == "" {
		t.Fatalf("metadata = %#v", diff)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].TargetSchemaRevision == "" {
		t.Fatalf("schema revision = %#v", diff.Changes)
	}
	if len(diff.GeneratedConsequences) == 0 {
		t.Fatalf("generated consequences = %#v", diff.GeneratedConsequences)
	}
}

func TestSemanticDiffClassifiesOperationOutcomesAndHTTPFacets(t *testing.T) {
	operationBase := Resource{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Module: "house", Spec: map[string]any{
		"input":  map[string]any{"$ref": "string"},
		"result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "string"}},
	}}
	operationTarget := operationBase
	operationTarget.Spec = map[string]any{
		"input": map[string]any{"$ref": "string"},
		"result": []any{
			map[string]any{"name": "ok", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "partial", "type": map[string]any{"$ref": "string"}},
		},
	}
	outcomeDiff := CompareManifests(manifestWith(operationBase), manifestWith(operationTarget), CompareOptions{})
	outcome := findSemanticChange(t, outcomeDiff, "/spec/result/partial")
	if got := outcome.Classifications["response_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("outcome response = %q", got)
	}
	if got := outcome.Classifications["request_wire"].Applicable; got {
		t.Fatalf("outcome request should not apply")
	}

	bindingBase := Resource{Address: "house/binding/get", Kind: "scenery.binding", Name: "get", Module: "house", Spec: map[string]any{
		"protocol": "http", "gateway": map[string]any{"$ref": "http_gateway.public"},
		"http": map[string]any{"method": "GET", "path": "/house/items", "request_limit": 1000, "codec_profile": "std.codec.http_json_v1"},
	}}
	bindingTarget := bindingBase
	bindingTarget.Spec = map[string]any{
		"protocol": "http", "gateway": map[string]any{"$ref": "http_gateway.private"},
		"http": map[string]any{"method": "GET", "path": "/house/items", "request_limit": 500, "codec_profile": "std.codec.http_json_v1"},
	}
	httpDiff := CompareManifests(manifestWith(bindingBase), manifestWith(bindingTarget), CompareOptions{})
	if got := findSemanticChange(t, httpDiff, "/spec/gateway").Classifications["request_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("gateway request = %q", got)
	}
	if got := findSemanticChange(t, httpDiff, "/spec/http/request_limit").Classifications["request_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("limit request = %q", got)
	}
}

func TestHTTPResponseMappingsDoNotLeakIntoRequestWireCompatibility(t *testing.T) {
	binding := Resource{Address: "house/binding/get", Kind: "scenery.binding", Spec: map[string]any{"protocol": "http"}}
	path := "/spec/http/response/ok/header/x-request-id"
	request := classifyBindingChange("request_wire", "replace", binding, path, "old", "new")
	if request.Applicable {
		t.Fatalf("response mapping classified as request-wire change: %#v", request)
	}
	response := classifyBindingChange("response_wire", "replace", binding, path, "old", "new")
	if !response.Applicable || response.Result != CompatibilityBreaking {
		t.Fatalf("response mapping classification = %#v", response)
	}
}

func TestSemanticDiffClassifiesBindingFamiliesAndScheduleState(t *testing.T) {
	tests := []struct {
		name       string
		kind       string
		baseSpec   map[string]any
		targetSpec map[string]any
		path       string
		dimension  string
		want       string
	}{
		{"internal", "scenery.binding", map[string]any{"protocol": "internal", "internal": map[string]any{"visibility": "application"}}, map[string]any{"protocol": "internal", "internal": map[string]any{"visibility": "package"}}, "/spec/internal/visibility", "internal_call", CompatibilityBreaking},
		{"cli", "scenery.binding", map[string]any{"protocol": "cli", "cli": map[string]any{"command": []any{"house", "run"}}}, map[string]any{"protocol": "cli", "cli": map[string]any{"command": []any{"house", "process"}}}, "/spec/cli/command", "source", CompatibilityBreaking},
		{"event", "scenery.binding", map[string]any{"protocol": "event", "event": map[string]any{"channel": "old"}}, map[string]any{"protocol": "event", "event": map[string]any{"channel": "new"}}, "/spec/event/channel", "runtime", CompatibilityMigrationRequired},
		{"schedule", "scenery.schedule", map[string]any{"trigger": "0 * * * *", "invoke": map[string]any{"$ref": "binding.run"}, "overlap": "skip"}, map[string]any{"trigger": "15 * * * *", "invoke": map[string]any{"$ref": "binding.run"}, "overlap": "skip"}, "/spec/trigger", "runtime", CompatibilityMigrationRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := Resource{Address: "house/" + test.name + "/value", Kind: test.kind, Name: "value", Module: "house", Spec: test.baseSpec}
			after := before
			after.Spec = test.targetSpec
			diff := CompareManifests(manifestWith(before), manifestWith(after), CompareOptions{})
			if got := findSemanticChange(t, diff, test.path).Classifications[test.dimension].Result; got != test.want {
				t.Fatalf("classification = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSemanticDiffClassifiesSecurityRelations(t *testing.T) {
	tests := []struct {
		name, path   string
		base, target any
		relation     string
		result       string
	}{
		{"stronger_auth", "/spec/authentication", "std.authentication.none", "app/authentication/standard", SecurityStronger, CompatibilityBreaking},
		{"weaker_auth", "/spec/authentication", "app/authentication/standard", "std.authentication.none", SecurityWeaker, CompatibilityBreaking},
		{"deny_all_to_public", "/spec/authorization", "std.authorization.none", "std.authorization.public", SecurityWeaker, CompatibilityBreaking},
		{"public_to_deny_all", "/spec/authorization", "std.authorization.public", "std.authorization.none", SecurityStronger, CompatibilityBreaking},
		{"deny_all_to_member", "/spec/authorization", "std.authorization.none", "app/authorization/member", SecurityWeaker, CompatibilityBreaking},
		{"incomparable_principal", "/spec/principal", "std.type.user", "std.type.service", SecurityIncomparable, CompatibilityBreaking},
		{"unknown_pipeline", "/spec/pipeline/step", "request_id", "custom", SecurityUnknown, CompatibilityUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification := classifySecurityChange("replace", test.path, test.base, test.target)
			if classification.Relation != test.relation || classification.Result != test.result {
				t.Fatalf("classification = %#v", classification)
			}
		})
	}
}

func TestSemanticDiffStorageProviderFallbackAndTargetInvalidation(t *testing.T) {
	entityBase := Resource{Address: "house/entity/scene", Kind: "scenery.entity", Name: "scene", Module: "house", Spec: map[string]any{"mapping": "scenes"}}
	entityTarget := entityBase
	entityTarget.Spec = map[string]any{"mapping": "roof_scenes"}
	storage := CompareManifests(manifestWith(entityBase), manifestWith(entityTarget), CompareOptions{})
	if got := findSemanticChange(t, storage, "/spec/mapping").Classifications["storage"].Result; got != CompatibilityUnknown {
		t.Fatalf("storage fallback = %q", got)
	}

	targetBase := Resource{Address: "app/go_target/prod", Kind: "scenery.go-target", Name: "prod", Module: "app", Spec: map[string]any{"goarch": "amd64"}}
	targetNext := targetBase
	targetNext.Spec = map[string]any{"goarch": "arm64"}
	targetDiff := CompareManifests(manifestWith(targetBase), manifestWith(targetNext), CompareOptions{})
	change := findSemanticChange(t, targetDiff, "/spec/goarch")
	if change.Classifications["request_wire"].Applicable {
		t.Fatal("go target request wire should not apply")
	}
	if !reflect.DeepEqual(change.AffectedArtifacts, []string{"deployment_revision[*]"}) {
		t.Fatalf("target artifacts = %#v", change.AffectedArtifacts)
	}
}

func TestSemanticDiffUnknownFallbackAndAggregationSeverity(t *testing.T) {
	base := Resource{Address: "house/renderer/web", Kind: "scenery.renderer", Name: "web", Module: "house", Spec: map[string]any{"runtime": "vite"}}
	target := base
	target.Spec = map[string]any{"runtime": "other"}
	diff := CompareManifests(manifestWith(base), manifestWith(target), CompareOptions{})
	if diff.Summary.Unknown != 1 || diff.Summary.Breaking != 0 {
		t.Fatalf("summary = %#v", diff.Summary)
	}
	change := findSemanticChange(t, diff, "/spec/runtime")
	if strongestClassification(change.Classifications) != CompatibilityUnknown {
		t.Fatalf("strongest = %q", strongestClassification(change.Classifications))
	}
}

func manifestWith(resources ...Resource) *Manifest {
	return &Manifest{Kind: ManifestKind, SchemaRevision: ManifestSchemaRevision, SpecRevision: string(spec.CurrentRevision()), Resources: resources}
}

func manifestWithWireType(resource Resource) *Manifest {
	kind := strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery.")
	typeRef := map[string]any{"$ref": kind + "." + resource.Name}
	operation := Resource{Address: resource.Module + "/operation/use_type", Kind: "scenery.operation", Name: "use_type", Module: resource.Module, Spec: map[string]any{
		"input":  typeRef,
		"result": map[string]any{"name": "ok", "type": typeRef},
	}}
	return manifestWith(resource, operation)
}

func recordForDiff(unknownFields string, fields []any) Resource {
	spec := map[string]any{}
	if unknownFields == "preserve" {
		spec["unknown_fields"] = "preserve"
	}
	if len(fields) == 1 {
		spec["field"] = fields[0]
	} else if len(fields) > 1 {
		spec["field"] = fields
	}
	return Resource{Address: "house/record/payload", Kind: "scenery.record", Name: "payload", Module: "house", Spec: spec}
}

func fieldForDiff(name, typeName string) map[string]any {
	typeValue := map[string]any{"$ref": typeName}
	if typeName != innermostType(typeName) || typeName == "optional(string)" || typeName == "nullable(string)" {
		typeValue = map[string]any{"$expression": typeName}
	}
	return map[string]any{"name": name, "type": typeValue}
}

func findSemanticChange(t *testing.T, diff SemanticDiff, path string) SemanticChange {
	t.Helper()
	for _, change := range diff.Changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("change %s not found in %#v", path, diff.Changes)
	return SemanticChange{}
}

func findSemanticChangeContaining(t *testing.T, diff SemanticDiff, pathPrefix string) SemanticChange {
	t.Helper()
	for _, change := range diff.Changes {
		if len(change.Path) >= len(pathPrefix) && change.Path[:len(pathPrefix)] == pathPrefix {
			return change
		}
	}
	t.Fatalf("change under %s not found in %#v", pathPrefix, diff.Changes)
	return SemanticChange{}
}
