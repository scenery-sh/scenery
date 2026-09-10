package storagefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type reclaimMaterial struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Bytes      int64  `json:"bytes"`
	ModifiedNS int64  `json:"modified_ns"`
	Device     uint64 `json:"device"`
	Inode      uint64 `json:"inode"`
}

func materialFor(name, kind string, info os.FileInfo) (reclaimMaterial, error) {
	if err := checkOwned(info, false); err != nil {
		return reclaimMaterial{}, err
	}
	device, inode := fileIdentity(info)
	return reclaimMaterial{Path: name, Kind: kind, Bytes: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Device: device, Inode: inode}, nil
}

// Validate every reference and referenced descriptor before considering any
// reclamation. A missing/corrupt reference is never inferred to be garbage.
func (n *Namespace) validateReclamation(ctx context.Context, lease *namespaceLease) error {
	return scanAllReferences(ctx, lease, func(ref reference) error {
		scope := Scope{Store: ref.Object.Store, Tenant: ref.Object.Tenant}
		f, err := n.io.openPayload(lease.root, scope.versionPath(lease.owner.Generation, ref.Object.Key, ref.VersionID))
		if err != nil {
			return fmt.Errorf("%w: referenced payload: %w", ErrCorrupt, err)
		}
		info, statErr := f.Stat()
		closeErr := f.Close()
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		if info.Size() != ref.Object.SizeBytes {
			return fmt.Errorf("%w: referenced payload length differs", ErrCorrupt)
		}
		return nil
	})
}

func scanGenerationMaterials(ctx context.Context, lease *namespaceLease, inactive bool, visit func(reclaimMaterial) error) error {
	base := generationPath(lease.owner.Generation)
	versions := filepath.Join(base, "versions")
	err := scanTokenDirectories(ctx, lease.root, versions, []string{"store", "tenant", "shard", "key"}, func(dir string) error {
		rel, err := filepath.Rel(versions, dir)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if parts[2] != parts[3][:2] {
			return fmt.Errorf("%w: version shard mismatch", ErrCorrupt)
		}
		refPath := filepath.Join(base, "refs", rel+".json")
		ref, err := readReferenceAt(lease, refPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: version reference: %w", ErrCorrupt, err)
		}
		current := ""
		if err == nil {
			current = ref.VersionID
		}
		return scanDirectory(ctx, lease.root, dir, func(info os.FileInfo) error {
			version, ok := strings.CutSuffix(info.Name(), ".data")
			if !ok || !isHexID(version, 16) {
				return fmt.Errorf("%w: unknown version file", ErrCorrupt)
			}
			material, err := materialFor(filepath.Join(dir, info.Name()), "unreferenced_version", info)
			if err != nil {
				return err
			}
			if version == current && !inactive {
				return nil
			}
			return visit(material)
		})
	})
	if err != nil {
		return err
	}
	err = scanDirectory(ctx, lease.root, filepath.Join(base, "staging"), func(info os.FileInfo) error {
		if isOrderedRun(info.Name()) {
			// Ordered scan runs are operation scratch; reclaim removes stale
			// runs before starting a new ordered material scan.
			return nil
		}
		if !isHexID(info.Name(), 16) {
			return fmt.Errorf("%w: unknown staging material", ErrCorrupt)
		}
		material, err := materialFor(filepath.Join(base, "staging", info.Name()), "abandoned_upload", info)
		if err != nil {
			return err
		}
		return visit(material)
	})
	if err != nil {
		return err
	}
	return scanTokenDirectories(ctx, lease.root, filepath.Join(base, "refs"), []string{"store", "tenant", "shard"}, func(dir string) error {
		return scanDirectory(ctx, lease.root, dir, func(info os.FileInfo) error {
			if !isReferenceTemp(info.Name()) {
				if inactive {
					name := filepath.Join(dir, info.Name())
					if _, err := readReferenceAt(lease, name); err != nil {
						return err
					}
					material, err := materialFor(name, "retired_reference", info)
					if err != nil {
						return err
					}
					return visit(material)
				}
				return nil
			}
			material, err := materialFor(filepath.Join(dir, info.Name()), "abandoned_reference", info)
			if err != nil {
				return err
			}
			return visit(material)
		})
	})
}

func (n *Namespace) syncReferenceDirectories(ctx context.Context, lease *namespaceLease) error {
	base := filepath.Join(generationPath(lease.owner.Generation), "refs")
	// Sync children and parents even when no references remain: reference
	// absence must be durable before the corresponding payload is removed.
	var walk func(string, []string) error
	walk = func(dir string, kinds []string) error {
		if len(kinds) > 0 {
			if err := scanTokenDirectories(ctx, lease.root, dir, kinds[:1], func(child string) error { return walk(child, kinds[1:]) }); err != nil {
				return err
			}
		}
		return n.io.syncDirectory(lease.root, dir)
	}
	return walk(base, []string{"store", "tenant", "shard"})
}
