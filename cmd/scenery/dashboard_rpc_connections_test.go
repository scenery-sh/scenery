package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"scenery.sh/internal/devdash"
)

func TestRuntimeRPCBackendCloseCancelsEveryCallBeforeReturning(t *testing.T) {
	t.Parallel()

	database := newRuntimeTestDatabase()
	server := newRuntimeRPCTestServer(database)
	url, returned := startRuntimeRPCBackend(t, context.Background(), server)
	slow := map[string]any{"app_id": "session-a", "query": "slow"}
	first, second := dialRuntimeRPC(t, url), dialRuntimeRPC(t, url)
	first.send(1, "db/query", slow)
	first.send(2, "db/query", slow)
	second.send(1, "db/query", slow)
	database.awaitStarted(t, 3)

	// net/http leaves hijacked connections open: without the backend's own
	// tracking, these statements would run until their clients left.
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	// Close returns only after every handler has finished its calls, so their
	// statements, databases and slots are already gone.
	active, connections := database.counts()
	if active != 0 || connections != 0 || database.canceledCount() != 3 || len(runtimeActiveWork(server.rpc)) != 0 {
		t.Fatalf("after Close: %d statements, %d open connections, %d canceled, app work %v", active, connections, database.canceledCount(), runtimeActiveWork(server.rpc))
	}
	awaitRuntimeHandlers(t, returned, 2)
	first.expectGoingAway()
	second.expectGoingAway()

	// A connection upgraded after the backend closed never serves a call.
	lateURL, lateReturned := serveRuntimeRPC(t, server)
	dialRuntimeRPC(t, lateURL).expectGoingAway()
	awaitRuntimeHandlers(t, lateReturned, 1)
}

func TestRuntimeRPCBackendShutdownClosesItsConnections(t *testing.T) {
	t.Parallel()

	database := newRuntimeTestDatabase()
	server := newRuntimeRPCTestServer(database)
	ctx, cancel := context.WithCancel(context.Background())
	url, returned := startRuntimeRPCBackend(t, ctx, server)
	client := dialRuntimeRPC(t, url)
	client.send(1, "db/query", map[string]any{"app_id": "session-a", "query": "slow"})
	database.awaitStarted(t, 1)

	// The listener shuts its server down when its context ends; the shutdown
	// hook closes the connection, which cancels the statement.
	cancel()
	database.awaitCanceled(t, 1)
	awaitRuntimeHandlers(t, returned, 1)
	client.expectGoingAway()
}

// startRuntimeRPCBackend serves the backend's own HTTP server over loopback,
// as scenery up does, and reports every handler return.
func startRuntimeRPCBackend(t *testing.T, ctx context.Context, server *dashboardServer) (string, <-chan struct{}) {
	t.Helper()
	returned := make(chan struct{}, 8)
	mux := server.http.Handler
	server.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() { returned <- struct{}{} }()
		mux.ServeHTTP(w, req)
	})
	server.state.cacheRoot = t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.startListener(ctx, listener); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return "ws://" + server.addr + devdash.WebSocketPath, returned
}

func (c *runtimeTestClient) expectGoingAway() {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := c.conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		c.t.Fatalf("read after the runtime closed = %v, want close 1001 (going away)", err)
	}
}
