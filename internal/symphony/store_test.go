package symphony

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/postgresdb"
)

func TestStorePersistsBoardCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	task, err := store.CreateTask(ctx, "demo", TaskInput{
		Title:       "Demo Symphony board",
		Description: "Persist the first task",
		StatusKey:   "backlog",
		Labels:      []string{"dashboard", " dashboard "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Identifier != "SYM-1" || len(task.Labels) != 1 {
		t.Fatalf("created task = %+v", task)
	}
	updated, err := store.UpdateTask(ctx, "demo", task.ID, TaskInput{
		Title:       "Demo Symphony board",
		Description: "Updated body",
		StatusKey:   "todo",
		Priority:    "high",
		Labels:      []string{"agent-dx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusKey != "todo" || updated.Priority != "high" {
		t.Fatalf("updated task = %+v", updated)
	}
	if err := store.MoveTask(ctx, "demo", task.ID, "in_progress", 0); err != nil {
		t.Fatal(err)
	}
	state, err := store.State(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Statuses) != len(defaultStatuses) || len(state.Tasks) != 1 || state.Tasks[0].StatusKey != "in_progress" {
		t.Fatalf("state = %+v", state)
	}
	if err := store.DeleteTask(ctx, "demo", task.ID); err != nil {
		t.Fatal(err)
	}
	state, err = store.State(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 0 {
		t.Fatalf("deleted task still visible: %+v", state.Tasks)
	}
}

func TestStoreIsolatesAppsAndKeepsWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	if _, err := store.CreateTask(ctx, "demo", TaskInput{Title: "Only demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateWorkflow(ctx, "demo", WorkflowInput{WorkflowMarkdown: "manual only", Mode: "auto", MaxConcurrency: 2}); err != nil {
		t.Fatal(err)
	}
	demo, err := store.State(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.State(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(demo.Tasks) != 1 || demo.Workflow.Mode != "auto" || demo.Workflow.MaxConcurrency != 2 {
		t.Fatalf("demo state = %+v", demo)
	}
	if demo.Tasks[0].Labels == nil {
		t.Fatalf("expected labels to encode as empty array, got nil: %+v", demo.Tasks[0])
	}
	if len(other.Tasks) != 0 || other.Workflow.MaxConcurrency != 1 {
		t.Fatalf("other state = %+v", other)
	}
}

func TestStoreRunLifecycleAndRunnableTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	backlog, err := store.CreateTask(ctx, "demo", TaskInput{Title: "Backlog task", StatusKey: "backlog"})
	if err != nil {
		t.Fatal(err)
	}
	todo, err := store.CreateTask(ctx, "demo", TaskInput{Title: "Ready task", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	runnable, err := store.RunnableTasks(ctx, "demo", []string{"todo"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runnable) != 1 || runnable[0].ID != todo.ID {
		t.Fatalf("runnable = %+v, backlog = %+v", runnable, backlog)
	}
	run, err := store.StartRun(ctx, "demo", todo.ID, "/tmp/work", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "queued" || run.Attempt != 1 || run.WorkspacePath != "/tmp/work" {
		t.Fatalf("queued run = %+v", run)
	}
	if run.OwnerStartedAt == "" || run.LeaseExpiresAt == "" {
		t.Fatalf("queued run missing lease fields: %+v", run)
	}
	if _, err := store.StartRun(ctx, "demo", todo.ID, "/tmp/other", "session-1"); err == nil {
		t.Fatal("expected duplicate active run error")
	}
	run, err = store.MarkRunRunning(ctx, "demo", run.ID, 1234, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" || run.ProcessID != 1234 || run.ThreadID != "thread-1" || run.StartedAt == "" {
		t.Fatalf("running run = %+v", run)
	}
	run, err = store.MarkRunTurn(ctx, "demo", run.ID, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.TurnID != "turn-1" {
		t.Fatalf("turn run = %+v", run)
	}
	run, err = store.RecordRunArtifacts(ctx, "demo", run.ID, "M file.go", "diff --git a/file.go b/file.go")
	if err != nil {
		t.Fatal(err)
	}
	if run.DiffStat != "M file.go" || run.Diff == "" {
		t.Fatalf("artifact run = %+v", run)
	}
	run, err = store.CompleteRun(ctx, "demo", run.ID, "succeeded", "done", "")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "succeeded" || run.Summary != "done" || run.EndedAt == "" {
		t.Fatalf("completed run = %+v", run)
	}
	events, err := store.RunEvents(ctx, "demo", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[0].Type != "run.queued" || events[3].Type != "run.artifacts" || events[4].Type != "run.succeeded" {
		t.Fatalf("events = %+v", events)
	}
	runnable, err = store.RunnableTasks(ctx, "demo", []string{"todo"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runnable) != 1 || runnable[0].ID != todo.ID {
		t.Fatalf("completed non-active run should not block retry: %+v", runnable)
	}
	runnable, err = store.RunnableTasksWithMaxAttempts(ctx, "demo", []string{"todo"}, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runnable) != 0 {
		t.Fatalf("max attempts should cap retry: %+v", runnable)
	}
}

func TestStoreMarksExpiredRunStalledAndReleasesTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	task, err := store.CreateTask(ctx, "demo", TaskInput{Title: "Recover me", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.StartRun(ctx, "demo", task.ID, "/tmp/work", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveTask(ctx, "demo", task.ID, "in_progress", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RenewRunLease(ctx, "demo", run.ID, -time.Second); err != nil {
		t.Fatal(err)
	}
	marked, err := store.MarkExpiredRunsStalled(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if marked != 1 {
		t.Fatalf("marked = %d, want 1", marked)
	}
	gotRun, err := store.Run(ctx, "demo", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRun.Status != "stalled" || gotRun.EndedAt == "" {
		t.Fatalf("run = %+v", gotRun)
	}
	state, err := store.State(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].StatusKey != "todo" {
		t.Fatalf("task not released: %+v", state.Tasks)
	}
	runnable, err := store.RunnableTasks(ctx, "demo", []string{"todo"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runnable) != 1 || runnable[0].ID != task.ID {
		t.Fatalf("runnable = %+v", runnable)
	}
	events, err := store.RunEvents(ctx, "demo", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != "run.stalled" {
		t.Fatalf("events = %+v", events)
	}
}

func TestStoreLatestRunUsesAttemptNotCreatedAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	task, err := store.CreateTask(ctx, "demo", TaskInput{Title: "Latest run", StatusKey: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.StartRun(ctx, "demo", task.ID, "/tmp/one", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteRun(ctx, "demo", first.ID, "failed", "", "nope"); err != nil {
		t.Fatal(err)
	}
	second, err := store.StartRun(ctx, "demo", task.ID, "/tmp/two", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteRun(ctx, "demo", second.ID, "succeeded", "done", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE symphony_runs SET created_at = CASE id WHEN $1 THEN '2026-01-01T00:00:00Z' WHEN $2 THEN '2026-01-01T00:00:00.5Z' ELSE created_at END`, first.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	state, err := store.State(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tasks) != 1 || state.Tasks[0].LatestRun == nil {
		t.Fatalf("state = %+v", state)
	}
	if state.Tasks[0].LatestRun.ID != second.ID || state.Tasks[0].LatestRun.Attempt != 2 {
		t.Fatalf("latest run = %+v, want attempt 2", state.Tasks[0].LatestRun)
	}
}

func TestStoreUpdateStatuses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	state, err := store.UpdateStatuses(ctx, "demo", []StatusUpdate{
		{Key: "todo", SortOrder: 1000},
		{Key: "backlog", SortOrder: 2000, Hidden: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Statuses[0].Key != "todo" || state.Statuses[1].Key != "backlog" || !state.Statuses[1].Hidden {
		t.Fatalf("statuses = %+v", state.Statuses[:2])
	}
	if _, err := store.UpdateStatuses(ctx, "demo", []StatusUpdate{{Key: "missing"}}); err == nil {
		t.Fatal("expected unknown status error")
	}
}

func TestStoreConcurrentCreatesUseUniqueIdentifiers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	testURL, cleanup := createLiveTestDatabase(t)
	t.Cleanup(cleanup)
	a, err := Open(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := Open(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	const count = 20
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		store := a
		if i%2 == 1 {
			store = b
		}
		go func() {
			defer wg.Done()
			task, err := store.CreateTask(ctx, "demo", TaskInput{Title: "Concurrent"})
			if err != nil {
				errs <- err
				return
			}
			ids <- task.Identifier
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate identifier %q", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("created %d identifiers, want %d", len(seen), count)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	testURL, cleanup := createLiveTestDatabase(t)
	t.Cleanup(cleanup)
	store, err := Open(context.Background(), testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createLiveTestDatabase(t *testing.T) (string, func()) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SCENERY_TEST_DATABASE_URL"))
	if raw == "" {
		t.Skip("SCENERY_TEST_DATABASE_URL is not set; skipping live Postgres symphony store test")
	}
	adminURL, err := adminDatabaseURL(raw)
	if err != nil {
		t.Fatalf("parse SCENERY_TEST_DATABASE_URL: %v", err)
	}
	admin, err := postgresdb.Open(context.Background(), adminURL)
	if err != nil {
		t.Skipf("SCENERY_TEST_DATABASE_URL is not reachable for live Postgres tests: %v", err)
	}
	name, err := randomID("scenery_symphony_test")
	if err != nil {
		t.Fatalf("random database name: %v", err)
	}
	if _, err := admin.ExecContext(context.Background(), `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()
		t.Skipf("SCENERY_TEST_DATABASE_URL cannot create per-test database: %v", err)
	}
	u, _ := url.Parse(raw)
	u.Path = "/" + name
	return u.String(), func() {
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
