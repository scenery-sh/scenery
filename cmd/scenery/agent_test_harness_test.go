package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
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
