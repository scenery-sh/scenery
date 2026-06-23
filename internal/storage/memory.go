package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	public "scenery.sh/storage"
)

type MemoryStore struct {
	name string
	now  func() time.Time

	mu      sync.RWMutex
	objects map[string]memoryObject
}

type memoryObject struct {
	meta public.Object
	body []byte
}

func NewMemoryStore(name string) *MemoryStore {
	return &MemoryStore{name: name, now: time.Now, objects: map[string]memoryObject{}}
}

func (s *MemoryStore) Put(ctx context.Context, key string, body io.Reader, opts public.PutOptions) (*public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := public.ValidateKey(key); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	meta := public.Object{
		Store:       s.name,
		Key:         key,
		SizeBytes:   int64(len(data)),
		ContentType: opts.ContentType,
		ETag:        `"` + hex.EncodeToString(sum[:]) + `"`,
		SHA256:      hex.EncodeToString(sum[:]),
		ModifiedAt:  s.now().UTC(),
		Metadata:    cloneMap(opts.Metadata),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if opts.IfNoneMatch {
		if _, ok := s.objects[key]; ok {
			return nil, &public.AlreadyExistsError{Store: s.name, Key: key}
		}
	}
	s.objects[key] = memoryObject{meta: meta, body: append([]byte(nil), data...)}
	return cloneObject(&meta), nil
}

func (s *MemoryStore) PutFile(ctx context.Context, key, localPath string, opts public.PutOptions) (*public.Object, error) {
	return public.PutFile(ctx, s, key, localPath, opts)
}

func (s *MemoryStore) Get(ctx context.Context, key string, opts public.GetOptions) (io.ReadCloser, *public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := public.ValidateKey(key); err != nil {
		return nil, nil, err
	}
	s.mu.RLock()
	obj, ok := s.objects[key]
	s.mu.RUnlock()
	if !ok {
		return nil, nil, &public.NotFoundError{Store: s.name, Key: key}
	}
	start := int64(0)
	if opts.Offset != nil {
		start = *opts.Offset
	}
	if start < 0 || start > int64(len(obj.body)) {
		return nil, nil, fmt.Errorf("storage get offset %d is outside object %q", start, key)
	}
	end := int64(len(obj.body))
	if opts.Length != nil {
		if *opts.Length < 0 {
			return nil, nil, fmt.Errorf("storage get length %d is invalid", *opts.Length)
		}
		end = start + *opts.Length
		if end > int64(len(obj.body)) {
			end = int64(len(obj.body))
		}
	}
	data := append([]byte(nil), obj.body[start:end]...)
	meta := cloneObject(&obj.meta)
	meta.SizeBytes = int64(len(data))
	return io.NopCloser(bytes.NewReader(data)), meta, nil
}

func (s *MemoryStore) Head(ctx context.Context, key string) (*public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := public.ValidateKey(key); err != nil {
		return nil, err
	}
	s.mu.RLock()
	obj, ok := s.objects[key]
	s.mu.RUnlock()
	if !ok {
		return nil, &public.NotFoundError{Store: s.name, Key: key}
	}
	return cloneObject(&obj.meta), nil
}

func (s *MemoryStore) List(ctx context.Context, opts public.ListOptions) (*public.ListPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := public.NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	var after string
	if opts.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(opts.Cursor)
		if err != nil {
			return nil, fmt.Errorf("invalid storage list cursor")
		}
		after = string(data)
	}
	s.mu.RLock()
	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	prefixes := map[string]bool{}
	page := &public.ListPage{}
	for _, key := range keys {
		if key <= after || !strings.HasPrefix(key, opts.Prefix) {
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
		obj := s.objects[key].meta
		page.Objects = append(page.Objects, *cloneObject(&obj))
		if len(page.Objects) >= opts.Limit {
			page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(key))
			break
		}
	}
	s.mu.RUnlock()
	sort.Strings(page.Prefixes)
	return page, nil
}

func (s *MemoryStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := public.ValidateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; !ok {
		return &public.NotFoundError{Store: s.name, Key: key}
	}
	delete(s.objects, key)
	return nil
}

func (s *MemoryStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := public.ValidatePrefix(prefix); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			delete(s.objects, key)
		}
	}
	return nil
}

func cloneObject(obj *public.Object) *public.Object {
	out := *obj
	out.Metadata = cloneMap(obj.Metadata)
	return &out
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
