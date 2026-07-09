package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	public "scenery.sh/storage"
)

type LocalStore struct {
	name           string
	dir            string
	maxObjectBytes int64
}

type LocalStoreOptions struct {
	MaxObjectBytes int64
}

func NewLocalStore(name, dir string) *LocalStore {
	return &LocalStore{name: name, dir: dir}
}

func NewLocalStoreWithOptions(name, dir string, opts LocalStoreOptions) *LocalStore {
	return &LocalStore{name: name, dir: dir, maxObjectBytes: opts.MaxObjectBytes}
}

func (s *LocalStore) Put(ctx context.Context, key string, body io.Reader, opts public.PutOptions) (*public.Object, error) {
	if opts.IfNoneMatch {
		return withStoragePutLock(s.name, key, func() (*public.Object, error) {
			return s.putUnlocked(ctx, key, body, opts)
		})
	}
	return s.putUnlocked(ctx, key, body, opts)
}

func (s *LocalStore) putUnlocked(ctx context.Context, key string, body io.Reader, opts public.PutOptions) (*public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := public.ValidateKey(key); err != nil {
		return nil, err
	}
	path := s.path(key)
	if opts.IfNoneMatch {
		if _, err := os.Stat(path); err == nil {
			return nil, &public.AlreadyExistsError{Store: s.name, Key: key}
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
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, err
	}
	if err := syncLocalDir(filepath.Dir(path)); err != nil {
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
	meta := storageMetadataSidecar{ContentType: contentType, Metadata: maps.Clone(opts.Metadata)}
	if err := s.writeMetadata(key, meta); err != nil {
		return nil, err
	}
	return &public.Object{
		Store:       s.name,
		Key:         key,
		SizeBytes:   n,
		ContentType: contentType,
		ETag:        `"` + sum + `"`,
		SHA256:      sum,
		ModifiedAt:  info.ModTime().UTC(),
		Metadata:    maps.Clone(meta.Metadata),
	}, nil
}

func (s *LocalStore) PutFile(ctx context.Context, key, localPath string, opts public.PutOptions) (*public.Object, error) {
	return public.PutFile(ctx, s, key, localPath, opts)
}

func (s *LocalStore) Get(ctx context.Context, key string, opts public.GetOptions) (io.ReadCloser, *public.Object, error) {
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
			return nil, nil, &public.NotFoundError{Store: s.name, Key: key}
		}
		return nil, nil, err
	}
	if opts.Offset != nil {
		if *opts.Offset < 0 || *opts.Offset > obj.SizeBytes {
			_ = file.Close()
			return nil, nil, &public.InvalidKeyError{Key: key, Reason: "range offset is outside object"}
		}
		if _, err := file.Seek(*opts.Offset, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, nil, err
		}
	}
	if opts.Length != nil {
		if *opts.Length < 0 {
			_ = file.Close()
			return nil, nil, &public.InvalidKeyError{Key: key, Reason: "range length must be non-negative"}
		}
		length := *opts.Length
		if opts.Offset != nil && length > obj.SizeBytes-*opts.Offset {
			length = obj.SizeBytes - *opts.Offset
		}
		obj.SizeBytes = length
		return struct {
			io.Reader
			io.Closer
		}{Reader: io.LimitReader(file, length), Closer: file}, obj, nil
	}
	if opts.Offset != nil {
		obj.SizeBytes -= *opts.Offset
	}
	return file, obj, nil
}

func (s *LocalStore) Head(ctx context.Context, key string) (*public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := public.ValidateKey(key); err != nil {
		return nil, err
	}
	path := s.path(key)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &public.NotFoundError{Store: s.name, Key: key}
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, &public.NotFoundError{Store: s.name, Key: key}
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	meta, err := s.readMetadata(key)
	if err != nil {
		return nil, err
	}
	contentType := meta.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(key))
	}
	return &public.Object{
		Store:       s.name,
		Key:         key,
		SizeBytes:   info.Size(),
		ContentType: contentType,
		ETag:        `"` + sum + `"`,
		SHA256:      sum,
		ModifiedAt:  info.ModTime().UTC(),
		Metadata:    maps.Clone(meta.Metadata),
	}, nil
}

func (s *LocalStore) List(ctx context.Context, opts public.ListOptions) (*public.ListPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := public.NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	var keys []string
	if err := filepath.WalkDir(s.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.dir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if isStorageMetadataKey(key) {
			return nil
		}
		if strings.HasPrefix(key, opts.Prefix) {
			keys = append(keys, key)
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(keys)
	prefixes := map[string]bool{}
	page := &public.ListPage{}
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

func (s *LocalStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := public.ValidateKey(key); err != nil {
		return err
	}
	if err := os.Remove(s.path(key)); err != nil {
		if os.IsNotExist(err) {
			return &public.NotFoundError{Store: s.name, Key: key}
		}
		return err
	}
	if err := syncLocalDir(filepath.Dir(s.path(key))); err != nil {
		return err
	}
	if err := s.deleteMetadata(key); err != nil {
		return err
	}
	return nil
}

func (s *LocalStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := public.ValidatePrefix(prefix); err != nil {
		return err
	}
	return filepath.WalkDir(s.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(s.dir, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if isStorageMetadataKey(key) {
			return nil
		}
		if strings.HasPrefix(key, prefix) {
			if err := os.Remove(path); err != nil {
				return err
			}
			if err := syncLocalDir(filepath.Dir(path)); err != nil {
				return err
			}
			if err := s.deleteMetadata(key); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *LocalStore) path(key string) string {
	return filepath.Join(s.dir, filepath.FromSlash(key))
}

func (s *LocalStore) metadataPath(key string) string {
	return filepath.Join(s.dir, filepath.FromSlash(storageMetadataKey(key)))
}

func (s *LocalStore) writeMetadata(key string, meta storageMetadataSidecar) error {
	path := s.metadataPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".scenery-meta-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		_ = tmp.Close()
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := json.NewEncoder(tmp).Encode(meta); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if err := syncLocalDir(filepath.Dir(path)); err != nil {
		return err
	}
	removeTmp = false
	return nil
}

func (s *LocalStore) readMetadata(key string) (storageMetadataSidecar, error) {
	data, err := os.ReadFile(s.metadataPath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return storageMetadataSidecar{}, nil
		}
		return storageMetadataSidecar{}, err
	}
	var meta storageMetadataSidecar
	if err := json.Unmarshal(data, &meta); err != nil {
		return storageMetadataSidecar{}, err
	}
	meta.Metadata = maps.Clone(meta.Metadata)
	return meta, nil
}

func (s *LocalStore) deleteMetadata(key string) error {
	path := s.metadataPath(key)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncLocalDir(filepath.Dir(path))
}

func fileSHA256(path string) (string, error) {
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

type storageMetadataSidecar struct {
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func storageMetadataKey(key string) string {
	return "__scenery/metadata/" + key + ".json"
}

func isStorageMetadataKey(key string) bool {
	return strings.HasPrefix(key, "__scenery/metadata/")
}

func syncLocalDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}
