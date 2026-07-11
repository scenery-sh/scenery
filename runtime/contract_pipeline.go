package runtime

import (
	"context"
	"fmt"
)

type contractPipelineInvoke func(context.Context) (any, error)

func InvokeContractPolicy(ctx context.Context, policy *ContractHTTPPolicy, input any, invoke func(context.Context) (any, error)) (any, error) {
	if err := validateContractHTTPPolicy(policy); err != nil {
		return nil, fmt.Errorf("contract invocation policy: %w", err)
	}
	if err := authorizeContractInvocation(policy, input); err != nil {
		return nil, err
	}
	return invokeContractPipeline(ctx, policy, invoke)
}

func invokeContractPipeline(ctx context.Context, policy *ContractHTTPPolicy, invoke contractPipelineInvoke) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if invoke == nil {
		return nil, fmt.Errorf("contract pipeline has no invocation")
	}
	steps := []string(nil)
	if policy != nil {
		steps = policy.PipelineSteps
	}
	current := invoke
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		next := current
		switch step {
		case "std.middleware.request_id", "std.middleware.trace":
			current = func(callCtx context.Context) (any, error) { return next(callCtx) }
		case "std.middleware.recover":
			current = func(callCtx context.Context) (value any, err error) {
				defer func() {
					if recovered := recover(); recovered != nil {
						value = nil
						err = ContractSystemError(fmt.Errorf("panic in contract invocation: %v", recovered))
					}
				}()
				return next(callCtx)
			}
		default:
			return nil, fmt.Errorf("unsupported contract pipeline step %q", step)
		}
	}
	return current(ctx)
}
