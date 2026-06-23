package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/storageconfig"
)

type localRuntimeStore struct {
	name           string
	root           string
	maxObjectBytes int64
}

type proxyRuntimeStore struct {
	name   string
	socket string
	client *http.Client
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

func newRuntimeStore(name string, cfg storageconfig.RuntimeStoreConfig) (Store, error) {
	switch strings.TrimSpace(cfg.Kind) {
	case "local":
		root := strings.TrimSpace(cfg.Root)
		if root == "" {
			return nil, fmt.Errorf("storage store %q root is empty", name)
		}
		return &localRuntimeStore{name: name, root: root, maxObjectBytes: cfg.MaxObjectBytes}, nil
	case "proxy":
		socket := strings.TrimSpace(cfg.ProxySocket)
		if socket == "" {
			return nil, fmt.Errorf("storage store %q proxy socket is empty", name)
		}
		return newProxyRuntimeStore(name, socket), nil
	default:
		return nil, fmt.Errorf("storage store %q backend %q is not supported by this runtime", name, cfg.Kind)
	}
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
	req, err := s.newRequest(ctx, http.MethodPut, key, nil, body)
	if err != nil {
		return nil, err
	}
	if opts.ContentType != "" {
		req.Header.Set("Content-Type", opts.ContentType)
	}
	if opts.IfNoneMatch {
		req.Header.Set("If-None-Match", "*")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, proxyStorageError(resp, s.name, key)
	}
	var obj Object
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func (s *proxyRuntimeStore) PutFile(ctx context.Context, key, localPath string, opts PutOptions) (*Object, error) {
	return PutFile(ctx, s, key, localPath, opts)
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
		defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, proxyStorageError(resp, s.name, "")
	}
	var page ListPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (s *proxyRuntimeStore) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	req, err := s.newRequest(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
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
	defer resp.Body.Close()
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
	return http.NewRequestWithContext(ctx, method, "http://scenery-storage"+path, body)
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
	switch resp.StatusCode {
	case http.StatusNotFound:
		return &NotFoundError{Store: store, Key: key}
	case http.StatusPreconditionFailed, http.StatusConflict:
		return &AlreadyExistsError{Store: store, Key: key}
	case http.StatusBadRequest:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &InvalidKeyError{Key: key, Reason: strings.TrimSpace(string(body))}
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("storage proxy returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func (s *localRuntimeStore) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	path := s.path(key)
	if opts.IfNoneMatch {
		if _, err := os.Stat(path); err == nil {
			return nil, &AlreadyExistsError{Store: s.name, Key: key}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".scenery-put-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		_ = tmp.Close()
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	h := sha256.New()
	reader := io.Reader(body)
	if s.maxObjectBytes > 0 {
		reader = io.LimitReader(body, s.maxObjectBytes+1)
	}
	n, err := io.Copy(tmp, io.TeeReader(reader, h))
	if err != nil {
		return nil, err
	}
	if s.maxObjectBytes > 0 && n > s.maxObjectBytes {
		return nil, fmt.Errorf("storage object %q exceeds max_object_bytes %d", key, s.maxObjectBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, err
	}
	removeTmp = false
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(key))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return &Object{
		Store:       s.name,
		Key:         key,
		SizeBytes:   n,
		ContentType: contentType,
		ETag:        `"` + sum + `"`,
		SHA256:      sum,
		ModifiedAt:  info.ModTime().UTC(),
		Metadata:    cloneMetadata(opts.Metadata),
	}, nil
}

func (s *localRuntimeStore) PutFile(ctx context.Context, key, localPath string, opts PutOptions) (*Object, error) {
	return PutFile(ctx, s, key, localPath, opts)
}

func (s *localRuntimeStore) Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	obj, err := s.Head(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(s.path(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, &NotFoundError{Store: s.name, Key: key}
		}
		return nil, nil, err
	}
	if opts.Offset != nil {
		if *opts.Offset < 0 || *opts.Offset > obj.SizeBytes {
			_ = file.Close()
			return nil, nil, &InvalidKeyError{Key: key, Reason: "range offset is outside object"}
		}
		if _, err := file.Seek(*opts.Offset, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, nil, err
		}
	}
	if opts.Length != nil {
		if *opts.Length < 0 {
			_ = file.Close()
			return nil, nil, &InvalidKeyError{Key: key, Reason: "range length must be non-negative"}
		}
		obj.SizeBytes = *opts.Length
		return struct {
			io.Reader
			io.Closer
		}{Reader: io.LimitReader(file, *opts.Length), Closer: file}, obj, nil
	}
	if opts.Offset != nil {
		obj.SizeBytes -= *opts.Offset
	}
	return file, obj, nil
}

func (s *localRuntimeStore) Head(ctx context.Context, key string) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	path := s.path(key)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &NotFoundError{Store: s.name, Key: key}
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, &NotFoundError{Store: s.name, Key: key}
	}
	sum, err := localFileSHA256(path)
	if err != nil {
		return nil, err
	}
	return &Object{
		Store:       s.name,
		Key:         key,
		SizeBytes:   info.Size(),
		ContentType: mime.TypeByExtension(filepath.Ext(key)),
		ETag:        `"` + sum + `"`,
		SHA256:      sum,
		ModifiedAt:  info.ModTime().UTC(),
	}, nil
}

func (s *localRuntimeStore) List(ctx context.Context, opts ListOptions) (*ListPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	var keys []string
	if err := filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, opts.Prefix) {
			keys = append(keys, key)
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(keys)
	prefixes := map[string]bool{}
	page := &ListPage{}
	for _, key := range keys {
		if opts.Cursor != "" && key <= opts.Cursor {
			continue
		}
		if opts.Delimiter == "/" {
			rest := strings.TrimPrefix(key, opts.Prefix)
			if idx := strings.Index(rest, "/"); idx >= 0 {
				prefix := opts.Prefix + rest[:idx+1]
				if !prefixes[prefix] {
					prefixes[prefix] = true
					page.Prefixes = append(page.Prefixes, prefix)
				}
				continue
			}
		}
		obj, err := s.Head(ctx, key)
		if err != nil {
			return nil, err
		}
		page.Objects = append(page.Objects, *obj)
		if len(page.Objects) >= opts.Limit {
			page.NextCursor = key
			break
		}
	}
	sort.Strings(page.Prefixes)
	return page, nil
}

func (s *localRuntimeStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := os.Remove(s.path(key)); err != nil {
		if os.IsNotExist(err) {
			return &NotFoundError{Store: s.name, Key: key}
		}
		return err
	}
	return nil
}

func (s *localRuntimeStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidatePrefix(prefix); err != nil {
		return err
	}
	return filepath.WalkDir(s.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(rel), prefix) {
			return os.Remove(path)
		}
		return nil
	})
}

func (s *localRuntimeStore) path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

func localFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
