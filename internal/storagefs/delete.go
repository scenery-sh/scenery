package storagefs

import (
	"context"
	"errors"
	"path/filepath"

	"scenery.sh/internal/atomicfile"
)

func (s *Store) Delete(ctx context.Context, key string, opts DeleteOptions) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return err
	}
	defer func() { _ = lease.Close() }()
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", true)
	if err != nil {
		return err
	}
	defer func() { _ = mutation.Close() }()
	ref, readErr := readReference(lease.root, lease.owner.Generation, s.scope, key)
	if err := checkCondition(ref, readErr, false, opts.IfMatch); err != nil {
		return err
	}
	if errors.Is(readErr, ErrNotFound) {
		return nil
	}
	return s.deleteHeld(ctx, lease, key)
}

func (s *Store) deleteHeld(ctx context.Context, lease *namespaceLease, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name := s.scope.refPath(lease.owner.Generation, key)
	if err := s.namespace.io.remove(lease.root, name); err != nil {
		return err
	}
	if err := s.namespace.io.syncDirectory(lease.root, filepath.Dir(name)); err != nil {
		return &atomicfile.PublicationError{Err: err}
	}
	return nil
}
