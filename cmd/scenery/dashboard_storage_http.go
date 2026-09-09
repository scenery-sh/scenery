package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"

	"scenery.sh/internal/storagefs"
	publicstorage "scenery.sh/storage"
)

const dashboardStoragePath = "/__storage"

// Files stream outside JSON RPC, on the same dashboard listener. A custom
// header plus exact browser origin prevents cross-origin form/navigation reads
// and writes; this is a local operator capability, not an application route.
func (s *dashboardServer) handleStorageTransfer(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !dashboardCheckOrigin(req) || req.Header.Get("X-Scenery-Storage-Request") != "1" {
		publicstorage.HTTPError(w, os.ErrPermission)
		return
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, HEAD, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.storageTransfer(w, req); err != nil {
		publicstorage.HTTPError(w, err)
	}
}

func (s *dashboardServer) storageTransfer(w http.ResponseWriter, req *http.Request) (returnErr error) {
	query := req.URL.Query()
	allowed := map[string]bool{"app_id": true, "worktree_key": true, "incarnation": true, "generation": true, "store": true, "tenant": true, "key": true}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return fmt.Errorf("%w: invalid storage transfer selector", storagefs.ErrInvalid)
		}
	}
	params := dashboardStorageRequest{AppID: query.Get("app_id"), WorktreeKey: query.Get("worktree_key"), Incarnation: query.Get("incarnation"), Generation: query.Get("generation"), Store: query.Get("store"), Tenant: query.Get("tenant"), Key: query.Get("key")}
	if err := storagefs.ValidateKey(params.Key); err != nil {
		return err
	}
	store, scope, lease, err := s.openDashboardStorage(req.Context(), params)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, lease.Close()) }()
	if req.Method == http.MethodPut {
		defer func() { _ = req.Body.Close() }()
		metadata, err := publicstorage.MetadataFromHeaders(req.Header)
		if err != nil {
			return err
		}
		opts := storagefs.PutOptions{ContentType: req.Header.Get("Content-Type"), Metadata: metadata, IfMatch: req.Header.Get("If-Match"), IfNoneMatch: req.Header.Get("If-None-Match") == "*"}
		if (!opts.IfNoneMatch && opts.IfMatch == "") || (req.Header.Get("If-None-Match") != "" && !opts.IfNoneMatch) {
			return fmt.Errorf("%w: upload requires create-only or an exact displayed version", storagefs.ErrPrecondition)
		}
		object, err := store.Put(req.Context(), params.Key, req.Body, opts)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		return writeStorageJSON(w, storageObjectResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.object"), Scope: scope, Object: *object})
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(params.Key)}))
	return publicstorage.ServeObject(w, req, dashboardDownloadStore{Store: store, etag: req.Header.Get("If-Match")}, params.Key)
}

type dashboardDownloadStore struct {
	publicstorage.Store
	etag string
}

func (s dashboardDownloadStore) Head(ctx context.Context, key string) (*publicstorage.Object, error) {
	object, err := s.Store.Head(ctx, key)
	if err == nil && s.etag != "" && s.etag != object.ETag {
		err = storagefs.ErrPrecondition
	}
	return object, err
}

func (s dashboardDownloadStore) Get(ctx context.Context, key string, opts publicstorage.GetOptions) (io.ReadCloser, *publicstorage.Object, error) {
	body, object, err := s.Store.Get(ctx, key, opts)
	if err == nil && s.etag != "" && s.etag != object.ETag {
		return nil, nil, errors.Join(storagefs.ErrPrecondition, body.Close())
	}
	return body, object, err
}
