package appsdk

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type duplexTraceBody struct{ bytes.Buffer }

func (*duplexTraceBody) Close() error { return nil }
func TestObservedStreamPreservesUpgradeWritesAndCompletesOnce(t *testing.T) {
	body := &duplexTraceBody{}
	var calls int
	var size int64
	wrapped := ObserveReadCloser(context.Background(), body, func(n int64, err error) {
		calls++
		size = n
		if err != nil {
			t.Error(err)
		}
	})
	duplex, ok := wrapped.(io.ReadWriteCloser)
	if !ok {
		t.Fatal("upgrade body lost writer")
	}
	_, _ = duplex.Write([]byte("hello"))
	if _, err := io.ReadAll(duplex); err != nil {
		t.Fatal(err)
	}
	_ = duplex.Close()
	if calls != 1 || size != 5 {
		t.Fatalf("completion calls %d, bytes %d", calls, size)
	}
}
func TestObservedStreamCompletesOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	body := ObserveReadCloser(ctx, io.NopCloser(bytes.NewReader(nil)), func(_ int64, err error) { done <- err })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
	_ = body.Close()
	select {
	case <-done:
		t.Fatal("duplicate completion")
	default:
	}
}
