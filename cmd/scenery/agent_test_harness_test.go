package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/workerplan"
)

func TestSQLSupplyCommandsIncludeTestAndWorktree(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"test", "worktree"} {
		if !sqlSupplyCommands[cmd] {
			t.Fatalf("sqlSupplyCommands[%q] = false, want true", cmd)
		}
	}
}

func TestWithTestStorageEnvAllocatesBoundNamespace(t *testing.T) {
	t.Parallel()
	appRoot := t.TempDir()
	storageDir := t.TempDir()
	cfg := appcfg.Config{
		Name: "filesapp",
		Storage: appcfg.StorageConfig{
			Stores: map[string]appcfg.StorageStoreConfig{
				"uploads": {Kind: "local", Access: "read_write"},
			},
		},
	}
	var bundle workerBundle
	bundle.Host.Name = "filesapp"
	bundle.Host.Workers = []workerplan.WorkerServicePlan{{Stores: map[string]string{"uploads": "uploads", "cache": "cache"}}}

	env, err := withTestStorageEnv(context.Background(), nil, appRoot, cfg, bundle, storageDir)
	if err != nil {
		t.Fatalf("withTestStorageEnv: %v", err)
	}
	raw, ok := storageRuntimeConfigValue(env)
	if !ok {
		t.Fatalf("missing %s in env: %v", storageconfig.RuntimeConfigEnv, env)
	}
	loaded, ok, err := storageconfig.LoadRuntimeConfigValue(raw)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeConfigValue: ok=%t err=%v", ok, err)
	}
	if loaded.Namespace == nil {
		t.Fatalf("expected allocated storage namespace in runtime config: %+v", loaded)
	}
	handle, err := loaded.Namespace.Handle()
	if err != nil {
		t.Fatalf("Namespace.Handle: %v", err)
	}
	if err := handle.CheckReady(context.Background()); err != nil {
		t.Fatalf("CheckReady: %v", err)
	}
	for _, storeName := range []string{"uploads", "cache"} {
		store, exists := loaded.Stores[storeName]
		if !exists || store.Root != loaded.Namespace.Root {
			t.Fatalf("store %q = %+v, want root %q", storeName, store, loaded.Namespace.Root)
		}
	}
}

func TestTemplateDatabaseSignatureTracksDatabaseApplySources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".scenery.json", `{"name":"schemaapp","database":{"apply":{"command":"go run ./cmd/schema"}}}`)
	write("cmd/schema/main.go", "package main\nconst DDL = `CREATE TABLE items (id text);`\n")

	cfg := appcfg.Config{
		Name: "schemaapp",
		Database: appcfg.DatabaseConfig{
			Apply: appcfg.DatabaseApplyConfig{Command: "go run ./cmd/schema"},
		},
	}
	bindings := []compiler.SQLBinding{{Name: "billing", Schema: "billing"}}
	setup1, _, err := buildDevDatabaseSetup(root, cfg, &compiler.Result{})
	if err != nil {
		t.Fatalf("buildDevDatabaseSetup: %v", err)
	}
	sig1, err := templateDatabaseSignature(root, cfg, setup1, bindings)
	if err != nil {
		t.Fatalf("templateDatabaseSignature 1: %v", err)
	}

	write("cmd/schema/main.go", "package main\nconst DDL = `CREATE TABLE items (id text, note text);`\n")
	sig2, err := templateDatabaseSignature(root, cfg, setup1, bindings)
	if err != nil {
		t.Fatalf("templateDatabaseSignature 2: %v", err)
	}
	if sig1 == sig2 {
		t.Fatalf("template signature did not change after editing cmd/schema/main.go: %q", sig1)
	}
}

func TestAcquireManagedPostgresHarnessCoordinatesConcurrentHoldersAndLiveOwner(t *testing.T) {
	resolver, docker := testWorktreePostgresResolver(t)
	ctx := context.Background()

	// Two concurrent harnesses share the started server; releasing the first must
	// keep it running until the second releases.
	_, _, release1, err := acquireManagedPostgresHarnessWithResolver(ctx, resolver)
	if err != nil {
		t.Fatalf("acquire 1: %v", err)
	}
	_, _, release2, err := acquireManagedPostgresHarnessWithResolver(ctx, resolver)
	if err != nil {
		t.Fatalf("acquire 2: %v", err)
	}
	if !docker.container.Running {
		t.Fatal("expected container running while harnesses are active")
	}
	if err := release1(); err != nil {
		t.Fatalf("release 1: %v", err)
	}
	if !docker.container.Running {
		t.Fatal("first harness stopped PostgreSQL while second harness was still active")
	}
	if err := release2(); err != nil {
		t.Fatalf("release 2: %v", err)
	}
	if docker.container.Running {
		t.Fatal("expected last harness to stop PostgreSQL when no live owner exists")
	}

	// If a live worktree owner (e.g. scenery up) starts while a harness holds a
	// lease, releasing the harness must leave the server running for the live owner.
	_, _, release3, err := acquireManagedPostgresHarnessWithResolver(ctx, resolver)
	if err != nil {
		t.Fatalf("acquire 3: %v", err)
	}
	live, err := resolver.paths.AcquireLiveLock()
	if err != nil {
		t.Fatalf("AcquireLiveLock: %v", err)
	}
	defer func() { _ = live.Release() }()
	if err := release3(); err != nil {
		t.Fatalf("release 3: %v", err)
	}
	if !docker.container.Running {
		t.Fatal("harness stopped PostgreSQL while worktree live lock was held")
	}
}

func TestTestDatabaseHarnessPoolsAndFastResetsEmptyTemplate(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	cloned := map[string]bool{}
	dropped := map[string]bool{}
	var clones, resets, inspects atomic.Int64

	h := &testDatabaseHarness{
		adminURL:     "postgres://scenery:test@127.0.0.1:5432/postgres?sslmode=disable",
		templateName: "scenery_tpl_empty",
		bindings:     []compiler.SQLBinding{{Name: "orders", Schema: "orders"}},
		maxTemplates: 4,
		cloneFn: func(_ context.Context, target, _ string) (time.Duration, error) {
			clones.Add(1)
			mu.Lock()
			cloned[target] = true
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			return 2 * time.Millisecond, nil
		},
		dropFn: func(_ context.Context, name string) error {
			mu.Lock()
			dropped[name] = true
			mu.Unlock()
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (*postgresdb.ResettableDatabaseState, error) {
			inspects.Add(1)
			return &postgresdb.ResettableDatabaseState{
				Resettable:        true,
				SchemaFingerprint: "fp-v1",
				Tables:            []string{`"orders"."items"`},
			}, nil
		},
		resetFn: func(_ context.Context, _ string, _ *postgresdb.ResettableDatabaseState) error {
			resets.Add(1)
			return nil
		},
	}

	const workers = 4
	const totalModules = 16
	var next atomic.Int64
	var reusedCount atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				idx := int(next.Add(1)) - 1
				if idx >= totalModules {
					return
				}
				summary, env, cleanup, err := h.branchForModule(context.Background(), fmt.Sprintf("mod-%d", idx))
				if err != nil {
					t.Errorf("branchForModule(%d): %v", idx, err)
					return
				}
				if len(env) == 0 {
					t.Errorf("module %d got empty db env", idx)
				}
				if summary.Reused {
					reusedCount.Add(1)
				}
				time.Sleep(2 * time.Millisecond)
				cleanup()
			}
		})
	}
	wg.Wait()

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if inspects.Load() != 1 {
		t.Fatalf("inspect count = %d, want 1", inspects.Load())
	}
	if resets.Load() == 0 || reusedCount.Load() != resets.Load() {
		t.Fatalf("expected pooled fast resets, got resets=%d reused=%d clones=%d", resets.Load(), reusedCount.Load(), clones.Load())
	}
	if clones.Load() > int64(workers+h.maxTemplates) {
		t.Fatalf("too many clones %d for %d workers across %d modules", clones.Load(), workers, totalModules)
	}
	mu.Lock()
	defer mu.Unlock()
	if dropped[h.templateName] {
		t.Fatalf("Close must never drop source template %q", h.templateName)
	}
	for name := range cloned {
		if !dropped[name] {
			t.Fatalf("cloned database %q was not dropped on Close", name)
		}
	}
}

func TestTestDatabaseHarnessSeededTemplateExpandsTemplatesInParallel(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	activeBySource := map[string]int{}
	activeTotal := 0
	maxConcurrent := 0
	cloned := map[string]bool{}
	dropped := map[string]bool{}
	var resets atomic.Int64

	h := &testDatabaseHarness{
		adminURL:     "postgres://scenery:test@127.0.0.1:5432/postgres?sslmode=disable",
		templateName: "scenery_tpl_seeded",
		bindings:     []compiler.SQLBinding{{Name: "orders", Schema: "orders"}},
		maxTemplates: 4,
		cloneFn: func(_ context.Context, target, src string) (time.Duration, error) {
			mu.Lock()
			activeBySource[src]++
			if activeBySource[src] > 1 {
				mu.Unlock()
				return 0, fmt.Errorf("concurrent clone on same source template %s", src)
			}
			activeTotal++
			if activeTotal > maxConcurrent {
				maxConcurrent = activeTotal
			}
			cloned[target] = true
			mu.Unlock()

			time.Sleep(15 * time.Millisecond)

			mu.Lock()
			activeBySource[src]--
			activeTotal--
			mu.Unlock()
			return 15 * time.Millisecond, nil
		},
		dropFn: func(_ context.Context, name string) error {
			mu.Lock()
			dropped[name] = true
			mu.Unlock()
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (*postgresdb.ResettableDatabaseState, error) {
			return &postgresdb.ResettableDatabaseState{Resettable: false}, nil
		},
		resetFn: func(_ context.Context, _ string, _ *postgresdb.ResettableDatabaseState) error {
			resets.Add(1)
			return nil
		},
	}

	const workers = 4
	const totalModules = 10
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				idx := int(next.Add(1)) - 1
				if idx >= totalModules {
					return
				}
				summary, _, cleanup, err := h.branchForModule(context.Background(), fmt.Sprintf("mod-%d", idx))
				if err != nil {
					t.Errorf("branchForModule(%d): %v", idx, err)
					return
				}
				if summary.Reused {
					t.Errorf("seeded template must never reuse DBs, got Reused=true for %s", summary.Name)
				}
				cleanup()
			}
		})
	}
	wg.Wait()

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if resets.Load() != 0 {
		t.Fatalf("resetFn called %d times on seeded template, want 0", resets.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if maxConcurrent < 2 {
		t.Fatalf("expected parallel template cloning (maxConcurrent >= 2), got %d", maxConcurrent)
	}
	if dropped[h.templateName] {
		t.Fatalf("Close must never drop source template %q", h.templateName)
	}
	for name := range cloned {
		if !dropped[name] {
			t.Fatalf("database %q was not dropped", name)
		}
	}
}

func TestTestDatabaseHarnessFallsBackToFreshCloneWhenResetFails(t *testing.T) {
	t.Parallel()
	var dropped []string
	var clones atomic.Int64
	failNextReset := true

	h := &testDatabaseHarness{
		adminURL:     "postgres://scenery:test@127.0.0.1:5432/postgres?sslmode=disable",
		templateName: "scenery_tpl_fallback",
		bindings:     []compiler.SQLBinding{{Name: "orders", Schema: "orders"}},
		cloneFn: func(_ context.Context, _, _ string) (time.Duration, error) {
			clones.Add(1)
			return time.Millisecond, nil
		},
		dropFn: func(_ context.Context, name string) error {
			dropped = append(dropped, name)
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (*postgresdb.ResettableDatabaseState, error) {
			return &postgresdb.ResettableDatabaseState{Resettable: true, SchemaFingerprint: "fp"}, nil
		},
		resetFn: func(_ context.Context, _ string, _ *postgresdb.ResettableDatabaseState) error {
			if failNextReset {
				failNextReset = false
				return postgresdb.ErrSchemaChanged
			}
			return nil
		},
	}

	s1, _, c1, err := h.branchForModule(context.Background(), "mod-a")
	if err != nil {
		t.Fatalf("branch 1: %v", err)
	}
	c1()

	s2, _, c2, err := h.branchForModule(context.Background(), "mod-b")
	if err != nil {
		t.Fatalf("branch 2: %v", err)
	}
	if s2.Reused || s2.Name == s1.Name {
		t.Fatalf("expected fresh clone after ErrSchemaChanged, got %+v (prev %s)", s2, s1.Name)
	}
	c2()

	s3, _, c3, err := h.branchForModule(context.Background(), "mod-c")
	if err != nil {
		t.Fatalf("branch 3: %v", err)
	}
	if !s3.Reused || s3.Name != s2.Name {
		t.Fatalf("expected reuse of healthy pooled DB %s, got %+v", s2.Name, s3)
	}
	c3()

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if clones.Load() != 2 {
		t.Fatalf("clones = %d, want 2", clones.Load())
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped = %v, want 2 drops (tainted + final pooled)", dropped)
	}
}

func TestTestDatabaseHarnessSingleModuleDoesNotExpandTemplates(t *testing.T) {
	t.Parallel()
	var clones atomic.Int64
	h := &testDatabaseHarness{
		adminURL:     "postgres://scenery:test@127.0.0.1:5432/postgres?sslmode=disable",
		templateName: "scenery_tpl_single",
		bindings:     []compiler.SQLBinding{{Name: "orders", Schema: "orders"}},
		cloneFn: func(_ context.Context, _, _ string) (time.Duration, error) {
			clones.Add(1)
			return time.Millisecond, nil
		},
		dropFn: func(_ context.Context, _ string) error { return nil },
		inspectFn: func(_ context.Context, _ string) (*postgresdb.ResettableDatabaseState, error) {
			return &postgresdb.ResettableDatabaseState{Resettable: false}, nil
		},
	}
	_, _, cleanup, err := h.branchForModule(context.Background(), "solo")
	if err != nil {
		t.Fatalf("branchForModule: %v", err)
	}
	cleanup()
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if clones.Load() != 1 {
		t.Fatalf("single module clones = %d, want 1 (no extra templates)", clones.Load())
	}
}

func TestTestDatabaseHarnessCleanupAfterCloseDropsImmediately(t *testing.T) {
	t.Parallel()
	var dropped []string
	var mu sync.Mutex
	h := &testDatabaseHarness{
		adminURL:     "postgres://scenery:test@127.0.0.1:5432/postgres?sslmode=disable",
		templateName: "scenery_tpl_close_race",
		bindings:     []compiler.SQLBinding{{Name: "orders", Schema: "orders"}},
		cloneFn:      func(_ context.Context, _, _ string) (time.Duration, error) { return time.Millisecond, nil },
		dropFn: func(_ context.Context, name string) error {
			mu.Lock()
			dropped = append(dropped, name)
			mu.Unlock()
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (*postgresdb.ResettableDatabaseState, error) {
			return &postgresdb.ResettableDatabaseState{Resettable: true, SchemaFingerprint: "fp"}, nil
		},
	}
	s, _, cleanup, err := h.branchForModule(context.Background(), "late")
	if err != nil {
		t.Fatalf("branchForModule: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	cleanup()
	cleanup() // verify sync.Once idempotency
	mu.Lock()
	defer mu.Unlock()
	if len(dropped) != 1 || dropped[0] != s.Name {
		t.Fatalf("dropped = %v, want [%s]", dropped, s.Name)
	}
}
