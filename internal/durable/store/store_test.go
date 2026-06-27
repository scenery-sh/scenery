package store

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDurableDBPathUsesServiceName(t *testing.T) {
	got, err := DurableDBPath("/state", "maps/service")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/state", "db", "maps-service.durable.sqlite")
	if got != want {
		t.Fatalf("DurableDBPath = %q, want %q", got, want)
	}
}

func TestDurableDBPathRejectsUnsafeServiceNames(t *testing.T) {
	for _, name := range []string{"", "../maps", "/absolute/path", `maps\windows`, "maps:bad", "bad/../name"} {
		t.Run(name, func(t *testing.T) {
			if _, err := DurableDBPath("/state", name); err == nil {
				t.Fatalf("expected error for %q", name)
			}
		})
	}
}

func TestOpenCreatesDurableDBWithExpectedTables(t *testing.T) {
	ctx := context.Background()
	path, err := DurableDBPath(t.TempDir(), "maps")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, "maps", path, Options{Synchronous: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !strings.HasSuffix(s.Path, "maps.durable.sqlite") {
		t.Fatalf("path = %q, want service durable sqlite suffix", s.Path)
	}
	if got := pragmaValue(t, s, "foreign_keys"); got != "1" {
		t.Fatalf("foreign_keys pragma = %q, want 1", got)
	}
	if got := pragmaValue(t, s, "busy_timeout"); got != "5000" {
		t.Fatalf("busy_timeout pragma = %q, want 5000", got)
	}

	tables := tableNames(t, s)
	for _, want := range []string{"meta", "schema_migrations", "tasks", "jobs", "job_events", "worker_tokens", "locks"} {
		if !tables[want] {
			t.Fatalf("missing table %q in %#v", want, tables)
		}
	}
	for name := range tables {
		if strings.Contains(strings.ToLower(name), "scenery") {
			t.Fatalf("table %q contains scenery", name)
		}
	}
}

func TestReconcileTasksAndStartAreIdempotentByDedupeKey(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{
		Name:       "maps.echo.v1",
		HandlerRef: "maps.Echo",
	}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Start(ctx, StartRequest{
		ID:        "job-1",
		TaskName:  "maps.echo.v1",
		DedupeKey: "echo:1",
		InputBlob: []byte(`{"message":"hi"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Start(ctx, StartRequest{
		ID:        "job-2",
		TaskName:  "maps.echo.v1",
		DedupeKey: "echo:1",
		InputBlob: []byte(`{"message":"hi again"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("dedupe returned %#v, want %#v", second, first)
	}
	if count := rowCount(t, s, "jobs"); count != 1 {
		t.Fatalf("jobs count = %d, want 1", count)
	}
	if count := rowCount(t, s, "job_events"); count != 1 {
		t.Fatalf("job_events count = %d, want 1", count)
	}
}

func TestLeaseCompleteAndFailJobs(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
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
	s := openTestStore(t)
	defer s.Close()

	token, err := s.CreateWorkerToken(ctx, WorkerTokenRequest{
		ID:     "tok-1",
		Name:   "worker token",
		Secret: "secret-worker-token",
	})
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
	if leased.LeaseID != "lease-good" {
		t.Fatalf("lease id = %q", leased.LeaseID)
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

func TestFailJobRetriesUntilMaxAttempts(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{
		Name:           "maps.retry.v1",
		HandlerRef:     "maps.retry.v1",
		MaxAttempts:    2,
		RetryInitialMS: 1,
		RetryMaxMS:     1,
	}}); err != nil {
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
	s := openTestStore(t)
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
	job, ok, err := s.GetJob(ctx, "job-admin")
	if err != nil || !ok {
		t.Fatalf("GetJob ok=%v err=%v", ok, err)
	}
	if job.TaskName != "maps.admin.v1" {
		t.Fatalf("job = %+v", job)
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

func TestStepsAndSignals(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.step.v1", HandlerRef: "maps.step.v1"}}); err != nil {
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
}

func TestRunDueSchedulesStartsJobs(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	defer s.Close()

	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "maps.scheduled.v1", HandlerRef: "maps.scheduled.v1"}}); err != nil {
		t.Fatal(err)
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
	listed, err := s.ListJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].State != "queued" {
		t.Fatalf("listed = %+v", listed)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	path, err := DurableDBPath(t.TempDir(), "maps")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, "maps", path, Options{Synchronous: "off"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pragmaValue(t *testing.T, s *Store, name string) string {
	t.Helper()
	var got string
	if err := s.DB().QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func tableNames(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query(`
SELECT name
FROM sqlite_master
WHERE type = 'table'
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

func rowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var count int
	if err := s.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertJobState(t *testing.T, s *Store, jobID, want string) {
	t.Helper()
	var got string
	if err := s.DB().QueryRow(`SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("job %s state = %q, want %q", jobID, got, want)
	}
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
