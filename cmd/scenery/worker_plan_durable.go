package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/workerplan"
)

func (p workerPlanProjection) durable(service workerplan.WorkerServicePlan, execution, operation graph.Resource) (workerplan.WorkerDurablePlan, error) {
	spec := execution.Spec
	retry, _ := spec["retry"].(map[string]any)
	retention, _ := spec["retention"].(map[string]any)
	concurrency, _ := spec["concurrency"].(map[string]any)
	dedupe, _ := spec["deduplication"].(map[string]any)
	factor, _ := strconv.ParseFloat(cp.StringValue(retry["factor"]), 64)
	jitter, _ := strconv.ParseFloat(cp.StringValue(retry["jitter"]), 64)
	plan := workerplan.WorkerDurablePlan{
		Operation: operation.Address,
		Registration: workerplan.ContractDurableRegistration{
			// Like the native runtime, an unnamed durable execution is its own task:
			// executions sharing one empty name would lease each other's jobs.
			Address: execution.Address, ExternalName: firstNonEmpty(cp.StringValue(spec["external_name"]), execution.Address), Service: service.Name,
			EngineAddress: workerReference(execution, spec["engine"], "execution_engine"), Revision: uint32(workerInt(spec["revision"])),
			DefaultTimeout: time.Duration(workerDuration(spec["timeout"])), DefaultLease: time.Duration(workerDuration(spec["lease"])),
			MaxAttempts: int(workerInt(spec["attempts"])), RetryInitial: time.Duration(workerDuration(retry["initial"])),
			RetryMax: time.Duration(workerDuration(retry["maximum"])), RetryBackoff: factor, RetryJitter: jitter,
			SuccessRetention: time.Duration(workerDuration(retention["success"])), FailureRetention: time.Duration(workerDuration(retention["failure"])),
			MaxConcurrency: int(workerInt(concurrency["limit"])), DeduplicationRetention: time.Duration(workerDuration(dedupe["retention"])),
			DeduplicationConflict: cp.StringValue(dedupe["conflict"]),
		},
	}
	shape := cp.ResolveOperationInputShape(p.resources, operation)
	var err error
	if idempotency, _ := operation.Spec["idempotency"].(map[string]any); idempotency["mode"] == "keyed" {
		keys, _ := idempotency["key"].([]any)
		plan.Dedupe, err = projectWorkerKeys(shape, keys, "input")
		if err != nil {
			return plan, err
		}
	}
	if concurrency != nil {
		plan.Concurrency, err = projectWorkerKeys(shape, []any{concurrency["key"]}, "input")
	}
	return plan, err
}

func projectWorkerKeys(shape cp.OperationInputShape, keys []any, root string) ([]workerplan.WorkerKeyField, error) {
	var fields []workerplan.WorkerKeyField
	for _, value := range keys {
		expression := cp.TypeExpression(value)
		name, ok := strings.CutPrefix(expression, root+".")
		field, exists := shape.Fields[name]
		if !ok || !exists {
			return nil, fmt.Errorf("key is not a declared %s field: %s", root, expression)
		}
		fields = append(fields, workerplan.WorkerKeyField{Name: field.WireName, Type: cp.TypeExpression(field.Type)})
	}
	return fields, nil
}
