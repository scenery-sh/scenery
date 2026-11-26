package shared

import (
	"context"
	"io"
)

// ByteStream is an exact-length response body whose reader ownership
// transfers to Scenery after a successful streaming handler return.
type ByteStream struct {
	Reader io.ReadCloser
	Size   int64
}

func NewByteStream(reader io.ReadCloser, size int64) ByteStream {
	return ByteStream{Reader: reader, Size: size}
}

func (stream *ByteStream) Close() error {
	if stream == nil || stream.Reader == nil {
		return nil
	}
	reader := stream.Reader
	stream.Reader = nil
	return reader.Close()
}

// Event is a single server-sent event item emitted by a stream_out operation.
type Event[T any] struct {
	Data  T
	ID    string
	Event string
}

// EventSender writes typed events to an open stream_out response.
// Send returns a non-nil error if the client disconnects or the stream ends.
type EventSender[T any] interface {
	Send(ctx context.Context, data T) error
	SendEvent(ctx context.Context, event Event[T]) error
}

// RawEventEmit writes one already-encoded canonical JSON event payload to the worker ABI transport.
type RawEventEmit = func(data []byte, id, event string) error

// RawEventProducer streams encoded events until EOF, cancellation, or error.
type RawEventProducer = func(ctx context.Context, emit RawEventEmit) error

// EventStream produces typed server-sent events after a successful stream_out handler return.
type EventStream[T any] struct {
	produce func(ctx context.Context, send EventSender[T]) error
}

// NewEventStream builds an EventStream from a producer function that pushes events to send.
func NewEventStream[T any](produce func(ctx context.Context, send EventSender[T]) error) EventStream[T] {
	return EventStream[T]{produce: produce}
}

// IsSet reports whether the EventStream carries a producer.
func (stream EventStream[T]) IsSet() bool {
	return stream.produce != nil
}

// Bind adapts a typed EventStream into a raw worker-ABI producer using encode.
func (stream EventStream[T]) Bind(encode func(T) ([]byte, error)) RawEventProducer {
	if stream.produce == nil {
		return nil
	}
	return func(ctx context.Context, emit RawEventEmit) error {
		return stream.produce(ctx, typedEventSender[T]{encode: encode, emit: emit})
	}
}

type typedEventSender[T any] struct {
	encode func(T) ([]byte, error)
	emit   RawEventEmit
}

func (sender typedEventSender[T]) Send(ctx context.Context, data T) error {
	return sender.SendEvent(ctx, Event[T]{Data: data})
}

func (sender typedEventSender[T]) SendEvent(ctx context.Context, event Event[T]) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := sender.encode(event.Data)
	if err != nil {
		return err
	}
	return sender.emit(encoded, event.ID, event.Event)
}
