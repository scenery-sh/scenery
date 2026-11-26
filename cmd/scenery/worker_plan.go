package main

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/workerplan"
)

type workerPlanProjection struct {
	resources map[string]graph.Resource
	services  map[string]workerplan.WorkerServicePlan
}

func projectWorkerServices(manifest *graph.Manifest) ([]workerplan.WorkerServicePlan, error) {
	projection := workerPlanProjection{
		resources: cp.ResourcesByAddress(manifest), services: make(map[string]workerplan.WorkerServicePlan),
	}
	operationOwners := make(map[string]string)
	moduleOwners := make(map[string]string)
	runtimeServices := make(map[string]bool)
	for _, service := range compiler.RuntimeServices(manifest.Resources) {
		runtimeServices[service.Address] = true
	}
	for _, resource := range manifest.Resources {
		if resource.Kind != "scenery.service" || !runtimeServices[resource.Address] && !compiler.IsExternalWorkerRuntime(cp.StringValue(resource.Spec["runtime"])) {
			continue
		}
		if other, exists := moduleOwners[resource.Module]; exists {
			return nil, fmt.Errorf("worker module %q declares services %s and %s; each service needs its own module", resource.Module, other, resource.Address)
		}
		moduleOwners[resource.Module] = resource.Address
		digest := sha256.Sum256([]byte(resource.Address))
		service := workerplan.WorkerServicePlan{
			Address: resource.Address, Name: resource.Name, Module: resource.Module, Process: fmt.Sprintf("worker-%x", digest[:8]),
			Operations: make(map[string]workerplan.WorkerOperationPlan), Clients: make(map[string]workerplan.WorkerClientPlan),
			Durable: make(map[string]workerplan.WorkerDurablePlan), Stores: make(map[string]string),
		}
		dependencies, err := compiler.ServiceGoDependencies(projection.resources, resource)
		if err != nil {
			return nil, err
		}
		for _, dependency := range dependencies {
			if dependency.Resolver == "object" {
				service.Stores[dependency.Name] = dependency.RuntimeName
			}
		}
		for _, client := range cp.NamedChildren(resource.Spec, "client") {
			binding := workerReference(resource, client["binding"], "binding")
			service.Clients[cp.StringValue(client["name"])] = workerplan.WorkerClientPlan{
				Binding: binding, Enqueue: cp.StringValue(projection.resources[binding].Spec["delivery"]) == "enqueue",
			}
		}
		for _, publisher := range cp.NamedChildren(resource.Spec, "publisher") {
			if service.Publishers == nil {
				service.Publishers = make(map[string]string)
			}
			service.Publishers[cp.StringValue(publisher["name"])] = workerReference(resource, publisher["event"], "event")
		}
		service.Runtime = firstNonEmpty(cp.StringValue(resource.Spec["runtime"]), "go")
		projection.services[resource.Address] = service
	}
	for _, operation := range manifest.Resources {
		if operation.Kind != "scenery.operation" {
			continue
		}
		owner := workerReference(operation, operation.Spec["service"], "service")
		service, ok := projection.services[owner]
		if !ok {
			continue
		}
		operationOwners[operation.Address] = owner
		outcomes := make(map[string]string)
		for _, kind := range []string{"result", "error"} {
			for _, variant := range cp.NamedChildren(operation.Spec, kind) {
				outcomes[kind+"."+cp.StringValue(variant["name"])] = workerTypeExpression(operation.Module, variant["type"])
			}
		}
		service.Operations[operation.Address] = workerplan.WorkerOperationPlan{
			Input: workerTypeExpression(operation.Module, operation.Spec["input"]), Outcomes: outcomes,
		}
	}
	for _, execution := range manifest.Resources {
		if execution.Kind != "scenery.execution" || cp.StringValue(execution.Spec["mode"]) != "durable" {
			continue
		}
		operation := workerReference(execution, execution.Spec["operation"], "operation")
		service, ok := projection.services[operationOwners[operation]]
		if !ok {
			continue
		}
		plan, err := projection.durable(service, execution, projection.resources[operation])
		if err != nil {
			return nil, fmt.Errorf("worker execution %s: %w", execution.Address, err)
		}
		service.Durable[execution.Address] = plan
	}
	for _, schedule := range manifest.Resources {
		if schedule.Kind != "scenery.schedule" {
			continue
		}
		invoke, _ := schedule.Spec["invoke"].(map[string]any)
		operation := workerReference(schedule, invoke["operation"], "operation")
		service, ok := projection.services[operationOwners[operation]]
		if !ok {
			continue
		}
		execution := workerReference(schedule, invoke["execution"], "execution")
		input, err := compiler.StaticOperationInputJSON(projection.resources, projection.resources[operation], invoke["input"])
		if err != nil {
			return nil, fmt.Errorf("schedule %s input: %w", schedule.Address, err)
		}
		trigger, _ := schedule.Spec["trigger"].(map[string]any)
		catchup, _ := schedule.Spec["catchup"].(map[string]any)
		policySource := schedule
		policySource.Spec = invoke
		service.Schedules = append(service.Schedules, workerplan.WorkerSchedulePlan{
			Address: schedule.Address, Operation: operation, Execution: execution, Input: input,
			Authorization: workerRefText(invoke["authorization"]), Pipeline: workerRefText(invoke["pipeline"]),
			Policy:  projection.policy(service, policySource, projection.resources[operation]),
			Timeout: time.Duration(workerDuration(projection.resources[execution].Spec["timeout"])),
			Every:   time.Duration(workerDuration(trigger["every"])), Overlap: cp.StringValue(schedule.Spec["overlap"]),
			CatchupMaximumAge: time.Duration(workerDuration(catchup["maximum_age"])),
		})
		projection.services[service.Address] = service
	}
	for _, binding := range manifest.Resources {
		if binding.Kind != "scenery.binding" {
			continue
		}
		operationAddress := workerReference(binding, binding.Spec["operation"], "operation")
		service, ok := projection.services[operationOwners[operationAddress]]
		if !ok {
			continue
		}
		plan, err := projection.binding(service, binding, projection.resources[operationAddress])
		if err != nil {
			return nil, fmt.Errorf("worker binding %s: %w", binding.Address, err)
		}
		service.Bindings = append(service.Bindings, plan)
		projection.services[service.Address] = service
	}
	addresses := make([]string, 0, len(projection.services))
	for address := range maps.Keys(projection.services) {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	services := make([]workerplan.WorkerServicePlan, 0, len(addresses))
	for _, address := range addresses {
		services = append(services, projection.services[address])
	}
	return services, nil
}

func workerReference(owner graph.Resource, value any, kind string) string {
	return cp.ResolveResourceRef(owner, workerRefText(value), kind)
}

func workerRefText(value any) string {
	if reference := cp.RefString(value); reference != "" {
		return reference
	}
	return cp.StringValue(value)
}

func workerTypeExpression(module string, value any) string {
	expression := cp.TypeExpression(value)
	return qualifyWorkerType(module, expression)
}

func qualifyWorkerType(module, expression string) string {
	var out strings.Builder
	for index := 0; index < len(expression); {
		start := index
		for index < len(expression) && !strings.ContainsRune("(), \t", rune(expression[index])) {
			index++
		}
		word := expression[start:index]
		kind, name, local := strings.Cut(word, ".")
		if local && (kind == "record" || kind == "enum" || kind == "union") {
			out.WriteString(graph.ResourceAddress(module, kind, name))
		} else {
			out.WriteString(word)
		}
		if index < len(expression) {
			out.WriteByte(expression[index])
			index++
		}
	}
	return out.String()
}

func (p workerPlanProjection) binding(service workerplan.WorkerServicePlan, binding, operation graph.Resource) (workerplan.WorkerBindingPlan, error) {
	result := workerplan.WorkerBindingPlan{
		Address: binding.Address, Operation: operation.Address, Policy: p.policy(service, binding, operation),
	}
	delivery := cp.StringValue(binding.Spec["delivery"])
	result.Delivery = delivery
	result.Execution = workerReference(binding, binding.Spec["execution"], "execution")
	result.Policy.TotalInvocationTimeoutNanos = workerDuration(p.resources[result.Execution].Spec["timeout"])
	if delivery != "call" && delivery != "stream" && delivery != "stream_out" && delivery != "enqueue" && delivery != "wait" {
		return result, fmt.Errorf("worker binding delivery %q has no Host plan", delivery)
	}
	protocol := cp.StringValue(binding.Spec["protocol"])
	isDurable := cp.StringValue(p.resources[result.Execution].Spec["mode"]) == "durable"
	if (delivery == "enqueue" || delivery == "wait" || (protocol == "mcp" && isDurable)) && service.Durable[result.Execution].Operation == "" {
		return result, fmt.Errorf("durable execution %s has no Host plan", result.Execution)
	}
	switch protocol {
	case "http":
		httpPlan, err := p.httpBinding(binding, operation, &result.Policy)
		if err != nil {
			return result, err
		}
		result.HTTP = &httpPlan
	case "mcp":
		result.MCP = true
	case "internal":
		internal, _ := binding.Spec["internal"].(map[string]any)
		result.Visibility = cp.StringValue(internal["visibility"])
	default:
		return result, fmt.Errorf("worker binding protocol has no Host plan")
	}
	return result, nil
}

func (p workerPlanProjection) policy(service workerplan.WorkerServicePlan, binding, operation graph.Resource) workerplan.ContractHTTPPolicy {
	policy := workerplan.ContractHTTPPolicy{ServiceName: service.Name, OperationAddress: operation.Address, BindingAddress: binding.Address}
	auth := p.resources[workerReference(binding, binding.Spec["authorization"], "authorization")]
	policy.AuthorizationStrategy = cp.StringValue(auth.Spec["strategy"])
	if policy.AuthorizationStrategy == "" {
		policy.AuthorizationStrategy = cp.LastRef(workerRefText(binding.Spec["authorization"]))
	}
	// Application and scheduled authorization only describe callers the Host
	// already admitted (internal and MCP bindings, schedules), as in the native
	// generator. An HTTP binding keeps the strategy, which the Host refuses.
	if (policy.AuthorizationStrategy == "application" || policy.AuthorizationStrategy == "scheduled") && cp.StringValue(binding.Spec["protocol"]) != "http" {
		policy.AuthorizationStrategy = "public"
	}
	for _, rule := range cp.OrderedChildren(auth.Spec, "rule") {
		effect, predicate := "allow", rule["allow"]
		if deny, ok := rule["deny"]; ok {
			effect, predicate = "deny", deny
		}
		expression := "false"
		switch value := predicate.(type) {
		case bool:
			expression = strconv.FormatBool(value)
		case map[string]any:
			expression = cp.StringValue(value["$expression"])
		}
		policy.AuthorizationRules = append(policy.AuthorizationRules, workerplan.ContractAuthorizationRule{
			Name: cp.StringValue(rule["name"]), Effect: effect, Expression: expression,
		})
	}
	policy.AuthorizationRuleCount = len(policy.AuthorizationRules)
	pipeline := p.resources[workerReference(binding, binding.Spec["pipeline"], "pipeline")]
	for _, step := range cp.OrderedChildren(pipeline.Spec, "step") {
		policy.PipelineSteps = append(policy.PipelineSteps, workerRefText(step["use"]))
	}
	return policy
}

func workerDuration(value any) int64 {
	if scalar, ok := value.(map[string]any); ok {
		nanoseconds, _ := strconv.ParseInt(cp.StringValue(scalar["nanoseconds"]), 10, 64)
		return nanoseconds
	}
	duration, err := contract.ParseDuration(cp.StringValue(value))
	if err != nil || !duration.Nanoseconds().IsInt64() {
		return 0
	}
	return duration.Nanoseconds().Int64()
}
