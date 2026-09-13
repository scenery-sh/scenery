package appsdk

import (
	"context"
	"errors"
	"testing"

	"scenery.sh/runtime/shared"
)

type testSpanStarter struct {
	starts int
	ended  []error
}

func (s *testSpanStarter) StartApplicationSpan(ctx context.Context, _ string) (context.Context, func(error)) {
	s.starts++
	return ctx, func(err error) { s.ended = append(s.ended, err) }
}

func TestInvocationProvidesDefensiveRequestAndIdempotentSpan(t *testing.T) {
	request := shared.Request{Type: shared.APICall, Service: "maps", Endpoint: "earth"}
	starter := &testSpanStarter{}
	restore := EnterInvocation(&request, starter)
	defer restore()

	got := CurrentRequest()
	got.Service = "mutated"
	if CurrentRequest().Service != "maps" {
		t.Fatal("CurrentRequest exposed mutable runtime request state")
	}
	want := errors.New("failed")
	ctx, span := StartSpan(context.Background(), "render")
	if ctx == nil || span == nil || starter.starts != 1 {
		t.Fatalf("StartSpan did not use the current invocation: ctx=%v span=%v starts=%d", ctx, span, starter.starts)
	}
	span.End(want)
	span.End(nil)
	if len(starter.ended) != 1 || !errors.Is(starter.ended[0], want) {
		t.Fatalf("Span.End calls = %v, want one failure", starter.ended)
	}
}

func TestNestedInvocationRestoresCurrentRequest(t *testing.T) {
	outer := shared.Request{Service: "outer"}
	inner := shared.Request{Service: "inner"}
	restoreOuter := EnterInvocation(&outer, nil)
	defer restoreOuter()
	restoreInner := EnterInvocation(&inner, nil)
	if CurrentRequest().Service != "inner" {
		t.Fatal("nested invocation was not current")
	}
	restoreInner()
	if CurrentRequest().Service != "outer" {
		t.Fatal("nested invocation did not restore its predecessor")
	}
}
