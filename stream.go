package scenery

import (
	"context"
	"io"

	"scenery.sh/runtime/shared"
)

// ByteStream is an exact-length HTTP response body. A successful streaming
// handler transfers ownership of Reader to Scenery, which always closes it.
type ByteStream = shared.ByteStream

func NewByteStream(reader io.ReadCloser, size int64) ByteStream {
	return shared.NewByteStream(reader, size)
}

// Event is a single typed server-sent event item emitted by a stream_out operation.
type Event[T any] = shared.Event[T]

// EventSender writes typed events to an open stream_out response.
type EventSender[T any] = shared.EventSender[T]

// EventStream produces typed server-sent events for a stream_out HTTP binding.
type EventStream[T any] = shared.EventStream[T]

// NewEventStream constructs an EventStream from a producer function.
func NewEventStream[T any](produce func(ctx context.Context, send EventSender[T]) error) EventStream[T] {
	return shared.NewEventStream(produce)
}
