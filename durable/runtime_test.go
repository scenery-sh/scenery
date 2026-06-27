package durable

import (
	"context"
	"testing"
)

func TestStepRunsWithoutDurableJobContext(t *testing.T) {
	t.Parallel()

	calls := 0
	got, err := Step(context.Background(), "plain", func(context.Context) (string, error) {
		calls++
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok" || calls != 1 {
		t.Fatalf("Step = %q calls=%d", got, calls)
	}
}
