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
	if _, err := store.UpdateWorkflow(ctx, "demo", WorkflowInput{WorkflowMarkdown: "manual only", Mode: "manual", MaxConcurrency: 2}); err != nil {
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
	if len(demo.Tasks) != 1 || demo.Workflow.MaxConcurrency != 2 {
		t.Fatalf("demo state = %+v", demo)
	}
	if demo.Tasks[0].Labels == nil {
		t.Fatalf("expected labels to encode as empty array, got nil: %+v", demo.Tasks[0])
	}
	if len(other.Tasks) != 0 || other.Workflow.MaxConcurrency != 1 {
		t.Fatalf("other state = %+v", other)
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
