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
	"sync"

	localagent "scenery.sh/internal/agent"
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
	var writer io.Writer = temporary
	var hash = sha256.New()
	if expectedDigest == "" {
		writer = io.MultiWriter(temporary, hash)
	}
	written, copyErr := io.Copy(writer, io.LimitReader(in, info.Size()+1))
	if copyErr != nil {
		return "", copyErr
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
	if stat, statErr := os.Lstat(target); statErr == nil {
		rememberRetainedAppBinary(target, digest, stat)
	}
	return target, nil
}

type retainedAppBinaryStamp struct {
	size    int64
	mode    os.FileMode
	modNano int64
	digest  string
}

var retainedAppBinaryDigests struct {
	mu     sync.Mutex
	values map[string]retainedAppBinaryStamp
}

func rememberRetainedAppBinary(path, digest string, info os.FileInfo) {
	retainedAppBinaryDigests.mu.Lock()
	defer retainedAppBinaryDigests.mu.Unlock()
	if retainedAppBinaryDigests.values == nil {
		retainedAppBinaryDigests.values = map[string]retainedAppBinaryStamp{}
	}
	retainedAppBinaryDigests.values[path] = retainedAppBinaryStamp{
		size:    info.Size(),
		mode:    info.Mode(),
		modNano: info.ModTime().UnixNano(),
		digest:  digest,
	}
}

func verifyRetainedAppBinary(path, digest string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("retained executable is not a regular file: %s", path)
	}
	retainedAppBinaryDigests.mu.Lock()
	remembered, ok := retainedAppBinaryDigests.values[path]
	retainedAppBinaryDigests.mu.Unlock()
	if ok && remembered.digest == digest && remembered.size == info.Size() && remembered.mode == info.Mode() && remembered.modNano == info.ModTime().UnixNano() {
		return nil
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
	rememberRetainedAppBinary(path, digest, info)
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
