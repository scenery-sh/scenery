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
		return scanDirectory(ctx, lease.root, dir, func(entry os.FileInfo) error {
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
			ref, err := readReferenceAt(lease, filepath.Join(dir, entry.Name()))
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

func readReferenceAt(lease *namespaceLease, name string) (reference, error) {
	data, err := readRecord(lease.root, name)
	if err != nil {
		return reference{}, err
	}
	var ref reference
	if err := machine.DecodeArtifact(data, &ref, &ref.ArtifactIdentity, referenceKind, referenceDescriptor, "inspect storage with the matching Scenery binary"); err != nil {
		return reference{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	scope := Scope{Store: ref.Object.Store, Tenant: ref.Object.Tenant}
	if err := scope.validate(); err != nil {
		return reference{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if err := ref.validate(scope, ref.Object.Key); err != nil {
		return reference{}, err
	}
	if scope.refPath(lease.owner.Generation, ref.Object.Key) != name {
		return reference{}, fmt.Errorf("%w: reference address mismatch", ErrCorrupt)
	}
	return ref, nil
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
