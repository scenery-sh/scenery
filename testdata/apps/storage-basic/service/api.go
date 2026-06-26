package service

import (
	"context"
	"io"
	"strings"

	"scenery.sh/storage"
)

//scenery:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

type ObjectSummary struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
	Body      string `json:"body,omitempty"`
}

//scenery:api private path=/storage/probe method=POST
func (*Service) StorageProbe(ctx context.Context) (ObjectSummary, error) {
	return runStorageProbe(ctx, "probe/hello.txt", "hello")
}

//scenery:api public path=/storage/probe-public method=POST
func (*Service) PublicStorageProbe(ctx context.Context) (ObjectSummary, error) {
	ctx = storage.WithTenantID(ctx, "storage-probe")
	return runStorageProbe(ctx, "probe/public.txt", "hello public")
}

//scenery:api public path=/storage/probe-public method=GET
func (*Service) ReadPublicStorageProbe(ctx context.Context) (ObjectSummary, error) {
	ctx = storage.WithTenantID(ctx, "storage-probe")
	return readStorageProbe(ctx, "probe/public.txt")
}

func runStorageProbe(ctx context.Context, key, value string) (ObjectSummary, error) {
	store, err := storage.Default(ctx)
	if err != nil {
		return ObjectSummary{}, err
	}
	obj, err := store.Put(ctx, key, strings.NewReader(value), storage.PutOptions{ContentType: "text/plain"})
	if err != nil {
		return ObjectSummary{}, err
	}
	return readObjectSummary(ctx, store, obj.Key, obj.SizeBytes)
}

func readStorageProbe(ctx context.Context, key string) (ObjectSummary, error) {
	store, err := storage.Default(ctx)
	if err != nil {
		return ObjectSummary{}, err
	}
	obj, err := store.Head(ctx, key)
	if err != nil {
		return ObjectSummary{}, err
	}
	return readObjectSummary(ctx, store, key, obj.SizeBytes)
}

func readObjectSummary(ctx context.Context, store storage.Store, key string, size int64) (ObjectSummary, error) {
	body, _, err := store.Get(ctx, key, storage.GetOptions{})
	if err != nil {
		return ObjectSummary{}, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return ObjectSummary{}, err
	}
	return ObjectSummary{Key: key, SizeBytes: size, Body: string(data)}, nil
}
