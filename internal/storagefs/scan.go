package storagefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/machine"
)

// Enumeration is deliberately unsorted and batched. Sorting/candidate bounds
// belong to the page collector, never to an all-reference directory slice.
func scanDirectory(ctx context.Context, root *os.Root, name string, visit func(os.FileInfo) error) error {
	f, err := openOwnedDirectory(root, name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := f.Readdir(32)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(entry); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func scanReferences(ctx context.Context, lease *namespaceLease, scope Scope, visit func(reference) error) error {
	partition := filepath.Join(generationPath(lease.owner.Generation), "refs", scope.partition())
	if _, err := lease.root.Lstat(partition); errors.Is(err, os.ErrNotExist) {
		return ctx.Err()
	} else if err != nil {
		return err
	}
	return scanReferencePartition(ctx, lease, partition, &scope, visit)
}

func scanReferencePartition(ctx context.Context, lease *namespaceLease, partition string, scope *Scope, visit func(reference) error) error {
	return scanDirectory(ctx, lease.root, partition, func(shard os.FileInfo) error {
		if !isHexID(shard.Name(), 1) {
			return fmt.Errorf("%w: unknown reference shard", ErrCorrupt)
		}
		if err := checkOwned(shard, true); err != nil {
			return err
		}
		dir := filepath.Join(partition, shard.Name())
		// Pin the checked shard once. Individual records still get opened,
		// ownership-checked and strictly decoded, without repeating the same
		// namespace parent checks for every reference in the shard.
		root, err := openReferenceShard(lease.root, dir, shard)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		return scanDirectory(ctx, root, ".", func(entry os.FileInfo) error {
			if isReferenceTemp(entry.Name()) {
				return nil
			}
			hash, ok := strings.CutSuffix(entry.Name(), ".json")
			if !ok || !isHexID(hash, 32) || hash[:2] != shard.Name() {
				return fmt.Errorf("%w: unknown reference filename", ErrCorrupt)
			}
			if err := checkOwned(entry, false); err != nil {
				return err
			}
			ref, err := readReferenceFrom(root, entry.Name(), lease.owner.Generation, filepath.Join(dir, entry.Name()))
			if err != nil {
				return err
			}
			if scope != nil && (ref.Object.Store != scope.Store || ref.Object.Tenant != scope.Tenant) {
				return fmt.Errorf("%w: reference partition mismatch", ErrCorrupt)
			}
			return visit(ref)
		})
	})
}

func openReferenceShard(parent *os.Root, name string, expected os.FileInfo) (*os.Root, error) {
	// Validate each parent before OpenRoot, and reject a shard replacement or
	// symlink alias by comparing the opened identity with the enumerated entry.
	current, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := checkOwned(current, true); err != nil {
		return nil, err
	}
	if !os.SameFile(expected, current) {
		return nil, ErrOwnership
	}
	f, err := openOwnedDirectory(parent, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(expected, info) {
		return nil, ErrOwnership
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = ErrOwnership
	}
	if err == nil {
		err = checkOwned(opened, true)
	}
	if err == nil {
		current, err = parent.Lstat(name)
		if err == nil {
			err = checkOwned(current, true)
		}
		if err == nil && !os.SameFile(opened, current) {
			err = ErrOwnership
		}
	}
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func readReferenceAt(lease *namespaceLease, name string) (reference, error) {
	return readReferenceFrom(lease.root, name, lease.owner.Generation, name)
}

func readReferenceFrom(root *os.Root, record, generation, address string) (reference, error) {
	data, err := readRecord(root, record)
	if err != nil {
		return reference{}, err
	}
	var ref reference
	if err := machine.DecodeArtifact(data, &ref, &ref.ArtifactIdentity, referenceKind, referenceDescriptor, "inspect storage with the matching Scenery binary"); err != nil {
		return reference{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if err := validateReferenceAt(ref, generation, address); err != nil {
		return reference{}, err
	}
	return ref, nil
}

func validateReferenceAt(ref reference, generation, name string) error {
	scope := Scope{Store: ref.Object.Store, Tenant: ref.Object.Tenant}
	if err := scope.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if err := ref.validate(scope, ref.Object.Key); err != nil {
		return err
	}
	if scope.refPath(generation, ref.Object.Key) != name {
		return fmt.Errorf("%w: reference address mismatch", ErrCorrupt)
	}
	return nil
}

func scanAllReferences(ctx context.Context, lease *namespaceLease, visit func(reference) error) error {
	root := filepath.Join(generationPath(lease.owner.Generation), "refs")
	return scanTokenDirectories(ctx, lease.root, root, []string{"store", "tenant"}, func(partition string) error {
		return scanReferencePartition(ctx, lease, partition, nil, visit)
	})
}

func scanTokenDirectories(ctx context.Context, r *os.Root, root string, kinds []string, visit func(string) error) error {
	if len(kinds) == 0 {
		return visit(root)
	}
	return scanDirectory(ctx, r, root, func(info os.FileInfo) error {
		valid := isHexID(info.Name(), 32)
		if kinds[0] == "tenant" {
			valid = valid || info.Name() == "unscoped"
		}
		if kinds[0] == "shard" {
			valid = isHexID(info.Name(), 1)
		}
		if !valid {
			return fmt.Errorf("%w: unknown %s directory", ErrCorrupt, kinds[0])
		}
		if err := checkOwned(info, true); err != nil {
			return err
		}
		return scanTokenDirectories(ctx, r, filepath.Join(root, info.Name()), kinds[1:], visit)
	})
}

func isReferenceTemp(name string) bool {
	if !strings.HasPrefix(name, ".") {
		return false
	}
	key, nonce, ok := strings.Cut(strings.TrimPrefix(name, "."), ".json.tmp-")
	return ok && isHexID(key, 32) && isHexID(nonce, 16)
}

func isMetadataTemp(name, base string) bool {
	nonce, ok := strings.CutPrefix(name, "."+base+".tmp-")
	return ok && isHexID(nonce, 16)
}
