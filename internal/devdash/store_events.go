package devdash

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

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
	return s.withStateDeferred(ctx, func(state *storeState) error {
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
	return s.withStateDeferred(ctx, func(state *storeState) error {
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
	err := s.withStateDeferred(ctx, func(state *storeState) error {
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
