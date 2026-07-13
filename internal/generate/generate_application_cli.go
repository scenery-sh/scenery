package generate

import (
	"fmt"
	"sort"
	"strings"
)

func cliBindingsForOperations(resources, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	var bindings []Resource
	for _, resource := range resources {
		if resource.Kind == "scenery.binding" && stringValue(resource.Spec["protocol"]) == "cli" && owned[resolveResourceRef(resource, refString(resource.Spec["operation"]), "operation")] {
			bindings = append(bindings, resource)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}

func renderCLIBindingRegistrations(b *strings.Builder, resources []Resource, service Resource, operations []Resource) error {
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	for _, binding := range cliBindingsForOperations(resources, operations) {
		if stringValue(binding.Spec["protocol"]) != "cli" {
			continue
		}
		operation := operationForBinding(operations, binding)
		if operation == nil {
			return fmt.Errorf("CLI binding %s references an unknown service operation", binding.Address)
		}
		cli, _ := binding.Spec["cli"].(map[string]any)
		command := stringValues(cli["command"])
		if len(command) == 0 {
			return fmt.Errorf("CLI binding %s has no command", binding.Address)
		}
		operationName := goName(operation.Name)
		policy := renderContractInternalPolicy(resourceMap, binding)
		mappings := make([]string, 0)
		shape := resolveOperationInputShape(resourceMap, *operation)
		for _, mapping := range namedChildren(cli, "context") {
			field, whole, ok := resolveOperationInputTarget(*operation, shape, refString(mapping["to"]))
			if !ok || whole {
				return fmt.Errorf("CLI binding %s has invalid context target", binding.Address)
			}
			mappings = append(mappings, fmt.Sprintf("{Source: %q, Target: %q}", refString(mapping["from"]), field.WireName))
		}
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractCLIBinding(sceneryruntime.ContractCLIBindingRegistration{Address: %q, Command: %#v, Policy: %s, Invoke: func(ctx context.Context, input []byte) (sceneryruntime.ContractCLIOutcome, error) {\n", binding.Address, command, policy)
		if len(mappings) > 0 {
			fmt.Fprintf(b, "\t\t\t\tinput, err := sceneryruntime.PopulateContractContextJSON(input, []sceneryruntime.ContractContextMapping{%s}); if err != nil { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractCLIInvalidInput(err) }\n", strings.Join(mappings, ", "))
		}
		fmt.Fprintf(b, "\t\t\t\ttyped, err := contract.Unmarshal%sInput(input); if err != nil { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractCLIInvalidInput(err) }\n", operationName)
		fmt.Fprintf(b, "\t\t\t\ttyped, err = contract.Clone%sInput(typed); if err != nil { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractCLIInvalidInput(err) }\n", operationName)
		delivery := stringValue(binding.Spec["delivery"])
		execution, _ := executionForBinding(resourceMap, binding)
		switch delivery {
		case "enqueue":
			if stringValue(execution.Spec["mode"]) != "durable" {
				return fmt.Errorf("enqueue CLI binding %s requires durable execution", binding.Address)
			}
			fmt.Fprintf(b, "\t\t\t\tvalue, err := sceneryruntime.InvokeContractPolicy(ctx, %s, typed, func(callCtx context.Context) (any, error) { options, err := %s(typed); if err != nil { return nil, err }; return sceneryruntime.DispatchContractDurableExecutionWithOptions(callCtx, %q, typed, options) }); if err != nil { return sceneryruntime.ContractCLIOutcome{}, err }; receipt, ok := value.(scenery.ExecutionReceipt); if !ok { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractSystemError(fmt.Errorf(\"CLI binding returned %%T, want scenery.ExecutionReceipt\", value)) }; payload, err := scenery.MarshalContractValue(receipt, \"std.type.execution_receipt\"); if err != nil { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractSystemError(err) }; return sceneryruntime.ContractCLIOutcome{Kind: \"dispatch\", Name: \"enqueued\", Payload: payload}, nil\n", policy, durableDispatchOptionsFunction(execution), execution.Address)
		case "wait":
			if stringValue(execution.Spec["mode"]) != "durable" {
				return fmt.Errorf("wait CLI binding %s requires durable execution", binding.Address)
			}
			fmt.Fprintf(b, "\t\t\t\tvalue, err := sceneryruntime.InvokeContractPolicy(ctx, %s, typed, func(callCtx context.Context) (any, error) { options, err := %s(typed); if err != nil { return nil, err }; data, err := sceneryruntime.DispatchAndWaitContractDurableExecutionWithOptions(callCtx, %q, typed, options); if err != nil { return nil, err }; return contract.Unmarshal%sOutcome(data) }); if err != nil { return sceneryruntime.ContractCLIOutcome{}, err }; outcome, ok := value.(contract.%sOutcome); if !ok { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractSystemError(fmt.Errorf(\"CLI binding returned %%T, want contract.%sOutcome\", value)) }; return render%sCLIOutcome(outcome)\n", policy, durableDispatchOptionsFunction(execution), execution.Address, operationName, operationName, operationName, operationName)
		default:
			handler, _ := operation.Spec["handler"].(map[string]any)
			method := stringValue(handler["method"])
			fmt.Fprintf(b, "\t\t\t\tvalue, err := sceneryruntime.InvokeContractPolicy(ctx, %s, typed, func(callCtx context.Context) (any, error) { if service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; outcome, err := service.%s(callCtx, typed); if err != nil { if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome\")) }; return outcome, nil }); if err != nil { return sceneryruntime.ContractCLIOutcome{}, err }; outcome, ok := value.(contract.%sOutcome); if !ok { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractSystemError(fmt.Errorf(\"CLI binding returned %%T, want contract.%sOutcome\", value)) }; return render%sCLIOutcome(outcome)\n", policy, method, operationName, operationName, operationName)
		}
		b.WriteString("\t\t\t}}); err != nil { return err }\n")
	}
	return nil
}

func renderCLIOutcomeHelpers(b *strings.Builder, resources []Resource, operations []Resource) {
	seen := map[string]bool{}
	for _, binding := range cliBindingsForOperations(resources, operations) {
		if stringValue(binding.Spec["protocol"]) != "cli" {
			continue
		}
		operation := operationForBinding(operations, binding)
		if operation == nil || seen[operation.Address] {
			continue
		}
		seen[operation.Address] = true
		name := goName(operation.Name)
		fmt.Fprintf(b, "func render%sCLIOutcome(outcome contract.%sOutcome) (sceneryruntime.ContractCLIOutcome, error) { raw, err := contract.Marshal%sOutcome(outcome); if err != nil { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractSystemError(err) }; kind, name, payload, err := scenery.DecodeContractOutcomeEnvelope(raw); if err != nil { return sceneryruntime.ContractCLIOutcome{}, sceneryruntime.ContractSystemError(err) }; return sceneryruntime.ContractCLIOutcome{Kind: kind, Name: name, Payload: payload}, nil }\n\n", name, name, name)
	}
}
