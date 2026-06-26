package devdash

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store struct {
	path   string
	shared *storeShared
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
	Onboarding          OnboardingState              `json:"onboarding,omitempty"`
	StoredRequests      map[string]StoredRequest     `json:"stored_requests,omitempty"`
	NextProcessEventID  int64                        `json:"next_process_event_id,omitempty"`
	NextProcessOutputID int64                        `json:"next_process_output_id,omitempty"`
	NextDevEventID      int64                        `json:"next_dev_event_id,omitempty"`
}

type StoredApp struct {
	RouteID             string            `json:"route_id,omitempty"`
	ID                  string            `json:"id"`
	BaseAppID           string            `json:"base_app_id,omitempty"`
	RuntimeAppID        string            `json:"runtime_app_id,omitempty"`
	SessionID           string            `json:"session_id,omitempty"`
	Name                string            `json:"name,omitempty"`
	Root                string            `json:"root,omitempty"`
	ListenAddr          string            `json:"listen_addr,omitempty"`
	Grafana             json.RawMessage   `json:"grafana,omitempty"`
	Routes              map[string]string `json:"routes,omitempty"`
	Aliases             map[string]string `json:"aliases,omitempty"`
	Offline             bool              `json:"offline,omitempty"`
	Running             bool              `json:"running,omitempty"`
	SessionStatus       string            `json:"session_status,omitempty"`
	SessionStatusReason string            `json:"session_status_reason,omitempty"`
	Compiling           bool              `json:"compiling,omitempty"`
	CompileError        string            `json:"compile_error,omitempty"`
	PID                 string            `json:"pid,omitempty"`
	UpdatedAt           time.Time         `json:"updated_at,omitempty"`
	MetadataRef         string            `json:"metadata_ref,omitempty"`
	MetadataHash        string            `json:"metadata_hash,omitempty"`
	APIEncodingRef      string            `json:"api_encoding_ref,omitempty"`
	APIEncodingHash     string            `json:"api_encoding_hash,omitempty"`
	AppRevision         string            `json:"app_revision,omitempty"`

	legacyMetadata    json.RawMessage
	legacyAPIEncoding json.RawMessage
}

type StoredAppSession struct {
	RouteID             string            `json:"route_id,omitempty"`
	ID                  string            `json:"id"`
	BaseAppID           string            `json:"base_app_id,omitempty"`
	RuntimeAppID        string            `json:"runtime_app_id,omitempty"`
	SessionID           string            `json:"session_id,omitempty"`
	Name                string            `json:"name,omitempty"`
	Root                string            `json:"root,omitempty"`
	ListenAddr          string            `json:"listen_addr,omitempty"`
	Grafana             json.RawMessage   `json:"grafana,omitempty"`
	Routes              map[string]string `json:"routes,omitempty"`
	Aliases             map[string]string `json:"aliases,omitempty"`
	Offline             bool              `json:"offline,omitempty"`
	Running             bool              `json:"running,omitempty"`
	SessionStatus       string            `json:"session_status,omitempty"`
	SessionStatusReason string            `json:"session_status_reason,omitempty"`
	Compiling           bool              `json:"compiling,omitempty"`
	CompileError        string            `json:"compile_error,omitempty"`
	PID                 string            `json:"pid,omitempty"`
	UpdatedAt           time.Time         `json:"updated_at,omitempty"`
	MetadataRef         string            `json:"metadata_ref,omitempty"`
	MetadataHash        string            `json:"metadata_hash,omitempty"`
	APIEncodingRef      string            `json:"api_encoding_ref,omitempty"`
	APIEncodingHash     string            `json:"api_encoding_hash,omitempty"`
	AppRevision         string            `json:"app_revision,omitempty"`

	legacyMetadata    json.RawMessage
	legacyAPIEncoding json.RawMessage
}

type StoredAppModelRef struct {
	Ref         string    `json:"ref"`
	Kind        string    `json:"kind"`
	Hash        string    `json:"hash"`
	AppID       string    `json:"app_id"`
	Root        string    `json:"root,omitempty"`
	AppRevision string    `json:"app_revision,omitempty"`
	Path        string    `json:"path,omitempty"`
	Bytes       int64     `json:"bytes,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type storedDevEvent struct {
	DevEvent
	AppID     string    `json:"app_id"`
	AppRoot   string    `json:"app_root,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (app *StoredApp) UnmarshalJSON(data []byte) error {
	type storedApp StoredApp
	var current storedApp
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*app = StoredApp(current)
	var legacy AppRecord
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil
	}
	mergeStoredAppLegacy(app, legacy)
	return nil
}

func (session *StoredAppSession) UnmarshalJSON(data []byte) error {
	type storedAppSession StoredAppSession
	var current storedAppSession
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*session = StoredAppSession(current)
	var legacy AppRecord
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil
	}
	mergeStoredAppSessionLegacy(session, legacy)
	return nil
}

func mergeStoredAppLegacy(app *StoredApp, legacy AppRecord) {
	if app.ID == "" {
		app.ID = legacy.ID
	}
	if app.RouteID == "" {
		app.RouteID = legacy.RouteID
	}
	if app.BaseAppID == "" {
		app.BaseAppID = legacy.BaseAppID
	}
	if app.RuntimeAppID == "" {
		app.RuntimeAppID = legacy.RuntimeAppID
	}
	if app.SessionID == "" {
		app.SessionID = legacy.SessionID
	}
	if app.Name == "" {
		app.Name = legacy.Name
	}
	if app.Root == "" {
		app.Root = legacy.Root
	}
	if app.ListenAddr == "" {
		app.ListenAddr = legacy.ListenAddr
	}
	if len(app.Grafana) == 0 {
		app.Grafana = copyRawMessage(legacy.Grafana)
	}
	if app.Routes == nil {
		app.Routes = maps.Clone(legacy.Routes)
	}
	if app.Aliases == nil {
		app.Aliases = maps.Clone(legacy.Aliases)
	}
	if !app.Offline {
		app.Offline = legacy.Offline
	}
	if !app.Running {
		app.Running = legacy.Running
	}
	if app.SessionStatus == "" {
		app.SessionStatus = legacy.SessionStatus
	}
	if app.SessionStatusReason == "" {
		app.SessionStatusReason = legacy.SessionStatusReason
	}
	if !app.Compiling {
		app.Compiling = legacy.Compiling
	}
	if app.CompileError == "" {
		app.CompileError = legacy.CompileError
	}
	if app.PID == "" {
		app.PID = legacy.PID
	}
	if app.UpdatedAt.IsZero() {
		app.UpdatedAt = legacy.UpdatedAt
	}
	app.legacyMetadata = copyRawMessage(legacy.Metadata)
	app.legacyAPIEncoding = copyRawMessage(legacy.APIEncoding)
}

func mergeStoredAppSessionLegacy(session *StoredAppSession, legacy AppRecord) {
	if session.ID == "" {
		session.ID = legacy.ID
	}
	if session.RouteID == "" {
		session.RouteID = legacy.RouteID
	}
	if session.BaseAppID == "" {
		session.BaseAppID = legacy.BaseAppID
	}
	if session.RuntimeAppID == "" {
		session.RuntimeAppID = legacy.RuntimeAppID
	}
	if session.SessionID == "" {
		session.SessionID = legacy.SessionID
	}
	if session.Name == "" {
		session.Name = legacy.Name
	}
	if session.Root == "" {
		session.Root = legacy.Root
	}
	if session.ListenAddr == "" {
		session.ListenAddr = legacy.ListenAddr
	}
	if len(session.Grafana) == 0 {
		session.Grafana = copyRawMessage(legacy.Grafana)
	}
	if session.Routes == nil {
		session.Routes = maps.Clone(legacy.Routes)
	}
	if session.Aliases == nil {
		session.Aliases = maps.Clone(legacy.Aliases)
	}
	if !session.Offline {
		session.Offline = legacy.Offline
	}
	if !session.Running {
		session.Running = legacy.Running
	}
	if session.SessionStatus == "" {
		session.SessionStatus = legacy.SessionStatus
	}
	if session.SessionStatusReason == "" {
		session.SessionStatusReason = legacy.SessionStatusReason
	}
	if !session.Compiling {
		session.Compiling = legacy.Compiling
	}
	if session.CompileError == "" {
		session.CompileError = legacy.CompileError
	}
	if session.PID == "" {
		session.PID = legacy.PID
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = legacy.UpdatedAt
	}
	session.legacyMetadata = copyRawMessage(legacy.Metadata)
	session.legacyAPIEncoding = copyRawMessage(legacy.APIEncoding)
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

func (s *Store) Close() error {
	return s.Flush(context.Background())
}

func (s *Store) Flush(ctx context.Context) error {
	if s == nil || s.path == "" || s.shared == nil {
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

func (s *Store) withStatePersist(ctx context.Context, write bool, immediate bool, fn storeMutation) error {
	if s == nil || s.path == "" || s.shared == nil {
		return errors.New("devdash store is nil")
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
	if err := s.migrateStoreStateAppModels(&state); err != nil {
		return nil, storeStamp{}, err
	}
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
	if err := s.migrateStoreStateAppModels(state); err != nil {
		return err
	}
	pruneStoreState(state)
	pruneStoreStateToBudget(state, softStoreFileBytes)
	data, err := json.Marshal(state)
	if err != nil {
		return err
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
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".devdash-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	ok = true
	return nil
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
	drop := len(items) / 4
	if drop < 1 {
		drop = 1
	}
	if drop > 256 {
		drop = 256
	}
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
		"onboarding":             state.Onboarding,
		"stored_requests":        state.StoredRequests,
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
	if state.Onboarding == nil {
		state.Onboarding = OnboardingState{}
	}
	if state.StoredRequests == nil {
		state.StoredRequests = map[string]StoredRequest{}
	}
	if len(state.AppSessions) == 0 && len(state.Apps) > 0 {
		for _, app := range state.Apps {
			session := storedAppSessionFromApp(app)
			state.AppSessions[storedAppSessionRecordKey(session)] = session
		}
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

func appSessionRecordKey(app AppRecord) string {
	if app.RouteID != "" {
		return app.RouteID
	}
	if app.SessionID != "" {
		return app.SessionID
	}
	return app.ID
}

func storedAppSessionRecordKey(session StoredAppSession) string {
	if session.RouteID != "" {
		return session.RouteID
	}
	if session.SessionID != "" {
		return session.SessionID
	}
	return session.ID
}

func normalizeAppRecord(app AppRecord) AppRecord {
	if app.UpdatedAt.IsZero() {
		app.UpdatedAt = time.Now().UTC()
	}
	if len(app.Metadata) == 0 {
		app.Metadata = json.RawMessage(`{}`)
	}
	if len(app.APIEncoding) == 0 {
		app.APIEncoding = json.RawMessage(`{}`)
	}
	if len(app.Grafana) == 0 {
		app.Grafana = json.RawMessage(`{}`)
	}
	return app
}

func (s *Store) splitAppRecordForStore(app AppRecord) (StoredApp, StoredAppSession, []StoredAppModelRef, error) {
	appRevision := appRevisionFromMetadata(app.Metadata)
	metadataRef, err := s.putAppModelBlob("metadata", app.ID, app.Root, appRevision, app.Metadata)
	if err != nil {
		return StoredApp{}, StoredAppSession{}, nil, err
	}
	apiEncodingRef, err := s.putAppModelBlob("api-encoding", app.ID, app.Root, appRevision, app.APIEncoding)
	if err != nil {
		return StoredApp{}, StoredAppSession{}, nil, err
	}
	stored := storedAppFromAppRecord(app)
	session := storedAppSessionFromAppRecord(app)
	applyAppModelRefsToStoredApp(&stored, appRevision, metadataRef, apiEncodingRef)
	applyAppModelRefsToStoredAppSession(&session, appRevision, metadataRef, apiEncodingRef)
	refs := make([]StoredAppModelRef, 0, 2)
	if metadataRef.Ref != "" {
		refs = append(refs, metadataRef)
	}
	if apiEncodingRef.Ref != "" {
		refs = append(refs, apiEncodingRef)
	}
	return stored, session, refs, nil
}

func storedAppFromAppRecord(app AppRecord) StoredApp {
	return StoredApp{
		RouteID:             app.RouteID,
		ID:                  app.ID,
		BaseAppID:           app.BaseAppID,
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		Name:                app.Name,
		Root:                app.Root,
		ListenAddr:          app.ListenAddr,
		Grafana:             compactRawMessage(app.Grafana),
		Routes:              maps.Clone(app.Routes),
		Aliases:             maps.Clone(app.Aliases),
		Offline:             app.Offline,
		Running:             app.Running,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
		PID:                 app.PID,
		UpdatedAt:           app.UpdatedAt,
	}
}

func storedAppSessionFromAppRecord(app AppRecord) StoredAppSession {
	return StoredAppSession{
		RouteID:             app.RouteID,
		ID:                  app.ID,
		BaseAppID:           app.BaseAppID,
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		Name:                app.Name,
		Root:                app.Root,
		ListenAddr:          app.ListenAddr,
		Grafana:             compactRawMessage(app.Grafana),
		Routes:              maps.Clone(app.Routes),
		Aliases:             maps.Clone(app.Aliases),
		Offline:             app.Offline,
		Running:             app.Running,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
		PID:                 app.PID,
		UpdatedAt:           app.UpdatedAt,
	}
}

func storedAppSessionFromApp(app StoredApp) StoredAppSession {
	return StoredAppSession{
		RouteID:             app.RouteID,
		ID:                  app.ID,
		BaseAppID:           app.BaseAppID,
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		Name:                app.Name,
		Root:                app.Root,
		ListenAddr:          app.ListenAddr,
		Grafana:             copyRawMessage(app.Grafana),
		Routes:              maps.Clone(app.Routes),
		Aliases:             maps.Clone(app.Aliases),
		Offline:             app.Offline,
		Running:             app.Running,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
		PID:                 app.PID,
		UpdatedAt:           app.UpdatedAt,
		MetadataRef:         app.MetadataRef,
		MetadataHash:        app.MetadataHash,
		APIEncodingRef:      app.APIEncodingRef,
		APIEncodingHash:     app.APIEncodingHash,
		AppRevision:         app.AppRevision,
	}
}

func (app StoredApp) toAppRecord() AppRecord {
	return AppRecord{
		RouteID:             app.RouteID,
		ID:                  app.ID,
		BaseAppID:           app.BaseAppID,
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		Name:                app.Name,
		Root:                app.Root,
		ListenAddr:          app.ListenAddr,
		Grafana:             copyRawMessage(app.Grafana),
		Routes:              maps.Clone(app.Routes),
		Aliases:             maps.Clone(app.Aliases),
		Offline:             app.Offline,
		Running:             app.Running,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
		PID:                 app.PID,
		UpdatedAt:           app.UpdatedAt,
	}
}

func (session StoredAppSession) toAppRecord() AppRecord {
	return AppRecord{
		RouteID:             session.RouteID,
		ID:                  session.ID,
		BaseAppID:           session.BaseAppID,
		RuntimeAppID:        session.RuntimeAppID,
		SessionID:           session.SessionID,
		Name:                session.Name,
		Root:                session.Root,
		ListenAddr:          session.ListenAddr,
		Grafana:             copyRawMessage(session.Grafana),
		Routes:              maps.Clone(session.Routes),
		Aliases:             maps.Clone(session.Aliases),
		Offline:             session.Offline,
		Running:             session.Running,
		SessionStatus:       session.SessionStatus,
		SessionStatusReason: session.SessionStatusReason,
		Compiling:           session.Compiling,
		CompileError:        session.CompileError,
		PID:                 session.PID,
		UpdatedAt:           session.UpdatedAt,
	}
}

func applyAppModelRefsToStoredApp(app *StoredApp, appRevision string, metadataRef, apiEncodingRef StoredAppModelRef) {
	app.AppRevision = firstNonEmptyString(app.AppRevision, appRevision)
	if metadataRef.Ref != "" {
		app.MetadataRef = metadataRef.Ref
		app.MetadataHash = metadataRef.Hash
	}
	if apiEncodingRef.Ref != "" {
		app.APIEncodingRef = apiEncodingRef.Ref
		app.APIEncodingHash = apiEncodingRef.Hash
	}
}

func applyAppModelRefsToStoredAppSession(session *StoredAppSession, appRevision string, metadataRef, apiEncodingRef StoredAppModelRef) {
	session.AppRevision = firstNonEmptyString(session.AppRevision, appRevision)
	if metadataRef.Ref != "" {
		session.MetadataRef = metadataRef.Ref
		session.MetadataHash = metadataRef.Hash
	}
	if apiEncodingRef.Ref != "" {
		session.APIEncodingRef = apiEncodingRef.Ref
		session.APIEncodingHash = apiEncodingRef.Hash
	}
}

func (s *Store) hydrateStoredApp(state *storeState, stored StoredApp) (AppRecord, error) {
	app := stored.toAppRecord()
	var err error
	app.Metadata, err = s.readAppModelBlob(stored.MetadataRef)
	if err != nil {
		return AppRecord{}, err
	}
	app.APIEncoding, err = s.readAppModelBlob(stored.APIEncodingRef)
	if err != nil {
		return AppRecord{}, err
	}
	return normalizeAppRecord(app), nil
}

func (s *Store) hydrateStoredAppSession(state *storeState, stored StoredAppSession) (AppRecord, error) {
	app := stored.toAppRecord()
	var err error
	app.Metadata, err = s.readAppModelBlob(stored.MetadataRef)
	if err != nil {
		return AppRecord{}, err
	}
	app.APIEncoding, err = s.readAppModelBlob(stored.APIEncodingRef)
	if err != nil {
		return AppRecord{}, err
	}
	return normalizeAppRecord(app), nil
}

func (s *Store) migrateStoreStateAppModels(state *storeState) error {
	if state == nil {
		return nil
	}
	normalizeStoreState(state)
	for key, app := range state.Apps {
		refs, err := s.migrateStoredAppModel(app.ID, app.Root, app.AppRevision, app.legacyMetadata, app.legacyAPIEncoding, func(metadataRef, apiEncodingRef StoredAppModelRef) {
			applyAppModelRefsToStoredApp(&app, appRevisionFromMetadata(app.legacyMetadata), metadataRef, apiEncodingRef)
			app.legacyMetadata = nil
			app.legacyAPIEncoding = nil
			state.Apps[key] = app
		})
		if err != nil {
			return err
		}
		for _, ref := range refs {
			state.AppModelRefs[ref.Ref] = ref
		}
	}
	for key, session := range state.AppSessions {
		refs, err := s.migrateStoredAppModel(session.ID, session.Root, session.AppRevision, session.legacyMetadata, session.legacyAPIEncoding, func(metadataRef, apiEncodingRef StoredAppModelRef) {
			applyAppModelRefsToStoredAppSession(&session, appRevisionFromMetadata(session.legacyMetadata), metadataRef, apiEncodingRef)
			session.legacyMetadata = nil
			session.legacyAPIEncoding = nil
			if session.RouteID == "" {
				session.RouteID = key
			}
			state.AppSessions[key] = session
		})
		if err != nil {
			return err
		}
		for _, ref := range refs {
			state.AppModelRefs[ref.Ref] = ref
		}
	}
	return nil
}

func (s *Store) migrateStoredAppModel(appID, root, revision string, metadata, apiEncoding json.RawMessage, apply func(StoredAppModelRef, StoredAppModelRef)) ([]StoredAppModelRef, error) {
	if len(metadata) == 0 && len(apiEncoding) == 0 {
		return nil, nil
	}
	appRevision := firstNonEmptyString(revision, appRevisionFromMetadata(metadata))
	metadataRef, err := s.putAppModelBlob("metadata", appID, root, appRevision, metadata)
	if err != nil {
		return nil, err
	}
	apiEncodingRef, err := s.putAppModelBlob("api-encoding", appID, root, appRevision, apiEncoding)
	if err != nil {
		return nil, err
	}
	apply(metadataRef, apiEncodingRef)
	refs := make([]StoredAppModelRef, 0, 2)
	if metadataRef.Ref != "" {
		refs = append(refs, metadataRef)
	}
	if apiEncodingRef.Ref != "" {
		refs = append(refs, apiEncodingRef)
	}
	return refs, nil
}

func (s *Store) UpsertApp(ctx context.Context, app AppRecord) error {
	if app.UpdatedAt.IsZero() {
		app.UpdatedAt = time.Now().UTC()
	}
	return s.withState(ctx, true, func(state *storeState) error {
		appRecord, sessionRecord, refs, err := s.splitAppRecordForStore(app)
		if err != nil {
			return err
		}
		appRecord.RouteID = appRecord.ID
		state.Apps[app.ID] = appRecord
		sessionRecord.RouteID = appSessionRecordKey(app)
		state.AppSessions[sessionRecord.RouteID] = sessionRecord
		for _, ref := range refs {
			if ref.Ref != "" {
				state.AppModelRefs[ref.Ref] = ref
			}
		}
		return nil
	})
}

func (s *Store) ListApps(ctx context.Context) ([]AppRecord, error) {
	var apps []AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, stored := range state.Apps {
			app := stored.toAppRecord()
			app.RouteID = app.ID
			app.Offline = !app.Running
			app = normalizeAppRecord(app)
			apps = append(apps, app)
		}
		sort.SliceStable(apps, func(i, j int) bool {
			if apps[i].Running != apps[j].Running {
				return apps[i].Running
			}
			return apps[i].Name < apps[j].Name
		})
		return nil
	})
	return apps, err
}

func (s *Store) ListAppSessions(ctx context.Context) ([]AppRecord, error) {
	var apps []AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		for routeID, stored := range state.AppSessions {
			app := stored.toAppRecord()
			app.RouteID = routeID
			app.Offline = !app.Running
			app = normalizeAppRecord(app)
			apps = append(apps, app)
		}
		sort.SliceStable(apps, func(i, j int) bool {
			if apps[i].Running != apps[j].Running {
				return apps[i].Running
			}
			if apps[i].Name != apps[j].Name {
				return apps[i].Name < apps[j].Name
			}
			if apps[i].SessionID != apps[j].SessionID {
				return apps[i].SessionID < apps[j].SessionID
			}
			return apps[i].UpdatedAt.After(apps[j].UpdatedAt)
		})
		return nil
	})
	return apps, err
}

func (s *Store) GetApp(ctx context.Context, appID string) (AppRecord, error) {
	var app AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		found, ok := state.Apps[appID]
		if !ok {
			return sql.ErrNoRows
		}
		var err error
		app, err = s.hydrateStoredApp(state, found)
		if err != nil {
			return err
		}
		app.RouteID = app.ID
		app.Offline = !app.Running
		return nil
	})
	return app, err
}

func (s *Store) GetAppSession(ctx context.Context, routeID string) (AppRecord, error) {
	var app AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		if found, ok := state.AppSessions[routeID]; ok {
			var err error
			app, err = s.hydrateStoredAppSession(state, found)
			if err != nil {
				return err
			}
			app.RouteID = routeID
			app.Offline = !app.Running
			return nil
		}
		var matches []AppRecord
		for key, candidate := range state.AppSessions {
			if candidate.SessionID == routeID {
				record, err := s.hydrateStoredAppSession(state, candidate)
				if err != nil {
					return err
				}
				record.RouteID = key
				matches = append(matches, record)
			}
		}
		if len(matches) == 0 {
			return sql.ErrNoRows
		}
		sortRunningUpdated(matches)
		app = matches[0]
		app.Offline = !app.Running
		return nil
	})
	return app, err
}

func (s *Store) GetAppForSession(ctx context.Context, appID, sessionID string) (AppRecord, error) {
	var app AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		var matches []AppRecord
		for key, candidate := range state.AppSessions {
			if candidate.ID == appID && candidate.SessionID == sessionID {
				record, err := s.hydrateStoredAppSession(state, candidate)
				if err != nil {
					return err
				}
				record.RouteID = key
				matches = append(matches, record)
			}
		}
		if len(matches) == 0 {
			return sql.ErrNoRows
		}
		sortRunningUpdated(matches)
		app = matches[0]
		app.Offline = !app.Running
		return nil
	})
	return app, err
}

func sortRunningUpdated(apps []AppRecord) {
	sort.SliceStable(apps, func(i, j int) bool {
		if apps[i].Running != apps[j].Running {
			return apps[i].Running
		}
		return apps[i].UpdatedAt.After(apps[j].UpdatedAt)
	})
}

func (s *Store) WriteProcessEvent(ctx context.Context, appID, kind string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(data) > maxProcessEventPayloadBytes {
		data, err = json.Marshal(map[string]any{
			"truncated":      true,
			"original_bytes": len(data),
		})
		if err != nil {
			return err
		}
	}
	return s.withState(ctx, true, func(state *storeState) error {
		event := ProcessEvent{
			ID:          state.NextProcessEventID,
			AppID:       appID,
			Kind:        kind,
			PayloadJSON: data,
			CreatedAt:   time.Now().UTC(),
		}
		state.NextProcessEventID++
		state.ProcessEvents = append(state.ProcessEvents, event)
		return nil
	})
}

func (s *Store) ListProcessEvents(ctx context.Context, appID string, limit int) ([]ProcessEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	var events []ProcessEvent
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, event := range state.ProcessEvents {
			if event.AppID == appID {
				events = append(events, event)
			}
		}
		sort.SliceStable(events, func(i, j int) bool {
			return events[i].ID > events[j].ID
		})
		if len(events) > limit {
			events = events[:limit]
		}
		return nil
	})
	return events, err
}

func (s *Store) WriteProcessOutput(ctx context.Context, output ProcessOutput) error {
	if output.CreatedAt.IsZero() {
		output.CreatedAt = time.Now().UTC()
	}
	return s.withState(ctx, true, func(state *storeState) error {
		output.ID = state.NextProcessOutputID
		state.NextProcessOutputID++
		state.ProcessOutput = append(state.ProcessOutput, output)
		return nil
	})
}

func (s *Store) ListProcessOutput(ctx context.Context, appID string, limit int) ([]ProcessOutput, error) {
	return s.ListProcessOutputForSession(ctx, appID, "", limit)
}

func (s *Store) ListProcessOutputForSession(ctx context.Context, appID, sessionID string, limit int) ([]ProcessOutput, error) {
	return s.listProcessOutput(ctx, appID, sessionID, 0, limit)
}

func (s *Store) ListProcessOutputSince(ctx context.Context, appID string, afterID int64, limit int) ([]ProcessOutput, error) {
	return s.ListProcessOutputSinceForSession(ctx, appID, "", afterID, limit)
}

func (s *Store) ListProcessOutputSinceForSession(ctx context.Context, appID, sessionID string, afterID int64, limit int) ([]ProcessOutput, error) {
	return s.listProcessOutput(ctx, appID, sessionID, afterID, limit)
}

func (s *Store) listProcessOutput(ctx context.Context, appID, sessionID string, afterID int64, limit int) ([]ProcessOutput, error) {
	if limit <= 0 {
		limit = 200
	}
	var items []ProcessOutput
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, item := range state.ProcessOutput {
			if item.AppID != appID || item.ID <= afterID {
				continue
			}
			if sessionID != "" && item.SessionID != sessionID {
				continue
			}
			items = append(items, item)
		}
		if afterID > 0 {
			sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		} else {
			sort.SliceStable(items, func(i, j int) bool { return items[i].ID > items[j].ID })
		}
		if len(items) > limit {
			items = items[:limit]
		}
		if afterID == 0 {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		return nil
	})
	return items, err
}

func (s *Store) UpsertDevSource(ctx context.Context, appID, sessionID string, source DevSource) error {
	source = normalizeDevSource(source)
	if appID == "" || source.ID == "" {
		return nil
	}
	return s.withState(ctx, true, func(state *storeState) error {
		state.DevSources[devSourceKey(appID, sessionID, source.ID)] = source
		return nil
	})
}

func (s *Store) WriteDevEvent(ctx context.Context, event DevEvent) error {
	_, err := s.WriteDevEventReturningID(ctx, event)
	return err
}

func (s *Store) WriteDevEventReturningID(ctx context.Context, event DevEvent) (int64, error) {
	var id int64
	err := s.withState(ctx, true, func(state *storeState) error {
		event = normalizeDevEvent(event)
		if event.ID <= 0 {
			event.ID = state.NextDevEventID
		}
		if state.NextDevEventID <= event.ID {
			state.NextDevEventID = event.ID + 1
		}
		state.DevSources[devSourceKey(event.AppID, event.SessionID, event.Source.ID)] = event.Source
		state.DevEvents = append(state.DevEvents, storeDevEvent(event))
		id = event.ID
		return nil
	})
	return id, err
}

func (s *Store) NextDevEventID(ctx context.Context) (int64, error) {
	var id int64
	err := s.withState(ctx, true, func(state *storeState) error {
		id = state.NextDevEventID
		state.NextDevEventID++
		return nil
	})
	return id, err
}

func (s *Store) AdvanceDevEventID(ctx context.Context, nextID int64) error {
	if nextID <= 0 {
		return nil
	}
	return s.withState(ctx, true, func(state *storeState) error {
		if state.NextDevEventID < nextID {
			state.NextDevEventID = nextID
		}
		return nil
	})
}

func (s *Store) ListDevSources(ctx context.Context, appID, sessionID string) ([]DevSource, error) {
	var sources []DevSource
	err := s.withState(ctx, false, func(state *storeState) error {
		for key, source := range state.DevSources {
			kAppID, kSessionID, _ := splitDevSourceKey(key)
			if kAppID != appID {
				continue
			}
			if sessionID != "" && kSessionID != sessionID {
				continue
			}
			sources = append(sources, source)
		}
		sort.SliceStable(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
		return nil
	})
	return sources, err
}

func (s *Store) DeleteDevEventsForSession(ctx context.Context, appID, sessionID string) (int64, int64, error) {
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(sessionID) == "" {
		return 0, 0, nil
	}
	var eventCount, sourceCount int64
	err := s.withState(ctx, true, func(state *storeState) error {
		events := state.DevEvents[:0]
		for _, stored := range state.DevEvents {
			event := stored.toDevEvent()
			if event.AppID == appID && event.SessionID == sessionID {
				eventCount++
				continue
			}
			events = append(events, stored)
		}
		state.DevEvents = events
		for key := range state.DevSources {
			kAppID, kSessionID, _ := splitDevSourceKey(key)
			if kAppID == appID && kSessionID == sessionID {
				delete(state.DevSources, key)
				sourceCount++
			}
		}
		return nil
	})
	return eventCount, sourceCount, err
}

func (s *Store) ListDevEvents(ctx context.Context, query DevEventQuery) ([]DevEvent, error) {
	if query.Limit <= 0 {
		query.Limit = 200
	}
	var items []DevEvent
	err := s.withState(ctx, false, func(state *storeState) error {
		grep := strings.ToLower(strings.TrimSpace(query.Grep))
		for _, stored := range state.DevEvents {
			event := stored.toDevEvent()
			if !devEventMatchesQuery(event, query, grep) {
				continue
			}
			items = append(items, event)
		}
		if query.AfterID > 0 {
			sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		} else {
			sort.SliceStable(items, func(i, j int) bool { return items[i].ID > items[j].ID })
		}
		if len(items) > query.Limit {
			items = items[:query.Limit]
		}
		if query.AfterID == 0 {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		return nil
	})
	return items, err
}

func normalizeDevEvent(event DevEvent) DevEvent {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.Source = normalizeDevSource(event.Source)
	event.Level = normalizeDevLevel(event.Level, event.Source.Stream)
	event.Message = strings.TrimSpace(event.Message)
	if event.Message == "" {
		event.Message = strings.TrimSpace(event.Raw)
	}
	if event.Message == "" {
		event.Message = event.Level
	}
	if len(event.Fields) == 0 || !json.Valid(event.Fields) {
		event.Fields = json.RawMessage(`{}`)
	}
	if event.Parse.Format == "" {
		event.Parse.Format = "raw"
	}
	return event
}

func devEventMatchesQuery(event DevEvent, query DevEventQuery, grep string) bool {
	if event.AppID != query.AppID {
		return false
	}
	if query.SessionID != "" && event.SessionID != query.SessionID {
		return false
	}
	if query.AfterID > 0 && event.ID <= query.AfterID {
		return false
	}
	if query.SourceID != "" && event.Source.ID != query.SourceID {
		return false
	}
	if query.Kind != "" && event.Source.Kind != query.Kind {
		return false
	}
	if query.Level != "" && event.Level != query.Level {
		return false
	}
	if query.Stream != "" && query.Stream != "all" && event.Source.Stream != query.Stream {
		return false
	}
	if !query.Since.IsZero() && event.CreatedAt.Before(query.Since.UTC()) {
		return false
	}
	if grep == "" {
		return true
	}
	return strings.Contains(strings.ToLower(event.Message), grep) ||
		strings.Contains(strings.ToLower(event.Raw), grep) ||
		strings.Contains(strings.ToLower(string(event.Fields)), grep)
}

func devSourceKey(appID, sessionID, sourceID string) string {
	return appID + "\x00" + sessionID + "\x00" + sourceID
}

func splitDevSourceKey(key string) (string, string, string) {
	parts := strings.SplitN(key, "\x00", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

func (s *Store) AppendTraceSummary(ctx context.Context, summary *TraceSummary) error {
	return s.appendTraceSummary(ctx, summary)
}

func (s *Store) AppendTraceSummaryDeferred(ctx context.Context, summary *TraceSummary) error {
	return s.appendTraceSummary(ctx, summary)
}

func (s *Store) appendTraceSummary(ctx context.Context, summary *TraceSummary) error {
	_ = s
	_ = ctx
	if summary == nil {
		return errors.New("trace summary is nil")
	}
	return nil
}

func (s *Store) ListTraceSummaries(ctx context.Context, appID string, limit int, messageID string) ([]*TraceSummary, error) {
	return s.ListTraceSummariesForSession(ctx, appID, "", limit, messageID)
}

func (s *Store) ListTraceSummariesForSession(ctx context.Context, appID, sessionID string, limit int, messageID string) ([]*TraceSummary, error) {
	query := TraceQuery{AppID: appID, SessionID: sessionID, Limit: limit}
	items, err := s.queryTraceSummaries(ctx, query, messageID, false)
	return items, err
}

func (s *Store) GetTraceSummaries(ctx context.Context, appID, traceID string) ([]*TraceSummary, error) {
	return s.GetTraceSummariesForSession(ctx, appID, "", traceID)
}

func (s *Store) GetTraceSummariesForSession(ctx context.Context, appID, sessionID, traceID string) ([]*TraceSummary, error) {
	query := TraceQuery{AppID: appID, SessionID: sessionID, TraceID: traceID, Limit: 0}
	items, err := s.queryTraceSummaries(ctx, query, "", true)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsRoot != items[j].IsRoot {
			return items[i].IsRoot
		}
		return items[i].StartedAt.Before(items[j].StartedAt)
	})
	return items, nil
}

func (s *Store) QueryTraceSummaries(ctx context.Context, query TraceQuery) ([]*TraceSummary, error) {
	if query.Limit <= 0 {
		query.Limit = 100
	}
	return s.queryTraceSummaries(ctx, query, "", false)
}

func (s *Store) QueryTraceMetrics(ctx context.Context, query TraceQuery) ([]*TraceSummary, error) {
	if query.Limit <= 0 {
		query.Limit = 10000
	}
	return s.QueryTraceSummaries(ctx, query)
}

func (s *Store) queryTraceSummaries(ctx context.Context, query TraceQuery, messageID string, includeChildren bool) ([]*TraceSummary, error) {
	_ = s
	_ = ctx
	_ = query
	_ = messageID
	_ = includeChildren
	return []*TraceSummary{}, nil
}

func (s *Store) AppendTraceEvent(ctx context.Context, event *TraceEvent) error {
	return s.appendTraceEvent(ctx, event)
}

func (s *Store) AppendTraceEventDeferred(ctx context.Context, event *TraceEvent) error {
	return s.appendTraceEvent(ctx, event)
}

func (s *Store) appendTraceEvent(ctx context.Context, event *TraceEvent) error {
	_ = s
	_ = ctx
	if event == nil {
		return errors.New("trace event is nil")
	}
	return nil
}

func (s *Store) GetTraceEvents(ctx context.Context, appID, traceID, spanID string) ([]*TraceEvent, error) {
	return s.GetTraceEventsForSession(ctx, appID, "", traceID, spanID)
}

func (s *Store) GetTraceEventsForSession(ctx context.Context, appID, sessionID, traceID, spanID string) ([]*TraceEvent, error) {
	_ = s
	_ = ctx
	_ = appID
	_ = sessionID
	_ = traceID
	_ = spanID
	return []*TraceEvent{}, nil
}

func (s *Store) WriteLogEvent(ctx context.Context, event *LogEvent) error {
	return s.writeLogEvent(ctx, event)
}

func (s *Store) WriteLogEventDeferred(ctx context.Context, event *LogEvent) error {
	return s.writeLogEvent(ctx, event)
}

func (s *Store) writeLogEvent(ctx context.Context, event *LogEvent) error {
	_ = s
	_ = ctx
	if event == nil {
		return errors.New("log event is nil")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return nil
}

func (s *Store) ClearTraces(ctx context.Context, appID string) error {
	return s.ClearTracesForSession(ctx, appID, "")
}

func (s *Store) ClearTracesForSession(ctx context.Context, appID, sessionID string) error {
	_ = s
	_ = ctx
	_ = appID
	_ = sessionID
	return nil
}

func (s *Store) CountTraceEvents(ctx context.Context, appID string, since time.Time) (int64, error) {
	return s.CountTraceEventsForSession(ctx, appID, "", since)
}

func (s *Store) CountTraceEventsForSession(ctx context.Context, appID, sessionID string, since time.Time) (int64, error) {
	_ = s
	_ = ctx
	_ = appID
	_ = sessionID
	_ = since
	return 0, nil
}

func (s *Store) CountLogsByLevel(ctx context.Context, appID string, since time.Time) ([]LogLevelCount, error) {
	return s.CountLogsByLevelForSession(ctx, appID, "", since)
}

func (s *Store) CountLogsByLevelForSession(ctx context.Context, appID, sessionID string, since time.Time) ([]LogLevelCount, error) {
	_ = s
	_ = ctx
	_ = appID
	_ = sessionID
	_ = since
	return []LogLevelCount{}, nil
}

func (s *Store) GetOnboarding(ctx context.Context) (OnboardingState, error) {
	stateOut := OnboardingState{}
	err := s.withState(ctx, false, func(state *storeState) error {
		maps.Copy(stateOut, state.Onboarding)
		return nil
	})
	return stateOut, err
}

func (s *Store) SetOnboarding(ctx context.Context, props []string) error {
	return s.withState(ctx, true, func(state *storeState) error {
		now := time.Now().UTC()
		for _, prop := range props {
			if prop == "" {
				continue
			}
			state.Onboarding[prop] = now
		}
		return nil
	})
}

func (s *Store) ListStoredRequests(ctx context.Context, appID string) ([]StoredRequest, error) {
	var list []StoredRequest
	err := s.withState(ctx, false, func(state *storeState) error {
		for key, req := range state.StoredRequests {
			kAppID, _ := splitStoredRequestKey(key)
			if kAppID != appID {
				continue
			}
			req.AppID = appID
			list = append(list, sanitizeStoredRequest(req))
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		return nil
	})
	return list, err
}

func (s *Store) CreateStoredRequest(ctx context.Context, req StoredRequest) (StoredRequest, error) {
	if req.AppID == "" {
		return StoredRequest{}, errors.New("stored request app id is required")
	}
	req = sanitizeStoredRequest(req)
	if req.ID == "" {
		id, err := newStoredRequestID()
		if err != nil {
			return StoredRequest{}, err
		}
		req.ID = id
	}
	err := s.withState(ctx, true, func(state *storeState) error {
		key := storedRequestKey(req.AppID, req.ID)
		if _, exists := state.StoredRequests[key]; exists {
			return fmt.Errorf("stored request %q already exists", req.ID)
		}
		state.StoredRequests[key] = req
		return nil
	})
	return req, err
}

func (s *Store) UpdateStoredRequest(ctx context.Context, req StoredRequest) (StoredRequest, error) {
	if req.AppID == "" {
		return StoredRequest{}, errors.New("stored request app id is required")
	}
	if req.ID == "" {
		return StoredRequest{}, errors.New("stored request id is required")
	}
	req = sanitizeStoredRequest(req)
	err := s.withState(ctx, true, func(state *storeState) error {
		key := storedRequestKey(req.AppID, req.ID)
		if _, exists := state.StoredRequests[key]; !exists {
			return sql.ErrNoRows
		}
		state.StoredRequests[key] = req
		return nil
	})
	return req, err
}

func (s *Store) DeleteStoredRequest(ctx context.Context, appID, id string) error {
	if appID == "" {
		return errors.New("stored request app id is required")
	}
	if id == "" {
		return errors.New("stored request id is required")
	}
	return s.withState(ctx, true, func(state *storeState) error {
		key := storedRequestKey(appID, id)
		if _, exists := state.StoredRequests[key]; !exists {
			return sql.ErrNoRows
		}
		delete(state.StoredRequests, key)
		return nil
	})
}

func storedRequestKey(appID, id string) string {
	return appID + "\x00" + id
}

func splitStoredRequestKey(key string) (string, string) {
	appID, id, ok := strings.Cut(key, "\x00")
	if !ok {
		return "", key
	}
	return appID, id
}

func SortTraceSummariesByDuration(items []*TraceSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].DurationNanos == items[j].DurationNanos {
			return items[i].StartedAt.After(items[j].StartedAt)
		}
		return items[i].DurationNanos > items[j].DurationNanos
	})
}

func sanitizeStoredRequest(req StoredRequest) StoredRequest {
	req.Data.PathParams = normalizeStoredRequestJSON(req.Data.PathParams)
	req.Data.Payload = normalizeStoredRequestJSON(req.Data.Payload)
	return req
}

func normalizeStoredRequestJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return compactRawMessage(value)
}

func compactRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return value
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return append(json.RawMessage(nil), value...)
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return append(json.RawMessage(nil), value...)
	}
	return normalized
}

func copyRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func appRevisionFromMetadata(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var decoded struct {
		AppRevision string `json:"app_revision"`
	}
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		return ""
	}
	return decoded.AppRevision
}

func (s *Store) putAppModelBlob(kind, appID, root, appRevision string, value json.RawMessage) (StoredAppModelRef, error) {
	value = compactRawMessage(value)
	if isEmptyJSONValue(value) {
		return StoredAppModelRef{}, nil
	}
	hashBytes := sha256.Sum256(value)
	hash := hex.EncodeToString(hashBytes[:])
	ref := kind + ":sha256:" + hash
	blobDir := filepath.Join(filepath.Dir(s.path), "app-model", kind, "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		return StoredAppModelRef{}, err
	}
	blobPath := filepath.Join(blobDir, hash+".json")
	if _, err := os.Stat(blobPath); errors.Is(err, os.ErrNotExist) {
		tmp, err := os.CreateTemp(blobDir, "."+hash+"-*.json")
		if err != nil {
			return StoredAppModelRef{}, err
		}
		tmpName := tmp.Name()
		ok := false
		defer func() {
			if !ok {
				_ = os.Remove(tmpName)
			}
		}()
		if _, err := tmp.Write(value); err != nil {
			_ = tmp.Close()
			return StoredAppModelRef{}, err
		}
		if _, err := tmp.Write([]byte("\n")); err != nil {
			_ = tmp.Close()
			return StoredAppModelRef{}, err
		}
		if err := tmp.Close(); err != nil {
			return StoredAppModelRef{}, err
		}
		if err := os.Rename(tmpName, blobPath); err != nil {
			return StoredAppModelRef{}, err
		}
		ok = true
	} else if err != nil {
		return StoredAppModelRef{}, err
	}
	rel, err := filepath.Rel(filepath.Dir(s.path), blobPath)
	if err != nil {
		rel = blobPath
	}
	return StoredAppModelRef{
		Ref:         ref,
		Kind:        kind,
		Hash:        hash,
		AppID:       appID,
		Root:        root,
		AppRevision: appRevision,
		Path:        rel,
		Bytes:       int64(len(value)),
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func (s *Store) readAppModelBlob(ref string) (json.RawMessage, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, nil
	}
	kind, hash, ok := splitAppModelRef(ref)
	if !ok {
		return nil, fmt.Errorf("invalid app model ref %q", ref)
	}
	blobPath := filepath.Join(filepath.Dir(s.path), "app-model", kind, "sha256", hash+".json")
	data, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, fmt.Errorf("read app model blob %s: %w", ref, err)
	}
	return compactRawMessage(data), nil
}

func splitAppModelRef(ref string) (string, string, bool) {
	kind, rest, ok := strings.Cut(ref, ":sha256:")
	if !ok || strings.TrimSpace(kind) == "" || len(rest) != sha256.Size*2 {
		return "", "", false
	}
	if _, err := hex.DecodeString(rest); err != nil {
		return "", "", false
	}
	return kind, rest, true
}

func isEmptyJSONValue(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("{}")) || bytes.Equal(value, []byte("[]")) {
		return true
	}
	return false
}

func newStoredRequestID() (string, error) {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("sr_%x", data[:]), nil
}
