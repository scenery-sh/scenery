package runtime

import (
	"context"
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

func TestCronOverlapPoliciesAreEnforced(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		base := time.Now().UTC().Add(100 * time.Millisecond)
		started := make(chan struct{}, 4)
		release := make(chan struct{})
		job := &CronJob{ID: "skip", OverlapPolicy: "skip", plan: scheduledTimesPlan{base, base.Add(40 * time.Millisecond), base.Add(80 * time.Millisecond)}, Invoke: func(ctx context.Context) error {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { runCronJobLoop(ctx, job); close(done) }()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("first skipped-policy invocation did not start")
		}
		select {
		case <-time.After(160 * time.Millisecond):
		case <-started:
			t.Fatal("skip policy allowed an overlapping invocation")
		}
		close(release)
		cancel()
		<-done
	})

	t.Run("allow", func(t *testing.T) {
		base := time.Now().UTC().Add(100 * time.Millisecond)
		started := make(chan struct{}, 4)
		release := make(chan struct{})
		job := &CronJob{ID: "allow", OverlapPolicy: "allow_all", plan: scheduledTimesPlan{base, base.Add(40 * time.Millisecond), base.Add(80 * time.Millisecond)}, Invoke: func(ctx context.Context) error {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { runCronJobLoop(ctx, job); close(done) }()
		for index := 0; index < 3; index++ {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatalf("allow policy started only %d invocations", index)
			}
		}
		close(release)
		cancel()
		<-done
	})
}
