package storagefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Capture owns exclusive maintenance until Close. Its caller must already hold
// the stopped worktree's live lock, followed by its operation lock.
type Capture struct {
	namespace *Namespace
	lease     *namespaceLease
}

func (n *Namespace) Capture(ctx context.Context) (*Capture, error) {
	lease, err := n.acquire(ctx, true)
	if err != nil {
		return nil, err
	}
	return &Capture{namespace: n, lease: lease}, nil
}

func (c *Capture) Close() error { return c.lease.Close() }
func (c *Capture) Owner() Owner { return c.lease.owner }

// Visit opens exactly one immutable payload at a time. The reader is valid only
// during visit. Consumers must verify its size and digest while streaming.
func (c *Capture) Visit(ctx context.Context, store string, visit func(Object, io.Reader) error) error {
	return scanAllReferences(ctx, c.lease, func(ref reference) error {
		if ref.Object.Store != store {
			return nil
		}
		scope := Scope{Store: store, Tenant: ref.Object.Tenant}
		f, err := openOwned(c.lease.root, scope.versionPath(c.lease.owner.Generation, ref.Object.Key, ref.VersionID), os.O_RDONLY)
		if err != nil {
			return fmt.Errorf("%w: capture payload: %w", ErrCorrupt, err)
		}
		info, err := f.Stat()
		if err == nil && info.Size() != ref.Object.SizeBytes {
			err = ErrCorrupt
		}
		if err == nil {
			err = visit(ref.Object, contextReader{ctx: ctx, reader: f})
		}
		return errors.Join(err, f.Close())
	})
}

// ValidateLogicalObject checks portable metadata without requiring a source
// ETag or internal version identity. Import always assigns a fresh ETag.
func ValidateLogicalObject(object Object) error {
	if err := (Scope{Store: object.Store, Tenant: object.Tenant}).validate(); err != nil {
		return err
	}
	if err := ValidateKey(object.Key); err != nil {
		return err
	}
	if object.SizeBytes < 0 || !isHexID(object.SHA256, 32) || object.ModifiedAt.IsZero() {
		return ErrInvalid
	}
	if err := validateMetadata(object.ContentType, object.Metadata); err != nil {
		return err
	}
	return validateListDescriptor(object)
}
