package vnext

import (
	"strings"

	scenery "scenery.sh"
)

func validateProfileResources(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	for _, resource := range resources {
		if resource.Origin.Kind == "legacy_v0" {
			continue
		}
		switch resource.Kind {
		case "scenery.operation/v1":
			diagnostics = append(diagnostics, validateOperation(resource, byAddress)...)
		case "scenery.execution/v1":
			diagnostics = append(diagnostics, validateExecution(resource)...)
		case "scenery.binding/v1":
			diagnostics = append(diagnostics, validateBinding(resource)...)
		case "scenery.schedule/v1":
			diagnostics = append(diagnostics, validateSchedule(resource)...)
		case "scenery.event/v1":
			if resource.Spec["payload"] == nil || !positiveInteger(resource.Spec["version"]) {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2701", "event requires payload and positive version", resource))
			}
		case "scenery.event-emission/v1":
			if missingAny(resource.Spec, "bus", "channel", "contract", "guarantee", "from") {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2702", "event emission requires bus, channel, contract, guarantee, and from", resource))
			}
		case "scenery.data-source/v1":
			if missingAny(resource.Spec, "provider", "lifecycle") {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2505", "data source requires provider and lifecycle", resource))
			}
		case "scenery.entity/v1":
			if resource.Spec["type"] == nil {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2501", "entity requires a record type", resource))
			}
			if resource.Spec["data_source"] == nil {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2502", "entity requires a data source", resource))
			}
		case "scenery.view/v1":
			if missingAny(resource.Spec, "data_source", "input", "result", "implementation") {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2503", "view requires data_source, input, result, and implementation", resource))
			}
		case "scenery.crud/v1":
			if missingAny(resource.Spec, "entity", "implementation", "actions", "execution") {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2504", "CRUD requires entity, implementation, actions, and execution", resource))
			}
		case "scenery.fixture/v1":
			if missingAny(resource.Spec, "entity", "environments", "mode", "values") {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2511", "fixture requires entity, environments, mode, and values", resource))
			}
		case "scenery.page/v1":
			if missingAny(resource.Spec, "path", "load") {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2601", "page requires path and typed load binding", resource))
			}
		case "scenery.renderer/v1":
			if missingAny(resource.Spec, "page", "runtime", "module") {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2602", "renderer requires page, runtime, and module", resource))
			}
		case "scenery.deployment/v1":
			if resource.Spec["environment"] == nil {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2805", "deployment requires environment", resource))
			}
			diagnostics = append(diagnostics, validateDeploymentDraftSurfaces(resource)...)
		}
	}
	diagnostics = append(diagnostics, validateExecutionBindings(resources)...)
	diagnostics = append(diagnostics, validateCLIBindings(resources)...)
	diagnostics = append(diagnostics, validateDurableExecutions(resources)...)
	diagnostics = append(diagnostics, validateScheduleAndEventSemantics(resources)...)
	return diagnostics
}

func validateOperation(resource Resource, resources map[string]Resource) []Diagnostic {
	raw, present := resource.Spec["idempotency"]
	if !present {
		return nil
	}
	idempotency, valid := raw.(map[string]any)
	if !valid {
		return []Diagnostic{profileDiagnostic("SCN2003", "operation idempotency must be a singleton block", resource)}
	}
	mode := stringValue(idempotency["mode"])
	if (mode == "keyed" && validKeyedIdempotency(resource, resources)) || (mode == "none" && idempotency["key"] == nil) {
		return nil
	}
	return []Diagnostic{profileDiagnostic("SCN2003", "operation idempotency must be none without a key or keyed with a non-empty ordered list of direct input-record field references", resource)}
}

func validKeyedIdempotency(operation Resource, resources map[string]Resource) bool {
	idempotency, _ := operation.Spec["idempotency"].(map[string]any)
	if stringValue(idempotency["mode"]) != "keyed" {
		return false
	}
	components, ok := idempotency["key"].([]any)
	if !ok || len(components) == 0 {
		return false
	}
	shape := resolveOperationInputShape(resources, operation)
	if shape.Record == nil {
		return false
	}
	for _, component := range components {
		name, ok := inputKeyFieldName(component)
		if !ok {
			return false
		}
		if _, exists := shape.Fields[name]; !exists {
			return false
		}
	}
	return true
}

func inputKeyFieldName(value any) (string, bool) {
	reference, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	expression := strings.TrimSpace(stringValue(reference["$expression"]))
	if expression == "" {
		expression = strings.TrimSpace(stringValue(reference["$ref"]))
	}
	name, found := strings.CutPrefix(expression, "input.")
	if !found || !validSemanticName(name) {
		return "", false
	}
	return name, true
}

func validateDeploymentDraftSurfaces(deployment Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, gateway := range namedChildren(deployment.Spec, "http_gateway") {
		for _, listener := range namedChildren(gateway, "listener") {
			for name := range listener {
				capability := authoredFieldOverrides[authoredFieldKey{Revision: deploymentListenerSourceSchema.Revision, Name: name}].UnsupportedDraft
				if capability == "" {
					continue
				}
				diagnostics = append(diagnostics, profileDiagnostic("SCN7009", "unsupported_draft: deployment listener field "+name+" requires unresolved capability "+capability, deployment))
			}
		}
	}
	return diagnostics
}

func validateExecution(resource Resource) []Diagnostic {
	mode, _ := resource.Spec["mode"].(string)
	if mode == "" || resource.Spec["operation"] == nil {
		return []Diagnostic{profileDiagnostic("SCN2200", "execution requires operation and mode", resource)}
	}
	if mode == "workflow" {
		return []Diagnostic{profileDiagnostic("SCN2204", "workflow execution requires a future workflow profile", resource)}
	}
	if mode != "durable" {
		return nil
	}
	var diagnostics []Diagnostic
	if resource.Spec["engine"] == nil {
		diagnostics = append(diagnostics, profileDiagnostic("SCN2201", "durable execution requires an engine", resource))
	}
	if !positiveInteger(resource.Spec["revision"]) {
		diagnostics = append(diagnostics, profileDiagnostic("SCN2202", "durable execution requires a positive revision", resource))
	}
	if missingAny(resource.Spec, "timeout", "lease", "attempts", "retry", "retention") {
		diagnostics = append(diagnostics, profileDiagnostic("SCN2203", "durable execution requires timeout, lease, attempts, retry, and retention", resource))
	}
	return diagnostics
}

func validateExecutionBindings(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	for _, binding := range resources {
		if binding.Kind != "scenery.binding/v1" || binding.Origin.Kind == "legacy_v0" {
			continue
		}
		executionAddress := resolveResourceRef(binding, refString(binding.Spec["execution"]), "execution")
		execution, ok := byAddress[executionAddress]
		if !ok || execution.Kind != "scenery.execution/v1" {
			continue
		}
		bindingOperation := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		executionOperation := resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation")
		if bindingOperation == "" || executionOperation == "" || bindingOperation != executionOperation {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2403", "binding operation must match its selected execution operation", binding))
		}
		delivery, mode := stringValue(binding.Spec["delivery"]), stringValue(execution.Spec["mode"])
		compatible := mode == "direct" && delivery == "call" || mode == "durable" && (delivery == "enqueue" || delivery == "wait")
		if !compatible {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2404", "binding delivery is not supported by its selected execution", binding))
		}
	}
	return diagnostics
}

func validateDurableExecutions(resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	var diagnostics []Diagnostic
	externalNames := map[string]Resource{}
	for _, execution := range resources {
		if execution.Kind != "scenery.execution/v1" || execution.Origin.Kind == "legacy_v0" || stringValue(execution.Spec["mode"]) != "durable" {
			continue
		}
		engineAddress := resolveResourceRef(execution, refString(execution.Spec["engine"]), "execution_engine")
		engine, engineOK := byAddress[engineAddress]
		if !engineOK || engine.Kind != "scenery.execution-engine/v1" {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2206", "durable execution engine must reference an execution_engine", execution))
		}
		if externalName := strings.TrimSpace(stringValue(execution.Spec["external_name"])); externalName != "" {
			key := engineAddress + "\x00" + externalName
			if previous, exists := externalNames[key]; exists {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2210", Severity: "error", Message: "durable external_name must be unique within its execution engine", Address: execution.Address, Related: []Related{{Address: previous.Address}}})
			} else {
				externalNames[key] = execution
			}
		}
		timeout, timeoutErr := scenery.ParseDuration(stringValue(execution.Spec["timeout"]))
		lease, leaseErr := scenery.ParseDuration(stringValue(execution.Spec["lease"]))
		attempts, attemptsOK := integerValue(execution.Spec["attempts"])
		if timeoutErr != nil || leaseErr != nil || timeout.Sign() <= 0 || lease.Sign() <= 0 || lease.Cmp(timeout) > 0 || !timeout.Nanoseconds().IsInt64() || !lease.Nanoseconds().IsInt64() || !attemptsOK || attempts <= 0 {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2207", "durable execution requires positive timeout, lease not exceeding timeout, and positive attempts", execution))
		}
		retry, _ := execution.Spec["retry"].(map[string]any)
		retention, _ := execution.Spec["retention"].(map[string]any)
		validRetry := validDurableRetry(retry)
		success, successErr := scenery.ParseDuration(stringValue(retention["success"]))
		failure, failureErr := scenery.ParseDuration(stringValue(retention["failure"]))
		if !validRetry || successErr != nil || failureErr != nil || success.Sign() <= 0 || failure.Sign() <= 0 || !success.Nanoseconds().IsInt64() || !failure.Nanoseconds().IsInt64() {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2208", "durable execution retry and retention policies are invalid", execution))
		}
		operationAddress := resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation")
		operation := byAddress[operationAddress]
		idempotency, _ := operation.Spec["idempotency"].(map[string]any)
		if stringValue(idempotency["mode"]) == "keyed" {
			deduplication, _ := execution.Spec["deduplication"].(map[string]any)
			retentionValue, retentionErr := scenery.ParseDuration(stringValue(deduplication["retention"]))
			if retentionErr != nil || retentionValue.Sign() <= 0 || !retentionValue.Nanoseconds().IsInt64() || stringValue(deduplication["conflict"]) != "return_existing" {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2205", "keyed-idempotent durable execution requires positive return_existing deduplication", execution))
			}
		}
		if concurrency, ok := execution.Spec["concurrency"].(map[string]any); ok {
			key := expressionText(concurrency["key"])
			limit, limitOK := integerValue(concurrency["limit"])
			if !strings.HasPrefix(key, "input.") || strings.ContainsAny(strings.TrimPrefix(key, "input."), " []()") || !limitOK || limit <= 0 {
				diagnostics = append(diagnostics, profileDiagnostic("SCN2209", "durable concurrency requires an input field key and positive limit", execution))
			}
		}
	}
	return diagnostics
}

func validDurableRetry(retry map[string]any) bool {
	strategy := stringValue(retry["strategy"])
	switch strategy {
	case "none":
		return retry["initial"] == nil && retry["maximum"] == nil && retry["factor"] == nil && retry["jitter"] == nil
	case "exponential":
		initial, initialErr := scenery.ParseDuration(stringValue(retry["initial"]))
		maximum, maximumErr := scenery.ParseDuration(stringValue(retry["maximum"]))
		factor, jitter := numericValue(retry["factor"]), numericValue(retry["jitter"])
		return initialErr == nil && maximumErr == nil && initial.Sign() > 0 && maximum.Cmp(initial) >= 0 && initial.Nanoseconds().IsInt64() && maximum.Nanoseconds().IsInt64() && factor > 1 && jitter >= 0 && jitter <= 1
	default:
		return false
	}
}

func expressionText(value any) string {
	if expression, ok := value.(map[string]any); ok {
		return strings.TrimSpace(stringValue(expression["$expression"]))
	}
	return strings.TrimSpace(stringValue(value))
}

func validateBinding(resource Resource) []Diagnostic {
	if resource.Origin.Kind == "legacy_v0" {
		return nil
	}
	var diagnostics []Diagnostic
	if missingAny(resource.Spec, "operation", "execution", "protocol", "delivery", "authentication", "authorization", "pipeline") {
		diagnostics = append(diagnostics, profileDiagnostic("SCN2401", "binding requires operation, execution, protocol, delivery, authentication, authorization, and pipeline", resource))
	}
	protocol, _ := resource.Spec["protocol"].(string)
	if protocol == "internal" {
		internal, _ := resource.Spec["internal"].(map[string]any)
		if internal == nil || internal["principal"] != "inherit" {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2402", "internal binding requires principal = inherit", resource))
		}
		visibility := stringValue(internal["visibility"])
		if visibility != "package" && visibility != "application" {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2405", "internal binding visibility must be package or application", resource))
		}
		exposure := stringValue(resource.Spec["exposure"])
		if visibility == "package" && exposure != "local" || visibility == "application" && exposure != "application" {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2406", "internal binding exposure must match its package/application visibility", resource))
		}
	}
	if protocol == "event" {
		event, _ := resource.Spec["event"].(map[string]any)
		if event == nil || missingAny(event, "direction", "bus", "channel", "contract", "guarantee") {
			diagnostics = append(diagnostics, profileDiagnostic("SCN2703", "event binding requires direction, bus, channel, contract, and guarantee", resource))
		}
	}
	return diagnostics
}

func validateSchedule(resource Resource) []Diagnostic {
	var diagnostics []Diagnostic
	trigger, _ := resource.Spec["trigger"].(map[string]any)
	selectors := 0
	for _, name := range []string{"cron", "every", "at", "calendar"} {
		if trigger != nil && trigger[name] != nil {
			selectors++
		}
	}
	if selectors != 1 {
		diagnostics = append(diagnostics, profileDiagnostic("SCN2301", "schedule requires exactly one trigger selector", resource))
	}
	invoke, _ := resource.Spec["invoke"].(map[string]any)
	if invoke == nil || missingAny(invoke, "operation", "execution", "identity", "authorization", "pipeline", "input") {
		diagnostics = append(diagnostics, profileDiagnostic("SCN2302", "schedule invoke requires operation, execution, identity, authorization, pipeline, and complete input", resource))
	}
	return diagnostics
}

func missingAny(values map[string]any, names ...string) bool {
	for _, name := range names {
		if values[name] == nil {
			return true
		}
	}
	return false
}

func positiveInteger(value any) bool {
	integer, ok := integerValue(value)
	return ok && integer > 0
}

func profileDiagnostic(code, message string, resource Resource) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address}
}
