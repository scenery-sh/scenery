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
	"scenery.sh/internal/atomicfile"
)

// Retain the executable bytes, not a symlink into the disposable build cache.
// A content-addressed name also separates different framework generations that
// happen to have the same app-source build filename.
func prepareSessionAppBinary(session *localagent.Session, binary string) (string, error) {
	if session == nil || strings.TrimSpace(session.StateRoot) == "" || strings.TrimSpace(binary) == "" {
		return "", nil
	}
	dir := filepath.Join(session.StateRoot, "run", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
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
	hash := sha256.New()
	if _, err := io.Copy(hash, in); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	target := filepath.Join(dir, "scenery-app-"+digest)
	if _, err := os.Lstat(target); err == nil {
		if err := verifyRetainedAppBinary(target, digest); err != nil {
			return "", err
		}
		return target, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	owner, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = owner.Close() }()
	if err := atomicfile.CopyRoot(owner, filepath.Base(target), in, info.Size(), info.Mode().Perm(), atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		return "", err
	}
	if err := verifyRetainedAppBinary(target, digest); err != nil {
		_ = owner.Remove(filepath.Base(target))
		return "", err
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
