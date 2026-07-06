package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/postgresdb"
)

func TestNormalizeServiceNameRejectsUnsafeServiceNames(t *testing.T) {
	for _, name := range []string{"", "../maps", "/absolute/path", `maps\windows`, "maps:bad", "bad/../name"} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeServiceName(name); err == nil {
				t.Fatalf("expected error for %q", name)
			}
		})
	}
	got, err := NormalizeServiceName("maps/service")
	if err != nil {
		t.Fatal(err)
	}
	if got != "maps-service" {
		t.Fatalf("NormalizeServiceName = %q, want maps-service", got)
	}
}

func TestOpenCreatesPostgresSchemaWithExpectedTables(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	tables := tableNames(t, s.DB())
	for _, want := range []string{"durable_schema_migrations", "durable_tasks", "durable_jobs", "durable_job_events", "durable_job_steps", "durable_job_signals", "durable_schedules", "durable_worker_tokens"} {
		if !tables[want] {
			t.Fatalf("missing table %q in %#v", want, tables)
		}
	}
	if tables["leases"] {
		t.Fatalf("separate leases table still exists")
	}
	second, err := Open(ctx, "maps", s.DatabaseURL, Options{})
	if err != nil {
		t.Fatalf("concurrent-safe second open failed: %v", err)
	}
	defer second.Close()
}

func TestReconcileTasksAndStartAreIdempotentByDedupeKey(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.echo.v1", HandlerRef: "maps.Echo"}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Start(ctx, StartRequest{ID: "job-1", TaskName: "maps.echo.v1", DedupeKey: "echo:1", InputBlob: []byte(`{"message":"hi"}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Start(ctx, StartRequest{ID: "job-2", TaskName: "maps.echo.v1", DedupeKey: "echo:1", InputBlob: []byte(`{"message":"hi again"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("dedupe returned %#v, want %#v", second, first)
	}
	if count := rowCount(t, s.DB(), "scenery.durable_jobs", s.Service); count != 1 {
		t.Fatalf("jobs count = %d, want 1", count)
	}
	if count := rowCount(t, s.DB(), "scenery.durable_job_events", s.Service); count != 1 {
		t.Fatalf("events count = %d, want 1", count)
	}
}

func TestLeaseCompleteAndFailJobs(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.detect.v1", HandlerRef: "maps.detect.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, StartRequest{ID: "job-success", TaskName: "maps.detect.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	leased, ok, err := s.LeaseReadyJob(ctx, "worker-1", "lease-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || leased.ID != "job-success" || leased.TaskName != "maps.detect.v1" || leased.Attempt != 1 {
		t.Fatalf("leased = %+v ok=%v", leased, ok)
	}
	if err := s.CompleteJob(ctx, leased.ID, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	assertJobState(t, s, "job-success", "succeeded")

	if _, err := s.Start(ctx, StartRequest{ID: "job-fail", TaskName: "maps.detect.v1", InputBlob: []byte(`{"id":"2"}`)}); err != nil {
		t.Fatal(err)
	}
	leased, ok, err = s.LeaseReadyJob(ctx, "worker-1", "lease-2")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || leased.ID != "job-fail" {
		t.Fatalf("leased fail = %+v ok=%v", leased, ok)
	}
	if err := s.FailJob(ctx, leased.ID, []byte("boom")); err != nil {
		t.Fatal(err)
	}
	assertJobState(t, s, "job-fail", "failed")
}

func TestWorkerTokenAndLeasedJobFencing(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	token, err := s.CreateWorkerToken(ctx, WorkerTokenRequest{ID: "tok-1", Name: "worker token", Secret: "secret-worker-token"})
	if err != nil {
		t.Fatal(err)
	}
	if token.TokenHash == "" || strings.Contains(token.TokenHash, "secret") {
		t.Fatalf("token hash = %q", token.TokenHash)
	}
	auth, ok, err := s.AuthenticateWorkerToken(ctx, "secret-worker-token")
	if err != nil || !ok {
		t.Fatalf("AuthenticateWorkerToken ok=%v err=%v", ok, err)
	}
	if auth.ID != "tok-1" || auth.TokenHash != token.TokenHash {
		t.Fatalf("auth token = %+v, want %+v", auth, token)
	}
	if _, ok, err := s.AuthenticateWorkerToken(ctx, "wrong"); err != nil || ok {
		t.Fatalf("AuthenticateWorkerToken(wrong) ok=%v err=%v", ok, err)
	}

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.remote.v1", HandlerRef: "maps.remote.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, StartRequest{ID: "job-remote", TaskName: "maps.remote.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	leased, ok, err := s.LeaseReadyJobWithToken(ctx, "worker-remote", "lease-good", token.TokenHash)
	if err != nil || !ok {
		t.Fatalf("LeaseReadyJobWithToken = %+v ok=%v err=%v", leased, ok, err)
	}
	if err := s.HeartbeatJob(ctx, leased.ID, "worker-remote", "wrong-lease"); err == nil {
		t.Fatal("expected heartbeat with wrong lease to fail")
	}
	if err := s.CompleteLeasedJob(ctx, leased.ID, "worker-remote", "wrong-lease", []byte(`{"ok":true}`)); err == nil {
		t.Fatal("expected complete with wrong lease to fail")
	}
	if err := s.HeartbeatJob(ctx, leased.ID, "worker-remote", "lease-good"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLeasedJob(ctx, leased.ID, "worker-remote", "lease-good", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	assertJobState(t, s, leased.ID, "succeeded")
}

func TestLeaseAndHeartbeatUseTaskLeaseDuration(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.lease.v1", HandlerRef: "maps.lease.v1", DefaultLeaseMS: 2500}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, StartRequest{ID: "job-lease", TaskName: "maps.lease.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	leased, ok, err := s.LeaseReadyJob(ctx, "worker-lease", "lease-1")
	if err != nil || !ok {
		t.Fatalf("LeaseReadyJob = %+v ok=%v err=%v", leased, ok, err)
	}
	if remaining := leaseRemainingMS(t, s, leased.ID); remaining < 1000 || remaining > 10000 {
		t.Fatalf("lease remaining = %dms, want task-specific lease near 2500ms", remaining)
	}

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.lease.v1", HandlerRef: "maps.lease.v1", DefaultLeaseMS: 5000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE scenery.durable_jobs SET lease_until = now() + interval '100 milliseconds' WHERE service = $1 AND id = $2`, s.Service, leased.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatJob(ctx, leased.ID, "worker-lease", "lease-1"); err != nil {
		t.Fatal(err)
	}
	if remaining := leaseRemainingMS(t, s, leased.ID); remaining < 3000 || remaining > 10000 {
		t.Fatalf("heartbeat remaining = %dms, want task-specific lease near 5000ms", remaining)
	}
}

func TestStaleWorkerCannotResurrectCanceledJob(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.cancel.v1", HandlerRef: "maps.cancel.v1", MaxAttempts: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, StartRequest{ID: "job-cancel", TaskName: "maps.cancel.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	leased, ok, err := s.LeaseReadyJob(ctx, "worker-stale", "lease-stale")
	if err != nil || !ok {
		t.Fatalf("LeaseReadyJob = %+v ok=%v err=%v", leased, ok, err)
	}
	if err := s.CancelJob(ctx, leased.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteLeasedJob(ctx, leased.ID, "worker-stale", "lease-stale", []byte(`{"ok":true}`)); err == nil {
		t.Fatal("expected stale complete after cancel to fail")
	}
	assertJobState(t, s, leased.ID, "canceled")
	if err := s.FailLeasedJob(ctx, leased.ID, "worker-stale", "lease-stale", []byte("boom")); err == nil {
		t.Fatal("expected stale fail after cancel to fail")
	}
	assertJobState(t, s, leased.ID, "canceled")
}

func TestFailJobRetriesUntilMaxAttempts(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.retry.v1", HandlerRef: "maps.retry.v1", MaxAttempts: 2, RetryInitialMS: 1, RetryMaxMS: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, StartRequest{ID: "job-retry", TaskName: "maps.retry.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	leased, ok, err := s.LeaseReadyJob(ctx, "worker-1", "lease-1")
	if err != nil || !ok {
		t.Fatalf("first lease = %+v ok=%v err=%v", leased, ok, err)
	}
	if err := s.FailJob(ctx, leased.ID, []byte("try again")); err != nil {
		t.Fatal(err)
	}
	assertJobState(t, s, "job-retry", "queued")

	leased = waitLeaseReady(t, s, "lease-2")
	if leased.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", leased.Attempt)
	}
	if err := s.FailJob(ctx, leased.ID, []byte("done")); err != nil {
		t.Fatal(err)
	}
	assertJobState(t, s, "job-retry", "failed")
}

func TestJobAdminListEventsCancelAndRetry(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.admin.v1", HandlerRef: "maps.admin.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, StartRequest{ID: "job-admin", TaskName: "maps.admin.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-admin" || jobs[0].State != "queued" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if err := s.CancelJob(ctx, "job-admin"); err != nil {
		t.Fatal(err)
	}
	assertJobState(t, s, "job-admin", "canceled")
	if err := s.RetryJob(ctx, "job-admin"); err != nil {
		t.Fatal(err)
	}
	assertJobState(t, s, "job-admin", "queued")
	events, err := s.JobEvents(ctx, "job-admin")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.EventType)
	}
	for _, want := range []string{"job.created", "job.canceled", "job.retry_requested"} {
		if !slices.Contains(got, want) {
			t.Fatalf("events = %+v, want %s", got, want)
		}
	}
}

func TestStepsSignalsAndSchedules(t *testing.T) {
	ctx := context.Background()
	s := openLiveTestStore(t, "maps")
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.step.v1", HandlerRef: "maps.step.v1"}, {Name: "maps.scheduled.v1", HandlerRef: "maps.scheduled.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, StartRequest{ID: "job-step", TaskName: "maps.step.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveStep(ctx, "job-step", "fetch", "succeeded", "json", []byte(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	step, ok, err := s.GetStep(ctx, "job-step", "fetch")
	if err != nil || !ok {
		t.Fatalf("GetStep ok=%v err=%v", ok, err)
	}
	if step.State != "succeeded" || string(step.ResultBlob) != `{"ok":true}` {
		t.Fatalf("step = %+v", step)
	}
	if err := s.SignalJob(ctx, "job-step", "wake", "wake-1", []byte(`{"now":true}`)); err != nil {
		t.Fatal(err)
	}
	events, err := s.JobEvents(ctx, "job-step")
	if err != nil {
		t.Fatal(err)
	}
	foundSignal := false
	for _, event := range events {
		if event.EventType == "job.signaled" {
			foundSignal = true
		}
	}
	if !foundSignal {
		t.Fatalf("events = %+v, want job.signaled", events)
	}

	if err := s.UpsertSchedule(ctx, "sched-1", "maps.scheduled.v1", time.Minute, []byte(`{"id":"1"}`)); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.RunDueSchedules(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].TaskName != "maps.scheduled.v1" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func openLiveTestStore(t *testing.T, service string) *Store {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SCENERY_TEST_DATABASE_URL"))
	if raw == "" {
		t.Skip("SCENERY_TEST_DATABASE_URL is not set; skipping live Postgres durable store test")
	}
	testURL, cleanup := createLiveTestDatabase(t, raw)
	t.Cleanup(cleanup)
	s, err := Open(context.Background(), service, testURL, Options{})
	if err != nil {
		t.Fatalf("open live durable store: %v", err)
	}
	return s
}

func createLiveTestDatabase(t *testing.T, raw string) (string, func()) {
	t.Helper()
	adminURL, err := adminDatabaseURL(raw)
	if err != nil {
		t.Fatalf("parse SCENERY_TEST_DATABASE_URL: %v", err)
	}
	admin, err := postgresdb.Open(context.Background(), adminURL)
	if err != nil {
		t.Skipf("SCENERY_TEST_DATABASE_URL is not reachable for live Postgres tests: %v", err)
	}
	name := "scenery_durable_test_" + randomHex(t, 8)
	if _, err := admin.ExecContext(context.Background(), `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()
		t.Skipf("SCENERY_TEST_DATABASE_URL cannot create per-test database: %v", err)
	}
	u, _ := url.Parse(raw)
	u.Path = "/" + name
	testURL := u.String()
	return testURL, func() {
		db, _ := sql.Open(postgresdb.DriverName, adminURL)
		if db != nil {
			_, _ = db.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
			_, _ = db.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name)
			_ = db.Close()
		}
		_ = admin.Close()
	}
}

func adminDatabaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/postgres"
	return u.String(), nil
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buf)
}

func tableNames(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`
SELECT table_name
FROM information_schema.tables
WHERE table_schema = 'scenery'
`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func rowCount(t *testing.T, db *sql.DB, table, service string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE service = $1", table), service).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertJobState(t *testing.T, s *Store, jobID, want string) {
	t.Helper()
	var got string
	if err := s.DB().QueryRow(`SELECT state FROM scenery.durable_jobs WHERE service = $1 AND id = $2`, s.Service, jobID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("job %s state = %q, want %q", jobID, got, want)
	}
}

func leaseRemainingMS(t *testing.T, s *Store, jobID string) int {
	t.Helper()
	var remaining float64
	if err := s.DB().QueryRow(`SELECT EXTRACT(EPOCH FROM (lease_until - now())) * 1000 FROM scenery.durable_jobs WHERE service = $1 AND id = $2`, s.Service, jobID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	return int(remaining)
}

func waitLeaseReady(t *testing.T, s *Store, leaseID string) LeasedJob {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, ok, err := s.LeaseReadyJob(ctx, "worker-1", leaseID)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for lease")
	return LeasedJob{}
}
