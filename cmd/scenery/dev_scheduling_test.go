package main

import (
	"context"
	"errors"
	"testing"

	"scenery.sh/internal/build"
)

func TestDevSchedulingRecordsObservedBackgroundPolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		reason string
		err    error
		steps  int
		ok     bool
	}{
		{reason: "", steps: 0},
		{reason: "darwin_background_absent", steps: 1, ok: true},
		{reason: "darwin_background_cleared", steps: 1, ok: true},
		{reason: "darwin_background_clear_failed", err: errors.New("denied"), steps: 1},
	} {
		var steps []build.Step
		ctx := build.WithTrace(context.Background(), func(step build.Step) { steps = append(steps, step) })
		recordDevScheduling(ctx, func() (string, error) { return test.reason, test.err })
		if len(steps) != test.steps {
			t.Fatalf("reason %q recorded %d steps", test.reason, len(steps))
		}
		if test.steps == 1 && (steps[0].Name != "process.scheduling" || steps[0].Reason != test.reason || steps[0].OK != test.ok || steps[0].Cache != "not_applicable") {
			t.Fatalf("reason %q recorded %#v", test.reason, steps[0])
		}
	}
}
