package storagefs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"scenery.sh/internal/machine"
)

const operationKind = "scenery.storage.operation"
const operationDescriptor = `{"identity":"artifact","binding":` + identityDescriptor + `,"operation":"string","incarnation":"string","generation":"string","selection_revision":"string","archive_sha256":"string","staged_generation":"string","staged_incarnation":"string","database":"boolean","storage":"boolean","mode":"string","on_conflict":"string","phase":"string"}`

// Operation is one bounded offline lifecycle intent, never an object journal.
type Operation struct {
	machine.ArtifactIdentity
	Binding           Binding `json:"binding"`
	Operation         string  `json:"operation"`
	Incarnation       string  `json:"incarnation"`
	Generation        string  `json:"generation"`
	SelectionRevision string  `json:"selection_revision,omitempty"`
	ArchiveSHA256     string  `json:"archive_sha256,omitempty"`
	StagedGeneration  string  `json:"staged_generation,omitempty"`
	StagedIncarnation string  `json:"staged_incarnation,omitempty"`
	Database          bool    `json:"database"`
	Storage           bool    `json:"storage"`
	Mode              string  `json:"mode,omitempty"`
	OnConflict        string  `json:"on_conflict,omitempty"`
	Phase             string  `json:"phase"`
}

func readOperation(root *os.Root, owner Owner) (*Operation, error) {
	data, err := readRecord(root, "operation.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var op Operation
	if err := machine.DecodeArtifact(data, &op, &op.ArtifactIdentity, operationKind, operationDescriptor, "resume the recorded operation with the matching binary"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if op.Binding != owner.Binding || !isHexID(op.Incarnation, 16) || !isHexID(op.Generation, 16) {
		return nil, ErrOwnership
	}
	if op.Incarnation != owner.Incarnation && (op.Operation != "restore" || op.StagedIncarnation != owner.Incarnation || owner.Generation != op.StagedGeneration || owner.State != "ready") {
		return nil, ErrOwnership
	}
	switch op.Operation {
	case "purge":
		if op.Generation != owner.Generation || op.SelectionRevision == "" || (op.Phase != "prepared" && op.Phase != "retired") {
			return nil, ErrCorrupt
		}
	case "restore":
		if (op.OnConflict != "fail" && op.OnConflict != "skip" && op.OnConflict != "overwrite") || (op.Mode != "merge" && op.OnConflict != "fail") {
			return nil, ErrCorrupt
		}
		if op.StagedIncarnation != "" && (!isHexID(op.StagedIncarnation, 16) || op.StagedIncarnation == op.Incarnation || op.Mode != "overwrite" || (owner.Incarnation == op.Incarnation && owner.State != "retired")) {
			return nil, ErrCorrupt
		}
		if !isHexID(op.ArchiveSHA256, 32) || !isHexID(op.StagedGeneration, 16) || !op.Storage || (op.Mode != "overwrite" && op.Mode != "merge") || (op.Database && op.Mode != "overwrite") || (owner.Generation != op.Generation && owner.Generation != op.StagedGeneration) || op.Generation == op.StagedGeneration {
			return nil, ErrCorrupt
		}
		if owner.Generation == op.StagedGeneration && op.Database && op.Phase == "staged" {
			return nil, ErrCorrupt
		}
		if op.Phase == "switched" && owner.Generation != op.StagedGeneration {
			return nil, ErrCorrupt
		}
		switch op.Phase {
		case "preparing":
			if owner.Generation != op.Generation || (op.StagedIncarnation == "" && owner.State != "ready") || (op.StagedIncarnation != "" && owner.State != "retired") {
				return nil, ErrCorrupt
			}
		case "staged", "database", "switched":
		default:
			return nil, ErrCorrupt
		}
	default:
		return nil, ErrCorrupt
	}
	return &op, nil
}

// Pending is read-only diagnostics and remains available behind the recovery
// barrier. It never clears or advances an operation.
func (n *Namespace) Pending(ctx context.Context) (*Operation, error) {
	r, err := openNamespaceRoot(n.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	l, err := lockFile(ctx, r, "maintenance.lock", false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()
	owner, err := readOwner(r, n.Binding, n.Incarnation)
	if err != nil {
		return nil, err
	}
	return readOperation(r, owner)
}

func validateGeneration(r *os.Root, binding Binding, incarnation, id string) error {
	if !isHexID(id, 16) {
		return ErrCorrupt
	}
	base := generationPath(id)
	if err := scanDirectory(context.Background(), r, base, func(info os.FileInfo) error {
		switch info.Name() {
		case "refs", "versions", "staging":
			return checkOwned(info, true)
		case "generation.json":
			return checkOwned(info, false)
		default:
			return fmt.Errorf("%w: unknown generation material", ErrCorrupt)
		}
	}); err != nil {
		return err
	}
	for _, dir := range []string{base, filepath.Join(base, "refs"), filepath.Join(base, "versions"), filepath.Join(base, "staging")} {
		if err := checkDirectory(r, dir); err != nil {
			return fmt.Errorf("%w: generation directory: %w", ErrCorrupt, err)
		}
	}
	data, err := readRecord(r, filepath.Join(base, "generation.json"))
	if err != nil {
		return fmt.Errorf("%w: generation record: %w", ErrCorrupt, err)
	}
	var gen generation
	if err := machine.DecodeArtifact(data, &gen, &gen.ArtifactIdentity, generationKind, generationDescriptor, "inspect storage with the matching binary"); err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if gen.Binding != binding || gen.Incarnation != incarnation || gen.Generation != id {
		return ErrOwnership
	}
	return nil
}

func scanOrderedGenerations(ctx context.Context, lease *namespaceLease, visit func(*namespaceLease) error) error {
	last := ""
	for {
		h := &entryHeap{present: make(map[string]bool), capacity: 128}
		if err := scanDirectory(ctx, lease.root, "generations", func(info os.FileInfo) error {
			if !isHexID(info.Name(), 16) {
				return fmt.Errorf("%w: unknown generation", ErrCorrupt)
			}
			if err := checkOwned(info, true); err != nil {
				return err
			}
			if info.Name() > last {
				h.add(listEntry{key: info.Name(), kind: "generation"})
			}
			return nil
		}); err != nil {
			return err
		}
		if h.Len() == 0 {
			return nil
		}
		sort.Slice(h.entries, func(i, j int) bool { return h.entries[i].key < h.entries[j].key })
		for _, entry := range h.entries {
			if err := validateGeneration(lease.root, lease.owner.Binding, lease.owner.Incarnation, entry.key); err != nil {
				return err
			}
			selected := *lease
			selected.owner.Generation = entry.key
			if err := visit(&selected); err != nil {
				return err
			}
			last = entry.key
		}
		if h.Len() < 128 {
			return nil
		}
	}
}
