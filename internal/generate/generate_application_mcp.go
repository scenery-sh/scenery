package generate

import (
	"fmt"
	"sort"
	"strings"
)

// mcpToolTarget is the provider-neutral projection consumed by generated
// adapter rendering. The generated invoke closure still lives beside the
// ordinary service adapter so policy and durable dispatch cannot drift.
type mcpToolTarget struct {
	Binding          Resource
	Operation        Resource
	Execution        Resource
	AssistantAddress string
	Name             string
	Title            string
	Description      string
	Approval         string
	ReadOnly         bool
	Destructive      bool
	Idempotent       bool
	OpenWorld        bool
	MaxInputBytes    int64
	MaxResultBytes   int64
	Durable          bool
	DurableService   string
	DurableTask      string
}

func mcpBindingsForService(resources []Resource, service Resource, operations []Resource) []mcpToolTarget {
	ownedOperations := map[string]Resource{}
	for _, operation := range operations {
		ownedOperations[operation.Address] = operation
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var targets []mcpToolTarget
	for _, binding := range resources {
		if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "mcp" {
			continue
		}
		operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		operation, owned := ownedOperations[operationAddress]
		if !owned {
			continue
		}
		execution, ok := executionForBinding(byAddress, binding)
		if !ok {
			continue
		}
		mcpSpec, _ := binding.Spec["mcp"].(map[string]any)
		for _, server := range resources {
			if server.Kind != "scenery.mcp-server" {
				continue
			}
			for _, capability := range namedChildren(server.Spec, "capability") {
				if resolveResourceRef(server, refString(capability["binding"]), "binding") != binding.Address {
					continue
				}
				for _, assistant := range resources {
					if assistant.Kind != "scenery.assistant" || resolveResourceRef(assistant, refString(assistant.Spec["mcp_server"]), "mcp_server") != server.Address {
						continue
					}
					maxInputValue, _ := integerValue(server.Spec["max_input_bytes"])
					maxResultValue, _ := integerValue(server.Spec["max_result_bytes"])
					maxInput, maxResult := int64(maxInputValue), int64(maxResultValue)
					target := mcpToolTarget{
						Binding: binding, Operation: operation, Execution: execution, AssistantAddress: assistant.Address,
						Name: stringValue(capability["name"]), Title: stringValue(mcpSpec["title"]), Description: stringValue(mcpSpec["description"]),
						Approval: stringValue(capability["approval"]), ReadOnly: mcpSpec["read_only"] == true, Destructive: mcpSpec["destructive"] == true,
						Idempotent: mcpSpec["idempotent"] == true, OpenWorld: mcpSpec["open_world"] == true,
						MaxInputBytes: maxInput, MaxResultBytes: maxResult,
						Durable: stringValue(execution.Spec["mode"]) == "durable", DurableService: service.Name,
						DurableTask: stringValue(execution.Spec["external_name"]),
					}
					if target.DurableTask == "" {
						target.DurableTask = execution.Address
					}
					targets = append(targets, target)
				}
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].AssistantAddress != targets[j].AssistantAddress {
			return targets[i].AssistantAddress < targets[j].AssistantAddress
		}
		if targets[i].Name != targets[j].Name {
			return targets[i].Name < targets[j].Name
		}
		return targets[i].Binding.Address < targets[j].Binding.Address
	})
	return targets
}

func mcpBindingResources(targets []mcpToolTarget) []Resource {
	resources := make([]Resource, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		if target.Binding.Address == "" || seen[target.Binding.Address] {
			continue
		}
		seen[target.Binding.Address] = true
		resources = append(resources, target.Binding)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
	return resources
}

func renderMCPToolRegistrations(b *strings.Builder, contractRevision string, service Resource, targets []mcpToolTarget, resources []Resource) error {
	if len(targets) == 0 {
		return nil
	}
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	for _, target := range targets {
		operationName := goName(target.Operation.Name)
		handlerMethod := operationHandlerMethod(target.Operation)
		if handlerMethod == "" {
			return fmt.Errorf("MCP binding %s operation %s has no handler method", target.Binding.Address, target.Operation.Address)
		}
		policy := renderContractInvocationPolicy(resourceMap, target.Binding, target.Binding.Address, target.Binding.Spec["authorization"], target.Binding.Spec["pipeline"])
		registrationID := target.AssistantAddress + "#" + target.Binding.Address
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterMCPTool(sceneryruntime.MCPToolRegistration{ID: %q, Name: %q, AssistantAddress: %q, CapabilityRevision: %q, OperationAddress: %q, ExecutionAddress: %q, Policy: %s, Limits: sceneryruntime.MCPToolLimits{MaxInputBytes: %d, MaxResultBytes: %d}, Effect: sceneryruntime.MCPToolEffect{ReadOnly: %t, Destructive: %t, Idempotent: %t, OpenWorld: %t}, Approval: %q, Durable: %t, DurableService: %q, DurableTask: %q, ", registrationID, target.Name, target.AssistantAddress, contractRevision, target.Operation.Address, target.Execution.Address, policy, target.MaxInputBytes, target.MaxResultBytes, target.ReadOnly, target.Destructive, target.Idempotent, target.OpenWorld, target.Approval, target.Durable, target.DurableService, target.DurableTask)
		fmt.Fprintf(b, "DecodeInput: func(data []byte) (any, error) { return contract.Unmarshal%sInput(data) }, ", operationName)
		if target.Durable {
			b.WriteString("EncodeOutput: func(value any) ([]byte, error) { receipt, ok := value.(scenery.ExecutionReceipt); if !ok { return nil, fmt.Errorf(\"MCP durable tool returned %T, want scenery.ExecutionReceipt\", value) }; return scenery.MarshalContractValue(receipt, \"std.type.execution_receipt\") }, ")
		} else {
			fmt.Fprintf(b, "EncodeOutput: func(value any) ([]byte, error) { typed, ok := value.(contract.%sOutcome); if !ok { return nil, fmt.Errorf(\"MCP tool returned %%T, want contract.%sOutcome\", value) }; return contract.Marshal%sOutcome(typed) }, ", operationName, operationName, operationName)
		}
		fmt.Fprintf(b, "Invoke: func(ctx context.Context, call sceneryruntime.MCPToolCallContext, input any) (any, error) { typed, ok := input.(contract.%sInput); if !ok { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"MCP input has type %%T\", input)) }; copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; ", operationName, operationName)
		if target.Durable {
			optionsFunction := durableDispatchOptionsFunction(target.Execution)
			fmt.Fprintf(b, "options, err := %s(copied); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; if call.IdempotencyKey != \"\" { options.DedupeKey = call.IdempotencyKey }; return sceneryruntime.DispatchContractDurableExecutionWithOptions(ctx, %q, copied, options) }, }); err != nil { return err }\n", optionsFunction, target.Execution.Address)
		} else if operationUsesHTTPStream(target.Operation, resources) {
			fmt.Fprintf(b, "if service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; outcome, stream, err := service.%s(ctx, copied); if err != nil { _ = stream.Close(); if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome without error\")) }; ", handlerMethod)
			if err := renderMCPStreamOutcomeBuffer(b, target, resources); err != nil {
				return err
			}
			fmt.Fprintf(b, "cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return cloned, nil }, }); err != nil { return err }\n", operationName)
		} else {
			fmt.Fprintf(b, "if service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; outcome, err := service.%s(ctx, copied); if err != nil { if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome without error\")) }; cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return cloned, nil }, }); err != nil { return err }\n", handlerMethod, operationName)
		}
	}
	return nil
}

func renderMCPStreamOutcomeBuffer(b *strings.Builder, target mcpToolTarget, resources []Resource) error {
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	var streamBinding Resource
	for _, binding := range resources {
		if stringValue(binding.Spec["protocol"]) == "http" && stringValue(binding.Spec["delivery"]) == "stream" && resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation") == target.Operation.Address {
			streamBinding = binding
			break
		}
	}
	if streamBinding.Address == "" {
		return fmt.Errorf("MCP binding %s has no HTTP stream binding for operation %s", target.Binding.Address, target.Operation.Address)
	}
	httpSpec, _ := streamBinding.Spec["http"].(map[string]any)
	b.WriteString("switch typed := outcome.(type) { ")
	for _, variant := range namedChildren(target.Operation.Spec, "result") {
		name := stringValue(variant["name"])
		response := responseMappings(httpSpec)["result."+name]
		body, _ := response["body"].(map[string]any)
		if body == nil || stringValue(body["codec"]) != "bytes" {
			return fmt.Errorf("MCP binding %s stream result %s has no bytes response", target.Binding.Address, name)
		}
		wrapper := goName(target.Operation.Name) + goName(name)
		expression, _, err := httpOutcomeValueExpression(resourceMap, target.Operation, "result."+name, refOrString(body["from"]), "typed.Value", variant["type"])
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "case contract.%s: if len(%s) != 0 { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"stream handler returned buffered bytes\")) }; buffered, err := sceneryruntime.BufferContractByteStream(stream, %d); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; %s = buffered; outcome = typed; ", wrapper, expression, target.MaxResultBytes, expression)
		pointerExpression := strings.Replace(expression, "typed.", "copiedOutcome.", 1)
		fmt.Fprintf(b, "case *contract.%s: if typed == nil { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome\")) }; copiedOutcome := *typed; if len(%s) != 0 { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"stream handler returned buffered bytes\")) }; buffered, err := sceneryruntime.BufferContractByteStream(stream, %d); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; %s = buffered; outcome = &copiedOutcome; ", wrapper, pointerExpression, target.MaxResultBytes, pointerExpression)
	}
	for _, variant := range namedChildren(target.Operation.Spec, "error") {
		wrapper := goName(target.Operation.Name) + goName(stringValue(variant["name"]))
		fmt.Fprintf(b, "case contract.%s, *contract.%s: if stream.Reader != nil { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"error outcome returned a byte stream\")) }; ", wrapper, wrapper)
	}
	b.WriteString("default: _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned an unknown outcome %T\", outcome)) }; ")
	return nil
}
