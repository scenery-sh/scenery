package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"sync/atomic"
	"testing"

	"scenery.sh/internal/devdash"
)

func TestAssistantStartPreparedBoundsStartsAndSerializesCallbacks(t *testing.T) {
	s, original, _ := assistantStageFixture(t)
	result := nextAssistantStageResult(original)
	resource := result.Manifest.Resources[0]
	result.Manifest.Resources = nil
	for _, name := range []string{"one", "two", "three"} {
		next := resource
		next.Address, next.Name = "app/assistant/"+name, name
		result.Manifest.Resources = append(result.Manifest.Resources, next)
	}
	if err := s.Prepare(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	var callbackActive, callbackCalls atomic.Int32
	var callbackOverlap atomic.Bool
	callback := func() {
		if callbackActive.Add(1) != 1 {
			callbackOverlap.Store(true)
		}
		runtime.Gosched()
		callbackCalls.Add(1)
		callbackActive.Add(-1)
	}
	s.config.OnStatus = func([]AssistantStatusRecord) { callback() }
	s.config.OnEvent = func(context.Context, devdash.DevSource, string, string, map[string]any) { callback() }
	var output bytes.Buffer
	s.config.Output, s.config.ErrOutput = &output, &output
	entered := make(chan string, 3)
	release := make(chan struct{}, 3)
	s.config.ProcessFactory = func(_ context.Context, request devProcessStartRequest) (*devManagedProcess, error) {
		entered <- request.Name
		<-release
		_, _ = io.WriteString(request.Stdout, "out")
		_, _ = io.WriteString(request.Stderr, "err")
		return nil, errors.New("independent helper outage")
	}
	done := make(chan error, 1)
	go func() {
		s.lifecycle.Lock()
		defer s.lifecycle.Unlock()
		done <- s.StartPrepared(context.Background())
	}()
	first, second := <-entered, <-entered
	if first == second {
		t.Error("concurrent starts selected the same assistant address")
	}
	select {
	case <-entered:
		t.Error("a third helper started before a worker became available")
	default:
	}
	release <- struct{}{}
	third := <-entered
	if third == first || third == second {
		t.Error("a helper address was started twice")
	}
	select {
	case <-done:
		t.Error("StartPrepared returned before all handshakes completed")
	default:
	}
	release <- struct{}{}
	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("independent helper outage aborted the app: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if callbackOverlap.Load() || callbackCalls.Load() == 0 || output.Len() != 18 {
		t.Fatalf("callback/output delivery: overlap=%t calls=%d output=%q", callbackOverlap.Load(), callbackCalls.Load(), output.String())
	}
	for _, status := range s.Status() {
		if status.Ready || status.State != "unavailable" {
			t.Fatalf("failed helper status = %+v", status)
		}
	}
}
