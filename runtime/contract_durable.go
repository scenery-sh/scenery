package runtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/runtimeapi"
)

type contractDurableFailureError struct {
	outcome string
	cause   error
}

func (failure *contractDurableFailureError) Error() string {
	if failure == nil || failure.outcome == "" {
		return "contract durable execution failed"
	}
	return failure.outcome
}

func (failure *contractDurableFailureError) Unwrap() error { return failure.cause }

func contractDurableFailure(outcome string, cause error) error {
	return &contractDurableFailureError{outcome: outcome, cause: cause}
}

func ContractDurableFailureOutcome(err error) string {
	var failure *contractDurableFailureError
	if errors.As(err, &failure) && failure.outcome != "" {
		return failure.outcome
	}
	return "system.internal"
}

// ContractDurableRegistration is the exact runtime projection of one
// scenery.execution/v1 durable execution.
type ContractDurableRegistration struct {
	Address                string
	ExternalName           string
	EngineAddress          string
	Service                string
	Revision               uint32
	DefaultTimeout         time.Duration
	DefaultLease           time.Duration
	MaxAttempts            int
	RetryInitial           time.Duration
	RetryMax               time.Duration
	RetryBackoff           float64
	RetryJitter            float64
	SuccessRetention       time.Duration
	FailureRetention       time.Duration
	MaxConcurrency         int
	DeduplicationRetention time.Duration
	DeduplicationConflict  string
	Handler                func(context.Context, []byte) ([]byte, error)
}

func RegisterContractDurableExecution(registration ContractDurableRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.ExternalName = strings.TrimSpace(registration.ExternalName)
	registration.EngineAddress = strings.TrimSpace(registration.EngineAddress)
	registration.Service = strings.TrimSpace(registration.Service)
	if registration.Address == "" || registration.EngineAddress == "" || registration.Service == "" {
		return fmt.Errorf("runtime: contract durable execution requires address, engine address, and service")
	}
	if registration.Revision == 0 {
		return fmt.Errorf("runtime: contract durable execution %s requires a positive revision", registration.Address)
	}
	if registration.Handler == nil {
		return fmt.Errorf("runtime: contract durable execution %s requires a handler", registration.Address)
	}
	if registration.ExternalName == "" {
		registration.ExternalName = registration.Address
	}
	if registration.DefaultTimeout <= 0 || registration.DefaultLease <= 0 || registration.MaxAttempts <= 0 {
		return fmt.Errorf("runtime: contract durable execution %s has invalid timeout, lease, or attempts", registration.Address)
	}
	if registration.DefaultLease > registration.DefaultTimeout {
		return fmt.Errorf("runtime: contract durable execution %s lease exceeds timeout", registration.Address)
	}
	if registration.RetryInitial < 0 || registration.RetryMax < 0 || registration.RetryBackoff < 0 || registration.RetryJitter < 0 {
		return fmt.Errorf("runtime: contract durable execution %s has invalid retry policy", registration.Address)
	}
	if registration.SuccessRetention <= 0 || registration.FailureRetention <= 0 {
		return fmt.Errorf("runtime: contract durable execution %s has invalid retention policy", registration.Address)
	}
	if registration.MaxConcurrency < 0 {
		return fmt.Errorf("runtime: contract durable execution %s has invalid concurrency limit", registration.Address)
	}
	if registration.DeduplicationRetention < 0 || registration.DeduplicationRetention > 0 && registration.DeduplicationConflict != "return_existing" {
		return fmt.Errorf("runtime: contract durable execution %s has invalid deduplication policy", registration.Address)
	}

	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractDurableExecutions == nil {
		global.contractDurableExecutions = make(map[string]ContractDurableRegistration)
	}
	if _, exists := global.contractDurableExecutions[registration.Address]; exists {
		return fmt.Errorf("runtime: duplicate contract durable execution %s", registration.Address)
	}
	if global.durableTasks == nil {
		global.durableTasks = make(map[string]*DurableTask)
	}
	key := registration.Service + ":" + registration.ExternalName
	if _, exists := global.durableTasks[key]; exists {
		return fmt.Errorf("runtime: duplicate durable task registration for %s", key)
	}
	global.durableTasks[key] = &DurableTask{
		Name: registration.ExternalName, Service: registration.Service, Version: int(registration.Revision),
		HandlerRef: registration.Address, Handler: registration.Handler,
		DefaultTimeout: registration.DefaultTimeout, DefaultLease: registration.DefaultLease,
		MaxAttempts: registration.MaxAttempts, RetryInitial: registration.RetryInitial,
		RetryMax: registration.RetryMax, RetryBackoff: registration.RetryBackoff,
		RetryJitter: registration.RetryJitter, SuccessRetention: registration.SuccessRetention,
		FailureRetention: registration.FailureRetention, MaxConcurrency: registration.MaxConcurrency,
		DeduplicationRetention: registration.DeduplicationRetention, DeduplicationConflict: registration.DeduplicationConflict,
	}
	global.contractDurableExecutions[registration.Address] = registration
	return nil
}

func DispatchContractDurableExecution(ctx context.Context, address string, input any, dedupeKey string) (runtimeapi.ExecutionReceipt, error) {
	return DispatchContractDurableExecutionWithOptions(ctx, address, input, ContractDurableDispatchOptions{DedupeKey: dedupeKey})
}

type ContractDurableDispatchOptions struct {
	DedupeKey      string
	ConcurrencyKey string
}

func DispatchContractDurableExecutionWithOptions(ctx context.Context, address string, input any, options ContractDurableDispatchOptions) (runtimeapi.ExecutionReceipt, error) {
	address = strings.TrimSpace(address)
	global.mu.RLock()
	registration, ok := global.contractDurableExecutions[address]
	global.mu.RUnlock()
	if !ok {
		return runtimeapi.ExecutionReceipt{}, fmt.Errorf("runtime: contract durable execution %s is not registered", address)
	}
	run, err := StartDurableTask(ctx, DurableStartRequest{
		Service: registration.Service, TaskName: registration.ExternalName,
		DedupeKey: strings.TrimSpace(options.DedupeKey), ConcurrencyKey: strings.TrimSpace(options.ConcurrencyKey), Input: input,
	})
	if err != nil {
		return runtimeapi.ExecutionReceipt{}, err
	}
	return contractExecutionReceipt(registration, run.ID), nil
}

func DispatchAndWaitContractDurableExecution(ctx context.Context, address string, input any, dedupeKey string) ([]byte, error) {
	return DispatchAndWaitContractDurableExecutionWithOptions(ctx, address, input, ContractDurableDispatchOptions{DedupeKey: dedupeKey})
}

func DispatchAndWaitContractDurableExecutionWithOptions(ctx context.Context, address string, input any, options ContractDurableDispatchOptions) ([]byte, error) {
	receipt, err := DispatchContractDurableExecutionWithOptions(ctx, address, input, options)
	if err != nil {
		return nil, contractDurableFailure("dispatch.rejected", err)
	}
	global.mu.RLock()
	registration, ok := global.contractDurableExecutions[strings.TrimSpace(address)]
	global.mu.RUnlock()
	if !ok {
		return nil, contractDurableFailure("dispatch.rejected", fmt.Errorf("runtime: contract durable execution %s is not registered", address))
	}
	result, err := WaitDurableTask(ctx, DurableRun{ID: receipt.ExecutionID, Service: registration.Service, TaskName: registration.ExternalName})
	if err == nil {
		return result, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, contractDurableFailure("dispatch.wait_timeout", err)
	}
	if errors.Is(err, context.Canceled) {
		return nil, err
	}
	return nil, contractDurableFailure("system.internal", err)
}

func contractExecutionReceipt(registration ContractDurableRegistration, executionID string) runtimeapi.ExecutionReceipt {
	return runtimeapi.ExecutionReceipt{
		DurableIdentity:  registration.EngineAddress + "::" + registration.Address,
		ExecutionID:      executionID,
		AcceptedRevision: strconv.FormatUint(uint64(registration.Revision), 10),
	}
}
