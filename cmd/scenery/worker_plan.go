package main

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/graph"
	workerruntime "scenery.sh/runtime"
)

type workerPlanProjection struct {
	resources map[string]graph.Resource
	services  map[string]workerruntime.WorkerServicePlan
}

func projectWorkerServices(manifest *graph.Manifest) ([]workerruntime.WorkerServicePlan, error) {
	projection := workerPlanProjection{
		resources: cp.ResourcesByAddress(manifest), services: make(map[string]workerruntime.WorkerServicePlan),
	}
	operationOwners := make(map[string]string)
	for _, resource := range manifest.Resources {
		if resource.Kind != "scenery.service" || cp.StringValue(resource.Spec["runtime"]) != "rust" {
			continue
		}
		digest := sha256.Sum256([]byte(resource.Address))
		service := workerruntime.WorkerServicePlan{
			Address: resource.Address, Name: resource.Name, Module: resource.Module, Process: fmt.Sprintf("worker-%x", digest[:8]),
			Operations: make(map[string]workerruntime.WorkerOperationPlan), Clients: make(map[string]workerruntime.WorkerClientPlan),
			Durable: make(map[string]workerruntime.WorkerDurablePlan),
		}
		for _, client := range cp.NamedChildren(resource.Spec, "client") {
			binding := workerReference(resource, client["binding"], "binding")
			service.Clients[cp.StringValue(client["name"])] = workerruntime.WorkerClientPlan{
				Binding: binding, Enqueue: cp.StringValue(projection.resources[binding].Spec["delivery"]) == "enqueue",
			}
		}
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
		service.Operations[operation.Address] = workerruntime.WorkerOperationPlan{
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
	services := make([]workerruntime.WorkerServicePlan, 0, len(addresses))
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

func (p workerPlanProjection) binding(service workerruntime.WorkerServicePlan, binding, operation graph.Resource) (workerruntime.WorkerBindingPlan, error) {
	result := workerruntime.WorkerBindingPlan{
		Address: binding.Address, Operation: operation.Address, Policy: p.policy(service, binding, operation),
	}
	delivery := cp.StringValue(binding.Spec["delivery"])
	result.Delivery = delivery
	result.Execution = workerReference(binding, binding.Spec["execution"], "execution")
	result.Policy.TotalInvocationTimeoutNanos = workerDuration(p.resources[result.Execution].Spec["timeout"])
	if delivery != "call" && delivery != "stream" && delivery != "enqueue" && delivery != "wait" {
		return result, fmt.Errorf("worker binding delivery %q has no Host plan", delivery)
	}
	if (delivery == "enqueue" || delivery == "wait") && service.Durable[result.Execution].Operation == "" {
		return result, fmt.Errorf("durable execution %s has no Host plan", result.Execution)
	}
	switch cp.StringValue(binding.Spec["protocol"]) {
	case "http":
		httpPlan, err := p.httpBinding(binding, operation, &result.Policy)
		if err != nil {
			return result, err
		}
		result.HTTP = &httpPlan
	case "internal":
		internal, _ := binding.Spec["internal"].(map[string]any)
		result.Visibility = cp.StringValue(internal["visibility"])
	default:
		return result, fmt.Errorf("worker binding protocol has no Host plan")
	}
	return result, nil
}

func (p workerPlanProjection) policy(service workerruntime.WorkerServicePlan, binding, operation graph.Resource) workerruntime.ContractHTTPPolicy {
	policy := workerruntime.ContractHTTPPolicy{ServiceName: service.Name, OperationAddress: operation.Address, BindingAddress: binding.Address}
	auth := p.resources[workerReference(binding, binding.Spec["authorization"], "authorization")]
	policy.AuthorizationStrategy = cp.StringValue(auth.Spec["strategy"])
	if policy.AuthorizationStrategy == "" {
		policy.AuthorizationStrategy = cp.LastRef(workerRefText(binding.Spec["authorization"]))
	}
	if policy.AuthorizationStrategy == "application" || policy.AuthorizationStrategy == "scheduled" {
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
		policy.AuthorizationRules = append(policy.AuthorizationRules, workerruntime.ContractAuthorizationRule{
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
