package storage

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
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/storageconfig"
	"scenery.sh/internal/storagefs"
	"strings"
)

type proxyRuntimeStore struct {
	name         string
	socket       string
	client       *http.Client
	binding      string
	tenantScoped bool
}

func loadRuntimeConfig() (storageconfig.RuntimeConfig, error) {
	cfg, ok, err := storageconfig.LoadRuntimeConfigValue(envpolicy.Get(storageconfig.RuntimeConfigEnv))
	if err != nil {
		return storageconfig.RuntimeConfig{}, err
	}
	if !ok {
		return storageconfig.RuntimeConfig{}, &NotConfiguredError{}
	}
	return cfg, nil
}

func newRuntimeStore(name string, cfg storageconfig.RuntimeStoreConfig, namespace *storageconfig.Namespace) (Store, error) {
	var store Store
	switch strings.TrimSpace(cfg.Kind) {
	case "local":
		root := strings.TrimSpace(cfg.Root)
		if namespace != nil {
			root = namespace.Root
		}
		if root == "" {
			return nil, fmt.Errorf("storage store %q root is empty", name)
		}
		local := &localRuntimeStore{name: name, root: root, maxObjectBytes: cfg.MaxObjectBytes, tenantScoped: cfg.TenantScoped}
		if namespace != nil {
			var err error
			local.namespace, err = namespace.Handle()
			if err != nil {
				return nil, err
			}
		}
		store = local
	case "proxy":
		socket := strings.TrimSpace(cfg.ProxySocket)
		if socket == "" {
			return nil, fmt.Errorf("storage store %q proxy socket is empty", name)
		}
		proxy := newProxyRuntimeStore(name, socket)
		proxy.tenantScoped = cfg.TenantScoped
		if namespace == nil {
			return nil, fmt.Errorf("storage proxy requires namespace binding")
		}
		proxy.binding = namespace.ProxyBinding()
		store = proxy
	default:
		return nil, fmt.Errorf("storage store %q backend %q is not supported by this runtime", name, cfg.Kind)
	}
	return store, nil
}

func newProxyRuntimeStore(name, socket string) *proxyRuntimeStore {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &proxyRuntimeStore{name: name, socket: socket, client: &http.Client{Transport: transport}}
}

func (s *proxyRuntimeStore) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if err := storagefs.ValidatePutOptions(opts); err != nil {
		return nil, adaptError(err, s.name, key, false)
	}
	req, err := s.newRequest(ctx, http.MethodPut, key, nil, body)
	if err != nil {
		return nil, err
	}
	if opts.ContentType != "" {
		req.Header.Set("Content-Type", opts.ContentType)
	}
	SetMetadataHeaders(req.Header, opts.Metadata)
	if opts.IfNoneMatch {
		req.Header.Set("If-None-Match", "*")
	}
	if opts.IfMatch != "" {
		req.Header.Set("If-Match", opts.IfMatch)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, adaptError(proxyStorageError(resp, s.name, key), s.name, key, opts.IfNoneMatch)
	}
	var obj Object
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func (s *proxyRuntimeStore) Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, nil, err
	}
	query := url.Values{}
	if opts.Offset != nil {
		query.Set("offset", fmt.Sprintf("%d", *opts.Offset))
	}
	if opts.Length != nil {
		query.Set("length", fmt.Sprintf("%d", *opts.Length))
	}
	req, err := s.newRequest(ctx, http.MethodGet, key, query, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, nil, proxyStorageError(resp, s.name, key)
	}
	obj, err := objectFromProxyHeaders(resp.Header)
	if err != nil {
		_ = resp.Body.Close()
		return nil, nil, err
	}
	return resp.Body, obj, nil
}

func (s *proxyRuntimeStore) Head(ctx context.Context, key string) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	req, err := s.newRequest(ctx, http.MethodHead, key, nil, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, proxyStorageError(resp, s.name, key)
	}
	return objectFromProxyHeaders(resp.Header)
}

func (s *proxyRuntimeStore) List(ctx context.Context, opts ListOptions) (*ListPage, error) {
	opts, err := NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("prefix", opts.Prefix)
	query.Set("delimiter", opts.Delimiter)
	query.Set("cursor", opts.Cursor)
	if opts.Limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	req, err := s.newRequest(ctx, http.MethodGet, "", query, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, proxyStorageError(resp, s.name, "")
	}
	var page ListPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (s *proxyRuntimeStore) Delete(ctx context.Context, key string, opts DeleteOptions) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return err
	}
	if opts.IfMatch != "" {
		req.Header.Set("If-Match", opts.IfMatch)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return proxyStorageError(resp, s.name, key)
	}
	return nil
}

func (s *proxyRuntimeStore) DeletePrefix(ctx context.Context, prefix string) error {
	query := url.Values{"recursive": []string{"1"}}
	req, err := s.newRequest(ctx, http.MethodDelete, prefix, query, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return proxyStorageError(resp, s.name, prefix)
	}
	return nil
}

func (s *proxyRuntimeStore) newRequest(ctx context.Context, method, key string, query url.Values, body io.Reader) (*http.Request, error) {
	path := "/v1/stores/" + url.PathEscape(s.name)
	if key != "" {
		path += "/objects/" + url.PathEscape(key)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	tenant, err := selectedTenant(ctx, s.name, s.tenantScoped)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://scenery-storage"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Scenery-Storage-Namespace", s.binding)
	if tenant != "" {
		req.Header.Set("X-Scenery-Storage-Tenant", base64.RawURLEncoding.EncodeToString([]byte(tenant)))
	}
	return req, nil
}

func objectFromProxyHeaders(header http.Header) (*Object, error) {
	raw := strings.TrimSpace(header.Get("X-Scenery-Storage-Object"))
	if raw == "" {
		return nil, fmt.Errorf("storage proxy response missing object metadata")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var obj Object
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func proxyStorageError(resp *http.Response, store, key string) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, (16<<10)+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<10 {
		return fmt.Errorf("storage proxy returned an oversized error")
	}
	var failure storagefs.Failure
	if err := json.Unmarshal(data, &failure); err != nil {
		return fmt.Errorf("storage proxy returned invalid HTTP %d error", resp.StatusCode)
	}
	switch failure.Diagnostic {
	case "SCN8006":
		return ErrMigration
	case "SCN8007":
		return &UncertainOutcomeError{Err: errors.New(failure.Message)}
	case "SCN8008":
		return ErrRecovery
	case "SCN8009":
		return ErrCorrupt
	case "SCN8010":
		encoded, _ := json.Marshal(failure.Details)
		var progress storagefs.DeleteResult
		if err := json.Unmarshal(encoded, &progress); err != nil {
			return err
		}
		return &PartialDeleteError{Result: progress, Err: errors.New(failure.Message)}
	}
	switch failure.Code {
	case "storage_ownership_conflict":
		return ErrOwnership
	case "failed_precondition", "already_exists":
		return &PreconditionError{Store: store, Key: key}
	case "not_found":
		return &NotFoundError{Store: store, Key: key}
	case "capability_unavailable":
		return &NotConfiguredError{Store: store}
	case "permission_denied":
		return fmt.Errorf("storage access denied: %w", os.ErrPermission)
	case "tenant_required":
		return &TenantRequiredError{Store: store}
	case "invalid_argument":
		return &InvalidKeyError{Key: key, Reason: failure.Message}
	default:
		return fmt.Errorf("storage proxy failed: %s (report %s)", failure.Message, failure.ReportToken)
	}
}
