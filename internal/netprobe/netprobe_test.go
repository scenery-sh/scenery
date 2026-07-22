package netprobe

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestDialReachable(t *testing.T) {
	ln := listenLocal(t)
	addr := ln.Addr().String()
	if !DialReachable(addr, time.Second) {
		t.Fatalf("DialReachable(%q) = false, want true", addr)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if DialReachable(addr, 50*time.Millisecond) {
		t.Fatalf("DialReachable(%q) = true after listener closed, want false", addr)
	}
}

func TestBindFree(t *testing.T) {
	ln := listenLocal(t)
	addr := ln.Addr().String()
	if err := BindFree(addr); err == nil {
		t.Fatalf("BindFree(%q) error = nil while occupied", addr)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := BindFree(addr); err != nil {
		t.Fatalf("BindFree(%q) after release error = %v", addr, err)
	}
}

func TestWaitBindFreeWaitsUntilReleased(t *testing.T) {
	ln := listenLocal(t)
	addr := ln.Addr().String()
	released := make(chan struct{})
	go func() {
		time.Sleep(25 * time.Millisecond)
		_ = ln.Close()
		close(released)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := WaitBindFree(ctx, addr, 5*time.Millisecond); err != nil {
		t.Fatalf("WaitBindFree(%q) error = %v", addr, err)
	}
	<-released
}

func TestWaitBindFreeContextTimeout(t *testing.T) {
	ln := listenLocal(t)
	defer ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := WaitBindFree(ctx, ln.Addr().String(), 5*time.Millisecond)
	if err == nil {
		t.Fatal("WaitBindFree() error = nil, want occupied-address error")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("context error = %v, want %v", ctx.Err(), context.DeadlineExceeded)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("WaitBindFree() returned after %v, want prompt context timeout", elapsed)
	}
}

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}
