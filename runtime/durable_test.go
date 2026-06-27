package runtime

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestStartDurableRuntimeReconcilesTasks(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	root := t.TempDir()
	t.Setenv("SCENERY_APP_ROOT", root)
	RegisterDurableTask(&DurableTask{
		Name:           "maps.detect.v1",
		Service:        "maps",
		Handler:        func(context.Context, []byte) ([]byte, error) { return []byte(`{"ok":true}`), nil },
		DefaultTimeout: 2 * time.Second,
		MaxAttempts:    3,
		RetryInitial:   time.Second,
		RetryMax:       5 * time.Second,
		RetryBackoff:   2,
	})

	stop, err := startDurableRuntime(context.Background(), AppConfig{Name: "demo", Role: "api"})
	if err != nil {
		t.Fatalf("startDurableRuntime: %v", err)
	}
	defer func() {
		if err := stop(context.Background()); err != nil {
			t.Fatalf("stop durable runtime: %v", err)
		}
	}()

	path := filepath.Join(root, ".scenery", "state", "db", "maps.durable.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open durable db: %v", err)
	}
	defer db.Close()
	var timeoutMS, attempts, retryInitialMS, retryMaxMS int
	var retryBackoff float64
	err = db.QueryRow(`
SELECT default_timeout_ms, max_attempts, retry_initial_ms, retry_max_ms, retry_backoff
FROM tasks
WHERE name = 'maps.detect.v1'
`).Scan(&timeoutMS, &attempts, &retryInitialMS, &retryMaxMS, &retryBackoff)
	if err != nil {
		t.Fatalf("query task row: %v", err)
	}
	if timeoutMS != 2000 || attempts != 3 || retryInitialMS != 1000 || retryMaxMS != 5000 || retryBackoff != 2 {
		t.Fatalf("task row = timeout %d attempts %d retry %d/%d backoff %v", timeoutMS, attempts, retryInitialMS, retryMaxMS, retryBackoff)
	}

	run, err := StartDurableTask(context.Background(), DurableStartRequest{
		Service:   "maps",
		TaskName:  "maps.detect.v1",
		ID:        "job-test",
		DedupeKey: "map-1",
		Input:     map[string]string{"id": "map-1"},
	})
	if err != nil {
		t.Fatalf("StartDurableTask: %v", err)
	}
	if run.ID != "job-test" || run.Service != "maps" || run.TaskName != "maps.detect.v1" || run.State != "queued" || run.DedupeKey != "map-1" {
		t.Fatalf("run = %+v", run)
	}
	var state string
	err = db.QueryRow(`SELECT state FROM jobs WHERE id = 'job-test'`).Scan(&state)
	if err != nil {
		t.Fatalf("query job row: %v", err)
	}
	if state != "queued" {
		t.Fatalf("job state = %q", state)
	}
}

func TestDurableLocalWorkerExecutesQueuedJob(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	root := t.TempDir()
	t.Setenv("SCENERY_APP_ROOT", root)
	RegisterDurableTask(&DurableTask{
		Name:    "maps.echo.v1",
		Service: "maps",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`{"ok":true}`), nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := startDurableRuntime(ctx, AppConfig{Name: "demo", Role: "worker"})
	if err != nil {
		t.Fatalf("startDurableRuntime: %v", err)
	}
	defer func() {
		if err := stop(context.Background()); err != nil {
			t.Fatalf("stop durable runtime: %v", err)
		}
	}()

	run, err := StartDurableTask(context.Background(), DurableStartRequest{
		Service:  "maps",
		TaskName: "maps.echo.v1",
		ID:       "job-worker",
		Input:    map[string]string{"message": "hi"},
	})
	if err != nil {
		t.Fatalf("StartDurableTask: %v", err)
	}
	if run.State != "queued" {
		t.Fatalf("run state = %q", run.State)
	}

	path := filepath.Join(root, ".scenery", "state", "db", "maps.durable.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open durable db: %v", err)
	}
	defer db.Close()
	waitRuntimeJobState(t, db, "job-worker", "succeeded")
}

func TestStartDurableRuntimeRequiresAppRootForTasks(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	t.Setenv("SCENERY_APP_ROOT", "")
	RegisterDurableTask(&DurableTask{
		Name:    "maps.detect.v1",
		Service: "maps",
		Handler: func(context.Context, []byte) ([]byte, error) { return []byte(`{}`), nil },
	})
	if _, err := startDurableRuntime(context.Background(), AppConfig{Name: "demo"}); err == nil {
		t.Fatal("expected missing app root error")
	}
}

func TestDurableScheduleEnqueuesAndRuns(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	root := t.TempDir()
	t.Setenv("SCENERY_APP_ROOT", root)
	RegisterDurableTask(&DurableTask{
		Name:    "maps.scheduled.v1",
		Service: "maps",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`{"ok":true}`), nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := startDurableRuntime(ctx, AppConfig{Name: "demo", Role: "all"})
	if err != nil {
		t.Fatalf("startDurableRuntime: %v", err)
	}
	defer func() {
		cancel()
		if err := stop(context.Background()); err != nil {
			t.Fatalf("stop durable runtime: %v", err)
		}
	}()

	if err := DurableSchedule(context.Background(), "maps", "maps.scheduled.v1", "sched-runtime", 10*time.Millisecond, []byte(`{"id":"1"}`)); err != nil {
		t.Fatalf("DurableSchedule: %v", err)
	}
	path := filepath.Join(root, ".scenery", "state", "db", "maps.durable.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open durable db: %v", err)
	}
	defer db.Close()
	waitRuntimeAnyJobState(t, db, "maps.scheduled.v1", "succeeded")
}

func waitRuntimeJobState(t *testing.T, db *sql.DB, jobID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		if err := db.QueryRow(`SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	var got string
	_ = db.QueryRow(`SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&got)
	t.Fatalf("job %s state = %q, want %q", jobID, got, want)
}

func waitRuntimeAnyJobState(t *testing.T, db *sql.DB, taskName, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		if err := db.QueryRow(`SELECT state FROM jobs WHERE task_name = ? ORDER BY created_at DESC LIMIT 1`, taskName).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	var got string
	_ = db.QueryRow(`SELECT state FROM jobs WHERE task_name = ? ORDER BY created_at DESC LIMIT 1`, taskName).Scan(&got)
	t.Fatalf("latest job for task %s state = %q, want %q", taskName, got, want)
}
