package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"onlava.com/internal/devdash"
)

func TestDashboardNotifyDoesNotBlockOnSlowClient(t *testing.T) {
	server := newTestDashboardServer(t)
	conn := newBlockingDashboardConn(nil)
	server.addClient(conn)

	done := make(chan struct{})
	go func() {
		server.notify(&devdash.Notification{
			Method: "trace/new",
			Params: map[string]any{"trace_id": "trace-1"},
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("notify blocked on slow websocket client")
	}

	select {
	case <-conn.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("notification write did not start")
	}
	close(conn.release)
}

func TestDashboardClientWriteJSONUsesDeadline(t *testing.T) {
	conn := newBlockingDashboardConn(nil)
	close(conn.release)
	client := &dashboardClient{conn: conn}

	if err := client.writeJSON(map[string]any{"ok": true}); err != nil {
		t.Fatalf("writeJSON() error = %v", err)
	}

	deadlines := conn.deadlines()
	if len(deadlines) != 2 {
		t.Fatalf("deadlines = %d, want 2", len(deadlines))
	}
	if deadlines[0].IsZero() {
		t.Fatal("first write deadline is zero")
	}
	if !deadlines[1].IsZero() {
		t.Fatalf("second write deadline = %v, want zero reset", deadlines[1])
	}
}

func TestDashboardBroadcastRemovesFailedClient(t *testing.T) {
	server := newTestDashboardServer(t)
	conn := newBlockingDashboardConn(errors.New("closed"))
	client := server.addClient(conn)

	close(conn.release)
	server.broadcastNotification(map[string]any{"jsonrpc": "2.0", "method": "trace/new"}, []*dashboardClient{client})

	server.mu.Lock()
	_, exists := server.clients[client]
	server.mu.Unlock()
	if exists {
		t.Fatal("failed client was not removed")
	}
	if !conn.closed() {
		t.Fatal("failed client connection was not closed")
	}
}

type blockingDashboardConn struct {
	release      chan struct{}
	writeStarted chan struct{}
	writeOnce    sync.Once
	err          error

	mu       sync.Mutex
	closedAt bool
	writes   []time.Time
}

func newBlockingDashboardConn(err error) *blockingDashboardConn {
	return &blockingDashboardConn{
		release:      make(chan struct{}),
		writeStarted: make(chan struct{}),
		err:          err,
	}
}

func (c *blockingDashboardConn) WriteJSON(any) error {
	c.writeOnce.Do(func() { close(c.writeStarted) })
	<-c.release
	return c.err
}

func (c *blockingDashboardConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closedAt = true
	return nil
}

func (c *blockingDashboardConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, deadline)
	return nil
}

func (c *blockingDashboardConn) deadlines() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.writes...)
}

func (c *blockingDashboardConn) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closedAt
}
