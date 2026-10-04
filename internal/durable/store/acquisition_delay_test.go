package store

import (
	"testing"
	"time"
)

func TestTaskWakeDelayBoundsDueAndFutureDeadlines(t *testing.T) {
	for _, test := range []struct {
		seconds float64
		want    time.Duration
	}{
		{-1, 10 * time.Millisecond}, {0, 10 * time.Millisecond}, {0.25, 250 * time.Millisecond}, {60, 10 * time.Second},
	} {
		if got := taskWakeDelay(test.seconds); got != test.want {
			t.Fatalf("deadline %f: %v, want %v", test.seconds, got, test.want)
		}
	}
}
