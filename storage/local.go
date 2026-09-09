package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"scenery.sh/internal/storagefs"
)

type localRuntimeStore struct {
	name           string
	root           string
	maxObjectBytes int64
	tenantScoped   bool
	mu             sync.Mutex
	namespace      *storagefs.Namespace
	resolve        func(context.Context, storagefs.Scope, bool) (Store, error)
}

type LocalStoreOptions struct {
	MaxObjectBytes int64
	TenantScoped   bool
}

// NewLocalStore selects an explicit external namespace, not a development
// worktree. Reads do not allocate; a first put initializes an empty private root.
func NewLocalStore(name, root string) Store {
	return NewLocalStoreWithOptions(name, root, LocalStoreOptions{})
}
func NewLocalStoreWithOptions(name, root string, opts LocalStoreOptions) Store {
	return &localRuntimeStore{name: name, root: root, maxObjectBytes: opts.MaxObjectBytes, tenantScoped: opts.TenantScoped}
}

func (s *localRuntimeStore) backend(ctx context.Context, write bool) (Store, error) {
	tenant, err := selectedTenant(ctx, s.name, s.tenantScoped)
	if err != nil {
		return nil, err
	}
	scope := storagefs.Scope{Store: s.name, Tenant: tenant}
	if err := storagefs.ValidateScope(scope); err != nil {
		return nil, err
	}
	if s.resolve != nil {
		return s.resolve(ctx, scope, write)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.namespace == nil {
		root, err := canonicalExternalRoot(s.root)
		if err != nil {
			return nil, err
		}
		binding := storagefs.Binding{AppID: "external-storage", AppRoot: root, UserID: os.Getuid()}
		n, err := storagefs.OpenExternal(ctx, root, binding)
		if write && errors.Is(err, storagefs.ErrUninitialized) {
			n, err = storagefs.AllocateExternal(ctx, root, binding)
		}
		if err != nil {
			return nil, err
		}
		s.namespace = n
	}
	return s.namespace.Store(scope, s.maxObjectBytes)
}

func canonicalExternalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func (s *localRuntimeStore) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if err := storagefs.ValidatePutOptions(opts); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, storagefs.ErrInvalid
	}
	b, err := s.backend(ctx, true)
	if err != nil {
		return nil, err
	}
	o, err := b.Put(ctx, key, body, opts)
	return o, adaptError(err, s.name, key, opts.IfNoneMatch)
}
func (s *localRuntimeStore) Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, nil, err
	}
	b, err := s.backend(ctx, false)
	if err != nil {
		return nil, nil, adaptError(err, s.name, key, false)
	}
	r, o, err := b.Get(ctx, key, opts)
	return r, o, adaptError(err, s.name, key, false)
}
func (s *localRuntimeStore) Head(ctx context.Context, key string) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	b, err := s.backend(ctx, false)
	if err != nil {
		return nil, adaptError(err, s.name, key, false)
	}
	o, err := b.Head(ctx, key)
	return o, adaptError(err, s.name, key, false)
}
func (s *localRuntimeStore) List(ctx context.Context, opts ListOptions) (*ListPage, error) {
	opts, err := NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	b, err := s.backend(ctx, false)
	if errors.Is(err, storagefs.ErrUninitialized) && opts.Cursor == "" {
		return &ListPage{Objects: []Object{}}, nil
	}
	if err != nil {
		return nil, err
	}
	page, err := b.List(ctx, opts)
	return page, adaptError(err, s.name, "", false)
}
func (s *localRuntimeStore) Delete(ctx context.Context, key string, opts DeleteOptions) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	b, err := s.backend(ctx, false)
	if errors.Is(err, storagefs.ErrUninitialized) {
		if opts.IfMatch == "" {
			return nil
		}
		return &PreconditionError{Store: s.name, Key: key}
	}
	if err != nil {
		return err
	}
	return adaptError(b.Delete(ctx, key, opts), s.name, key, false)
}
func (s *localRuntimeStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ValidatePrefix(prefix); err != nil {
		return err
	}
	b, err := s.backend(ctx, false)
	if errors.Is(err, storagefs.ErrUninitialized) {
		return nil
	}
	if err != nil {
		return err
	}
	return b.DeletePrefix(ctx, prefix)
}
