package runtime

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// A queued job starts on a notification, not on the idle poll: with the poll
// set far beyond the test, the job still succeeds within a second.
func TestDurableLocalWorkerStartsQueuedJobOnNotification(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	dsn := liveRuntimeDatabaseURL(t)
	t.Setenv("DATABASE_URL", dsn)
	previousIdle, previousWait := durableIdlePoll, durableWaitPoll
	durableIdlePoll, durableWaitPoll = time.Hour, time.Hour
	t.Cleanup(func() { durableIdlePoll, durableWaitPoll = previousIdle, previousWait })

	registerDurableTaskForTest(t, &DurableTask{
		Name:    "maps.notify.v1",
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
		if err := stop(context.Background()); err != nil {
			t.Fatalf("stop durable runtime: %v", err)
		}
	}()
	// Let the task loop lease once and settle into its notification wait.
	time.Sleep(200 * time.Millisecond)

	started := time.Now()
	run, err := StartDurableTask(context.Background(), DurableStartRequest{Service: "maps", TaskName: "maps.notify.v1", ID: "job-notify", Input: map[string]string{}})
	if err != nil {
		t.Fatalf("StartDurableTask: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := WaitDurableTask(waitCtx, run)
	if err != nil {
		t.Fatalf("WaitDurableTask: %v", err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("result = %s", result)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("job took %s with polling disabled; notifications did not wake the worker or the waiter", elapsed)
	}
}

// A failed attempt keeps the handler's own error text as its reason.
func TestDurableFailedAttemptRecordsTheHandlerError(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	dsn := liveRuntimeDatabaseURL(t)
	t.Setenv("DATABASE_URL", dsn)

	registerDurableTaskForTest(t, &DurableTask{
		Name:        "maps.broken.v1",
		Service:     "maps",
		MaxAttempts: 1,
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return nil, errors.New("provider returned no tiles for 37.7,-121.9")
		},
	})
	ctx := t.Context()
	stop, err := startDurableRuntime(ctx, AppConfig{Name: "demo", Role: "worker"})
	if err != nil {
		t.Fatalf("startDurableRuntime: %v", err)
	}
	defer func() {
		if err := stop(context.Background()); err != nil {
			t.Fatalf("stop durable runtime: %v", err)
		}
	}()
	if _, err := StartDurableTask(context.Background(), DurableStartRequest{Service: "maps", TaskName: "maps.broken.v1", ID: "job-broken", Input: map[string]string{}}); err != nil {
		t.Fatalf("StartDurableTask: %v", err)
	}
	db := openRuntimeDB(t, dsn)
	defer func() { _ = db.Close() }()
	waitRuntimeJobState(t, db, "job-broken", "failed")
	var reason sql.NullString
	if err := db.QueryRow(`SELECT convert_from(error_blob, 'UTF8') FROM scenery.durable_jobs WHERE id = 'job-broken'`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason.String != "durable task failed: provider returned no tiles for 37.7,-121.9" {
		t.Fatalf("failure reason = %q", reason.String)
	}
}
