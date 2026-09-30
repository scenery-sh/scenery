package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// A task with a concurrency limit runs that many attempts at once in one
// worker process, and a task never waits behind another task's attempts.
func TestDurableLocalWorkerRunsTaskSlotsIndependently(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	dsn := liveRuntimeDatabaseURL(t)
	t.Setenv("DATABASE_URL", dsn)

	var running, peak atomic.Int32
	release := make(chan struct{})
	registerDurableTaskForTest(t, &DurableTask{
		Name:           "maps.slow.v1",
		Service:        "maps",
		MaxConcurrency: 2,
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			n := running.Add(1)
			for {
				current := peak.Load()
				if n <= current || peak.CompareAndSwap(current, n) {
					break
				}
			}
			defer running.Add(-1)
			select {
			case <-release:
				return []byte(`{"ok":true}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	registerDurableTaskForTest(t, &DurableTask{
		Name:    "maps.fast.v1",
		Service: "maps",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`{"ok":true}`), nil
		},
	})

	ctx := t.Context()
	stop, err := startDurableRuntime(ctx, AppConfig{Name: "demo", Role: "worker"})
	if err != nil {
		t.Fatalf("startDurableRuntime: %v", err)
	}
	defer func() {
		close(release)
		if err := stop(context.Background()); err != nil {
			t.Fatalf("stop durable runtime: %v", err)
		}
	}()

	for _, id := range []string{"slow-1", "slow-2", "slow-3"} {
		if _, err := StartDurableTask(context.Background(), DurableStartRequest{Service: "maps", TaskName: "maps.slow.v1", ID: id, Input: map[string]string{}}); err != nil {
			t.Fatalf("StartDurableTask %s: %v", id, err)
		}
	}
	if _, err := StartDurableTask(context.Background(), DurableStartRequest{Service: "maps", TaskName: "maps.fast.v1", ID: "fast-1", Input: map[string]string{}}); err != nil {
		t.Fatalf("StartDurableTask fast-1: %v", err)
	}

	db := openRuntimeDB(t, dsn)
	defer func() { _ = db.Close() }()
	// The fast task completes while both slow slots are still blocked.
	waitRuntimeJobState(t, db, "fast-1", "succeeded")
	deadline := time.Now().Add(2 * time.Second)
	for peak.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := peak.Load(); got != 2 {
		t.Fatalf("peak concurrent slow attempts = %d, want 2", got)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM scenery.durable_jobs WHERE id = 'slow-3'`).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("slow-3 state = %q (%v), want queued while both slots are busy", state, err)
	}
}
