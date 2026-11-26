package runtime

import (
	"context"
	"encoding/json"
	"fmt"
)

func workerScheduleIdentity() ContractWorkloadIdentity {
	return ContractWorkloadIdentity{
		Address: "std.workload_identity.scheduler", Issuer: "std.identity_issuer.runtime",
		PrincipalType: "std.type.workload_principal", ClaimsJSON: `{"workload":"scheduler"}`,
	}
}

func (workers *workerHost) registerSchedule(service WorkerServicePlan, schedule WorkerSchedulePlan) error {
	operation, exists := service.Operations[schedule.Operation]
	if !exists {
		return fmt.Errorf("worker schedule %s has no declared operation", schedule.Address)
	}
	wire, err := workers.codec(operation.Input, schedule.Input)
	if err != nil {
		return fmt.Errorf("worker schedule %s input: %w", schedule.Address, err)
	}
	input := json.RawMessage(wire)
	return RegisterContractSchedule(ContractScheduleRegistration{
		Address: schedule.Address, Name: devDurableName(schedule.Address),
		TriggerKind: "every", TriggerValue: schedule.Every.String(),
		Overlap: schedule.Overlap, CatchupMaximumAge: schedule.CatchupMaximumAge,
		Identity: workerScheduleIdentity(), AuthorizationAddress: schedule.Authorization,
		PipelineAddress: schedule.Pipeline, Policy: &schedule.Policy,
		Invoke: func(ctx context.Context) error {
			if schedule.Timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, schedule.Timeout)
				defer cancel()
			}
			_, err := InvokeContractPolicy(ctx, &schedule.Policy, input, func(ctx context.Context) (any, error) {
				if execution, durable := service.Durable[schedule.Execution]; durable {
					options, err := workerDurableOptions(execution, input)
					if err != nil {
						return nil, err
					}
					return DispatchContractDurableExecutionWithOptions(ctx, execution.Registration.Address, input, options)
				}
				return workers.invokeScheduledOperation(ctx, service, schedule, input)
			})
			return err
		},
	})
}

func (workers *workerHost) invokeScheduledOperation(ctx context.Context, service WorkerServicePlan, schedule WorkerSchedulePlan, input json.RawMessage) (json.RawMessage, error) {
	result, err := workers.invoke(ctx, service, service.Operations[schedule.Operation], schedule.Operation, "", "direct", input)
	if result.Stream != nil {
		return nil, closeWorkerResult(result, fmt.Errorf("scheduled operation returned a stream"))
	}
	return result.Outcome, err
}
