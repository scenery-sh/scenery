package vnext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGraphClosureIncludesDependenciesAndDependents(t *testing.T) {
	manifest := &Manifest{Resources: []Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1"},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}}},
		{Address: "house/binding/process", Module: "house", Kind: "scenery.binding/v1", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.process"}}},
	}}
	graph, err := Graph(manifest, "house/operation/process", GraphOptions{Direction: "both", Depth: 1, MaxResources: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Resources) != 3 || len(graph.Edges) != 2 || graph.Truncated {
		t.Fatalf("graph = %#v", graph)
	}
}

func TestContextBundleIsBounded(t *testing.T) {
	manifest := &Manifest{ContractRevision: "sha256:contract", Resources: []Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1"},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}}},
	}}
	bundle, err := Context(manifest, ContextOptions{Focus: []string{"house/operation/process"}, Include: []string{"dependencies"}, Depth: 2, MaxResources: 1, MaxBytes: 10000, View: "effective"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Resources) != 1 || !bundle.Truncated || bundle.ContinuationToken == "" {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestContextBundleHonorsSerializedByteLimitIncludingContinuation(t *testing.T) {
	manifest := &Manifest{ContractRevision: "sha256:contract", Resources: []Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Spec: map[string]any{"description": strings.Repeat("x", 2048)}},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}}},
	}}
	first, err := ContextSnapshot(manifest, "sha256:workspace", ContextOptions{
		Focus: []string{"house/operation/process"}, Include: []string{"dependencies"}, Depth: 2, MaxResources: 1, MaxBytes: 10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	encodedFirst, _ := json.Marshal(first)
	bundle, err := ContextSnapshot(manifest, "sha256:workspace", ContextOptions{
		Focus: []string{"house/operation/process"}, Include: []string{"dependencies"}, Depth: 2, MaxResources: 10, MaxBytes: len(encodedFirst),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(bundle)
	if len(encoded) > len(encodedFirst) || len(bundle.Resources) != 1 || !bundle.Truncated || bundle.ContinuationToken == "" {
		t.Fatalf("bundle bytes=%d limit=%d bundle=%#v", len(encoded), len(encodedFirst), bundle)
	}
	if _, err := ContextSnapshot(manifest, "sha256:workspace", ContextOptions{
		Focus: []string{"house/operation/process"}, MaxResources: 10, MaxBytes: 1,
	}); err == nil || !strings.Contains(err.Error(), "max_bytes is too small") {
		t.Fatalf("small max_bytes error = %v", err)
	}
}

func TestContextBundleProjectsRequestedSchemasDiagnosticsAndProvenance(t *testing.T) {
	manifest := &Manifest{ContractRevision: "sha256:contract", Resources: []Resource{{
		Address: "house/record/item", Module: "house", Kind: "scenery.record/v1", Name: "item", Origin: Origin{Kind: "authored", SourceID: "src_item"}, Spec: map[string]any{},
	}}}
	bundle, err := ContextSnapshotWithDiagnostics(manifest, "sha256:workspace", []Diagnostic{{Code: "SCNTEST", Severity: "warning", Message: "test", Address: "house/record/item"}}, ContextOptions{
		Focus: []string{"house/record/item"}, Include: []string{"schemas", "diagnostics", "provenance"}, MaxResources: 10, MaxBytes: 10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Schemas["scenery.record/v1"] == nil || bundle.Provenance["house/record/item"].SourceID != "src_item" || len(bundle.Diagnostics) != 1 {
		t.Fatalf("bundle = %#v", bundle)
	}
	if _, err := Graph(manifest, "house/record/item", GraphOptions{Depth: agentMaxDepth + 1}); err == nil {
		t.Fatal("graph accepted depth above the advertised limit")
	}
}

func TestContextContinuationResumesSameRevisionAndQuery(t *testing.T) {
	manifest := &Manifest{ContractRevision: "sha256:contract", Resources: []Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1"},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}}},
	}}
	now := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	options := ContextOptions{Focus: []string{"house/operation/process"}, Include: []string{"dependencies"}, Depth: 2, MaxResources: 1, MaxBytes: 10000, View: "effective"}
	first, err := contextAt(manifest, "sha256:workspace", nil, options, now)
	if err != nil {
		t.Fatal(err)
	}
	options.ContinuationToken = first.ContinuationToken
	second, err := contextAt(manifest, "sha256:workspace", nil, options, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Resources) != 1 || len(second.Resources) != 1 || first.Resources[0].Address == second.Resources[0].Address || second.Truncated {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if second.WorkspaceRevision != "sha256:workspace" || second.ContractRevision != "sha256:contract" {
		t.Fatalf("second revisions = %#v", second)
	}
}

func TestContextContinuationRejectsSnapshotQueryAndExpiryDrift(t *testing.T) {
	manifest := &Manifest{ContractRevision: "sha256:contract", Resources: []Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1"},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}}},
	}}
	now := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	options := ContextOptions{Focus: []string{"house/operation/process"}, Include: []string{"dependencies"}, Depth: 2, MaxResources: 1, MaxBytes: 10000}
	first, err := contextAt(manifest, "sha256:workspace", nil, options, now)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		workspace string
		options   ContextOptions
		now       time.Time
	}{
		{name: "workspace", workspace: "sha256:new", options: withContextToken(options, first.ContinuationToken), now: now},
		{name: "query", workspace: "sha256:workspace", options: withContextToken(ContextOptions{Focus: options.Focus, Include: []string{"dependents"}, Depth: 2, MaxResources: 1, MaxBytes: 10000}, first.ContinuationToken), now: now},
		{name: "expired", workspace: "sha256:workspace", options: withContextToken(options, first.ContinuationToken), now: now.Add(contextTokenTTL + time.Second)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := contextAt(manifest, test.workspace, nil, test.options, test.now)
			if err == nil || !strings.HasPrefix(err.Error(), "failed_precondition:") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func withContextToken(options ContextOptions, token string) ContextOptions {
	options.ContinuationToken = token
	return options
}
