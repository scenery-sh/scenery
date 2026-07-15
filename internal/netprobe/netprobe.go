// Package netprobe is the single implementation of local TCP probing:
// whether a listener currently accepts connections at an address, and
// whether an address is free to bind.
package netprobe

import (
	"context"
	"net"
	"time"
)

// DialReachable reports whether a TCP listener accepts connections at addr
// within timeout. An address without a host probes loopback.
func DialReachable(addr string, timeout time.Duration) bool {
	target := addr
	if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
		target = net.JoinHostPort("127.0.0.1", port)
	}
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// BindFree returns nil when addr can be bound, meaning no listener holds it.
func BindFree(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

// WaitBindFree polls every interval until addr can be bound or ctx expires.
// On timeout it returns the most recent bind error.
func WaitBindFree(ctx context.Context, addr string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr error
	for {
		if lastErr = BindFree(addr); lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return lastErr
		case <-ticker.C:
		}
	}
}
