package main

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// runtimeConnections are one backend's live development runtime RPC
// connections. net/http neither closes nor waits for hijacked connections such
// as these WebSockets, so the backend closes them itself when it closes or
// shuts down: each handler's read fails, and the handler cancels its calls and
// returns once they have.
type runtimeConnections struct {
	mu       sync.Mutex
	closed   bool
	live     map[*websocket.Conn]struct{}
	handlers sync.WaitGroup
}

// add registers the connection a handler is about to serve. It refuses once
// the backend has closed, so a connection upgraded while the backend closes
// never serves a call.
func (r *runtimeConnections) add(conn *websocket.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	if r.live == nil {
		r.live = map[*websocket.Conn]struct{}{}
	}
	r.live[conn] = struct{}{}
	r.handlers.Add(1)
	return true
}

// remove unregisters a connection after its handler has finished its calls.
func (r *runtimeConnections) remove(conn *websocket.Conn) {
	r.mu.Lock()
	delete(r.live, conn)
	r.mu.Unlock()
	r.handlers.Done()
}

// closeAll refuses later connections and closes every live one. As the
// server's shutdown hook it does not wait for their handlers.
func (r *runtimeConnections) closeAll() {
	r.mu.Lock()
	r.closed = true
	live := r.live
	r.live = nil
	r.mu.Unlock()
	var closing sync.WaitGroup
	for conn := range live {
		closing.Go(func() { closeRuntimeConnection(conn) })
	}
	closing.Wait()
}

// wait returns once every registered handler has returned.
func (r *runtimeConnections) wait() {
	r.handlers.Wait()
}

// closeRuntimeConnection tells the client that the runtime is going away
// (1001), within the write timeout, and closes the connection.
func closeRuntimeConnection(conn *websocket.Conn) {
	goingAway := websocket.FormatCloseMessage(websocket.CloseGoingAway, "development runtime closed")
	_ = conn.WriteControl(websocket.CloseMessage, goingAway, time.Now().Add(dashboardClientWriteTimeout))
	_ = conn.Close()
}
