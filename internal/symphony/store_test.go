package symphony

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestStorePersistsBoardCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	task, err := store.CreateTask(ctx, "demo", TaskInput{
		Title:       "Wire Symphony board",
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
		Title:       "Wire Symphony board",
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
	path := filepath.Join(t.TempDir(), "symphony.sqlite")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := Open(ctx, path)
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
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "symphony.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
