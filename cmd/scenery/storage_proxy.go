package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/storagefs"
	publicstorage "scenery.sh/storage"
)

type managedStorageProxy struct {
	socketPath string
	server     *http.Server
	listener   net.Listener
	done       chan error
}

func (s *devSupervisor) ensureManagedStorageProxy(ctx context.Context) error {
	if s == nil || s.storageProxy != nil || len(s.cfg.Storage.Stores) == 0 {
		return nil
	}
	session := s.currentAgentSession()
	if session == nil || storageProxySocketPath(session) == "" {
		return nil
	}
	plan, err := resolveStorageNamespacePlan(s.cfg, s.root, "")
	if err != nil || plan == nil {
		return err
	}
	proxy, err := startManagedStorageProxy(ctx, s.cfg, session, plan)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.storageProxy = proxy
	s.mu.Unlock()
	return nil
}

func startManagedStorageProxy(ctx context.Context, cfg appcfg.Config, session *localagent.Session, plan *storageNamespacePlan) (*managedStorageProxy, error) {
	socketPath := storageProxySocketPath(session)
	if socketPath == "" || plan == nil || len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	namespace, err := plan.allocate(ctx)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, err
	}
	dirInfo, err := os.Lstat(filepath.Dir(socketPath))
	if err != nil {
		return nil, err
	}
	dirStat, ok := dirInfo.Sys().(*syscall.Stat_t)
	if !ok || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 || dirStat.Uid != uint32(os.Getuid()) {
		return nil, storagefs.ErrOwnership
	}
	if old, err := os.Lstat(socketPath); err == nil {
		if old.Mode()&os.ModeSocket == 0 {
			return nil, storagefs.ErrOwnership
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	descriptor := storageconfig.Namespace{Root: plan.Root, Binding: plan.Binding, Incarnation: namespace.Incarnation}
	mux := http.NewServeMux()
	mux.Handle("/v1/stores/", storageProxyHandler(cfg.Storage.Stores, descriptor.ProxyBinding(), func(name, tenant string) (publicstorage.Store, error) {
		return namespace.Store(storagefs.Scope{Store: name, Tenant: tenant}, cfg.Storage.Stores[name].MaxObjectBytes)
	}))
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	proxy := &managedStorageProxy{socketPath: socketPath, server: server, listener: ln, done: make(chan error, 1)}
	go func() {
		err := server.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		proxy.done <- err
		close(proxy.done)
	}()
	select {
	case <-ctx.Done():
		_ = proxy.Close()
		return nil, ctx.Err()
	default:
	}
	return proxy, nil
}

func storageProxySocketPath(session *localagent.Session) string {
	if session == nil || strings.TrimSpace(session.StateRoot) == "" {
		return ""
	}
	path := filepath.Join(session.StateRoot, "run", "storage", "proxy.sock")
	if len(path) <= 100 {
		return path
	}
	hash := shortIdentityHash(firstNonEmpty(session.StateRoot, session.AppRoot, session.SessionID))
	if hash == "" {
		hash = "storage"
	}
	return filepath.Join(os.TempDir(), "scenery-"+strconv.Itoa(os.Getuid()), "storage-"+hash+".sock")
}

func storageProxyHandler(stores map[string]appcfg.StorageStoreConfig, binding string, resolve func(string, string) (publicstorage.Store, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if binding == "" || req.Header.Get("X-Scenery-Storage-Namespace") != binding {
			writeStorageProxyError(w, storagefs.ErrOwnership)
			return
		}
		storeName, key, objectRoute, ok := parseStorageProxyPath(req.URL.EscapedPath())
		if !ok {
			http.NotFound(w, req)
			return
		}
		policy, ok := stores[storeName]
		if !ok {
			http.Error(w, "storage store is not configured", http.StatusNotFound)
			return
		}
		rawTenant := req.Header.Get("X-Scenery-Storage-Tenant")
		if len(rawTenant) > 344 {
			writeStorageProxyError(w, storagefs.ErrInvalid)
			return
		}
		tenant, err := base64.RawURLEncoding.Strict().DecodeString(rawTenant)
		if err != nil {
			writeStorageProxyError(w, storagefs.ErrInvalid)
			return
		}
		if policy.TenantScoped && len(tenant) == 0 {
			writeStorageProxyError(w, &publicstorage.TenantRequiredError{Store: storeName})
			return
		}
		if !policy.TenantScoped && len(tenant) != 0 {
			writeStorageProxyError(w, storagefs.ErrInvalid)
			return
		}
		store, err := resolve(storeName, string(tenant))
		if err != nil {
			writeStorageProxyError(w, err)
			return
		}
		if !objectRoute {
			handleStorageProxyList(w, req, store)
			return
		}
		handleStorageProxyObject(w, req, store, key)
	}
}

func parseStorageProxyPath(path string) (store, key string, objectRoute bool, ok bool) {
	rest := strings.TrimPrefix(path, "/v1/stores/")
	if rest == path || rest == "" {
		return "", "", false, false
	}
	storePart, objectPart, hasObject := strings.Cut(rest, "/objects/")
	store, err := url.PathUnescape(storePart)
	if err != nil || strings.TrimSpace(store) == "" {
		return "", "", false, false
	}
	if !hasObject {
		return store, "", false, true
	}
	key, err = url.PathUnescape(objectPart)
	if err != nil || strings.TrimSpace(key) == "" {
		return "", "", false, false
	}
	return store, key, true, true
}

func handleStorageProxyList(w http.ResponseWriter, req *http.Request, store publicstorage.Store) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, err := parseStorageProxyInt(req.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := store.List(req.Context(), publicstorage.ListOptions{
		Prefix:    req.URL.Query().Get("prefix"),
		Delimiter: req.URL.Query().Get("delimiter"),
		Cursor:    req.URL.Query().Get("cursor"),
		Limit:     int(limit),
	})
	if err != nil {
		writeStorageProxyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(page)
}

func handleStorageProxyObject(w http.ResponseWriter, req *http.Request, store publicstorage.Store, key string) {
	switch req.Method {
	case http.MethodPut:
		metadata, err := publicstorage.MetadataFromHeaders(req.Header)
		if err != nil {
			writeStorageProxyError(w, err)
			return
		}
		obj, err := store.Put(req.Context(), key, req.Body, publicstorage.PutOptions{
			ContentType: req.Header.Get("Content-Type"),
			Metadata:    metadata,
			IfNoneMatch: req.Header.Get("If-None-Match") == "*",
			IfMatch:     req.Header.Get("If-Match"),
		})
		if err != nil {
			writeStorageProxyError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(obj)
	case http.MethodHead:
		obj, err := store.Head(req.Context(), key)
		if err != nil {
			writeStorageProxyError(w, err)
			return
		}
		setStorageProxyObjectHeaders(w.Header(), obj)
	case http.MethodGet:
		offset, length, err := storageProxyRange(req.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, obj, err := store.Get(req.Context(), key, publicstorage.GetOptions{Offset: offset, Length: length})
		if err != nil {
			writeStorageProxyError(w, err)
			return
		}
		defer func() { _ = body.Close() }()
		setStorageProxyObjectHeaders(w.Header(), obj)
		transfer := obj.SizeBytes
		if offset != nil {
			transfer -= *offset
		}
		if length != nil && *length < transfer {
			transfer = *length
		}
		w.Header().Set("Content-Length", strconv.FormatInt(transfer, 10))
		_, _ = io.Copy(w, body)
	case http.MethodDelete:
		var err error
		if storageProxyBool(req.URL.Query().Get("recursive")) {
			err = store.DeletePrefix(req.Context(), key)
		} else {
			err = store.Delete(req.Context(), key, publicstorage.DeleteOptions{IfMatch: req.Header.Get("If-Match")})
		}
		if err != nil {
			writeStorageProxyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func storageProxyRange(values url.Values) (*int64, *int64, error) {
	offset, err := parseStorageProxyOptionalInt(values.Get("offset"))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid offset")
	}
	length, err := parseStorageProxyOptionalInt(values.Get("length"))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid length")
	}
	return offset, length, nil
}

func parseStorageProxyOptionalInt(raw string) (*int64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	n, err := parseStorageProxyInt(raw)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func parseStorageProxyInt(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	return n, nil
}

func storageProxyBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func setStorageProxyObjectHeaders(header http.Header, obj *publicstorage.Object) {
	if obj == nil {
		return
	}
	data, _ := json.Marshal(obj)
	header.Set("X-Scenery-Storage-Object", base64.RawURLEncoding.EncodeToString(data))
	if obj.ContentType != "" {
		header.Set("Content-Type", obj.ContentType)
	}
	if obj.SizeBytes >= 0 {
		header.Set("Content-Length", strconv.FormatInt(obj.SizeBytes, 10))
	}
	if obj.ETag != "" {
		header.Set("ETag", obj.ETag)
	}
	if !obj.ModifiedAt.IsZero() {
		header.Set("Last-Modified", obj.ModifiedAt.UTC().Format(http.TimeFormat))
	}
	publicstorage.SetMetadataHeaders(header, obj.Metadata)
}

func writeStorageProxyError(w http.ResponseWriter, err error) {
	publicstorage.HTTPError(w, err)
}

func (p *managedStorageProxy) Close() error {
	if p == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := p.server.Shutdown(ctx)
	if p.listener != nil {
		_ = p.listener.Close()
	}
	if p.socketPath != "" {
		_ = os.Remove(p.socketPath)
	}
	select {
	case serveErr := <-p.done:
		return errors.Join(err, serveErr)
	case <-time.After(2 * time.Second):
		return err
	}
}
