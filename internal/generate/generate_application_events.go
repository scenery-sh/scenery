package generate

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func schedulesForOperations(resources, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	var schedules []Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.schedule" {
			continue
		}
		invoke, _ := resource.Spec["invoke"].(map[string]any)
		if owned[resolveResourceRef(resource, refString(invoke["operation"]), "operation")] {
			schedules = append(schedules, resource)
		}
	}
	sort.Slice(schedules, func(i, j int) bool { return schedules[i].Address < schedules[j].Address })
	return schedules
}

func operationAddressSet(operations []Resource) map[string]bool {
	owned := make(map[string]bool, len(operations))
	for _, operation := range operations {
		owned[operation.Address] = true
	}
	return owned
}

func renderScheduleRegistrations(b *strings.Builder, operations, resources []Resource) error {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for _, schedule := range schedulesForOperations(resources, operations) {
		if err := renderScheduleRegistration(b, byAddress, operations, schedule); err != nil {
			return err
		}
	}
	return nil
}

func renderScheduleRegistration(b *strings.Builder, resources map[string]Resource, operations []Resource, schedule Resource) error {
	invoke, _ := schedule.Spec["invoke"].(map[string]any)
	operationAddress := resolveResourceRef(schedule, refString(invoke["operation"]), "operation")
	var operation *Resource
	for index := range operations {
		if operations[index].Address == operationAddress {
			operation = &operations[index]
			break
		}
	}
	if operation == nil {
		return fmt.Errorf("schedule %s references an unknown operation", schedule.Address)
	}
	executionAddress := resolveResourceRef(schedule, refString(invoke["execution"]), "execution")
	execution, executionOK := resources[executionAddress]
	if !executionOK {
		return fmt.Errorf("schedule %s references an unknown execution", schedule.Address)
	}
	trigger, _ := schedule.Spec["trigger"].(map[string]any)
	triggerKind, triggerValue := "", ""
	if trigger["every"] != nil {
		triggerKind, triggerValue = "every", time.Duration(durationNanos(trigger["every"])).String()
	}
	inputJSON, err := goConfigWireJSON(invoke["input"], goWireTypeExpression(operation.Spec["input"]))
	if err != nil {
		return fmt.Errorf("schedule %s input: %w", schedule.Address, err)
	}
	catchup, _ := schedule.Spec["catchup"].(map[string]any)
	operationName := goName(operation.Name)
	policy := renderContractInvocationPolicy(resources, schedule, schedule.Address, invoke["authorization"], invoke["pipeline"])
	identity, err := renderWorkloadIdentity(invoke["identity"])
	if err != nil {
		return fmt.Errorf("schedule %s identity: %w", schedule.Address, err)
	}
	fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractSchedule(sceneryruntime.ContractScheduleRegistration{\n")
	fmt.Fprintf(b, "\t\t\t\tAddress: %q, Name: %q, TriggerKind: %q, TriggerValue: %q, Overlap: %q, CatchupMaximumAge: %d, Identity: %s, AuthorizationAddress: %q, PipelineAddress: %q, Policy: %s,\n", schedule.Address, schedule.Name, triggerKind, triggerValue, stringValue(schedule.Spec["overlap"]), durationNanos(catchup["maximum_age"]), identity, refOrString(invoke["authorization"]), refOrString(invoke["pipeline"]), policy)
	fmt.Fprintf(b, "\t\t\t\tInvoke: func(ctx context.Context) error { var input contract.%sInput; if err := scenery.UnmarshalContractValue([]byte(%q), &input, %q); err != nil { return fmt.Errorf(\"decode schedule input: %%w\", err) }; copied, err := contract.Clone%sInput(input); if err != nil { return err }; _, err = sceneryruntime.InvokeContractPolicy(ctx, %s, copied, func(callCtx context.Context) (any, error) { ", operationName, string(inputJSON), goWireTypeExpression(operation.Spec["input"]), operationName, policy)
	if stringValue(execution.Spec["mode"]) == "durable" {
		fmt.Fprintf(b, "options, err := %s(copied); if err != nil { return nil, err }; return sceneryruntime.DispatchContractDurableExecutionWithOptions(callCtx, %q, copied, options) }); return err },\n", durableDispatchOptionsFunction(execution), execution.Address)
	} else {
		handler, _ := operation.Spec["handler"].(map[string]any)
		fmt.Fprintf(b, "if service == nil { return nil, fmt.Errorf(\"service is not initialized\") }; outcome, err := service.%s(callCtx, copied); if err != nil { if outcome != nil { return nil, fmt.Errorf(\"handler returned outcome and error\") }; return nil, err }; if outcome == nil { return nil, fmt.Errorf(\"handler returned nil outcome without error\") }; cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, err }; return cloned, nil }); return err },\n", stringValue(handler["method"]), operationName)
	}
	b.WriteString("\t\t\t}); err != nil { return err }\n")
	return nil
}

func renderWorkloadIdentity(value any) (string, error) {
	const address = "std.workload_identity.scheduler"
	if refOrString(value) != address {
		return "", fmt.Errorf("schedule identity must be %s", address)
	}
	return `sceneryruntime.ContractWorkloadIdentity{Address: "std.workload_identity.scheduler", Issuer: "std.identity_issuer.runtime", PrincipalType: "std.type.workload_principal", ClaimsJSON: "{\"workload\":\"scheduler\"}"}`, nil
}
