package devdash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	mu          sync.Mutex
	state       *storeState
	stamp       storeStamp
	dirty       bool
	savePending bool
	pending     []storeMutation
}

type storeMutation func(*storeState) error

type storeStamp struct {
	exists  bool
	size    int64
	modTime time.Time
}

const (
	maxStoredProcessEvents = 1000
	maxStoredProcessOutput = 5000
	maxStoredDevEvents     = 5000
	deferredSaveDelay      = 500 * time.Millisecond

	// Process events are diagnostic breadcrumbs. A payload above this size
	// (e.g. full app metadata on every reload) bloats devdash.json until
	// every store refresh re-parses hundreds of megabytes of JSON.
	maxProcessEventPayloadBytes = 64 * 1024

	softStoreFileBytes = 2 * 1024 * 1024
	hardStoreFileBytes = 8 * 1024 * 1024
)

type ProcessEvent struct {
	ID          int64           `json:"id"`
	AppID       string          `json:"app_id"`
	Kind        string          `json:"kind"`
	PayloadJSON json.RawMessage `json:"payload_json"`
	CreatedAt   time.Time       `json:"created_at"`
}

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
	Version             int                          `json:"version"`
	Apps                map[string]StoredApp         `json:"apps,omitempty"`
	AppSessions         map[string]StoredAppSession  `json:"app_sessions,omitempty"`
	AppModelRefs        map[string]StoredAppModelRef `json:"app_model_refs,omitempty"`
	ProcessEvents       []ProcessEvent               `json:"process_events,omitempty"`
	ProcessOutput       []ProcessOutput              `json:"process_output,omitempty"`
	DevSources          map[string]DevSource         `json:"dev_sources,omitempty"`
	DevEvents           []storedDevEvent             `json:"dev_events,omitempty"`
	NextProcessEventID  int64                        `json:"next_process_event_id,omitempty"`
	NextProcessOutputID int64                        `json:"next_process_output_id,omitempty"`
	NextDevEventID      int64                        `json:"next_dev_event_id,omitempty"`
}

type storedDevEvent struct {
	DevEvent
	AppID     string    `json:"app_id"`
	AppRoot   string    `json:"app_root,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

// OpenReadOnlyStore inspects an existing cache without creating, compacting,
// flushing, or joining a writer's pending mutations. It has no default root.
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
	return s.Flush(context.Background())
}

func (s *Store) Flush(ctx context.Context) error {
	if s == nil || s.path == "" || s.shared == nil || s.readOnly {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.shared.mu.Lock()
	defer s.shared.mu.Unlock()
	if err := s.ensureLoadedLocked(); err != nil {
		return err
	}
	if !s.shared.dirty {
		s.shared.savePending = false
		return nil
	}
	if err := s.refreshForExternalChangeLocked(); err != nil {
		s.shared.savePending = false
		return err
	}
	if err := s.saveState(s.shared.state); err != nil {
		s.shared.savePending = false
		return err
	}
	if stamp, err := s.statStamp(); err == nil {
		s.shared.stamp = stamp
	}
	s.shared.dirty = false
	s.shared.savePending = false
	s.shared.pending = nil
	return nil
}

func (s *Store) withState(ctx context.Context, write bool, fn func(*storeState) error) error {
	return s.withStatePersist(ctx, write, true, fn)
}

// withStateDeferred applies a write mutation now but coalesces persistence
// into the deferred save path, so high-frequency appenders do not rewrite the
// whole store file under the shared lock on every call. Deferred mutations
// must stay safe to replay onto a freshly loaded state snapshot.
func (s *Store) withStateDeferred(ctx context.Context, fn storeMutation) error {
	return s.withStatePersist(ctx, true, false, fn)
}

func (s *Store) withStatePersist(ctx context.Context, write bool, immediate bool, fn storeMutation) error {
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
		if immediate {
			if err := s.saveState(s.shared.state); err != nil {
				return err
			}
			if stamp, err := s.statStamp(); err == nil {
				s.shared.stamp = stamp
			}
			s.shared.dirty = false
			s.shared.pending = nil
			return nil
		}
		s.shared.pending = append(s.shared.pending, fn)
		s.scheduleSaveLocked()
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
	for _, mutation := range s.shared.pending {
		if err := mutation(state); err != nil {
			return err
		}
	}
	pruneStoreState(state)
	s.shared.state = state
	s.shared.stamp = loadedStamp
	return nil
}

func (s *Store) saveState(state *storeState) error {
	normalizeStoreState(state)
	pruneStoreState(state)
	// A state inside its budget is encoded once; measuring it for the budget
	// first would encode it twice on every save.
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > softStoreFileBytes {
		pruneStoreStateToBudget(state, softStoreFileBytes)
		if data, err = json.Marshal(state); err != nil {
			return err
		}
	}
	if len(data) > hardStoreFileBytes {
		pruneStoreStateToBudget(state, hardStoreFileBytes)
		data, err = json.Marshal(state)
		if err != nil {
			return err
		}
		if len(data) > hardStoreFileBytes {
			return fmt.Errorf("devdash store exceeds hard budget: %d > %d (%s)", len(data), hardStoreFileBytes, formatStoreSizeBreakdown(state))
		}
	}
	return atomicfile.Write(s.path, append(data, '\n'), 0o600, atomicfile.Options{})
}

func (s *Store) scheduleSaveLocked() {
	s.shared.dirty = true
	if s.shared.savePending {
		return
	}
	s.shared.savePending = true
	go func() {
		time.Sleep(deferredSaveDelay)
		_ = s.Flush(context.Background())
	}()
}

func pruneStoreState(state *storeState) {
	if state == nil {
		return
	}
	state.ProcessEvents = tailSlice(state.ProcessEvents, maxStoredProcessEvents)
	state.ProcessOutput = tailSlice(state.ProcessOutput, maxStoredProcessOutput)
	state.DevEvents = tailSlice(state.DevEvents, maxStoredDevEvents)
	truncateOversizedProcessEvents(state.ProcessEvents)
	pruneOrphanedAppModelRefs(state)
}

func pruneStoreStateToBudget(state *storeState, targetBytes int) {
	if state == nil || targetBytes <= 0 {
		return
	}
	for serializedStoreSize(state) > targetBytes {
		switch {
		case len(state.ProcessOutput) > 0:
			state.ProcessOutput = dropOldestBudgetChunk(state.ProcessOutput)
		case len(state.DevEvents) > 0:
			state.DevEvents = dropOldestBudgetChunk(state.DevEvents)
		case len(state.ProcessEvents) > 0:
			state.ProcessEvents = dropOldestBudgetChunk(state.ProcessEvents)
		default:
			return
		}
	}
}

func dropOldestBudgetChunk[T any](items []T) []T {
	if len(items) <= 1 {
		return nil
	}
	drop := min(max(len(items)/4, 1), 256)
	return items[drop:]
}

func serializedStoreSize(state *storeState) int {
	data, err := json.Marshal(state)
	if err != nil {
		return 0
	}
	return len(data)
}

func storeSizeBreakdown(state *storeState) map[string]int {
	if state == nil {
		return nil
	}
	parts := map[string]any{
		"apps":                   state.Apps,
		"app_sessions":           state.AppSessions,
		"app_model_refs":         state.AppModelRefs,
		"process_events":         state.ProcessEvents,
		"process_output":         state.ProcessOutput,
		"dev_sources":            state.DevSources,
		"dev_events":             state.DevEvents,
		"next_process_event_id":  state.NextProcessEventID,
		"next_process_output_id": state.NextProcessOutputID,
		"next_dev_event_id":      state.NextDevEventID,
	}
	out := make(map[string]int, len(parts))
	for key, value := range parts {
		data, err := json.Marshal(value)
		if err != nil {
			continue
		}
		out[key] = len(data)
	}
	return out
}

func formatStoreSizeBreakdown(state *storeState) string {
	breakdown := storeSizeBreakdown(state)
	keys := make([]string, 0, len(breakdown))
	for key := range breakdown {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if breakdown[keys[i]] == breakdown[keys[j]] {
			return keys[i] < keys[j]
		}
		return breakdown[keys[i]] > breakdown[keys[j]]
	})
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if breakdown[key] == 0 || breakdown[key] == 4 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d", key, breakdown[key]))
	}
	return strings.Join(parts, ", ")
}

// truncateOversizedProcessEvents retroactively applies the payload size cap
// so stores bloated by older writers shrink on the next load/save instead of
// keeping multi-megabyte payloads alive until count-based pruning ages them
// out hundreds of events later.
func truncateOversizedProcessEvents(events []ProcessEvent) {
	for i := range events {
		if len(events[i].PayloadJSON) <= maxProcessEventPayloadBytes {
			continue
		}
		marker, err := json.Marshal(map[string]any{
			"truncated":      true,
			"original_bytes": len(events[i].PayloadJSON),
		})
		if err != nil {
			continue
		}
		events[i].PayloadJSON = marker
	}
}

func pruneOrphanedAppModelRefs(state *storeState) {
	if state == nil || len(state.AppModelRefs) == 0 {
		return
	}
	live := map[string]bool{}
	for _, app := range state.Apps {
		if app.MetadataRef != "" {
			live[app.MetadataRef] = true
		}
		if app.APIEncodingRef != "" {
			live[app.APIEncodingRef] = true
		}
	}
	for _, session := range state.AppSessions {
		if session.MetadataRef != "" {
			live[session.MetadataRef] = true
		}
		if session.APIEncodingRef != "" {
			live[session.APIEncodingRef] = true
		}
	}
	for ref := range state.AppModelRefs {
		if !live[ref] {
			delete(state.AppModelRefs, ref)
		}
	}
}

func tailSlice[T any](items []T, max int) []T {
	if max <= 0 || len(items) <= max {
		return items
	}
	return items[len(items)-max:]
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
	if state.DevSources == nil {
		state.DevSources = map[string]DevSource{}
	}
	if state.NextProcessEventID <= 0 {
		state.NextProcessEventID = maxProcessEventID(state.ProcessEvents) + 1
	}
	if state.NextProcessOutputID <= 0 {
		state.NextProcessOutputID = maxProcessOutputID(state.ProcessOutput) + 1
	}
	if state.NextDevEventID <= 0 {
		state.NextDevEventID = maxDevEventID(state.DevEvents) + 1
	}
}

func maxProcessEventID(events []ProcessEvent) int64 {
	var maxID int64
	for _, event := range events {
		if event.ID > maxID {
			maxID = event.ID
		}
	}
	return maxID
}

func maxProcessOutputID(items []ProcessOutput) int64 {
	var maxID int64
	for _, item := range items {
		if item.ID > maxID {
			maxID = item.ID
		}
	}
	return maxID
}

func maxDevEventID(events []storedDevEvent) int64 {
	var maxID int64
	for _, event := range events {
		if event.ID > maxID {
			maxID = event.ID
		}
	}
	return maxID
}

func storeDevEvent(event DevEvent) storedDevEvent {
	return storedDevEvent{
		DevEvent:  event,
		AppID:     event.AppID,
		AppRoot:   event.AppRoot,
		CreatedAt: event.CreatedAt,
	}
}

func (event storedDevEvent) toDevEvent() DevEvent {
	item := event.DevEvent
	item.AppID = event.AppID
	item.AppRoot = event.AppRoot
	item.CreatedAt = event.CreatedAt
	item.Fields = compactRawMessage(item.Fields)
	return item
}

func SortTraceSummariesByDuration(items []*TraceSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].DurationNanos == items[j].DurationNanos {
			return items[i].StartedAt.After(items[j].StartedAt)
		}
		return items[i].DurationNanos > items[j].DurationNanos
	})
}
