package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/filemeta"
)

// Retain the executable bytes, not a symlink into the disposable build cache.
// A content-addressed name also separates different framework generations that
// happen to have the same app-source build filename.
func prepareSessionAppBinary(session *localagent.Session, binary, expectedDigest string) (string, error) {
	if session == nil || strings.TrimSpace(session.StateRoot) == "" || strings.TrimSpace(binary) == "" {
		return "", nil
	}
	dir := filepath.Join(session.StateRoot, "run", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Retained bytes of the expected digest need no second copy; they are
	// verified instead, as retained bytes found after a copy are.
	if digest := strings.TrimPrefix(expectedDigest, "sha256:"); digest != "" {
		if decoded, decodeErr := hex.DecodeString(digest); decodeErr == nil && len(decoded) == sha256.Size {
			target := filepath.Join(dir, "scenery-app-"+digest)
			if _, err := os.Lstat(target); err == nil {
				if err := verifyRetainedAppBinary(target, digest); err != nil {
					return "", err
				}
				return target, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
	}
	in, err := os.Open(binary)
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("candidate executable is not a regular file: %s", binary)
	}
	expectedDigest = strings.TrimPrefix(expectedDigest, "sha256:")
	if expectedDigest != "" {
		decoded, decodeErr := hex.DecodeString(expectedDigest)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("candidate executable digest is invalid: %q", expectedDigest)
		}
	}
	temporary, err := os.CreateTemp(dir, ".scenery-app-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	var hash = sha256.New()
	// Where the filesystem clones files, the retained executable is a clone:
	// independent bytes that share blocks with the build output until either
	// changes, so retention costs neither a copy nor a second copy's disk. A
	// clone needs a path that does not exist yet; anything that prevents one
	// falls back to copying into the temporary file.
	cloned := false
	if closeErr := temporary.Close(); closeErr == nil {
		if removeErr := os.Remove(temporaryPath); removeErr == nil {
			// The clone of a symlink is a symlink; only cloned bytes are retained.
			if filemeta.Clone(binary, temporaryPath) == nil {
				if cloneInfo, statErr := os.Lstat(temporaryPath); statErr == nil && cloneInfo.Mode().IsRegular() {
					cloned = true
				}
			}
			if !cloned {
				_ = os.Remove(temporaryPath)
			}
		}
	}
	if temporary, err = os.OpenFile(temporaryPath, os.O_RDWR|os.O_CREATE, 0o600); err != nil {
		return "", err
	}
	var written int64
	if cloned {
		cloneInfo, statErr := temporary.Stat()
		if statErr != nil || !cloneInfo.Mode().IsRegular() {
			return "", fmt.Errorf("candidate executable is not a regular file: %s", binary)
		}
		written = cloneInfo.Size()
		if expectedDigest == "" {
			if _, err := io.Copy(hash, temporary); err != nil {
				return "", err
			}
		}
	} else {
		var writer io.Writer = temporary
		if expectedDigest == "" {
			writer = io.MultiWriter(temporary, hash)
		}
		var copyErr error
		if written, copyErr = io.Copy(writer, io.LimitReader(in, info.Size()+1)); copyErr != nil {
			return "", copyErr
		}
	}
	after, err := in.Stat()
	if err != nil || written != info.Size() || after.Size() != info.Size() || after.ModTime() != info.ModTime() {
		return "", fmt.Errorf("candidate executable changed while copying: %s", binary)
	}
	digest := expectedDigest
	if digest == "" {
		digest = hex.EncodeToString(hash.Sum(nil))
	}
	target := filepath.Join(dir, "scenery-app-"+digest)
	if _, err := os.Lstat(target); err == nil {
		if closeErr := temporary.Close(); closeErr != nil {
			return "", closeErr
		}
		if err := verifyRetainedAppBinary(target, digest); err != nil {
			return "", err
		}
		return target, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", err
	}
	keepTemporary = true
	directory, err := os.Open(dir)
	if err != nil {
		_ = os.Remove(target)
		return "", err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(target)
		return "", errors.Join(syncErr, closeErr)
	}
	return target, nil
}

func verifyRetainedAppBinary(path, digest string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("retained executable is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return fmt.Errorf("retained executable content changed: %s", path)
	}
	return nil
}

func (s *devSupervisor) releaseUnusedAppBinary(plan *appStartPlan) {
	if plan == nil {
		return
	}
	s.mu.RLock()
	current := s.current
	s.mu.RUnlock()
	if current != nil && current.launch != nil && current.launch.request.Command == plan.request.Command {
		return
	}
	session := s.currentAgentSession()
	if session == nil || filepath.Dir(plan.request.Command) != filepath.Join(session.StateRoot, "run", "app") {
		return
	}
	_ = os.Remove(plan.request.Command)
}

// releaseSessionAppBinariesExcept deletes the retained executables of a
// closing session that its last served generation did not use, so retention
// stays bounded by one generation however many builds the session replaced.
func releaseSessionAppBinariesExcept(session *localagent.Session, keep map[string]bool) {
	if session == nil || strings.TrimSpace(session.StateRoot) == "" {
		return
	}
	dir := filepath.Join(session.StateRoot, "run", "app")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "scenery-app-") && !keep[path] {
			_ = os.Remove(path)
		}
	}
}
