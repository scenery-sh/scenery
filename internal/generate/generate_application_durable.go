package generate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func durableExecutionsForOperations(resources []Resource, operations []Resource) []Resource {
	operationAddresses := map[string]bool{}
	for _, operation := range operations {
		operationAddresses[operation.Address] = true
	}
	var executions []Resource
	for _, execution := range resources {
		if execution.Kind != "scenery.execution" || stringValue(execution.Spec["mode"]) != "durable" {
			continue
		}
		operationAddress := resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation")
		if operationAddresses[operationAddress] {
			executions = append(executions, execution)
		}
	}
	sort.Slice(executions, func(i, j int) bool { return executions[i].Address < executions[j].Address })
	return executions
}

func executionForBinding(resources map[string]Resource, binding Resource) (Resource, bool) {
	address := resolveResourceRef(binding, refString(binding.Spec["execution"]), "execution")
	execution, ok := resources[address]
	return execution, ok && execution.Kind == "scenery.execution"
}

func renderDurableExecutionRegistrations(b *strings.Builder, service Resource, operations, resources []Resource) error {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for _, execution := range durableExecutionsForOperations(resources, operations) {
		operationAddress := resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation")
		operation, ok := byAddress[operationAddress]
		if !ok {
			return fmt.Errorf("durable execution %s references an unknown operation", execution.Address)
		}
		revision, ok := integerValue(execution.Spec["revision"])
		if !ok || revision <= 0 {
			return fmt.Errorf("durable execution %s has invalid revision", execution.Address)
		}
		engineAddress := resolveResourceRef(execution, refString(execution.Spec["engine"]), "execution_engine")
		if engineAddress == "" {
			return fmt.Errorf("durable execution %s has no resolved engine", execution.Address)
		}
		attempts, ok := integerValue(execution.Spec["attempts"])
		if !ok || attempts <= 0 {
			return fmt.Errorf("durable execution %s has invalid attempts", execution.Address)
		}
		retry, _ := execution.Spec["retry"].(map[string]any)
		retention, _ := execution.Spec["retention"].(map[string]any)
		concurrency, _ := execution.Spec["concurrency"].(map[string]any)
		deduplication, _ := execution.Spec["deduplication"].(map[string]any)
		maxConcurrency, _ := integerValue(concurrency["limit"])
		factor := numericValue(retry["factor"])
		jitter := numericValue(retry["jitter"])
		handler, _ := operation.Spec["handler"].(map[string]any)
		method := stringValue(handler["method"])
		if method == "" {
			return fmt.Errorf("durable operation %s has no Go handler method", operation.Address)
		}
		operationName := goName(operation.Name)
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractDurableExecution(sceneryruntime.ContractDurableRegistration{\n")
		fmt.Fprintf(b, "\t\t\t\tAddress: %q, ExternalName: %q, EngineAddress: %q, Service: %q, Revision: %d,\n", execution.Address, stringValue(execution.Spec["external_name"]), engineAddress, service.Name, revision)
		fmt.Fprintf(b, "\t\t\t\tDefaultTimeout: %d, DefaultLease: %d, MaxAttempts: %d, RetryInitial: %d, RetryMax: %d, RetryBackoff: %s, RetryJitter: %s, SuccessRetention: %d, FailureRetention: %d, MaxConcurrency: %d, DeduplicationRetention: %d, DeduplicationConflict: %q,\n", durationNanos(execution.Spec["timeout"]), durationNanos(execution.Spec["lease"]), attempts, durationNanos(retry["initial"]), durationNanos(retry["maximum"]), goFloatLiteral(factor), goFloatLiteral(jitter), durationNanos(retention["success"]), durationNanos(retention["failure"]), maxConcurrency, durationNanos(deduplication["retention"]), stringValue(deduplication["conflict"]))
		fmt.Fprintf(b, "\t\t\t\tHandler: func(ctx context.Context, data []byte) ([]byte, error) { if service == nil { return nil, fmt.Errorf(\"service is not initialized\") }; var input contract.%sInput; if err := scenery.UnmarshalContractValue(data, &input, %q); err != nil { return nil, fmt.Errorf(\"decode durable input: %%w\", err) }; copied, err := contract.Clone%sInput(input); if err != nil { return nil, err }; outcome, err := service.%s(ctx, copied); if err != nil { if outcome != nil { return nil, fmt.Errorf(\"handler returned outcome and error\") }; return nil, err }; if outcome == nil { return nil, fmt.Errorf(\"handler returned nil outcome without error\") }; cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, err }; if err := sceneryruntime.PublishContractOperationOutcome(ctx, %q, cloned); err != nil { return nil, err }; return contract.Marshal%sOutcome(cloned) },\n", operationName, goWireTypeExpression(operation.Spec["input"]), operationName, method, operationName, operation.Address, operationName)
		b.WriteString("\t\t\t}); err != nil { return err }\n")
	}
	return nil
}

func renderDurableDispatchOptionHelpers(b *strings.Builder, operations, resources []Resource) error {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for _, execution := range durableExecutionsForOperations(resources, operations) {
		operationAddress := resolveResourceRef(execution, refString(execution.Spec["operation"]), "operation")
		operation, ok := byAddress[operationAddress]
		if !ok {
			return fmt.Errorf("durable execution %s references an unknown operation", execution.Address)
		}
		shape := resolveOperationInputShape(byAddress, operation)
		functionName := durableDispatchOptionsFunction(execution)
		fmt.Fprintf(b, "func %s(input contract.%sInput) (sceneryruntime.ContractDurableDispatchOptions, error) {\n", functionName, goName(operation.Name))
		b.WriteString("\toptions := sceneryruntime.ContractDurableDispatchOptions{}\n")
		idempotency, _ := operation.Spec["idempotency"].(map[string]any)
		if stringValue(idempotency["mode"]) == "keyed" {
			components, err := renderDurableKeyComponents(b, shape, idempotency["key"], "deduplication")
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "\tdedupeKey, err := scenery.EncodeContractCompositeKey(%s); if err != nil { return options, fmt.Errorf(\"encode durable deduplication key: %%w\", err) }; options.DedupeKey = dedupeKey\n", strings.Join(components, ", "))
		}
		if concurrency, ok := execution.Spec["concurrency"].(map[string]any); ok {
			components, err := renderDurableKeyComponents(b, shape, []any{concurrency["key"]}, "concurrency")
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "\tconcurrencyKey, err := scenery.EncodeContractCompositeKey(%s); if err != nil { return options, fmt.Errorf(\"encode durable concurrency key: %%w\", err) }; options.ConcurrencyKey = concurrencyKey\n", strings.Join(components, ", "))
		}
		b.WriteString("\treturn options, nil\n}\n\n")
	}
	return nil
}

func renderDurableKeyComponents(b *strings.Builder, shape operationInputShape, value any, purpose string) ([]string, error) {
	var values []any
	switch typed := value.(type) {
	case []any:
		values = typed
	case nil:
		return nil, fmt.Errorf("durable %s key is missing", purpose)
	default:
		return nil, fmt.Errorf("durable %s key must be an ordered component list", purpose)
	}
	if len(values) == 0 || shape.Record == nil {
		return nil, fmt.Errorf("durable %s key requires a record input", purpose)
	}
	components := make([]string, len(values))
	for index, value := range values {
		fieldName, ok := inputKeyFieldName(value)
		field, exists := shape.Fields[fieldName]
		if !ok || !exists {
			return nil, fmt.Errorf("durable %s key expression %q is not a direct input field", purpose, expressionText(value))
		}
		component := fmt.Sprintf("component%d", index)
		if purpose == "concurrency" {
			component = fmt.Sprintf("concurrencyComponent%d", index)
		}
		fmt.Fprintf(b, "\t%s, err := scenery.EncodeContractKeyComponent(input.%s, %q); if err != nil { return options, fmt.Errorf(\"encode durable %s component %d: %%w\", err) }\n", component, goName(field.Name), goWireTypeExpression(field.Type), purpose, index)
		components[index] = component
	}
	return components, nil
}

func durableDispatchOptionsFunction(execution Resource) string {
	name := goName(execution.Name) + "DispatchOptions"
	if name == "" {
		return "durableDispatchOptions"
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func numericValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	case map[string]any:
		parsed, _ := strconv.ParseFloat(stringValue(typed), 64)
		return parsed
	default:
		return 0
	}
}

func goFloatLiteral(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
