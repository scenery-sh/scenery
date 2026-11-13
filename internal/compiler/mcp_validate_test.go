package compiler

import "testing"

func TestMCPBindingValidationCoversProjectionAndEffects(t *testing.T) {
	input := Resource{Address: "house/record/input", Module: "house", Kind: "scenery.record", Name: "input", Spec: map[string]any{
		"field": []any{map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}},
	}}
	result := Resource{Address: "house/record/result", Module: "house", Kind: "scenery.record", Name: "result", Spec: map[string]any{
		"field": []any{map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}},
	}}
	operation := Resource{Address: "house/operation/process", Module: "house", Kind: "scenery.operation", Name: "process", Spec: map[string]any{
		"input":       map[string]any{"$ref": "record.input"},
		"result":      map[string]any{"name": "ok", "type": map[string]any{"$ref": "record.result"}},
		"idempotency": map[string]any{"mode": "none"},
	}}
	execution := Resource{Address: "house/execution/process", Module: "house", Kind: "scenery.execution", Name: "process", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.process"}, "mode": "direct",
	}}
	binding := Resource{Address: "house/binding/process_mcp", Module: "house", Kind: "scenery.binding", Name: "process_mcp", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.process"}, "execution": map[string]any{"$ref": "execution.process"},
		"protocol": "mcp", "delivery": "call", "authentication": map[string]any{"$ref": "std.authentication.inherit"},
		"authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
		"mcp": map[string]any{"name": "process", "read_only": false, "destructive": false, "idempotent": false, "open_world": false},
	}}
	resources := []Resource{input, result, operation, execution, binding}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	if diagnostics := validateMCPBinding(binding, byAddress); hasErrors(diagnostics) {
		t.Fatalf("valid MCP binding diagnostics = %#v", diagnostics)
	}

	binding.Spec["delivery"] = "enqueue"
	if diagnostics := validateMCPBinding(binding, byAddress); !hasDiagnostic(diagnostics, mcpBindingShapeCode) {
		t.Fatalf("delivery diagnostics = %#v", diagnostics)
	}
	binding.Spec["delivery"] = "call"
	execution.Spec["mode"] = "durable"
	if diagnostics := validateMCPBinding(binding, byAddress); !hasDiagnostic(diagnostics, mcpBindingShapeCode) {
		t.Fatalf("durable execution diagnostics = %#v", diagnostics)
	}
	execution.Spec["mode"] = "direct"
	binding.Spec["mcp"].(map[string]any)["name"] = "NotPortable"
	if diagnostics := validateMCPBinding(binding, byAddress); !hasDiagnostic(diagnostics, mcpToolIdentityCode) {
		t.Fatalf("name diagnostics = %#v", diagnostics)
	}
	binding.Spec["mcp"].(map[string]any)["name"] = "process"
	binding.Spec["mcp"].(map[string]any)["read_only"] = true
	binding.Spec["mcp"].(map[string]any)["destructive"] = true
	if diagnostics := validateMCPBinding(binding, byAddress); !hasDiagnostic(diagnostics, mcpEffectMetadataCode) {
		t.Fatalf("effect diagnostics = %#v", diagnostics)
	}
	binding.Spec["mcp"].(map[string]any)["read_only"] = false
	binding.Spec["mcp"].(map[string]any)["destructive"] = false
	operation.Spec["input"] = map[string]any{"$ref": "string"}
	if diagnostics := validateMCPBinding(binding, byAddress); !hasDiagnostic(diagnostics, mcpProjectionTypeCode) {
		t.Fatalf("input type diagnostics = %#v", diagnostics)
	}
	operation.Spec["input"] = map[string]any{"$ref": "record.input"}
	result.Spec["field"].([]any)[0].(map[string]any)["sensitive"] = true
	if diagnostics := validateMCPBinding(binding, byAddress); !hasDiagnostic(diagnostics, mcpSensitiveOutputCode) {
		t.Fatalf("sensitive output diagnostics = %#v", diagnostics)
	}
}

func TestMCPAssistantPathValidationRejectsEscapesAndUnsupportedAdapter(t *testing.T) {
	assistant := Resource{Address: "app/assistant/support", Module: "app", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
		"implementation": map[string]any{"adapter": "unknown", "source": "../outside", "package": "./assistant/package.json", "package_lock": "./assistant/package-lock.json"},
		"surface":        map[string]any{"path": "/assistants/support", "session_access": "initiator"},
	}}
	diagnostics := validateAssistantResource(assistant, map[string]Resource{})
	if !hasDiagnostic(diagnostics, assistantAdapterCode) || hasDiagnostic(diagnostics, mcpBindingShapeCode) {
		t.Fatalf("assistant adapter diagnostics = %#v", diagnostics)
	}
	assistant.Spec["surface"].(map[string]any)["authentication"] = map[string]any{"$ref": "std.authentication.none"}
	if diagnostics := validateAssistantResource(assistant, map[string]Resource{}); !hasDiagnostic(diagnostics, mcpBindingShapeCode) {
		t.Fatalf("unauthenticated assistant surface diagnostics = %#v", diagnostics)
	}
	delete(assistant.Spec["surface"].(map[string]any), "authentication")
	diagnostics = validateMCPAssistantPaths("", []Resource{assistant})
	if !hasDiagnostic(diagnostics, assistantSourcePathCode) {
		t.Fatalf("assistant path diagnostics = %#v", diagnostics)
	}
	assistant.Spec["implementation"].(map[string]any)["source"] = "./assistant"
	assistant.Spec["implementation"].(map[string]any)["package_lock"] = "./assistant/lock.json"
	diagnostics = validateMCPAssistantPaths("", []Resource{assistant})
	if !hasDiagnostic(diagnostics, assistantPackageLockCode) {
		t.Fatalf("assistant lock diagnostics = %#v", diagnostics)
	}

	other := assistant
	other.Address = "app/assistant/other"
	other.Name = "other"
	other.Spec = cloneMapValue(assistant.Spec)
	diagnostics = validateMCPAssistantPaths("", []Resource{assistant, other})
	if !hasDiagnostic(diagnostics, assistantRouteCollision) {
		t.Fatalf("assistant route diagnostics = %#v", diagnostics)
	}
}

func TestMCPReferenceCycleValidation(t *testing.T) {
	server := Resource{Address: "app/mcp_server/support", Module: "app", Kind: "scenery.mcp-server", Name: "support", Spec: map[string]any{
		"assistant": map[string]any{"$ref": "assistant.support"},
	}}
	assistant := Resource{Address: "app/assistant/support", Module: "app", Kind: "scenery.assistant", Name: "support", Spec: map[string]any{
		"mcp_server": map[string]any{"$ref": "mcp_server.support"},
	}}
	diagnostics := validateMCPGraph([]Resource{server, assistant}, resourcesByAddress(&Manifest{Resources: []Resource{server, assistant}}))
	if !hasDiagnostic(diagnostics, mcpReferenceCycle) {
		t.Fatalf("cycle diagnostics = %#v", diagnostics)
	}
}
