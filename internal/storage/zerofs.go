package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hugelgupf/p9/linux"
	"github.com/hugelgupf/p9/p9"

	public "scenery.sh/storage"
)

type ZeroFSStore struct {
	name           string
	socketPath     string
	prefix         string
	maxObjectBytes int64
}

type ZeroFSStoreOptions struct {
	Prefix         string
	MaxObjectBytes int64
}

func NewZeroFSStore(name, socketPath string, opts ZeroFSStoreOptions) *ZeroFSStore {
	prefix := strings.Trim(strings.TrimSpace(opts.Prefix), "/")
	if prefix == "" {
		prefix = name
	}
	return &ZeroFSStore{name: name, socketPath: socketPath, prefix: prefix, maxObjectBytes: opts.MaxObjectBytes}
}

func (s *ZeroFSStore) Put(ctx context.Context, key string, body io.Reader, opts public.PutOptions) (*public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := public.ValidateKey(key); err != nil {
		return nil, err
	}
	session, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	if opts.IfNoneMatch {
		if _, err := s.headWithSession(ctx, session.root, key); err == nil {
			return nil, &public.AlreadyExistsError{Store: s.name, Key: key}
		} else if !isP9NotFound(err) {
			return nil, err
		}
	}
	dir, base, err := s.ensureParent(ctx, session.root, key)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	tmpName, file, err := createP9TempFile(dir)
	if err != nil {
		return nil, err
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = dir.UnlinkAt(tmpName, 0)
		}
	}()
	fileClosed := false
	defer func() {
		if !fileClosed {
			_ = file.Close()
		}
	}()
	h := sha256.New()
	reader := io.Reader(body)
	if s.maxObjectBytes > 0 {
		reader = io.LimitReader(body, s.maxObjectBytes+1)
	}
	n, err := copyToP9File(ctx, file, io.TeeReader(reader, h))
	if err != nil {
		return nil, err
	}
	if s.maxObjectBytes > 0 && n > s.maxObjectBytes {
		return nil, fmt.Errorf("storage object %q exceeds max_object_bytes %d", key, s.maxObjectBytes)
	}
	_ = file.FSync()
	if err := file.Close(); err != nil {
		return nil, err
	}
	fileClosed = true
	if err := dir.RenameAt(tmpName, dir, base); err != nil {
		return nil, err
	}
	cleanupTemp = false
	sum := hex.EncodeToString(h.Sum(nil))
	obj, err := s.headWithSession(ctx, session.root, key)
	if err != nil {
		return nil, err
	}
	obj.SizeBytes = n
	obj.ContentType = firstNonEmpty(opts.ContentType, obj.ContentType)
	obj.ETag = `"` + sum + `"`
	obj.SHA256 = sum
	obj.Metadata = cloneMap(opts.Metadata)
	return obj, nil
}

func (s *ZeroFSStore) PutFile(ctx context.Context, key, localPath string, opts public.PutOptions) (*public.Object, error) {
	return public.PutFile(ctx, s, key, localPath, opts)
}

func (s *ZeroFSStore) Get(ctx context.Context, key string, opts public.GetOptions) (io.ReadCloser, *public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	obj, err := s.Head(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	if opts.Offset != nil {
		if *opts.Offset < 0 || *opts.Offset > obj.SizeBytes {
			return nil, nil, &public.InvalidKeyError{Key: key, Reason: "range offset is outside object"}
		}
	}
	if opts.Length != nil && *opts.Length < 0 {
		return nil, nil, &public.InvalidKeyError{Key: key, Reason: "range length must be non-negative"}
	}
	session, err := s.connect(ctx)
	if err != nil {
		return nil, nil, err
	}
	file, err := s.walkObject(session.root, key)
	if err != nil {
		_ = session.Close()
		if isP9NotFound(err) {
			return nil, nil, &public.NotFoundError{Store: s.name, Key: key}
		}
		return nil, nil, err
	}
	if _, _, err := file.Open(p9.ReadOnly); err != nil {
		_ = file.Close()
		_ = session.Close()
		return nil, nil, err
	}
	offset := int64(0)
	if opts.Offset != nil {
		offset = *opts.Offset
		obj.SizeBytes -= offset
	}
	var remaining *int64
	if opts.Length != nil {
		length := *opts.Length
		remaining = &length
		obj.SizeBytes = length
	}
	return &zeroFSReadCloser{session: session, file: file, offset: offset, remaining: remaining}, obj, nil
}

func (s *ZeroFSStore) Head(ctx context.Context, key string) (*public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := public.ValidateKey(key); err != nil {
		return nil, err
	}
	session, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return s.headWithSession(ctx, session.root, key)
}

func (s *ZeroFSStore) List(ctx context.Context, opts public.ListOptions) (*public.ListPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts, err := public.NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	session, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var keys []string
	storeRoot, err := s.walkStoreRoot(session.root)
	if err != nil {
		if isP9NotFound(err) {
			return &public.ListPage{}, nil
		}
		return nil, err
	}
	defer storeRoot.Close()
	if err := s.collectKeys(ctx, storeRoot, "", opts.Prefix, &keys); err != nil {
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
		obj, err := s.headWithSession(ctx, session.root, key)
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

func (s *ZeroFSStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := public.ValidateKey(key); err != nil {
		return err
	}
	session, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	dir, base, err := s.walkParent(session.root, key)
	if err != nil {
		if isP9NotFound(err) {
			return &public.NotFoundError{Store: s.name, Key: key}
		}
		return err
	}
	defer dir.Close()
	if err := dir.UnlinkAt(base, 0); err != nil {
		if isP9NotFound(err) {
			return &public.NotFoundError{Store: s.name, Key: key}
		}
		return err
	}
	return nil
}

func (s *ZeroFSStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := public.ValidatePrefix(prefix); err != nil {
		return err
	}
	page, err := s.List(ctx, public.ListOptions{Prefix: prefix, Limit: public.MaxListLimit})
	if err != nil {
		return err
	}
	for {
		for _, obj := range page.Objects {
			if err := s.Delete(ctx, obj.Key); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			break
		}
		page, err = s.List(ctx, public.ListOptions{Prefix: prefix, Cursor: page.NextCursor, Limit: public.MaxListLimit})
		if err != nil {
			return err
		}
	}
	return nil
}

type zeroFSSession struct {
	client *p9.Client
	root   p9.File
}

func (s *ZeroFSStore) connect(ctx context.Context) (*zeroFSSession, error) {
	if strings.TrimSpace(s.socketPath) == "" {
		return nil, fmt.Errorf("ZeroFS 9P socket is not configured")
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", s.socketPath)
	if err != nil {
		return nil, err
	}
	client, err := p9.NewClient(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	root, err := client.Attach("/")
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &zeroFSSession{client: client, root: root}, nil
}

func (s *zeroFSSession) Close() error {
	if s == nil {
		return nil
	}
	err := s.root.Close()
	return errors.Join(err, s.client.Close())
}

func (s *ZeroFSStore) ensureParent(ctx context.Context, root p9.File, key string) (p9.File, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	parts := append(pathComponents(s.prefix), pathComponents(path.Dir(key))...)
	dir, err := ensureP9Dir(root, parts)
	if err != nil {
		return nil, "", err
	}
	return dir, path.Base(key), nil
}

func (s *ZeroFSStore) walkParent(root p9.File, key string) (p9.File, string, error) {
	parts := append(pathComponents(s.prefix), pathComponents(path.Dir(key))...)
	dir, err := walkP9(root, parts)
	if err != nil {
		return nil, "", err
	}
	return dir, path.Base(key), nil
}

func (s *ZeroFSStore) walkStoreRoot(root p9.File) (p9.File, error) {
	return walkP9(root, pathComponents(s.prefix))
}

func (s *ZeroFSStore) walkObject(root p9.File, key string) (p9.File, error) {
	return walkP9(root, append(pathComponents(s.prefix), pathComponents(key)...))
}

func (s *ZeroFSStore) headWithSession(ctx context.Context, root p9.File, key string) (*public.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := s.walkObject(root, key)
	if err != nil {
		if isP9NotFound(err) {
			return nil, &public.NotFoundError{Store: s.name, Key: key}
		}
		return nil, err
	}
	defer file.Close()
	_, _, attr, err := file.GetAttr(p9.AttrMask{Size: true, MTime: true})
	if err != nil {
		return nil, err
	}
	sum, err := sha256P9File(ctx, file)
	if err != nil {
		return nil, err
	}
	return &public.Object{
		Store:       s.name,
		Key:         key,
		SizeBytes:   int64(attr.Size),
		ContentType: mime.TypeByExtension(filepath.Ext(key)),
		ETag:        `"` + sum + `"`,
		SHA256:      sum,
		ModifiedAt:  time.Unix(int64(attr.MTimeSeconds), int64(attr.MTimeNanoSeconds)).UTC(),
	}, nil
}

func (s *ZeroFSStore) collectKeys(ctx context.Context, dir p9.File, rel, prefix string, keys *[]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	listing, err := walkP9(dir, nil)
	if err != nil {
		return err
	}
	defer listing.Close()
	if _, _, err := listing.Open(p9.ReadOnly); err != nil {
		return err
	}
	offset := uint64(0)
	for {
		entries, err := listing.Readdir(offset, 8192)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		for _, entry := range entries {
			if entry.Name == "." || entry.Name == ".." {
				offset = entry.Offset
				continue
			}
			key := path.Join(rel, entry.Name)
			if entry.Type == p9.TypeDir {
				child, err := walkP9(dir, []string{entry.Name})
				if err != nil {
					return err
				}
				if err := s.collectKeys(ctx, child, key, prefix, keys); err != nil {
					_ = child.Close()
					return err
				}
				_ = child.Close()
			} else if strings.HasPrefix(key, prefix) {
				*keys = append(*keys, key)
			}
			offset = entry.Offset
		}
		if err == io.EOF {
			return nil
		}
	}
}

func ensureP9Dir(root p9.File, parts []string) (p9.File, error) {
	current, err := walkP9(root, nil)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		next, err := walkP9(current, []string{part})
		if err != nil {
			if !isP9NotFound(err) {
				_ = current.Close()
				return nil, err
			}
			if _, err := current.Mkdir(part, p9.FileMode(0o755), p9.NoUID, p9.NoGID); err != nil && !isP9Exists(err) {
				_ = current.Close()
				return nil, err
			}
			next, err = walkP9(current, []string{part})
			if err != nil {
				_ = current.Close()
				return nil, err
			}
		}
		_ = current.Close()
		current = next
	}
	return current, nil
}

func createP9TempFile(dir p9.File) (string, p9.File, error) {
	for i := 0; i < 16; i++ {
		name := fmt.Sprintf(".scenery-put-%d-%d", time.Now().UnixNano(), i)
		createDir, err := walkP9(dir, nil)
		if err != nil {
			return "", nil, err
		}
		file, _, _, err := createDir.Create(name, p9.WriteOnly, p9.FileMode(0o644), p9.NoUID, p9.NoGID)
		if err == nil {
			return name, file, nil
		}
		_ = createDir.Close()
		if !isP9Exists(err) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("allocate temporary storage object")
}

func walkP9(root p9.File, parts []string) (p9.File, error) {
	_, file, err := root.Walk(parts)
	return file, err
}

func pathComponents(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" || value == "." {
		return nil
	}
	return strings.Split(value, "/")
}

func copyToP9File(ctx context.Context, file p9.File, body io.Reader) (int64, error) {
	buf := make([]byte, 64*1024)
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return offset, err
		}
		n, readErr := body.Read(buf)
		if n > 0 {
			written, err := file.WriteAt(buf[:n], offset)
			offset += int64(written)
			if err != nil {
				return offset, err
			}
			if written != n {
				return offset, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return offset, nil
		}
		if readErr != nil {
			return offset, readErr
		}
	}
}

func sha256P9File(ctx context.Context, file p9.File) (string, error) {
	if _, _, err := file.Open(p9.ReadOnly); err != nil {
		return "", err
	}
	h := sha256.New()
	reader := &zeroFSFileReader{ctx: ctx, file: file}
	if _, err := io.Copy(h, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type zeroFSFileReader struct {
	ctx    context.Context
	file   p9.File
	offset int64
}

func (r *zeroFSFileReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.file.ReadAt(p, r.offset)
	r.offset += int64(n)
	return n, err
}

type zeroFSReadCloser struct {
	session   *zeroFSSession
	file      p9.File
	offset    int64
	remaining *int64
}

func (r *zeroFSReadCloser) Read(p []byte) (int, error) {
	if r.remaining != nil {
		if *r.remaining <= 0 {
			return 0, io.EOF
		}
		if int64(len(p)) > *r.remaining {
			p = p[:int(*r.remaining)]
		}
	}
	n, err := r.file.ReadAt(p, r.offset)
	r.offset += int64(n)
	if r.remaining != nil {
		*r.remaining -= int64(n)
	}
	return n, err
}

func (r *zeroFSReadCloser) Close() error {
	return errors.Join(r.file.Close(), r.session.Close())
}

func isP9NotFound(err error) bool {
	return errors.Is(err, linux.ENOENT)
}

func isP9Exists(err error) bool {
	return errors.Is(err, linux.EEXIST)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
