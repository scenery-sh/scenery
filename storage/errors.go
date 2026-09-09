package storage

import (
	"errors"
	"fmt"
	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/storagefs"
)

var (
	ErrInvalidInput = storagefs.ErrInvalid
	ErrPrecondition = storagefs.ErrPrecondition
	ErrCorrupt      = storagefs.ErrCorrupt
	ErrOwnership    = storagefs.ErrOwnership
	ErrRetired      = storagefs.ErrRetired
	ErrRecovery     = storagefs.ErrRecovery
	ErrMigration    = storagefs.ErrMigration
)

type UncertainOutcomeError = atomicfile.PublicationError
type PartialDeleteError = storagefs.PartialDeleteError

type PreconditionError struct{ Store, Key string }

func (e *PreconditionError) Error() string {
	return fmt.Sprintf("storage version precondition failed for %q/%q", e.Store, e.Key)
}
func (e *PreconditionError) Unwrap() error { return ErrPrecondition }

func adaptError(err error, store, key string, ifAbsent bool) error {
	if errors.Is(err, storagefs.ErrNotFound) || errors.Is(err, storagefs.ErrUninitialized) {
		return &NotFoundError{Store: store, Key: key}
	}
	if errors.Is(err, storagefs.ErrInvalid) {
		return &InvalidKeyError{Key: key, Reason: err.Error()}
	}
	if errors.Is(err, storagefs.ErrPrecondition) {
		if ifAbsent {
			return &AlreadyExistsError{Store: store, Key: key}
		}
		return &PreconditionError{Store: store, Key: key}
	}
	return err
}

func (e *InvalidKeyError) Unwrap() error     { return ErrInvalidInput }
func (e *AlreadyExistsError) Unwrap() error  { return ErrPrecondition }
func (e *NotFoundError) Unwrap() error       { return storagefs.ErrNotFound }
func (e *NotConfiguredError) Unwrap() error  { return storagefs.ErrNotConfigured }
func (e *TenantRequiredError) Unwrap() error { return storagefs.ErrTenantRequired }

type InvalidKeyError struct {
	Key    string
	Reason string
}

func (e *InvalidKeyError) Error() string {
	if e == nil {
		return "invalid storage key"
	}
	if e.Reason == "" {
		return fmt.Sprintf("invalid storage key %q", e.Key)
	}
	return fmt.Sprintf("invalid storage key %q: %s", e.Key, e.Reason)
}

type NotFoundError struct {
	Store string
	Key   string
}

func (e *NotFoundError) Error() string {
	if e == nil {
		return "storage object not found"
	}
	if e.Store == "" {
		return fmt.Sprintf("storage object %q not found", e.Key)
	}
	return fmt.Sprintf("storage object %q/%q not found", e.Store, e.Key)
}

type AlreadyExistsError struct {
	Store string
	Key   string
}

func (e *AlreadyExistsError) Error() string {
	if e == nil {
		return "storage object already exists"
	}
	if e.Store == "" {
		return fmt.Sprintf("storage object %q already exists", e.Key)
	}
	return fmt.Sprintf("storage object %q/%q already exists", e.Store, e.Key)
}

type NotConfiguredError struct {
	Store string
}

func (e *NotConfiguredError) Error() string {
	if e == nil || e.Store == "" {
		return "scenery storage is not configured"
	}
	return fmt.Sprintf("scenery storage store %q is not configured", e.Store)
}

type TenantRequiredError struct {
	Store string
}

func (e *TenantRequiredError) Error() string {
	if e == nil || e.Store == "" {
		return "scenery storage tenant is required"
	}
	return fmt.Sprintf("scenery storage store %q requires a tenant", e.Store)
}
