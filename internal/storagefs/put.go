package storagefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"os"
	"path"
	"path/filepath"
	"time"

	"scenery.sh/internal/machine"
)

func (s *Store) Put(ctx context.Context, key string, body io.Reader, opts PutOptions) (*Object, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("%w: missing upload body", ErrInvalid)
	}
	if opts.IfNoneMatch && opts.IfMatch != "" {
		return nil, fmt.Errorf("%w: write preconditions are mutually exclusive", ErrInvalid)
	}
	if opts.ContentType == "" {
		opts.ContentType = mime.TypeByExtension(path.Ext(key))
	}
	if err := validateMetadata(opts.ContentType, opts.Metadata); err != nil {
		return nil, err
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lease.Close() }()
	version, err := randomID()
	if err != nil {
		return nil, err
	}
	etag, err := randomID()
	if err != nil {
		return nil, err
	}
	stage := filepath.Join(generationPath(lease.owner.Generation), "staging", version)
	f, err := lease.root.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lease.root.Remove(stage) }()
	hash := sha256.New()
	reader := io.Reader(contextReader{ctx: ctx, reader: body})
	if s.maxBytes > 0 && s.maxBytes < 1<<63-1 {
		reader = io.LimitReader(reader, s.maxBytes+1)
	}
	size, copyErr := io.CopyBuffer(io.MultiWriter(f, hash), reader, make([]byte, 32<<10))
	if copyErr == nil {
		copyErr = ctx.Err()
	}
	if copyErr == nil && s.maxBytes > 0 && size > s.maxBytes {
		copyErr = fmt.Errorf("%w: object exceeds %d bytes", ErrInvalid, s.maxBytes)
	}
	if copyErr == nil {
		copyErr = s.namespace.io.syncFile(f)
	}
	closeErr := f.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	ref := reference{
		ArtifactIdentity: machine.NewArtifactIdentity(referenceKind, referenceDescriptor),
		Object:           Object{Store: s.scope.Store, Tenant: s.scope.Tenant, Key: key, SizeBytes: size, ContentType: opts.ContentType, ETag: `"` + etag + `"`, SHA256: hex.EncodeToString(hash.Sum(nil)), ModifiedAt: time.Now().UTC(), Metadata: maps.Clone(opts.Metadata)},
		VersionID:        version,
	}
	if err := validateListDescriptor(ref.Object); err != nil {
		return nil, err
	}
	payload := s.scope.versionPath(lease.owner.Generation, key, version)
	if err := s.namespace.io.makeParents(lease.root, payload); err != nil {
		return nil, err
	}
	if err := s.publishPayload(ctx, lease, stage, payload); err != nil {
		return nil, err
	}
	// The immutable version is now durable. Even a failed condition leaves it
	// for explicit reclamation; no error path guesses publication ownership.
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mutation.Close() }()
	if _, err := loadOwner(lease.root, s.namespace.Binding, s.namespace.Incarnation, false); err != nil {
		return nil, err
	}
	current, readErr := readReference(lease.root, lease.owner.Generation, s.scope, key)
	if err := checkCondition(current, readErr, opts.IfNoneMatch, opts.IfMatch); err != nil {
		return nil, err
	}
	refPath := s.scope.refPath(lease.owner.Generation, key)
	if err := s.namespace.io.makeParents(lease.root, refPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.namespace.io.writeRecord(lease.root, refPath, ref); err != nil {
		return nil, err
	}
	return &ref.Object, nil
}

// Serialize only the no-reuse check and rename, not the upload. All namespace
// writers use this same stable lock; a nonce collision never overwrites bytes.
func (s *Store) publishPayload(ctx context.Context, lease *namespaceLease, stage, payload string) error {
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", true)
	if err != nil {
		return err
	}
	defer func() { _ = mutation.Close() }()
	if _, err := lease.root.Lstat(payload); err == nil {
		return fmt.Errorf("%w: immutable version already exists", ErrCorrupt)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := s.namespace.io.rename(lease.root, stage, payload); err != nil {
		return err
	}
	if err := s.namespace.io.syncDirectory(lease.root, filepath.Dir(payload)); err != nil {
		return err
	}
	return s.namespace.io.syncDirectory(lease.root, filepath.Dir(stage))
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
