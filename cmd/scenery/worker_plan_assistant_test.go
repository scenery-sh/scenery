package main

import (
	"path/filepath"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
)

func TestWorkerAssistantPlanProjectsToolBindingAndSurface(t *testing.T) {
	root := filepath.Join(repoRootForTest(t), "testdata", "assistant")
	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.Valid() {
		t.Fatalf("assistant fixture is invalid: %v", compiled.Diagnostics)
	}
	workers, err := projectWorkerServices(compiled.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundle := workerBundle{Host: workerHostPlan{Workers: workers}}
	if err := bundle.projectAssistants(compiled); err != nil {
		t.Fatal(err)
	}
	assistant := bundle.Host.Assistants[0]
	capability := bundle.Host.MCPServers[assistant.MCPServer].Capabilities[0]
	if assistant.Path != "/assistants/support" || assistant.SessionAccess != "initiator" ||
		assistant.Authentication == "" || assistant.CapabilityRevision == "" || assistant.Policy.GatewayAddress == "" {
		t.Fatalf("assistant surface is incomplete: %+v", assistant)
	}
	if capability.Name != "house__process_scene" || capability.Approval != mcpcontract.ApprovalAlways ||
		capability.Limits.MaxInputBytes != 262144 || len(capability.InputSchema) == 0 || len(capability.OutputSchema) == 0 {
		t.Fatalf("capability manifest is incomplete: %+v", capability)
	}
	for _, worker := range workers {
		for _, binding := range worker.Bindings {
			if binding.MCP && binding.Address == capability.Origin.Address &&
				binding.Operation == capability.OperationAddress && binding.Execution == capability.ExecutionAddress {
				return
			}
		}
	}
	t.Fatalf("missing worker binding for %s", capability.Origin.Address)
}

func TestWorkerAssistantPlanPreservesGatewayPolicyAndHTTPAuthorization(t *testing.T) {
	root := filepath.Join(repoRootForTest(t), "testdata", "assistant")
	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.Valid() {
		t.Fatalf("assistant fixture is invalid: %v", compiled.Diagnostics)
	}
	auth := graph.Resource{
		Address: "app/authorization/app_only",
		Kind:    "scenery.authorization",
		Spec:    map[string]any{"strategy": "application"},
	}
	for i := range compiled.Manifest.Resources {
		resource := &compiled.Manifest.Resources[i]
		switch resource.Kind {
		case "scenery.http-gateway":
			resource.Spec["request_limit"] = map[string]any{"header_bytes": 4096}
			resource.Spec["timeouts"] = map[string]any{
				"read":             "5s",
				"write":            "10s",
				"idle":             "60s",
				"total_invocation": "15s",
			}
		case "scenery.assistant":
			surface, ok := resource.Spec["surface"].(map[string]any)
			if !ok {
				t.Fatalf("assistant %s missing surface", resource.Address)
			}
			surface["authorization"] = map[string]any{"$ref": auth.Address}
		}
	}
	compiled.Manifest.Resources = append(compiled.Manifest.Resources, auth)
	workers, err := projectWorkerServices(compiled.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundle := workerBundle{Host: workerHostPlan{Workers: workers}}
	if err := bundle.projectAssistants(compiled); err != nil {
		t.Fatal(err)
	}
	plan := bundle.Host.Assistants[0]
	if plan.Policy.GatewayAddress == "" {
		t.Fatalf("expected non-empty GatewayAddress, got %+v", plan.Policy)
	}
	if plan.Policy.AuthorizationStrategy != "application" {
		t.Fatalf("authorization strategy = %q, want application", plan.Policy.AuthorizationStrategy)
	}
	if plan.Policy.MaxRequestHeaderBytes != 4096 {
		t.Fatalf("max request header bytes = %d, want 4096", plan.Policy.MaxRequestHeaderBytes)
	}
	if plan.Policy.ReadTimeoutNanos != int64(5*time.Second) ||
		plan.Policy.WriteTimeoutNanos != int64(10*time.Second) ||
		plan.Policy.IdleTimeoutNanos != int64(60*time.Second) ||
		plan.Policy.TotalInvocationTimeoutNanos != int64(15*time.Second) {
		t.Fatalf("timeouts not populated from gateway: %+v", plan.Policy)
	}
}
