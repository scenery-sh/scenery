package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var ErrProcessLocked = errors.New("process lock is held")

// ProcessLock prevents two owners from managing the same long-lived process.
type ProcessLock struct {
	file *os.File
}

func AcquireProcessLock(path string) (*ProcessLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryProcessLock(file); err != nil {
		_ = file.Close()
		if processLockBusy(err) {
			return nil, fmt.Errorf("%w: %s", ErrProcessLocked, path)
		}
		return nil, err
	}
	return &ProcessLock{file: file}, nil
}

func (l *ProcessLock) Inherit(cmd *exec.Cmd) (bool, error) {
	if l == nil || l.file == nil || cmd == nil {
		return false, nil
	}
	return inheritProcessLock(l.file, cmd)
}

// CloseParent leaves an inherited lock held by the child until it exits.
func (l *ProcessLock) CloseParent() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *ProcessLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := errors.Join(unlockProcessLock(l.file), l.file.Close())
	l.file = nil
	return err
}
