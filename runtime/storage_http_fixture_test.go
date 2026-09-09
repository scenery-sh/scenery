package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	"scenery.sh/storage"
)

// No listener, socket or filesystem durability in the route unit lane. Native
// SDK/runtime agreement is verified by the storage release probe.
type storageInProcessServer struct {
	URL     string
	handler http.Handler
}

func newStorageInProcessServer(handler http.Handler) *storageInProcessServer {
	return &storageInProcessServer{URL: "http://storage.test", handler: handler}
}
func (s *storageInProcessServer) Close()               {}
func (s *storageInProcessServer) Client() *http.Client { return &http.Client{Transport: s} }
func (s *storageInProcessServer) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func newStorageHTTPTestServer() (*http.Server, error) {
	router := newRouteTable()
	storageHTTPTestRoutes().register(router, false)
	return &http.Server{Handler: router}, nil
}
func storageHTTPTestRoutes() storageHTTPRoutes {
	stores := map[string]*storageHTTPTestStore{}
	return storageHTTPRoutes{resolve: func(ctx context.Context, name string) (storage.Store, error) {
		tenant := ""
		if state := stateFromContext(ctx); state != nil {
			tenant, _ = storageHTTPTenantID(state.auth.Data)
		}
		key := name + ":" + tenant
		if stores[key] == nil {
			stores[key] = &storageHTTPTestStore{name: name, tenant: tenant, objects: map[string]storageHTTPTestObject{}}
		}
		return stores[key], nil
	}}
}

type storageHTTPTestObject struct {
	object storage.Object
	data   string
}
type storageHTTPTestStore struct {
	storage.Store
	name, tenant string
	version      int
	objects      map[string]storageHTTPTestObject
}

func (s *storageHTTPTestStore) Put(_ context.Context, key string, body io.Reader, opts storage.PutOptions) (*storage.Object, error) {
	old, exists := s.objects[key]
	if (opts.IfNoneMatch && exists) || (opts.IfMatch != "" && (!exists || old.object.ETag != opts.IfMatch)) {
		return nil, &storage.PreconditionError{Store: s.name, Key: key}
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	s.version++
	object := storage.Object{Store: s.name, Tenant: s.tenant, Key: key, SizeBytes: int64(len(data)), ContentType: opts.ContentType, Metadata: opts.Metadata, ETag: fmt.Sprintf(`"%032x"`, s.version), SHA256: strings.Repeat("a", 64), ModifiedAt: time.Unix(1, 0).UTC()}
	s.objects[key] = storageHTTPTestObject{object: object, data: string(data)}
	return &object, nil
}
func (s *storageHTTPTestStore) Head(_ context.Context, key string) (*storage.Object, error) {
	object, ok := s.objects[key]
	if !ok {
		return nil, &storage.NotFoundError{Store: s.name, Key: key}
	}
	return &object.object, nil
}
func (s *storageHTTPTestStore) Get(ctx context.Context, key string, opts storage.GetOptions) (io.ReadCloser, *storage.Object, error) {
	object, err := s.Head(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	data := s.objects[key].data
	start, end := int64(0), int64(len(data))
	if opts.Offset != nil {
		start = *opts.Offset
	}
	if opts.Length != nil {
		end = start + *opts.Length
	}
	if start < 0 || start > end || end > int64(len(data)) {
		return nil, nil, storage.ErrInvalidInput
	}
	return io.NopCloser(strings.NewReader(data[start:end])), object, nil
}
func (s *storageHTTPTestStore) List(_ context.Context, opts storage.ListOptions) (*storage.ListPage, error) {
	page := &storage.ListPage{Objects: []storage.Object{}}
	for key, stored := range s.objects {
		if strings.HasPrefix(key, opts.Prefix) {
			object := stored.object
			object.Metadata = nil
			page.Objects = append(page.Objects, object)
		}
	}
	sort.Slice(page.Objects, func(i, j int) bool { return page.Objects[i].Key < page.Objects[j].Key })
	return page, nil
}
func (s *storageHTTPTestStore) Delete(_ context.Context, key string, opts storage.DeleteOptions) error {
	object, ok := s.objects[key]
	if opts.IfMatch != "" && (!ok || object.object.ETag != opts.IfMatch) {
		return &storage.PreconditionError{Store: s.name, Key: key}
	}
	delete(s.objects, key)
	return nil
}
