package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"scenery.sh/internal/appsdk"
)

type storageTraceHost struct {
	appsdk.Host
	operations []string
	sizes      []int64
	failures   []error
}

func (h *storageTraceHost) TraceStorageOperation(ctx context.Context, store, operation string) (context.Context, func(int64, error)) {
	h.operations = append(h.operations, store+"."+operation)
	return ctx, func(size int64, err error) { h.sizes = append(h.sizes, size); h.failures = append(h.failures, err) }
}

type traceStoreBackend struct{ Store }

func (traceStoreBackend) Get(context.Context, string, GetOptions) (io.ReadCloser, *Object, error) {
	return io.NopCloser(strings.NewReader("hello")), &Object{SizeBytes: 5}, nil
}
func (traceStoreBackend) Delete(context.Context, string, DeleteOptions) error {
	return context.Canceled
}
func TestStorageTraceCountsStreamAndPreservesFailure(t *testing.T) {
	previous := appsdk.CurrentHost()
	host := &storageTraceHost{}
	appsdk.RegisterHost(host)
	defer appsdk.RegisterHost(previous)
	store := &tracedStore{Store: traceStoreBackend{}, name: "files"}
	body, _, err := store.Get(context.Background(), "private/key", GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.sizes) != 0 {
		t.Fatal("stream ended at headers")
	}
	_, err = io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if err := store.Delete(context.Background(), "private/key", DeleteOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(host.sizes) != 2 || host.sizes[0] != 5 || host.operations[0] != "files.get" || host.operations[1] != "files.delete" || !errors.Is(host.failures[1], context.Canceled) {
		t.Fatalf("tracing %+v", host)
	}
}
