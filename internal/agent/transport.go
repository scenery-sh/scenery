package agent

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// UnixTransportCache reuses one *http.Transport per unix socket path so proxied
// requests share connections instead of leaking a fresh transport — its idle
// connections, their read/write goroutines, and a file descriptor — on every
// request. Call Evict when a socket's owner goes away to close idle connections
// and drop the entry, keeping the cache bounded in long-lived processes. The
// zero value is ready to use and safe for concurrent use.
type UnixTransportCache struct {
	transports sync.Map // socket path -> *http.Transport
}

// For returns the cached transport for the unix socket at addr, creating it on
// first use.
func (c *UnixTransportCache) For(addr string) *http.Transport {
	if cached, ok := c.transports.Load(addr); ok {
		return cached.(*http.Transport)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", addr)
		},
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	actual, _ := c.transports.LoadOrStore(addr, transport)
	return actual.(*http.Transport)
}

// Evict drops the transport for addr and closes its idle connections. It is a
// no-op when addr was never cached.
func (c *UnixTransportCache) Evict(addr string) {
	if cached, ok := c.transports.LoadAndDelete(addr); ok {
		cached.(*http.Transport).CloseIdleConnections()
	}
}
