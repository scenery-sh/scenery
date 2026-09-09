package storagefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"scenery.sh/internal/machine"
)

// Namespace handles bind one allocation. A handle cannot revive a retired
// allocation or silently follow a later incarnation at the same path.
type Namespace struct {
	Path        string
	Binding     Binding
	Incarnation string
	io          diskIO
}

// Bind constructs a non-allocating handle from an already-issued runtime
// descriptor. Every operation rechecks this exact incarnation against disk.
func Bind(path string, binding Binding, incarnation string) (*Namespace, error) {
	if err := binding.validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !isHexID(incarnation, 16) {
		return nil, ErrOwnership
	}
	return &Namespace{Path: path, Binding: binding, Incarnation: incarnation, io: durableIO()}, nil
}

func (n *Namespace) CheckReady(ctx context.Context) error {
	lease, err := n.acquire(ctx, false)
	if err != nil {
		return err
	}
	return lease.Close()
}

// HoldTask excludes snapshot capture while an owned task can write database
// state as well as files. Ordinary object calls retain their shorter leases.
func (n *Namespace) HoldTask(ctx context.Context) (io.Closer, error) { return n.acquire(ctx, false) }

// Discover never creates state and remains available during restore recovery.
func Discover(ctx context.Context, path string, binding Binding) (Owner, error) {
	if err := binding.validate(); err != nil {
		return Owner{}, err
	}
	r, err := openNamespaceRoot(path)
	if errors.Is(err, os.ErrNotExist) {
		return Owner{}, ErrUninitialized
	}
	if err != nil {
		return Owner{}, err
	}
	defer func() { _ = r.Close() }()
	lock, err := lockFile(ctx, r, "maintenance.lock", false)
	if err != nil {
		return Owner{}, fmt.Errorf("%w: maintenance lock: %w", ErrCorrupt, err)
	}
	defer func() { _ = lock.Close() }()
	return loadOwner(r, binding, "", true)
}

func Open(ctx context.Context, path string, binding Binding) (*Namespace, error) {
	owner, err := Discover(ctx, path, binding)
	if err != nil {
		return nil, err
	}
	if owner.State != "ready" {
		return nil, ErrRetired
	}
	return &Namespace{Path: path, Binding: binding, Incarnation: owner.Incarnation, io: durableIO()}, nil
}

// OpenExternal distinguishes an empty explicit root from both legacy payloads
// and an interrupted current allocation, without creating any file or lock.
func OpenExternal(ctx context.Context, path string, binding Binding) (*Namespace, error) {
	if binding.Managed {
		return nil, ErrOwnership
	}
	r, err := openNamespaceRoot(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrUninitialized
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if _, err := r.Lstat("owner.json"); err == nil {
		return Open(ctx, path, binding)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	empty := true
	current := false
	if err := scanDirectory(ctx, r, ".", func(info os.FileInfo) error {
		empty = false
		if info.Name() == "maintenance.lock" || info.Name() == "mutation.lock" || info.Name() == "generations" || info.Name() == "operation.json" {
			current = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if current {
		return nil, ErrCorrupt
	}
	if !empty {
		return nil, ErrMigration
	}
	return nil, ErrUninitialized
}

// Allocate requires the caller's retained-worktree operation lease (or explicit
// external-root allocation authority). It never manufactures that authority.
// An interrupted recorded allocation can only complete the same identity.
func Allocate(ctx context.Context, path string, binding Binding) (*Namespace, error) {
	return allocate(ctx, path, binding, durableIO())
}

func allocate(ctx context.Context, path string, binding Binding, disk diskIO) (*Namespace, error) {
	return allocateRoot(ctx, path, binding, disk, false)
}

// AllocateExternal initializes only an explicitly selected empty private root.
// A nonempty pre-format directory is migration evidence, never a local backend.
func AllocateExternal(ctx context.Context, path string, binding Binding) (*Namespace, error) {
	if binding.Managed {
		return nil, ErrOwnership
	}
	r, err := openNamespaceRoot(path)
	if errors.Is(err, os.ErrNotExist) {
		return Allocate(ctx, path, binding)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if _, err := r.Lstat("owner.json"); err == nil {
		return Allocate(ctx, path, binding)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	dir, err := openOwnedDirectory(r, ".")
	if err != nil {
		return nil, err
	}
	entries, readErr := dir.Readdirnames(1)
	closeErr := dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) != 0 {
		return nil, ErrMigration
	}
	return allocateRoot(ctx, path, binding, durableIO(), true)
}

func allocateRoot(ctx context.Context, path string, binding Binding, disk diskIO, initializeEmpty bool) (*Namespace, error) {
	if err := binding.validate(); err != nil {
		return nil, err
	}
	openParent := openNamespaceRoot
	if !binding.Managed {
		openParent = openExternalParent
	}
	parent, err := openParent(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	created := false
	if err := parent.Mkdir(filepath.Base(path), 0o700); err == nil {
		created = true
		if err := disk.syncDirectory(parent, "."); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	} else if initializeEmpty {
		created = true
	}
	r, err := openNamespaceRoot(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if created {
		for _, name := range []string{"maintenance.lock", "mutation.lock"} {
			f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if err != nil {
				return nil, err
			}
			err = disk.syncFile(f)
			closeErr := f.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		if err := disk.syncDirectory(r, "."); err != nil {
			return nil, err
		}
	}
	lock, err := lockFile(ctx, r, "maintenance.lock", true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	var owner Owner
	if _, err := r.Lstat("operation.json"); err == nil {
		return nil, ErrRecovery
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if created {
		incarnation, err := randomID()
		if err != nil {
			return nil, err
		}
		gen, err := randomID()
		if err != nil {
			return nil, err
		}
		owner = Owner{ArtifactIdentity: machine.NewArtifactIdentity(ownerKind, ownerDescriptor), Binding: binding, Incarnation: incarnation, Generation: gen, State: "allocating"}
		if err := disk.writeRecord(r, "owner.json", owner); err != nil {
			return nil, err
		}
	} else {
		owner, err = readOwner(r, binding, "")
		if err != nil {
			return nil, err
		}
		if owner.State == "ready" {
			if _, err := loadOwner(r, binding, owner.Incarnation, false); err != nil {
				return nil, err
			}
			return &Namespace{Path: path, Binding: binding, Incarnation: owner.Incarnation, io: disk}, nil
		}
		if owner.State != "allocating" {
			return nil, ErrRetired
		}
	}
	base := generationPath(owner.Generation)
	for _, name := range []string{"generations", base, filepath.Join(base, "refs"), filepath.Join(base, "versions"), filepath.Join(base, "staging")} {
		if err := r.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if err := checkDirectory(r, name); err != nil {
			return nil, err
		}
		if err := disk.syncDirectory(r, filepath.Dir(name)); err != nil {
			return nil, err
		}
	}
	g := generation{ArtifactIdentity: machine.NewArtifactIdentity(generationKind, generationDescriptor), Binding: binding, Incarnation: owner.Incarnation, Generation: owner.Generation}
	if _, err := r.Lstat(filepath.Join(base, "generation.json")); err == nil {
		if err := validateGeneration(r, binding, owner.Incarnation, owner.Generation); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else {
		for _, dir := range []string{"refs", "versions", "staging"} {
			if err := scanDirectory(ctx, r, filepath.Join(base, dir), func(os.FileInfo) error { return fmt.Errorf("%w: unrecorded generation contains material", ErrCorrupt) }); err != nil {
				return nil, err
			}
		}
		if err := disk.writeRecord(r, filepath.Join(base, "generation.json"), g); err != nil {
			return nil, err
		}
	}
	owner.State = "ready"
	if err := disk.writeRecord(r, "owner.json", owner); err != nil {
		return nil, err
	}
	return &Namespace{Path: path, Binding: binding, Incarnation: owner.Incarnation, io: disk}, nil
}

type namespaceLease struct {
	root        *os.Root
	maintenance *fileLease
	owner       Owner
}

func (n *Namespace) acquire(ctx context.Context, exclusive bool) (*namespaceLease, error) {
	if n.Incarnation == "" {
		return nil, ErrOwnership
	}
	r, err := openNamespaceRoot(n.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	l, err := lockFile(ctx, r, "maintenance.lock", exclusive)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	owner, err := loadOwner(r, n.Binding, n.Incarnation, false)
	if err != nil {
		_ = l.Close()
		_ = r.Close()
		return nil, err
	}
	return &namespaceLease{root: r, maintenance: l, owner: owner}, nil
}

func (l *namespaceLease) Close() error { return errors.Join(l.maintenance.Close(), l.root.Close()) }

func loadOwner(r *os.Root, binding Binding, incarnation string, inspection bool) (Owner, error) {
	owner, err := readOwner(r, binding, incarnation)
	if err != nil {
		return Owner{}, err
	}
	if !inspection {
		op, err := readOperation(r, owner)
		if err != nil {
			return Owner{}, err
		}
		if op != nil {
			return Owner{}, operationRecovery(op)
		}
	}
	if owner.State == "retired" {
		if inspection {
			return owner, nil
		}
		return Owner{}, ErrRetired
	}
	if owner.State != "ready" {
		return Owner{}, fmt.Errorf("%w: allocation is incomplete", ErrCorrupt)
	}
	f, err := openOwned(r, "mutation.lock", os.O_RDWR)
	if err != nil {
		return Owner{}, fmt.Errorf("%w: mutation lock: %w", ErrCorrupt, err)
	}
	if err := f.Close(); err != nil {
		return Owner{}, err
	}
	base := generationPath(owner.Generation)
	for _, name := range []string{"generations", base, filepath.Join(base, "refs"), filepath.Join(base, "versions"), filepath.Join(base, "staging")} {
		if err := checkDirectory(r, name); err != nil {
			return Owner{}, fmt.Errorf("%w: generation directory: %w", ErrCorrupt, err)
		}
	}
	var g generation
	data, err := readRecord(r, filepath.Join(base, "generation.json"))
	if err != nil {
		return Owner{}, fmt.Errorf("%w: generation record: %w", ErrCorrupt, err)
	}
	if err := machine.DecodeArtifact(data, &g, &g.ArtifactIdentity, generationKind, generationDescriptor, "inspect retained storage with the matching binary"); err != nil {
		return Owner{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if g.Binding != binding || g.Incarnation != owner.Incarnation || g.Generation != owner.Generation {
		return Owner{}, ErrOwnership
	}
	return owner, nil
}

func readOwner(r *os.Root, binding Binding, incarnation string) (Owner, error) {
	var owner Owner
	data, err := readRecord(r, "owner.json")
	if err != nil {
		return owner, fmt.Errorf("%w: owner record: %w", ErrCorrupt, err)
	}
	if err := machine.DecodeArtifact(data, &owner, &owner.ArtifactIdentity, ownerKind, ownerDescriptor, "inspect retained storage with the matching binary"); err != nil {
		return Owner{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if owner.Binding != binding || (incarnation != "" && owner.Incarnation != incarnation) {
		return Owner{}, ErrOwnership
	}
	if !isHexID(owner.Incarnation, 16) || !isHexID(owner.Generation, 16) || (owner.State != "ready" && owner.State != "allocating" && owner.State != "retired") {
		return Owner{}, ErrCorrupt
	}
	return owner, nil
}

func readRecord(r *os.Root, name string) ([]byte, error) {
	f, err := openOwned(r, name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRecordBytes {
		return nil, fmt.Errorf("%w: record exceeds %d bytes", ErrCorrupt, maxRecordBytes)
	}
	return data, nil
}

func openNamespaceRoot(path string) (*os.Root, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := checkOwned(info, true); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = ErrOwnership
	}
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func checkDirectory(r *os.Root, name string) error {
	info, err := r.Lstat(name)
	if err != nil {
		return err
	}
	return checkOwned(info, true)
}
