package devdash

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
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
	maxStoredProcessEvents  = 1000
	maxStoredProcessOutput  = 5000
	maxStoredDevEvents      = 5000
	maxStoredTraceSummaries = 2000
	maxStoredTraceEvents    = 6000
	maxStoredLogEvents      = 5000
	deferredSaveDelay       = 500 * time.Millisecond

	// Process events are diagnostic breadcrumbs. A payload above this size
	// (e.g. full app metadata on every reload) bloats devdash.json until
	// every store refresh re-parses hundreds of megabytes of JSON.
	maxProcessEventPayloadBytes = 64 * 1024
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
	Version             int                      `json:"version"`
	Apps                map[string]AppRecord     `json:"apps,omitempty"`
	AppSessions         map[string]AppRecord     `json:"app_sessions,omitempty"`
	ProcessEvents       []ProcessEvent           `json:"process_events,omitempty"`
	ProcessOutput       []ProcessOutput          `json:"process_output,omitempty"`
	DevSources          map[string]DevSource     `json:"dev_sources,omitempty"`
	DevEvents           []storedDevEvent         `json:"dev_events,omitempty"`
	TraceSummaries      []storedTraceSummary     `json:"trace_summaries,omitempty"`
	TraceEvents         []storedTraceEvent       `json:"trace_events,omitempty"`
	LogEvents           []LogEvent               `json:"log_events,omitempty"`
	Onboarding          OnboardingState          `json:"onboarding,omitempty"`
	StoredRequests      map[string]StoredRequest `json:"stored_requests,omitempty"`
	NextProcessEventID  int64                    `json:"next_process_event_id,omitempty"`
	NextProcessOutputID int64                    `json:"next_process_output_id,omitempty"`
	NextDevEventID      int64                    `json:"next_dev_event_id,omitempty"`
}

type storedDevEvent struct {
	DevEvent
	AppID     string    `json:"app_id"`
	AppRoot   string    `json:"app_root,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type storedTraceSummary struct {
	TraceSummary
	AppID     string `json:"app_id"`
	TestTrace bool   `json:"test_trace,omitempty"`
}

type storedTraceEvent struct {
	TraceEvent
	AppID string          `json:"app_id"`
	Data  json.RawMessage `json:"data,omitempty"`
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

func (s *Store) withDeferredState(ctx context.Context, mutation storeMutation) error {
	return s.withStatePersist(ctx, true, false, mutation)
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
	data, err := json.Marshal(state)
	if err != nil {
		return err
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
	state.TraceSummaries = tailSlice(state.TraceSummaries, maxStoredTraceSummaries)
	state.TraceEvents = tailSlice(state.TraceEvents, maxStoredTraceEvents)
	state.LogEvents = tailSlice(state.LogEvents, maxStoredLogEvents)
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
		state.Apps = map[string]AppRecord{}
	}
	if state.AppSessions == nil {
		state.AppSessions = map[string]AppRecord{}
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
			state.AppSessions[appSessionRecordKey(app)] = app
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

func storeTraceSummary(summary TraceSummary) storedTraceSummary {
	return storedTraceSummary{
		TraceSummary: summary,
		AppID:        summary.AppID,
		TestTrace:    summary.TestTrace,
	}
}

func (summary storedTraceSummary) toTraceSummary() TraceSummary {
	item := summary.TraceSummary
	item.AppID = summary.AppID
	item.TestTrace = summary.TestTrace
	return item
}

func storeTraceEvent(event TraceEvent) storedTraceEvent {
	return storedTraceEvent{
		TraceEvent: event,
		AppID:      event.AppID,
		Data:       event.Data,
	}
}

func (event storedTraceEvent) toTraceEvent() TraceEvent {
	item := event.TraceEvent
	item.AppID = event.AppID
	item.Data = event.Data
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

func (s *Store) UpsertApp(ctx context.Context, app AppRecord) error {
	app = normalizeAppRecord(app)
	return s.withState(ctx, true, func(state *storeState) error {
		legacy := app
		legacy.RouteID = legacy.ID
		state.Apps[app.ID] = legacy
		session := app
		session.RouteID = appSessionRecordKey(app)
		state.AppSessions[session.RouteID] = session
		return nil
	})
}

func (s *Store) ListApps(ctx context.Context) ([]AppRecord, error) {
	var apps []AppRecord
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, app := range state.Apps {
			app.RouteID = app.ID
			app.Offline = !app.Running
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
		for routeID, app := range state.AppSessions {
			app.RouteID = routeID
			app.Offline = !app.Running
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
		var found bool
		app, found = state.Apps[appID]
		if !found {
			return sql.ErrNoRows
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
			app = found
			app.RouteID = routeID
			app.Offline = !app.Running
			return nil
		}
		var matches []AppRecord
		for key, candidate := range state.AppSessions {
			if candidate.SessionID == routeID {
				candidate.RouteID = key
				matches = append(matches, candidate)
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
				candidate.RouteID = key
				matches = append(matches, candidate)
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
	return s.appendTraceSummary(ctx, summary, false)
}

func (s *Store) AppendTraceSummaryDeferred(ctx context.Context, summary *TraceSummary) error {
	return s.appendTraceSummary(ctx, summary, true)
}

func (s *Store) appendTraceSummary(ctx context.Context, summary *TraceSummary, deferred bool) error {
	if summary == nil {
		return errors.New("trace summary is nil")
	}
	replacement := storeTraceSummary(*summary)
	update := func(state *storeState) error {
		for i, existing := range state.TraceSummaries {
			if existing.AppID == replacement.AppID && existing.SessionID == replacement.SessionID && existing.TraceID == replacement.TraceID && existing.SpanID == replacement.SpanID {
				state.TraceSummaries[i] = replacement
				return nil
			}
		}
		state.TraceSummaries = append(state.TraceSummaries, replacement)
		return nil
	}
	if deferred {
		return s.withDeferredState(ctx, update)
	}
	return s.withState(ctx, true, update)
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
	if query.Limit <= 0 && !includeChildren {
		query.Limit = 100
	}
	var items []*TraceSummary
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, stored := range state.TraceSummaries {
			summary := stored.toTraceSummary()
			if !traceSummaryMatches(summary, query, messageID, includeChildren) {
				continue
			}
			item := summary
			items = append(items, &item)
		}
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].StartedAt.After(items[j].StartedAt)
		})
		if query.Limit > 0 && len(items) > query.Limit {
			items = items[:query.Limit]
		}
		return nil
	})
	return items, err
}

func traceSummaryMatches(summary TraceSummary, query TraceQuery, messageID string, includeChildren bool) bool {
	if summary.AppID != query.AppID {
		return false
	}
	if !includeChildren && !summary.IsRoot {
		return false
	}
	if query.SessionID != "" && summary.SessionID != query.SessionID {
		return false
	}
	if query.TraceID != "" && summary.TraceID != query.TraceID {
		return false
	}
	if query.ServiceName != "" && summary.ServiceName != query.ServiceName {
		return false
	}
	if query.EndpointName != "" && (summary.EndpointName == nil || *summary.EndpointName != query.EndpointName) {
		return false
	}
	switch query.Status {
	case "ok":
		if summary.IsError {
			return false
		}
	case "error":
		if !summary.IsError {
			return false
		}
	}
	if !query.Since.IsZero() && summary.StartedAt.Before(query.Since.UTC()) {
		return false
	}
	if query.MinDurationNanos > 0 && summary.DurationNanos < query.MinDurationNanos {
		return false
	}
	if messageID != "" {
		data, _ := json.Marshal(summary)
		if !strings.Contains(string(data), messageID) {
			return false
		}
	}
	return true
}

func (s *Store) AppendTraceEvent(ctx context.Context, event *TraceEvent) error {
	return s.appendTraceEvent(ctx, event, false)
}

func (s *Store) AppendTraceEventDeferred(ctx context.Context, event *TraceEvent) error {
	return s.appendTraceEvent(ctx, event, true)
}

func (s *Store) appendTraceEvent(ctx context.Context, event *TraceEvent, deferred bool) error {
	if event == nil {
		return errors.New("trace event is nil")
	}
	stored := storeTraceEvent(*event)
	update := func(state *storeState) error {
		state.TraceEvents = append(state.TraceEvents, stored)
		return nil
	}
	if deferred {
		return s.withDeferredState(ctx, update)
	}
	return s.withState(ctx, true, update)
}

func (s *Store) GetTraceEvents(ctx context.Context, appID, traceID, spanID string) ([]*TraceEvent, error) {
	return s.GetTraceEventsForSession(ctx, appID, "", traceID, spanID)
}

func (s *Store) GetTraceEventsForSession(ctx context.Context, appID, sessionID, traceID, spanID string) ([]*TraceEvent, error) {
	var list []*TraceEvent
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, stored := range state.TraceEvents {
			event := stored.toTraceEvent()
			if event.AppID != appID || event.TraceID != traceID || event.SpanID != spanID {
				continue
			}
			if sessionID != "" && event.SessionID != sessionID {
				continue
			}
			item := event
			if item.SessionID == "" {
				item.SessionID = sessionID
			}
			list = append(list, &item)
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].EventID < list[j].EventID })
		return nil
	})
	return list, err
}

func (s *Store) WriteLogEvent(ctx context.Context, event *LogEvent) error {
	return s.writeLogEvent(ctx, event, false)
}

func (s *Store) WriteLogEventDeferred(ctx context.Context, event *LogEvent) error {
	return s.writeLogEvent(ctx, event, true)
}

func (s *Store) writeLogEvent(ctx context.Context, event *LogEvent, deferred bool) error {
	if event == nil {
		return errors.New("log event is nil")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	stored := *event
	update := func(state *storeState) error {
		state.LogEvents = append(state.LogEvents, stored)
		return nil
	}
	if deferred {
		return s.withDeferredState(ctx, update)
	}
	return s.withState(ctx, true, update)
}

func (s *Store) ClearTraces(ctx context.Context, appID string) error {
	return s.ClearTracesForSession(ctx, appID, "")
}

func (s *Store) ClearTracesForSession(ctx context.Context, appID, sessionID string) error {
	return s.withState(ctx, true, func(state *storeState) error {
		state.TraceSummaries = filterTraceSummaries(state.TraceSummaries, appID, sessionID)
		state.TraceEvents = filterTraceEvents(state.TraceEvents, appID, sessionID)
		state.LogEvents = filterLogEvents(state.LogEvents, appID, sessionID)
		return nil
	})
}

func filterTraceSummaries(items []storedTraceSummary, appID, sessionID string) []storedTraceSummary {
	out := items[:0]
	for _, item := range items {
		if item.AppID == appID && (sessionID == "" || item.SessionID == sessionID) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterTraceEvents(items []storedTraceEvent, appID, sessionID string) []storedTraceEvent {
	out := items[:0]
	for _, item := range items {
		if item.AppID == appID && (sessionID == "" || item.SessionID == sessionID) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterLogEvents(items []LogEvent, appID, sessionID string) []LogEvent {
	out := items[:0]
	for _, item := range items {
		if item.AppID == appID && (sessionID == "" || item.SessionID == sessionID) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (s *Store) CountTraceEvents(ctx context.Context, appID string, since time.Time) (int64, error) {
	return s.CountTraceEventsForSession(ctx, appID, "", since)
}

func (s *Store) CountTraceEventsForSession(ctx context.Context, appID, sessionID string, since time.Time) (int64, error) {
	var count int64
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, stored := range state.TraceEvents {
			event := stored.toTraceEvent()
			if event.AppID != appID {
				continue
			}
			if sessionID != "" && event.SessionID != sessionID {
				continue
			}
			if !since.IsZero() && event.EventTime.Before(since.UTC()) {
				continue
			}
			count++
		}
		return nil
	})
	return count, err
}

func (s *Store) CountLogsByLevel(ctx context.Context, appID string, since time.Time) ([]LogLevelCount, error) {
	return s.CountLogsByLevelForSession(ctx, appID, "", since)
}

func (s *Store) CountLogsByLevelForSession(ctx context.Context, appID, sessionID string, since time.Time) ([]LogLevelCount, error) {
	counts := map[string]int64{}
	err := s.withState(ctx, false, func(state *storeState) error {
		for _, event := range state.LogEvents {
			if event.AppID != appID {
				continue
			}
			if sessionID != "" && event.SessionID != sessionID {
				continue
			}
			if !since.IsZero() && event.Timestamp.Before(since.UTC()) {
				continue
			}
			counts[event.Level]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := make([]LogLevelCount, 0, len(counts))
	for level, count := range counts {
		items = append(items, LogLevelCount{Level: level, Count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Level < items[j].Level
	})
	return items, nil
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

func newStoredRequestID() (string, error) {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("sr_%x", data[:]), nil
}
