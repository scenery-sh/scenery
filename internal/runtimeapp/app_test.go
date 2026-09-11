package runtimeapp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"scenery.sh/runtime/shared"
)

func TestStandaloneApplicationHasNoRequestOrTracingWork(t *testing.T) {
	meta := Meta()
	meta.AppID = "caller-owned-copy"
	if Meta().AppID != "" || CurrentRequest().Type != shared.None {
		t.Fatal("standalone app acquired runtime state")
	}
	ctx, span := StartSpan(context.Background(), "outside-runtime")
	if ctx == nil || span == nil {
		t.Fatal("nil context or span")
	}
	span.End(errors.New("no reporter"))
}

func TestApplicationBindingRejectsIncompleteAndDuplicateOwners(t *testing.T) {
	var owner binding
	mustPanic := func(call func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Error("invalid binding was accepted")
			}
		}()
		call()
	}
	mustPanic(func() { owner.bind(Provider{}) })
	provider := Provider{
		Meta:            func() *shared.AppMetadata { return &shared.AppMetadata{} },
		CurrentRequest:  func() *shared.Request { return &shared.Request{} },
		StartSpan:       func(ctx context.Context, _ string) (context.Context, *Span) { return ctx, &Span{} },
		CurrentAuth:     func() *AuthInfo { return nil },
		WithAuthContext: func(ctx context.Context, _ AuthInfo) context.Context { return ctx },
	}
	owner.bind(provider)
	mustPanic(func() { owner.bind(provider) })
}

func TestSpanConcurrentEndKeepsOneNativeOutcome(t *testing.T) {
	want := errors.New("native failure")
	var calls atomic.Int32
	span := NewSpan(func(err error) {
		if err != want { //nolint:errorlint // The native callback must receive the identical error, without wrapping.
			t.Error("native error identity changed")
		}
		calls.Add(1)
	})
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() { span.End(want) })
	}
	group.Wait()
	span.End(errors.New("late outcome"))
	if calls.Load() != 1 {
		t.Fatalf("completion calls = %d", calls.Load())
	}
	var absent *Span
	absent.End(nil)
	new(Span).End(nil)
}
