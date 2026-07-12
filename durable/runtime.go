package durable

import (
	"context"
	"encoding/json"
	"strings"

	"scenery.sh/runtime"
)

type SignalOptions struct {
	DedupeKey string
}

func Signal(ctx context.Context, run runtime.DurableRun, name string, payload any, opts ...SignalOptions) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	dedupeKey := ""
	for _, opt := range opts {
		if strings.TrimSpace(opt.DedupeKey) != "" {
			dedupeKey = strings.TrimSpace(opt.DedupeKey)
		}
	}
	return runtime.DurableSignal(ctx, run.Service, run.ID, name, dedupeKey, data)
}

func Step[O any](ctx context.Context, key string, fn func(context.Context) (O, error)) (O, error) {
	var zero O
	data, err := runtime.DurableStep(ctx, key, func(stepCtx context.Context) ([]byte, error) {
		value, err := fn(stepCtx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	})
	if err != nil {
		return zero, err
	}
	var out O
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, err
	}
	return out, nil
}
