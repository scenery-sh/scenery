package storagefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Head resolves only the authoritative reference; it never opens a payload.
func (s *Store) Head(ctx context.Context, key string) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lease.Close() }()
	ref, err := readReference(lease.root, lease.owner.Generation, s.scope, key)
	if err != nil {
		return nil, err
	}
	return &ref.Object, nil
}

// Get opens exactly one immutable version and keeps its maintenance lease
// through stream Close. SizeBytes always remains the full object length.
func (s *Store) Get(ctx context.Context, key string, opts GetOptions) (io.ReadCloser, *Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, nil, err
	}
	if (opts.Offset != nil && *opts.Offset < 0) || (opts.Length != nil && *opts.Length < 0) {
		return nil, nil, fmt.Errorf("%w: negative byte range", ErrInvalid)
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (io.ReadCloser, *Object, error) { _ = lease.Close(); return nil, nil, err }
	ref, err := readReference(lease.root, lease.owner.Generation, s.scope, key)
	if err != nil {
		return fail(err)
	}
	f, err := s.namespace.io.openPayload(lease.root, s.scope.versionPath(lease.owner.Generation, key, ref.VersionID))
	if err != nil {
		return fail(fmt.Errorf("%w: referenced payload cannot be opened: %w", ErrCorrupt, err))
	}
	info, err := f.Stat()
	if err == nil && info.Size() != ref.Object.SizeBytes {
		err = fmt.Errorf("%w: referenced payload length does not match", ErrCorrupt)
	}
	if err != nil {
		_ = f.Close()
		return fail(err)
	}
	offset := int64(0)
	if opts.Offset != nil {
		offset = *opts.Offset
	}
	if offset > ref.Object.SizeBytes {
		_ = f.Close()
		return fail(fmt.Errorf("%w: offset exceeds object length", ErrInvalid))
	}
	length := ref.Object.SizeBytes - offset
	if opts.Length != nil && *opts.Length < length {
		length = *opts.Length
	}
	stream := &objectStream{file: f, lease: lease, reader: io.NewSectionReader(f, offset, length), ctx: ctx, remaining: length}
	return stream, &ref.Object, nil
}

type objectStream struct {
	file      *os.File
	lease     *namespaceLease
	reader    io.Reader
	ctx       context.Context
	once      sync.Once
	closeErr  error
	remaining int64
}

func (s *objectStream) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		_ = s.Close()
		return 0, err
	}
	n, err := s.reader.Read(p)
	s.remaining -= int64(n)
	if errors.Is(err, io.EOF) && s.remaining > 0 {
		err = fmt.Errorf("%w: payload truncated during read", ErrCorrupt)
	}
	if err != nil {
		_ = s.Close()
	}
	return n, err
}

func (s *objectStream) Close() error {
	s.once.Do(func() { s.closeErr = errors.Join(s.file.Close(), s.lease.Close()) })
	return s.closeErr
}
