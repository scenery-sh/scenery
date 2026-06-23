package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"scenery.sh/errs"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/storage"
)

func storageHTTPConfigured() bool {
	return strings.TrimSpace(envpolicy.Get(storageconfig.RuntimeConfigEnv)) != ""
}

func (s *server) registerStorageRoutes() {
	s.registerStorageRoutesOn(s.public, false)
	s.registerStorageRoutesOn(s.private, true)
}

func (s *server) registerStorageRoutesOn(router *routeTable, internal bool) {
	registerRoute(router, "/__scenery/storage/:store", []string{http.MethodGet}, func(w http.ResponseWriter, req *http.Request, params routeParams) {
		s.handleStorageList(w, req, params, internal)
	})
	registerRoute(router, "/__scenery/storage/:store/*key", []string{http.MethodGet, http.MethodPut, http.MethodDelete}, func(w http.ResponseWriter, req *http.Request, params routeParams) {
		s.handleStorageObject(w, req, params, internal)
	})
}

func (s *server) handleStorageList(w http.ResponseWriter, req *http.Request, params routeParams, internal bool) {
	storeName := params.ByName("store")
	ctx, ok := authenticateStorageHTTPRequest(w, req, storeName, internal)
	if !ok {
		return
	}
	store, err := storage.Named(ctx, storeName)
	if err != nil {
		errs.HTTPError(w, storageHTTPError(err))
		return
	}
	limit, err := parseStorageHTTPLimit(req.URL.Query().Get("limit"))
	if err != nil {
		errs.HTTPError(w, err)
		return
	}
	page, err := store.List(ctx, storage.ListOptions{
		Prefix:    req.URL.Query().Get("prefix"),
		Delimiter: req.URL.Query().Get("delimiter"),
		Cursor:    req.URL.Query().Get("cursor"),
		Limit:     limit,
	})
	if err != nil {
		errs.HTTPError(w, storageHTTPError(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(page); err != nil {
		errs.HTTPError(w, errs.Wrap(err, "encode storage list response"))
	}
}

func (s *server) handleStorageObject(w http.ResponseWriter, req *http.Request, params routeParams, internal bool) {
	storeName := params.ByName("store")
	key := strings.TrimPrefix(params.ByName("key"), "/")
	if key == "" {
		errs.HTTPError(w, errs.B().Code(errs.InvalidArgument).Msg("storage object key is required").Err())
		return
	}
	ctx, ok := authenticateStorageHTTPRequest(w, req, storeName, internal)
	if !ok {
		return
	}
	store, err := storage.Named(ctx, storeName)
	if err != nil {
		errs.HTTPError(w, storageHTTPError(err))
		return
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead:
		body, obj, err := store.Get(ctx, key, storage.GetOptions{})
		if err != nil {
			errs.HTTPError(w, storageHTTPError(err))
			return
		}
		storage.ServeObject(w, req, body, obj)
	case http.MethodPut:
		obj, err := store.Put(ctx, key, req.Body, storage.PutOptions{
			ContentType: req.Header.Get("Content-Type"),
			IfNoneMatch: req.Header.Get("If-None-Match") == "*",
		})
		if err != nil {
			errs.HTTPError(w, storageHTTPError(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(obj); err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode storage object response"))
		}
	case http.MethodDelete:
		if storageHTTPBool(req.URL.Query().Get("recursive")) {
			if err := store.DeletePrefix(ctx, key); err != nil {
				errs.HTTPError(w, storageHTTPError(err))
				return
			}
		} else if err := store.Delete(ctx, key); err != nil {
			errs.HTTPError(w, storageHTTPError(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		errs.HTTPErrorWithCode(w, errs.B().Code(errs.InvalidArgument).Msg("method not allowed").Err(), http.StatusMethodNotAllowed)
	}
}

func authenticateStorageHTTPRequest(w http.ResponseWriter, req *http.Request, store string, internal bool) (context.Context, bool) {
	access, err := storageHTTPStoreAccess(store)
	if err != nil {
		errs.HTTPError(w, err)
		return nil, false
	}
	if internal {
		return req.Context(), true
	}
	if access == Private {
		errs.HTTPError(w, errs.B().Code(errs.PermissionDenied).Msg("storage store is private").Err())
		return nil, false
	}
	authInfo, err := authenticateRequest(req, &Endpoint{
		Service: "scenery.storage",
		Name:    store,
		Access:  access,
		Raw:     true,
		Path:    req.URL.Path,
		Methods: []string{req.Method},
	})
	if err != nil {
		errs.HTTPError(w, err)
		return nil, false
	}
	ctx := req.Context()
	if authInfo.UID != "" {
		ctx = WithAuthContext(ctx, authInfo)
	}
	return ctx, true
}

func storageHTTPStoreAccess(name string) (Access, error) {
	cfg, err := loadStorageHTTPRuntimeConfig()
	if err != nil {
		return Auth, storageHTTPError(err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(cfg.Default)
	}
	if name == "" {
		return Auth, errs.B().Code(errs.FailedPrecondition).Msg("scenery storage default store is not configured").Err()
	}
	store, ok := cfg.Stores[name]
	if !ok {
		return Auth, errs.B().Code(errs.NotFound).Msgf("storage store %q is not configured", name).Err()
	}
	switch strings.TrimSpace(store.Access) {
	case "", "auth":
		return Auth, nil
	case "private":
		return Private, nil
	default:
		return Auth, errs.B().Code(errs.FailedPrecondition).Msgf("storage store %q access is invalid", name).Err()
	}
}

func loadStorageHTTPRuntimeConfig() (storageconfig.RuntimeConfig, error) {
	cfg, ok, err := storageconfig.LoadRuntimeConfigValue(envpolicy.Get(storageconfig.RuntimeConfigEnv))
	if err != nil {
		return storageconfig.RuntimeConfig{}, err
	}
	if !ok {
		return storageconfig.RuntimeConfig{}, &storage.NotConfiguredError{}
	}
	return cfg, nil
}

func parseStorageHTTPLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errs.B().Code(errs.InvalidArgument).Msg("invalid storage list limit").Cause(err).Err()
	}
	if limit < 0 {
		return 0, errs.B().Code(errs.InvalidArgument).Msg("storage list limit must be >= 0").Err()
	}
	return limit, nil
}

func storageHTTPBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func storageHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var invalid *storage.InvalidKeyError
	if errors.As(err, &invalid) {
		return errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Cause(err).Err()
	}
	var notFound *storage.NotFoundError
	if errors.As(err, &notFound) {
		return errs.B().Code(errs.NotFound).Msg(err.Error()).Cause(err).Err()
	}
	var alreadyExists *storage.AlreadyExistsError
	if errors.As(err, &alreadyExists) {
		return errs.B().Code(errs.AlreadyExists).Msg(err.Error()).Cause(err).Err()
	}
	var notConfigured *storage.NotConfiguredError
	if errors.As(err, &notConfigured) {
		return errs.B().Code(errs.FailedPrecondition).Msg(err.Error()).Cause(err).Err()
	}
	return errs.Wrap(err, "storage request failed")
}
