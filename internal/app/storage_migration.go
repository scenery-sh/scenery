package app

import "fmt"

// StorageMigrationError distinguishes retired selectors from a misspelled
// field. Removing them is an explicit operator step, never an automatic edit.
type StorageMigrationError struct{ Field string }

func (e *StorageMigrationError) Error() string {
	return fmt.Sprintf("%s was removed: stop legacy writers, export and verify existing storage, preserve its source, then remove the field and import the snapshot into the selected worktree; see docs/runbooks/worktree-storage-migration.md", e.Field)
}
