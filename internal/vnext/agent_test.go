package vnext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCapabilitiesAdvertiseExactProfiles(t *testing.T) {
	result, err := Compile("testdata/house")
	if err != nil {
		t.Fatal(err)
	}
	response := HandleAgentRequest(result, AgentRequest{ID: json.RawMessage(`1`), Method: "capabilities"})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	b, _ := json.Marshal(response.Result)
	if !json.Valid(b) || !containsJSONText(b, "scenery.agent-read/v1") || !containsJSONText(b, "resources.list") || !containsJSONText(b, "scenery.http-codec/v1") || !containsJSONText(b, "scenery.semantic-operation/v1") {
		t.Fatalf("result = %s", b)
	}
}

func TestAgentDiagnosticsAndRepairPlanRemainAvailableWithoutManifest(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	path := filepath.Join(root, "house", "scenery.package.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `timeout   = "40m"`, `timeout   = "not-a-duration"`, 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil || result.Manifest != nil || result.WorkspaceRevision == "" {
		t.Fatalf("invalid compile = %#v, %v", result, err)
	}
	diagnostics := HandleAgentRequest(result, AgentRequest{Method: "diagnostics.get"})
	if diagnostics.Error != nil {
		t.Fatalf("diagnostics response = %#v", diagnostics)
	}
	params, _ := json.Marshal(ChangeRequest{
		BaseWorkspaceRevision: result.WorkspaceRevision, BaseContractRevision: nil, Caller: "test",
		Operations: []SemanticOperation{{Op: "value.set", Address: "house/execution/process_scene_direct", Path: "/spec/timeout", Value: "40m"}},
	})
	planned := HandleAgentRequest(result, AgentRequest{Method: "changes.plan", Params: params})
	if planned.Error != nil {
		t.Fatalf("repair plan response = %#v", planned)
	}
	plan, ok := planned.Result.(ChangePlan)
	if !ok || plan.BaseContractRevision != nil || plan.PredictedContractRevision == "" {
		t.Fatalf("repair plan = %#v", planned.Result)
	}
}

func TestAgentSchemaGetCoversEveryMutationAndDiagnosticValue(t *testing.T) {
	result, err := Compile("testdata/house")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"scenery.value/v1", "scenery.diagnostic/v1", "scenery.semantic-operation/v1",
		"resource.create", "resource.delete", "resource.rename", "value.set", "value.unset", "module.configure", "module.upgrade",
	} {
		t.Run(name, func(t *testing.T) {
			params, _ := json.Marshal(map[string]string{"kind": name})
			response := HandleAgentRequest(result, AgentRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "schema.get", Params: params})
			if response.Error != nil {
				t.Fatal(response.Error)
			}
			encoded, _ := json.Marshal(response.Result)
			if !json.Valid(encoded) || !containsJSONText(encoded, "schema_revision") {
				t.Fatalf("schema = %s", encoded)
			}
		})
	}
}

func TestAgentResourcesAndContextUseCanonicalGraph(t *testing.T) {
	result, err := Compile("testdata/house")
	if err != nil {
		t.Fatal(err)
	}
	list := HandleAgentRequest(result, AgentRequest{ID: json.RawMessage(`1`), Method: "resources.list", Params: json.RawMessage(`{"kind":"operation"}`)})
	if list.Error != nil {
		t.Fatal(list.Error)
	}
	b, _ := json.Marshal(list.Result)
	if !containsJSONText(b, "house/operation/process_scene") {
		t.Fatalf("list = %s", b)
	}
	contextResponse := HandleAgentRequest(result, AgentRequest{ID: json.RawMessage(`2`), Method: "context.get", Params: json.RawMessage(`{"focus":["house/operation/process_scene"],"include":["dependencies"],"depth":2,"max_resources":10,"max_bytes":10000}`)})
	if contextResponse.Error != nil {
		t.Fatal(contextResponse.Error)
	}
}

func TestAgentReadsUseRequestedGraphView(t *testing.T) {
	result, err := Compile("testdata/house")
	if err != nil {
		t.Fatal(err)
	}
	params := json.RawMessage(`{"address":"app/http_gateway/public_api","view":"source"}`)
	response := HandleAgentRequest(result, AgentRequest{Method: "resources.get", Params: params})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	encoded, _ := json.Marshal(response.Result)
	if containsJSONText(encoded, "request_limit") {
		t.Fatalf("source view leaked effective defaults: %s", encoded)
	}
	params = json.RawMessage(`{"address":"app/http_gateway/public_api","view":"effective"}`)
	response = HandleAgentRequest(result, AgentRequest{Method: "resources.get", Params: params})
	encoded, _ = json.Marshal(response.Result)
	if response.Error != nil || !containsJSONText(encoded, "request_limit") {
		t.Fatalf("effective view = %s, error = %#v", encoded, response.Error)
	}
	invalid := HandleAgentRequest(result, AgentRequest{Method: "resources.list", Params: json.RawMessage(`{"view":"invented"}`)})
	if invalid.Error == nil || invalid.Error.Kind != "invalid_request" {
		t.Fatalf("invalid view response = %#v", invalid)
	}
	explained := HandleAgentRequest(result, AgentRequest{Method: "resources.explain", Params: json.RawMessage(`{"address":"app/http_gateway/public_api","view":"effective"}`)})
	encoded, _ = json.Marshal(explained.Result)
	if explained.Error != nil || !containsJSONText(encoded, `"provenance":{"app/http_gateway/public_api":`) || containsJSONText(encoded, `"provenance":true`) {
		t.Fatalf("explain response = %s, error = %#v", encoded, explained.Error)
	}
}

func TestAgentContextContinuationUsesRetainedWorkspaceSnapshot(t *testing.T) {
	base := &Manifest{ContractRevision: "sha256:base", Resources: []Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Name: "house", Spec: map[string]any{}},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Name: "process", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}}},
	}}
	session := NewAgentSession()
	first := session.Handle(&Result{Manifest: base, WorkspaceRevision: "sha256:workspace-base"}, AgentRequest{Method: "context.get", Params: json.RawMessage(`{"focus":["house/operation/process"],"include":["dependencies"],"depth":2,"max_resources":1,"max_bytes":10000,"view":"effective"}`)})
	if first.Error != nil {
		t.Fatal(first.Error)
	}
	bundle := first.Result.(ContextBundle)
	if bundle.ContinuationToken == "" {
		t.Fatalf("first bundle = %#v", bundle)
	}
	target := cloneAgentManifest(base)
	target.ContractRevision = "sha256:target"
	target.Resources = append(target.Resources, Resource{Address: "house/record/new", Module: "house", Kind: "scenery.record/v1", Name: "new", Spec: map[string]any{}})
	params, _ := json.Marshal(ContextOptions{Focus: []string{"house/operation/process"}, Include: []string{"dependencies"}, Depth: 2, MaxResources: 1, MaxBytes: 10000, View: "effective", ContinuationToken: bundle.ContinuationToken})
	second := session.Handle(&Result{Manifest: target, WorkspaceRevision: "sha256:workspace-target"}, AgentRequest{Method: "context.get", Params: params})
	if second.Error != nil {
		t.Fatal(second.Error)
	}
	resumed := second.Result.(ContextBundle)
	if resumed.WorkspaceRevision != "sha256:workspace-base" || resumed.ContractRevision != "sha256:base" {
		t.Fatalf("resumed bundle = %#v", resumed)
	}
}

func TestAgentSessionRetainsExactRevisionSnapshots(t *testing.T) {
	baseManifest := &Manifest{ContractRevision: "sha256:base", Resources: []Resource{{Address: "house/record/item", Kind: "scenery.record/v1", Module: "house", Name: "item", Spec: map[string]any{}}}}
	targetManifest := &Manifest{ContractRevision: "sha256:target", Resources: []Resource{{Address: "house/record/item", Kind: "scenery.record/v1", Module: "house", Name: "item", Spec: map[string]any{"unknown_fields": "preserve"}}}}
	session := NewAgentSession()
	if response := session.Handle(&Result{Manifest: baseManifest, WorkspaceRevision: "sha256:workspace-base"}, AgentRequest{Method: "capabilities"}); response.Error != nil {
		t.Fatalf("retain base: %#v", response.Error)
	}
	params, _ := json.Marshal(map[string]any{"base_revision": "sha256:base", "target_revision": "sha256:target"})
	response := session.Handle(&Result{Manifest: targetManifest, WorkspaceRevision: "sha256:workspace-target"}, AgentRequest{Method: "revisions.diff", Params: params})
	if response.Error != nil {
		t.Fatalf("retained diff: %#v", response.Error)
	}
	diff, ok := response.Result.(SemanticDiff)
	if !ok || diff.BaseRevision != "sha256:base" || diff.TargetRevision != "sha256:target" || len(diff.Changes) == 0 {
		t.Fatalf("retained diff = %#v", response.Result)
	}
	missing, _ := json.Marshal(map[string]any{"base_revision": "sha256:missing", "target_revision": "sha256:target"})
	response = session.Handle(&Result{Manifest: targetManifest, WorkspaceRevision: "sha256:workspace-target"}, AgentRequest{Method: "revisions.diff", Params: missing})
	if response.Error == nil || response.Error.Kind != "failed_precondition" {
		t.Fatalf("missing snapshot response = %#v", response)
	}
}

func TestAgentMutationConvenienceMethodsValidateAndNormalizeWithoutWriting(t *testing.T) {
	result, err := Compile("testdata/house")
	if err != nil {
		t.Fatal(err)
	}
	source := result.Sources[0].Bytes
	response := HandleAgentRequest(result, AgentRequest{Method: "value.set", Params: json.RawMessage(`{"address":"house/execution/process_scene_direct","path":"/spec/timeout","value":"30m"}`)})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	operation, ok := response.Result.(SemanticOperation)
	if !ok || operation.Op != "value.set" || operation.View != "source" {
		t.Fatalf("normalized operation = %#v", response.Result)
	}
	if string(result.Sources[0].Bytes) != string(source) {
		t.Fatal("mutation convenience method changed source")
	}

	rejected := HandleAgentRequest(result, AgentRequest{Method: "resource.delete", Params: json.RawMessage(`{"address":"house/record/process_scene_input"}`)})
	if rejected.Error == nil || rejected.Error.Kind != "failed_precondition" {
		t.Fatalf("dependent delete response = %#v", rejected)
	}
	unknown := HandleAgentRequest(result, AgentRequest{Method: "resources.list", Params: json.RawMessage(`{"kind":"operation","unknown":true}`)})
	if unknown.Error == nil || unknown.Error.Kind != "invalid_request" {
		t.Fatalf("unknown parameter response = %#v", unknown)
	}
}

func containsJSONText(value []byte, want string) bool {
	for index := 0; index+len(want) <= len(value); index++ {
		if string(value[index:index+len(want)]) == want {
			return true
		}
	}
	return false
}
