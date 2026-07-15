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

func TestTransportForBackendUsesSharedDefaultForTCP(t *testing.T) {
	s := &Server{}
	transport := s.transportForBackend(Backend{Network: "tcp", Addr: "127.0.0.1:4000"})
	if transport != http.DefaultTransport {
		t.Fatal("tcp backend should reuse http.DefaultTransport")
	}
}
