package nativedurable

import (
	"context"
	"fmt"
	"scenery.sh/internal/runtimeapi"
	"sync/atomic"
	"time"
)

type StartRequest struct {
	Service        string
	TaskName       string
	ID             string
	DedupeKey      string
	ConcurrencyKey string
	Input          any
}

type ExecutionFailure struct {
	Service  string
	ID       string
	State    string
	TaskName string
}

func (failure *ExecutionFailure) Error() string {
	if failure == nil {
		return "runtime: durable execution failed"
	}
	return fmt.Sprintf("runtime: durable execution %s/%s reached %s", failure.Service, failure.ID, failure.State)
}

type DispatchOptions struct {
	DedupeKey      string
	ConcurrencyKey string
}

// ExecutionProvider supplies native dispatch into the active process owner.
// This is not a wire protocol: inputs, contexts and errors retain Go identity.
type ExecutionProvider struct {
	Start           func(context.Context, StartRequest) (Run, error)
	Wait            func(context.Context, Run) ([]byte, error)
	Schedule        func(context.Context, string, string, string, time.Duration, []byte) error
	Dispatch        func(context.Context, string, any, DispatchOptions) (runtimeapi.ExecutionReceipt, error)
	DispatchAndWait func(context.Context, string, any, DispatchOptions) ([]byte, error)
}

var executionOwner atomic.Pointer[ExecutionProvider]

func BindExecution(provider ExecutionProvider) {
	if provider.Start == nil || provider.Wait == nil || provider.Schedule == nil || provider.Dispatch == nil || provider.DispatchAndWait == nil {
		panic("scenery: incomplete native durable execution owner")
	}
	if !executionOwner.CompareAndSwap(nil, &provider) {
		panic("scenery: native durable execution owner already bound")
	}
}

func Start(ctx context.Context, request StartRequest) (Run, error) {
	if owner := executionOwner.Load(); owner != nil {
		return owner.Start(ctx, request)
	}
	return Run{}, fmt.Errorf("runtime: durable execution owner is not initialized")
}
func Wait(ctx context.Context, run Run) ([]byte, error) {
	if owner := executionOwner.Load(); owner != nil {
		return owner.Wait(ctx, run)
	}
	return nil, fmt.Errorf("runtime: durable execution owner is not initialized")
}
func Schedule(ctx context.Context, service, taskName, id string, every time.Duration, input []byte) error {
	if owner := executionOwner.Load(); owner != nil {
		return owner.Schedule(ctx, service, taskName, id, every, input)
	}
	return fmt.Errorf("runtime: durable execution owner is not initialized")
}
func Dispatch(ctx context.Context, address string, input any, options DispatchOptions) (runtimeapi.ExecutionReceipt, error) {
	if owner := executionOwner.Load(); owner != nil {
		return owner.Dispatch(ctx, address, input, options)
	}
	return runtimeapi.ExecutionReceipt{}, fmt.Errorf("runtime: contract durable execution %s is not registered", address)
}
func DispatchAndWait(ctx context.Context, address string, input any, options DispatchOptions) ([]byte, error) {
	if owner := executionOwner.Load(); owner != nil {
		return owner.DispatchAndWait(ctx, address, input, options)
	}
	return nil, fmt.Errorf("runtime: contract durable execution %s is not registered", address)
}
