package storage

import (
	"context"
	"io"
	"os"
	"strings"
	"time"
)

const (
	DefaultListLimit = 100
	MaxListLimit     = 1000
)

type Store interface {
	Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error)
	PutFile(ctx context.Context, key, localPath string, opts PutOptions) (*Object, error)
	Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error)
	Head(ctx context.Context, key string) (*Object, error)
	List(ctx context.Context, opts ListOptions) (*ListPage, error)
	Delete(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) error
}

type Object struct {
	Store       string            `json:"store"`
	Key         string            `json:"key"`
	SizeBytes   int64             `json:"size_bytes"`
	ContentType string            `json:"content_type,omitempty"`
	ETag        string            `json:"etag,omitempty"`
	SHA256      string            `json:"sha256,omitempty"`
	ModifiedAt  time.Time         `json:"modified_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
	IfNoneMatch bool
}

type GetOptions struct {
	Offset *int64
	Length *int64
}

type ListOptions struct {
	Prefix    string
	Delimiter string
	Cursor    string
	Limit     int
}

type ListPage struct {
	Objects    []Object `json:"objects"`
	Prefixes   []string `json:"prefixes,omitempty"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

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
	return newRuntimeStore(name, store)
}

func PutFile(ctx context.Context, store Store, key, localPath string, opts PutOptions) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return store.Put(ctx, key, file, opts)
}
