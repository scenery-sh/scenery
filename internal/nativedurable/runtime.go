// Package nativedurable owns native durable helpers and request-local step
// execution. The runtime supplies persistence; callbacks execute in-process.
package nativedurable

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

type Run struct {
	ID        string
	Service   string
	TaskName  string
	State     string
	DedupeKey string
}

// StepStore is the durable persistence boundary. Callback execution and its Go
// context stay local; only the already-persisted result/error bytes are stored.
type StepStore interface {
	Load(context.Context, string, string) ([]byte, bool, error)
	Save(context.Context, string, string, string, []byte, []byte) error
}

type stepContextKey struct{}
type stepContext struct {
	store StepStore
	jobID string
}

func WithStepStore(ctx context.Context, jobID string, store StepStore) context.Context {
	return context.WithValue(ctx, stepContextKey{}, stepContext{store: store, jobID: jobID})
}

func Step(ctx context.Context, key string, run func(context.Context) ([]byte, error)) ([]byte, error) {
	if run == nil {
		return nil, errors.New("runtime: durable step function is required")
	}
	execution, _ := ctx.Value(stepContextKey{}).(stepContext)
	if execution.store == nil || strings.TrimSpace(execution.jobID) == "" {
		return run(ctx)
	}
	if result, ok, err := execution.store.Load(ctx, execution.jobID, key); err != nil {
		return nil, err
	} else if ok {
		return result, nil
	}
	result, err := run(ctx)
	if err != nil {
		_ = execution.store.Save(ctx, execution.jobID, key, "failed", nil, []byte(err.Error()))
		return nil, err
	}
	if err := execution.store.Save(ctx, execution.jobID, key, "succeeded", result, nil); err != nil {
		return nil, err
	}
	return result, nil
}

type SignalFunc func(context.Context, string, string, string, string, []byte) error

var signalOwner atomic.Pointer[SignalFunc]

func BindSignal(signal SignalFunc) {
	if signal == nil {
		panic("scenery: nil durable signal owner")
	}
	if !signalOwner.CompareAndSwap(nil, &signal) {
		panic("scenery: durable signal owner already bound")
	}
}

func Signal(ctx context.Context, service, jobID, name, dedupeKey string, payload []byte) error {
	service, err := NormalizeServiceName(service)
	if err != nil {
		return err
	}
	if signal := signalOwner.Load(); signal != nil {
		return (*signal)(ctx, service, jobID, name, dedupeKey, payload)
	}
	return fmt.Errorf("runtime: durable service %q is not active", service)
}
