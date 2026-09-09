package storagefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/stateupgrade"
)

// SpecUpgrade keeps maintenance exclusion from metadata validation through
// transaction publication. It never allocates or rewrites object payloads.
type SpecUpgrade struct {
	Changes []stateupgrade.Change
	root    *os.Root
	lock    *fileLease
	owner   Owner
	bytes   int64
}

func (u *SpecUpgrade) Close() error { return errors.Join(u.lock.Close(), u.root.Close()) }

func PrepareSpecUpgrade(ctx context.Context, path string, binding Binding) (*SpecUpgrade, error) {
	if err := binding.validate(); err != nil {
		return nil, err
	}
	if !binding.Managed || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "storage" || filepath.Base(filepath.Dir(path)) != binding.WorktreeKey {
		return nil, ErrOwnership
	}
	r, err := openNamespaceRoot(path)
	if err != nil {
		return nil, err
	}
	lock, err := lockFile(ctx, r, "maintenance.lock", true)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	u := &SpecUpgrade{root: r, lock: lock}
	if err := u.prepare(ctx, binding); err != nil {
		_ = u.Close()
		return nil, err
	}
	sort.Slice(u.Changes, func(i, j int) bool { return u.Changes[i].Path < u.Changes[j].Path })
	return u, nil
}

func (u *SpecUpgrade) prepare(ctx context.Context, binding Binding) error {
	if _, err := u.root.Lstat("operation.json"); err == nil {
		return ErrRecovery
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := scanDirectory(ctx, u.root, ".", func(info os.FileInfo) error {
		switch info.Name() {
		case "owner.json", "maintenance.lock", "mutation.lock":
			return checkOwned(info, false)
		case "generations":
			return checkOwned(info, true)
		default:
			if isMetadataTemp(info.Name(), "owner.json") {
				return checkOwned(info, false)
			}
			return fmt.Errorf("%w: unknown namespace material", ErrCorrupt)
		}
	}); err != nil {
		return err
	}
	if err := u.prepareRecord("owner.json", &u.owner, &u.owner.ArtifactIdentity, ownerKind, ownerDescriptor); err != nil {
		return err
	}
	if u.owner.Binding != binding || !isHexID(u.owner.Incarnation, 16) || !isHexID(u.owner.Generation, 16) {
		return ErrOwnership
	}
	if u.owner.State != "ready" {
		return fmt.Errorf("%w: storage must be ready before a specification upgrade", ErrRecovery)
	}
	f, err := openOwned(u.root, "mutation.lock", os.O_RDWR)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	currentFound := false
	if err := scanDirectory(ctx, u.root, "generations", func(info os.FileInfo) error {
		id := info.Name()
		if !isHexID(id, 16) {
			return fmt.Errorf("%w: unknown generation", ErrCorrupt)
		}
		if err := checkOwned(info, true); err != nil {
			return err
		}
		currentFound = currentFound || id == u.owner.Generation
		return u.prepareGeneration(ctx, id)
	}); err != nil {
		return err
	}
	if !currentFound {
		return fmt.Errorf("%w: active storage generation is missing", ErrCorrupt)
	}
	return nil
}

func (u *SpecUpgrade) prepareGeneration(ctx context.Context, id string) error {
	if err := validateGenerationLayout(ctx, u.root, id); err != nil {
		return err
	}
	base := generationPath(id)
	var gen generation
	if err := u.prepareRecord(filepath.Join(base, "generation.json"), &gen, &gen.ArtifactIdentity, generationKind, generationDescriptor); err != nil {
		return err
	}
	if gen.Binding != u.owner.Binding || gen.Incarnation != u.owner.Incarnation || gen.Generation != id {
		return ErrOwnership
	}
	return scanTokenDirectories(ctx, u.root, filepath.Join(base, "refs"), []string{"store", "tenant", "shard"}, func(dir string) error {
		return scanDirectory(ctx, u.root, dir, func(info os.FileInfo) error {
			if err := checkOwned(info, false); err != nil {
				return err
			}
			if isReferenceTemp(info.Name()) {
				return nil
			}
			hash, ok := strings.CutSuffix(info.Name(), ".json")
			if !ok || !isHexID(hash, 32) || hash[:2] != filepath.Base(dir) {
				return fmt.Errorf("%w: unknown reference filename", ErrCorrupt)
			}
			name := filepath.Join(dir, info.Name())
			var ref reference
			if err := u.prepareRecord(name, &ref, &ref.ArtifactIdentity, referenceKind, referenceDescriptor); err != nil {
				return err
			}
			if err := validateReferenceAt(ref, id, name); err != nil {
				return err
			}
			scope := Scope{Store: ref.Object.Store, Tenant: ref.Object.Tenant}
			payload, err := openOwned(u.root, scope.versionPath(id, ref.Object.Key, ref.VersionID), os.O_RDONLY)
			if err != nil {
				return err
			}
			stat, statErr := payload.Stat()
			if err := errors.Join(statErr, payload.Close()); err != nil {
				return err
			}
			if stat.Size() != ref.Object.SizeBytes {
				return fmt.Errorf("%w: reference payload size differs", ErrCorrupt)
			}
			return nil
		})
	})
}

func (u *SpecUpgrade) prepareRecord(name string, target any, identity *machine.ArtifactIdentity, kind, descriptor string) error {
	before, err := readRecord(u.root, name)
	if err != nil {
		return err
	}
	after, err := machine.PrepareArtifactSpecUpgrade(before, target, identity, kind, descriptor)
	if err != nil {
		return err
	}
	u.bytes += int64(len(before)) + int64(len(after))
	if u.bytes > 64<<20 {
		return fmt.Errorf("%w: upgrade metadata exceeds the 64 MiB selection limit", ErrCorrupt)
	}
	u.Changes = append(u.Changes, stateupgrade.Change{Path: filepath.Join("storage", name), Kind: kind, Before: before, After: after})
	return nil
}
