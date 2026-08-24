package runtime

import (
	"context"
	"sync"
	"testing"
	"time"
)

type scheduledTimesPlan []time.Time

func (plan scheduledTimesPlan) Next(after time.Time) time.Time {
	for _, candidate := range plan {
		if candidate.After(after) {
			return candidate
		}
	}
	return time.Time{}
}

type observedScheduledTimesPlan struct {
	times     []time.Time
	exhausted chan struct{}
	once      sync.Once
}

func (plan *observedScheduledTimesPlan) Next(after time.Time) time.Time {
	for _, candidate := range plan.times {
		if candidate.After(after) {
			return candidate
		}
	}
	plan.once.Do(func() { close(plan.exhausted) })
	return time.Time{}
}

func TestCronSkipOverlapPolicyIsEnforced(t *testing.T) {
	base := time.Now().UTC().Add(30 * time.Millisecond)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	plan := &observedScheduledTimesPlan{
		times:     []time.Time{base, base.Add(2 * time.Millisecond), base.Add(4 * time.Millisecond)},
		exhausted: make(chan struct{}),
	}
	job := &CronJob{ID: "skip", OverlapPolicy: "skip", plan: plan, Invoke: func(ctx context.Context) error {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { runCronJobLoop(ctx, job); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first skipped-policy invocation did not start")
	}
	select {
	case <-plan.exhausted:
	case <-time.After(time.Second):
		t.Fatal("skip-policy schedule was not exhausted")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("skip-policy scheduler did not finish")
	}
	select {
	case <-started:
		t.Fatal("skip policy allowed an overlapping invocation")
	default:
	}
}

func TestCronAllowOverlapPolicyIsEnforced(t *testing.T) {
	base := time.Now().UTC().Add(30 * time.Millisecond)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	job := &CronJob{ID: "allow", OverlapPolicy: "allow_all", plan: scheduledTimesPlan{base, base.Add(2 * time.Millisecond), base.Add(4 * time.Millisecond)}, Invoke: func(ctx context.Context) error {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { runCronJobLoop(ctx, job); close(done) }()
	for index := range 3 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("allow policy started only %d invocations", index)
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("allow-policy scheduler did not finish")
	}
}
