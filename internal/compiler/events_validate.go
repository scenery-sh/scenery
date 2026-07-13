package compiler

import (
	"fmt"
	"strings"
	"time"

	scenery "scenery.sh"
	"scenery.sh/internal/calendartrigger"
)

func validateScheduleAndEventSemantics(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.schedule":
			diagnostics = append(diagnostics, validateScheduleSemantics(byAddress, resource)...)
		case "scenery.binding":
			if stringValue(resource.Spec["protocol"]) == "event" {
				diagnostics = append(diagnostics, validateEventBindingSemantics(byAddress, resource)...)
			}
		case "scenery.event-emission":
			diagnostics = append(diagnostics, validateEventEmissionSemantics(byAddress, resource)...)
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
	selected := ""
	for _, selector := range []string{"cron", "every", "at", "calendar"} {
		if trigger[selector] != nil {
			if selected != "" {
				return fmt.Errorf("schedule trigger must select exactly one of cron, every, at, or calendar")
			}
			selected = selector
		}
	}
	if selected == "" {
		return fmt.Errorf("schedule trigger must select exactly one of cron, every, at, or calendar")
	}
	switch selected {
	case "cron":
		if len(strings.Fields(stringValue(trigger["cron"]))) != 5 {
			return fmt.Errorf("schedule cron must contain five fields")
		}
		zone := defaultString(stringValue(trigger["timezone"]), "UTC")
		if _, err := time.LoadLocation(zone); err != nil {
			return fmt.Errorf("schedule timezone is invalid")
		}
	case "every":
		value, err := scenery.ParseDuration(stringValue(trigger["every"]))
		if err != nil || value.Sign() <= 0 || !value.Nanoseconds().IsInt64() {
			return fmt.Errorf("schedule every duration must be positive")
		}
	case "at":
		if _, err := scenery.ParseDateTime(stringValue(trigger["at"])); err != nil {
			return fmt.Errorf("schedule at must be an RFC 3339 datetime")
		}
	case "calendar":
		if _, err := calendartrigger.Parse(stringValue(trigger["calendar"])); err != nil {
			return fmt.Errorf("schedule calendar is invalid: %w", err)
		}
		zone := defaultString(stringValue(trigger["timezone"]), "UTC")
		if _, err := time.LoadLocation(zone); err != nil {
			return fmt.Errorf("schedule timezone is invalid")
		}
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

func validateEventBindingSemantics(resources map[string]Resource, binding Resource) []Diagnostic {
	eventSpec, _ := binding.Spec["event"].(map[string]any)
	if eventSpec == nil {
		return nil
	}
	var diagnostics []Diagnostic
	if stringValue(eventSpec["direction"]) != "consume" {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2704", "event binding direction must be consume", binding))
	}
	if !validEventGuarantee(stringValue(eventSpec["guarantee"])) || strings.TrimSpace(stringValue(eventSpec["channel"])) == "" {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2705", "event binding requires a channel and supported delivery guarantee", binding))
	}
	busAddress := resolveResourceRef(binding, refString(eventSpec["bus"]), "event_bus")
	contractAddress := resolveResourceRef(binding, refString(eventSpec["contract"]), "event")
	bus, busOK := resources[busAddress]
	event, eventOK := resources[contractAddress]
	if !busOK || bus.Kind != "scenery.event-bus" || !eventOK || event.Kind != "scenery.event" {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2705", "event binding bus and contract must resolve to typed resources", binding))
		return diagnostics
	}
	operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
	operation, operationOK := resources[operationAddress]
	mappings := namedChildren(eventSpec, "map")
	if !operationOK || len(mappings) != 1 || refOrString(mappings[0]["from"]) != "message.payload" || refOrString(mappings[0]["to"]) != "operation."+operation.Name+".input" || !sameTypeExpression(event.Spec["payload"], operation.Spec["input"]) {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2705", "event payload must map exactly once to a type-compatible operation input", binding))
	}
	if retry, ok := eventSpec["broker_retry"].(map[string]any); ok {
		attempts, attemptsOK := integerValue(retry["attempts"])
		if !attemptsOK || attempts <= 0 || !eventStringIn([]string{"none", "fixed", "exponential"}, stringValue(retry["backoff"])) {
			diagnostics = append(diagnostics, resourceDiagnostic("SCN2705", "event broker_retry policy is invalid", binding))
		}
	} else if stringValue(eventSpec["guarantee"]) == "at_least_once" {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2705", "at_least_once event consumption requires broker_retry policy", binding))
	}
	return diagnostics
}

func validateEventEmissionSemantics(resources map[string]Resource, emission Resource) []Diagnostic {
	var diagnostics []Diagnostic
	busAddress := resolveResourceRef(emission, refString(emission.Spec["bus"]), "event_bus")
	contractAddress := resolveResourceRef(emission, refString(emission.Spec["contract"]), "event")
	bus, busOK := resources[busAddress]
	event, eventOK := resources[contractAddress]
	if !busOK || bus.Kind != "scenery.event-bus" || !eventOK || event.Kind != "scenery.event" || !validEventGuarantee(stringValue(emission.Spec["guarantee"])) || strings.TrimSpace(stringValue(emission.Spec["channel"])) == "" {
		return []Diagnostic{resourceDiagnostic("SCN2706", "event emission requires typed bus, contract, channel, and guarantee", emission)}
	}
	from, _ := emission.Spec["from"].(map[string]any)
	operationAddress := resolveResourceRef(emission, refString(from["operation"]), "operation")
	operation, operationOK := resources[operationAddress]
	when := strings.Split(refOrString(from["when"]), ".")
	payload := strings.Split(refOrString(from["payload"]), ".")
	if !operationOK || len(when) != 2 || when[0] != "result" || len(payload) < 2 || payload[0] != "result" || payload[1] != when[1] {
		return []Diagnostic{resourceDiagnostic("SCN2706", "event emission must select one operation result and payload", emission)}
	}
	var resultType any
	for _, variant := range namedChildren(operation.Spec, "result") {
		if stringValue(variant["name"]) == when[1] {
			resultType = variant["type"]
			break
		}
	}
	if resultType == nil {
		return []Diagnostic{resourceDiagnostic("SCN2706", "event emission references an unknown result variant", emission)}
	}
	payloadType := resultType
	if len(payload) > 2 {
		payloadType = recordFieldType(resources, operation.Module, resultType, payload[2:])
	}
	if payloadType == nil || !sameTypeExpression(payloadType, event.Spec["payload"]) {
		diagnostics = append(diagnostics, resourceDiagnostic("SCN2706", "event emission payload type does not match the event contract", emission))
	}
	for _, field := range []string{"ordering_key", "deduplication_key"} {
		if _, _, err := eventEmissionKeyExpression(resources, operation, when[1], emission.Spec[field]); err != nil {
			diagnostics = append(diagnostics, resourceDiagnostic("SCN2706", "event emission "+field+" "+err.Error(), emission))
		}
	}
	if retry, ok := emission.Spec["broker_retry"].(map[string]any); ok {
		attempts, attemptsOK := integerValue(retry["attempts"])
		if !attemptsOK || attempts <= 0 || !eventStringIn([]string{"none", "fixed", "exponential"}, stringValue(retry["backoff"])) {
			diagnostics = append(diagnostics, resourceDiagnostic("SCN2706", "event emission broker_retry policy is invalid", emission))
		}
	}
	return diagnostics
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

func sameTypeExpression(left, right any) bool {
	return strings.TrimSpace(typeExpression(left)) == strings.TrimSpace(typeExpression(right))
}

func validEventGuarantee(value string) bool {
	return eventStringIn([]string{"at_most_once", "at_least_once", "exactly_once"}, value)
}

func eventStringIn(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
