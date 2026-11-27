package agent

import (
	"net/http"
	"testing"
)

func TestTransportForBackendReusesUnixTransport(t *testing.T) {
	s := &Server{}
	backend := Backend{Network: "unix", Addr: "/tmp/scenery-test.sock"}

	first := s.transportForBackend(backend)
	second := s.transportForBackend(backend)
	if first == nil {
		t.Fatal("transportForBackend returned nil for unix backend")
	}
	if first != second {
		t.Fatal("unix backend produced distinct transports; per-request allocation leaks goroutines and FDs")
	}
	if _, ok := first.(*http.Transport); !ok {
		t.Fatalf("unix transport type = %T, want *http.Transport", first)
	}
	if tr := first.(*http.Transport); tr.IdleConnTimeout == 0 {
		t.Fatal("cached unix transport has no IdleConnTimeout; idle connections never reap")
	}

	// A different socket path gets its own transport.
	other := s.transportForBackend(Backend{Network: "unix", Addr: "/tmp/scenery-other.sock"})
	if other == first {
		t.Fatal("distinct socket paths shared one transport")
	}
}

func TestTransportForBackendUsesSharedBoundedTCPTransport(t *testing.T) {
	s := &Server{}
	first := s.transportForBackend(Backend{Network: "tcp", Addr: "127.0.0.1:4000"})
	second := s.transportForBackend(Backend{Network: "tcp", Addr: "127.0.0.1:4001"})
	if first != second || first != defaultLocalTCPTransport {
		t.Fatal("tcp backend should reuse the shared local TCP transport")
	}
	tr, ok := first.(*http.Transport)
	if !ok {
		t.Fatalf("tcp transport type = %T, want *http.Transport", first)
	}
	if tr.MaxConnsPerHost <= 0 || tr.MaxConnsPerHost >= 128 {
		t.Fatalf("MaxConnsPerHost = %d, want bounded below OS somaxconn=128", tr.MaxConnsPerHost)
	}
	if tr.MaxIdleConnsPerHost < 32 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want >= 32 for Vite module bursts", tr.MaxIdleConnsPerHost)
	}
}
