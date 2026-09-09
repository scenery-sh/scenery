package storagefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/machine"
)

func readReference(r *os.Root, generation string, scope Scope, key string) (reference, error) {
	var ref reference
	data, err := readRecord(r, scope.refPath(generation, key))
	if errors.Is(err, os.ErrNotExist) {
		return ref, ErrNotFound
	}
	if err != nil {
		return ref, fmt.Errorf("%w: read reference: %w", ErrCorrupt, err)
	}
	if err := machine.DecodeArtifact(data, &ref, &ref.ArtifactIdentity, referenceKind, referenceDescriptor, "inspect storage using the matching Scenery binary"); err != nil {
		return reference{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if err := ref.validate(scope, key); err != nil {
		return reference{}, err
	}
	return ref, nil
}

// Parents are created from the anchored root and checked component by
// component. Synchronize each new directory entry before any reference can
// select a payload beneath it.
func (d diskIO) makeParents(r *os.Root, name string) error {
	current := ""
	for part := range strings.SplitSeq(filepath.Dir(name), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := r.Mkdir(current, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := checkDirectory(r, current); err != nil {
			return err
		}
		if err := d.syncDirectory(r, filepath.Dir(current)); err != nil {
			return err
		}
	}
	return nil
}

func checkCondition(current reference, readErr error, ifNone bool, ifMatch string) error {
	if readErr != nil && !errors.Is(readErr, ErrNotFound) {
		return readErr
	}
	if ifNone && readErr == nil {
		return ErrPrecondition
	}
	if ifMatch != "" && (readErr != nil || current.Object.ETag != ifMatch) {
		return ErrPrecondition
	}
	return nil
}
