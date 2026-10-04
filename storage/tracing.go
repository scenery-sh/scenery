package storage

import (
	"context"
	"io"

	"scenery.sh/internal/appsdk"
)

// tracedStore wraps the generated dependency once, independently of its local
// or proxy backend. Object keys and contents are deliberately not reported.
type tracedStore struct {
	Store
	name string
}

func (s *tracedStore) begin(ctx context.Context, operation string) (context.Context, func(int64, error)) {
	if host := appsdk.CurrentHost(); host != nil {
		return host.TraceStorageOperation(ctx, s.name, operation)
	}
	return ctx, func(int64, error) {}
}
func (s *tracedStore) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error) {
	ctx, end := s.begin(ctx, "put")
	obj, err := s.Store.Put(ctx, key, body, opts)
	var size int64
	if obj != nil {
		size = obj.SizeBytes
	}
	end(size, err)
	return obj, err
}
func (s *tracedStore) Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error) {
	ctx, end := s.begin(ctx, "get")
	body, obj, err := s.Store.Get(ctx, key, opts)
	if err != nil || body == nil {
		end(0, err)
		return body, obj, err
	}
	return appsdk.ObserveReadCloser(ctx, body, end), obj, nil
}
func (s *tracedStore) Head(ctx context.Context, key string) (*Object, error) {
	ctx, end := s.begin(ctx, "head")
	obj, err := s.Store.Head(ctx, key)
	end(0, err)
	return obj, err
}
func (s *tracedStore) List(ctx context.Context, opts ListOptions) (*ListPage, error) {
	ctx, end := s.begin(ctx, "list")
	page, err := s.Store.List(ctx, opts)
	end(0, err)
	return page, err
}
func (s *tracedStore) Delete(ctx context.Context, key string, opts DeleteOptions) error {
	ctx, end := s.begin(ctx, "delete")
	err := s.Store.Delete(ctx, key, opts)
	end(0, err)
	return err
}
func (s *tracedStore) DeletePrefix(ctx context.Context, prefix string) error {
	ctx, end := s.begin(ctx, "delete_prefix")
	err := s.Store.DeletePrefix(ctx, prefix)
	end(0, err)
	return err
}
