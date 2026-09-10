package build

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBuildTracePreservesNestedIntervalsAndFailure(t *testing.T) {
	t.Parallel()
	var steps []Step
	ctx := WithTrace(context.Background(), func(step Step) { steps = append(steps, step) })
	want := errors.New("compile failed")
	err := observeBuildAction(ctx, "prepare", func() error {
		finishStep(ctx, "cache", time.Now(), "miss", "source_snapshot_changed", nil)
		return observeBuildAction(ctx, "compile", func() error { return want })
	})
	if !errors.Is(err, want) || len(steps) != 3 {
		t.Fatalf("trace altered execution: steps=%+v err=%v", steps, err)
	}
	if steps[0].Cache != "miss" || steps[0].Reason != "source_snapshot_changed" || !steps[0].OK {
		t.Fatalf("cache decision missing: %+v", steps[0])
	}
	inner, outer := steps[1], steps[2]
	if inner.OK || outer.OK || inner.Name != "compile" || outer.Name != "prepare" {
		t.Fatalf("failure propagation missing: %+v", steps)
	}
	if outer.StartedAt.After(inner.StartedAt) || outer.Duration < inner.Duration {
		t.Fatalf("nested intervals lost their critical-path relation: %+v", steps)
	}
}

func TestBuildTraceIsOptional(t *testing.T) {
	t.Parallel()
	value, err := observeBuild(context.Background(), "work", func() (int, error) { return 42, nil })
	if err != nil || value != 42 {
		t.Fatalf("unobserved work changed: value=%d err=%v", value, err)
	}
}
