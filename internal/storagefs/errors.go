// Package storagefs implements worktree-owned immutable file storage. It owns
// filesystem mechanics, not application configuration, authentication, or SQL.
package storagefs

import "errors"

var (
	ErrUninitialized  = errors.New("storage namespace is uninitialized")
	ErrCorrupt        = errors.New("storage namespace is corrupt or incomplete")
	ErrOwnership      = errors.New("storage namespace ownership does not match")
	ErrRetired        = errors.New("storage namespace incarnation is retired")
	ErrRecovery       = errors.New("storage restore is pending; resume snapshot load with the same pinned archive")
	ErrInvalid        = errors.New("invalid storage input")
	ErrNotFound       = errors.New("storage object not found")
	ErrPrecondition   = errors.New("storage object precondition failed")
	ErrMigration      = errors.New("legacy storage requires explicit export and snapshot import")
	ErrNotConfigured  = errors.New("storage capability is not configured")
	ErrTenantRequired = errors.New("storage tenant is required")
)
