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
	identity, err := renderWorkloadIdentity(resources, schedule, invoke["identity"])
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

func renderWorkloadIdentity(resources map[string]Resource, owner Resource, value any) (string, error) {
	reference := refOrString(value)
	address, issuer, principalType := reference, "std.identity_issuer.runtime", "std.type.workload_principal"
	claims := map[string]any{"workload": lastRef(reference)}
	if !strings.HasPrefix(reference, "std.workload_identity.") {
		address = resolveResourceRef(owner, refString(value), "workload_identity")
		identity, ok := resources[address]
		if !ok || identity.Kind != "scenery.workload-identity" {
			return "", fmt.Errorf("reference %s does not resolve", reference)
		}
		issuer, principalType = refOrString(identity.Spec["issuer"]), refOrString(identity.Spec["principal_type"])
		claims, _ = identity.Spec["claims"].(map[string]any)
	}
	encoded, err := MarshalCanonical(claims)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sceneryruntime.ContractWorkloadIdentity{Address: %q, Issuer: %q, PrincipalType: %q, ClaimsJSON: %q}", address, issuer, principalType, string(encoded)), nil
}
