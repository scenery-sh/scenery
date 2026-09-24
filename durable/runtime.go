package durable

import (
	"context"
	"encoding/json"
	"strings"

	"scenery.sh/internal/appsdk"
	"scenery.sh/runtime/shared"
)

type SignalOptions struct {
	DedupeKey string
}

// Signal delivers a named signal to a running durable task.
func Signal(ctx context.Context, run shared.DurableRun, name string, payload any, opts ...SignalOptions) error {
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
	host := appsdk.CurrentHost()
	if host == nil {
		return appsdk.ErrNoHost
	}
	return host.DurableSignal(ctx, run.Service, run.ID, name, dedupeKey, data)
}

// Step runs fn once per durable task run and key, replaying its recorded
// result afterwards. Outside a durable task it runs fn directly.
func Step[O any](ctx context.Context, key string, fn func(context.Context) (O, error)) (O, error) {
	var zero O
	run := func(stepCtx context.Context) ([]byte, error) {
		value, err := fn(stepCtx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	var data []byte
	var err error
	if host := appsdk.CurrentHost(); host != nil {
		data, err = host.DurableStep(ctx, key, run)
	} else {
		data, err = run(ctx)
	}
	if err != nil {
		return zero, err
	}
	var out O
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, err
	}
	return out, nil
}
