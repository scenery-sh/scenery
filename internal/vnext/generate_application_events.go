package vnext

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func eventBindingsForOperations(resources, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	var bindings []Resource
	for _, resource := range resources {
		if resource.Kind == "scenery.binding/v1" && stringValue(resource.Spec["protocol"]) == "event" && owned[resolveResourceRef(resource, refString(resource.Spec["operation"]), "operation")] {
			bindings = append(bindings, resource)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}

func schedulesForOperations(resources, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	var schedules []Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.schedule/v1" {
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

func eventEmissionsForOperations(resources, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	var emissions []Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.event-emission/v1" {
			continue
		}
		from, _ := resource.Spec["from"].(map[string]any)
		if owned[resolveResourceRef(resource, refString(from["operation"]), "operation")] {
			emissions = append(emissions, resource)
		}
	}
	sort.Slice(emissions, func(i, j int) bool { return emissions[i].Address < emissions[j].Address })
	return emissions
}

func operationAddressSet(operations []Resource) map[string]bool {
	owned := make(map[string]bool, len(operations))
	for _, operation := range operations {
		owned[operation.Address] = true
	}
	return owned
}

func renderScheduleAndEventRegistrations(b *strings.Builder, operations, resources []Resource) error {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for _, emission := range eventEmissionsForOperations(resources, operations) {
		if err := renderEventEmissionRegistration(b, byAddress, operations, emission); err != nil {
			return err
		}
	}
	for _, binding := range eventBindingsForOperations(resources, operations) {
		if err := renderEventConsumerRegistration(b, byAddress, operations, binding); err != nil {
			return err
		}
	}
	for _, schedule := range schedulesForOperations(resources, operations) {
		if err := renderScheduleRegistration(b, byAddress, operations, schedule); err != nil {
			return err
		}
	}
	return nil
}

func renderEventConsumerRegistration(b *strings.Builder, resources map[string]Resource, operations []Resource, binding Resource) error {
	operation := operationForBinding(operations, binding)
	if operation == nil {
		return fmt.Errorf("event binding %s references an unknown operation", binding.Address)
	}
	eventSpec, _ := binding.Spec["event"].(map[string]any)
	contractAddress := resolveResourceRef(binding, refString(eventSpec["contract"]), "event")
	contract, ok := resources[contractAddress]
	if !ok || contract.Kind != "scenery.event/v1" {
		return fmt.Errorf("event binding %s references an unknown event contract", binding.Address)
	}
	version, ok := integerValue(contract.Spec["version"])
	if !ok || version <= 0 {
		return fmt.Errorf("event contract %s has invalid version", contract.Address)
	}
	retry, _ := eventSpec["broker_retry"].(map[string]any)
	attempts, _ := integerValue(retry["attempts"])
	if attempts <= 0 {
		attempts = 1
	}
	backoff := stringValue(retry["backoff"])
	if backoff == "" {
		backoff = "none"
	}
	execution, executionOK := executionForBinding(resources, binding)
	operationName := goName(operation.Name)
	policy := renderContractInternalPolicy(resources, binding)
	fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractEventConsumer(sceneryruntime.ContractEventConsumerRegistration{\n")
	fmt.Fprintf(b, "\t\t\t\tAddress: %q, BusAddress: %q, Channel: %q, ContractAddress: %q, ContractVersion: %d, Guarantee: %q, Identity: %q, Attempts: %d, Backoff: %q, DeadLetterChannel: %q, Policy: %s,\n", binding.Address, resolveResourceRef(binding, refString(eventSpec["bus"]), "event_bus"), stringValue(eventSpec["channel"]), contract.Address, version, stringValue(eventSpec["guarantee"]), refOrString(binding.Spec["authentication"]), attempts, backoff, stringValue(eventSpec["dead_letter_channel"]), policy)
	fmt.Fprintf(b, "\t\t\t\tInvoke: func(ctx context.Context, payload []byte) error { var input contract.%sInput; if err := scenery.UnmarshalContractValue(payload, &input, %q); err != nil { return fmt.Errorf(\"decode event input: %%w\", err) }; copied, err := contract.Clone%sInput(input); if err != nil { return err }; _, err = sceneryruntime.InvokeContractPolicy(ctx, %s, copied, func(callCtx context.Context) (any, error) { ", operationName, goWireTypeExpression(operation.Spec["input"]), operationName, policy)
	if stringValue(binding.Spec["delivery"]) == "enqueue" {
		if !executionOK || stringValue(execution.Spec["mode"]) != "durable" {
			return fmt.Errorf("event binding %s enqueue delivery requires durable execution", binding.Address)
		}
		fmt.Fprintf(b, "options, err := %s(copied); if err != nil { return nil, err }; return sceneryruntime.DispatchContractDurableExecutionWithOptions(callCtx, %q, copied, options) }); return err },\n", durableDispatchOptionsFunction(execution), execution.Address)
	} else {
		handler, _ := operation.Spec["handler"].(map[string]any)
		method := stringValue(handler["method"])
		fmt.Fprintf(b, "if service == nil { return nil, fmt.Errorf(\"service is not initialized\") }; outcome, err := service.%s(callCtx, copied); if err != nil { if outcome != nil { return nil, fmt.Errorf(\"handler returned outcome and error\") }; return nil, err }; if outcome == nil { return nil, fmt.Errorf(\"handler returned nil outcome without error\") }; cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, err }; if err := sceneryruntime.PublishContractOperationOutcome(callCtx, %q, cloned); err != nil { return nil, err }; return cloned, nil }); return err },\n", method, operationName, operation.Address)
	}
	b.WriteString("\t\t\t}); err != nil { return err }\n")
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
	for _, candidate := range []string{"cron", "every", "at", "calendar"} {
		if trigger[candidate] != nil {
			triggerKind, triggerValue = candidate, refOrString(trigger[candidate])
			break
		}
	}
	if triggerKind == "every" {
		triggerValue = time.Duration(durationNanos(trigger["every"])).String()
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
	fmt.Fprintf(b, "\t\t\t\tAddress: %q, Name: %q, TriggerKind: %q, TriggerValue: %q, Timezone: %q, Overlap: %q, CatchupMaximumAge: %d, Identity: %s, AuthorizationAddress: %q, PipelineAddress: %q, Policy: %s,\n", schedule.Address, schedule.Name, triggerKind, triggerValue, stringValue(trigger["timezone"]), stringValue(schedule.Spec["overlap"]), durationNanos(catchup["maximum_age"]), identity, refOrString(invoke["authorization"]), refOrString(invoke["pipeline"]), policy)
	fmt.Fprintf(b, "\t\t\t\tInvoke: func(ctx context.Context) error { var input contract.%sInput; if err := scenery.UnmarshalContractValue([]byte(%q), &input, %q); err != nil { return fmt.Errorf(\"decode schedule input: %%w\", err) }; copied, err := contract.Clone%sInput(input); if err != nil { return err }; _, err = sceneryruntime.InvokeContractPolicy(ctx, %s, copied, func(callCtx context.Context) (any, error) { ", operationName, string(inputJSON), goWireTypeExpression(operation.Spec["input"]), operationName, policy)
	if stringValue(execution.Spec["mode"]) == "durable" {
		fmt.Fprintf(b, "options, err := %s(copied); if err != nil { return nil, err }; return sceneryruntime.DispatchContractDurableExecutionWithOptions(callCtx, %q, copied, options) }); return err },\n", durableDispatchOptionsFunction(execution), execution.Address)
	} else {
		handler, _ := operation.Spec["handler"].(map[string]any)
		fmt.Fprintf(b, "if service == nil { return nil, fmt.Errorf(\"service is not initialized\") }; outcome, err := service.%s(callCtx, copied); if err != nil { if outcome != nil { return nil, fmt.Errorf(\"handler returned outcome and error\") }; return nil, err }; if outcome == nil { return nil, fmt.Errorf(\"handler returned nil outcome without error\") }; cloned, err := contract.Clone%sOutcome(outcome); if err != nil { return nil, err }; if err := sceneryruntime.PublishContractOperationOutcome(callCtx, %q, cloned); err != nil { return nil, err }; return cloned, nil }); return err },\n", stringValue(handler["method"]), operationName, operation.Address)
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
		if !ok || identity.Kind != "scenery.workload-identity/v1" {
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

func renderEventEmissionRegistration(b *strings.Builder, resources map[string]Resource, operations []Resource, emission Resource) error {
	from, _ := emission.Spec["from"].(map[string]any)
	operationAddress := resolveResourceRef(emission, refString(from["operation"]), "operation")
	var operation *Resource
	for index := range operations {
		if operations[index].Address == operationAddress {
			operation = &operations[index]
			break
		}
	}
	if operation == nil {
		return fmt.Errorf("event emission %s references an unknown operation", emission.Address)
	}
	contractAddress := resolveResourceRef(emission, refString(emission.Spec["contract"]), "event")
	contract, ok := resources[contractAddress]
	if !ok {
		return fmt.Errorf("event emission %s references an unknown event", emission.Address)
	}
	version, ok := integerValue(contract.Spec["version"])
	if !ok || version <= 0 {
		return fmt.Errorf("event emission %s contract version is invalid", emission.Address)
	}
	when := strings.Split(refOrString(from["when"]), ".")
	payload := strings.Split(refOrString(from["payload"]), ".")
	if len(when) != 2 || len(payload) < 2 || payload[1] != when[1] {
		return fmt.Errorf("event emission %s outcome selection is invalid", emission.Address)
	}
	var variant map[string]any
	for _, candidate := range namedChildren(operation.Spec, "result") {
		if stringValue(candidate["name"]) == when[1] {
			variant = candidate
			break
		}
	}
	if variant == nil {
		return fmt.Errorf("event emission %s references unknown result %s", emission.Address, when[1])
	}
	payloadType := variant["type"]
	payloadGo := "typed.Value"
	if len(payload) > 2 {
		payloadType = recordFieldType(resources, operation.Module, variant["type"], payload[2:])
		for _, field := range payload[2:] {
			payloadGo += "." + goName(field)
		}
	}
	if payloadType == nil {
		return fmt.Errorf("event emission %s payload path is invalid", emission.Address)
	}
	orderingKey, orderingKeyType, err := eventEmissionKeyExpression(resources, *operation, when[1], emission.Spec["ordering_key"])
	if err != nil {
		return fmt.Errorf("event emission %s ordering_key: %w", emission.Address, err)
	}
	deduplicationKey, deduplicationKeyType, err := eventEmissionKeyExpression(resources, *operation, when[1], emission.Spec["deduplication_key"])
	if err != nil {
		return fmt.Errorf("event emission %s deduplication_key: %w", emission.Address, err)
	}
	retry, _ := emission.Spec["broker_retry"].(map[string]any)
	attempts, _ := integerValue(retry["attempts"])
	if attempts <= 0 {
		attempts = 1
	}
	backoff := defaultString(stringValue(retry["backoff"]), "none")
	fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractEventEmission(sceneryruntime.ContractEventEmissionRegistration{\n")
	fmt.Fprintf(b, "\t\t\t\tAddress: %q, OperationAddress: %q, BusAddress: %q, Channel: %q, ContractAddress: %q, ContractVersion: %d, Guarantee: %q, Attempts: %d, Backoff: %q, DeadLetterChannel: %q,\n", emission.Address, operation.Address, resolveResourceRef(emission, refString(emission.Spec["bus"]), "event_bus"), stringValue(emission.Spec["channel"]), contract.Address, version, stringValue(emission.Spec["guarantee"]), attempts, backoff, stringValue(emission.Spec["dead_letter_channel"]))
	fmt.Fprintf(b, "\t\t\t\tEncode: func(outcome any) ([]byte, bool, error) { typed, ok := outcome.(contract.%s%s); if !ok { return nil, false, nil }; payload, err := scenery.MarshalContractValue(%s, %q); return payload, true, err },\n", goName(operation.Name), goName(when[1]), payloadGo, goWireTypeExpression(payloadType))
	if orderingKey != "" {
		fmt.Fprintf(b, "\t\t\t\tOrderingKey: func(outcome any) (string, error) { typed, ok := outcome.(contract.%s%s); if !ok { return \"\", fmt.Errorf(\"event ordering key outcome has type %%T\", outcome) }; component, err := scenery.EncodeContractKeyComponent(%s, %q); if err != nil { return \"\", err }; return scenery.EncodeContractCompositeKey(component) },\n", goName(operation.Name), goName(when[1]), orderingKey, orderingKeyType)
	}
	if deduplicationKey != "" {
		fmt.Fprintf(b, "\t\t\t\tDeduplicationKey: func(outcome any) (string, error) { typed, ok := outcome.(contract.%s%s); if !ok { return \"\", fmt.Errorf(\"event deduplication key outcome has type %%T\", outcome) }; component, err := scenery.EncodeContractKeyComponent(%s, %q); if err != nil { return \"\", err }; return scenery.EncodeContractCompositeKey(component) },\n", goName(operation.Name), goName(when[1]), deduplicationKey, deduplicationKeyType)
	}
	b.WriteString("\t\t\t}); err != nil { return err }\n")
	return nil
}

func eventEmissionKeyExpression(resources map[string]Resource, operation Resource, variantName string, value any) (string, string, error) {
	if value == nil {
		return "", "", nil
	}
	reference := refOrString(value)
	parts := strings.Split(reference, ".")
	if len(parts) < 3 || parts[0] != "result" || parts[1] != variantName {
		return "", "", fmt.Errorf("must reference a field of result.%s", variantName)
	}
	var resultType any
	for _, variant := range namedChildren(operation.Spec, "result") {
		if stringValue(variant["name"]) == variantName {
			resultType = variant["type"]
			break
		}
	}
	keyType := recordFieldType(resources, operation.Module, resultType, parts[2:])
	if keyType == nil {
		return "", "", fmt.Errorf("references an unknown result field")
	}
	typeExpression := goWireTypeExpression(keyType)
	if !eventKeyTypeSupported(typeExpression) {
		return "", "", fmt.Errorf("must reference a supported non-null scalar key, got %s", typeExpression)
	}
	expression := "typed.Value"
	for _, field := range parts[2:] {
		expression += "." + goName(field)
	}
	return expression, typeExpression, nil
}

func eventKeyTypeSupported(typeExpression string) bool {
	if strings.HasPrefix(typeExpression, "enum.") {
		return true
	}
	switch typeExpression {
	case "bool", "int", "int32", "uint32", "int64", "uint64", "decimal", "float32", "float64", "string", "uuid", "date", "datetime", "duration", "size", "url", "relative_path":
		return true
	default:
		return false
	}
}
