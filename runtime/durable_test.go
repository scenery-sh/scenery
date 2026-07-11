package runtime

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/runtimeapi"
)

func TestDurableInvocationMetadataSurvivesDispatchBoundary(t *testing.T) {
	base := runtimeapi.NewInvocationWithMetadata(runtimeapi.InvocationMetadata{
		ID: "http-1", Principal: "user-1", TenantID: "tenant-1", TraceID: "trace-1",
		CallerBinding: "house/binding/process", Deployment: "app/deployment/preview", Locale: "en-GB",
	})
	encoded, err := durableInvocationMetadataJSON(runtimeapi.WithInvocation(context.Background(), base))
	if err != nil {
		t.Fatal(err)
	}
	ctx, restore := enterDurableInvocation(context.Background(), "house", "house/execution/process", "job-1", time.Minute, durableInvocationMetadataFromJSON(encoded))
	defer restore()
	_, err = runDurableTaskHandler(ctx, time.Minute, func(handlerCtx context.Context, _ []byte) ([]byte, error) {
		invocation, ok := runtimeapi.InvocationFromContext(handlerCtx)
		if !ok {
			t.Fatal("durable handler has no invocation")
		}
		if invocation.ID() != "job-1" || invocation.ExecutionID() != "job-1" || invocation.Principal() != "user-1" || invocation.TenantID() != "tenant-1" || invocation.TraceID() != "trace-1" || invocation.CallerBinding() != "house/binding/process" || invocation.Deployment() != "app/deployment/preview" || invocation.Locale() != "en-GB" {
			t.Fatalf("durable invocation = %#v", invocation)
		}
		if request := CurrentRequest(); request.Type != "durable-call" || request.ExecutionID != "job-1" {
			t.Fatalf("durable request = %#v", request)
		}
		return []byte(`{}`), nil
	}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartDurableRuntimeReconcilesTasks(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	dsn := liveRuntimeDatabaseURL(t)
	t.Setenv("DATABASE_URL", dsn)

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

	db := openRuntimeDB(t, dsn)
	defer db.Close()
	var timeoutMS, attempts, retryInitialMS, retryMaxMS int
	var retryBackoff float64
	err = db.QueryRow(`
SELECT default_timeout_ms, max_attempts, retry_initial_ms, retry_max_ms, retry_backoff
FROM scenery.durable_tasks
WHERE service = 'maps' AND name = 'maps.detect.v1'
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
	err = db.QueryRow(`SELECT state FROM scenery.durable_jobs WHERE service = 'maps' AND id = 'job-test'`).Scan(&state)
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
	dsn := liveRuntimeDatabaseURL(t)
	t.Setenv("DATABASE_URL", dsn)

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

	db := openRuntimeDB(t, dsn)
	defer db.Close()
	waitRuntimeJobState(t, db, "job-worker", "succeeded")
}

func TestStartDurableRuntimeRequiresDatabaseURLForTasks(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()

	t.Setenv("DATABASE_URL", "")
	t.Setenv(postgresdb.RegistryEnv, "")
	RegisterDurableTask(&DurableTask{
		Name:    "maps.detect.v1",
		Service: "maps",
		Handler: func(context.Context, []byte) ([]byte, error) { return []byte(`{}`), nil },
	})
	if _, err := startDurableRuntime(context.Background(), AppConfig{Name: "demo"}); err == nil {
		t.Fatal("expected missing DATABASE_URL error")
	}
}

func TestRunDurableTaskHandlerEnforcesDeclaredTimeout(t *testing.T) {
	started := make(chan struct{})
	_, err := runDurableTaskHandler(context.Background(), 10*time.Millisecond, func(ctx context.Context, _ []byte) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handler error = %v, want deadline exceeded", err)
	}
	select {
	case <-started:
	default:
		t.Fatal("handler was not invoked")
	}
}

func TestDurableScheduleEnqueuesAndRuns(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	dsn := liveRuntimeDatabaseURL(t)
	t.Setenv("DATABASE_URL", dsn)

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
	db := openRuntimeDB(t, dsn)
	defer db.Close()
	waitRuntimeAnyJobState(t, db, "maps.scheduled.v1", "succeeded")
}

func liveRuntimeDatabaseURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SCENERY_TEST_DATABASE_URL"))
	if raw == "" {
		t.Skip("SCENERY_TEST_DATABASE_URL is not set; skipping live Postgres runtime durable test")
	}
	adminURL, err := runtimeAdminDatabaseURL(raw)
	if err != nil {
		t.Fatalf("parse SCENERY_TEST_DATABASE_URL: %v", err)
	}
	admin, err := postgresdb.Open(context.Background(), adminURL)
	if err != nil {
		t.Skipf("SCENERY_TEST_DATABASE_URL is not reachable for live Postgres runtime tests: %v", err)
	}
	name := "scenery_runtime_durable_test_" + randomRuntimeHex(t, 8)
	if _, err := admin.ExecContext(context.Background(), `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()
		t.Skipf("SCENERY_TEST_DATABASE_URL cannot create per-test database: %v", err)
	}
	u, _ := url.Parse(raw)
	u.Path = "/" + name
	t.Cleanup(func() {
		db, _ := sql.Open(postgresdb.DriverName, adminURL)
		if db != nil {
			_, _ = db.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
			_, _ = db.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name)
			_ = db.Close()
		}
		_ = admin.Close()
	})
	return u.String()
}

func runtimeAdminDatabaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/postgres"
	return u.String(), nil
}

func randomRuntimeHex(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buf)
}

func openRuntimeDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := postgresdb.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func waitRuntimeJobState(t *testing.T, db *sql.DB, jobID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		if err := db.QueryRow(`SELECT state FROM scenery.durable_jobs WHERE id = $1`, jobID).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	var got string
	_ = db.QueryRow(`SELECT state FROM scenery.durable_jobs WHERE id = $1`, jobID).Scan(&got)
	t.Fatalf("job %s state = %q, want %q", jobID, got, want)
}

func waitRuntimeAnyJobState(t *testing.T, db *sql.DB, taskName, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		if err := db.QueryRow(`SELECT state FROM scenery.durable_jobs WHERE task_name = $1 ORDER BY created_at DESC LIMIT 1`, taskName).Scan(&got); err == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	var got string
	_ = db.QueryRow(`SELECT state FROM scenery.durable_jobs WHERE task_name = $1 ORDER BY created_at DESC LIMIT 1`, taskName).Scan(&got)
	t.Fatalf("latest job for task %s state = %q, want %q", taskName, got, want)
}
