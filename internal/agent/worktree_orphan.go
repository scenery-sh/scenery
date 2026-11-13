package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// OrphanedWorktree is the retained state of a checkout that no longer exists.
// Its record is read tolerantly: no checkout remains to upgrade incompatible
// metadata from, so the fields below are all that removal may rely on. The
// directory key authenticates the app root and the record digest binds a
// removal to the exact bytes its preview reported.
type OrphanedWorktree struct {
	Key          string
	AppRoot      string
	AppID        string
	UserID       int
	Directory    string
	UpdatedAt    time.Time
	RecordDigest string
	Postgres     *OrphanedWorktreePostgres
}

// OrphanedWorktreePostgres names the database resources an orphaned record
// claims. The caller verifies them against Docker before removing anything.
type OrphanedWorktreePostgres struct {
	InstanceID     string `json:"instance_id"`
	DaemonID       string `json:"daemon_id"`
	DaemonEndpoint string `json:"daemon_endpoint"`
	Container      string `json:"container"`
	Volume         string `json:"volume"`
}

// DiscoverOrphanedWorktrees lists retained state directories of this user
// whose authenticated app root is absent. It reads only; a directory whose
// record cannot name its root, belongs to another user or whose root cannot be
// proven absent is not an orphan.
func DiscoverOrphanedWorktrees(home string) ([]OrphanedWorktree, error) {
	discovered, err := DiscoverWorktrees(home)
	if err != nil {
		return nil, err
	}
	result := []OrphanedWorktree{}
	for _, item := range discovered {
		if item.AppRoot == "" {
			continue
		}
		paths, err := PathsForWorktree(home, item.AppRoot)
		if err != nil || paths.Key != item.Key {
			continue
		}
		orphan, err := paths.loadOrphan()
		if err != nil {
			continue
		}
		result = append(result, orphan)
	}
	return result, nil
}

func (p WorktreePaths) loadOrphan() (OrphanedWorktree, error) {
	if _, err := os.Lstat(p.AppRoot); !errors.Is(err, os.ErrNotExist) {
		return OrphanedWorktree{}, fmt.Errorf("worktree root %s is not absent", p.AppRoot)
	}
	if err := checkPrivateWorktreeFile(p.Record); err != nil {
		return OrphanedWorktree{}, err
	}
	info, err := os.Stat(p.Record)
	if err != nil {
		return OrphanedWorktree{}, err
	}
	data, err := os.ReadFile(p.Record)
	if err != nil {
		return OrphanedWorktree{}, err
	}
	var record struct {
		AppRoot   string                    `json:"app_root"`
		AppID     string                    `json:"app_id"`
		UserID    int                       `json:"user_id"`
		UpdatedAt time.Time                 `json:"updated_at"`
		Postgres  *OrphanedWorktreePostgres `json:"postgres"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return OrphanedWorktree{}, err
	}
	if record.AppRoot != p.AppRoot || record.UserID != os.Getuid() {
		return OrphanedWorktree{}, fmt.Errorf("retained worktree record does not belong to the selected root and local user")
	}
	// The last write to the record bounds its age even when an older producer
	// stored no readable timestamp.
	updated := info.ModTime().UTC()
	if record.UpdatedAt.After(updated) {
		updated = record.UpdatedAt.UTC()
	}
	sum := sha256.Sum256(data)
	return OrphanedWorktree{
		Key: p.Key, AppRoot: p.AppRoot, AppID: record.AppID, UserID: record.UserID, Directory: p.Directory,
		UpdatedAt: updated, RecordDigest: "sha256:" + hex.EncodeToString(sum[:]), Postgres: record.Postgres,
	}, nil
}

// RemoveOrphaned deletes the retained state directory of an absent checkout.
// It holds the owner and operation locks throughout, so a runtime or a
// retained-state operation that still uses the directory keeps it. The record
// must still carry recordDigest and the root must still be absent. release
// runs under the locks before any file is removed and frees the resources the
// record names outside the directory; its failure retains the directory and
// with it the authority to retry.
func (p WorktreePaths) RemoveOrphaned(recordDigest string, release func(OrphanedWorktree) error) error {
	info, err := os.Lstat(p.Directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !worktreeFileOwned(info) {
		return fmt.Errorf("worktree state directory is not private and owned by the current user: %s", p.Directory)
	}
	var locks []*ProcessLock
	defer func() {
		for _, lock := range locks {
			_ = lock.Release()
		}
	}()
	for _, path := range []string{p.LiveLock, p.OperationLock} {
		if err := checkPrivateWorktreeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		lock, err := AcquireProcessLock(path)
		if err != nil {
			return fmt.Errorf("retained worktree state is in use; nothing was removed: %w", err)
		}
		locks = append(locks, lock)
	}
	orphan, err := p.loadOrphan()
	if err != nil {
		return err
	}
	if orphan.RecordDigest != recordDigest {
		return fmt.Errorf("retained worktree record changed after its preview; nothing was removed")
	}
	if release != nil {
		if err := release(orphan); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(p.Directory); err != nil {
		return err
	}
	if p.SocketDir != p.Directory && filepath.Base(p.SocketDir) != "" {
		_ = os.RemoveAll(p.SocketDir)
	}
	return nil
}
