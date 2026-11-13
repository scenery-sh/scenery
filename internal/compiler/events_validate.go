package compiler

import (
	"fmt"
	"slices"
	"strings"

	scenery "scenery.sh/internal/contract"
)

func validateSchedulesSemantics(resources []Resource, byAddress map[string]Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, resource := range resources {
		if resource.Kind == "scenery.schedule" {
			diagnostics = append(diagnostics, validateScheduleSemantics(byAddress, resource)...)
		}
	}
	return diagnostics
}

func validateScheduleSemantics(resources map[string]Resource, schedule Resource) []Diagnostic {
	var diagnostics []Diagnostic
	trigger, _ := schedule.Spec["trigger"].(map[string]any)
	if err := validateScheduleTrigger(trigger); err != nil {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2304", err.Error(), schedule))
	}
	if overlap := stringValue(schedule.Spec["overlap"]); overlap != "skip" && overlap != "queue" && overlap != "replace" && overlap != "allow" {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2304", "schedule overlap must be skip, queue, replace, or allow", schedule))
	}
	if catchup, ok := schedule.Spec["catchup"].(map[string]any); ok {
		maximumAge, err := scenery.ParseDuration(stringValue(catchup["maximum_age"]))
		if err != nil || maximumAge.Sign() <= 0 || !maximumAge.Nanoseconds().IsInt64() {
			diagnostics = append(diagnostics, resourceDiagnostic("SCN2304", "schedule catchup maximum_age must be positive", schedule))
		}
	}
	invoke, _ := schedule.Spec["invoke"].(map[string]any)
	if invoke == nil {
		return diagnostics
	}
	operationAddress := resolveResourceRef(schedule, refString(invoke["operation"]), "operation")
	operation, operationOK := resources[operationAddress]
	executionAddress := resolveResourceRef(schedule, refString(invoke["execution"]), "execution")
	execution, executionOK := resources[executionAddress]
	if !operationOK || operation.Kind != "scenery.operation" || !executionOK || execution.Kind != "scenery.execution" || resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation") != operationAddress {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2303", "schedule operation and execution must resolve to the same operation", schedule))
		return diagnostics
	}
	if refString(invoke["identity"]) == "" || refString(invoke["authorization"]) == "" || refString(invoke["pipeline"]) == "" {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2303", "schedule requires typed identity, authorization, and pipeline references", schedule))
	} else if err := validateScheduleWorkloadIdentity(resources, schedule, invoke["identity"]); err != nil {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2303", err.Error(), schedule))
	}
	if err := validateStaticOperationInput(resources, operation, invoke["input"]); err != nil {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2303", err.Error(), schedule))
	}
	return diagnostics
}

func validateScheduleWorkloadIdentity(resources map[string]Resource, schedule Resource, value any) error {
	reference := refString(value)
	if reference == "std.workload_identity.scheduler" {
		return nil
	}
	address := resolveResourceRef(schedule, reference, "workload_identity")
	identity, ok := resources[address]
	if !ok || identity.Kind != "scenery.workload-identity" {
		return fmt.Errorf("schedule identity must resolve to a workload_identity")
	}
	if refOrString(identity.Spec["issuer"]) == "" || refOrString(identity.Spec["principal_type"]) == "" {
		return fmt.Errorf("schedule workload identity requires issuer and principal_type")
	}
	claims, ok := identity.Spec["claims"].(map[string]any)
	if !ok {
		return fmt.Errorf("schedule workload identity claims must be an object")
	}
	for _, reserved := range []string{"issuer", "principal_type", "workload_identity"} {
		if _, exists := claims[reserved]; exists {
			return fmt.Errorf("schedule workload identity claim %s is reserved", reserved)
		}
	}
	return nil
}

func validateScheduleTrigger(trigger map[string]any) error {
	if trigger == nil {
		return fmt.Errorf("schedule requires a trigger")
	}
	value, err := scenery.ParseDuration(stringValue(trigger["every"]))
	if err != nil || value.Sign() <= 0 || !value.Nanoseconds().IsInt64() {
		return fmt.Errorf("schedule every duration must be positive")
	}
	return nil
}

func validateStaticOperationInput(resources map[string]Resource, operation Resource, value any) error {
	if value == nil {
		return fmt.Errorf("schedule input is required")
	}
	if err := validateFixtureValue(value, typeExpression(operation.Spec["input"]), operation.Module, resources); err != nil {
		return fmt.Errorf("schedule input does not match %s: %w", typeExpression(operation.Spec["input"]), err)
	}
	return nil
}

func recordFieldType(resources map[string]Resource, module string, value any, path []string) any {
	current := value
	for _, name := range path {
		record, ok := recordResourceForType(resources, module, current)
		if !ok {
			return nil
		}
		module = record.Module
		current = nil
		for _, field := range namedChildren(record.Spec, "field") {
			if stringValue(field["name"]) == name {
				current = field["type"]
				break
			}
		}
		if current == nil {
			return nil
		}
	}
	return current
}

func recordResourceForType(resources map[string]Resource, module string, value any) (Resource, bool) {
	reference := refString(value)
	if index := strings.Index(reference, "/record/"); index > 0 {
		record, ok := resources[reference]
		return record, ok
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 || parts[0] != "record" {
		return Resource{}, false
	}
	record, ok := resources[resourceAddress(module, "record", parts[1])]
	return record, ok
}

func eventStringIn(values []string, want string) bool {
	return slices.Contains(values, want)
}
