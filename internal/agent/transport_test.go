package agent

import (
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
)

func TestUnixTransportCacheReusesAndEvicts(t *testing.T) {
	var cache UnixTransportCache
	addr := "/tmp/scenery-cache-test.sock"

	first := cache.For(addr)
	second := cache.For(addr)
	if first == nil {
		t.Fatal("For returned nil transport")
	}
	if first != second {
		t.Fatal("For returned distinct transports for the same socket; connections would not be reused")
	}
	if first.IdleConnTimeout == 0 {
		t.Fatal("cached transport has no IdleConnTimeout; idle connections never reap")
	}

	// After eviction the next lookup builds a fresh transport, so a dead
	// session's entry does not linger in the cache forever.
	cache.Evict(addr)
	third := cache.For(addr)
	if third == first {
		t.Fatal("Evict did not drop the cached transport")
	}
	cache.Evict("/tmp/never-cached.sock") // no-op, must not panic
}

func TestIsBackendUnavailableErrorMatchesReusedDeadConn(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dial", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"refused", syscall.ECONNREFUSED, true},
		{"missing socket", syscall.ENOENT, true},
		{"reset reused conn", &net.OpError{Op: "write", Err: syscall.ECONNRESET}, true},
		{"broken pipe", &net.OpError{Op: "write", Err: syscall.EPIPE}, true},
		{"eof from closed conn", io.EOF, true},
		{"unrelated", errors.New("boom"), false},
	}
	for _, tc := range cases {
		if got := isBackendUnavailableError(tc.err); got != tc.want {
			t.Errorf("%s: isBackendUnavailableError = %v, want %v", tc.name, got, tc.want)
		}
	}
}
