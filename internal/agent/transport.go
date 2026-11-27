package agent

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// NewLocalTCPTransport returns a pooled loopback transport sized for local
// development proxies (including unbundled Vite module bursts over HTTP/2).
// Bounding MaxConnsPerHost below the default OS listen backlog (somaxconn=128
// on macOS) queues excess requests cleanly instead of overflowing loopback
// accept queues and turning burst traffic into spurious 503s.
func NewLocalTCPTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   64,
		MaxConnsPerHost:       64,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

var defaultLocalTCPTransport = NewLocalTCPTransport()

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
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		MaxConnsPerHost:       64,
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
