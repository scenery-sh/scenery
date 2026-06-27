package durable

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	sceneryruntime "scenery.sh/runtime"
)

type Task[I, O any] struct {
	name    string
	config  TaskConfig
	handler func(context.Context, I) (O, error)
}

type Run[O any] struct {
	ID        string
	Service   string
	TaskName  string
	State     string
	DedupeKey string
}

type TaskConfig struct {
	Service        string
	Timeout        time.Duration
	LeaseDuration  time.Duration
	MaxAttempts    int
	Retry          RetryPolicy
	Requirements   Requirements
	MaxConcurrency int
}

type RetryPolicy struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	BackoffFactor   float64
	Jitter          float64
}

type Requirements struct {
	Labels map[string]string
}

type StartOptions struct {
	ID        string
	DedupeKey string
}

func NewTask[I, O any](name string, cfg TaskConfig, handler func(context.Context, I) (O, error)) *Task[I, O] {
	name = strings.TrimSpace(name)
	cfg.Service = strings.TrimSpace(cfg.Service)
	if name == "" {
		panic("durable: task name must not be empty")
	}
	if cfg.Service == "" {
		panic("durable: task service must not be empty")
	}
	if handler == nil {
		panic("durable: task handler must not be nil")
	}
	task := &Task[I, O]{name: name, config: cfg, handler: handler}
	sceneryruntime.RegisterDurableTask(&sceneryruntime.DurableTask{
		Name:             name,
		Service:          cfg.Service,
		HandlerRef:       name,
		Handler:          task.runtimeHandler,
		DefaultTimeout:   cfg.Timeout,
		DefaultLease:     cfg.LeaseDuration,
		MaxAttempts:      cfg.MaxAttempts,
		RetryInitial:     cfg.Retry.InitialInterval,
		RetryMax:         cfg.Retry.MaxInterval,
		RetryBackoff:     cfg.Retry.BackoffFactor,
		RetryJitter:      cfg.Retry.Jitter,
		RequirementsJSON: requirementsJSON(cfg.Requirements),
	})
	return task
}

func (t *Task[I, O]) runtimeHandler(ctx context.Context, data []byte) ([]byte, error) {
	var input I
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, err
	}
	output, err := t.handler(ctx, input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(output)
}

func Start[I, O any](ctx context.Context, task *Task[I, O], input I, options ...StartOptions) (Run[O], error) {
	if task == nil {
		return Run[O]{}, errors.New("durable: task must not be nil")
	}
	opts := StartOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	run, err := sceneryruntime.StartDurableTask(ctx, sceneryruntime.DurableStartRequest{
		Service:   task.config.Service,
		TaskName:  task.name,
		ID:        opts.ID,
		DedupeKey: opts.DedupeKey,
		Input:     input,
	})
	if err != nil {
		return Run[O]{}, err
	}
	return Run[O]{
		ID:        run.ID,
		Service:   run.Service,
		TaskName:  run.TaskName,
		State:     run.State,
		DedupeKey: run.DedupeKey,
	}, nil
}

func (t *Task[I, O]) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}

func (t *Task[I, O]) Config() TaskConfig {
	if t == nil {
		return TaskConfig{}
	}
	return t.config
}

func (t *Task[I, O]) Handler() func(context.Context, I) (O, error) {
	if t == nil {
		return nil
	}
	return t.handler
}

func requirementsJSON(req Requirements) string {
	if len(req.Labels) == 0 {
		return "{}"
	}
	data, err := json.Marshal(req)
	if err != nil {
		return "{}"
	}
	return string(data)
}
