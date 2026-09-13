// Package appsdk owns the lightweight process-local state used by the public
// scenery.sh facade. The full runtime supplies request/span behavior through a
// context-scoped bridge; importing the SDK does not import runtime orchestration.
package appsdk

import (
	"context"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"

	"scenery.sh/runtime/shared"
)

// Span is an application-owned child span. End is safe to call more than once.
type Span struct {
	once sync.Once
	end  func(error)
}

func (s *Span) End(err error) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.end != nil {
			s.end(err)
		}
	})
}

// SpanStarter is the narrow runtime bridge needed by application-owned spans.
// Its value is bound to the request context/current invocation, never installed
// through a mutable process-wide callback registry.
type SpanStarter interface {
	StartApplicationSpan(context.Context, string) (context.Context, func(error))
}

type invocation struct {
	request *shared.Request
	spans   SpanStarter
}

type invocationKey struct{}

var currentInvocations sync.Map

func WithInvocation(ctx context.Context, request *shared.Request, spans SpanStarter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationKey{}, invocation{request: request, spans: spans})
}

// EnterInvocation supplies CurrentRequest and the context-free StartSpan
// fallback only for the calling runtime goroutine. The returned restoration
// function preserves nested dispatch.
func EnterInvocation(request *shared.Request, spans SpanStarter) func() {
	id := goroutineID()
	previous, hadPrevious := currentInvocations.Load(id)
	currentInvocations.Store(id, invocation{request: request, spans: spans})
	return func() {
		if hadPrevious {
			currentInvocations.Store(id, previous)
			return
		}
		currentInvocations.Delete(id)
	}
}

func CurrentRequest() *shared.Request {
	current, ok := currentInvocations.Load(goroutineID())
	if !ok || current.(invocation).request == nil {
		return &shared.Request{Type: shared.None}
	}
	request := *current.(invocation).request
	return &request
}

func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	current, _ := ctx.Value(invocationKey{}).(invocation)
	if current.spans == nil {
		if stored, ok := currentInvocations.Load(goroutineID()); ok {
			current = stored.(invocation)
		}
	}
	if current.spans == nil {
		return ctx, &Span{}
	}
	child, end := current.spans.StartApplicationSpan(ctx, name)
	if child == nil {
		child = ctx
	}
	return child, &Span{end: end}
}

func goroutineID() uint64 {
	var buf [64]byte
	n := goruntime.Stack(buf[:], false)
	line := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	space := strings.IndexByte(line, ' ')
	if space < 0 {
		return 0
	}
	id, _ := strconv.ParseUint(line[:space], 10, 64)
	return id
}
