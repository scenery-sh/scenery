package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/storagefs"
	publicstorage "scenery.sh/storage"
)

// Native CLI/Unix-socket CRUD, durable cleanup and cross-process publication
// belong to the named storage release probe. These tests cover parsing and
// adapter contracts in process; storagefs owns the object protocol tests.
func TestParseStorageArgs(t *testing.T) {
	opts, err := parseStorageArgs([]string{"ls", "app", "--tenant", "team", "--prefix", "reports/", "--delimiter", "/", "--limit", "10", "-o", "json"})
	if err != nil || opts.Store != "app" || opts.Tenant != "team" || opts.Limit != 10 || opts.Delimiter != "/" {
		t.Fatalf("options=%+v error=%v", opts, err)
	}
	for _, args := range [][]string{
		{"status"}, {"webui"}, {"ls"}, {"put", "app", "a", "-", "--if-absent", "--if-match", "tag"},
		{"cleanup", "--yes"}, {"rm", "app", "a", "--recursive", "--yes"}, {"ls", "app", "--if-match", "tag"},
	} {
		if _, err := parseStorageArgs(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := parseStorageArgs([]string{"cleanup", "--purge", "--yes", "--expect-revision", "sha256:selection", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
}

func TestStorageReadOnlyCommandsDoNotAllocate(t *testing.T) {
	home := t.TempDir()
	_ = isolateCommandAgentHomeAt(t, home)
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"files-app","storage":{"stores":{"app":{"kind":"local"}}}}`)
	for _, command := range []string{"ls", "stat", "cleanup"} {
		var out bytes.Buffer
		args := []string{command}
		if command != "cleanup" {
			args = append(args, "app")
		}
		if command == "stat" {
			args = append(args, "missing")
		}
		args = append(args, "--app-root", root, "-o", "json")
		err := runStorageCommand(args, &out)
		if command == "stat" {
			if !errors.Is(err, storagefs.ErrNotFound) {
				t.Fatalf("stat: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	plan, err := resolveStorageNamespacePlan(appcfg.Config{Name: "files-app"}, root, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.Worktree.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only commands allocated worktree: %v", err)
	}
}

func TestHeadlessStorageRequiresExplicitExternalAuthority(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name      string
		config    storageconfig.RuntimeConfig
		wantError bool
	}{
		{"local", storageconfig.RuntimeConfig{Stores: map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "local", Root: root}}}, false},
		{"relative", storageconfig.RuntimeConfig{Stores: map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "local", Root: "relative"}}}, true},
		{"unbound proxy", storageconfig.RuntimeConfig{Stores: map[string]storageconfig.RuntimeStoreConfig{"app": {Kind: "proxy", ProxySocket: "/tmp/storage.sock"}}}, true},
		{"empty", storageconfig.RuntimeConfig{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.config.ArtifactIdentity = storageconfig.NewRuntimeIdentity()
			data, err := json.Marshal(test.config)
			if err != nil {
				t.Fatal(err)
			}
			err = validateHeadlessStorageRuntimeConfig(string(data))
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v", err)
			}
		})
	}
	cfg := appcfg.Config{Storage: appcfg.StorageConfig{Stores: map[string]appcfg.StorageStoreConfig{"app": {Kind: "local"}}}}
	if _, err := headlessStorageCapabilityEnv(cfg, nil); err == nil {
		t.Fatal("headless runtime silently selected managed dev files")
	}
}

func TestStorageProxyRejectsNamespaceAndTenantBeforeResolution(t *testing.T) {
	for _, test := range []struct {
		binding, tenant string
		want            int
	}{
		{"wrong", "", http.StatusConflict}, {"bound", "", http.StatusForbidden}, {"bound", "***", http.StatusBadRequest},
	} {
		h := storageProxyHandler(map[string]appcfg.StorageStoreConfig{"app": {TenantScoped: true}}, "bound", func(string, string) (publicstorage.Store, error) {
			t.Fatal("resolved rejected request")
			return nil, nil
		})
		req := httptest.NewRequest(http.MethodGet, "http://proxy/v1/stores/app", nil)
		req.Header.Set("X-Scenery-Storage-Namespace", test.binding)
		req.Header.Set("X-Scenery-Storage-Tenant", test.tenant)
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, req)
		if recorder.Code != test.want {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

type storageProxyContractStore struct {
	publicstorage.Store
	put     publicstorage.PutOptions
	deleted publicstorage.DeleteOptions
	key     string
}

func (s *storageProxyContractStore) Put(_ context.Context, key string, body io.Reader, opts publicstorage.PutOptions) (*publicstorage.Object, error) {
	s.key = key
	s.put = opts
	_, err := io.Copy(io.Discard, body)
	return &publicstorage.Object{Store: "app", Key: key, ETag: `"new"`}, err
}
func (s *storageProxyContractStore) Delete(_ context.Context, key string, opts publicstorage.DeleteOptions) error {
	s.key = key
	s.deleted = opts
	return storagefs.ErrPrecondition
}

func TestStorageProxyPreservesLogicalMetadataAndConditions(t *testing.T) {
	store := &storageProxyContractStore{}
	h := storageProxyHandler(map[string]appcfg.StorageStoreConfig{"app": {TenantScoped: true}}, "bound", func(name, tenant string) (publicstorage.Store, error) {
		if name != "app" || tenant != "team/č" {
			t.Fatalf("wrong tuple %q %q", name, tenant)
		}
		return store, nil
	})
	req := httptest.NewRequest(http.MethodPut, "http://proxy/v1/stores/app/objects/a%252Fb", strings.NewReader("bytes"))
	req.Header.Set("X-Scenery-Storage-Namespace", "bound")
	req.Header.Set("X-Scenery-Storage-Tenant", base64.RawURLEncoding.EncodeToString([]byte("team/č")))
	req.Header.Set("If-Match", `"old"`)
	publicstorage.SetMetadataHeaders(req.Header, map[string]string{"Case-Key": "český"})
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated || store.key != "a%2Fb" || store.put.IfMatch != `"old"` || store.put.Metadata["Case-Key"] != "český" {
		t.Fatalf("bad adapter: %+v status=%d body=%s", store, recorder.Code, recorder.Body.String())
	}
	req.Method = http.MethodDelete
	recorder = httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusPreconditionFailed || store.deleted.IfMatch != `"old"` {
		t.Fatalf("conditional delete: %+v status=%d", store.deleted, recorder.Code)
	}
}

func TestStorageProxyDeletesEmptyPrefixPerTenant(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	binding := storagefs.Binding{AppID: "storage-proxy-test", AppRoot: root, UserID: os.Getuid()}
	namespace, err := storagefs.Allocate(ctx, filepath.Join(root, "storage"), binding)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := storageconfig.Namespace{Root: namespace.Path, Binding: binding, Incarnation: namespace.Incarnation}
	proxyBinding := descriptor.ProxyBinding()
	server := &http.Server{Handler: storageProxyHandler(
		map[string]appcfg.StorageStoreConfig{"app": {TenantScoped: true}},
		proxyBinding,
		func(name, tenant string) (publicstorage.Store, error) {
			if name != "app" {
				return nil, errors.New("resolved unexpected store")
			}
			return namespace.Store(storagefs.Scope{Store: name, Tenant: tenant}, 0)
		},
	)}
	socketDir, err := os.MkdirTemp("", "scenery-storage-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := filepath.Join(socketDir, "storage.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		<-done
	})
	runtimeConfig := storageconfig.RuntimeConfig{
		ArtifactIdentity: storageconfig.NewRuntimeIdentity(),
		Namespace:        &descriptor,
		Default:          "app",
		Stores: map[string]storageconfig.RuntimeStoreConfig{
			"app": {Kind: "proxy", ProxySocket: socket, TenantScoped: true},
		},
	}
	data, err := json.Marshal(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(storageconfig.RuntimeConfigEnv, string(data))
	store, err := publicstorage.Default(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tenantA := publicstorage.WithTenantID(ctx, "tenant-a")
	tenantB := publicstorage.WithTenantID(ctx, "tenant-b")
	if _, err := store.Put(tenantA, "a", strings.NewReader("tenant a"), publicstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(tenantB, "a", strings.NewReader("tenant b"), publicstorage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrefix(tenantA, ""); err != nil {
		t.Fatalf("empty-prefix delete: %v", err)
	}
	var missing *publicstorage.NotFoundError
	if _, err := store.Head(tenantA, "a"); !errors.As(err, &missing) {
		t.Fatalf("tenant A was not cleared: %v", err)
	}
	if object, err := store.Head(tenantB, "a"); err != nil || object.Key != "a" {
		t.Fatalf("tenant B was affected: object=%+v error=%v", object, err)
	}
}

func TestStorageProxySocketPathFallsBackToPrivateShortPath(t *testing.T) {
	name := storageProxySocketPath(&localagent.Session{StateRoot: filepath.Join(t.TempDir(), strings.Repeat("long-", 40))})
	if len(name) > 100 || filepath.Dir(name) == os.TempDir() {
		t.Fatalf("unsafe socket path %q", name)
	}
}
