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
	return runStorageProbe(ctx, "probe/public.txt", "hello public")
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
	body, _, err := store.Get(ctx, obj.Key, storage.GetOptions{})
	if err != nil {
		return ObjectSummary{}, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return ObjectSummary{}, err
	}
	return ObjectSummary{Key: obj.Key, SizeBytes: obj.SizeBytes, Body: string(data)}, nil
}
