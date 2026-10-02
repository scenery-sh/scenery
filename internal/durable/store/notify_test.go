package store

import (
	"context"
	"testing"
	"time"
)

func awaitWake(t *testing.T, wake <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-wake:
	case <-time.After(3 * time.Second):
		t.Fatalf("no notification woke the %s waiter", what)
	}
}

// Queuing, finishing, requeuing, canceling and retrying a job each notify the
// task's and the job's waiters through Postgres, without polling.
func TestJobTransitionsNotifyTaskAndJobWaiters(t *testing.T) {
	s := openLiveTestStore(t, "reports")
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if err := s.ReconcileTasks(ctx, []TaskDeclaration{{Name: "reports.notify.v1", HandlerRef: "reports.notify.v1", MaxAttempts: 2, RetryInitialMS: 1, RetryMaxMS: 1}}); err != nil {
		t.Fatal(err)
	}
	taskWake, releaseTask := s.TaskWake("reports.notify.v1")
	defer releaseTask()
	if _, err := s.Start(ctx, StartRequest{ID: "job-notify", TaskName: "reports.notify.v1", InputBlob: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, taskWake, "queued task")

	jobWake, releaseJob := s.JobWake("job-notify")
	defer releaseJob()
	taskWake, releaseTask = s.TaskWake("reports.notify.v1")
	defer releaseTask()
	leased, ok, err := s.LeaseReadyJob(ctx, "worker-1", "lease-1", "reports.notify.v1")
	if err != nil || !ok || leased.ID != "job-notify" {
		t.Fatalf("lease = %+v ok=%v err=%v", leased, ok, err)
	}
	// The first failure requeues the job: both the waiter and the task loop hear it.
	if err := s.FailLeasedJob(ctx, "job-notify", "worker-1", "lease-1", []byte("first attempt failed")); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, jobWake, "requeued job")
	awaitWake(t, taskWake, "requeued task")

	jobWake, releaseJob = s.JobWake("job-notify")
	defer releaseJob()
	leased = waitLeaseReady(t, s, "lease-2", "reports.notify.v1")
	if err := s.CompleteLeasedJob(ctx, leased.ID, "worker-1", "lease-2", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, jobWake, "finished job")

	if _, err := s.Start(ctx, StartRequest{ID: "job-cancel", TaskName: "reports.notify.v1", InputBlob: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	jobWake, releaseJob = s.JobWake("job-cancel")
	defer releaseJob()
	if err := s.CancelJob(ctx, "job-cancel"); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, jobWake, "canceled job")
	taskWake, releaseTask = s.TaskWake("reports.notify.v1")
	defer releaseTask()
	if err := s.RetryJob(ctx, "job-cancel"); err != nil {
		t.Fatal(err)
	}
	awaitWake(t, taskWake, "retried task")
}

func TestNotificationChannelStaysAnIdentifier(t *testing.T) {
	if got := notificationChannel("maps"); got != "scenery_durable_maps" {
		t.Fatalf("channel = %q", got)
	}
	long := notificationChannel("a-very-long-service-name-that-exceeds-postgres-identifier-length-limits")
	if len(long) > maxIdentifierLength || long[:len(notificationPrefix)] != notificationPrefix {
		t.Fatalf("long channel = %q", long)
	}
}
