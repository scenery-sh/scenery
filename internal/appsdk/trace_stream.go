package appsdk

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

// ObserveReadCloser completes telemetry on EOF, read failure, close or context
// cancellation. It preserves streaming and never buffers the application body.
func ObserveReadCloser(ctx context.Context, body io.ReadCloser, end func(int64, error)) io.ReadCloser {
	r := &observedReadCloser{ReadCloser: body, end: end}
	stop := context.AfterFunc(ctx, func() { r.finish(ctx.Err()) })
	r.stop = stop
	if writer, ok := body.(io.Writer); ok {
		return &observedReadWriteCloser{observedReadCloser: r, Writer: writer}
	}
	return r
}

type observedReadCloser struct {
	io.ReadCloser
	bytes atomic.Int64
	once  sync.Once
	end   func(int64, error)
	stop  func() bool
}

func (r *observedReadCloser) finish(err error) {
	r.once.Do(func() {
		if errors.Is(err, io.EOF) {
			err = nil
		}
		r.end(r.bytes.Load(), err)
	})
}
func (r *observedReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytes.Add(int64(n))
	if err != nil {
		r.stop()
		r.finish(err)
	}
	return n, err
}
func (r *observedReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.stop()
	r.finish(err)
	return err
}

// HTTP upgrades use a bidirectional response body; retain its write capability.
type observedReadWriteCloser struct {
	*observedReadCloser
	io.Writer
}
