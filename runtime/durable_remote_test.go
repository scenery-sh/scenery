package runtime

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"

	durablestore "scenery.sh/internal/durable/store"
)

func TestDurableRemoteWorkerExecutesJobOverHTTP(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	defer setActiveDurableStores(nil)

	root := t.TempDir()
	stateRoot := filepath.Join(root, ".scenery", "state")
	path, err := durablestore.DurableDBPath(stateRoot, "maps")
	if err != nil {
		t.Fatal(err)
	}
	db, err := durablestore.Open(context.Background(), "maps", path, durablestore.Options{Synchronous: "off"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ReconcileTasks(context.Background(), []durablestore.TaskDeclaration{{Name: "maps.remote.v1", HandlerRef: "maps.remote.v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateWorkerToken(context.Background(), durablestore.WorkerTokenRequest{ID: "tok-remote", Name: "remote", Secret: "secret-token"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Start(context.Background(), durablestore.StartRequest{ID: "job-remote-worker", TaskName: "maps.remote.v1", InputBlob: []byte(`{"id":"1"}`)}); err != nil {
		t.Fatal(err)
	}
	setActiveDurableStores([]*durablestore.Store{db})

	api, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	httpServer := httptest.NewServer(api.Handler)
	defer httpServer.Close()

	t.Setenv(envDurableEndpoint, httpServer.URL)
	t.Setenv(envDurableToken, "secret-token")
	t.Setenv(envDurableServices, "maps")
	RegisterDurableTask(&DurableTask{
		Name:    "maps.remote.v1",
		Service: "maps",
		Handler: func(ctx context.Context, input []byte) ([]byte, error) {
			return []byte(`{"ok":true}`), nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := startDurableRuntime(ctx, AppConfig{Name: "demo", Role: "worker"})
	if err != nil {
		t.Fatalf("startDurableRuntime remote: %v", err)
	}
	defer func() {
		cancel()
		if err := stop(context.Background()); err != nil {
			t.Fatalf("stop durable runtime: %v", err)
		}
	}()

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	waitRuntimeJobState(t, sqlDB, "job-remote-worker", "succeeded")
}
