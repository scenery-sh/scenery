package feature

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// tryLock relies on kernel ownership. Files remain reusable after owner exit.
func tryLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (m *Manager) metadataLock(ctx context.Context) (*os.File, error) {
	for {
		file, err := tryLock(filepath.Join(m.State, "metadata.lock"))
		if err == nil || !lockBusy(err) {
			return file, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (m *Manager) publicationLock() (*os.File, error) {
	file, err := tryLock(filepath.Join(m.State, "publication.lock"))
	if lockBusy(err) {
		return nil, errors.New("another local main landing is active; other feature work can continue")
	}
	return file, err
}

func (m *Manager) probeLock(ctx context.Context, limit int) (*os.File, error) {
	// One committed main policy owns admission across all feature revisions.
	policyText, err := git(ctx, m.MainRoot, "show", "main:"+PolicyFile)
	if err != nil {
		return nil, err
	}
	var mainPolicy Policy
	if err := decodeJSON([]byte(policyText), &mainPolicy); err != nil {
		return nil, err
	}
	if mainPolicy.ProbeLimit < limit {
		limit = mainPolicy.ProbeLimit
	}
	if limit < 1 || limit > 16 {
		return nil, fmt.Errorf("probe_limit must be between 1 and 16")
	}
	for {
		for slot := 0; slot < limit; slot++ {
			file, err := tryLock(filepath.Join(m.State, "probes", fmt.Sprintf("%d.lock", slot)))
			if err == nil {
				return file, nil
			}
			if !lockBusy(err) {
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
