package storage

import (
	"context"
	"io"
	"scenery.sh/internal/storagefs"
	"strings"
)

const (
	DefaultListLimit = 100
	MaxListLimit     = 1000
)

type Store interface {
	Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error)
	Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error)
	Head(ctx context.Context, key string) (*Object, error)
	List(ctx context.Context, opts ListOptions) (*ListPage, error)
	Delete(ctx context.Context, key string, opts DeleteOptions) error
	// DeletePrefix may partially complete; it does not roll back deleted objects.
	DeletePrefix(ctx context.Context, prefix string) error
}

type Object = storagefs.Object
type PutOptions = storagefs.PutOptions
type GetOptions = storagefs.GetOptions
type DeleteOptions = storagefs.DeleteOptions
type ListOptions = storagefs.ListOptions
type ListPage = storagefs.ListPage

func Default(ctx context.Context) (Store, error) {
	return Named(ctx, "")
}

func Named(ctx context.Context, name string) (Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg, err := loadRuntimeConfig()
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(cfg.Default)
	}
	if name == "" {
		return nil, &NotConfiguredError{}
	}
	store, ok := cfg.Stores[name]
	if !ok {
		return nil, &NotConfiguredError{Store: name}
	}
	return newRuntimeStore(name, store, cfg.Namespace)
}
