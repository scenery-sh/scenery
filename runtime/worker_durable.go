package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/workerabi"
)

func (workers *workerHost) registerDurable(service WorkerServicePlan, execution WorkerDurablePlan) error {
	operation, ok := service.Operations[execution.Operation]
	if !ok {
		return fmt.Errorf("worker durable execution has no declared operation")
	}
	registration := execution.Registration
	registration.workerRuntime, registration.workerModule = service.Runtime, service.Module
	registration.Handler = func(ctx context.Context, raw []byte) ([]byte, error) {
		input, err := workers.codec(operation.Input, raw)
		if err != nil {
			return nil, err
		}
		result, err := workers.invoke(ctx, service, operation, execution.Operation, "", "durable", input)
		if err != nil {
			return nil, err
		}
		if result.Stream != nil {
			return nil, closeWorkerResult(result, fmt.Errorf("durable worker returned a stream"))
		}
		return result.Outcome, nil
	}
	return RegisterContractDurableExecution(registration)
}

func (workers *workerHost) dispatchDurable(ctx context.Context, plan WorkerDurablePlan, delivery string, input json.RawMessage) (workerabi.Result, error) {
	options, err := workerDurableOptions(plan, input)
	if err != nil {
		return workerabi.Result{}, ContractSystemError(err)
	}
	address := plan.Registration.Address
	if delivery == "wait" {
		output, err := DispatchAndWaitContractDurableExecutionWithOptions(ctx, address, input, options)
		return workerabi.Result{Outcome: output}, err
	}
	receipt, err := DispatchContractDurableExecutionWithOptions(ctx, address, input, options)
	if err != nil {
		return workerabi.Result{}, contractDurableFailure("dispatch.rejected", err)
	}
	if len(plan.Registration.ExternalName) == 0 {
		return workerabi.Result{}, ContractSystemError(fmt.Errorf("worker durable execution has no external name"))
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return workerabi.Result{}, ContractSystemError(err)
	}
	output, err := contract.EncodeManifestOutcome("result", "dispatch.enqueued", data)
	return workerabi.Result{Outcome: output}, err
}

func workerDurableOptions(plan WorkerDurablePlan, input json.RawMessage) (ContractDurableDispatchOptions, error) {
	key, err := workerDispatchKey(input, plan.Dedupe)
	if err != nil {
		return ContractDurableDispatchOptions{}, err
	}
	concurrency, err := workerDispatchKey(input, plan.Concurrency)
	return ContractDurableDispatchOptions{DedupeKey: key, ConcurrencyKey: concurrency}, err
}

func workerDispatchKey(input json.RawMessage, fields []WorkerKeyField) (string, error) {
	if len(fields) == 0 {
		return "", nil
	}
	object, err := contract.DecodeJSONObject(input)
	if err != nil {
		return "", err
	}
	components := make([][]byte, 0, len(fields))
	for _, field := range fields {
		component, err := contract.EncodeWireKeyComponent(object[field.Name], field.Type)
		if err != nil {
			return "", err
		}
		components = append(components, component)
	}
	return contract.EncodeContractCompositeKey(components...)
}
