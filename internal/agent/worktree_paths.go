package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WorktreePaths binds retained capabilities to a canonical checkout root. Home
// is explicit: process-global agent/socket overrides never participate here.
type WorktreePaths struct {
	AppRoot       string
	Key           string
	Directory     string
	Record        string
	LiveLock      string
	OperationLock string
	Socket        string
	SocketDir     string
}

func PathsForWorktree(home, appRoot string) (WorktreePaths, error) {
	if strings.TrimSpace(home) == "" || strings.TrimSpace(appRoot) == "" {
		return WorktreePaths{}, fmt.Errorf("worktree state home and app root are required")
	}
	root, err := canonicalWorktreePath(appRoot)
	if err != nil {
		return WorktreePaths{}, err
	}
	home, err = canonicalWorktreePath(home)
	if err != nil {
		return WorktreePaths{}, err
	}
	sum := sha256.Sum256([]byte(root))
	key := hex.EncodeToString(sum[:])
	dir := filepath.Join(home, "worktrees", key)
	socketDir := dir
	if len(filepath.Join(socketDir, "control.sock")) > 100 {
		// The shortened address is only a locator. The complete root and owner
		// binding must still be verified before using the control protocol.
		address := sha256.Sum256([]byte(dir))
		socketDir = filepath.Join(os.TempDir(), "scenery-"+strconv.Itoa(os.Getuid()), hex.EncodeToString(address[:16]))
		if len(filepath.Join(socketDir, "control.sock")) > 100 {
			socketDir = filepath.Join("/tmp", "scenery-"+strconv.Itoa(os.Getuid()), hex.EncodeToString(address[:16]))
		}
	}
	return WorktreePaths{
		AppRoot: root, Key: key, Directory: dir,
		Record:        filepath.Join(dir, "worktree.json"),
		LiveLock:      filepath.Join(dir, "owner.lock"),
		OperationLock: filepath.Join(dir, "operation.lock"),
		Socket:        filepath.Join(socketDir, "control.sock"), SocketDir: socketDir,
	}, nil
}

// Resolve existing ancestors too, so orphaned roots retain the same identity
// and aliases through a symlinked parent cannot obtain independent owners.
func canonicalWorktreePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(abs)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve worktree path: %w", err)
		}
		if _, statErr := os.Lstat(abs); statErr == nil {
			return "", fmt.Errorf("resolve worktree path with inaccessible symlink target: %w", err)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", err
		}
		missing = append(missing, filepath.Base(abs))
		abs = parent
	}
}

// Prepare creates only private state directories, never application data. An
// existing unsafe directory is rejected, not repaired or silently chmodded.
func (p WorktreePaths) Prepare() error {
	if p.Directory == "" || p.SocketDir == "" {
		return fmt.Errorf("worktree paths are incomplete")
	}
	for _, path := range []string{filepath.Dir(p.Directory), p.Directory, filepath.Dir(p.SocketDir), p.SocketDir} {
		if err := ensurePrivateWorktreeDir(path); err != nil {
			return err
		}
	}
	return nil
}

func ensurePrivateWorktreeDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || !worktreeFileOwned(info) {
		return fmt.Errorf("worktree state directory is not private and owned by the current user: %s", path)
	}
	return nil
}

func (p WorktreePaths) AcquireLiveLock() (*ProcessLock, error) {
	return p.acquireLock(p.LiveLock)
}

// AcquireExistingLiveLock is the non-allocating stopped-owner boundary used by
// read-only capture and destructive previews of retained capabilities.
func (p WorktreePaths) AcquireExistingLiveLock() (*ProcessLock, error) {
	if err := checkPrivateWorktreeFile(p.LiveLock); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p.LiveLock, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := tryProcessLock(f); err != nil {
		_ = f.Close()
		if processLockBusy(err) {
			return nil, fmt.Errorf("%w: selected worktree must be stopped", ErrProcessLocked)
		}
		return nil, err
	}
	return &ProcessLock{file: f}, nil
}

// ProbeLiveLock observes an existing owner lock without creating state. A free
// lock is released immediately; callers must acquire it before any mutation.
func (p WorktreePaths) ProbeLiveLock() (bool, error) {
	if err := checkPrivateWorktreeFile(p.LiveLock); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	file, err := os.OpenFile(p.LiveLock, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	if err := tryProcessLock(file); err != nil {
		if processLockBusy(err) {
			return true, nil
		}
		return false, err
	}
	return false, unlockProcessLock(file)
}

func (p WorktreePaths) AcquireOperationLock() (*ProcessLock, error) {
	return p.acquireLock(p.OperationLock)
}

func (p WorktreePaths) acquireLock(path string) (*ProcessLock, error) {
	if err := p.Prepare(); err != nil {
		return nil, err
	}
	if err := checkPrivateWorktreeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return AcquireProcessLock(path)
}

func checkPrivateWorktreeFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !worktreeFileOwned(info) {
		return fmt.Errorf("worktree state file is not private and owned by the current user: %s", path)
	}
	return nil
}
