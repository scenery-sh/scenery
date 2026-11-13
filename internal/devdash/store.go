package devdash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"scenery.sh/internal/atomicfile"
)

type Store struct {
	path     string
	shared   *storeShared
	readOnly bool
}

type storeShared struct {
	mu    sync.Mutex
	state *storeState
	stamp storeStamp
}

type storeStamp struct {
	exists  bool
	size    int64
	modTime time.Time
}

const hardStoreFileBytes = 8 * 1024 * 1024

type TraceQuery struct {
	AppID            string
	SessionID        string
	TraceID          string
	ServiceName      string
	EndpointName     string
	Status           string
	Since            time.Time
	MinDurationNanos uint64
	Limit            int
}

type LogLevelCount struct {
	Level string `json:"level"`
	Count int64  `json:"count"`
}

type storeState struct {
	Version      int                          `json:"version"`
	Apps         map[string]StoredApp         `json:"apps,omitempty"`
	AppSessions  map[string]StoredAppSession  `json:"app_sessions,omitempty"`
	AppModelRefs map[string]StoredAppModelRef `json:"app_model_refs,omitempty"`
}

var storeLocks sync.Map

func OpenStore(cacheRoot string) (*Store, error) {
	if cacheRoot == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cacheRoot = filepath.Join(dir, "scenery")
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(cacheRoot, "devdash.json")
	sharedAny, _ := storeLocks.LoadOrStore(path, &storeShared{})
	store := &Store{path: path, shared: sharedAny.(*storeShared)}
	if err := store.withState(context.Background(), true, func(*storeState) error { return nil }); err != nil {
		return nil, err
	}
	return store, nil
}

// OpenReadOnlyStore inspects an existing cache without creating or compacting
// it. It has no default root.
func OpenReadOnlyStore(cacheRoot string) (*Store, error) {
	if cacheRoot == "" {
		return nil, errors.New("read-only dashboard store requires an explicit root")
	}
	path := filepath.Join(cacheRoot, "devdash.json")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("dashboard store is not a regular file")
	}
	store := &Store{path: path, shared: &storeShared{}, readOnly: true}
	if err := store.withState(context.Background(), false, func(*storeState) error { return nil }); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return nil
}

func (s *Store) withState(ctx context.Context, write bool, fn func(*storeState) error) error {
	if s == nil || s.path == "" || s.shared == nil {
		return errors.New("devdash store is nil")
	}
	if write && s.readOnly {
		return errors.New("dashboard store is read-only")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.shared.mu.Lock()
	defer s.shared.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return err
	}
	if err := s.refreshForExternalChangeLocked(); err != nil {
		return err
	}
	if err := fn(s.shared.state); err != nil {
		return err
	}
	if write {
		pruneStoreState(s.shared.state)
		if err := s.saveState(s.shared.state); err != nil {
			return err
		}
		if stamp, err := s.statStamp(); err == nil {
			s.shared.stamp = stamp
		}
	}
	return nil
}

func (s *Store) ensureLoadedLocked() error {
	if s.shared.state != nil {
		return nil
	}
	state, stamp, err := s.loadStateWithStamp()
	if err != nil {
		return err
	}
	s.shared.state = state
	s.shared.stamp = stamp
	return nil
}

func (s *Store) loadStateWithStamp() (*storeState, storeStamp, error) {
	stamp, err := s.statStamp()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newStoreState(), storeStamp{}, nil
		}
		return nil, storeStamp{}, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newStoreState(), storeStamp{}, nil
		}
		return nil, storeStamp{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return newStoreState(), stamp, nil
	}
	var state storeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, storeStamp{}, err
	}
	normalizeStoreState(&state)
	pruneStoreState(&state)
	return &state, stamp, nil
}

func (s *Store) statStamp() (storeStamp, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return storeStamp{}, err
	}
	return storeStamp{exists: true, size: info.Size(), modTime: info.ModTime()}, nil
}

func (s *Store) refreshForExternalChangeLocked() error {
	stamp, err := s.statStamp()
	if errors.Is(err, os.ErrNotExist) {
		stamp = storeStamp{}
	} else if err != nil {
		return err
	}
	if stamp == s.shared.stamp {
		return nil
	}
	state, loadedStamp, err := s.loadStateWithStamp()
	if err != nil {
		return err
	}
	pruneStoreState(state)
	s.shared.state = state
	s.shared.stamp = loadedStamp
	return nil
}

func (s *Store) saveState(state *storeState) error {
	normalizeStoreState(state)
	pruneStoreState(state)
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > hardStoreFileBytes {
		return fmt.Errorf("devdash store exceeds hard budget: %d > %d (%s)", len(data), hardStoreFileBytes, formatStoreSizeBreakdown(state))
	}
	return atomicfile.Write(s.path, append(data, '\n'), 0o600, atomicfile.Options{})
}

func newStoreState() *storeState {
	state := &storeState{Version: 1}
	normalizeStoreState(state)
	return state
}

func normalizeStoreState(state *storeState) {
	if state.Version == 0 {
		state.Version = 1
	}
	if state.Apps == nil {
		state.Apps = map[string]StoredApp{}
	}
	if state.AppSessions == nil {
		state.AppSessions = map[string]StoredAppSession{}
	}
	if state.AppModelRefs == nil {
		state.AppModelRefs = map[string]StoredAppModelRef{}
	}
}
