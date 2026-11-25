package main

import (
	"path/filepath"
	"testing"

	"scenery.sh/internal/compiler"
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
		assistant.Authentication == "" || assistant.CapabilityRevision == "" {
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
