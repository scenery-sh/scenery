package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/storagefs"
	"strings"
	"testing"
)

func TestProxyHeadPreservesFailureIdentityOverUnixTransport(t *testing.T) {
	socket := startUnixStorageTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/stores/app/objects/missing":
			HTTPError(w, &NotFoundError{Store: "app", Key: "missing"})
		case "/v1/stores/app/objects/recovery":
			HTTPError(w, storagefs.ErrRecovery)
		default:
			http.NotFound(w, req)
		}
	}))
	proxy := newProxyRuntimeStore("app", socket)
	proxy.binding = "test-binding"

	_, err := proxy.Head(context.Background(), "missing")
	var missing *NotFoundError
	if !errors.As(err, &missing) || missing.Store != "app" || missing.Key != "missing" {
		t.Fatalf("missing HEAD identity: %v", err)
	}
	_, err = proxy.Head(context.Background(), "recovery")
	if !errors.Is(err, ErrRecovery) {
		t.Fatalf("recovery HEAD identity: %v", err)
	}
}

func TestDefaultUsesRuntimeConfigWithoutAllocating(t *testing.T) {
	root := filepath.Join(t.TempDir(), "external")
	raw, err := json.Marshal(storageconfig.RuntimeConfig{ArtifactIdentity: storageconfig.NewRuntimeIdentity(), Default: "app", Stores: map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "local", Root: root}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(storageconfig.RuntimeConfigEnv, string(raw))
	store, err := Default(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	local, ok := store.(*localRuntimeStore)
	if !ok || local.root != root || local.name != "app" {
		t.Fatalf("configured store: %#v", store)
	}
	page, err := store.List(context.Background(), ListOptions{})
	if err != nil || len(page.Objects) != 0 {
		t.Fatalf("uninitialized list: %+v, %v", page, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read allocated namespace: %v", err)
	}
}
func TestDefaultWithoutRuntimeConfigReturnsNotConfigured(t *testing.T) {
	t.Setenv(storageconfig.RuntimeConfigEnv, "")
	_, err := Default(context.Background())
	var missing *NotConfiguredError
	if !errors.As(err, &missing) {
		t.Fatalf("Default: %v", err)
	}
}
func TestLocalAdapterPreservesVersionConditionsAndFullSize(t *testing.T) {
	backend := &recordingStore{putErr: storagefs.ErrPrecondition}
	local := &localRuntimeStore{name: "app", resolve: func(context.Context, storagefs.Scope, bool) (Store, error) { return backend, nil }}
	_, err := local.Put(context.Background(), "a", strings.NewReader("body"), PutOptions{IfNoneMatch: true})
	var exists *AlreadyExistsError
	if !errors.As(err, &exists) || !backend.putOptions.IfNoneMatch {
		t.Fatalf("create-only: %v, %+v", err, backend.putOptions)
	}
	_, err = local.Put(context.Background(), "a", strings.NewReader("body"), PutOptions{IfMatch: "opaque"})
	var condition *PreconditionError
	if !errors.As(err, &condition) || backend.putOptions.IfMatch != "opaque" {
		t.Fatalf("If-Match: %v, %+v", err, backend.putOptions)
	}
	backend.object = &Object{Key: "a", SizeBytes: 100, ETag: "version"}
	offset, length := int64(5), int64(2)
	body, obj, err := local.Get(context.Background(), "a", GetOptions{Offset: &offset, Length: &length})
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if obj.SizeBytes != 100 || *backend.getOptions.Offset != 5 || *backend.getOptions.Length != 2 {
		t.Fatalf("range metadata: %+v", obj)
	}
	if err := local.Delete(context.Background(), "a", DeleteOptions{IfMatch: "version"}); err != nil {
		t.Fatal(err)
	}
	if backend.deleteOptions.IfMatch != "version" {
		t.Fatal("delete lost condition")
	}
}
func TestPackagePutFileUsesOnlyPut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("file bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &recordingStore{}
	if _, err := PutFile(context.Background(), backend, "a", path, PutOptions{IfMatch: "etag"}); err != nil {
		t.Fatal(err)
	}
	if backend.body != "file bytes" || backend.putOptions.IfMatch != "etag" {
		t.Fatalf("PutFile: %#v", backend)
	}
}

type recordingStore struct {
	keys          []string
	body          string
	putOptions    PutOptions
	getOptions    GetOptions
	deleteOptions DeleteOptions
	listOptions   ListOptions
	object        *Object
	putErr        error
}

func (s *recordingStore) Put(_ context.Context, key string, body io.Reader, opts PutOptions) (*Object, error) {
	s.keys = append(s.keys, key)
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	s.body = string(data)
	s.putOptions = opts
	return s.object, s.putErr
}
func (s *recordingStore) Get(_ context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error) {
	s.keys = append(s.keys, key)
	s.getOptions = opts
	return io.NopCloser(strings.NewReader(s.body)), s.object, nil
}
func (s *recordingStore) Head(_ context.Context, key string) (*Object, error) {
	s.keys = append(s.keys, key)
	return s.object, nil
}
func (s *recordingStore) List(_ context.Context, opts ListOptions) (*ListPage, error) {
	s.listOptions = opts
	return &ListPage{Objects: []Object{}, NextCursor: opts.Cursor}, nil
}
func (s *recordingStore) Delete(_ context.Context, key string, opts DeleteOptions) error {
	s.keys = append(s.keys, key)
	s.deleteOptions = opts
	return nil
}
func (s *recordingStore) DeletePrefix(_ context.Context, prefix string) error {
	s.keys = append(s.keys, prefix)
	return nil
}

func startUnixStorageTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "scenery-storage-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "storage.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		<-done
	})
	return socket
}
