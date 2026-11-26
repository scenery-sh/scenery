package main

import (
	"cmp"
	"fmt"
	"slices"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/workerplan"
)

func projectWorkerEvents(manifest *graph.Manifest, services []workerplan.WorkerServicePlan) ([]workerplan.WorkerEventPlan, error) {
	projection := workerPlanProjection{resources: cp.ResourcesByAddress(manifest)}
	owners := make(map[string]workerplan.WorkerServicePlan)
	for _, service := range services {
		for operation := range service.Operations {
			owners[operation] = service
		}
	}
	subscriptions := make(map[string][]workerplan.WorkerEventSubscriptionPlan)
	for _, subscription := range manifest.Resources {
		if subscription.Kind != "scenery.subscription" {
			continue
		}
		invoke, _ := subscription.Spec["invoke"].(map[string]any)
		operation := workerReference(subscription, invoke["operation"], "operation")
		execution := workerReference(subscription, invoke["execution"], "execution")
		service := owners[operation]
		if service.Durable[execution].Operation != operation || operation == "" {
			return nil, fmt.Errorf("subscription %s execution %s has no Host plan", subscription.Address, execution)
		}
		event := workerReference(subscription, subscription.Spec["event"], "event")
		policySource := subscription
		policySource.Spec = invoke
		subscriptions[event] = append(subscriptions[event], workerplan.WorkerEventSubscriptionPlan{
			Address: subscription.Address, Service: service.Address, Operation: operation, Execution: execution,
			Policy: projection.policy(service, policySource, projection.resources[operation]),
		})
	}
	var events []workerplan.WorkerEventPlan
	for _, event := range manifest.Resources {
		if event.Kind != "scenery.event" {
			continue
		}
		idempotency, _ := event.Spec["idempotency"].(map[string]any)
		keys, _ := idempotency["key"].([]any)
		payload := event
		payload.Spec = map[string]any{"input": event.Spec["payload"]}
		fields, err := projectWorkerKeys(cp.ResolveOperationInputShape(projection.resources, payload), keys, "payload")
		if err != nil {
			return nil, fmt.Errorf("event %s: %w", event.Address, err)
		}
		subs := subscriptions[event.Address]
		slices.SortFunc(subs, func(a, b workerplan.WorkerEventSubscriptionPlan) int { return cmp.Compare(a.Address, b.Address) })
		events = append(events, workerplan.WorkerEventPlan{
			Address: event.Address, ExternalName: firstNonEmpty(cp.StringValue(event.Spec["external_name"]), event.Address),
			PayloadType: workerTypeExpression(event.Module, event.Spec["payload"]), Dedupe: fields, Subscriptions: subs,
		})
	}
	slices.SortFunc(events, func(a, b workerplan.WorkerEventPlan) int { return cmp.Compare(a.Address, b.Address) })
	return events, nil
}
