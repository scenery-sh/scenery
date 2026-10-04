package devdash

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

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

func pruneStoreStateToBudget(state *storeState, targetBytes, currentBytes int) {
	if state == nil || targetBytes <= 0 {
		return
	}
	for currentBytes > targetBytes {
		var removed int
		switch {
		case len(state.ProcessOutput) > 0:
			state.ProcessOutput, removed = dropOldestBudgetChunk(state.ProcessOutput, "process_output")
		case len(state.DevEvents) > 0:
			state.DevEvents, removed = dropOldestBudgetChunk(state.DevEvents, "dev_events")
		case len(state.ProcessEvents) > 0:
			state.ProcessEvents, removed = dropOldestBudgetChunk(state.ProcessEvents, "process_events")
		default:
			return
		}
		if removed == 0 {
			return
		}
		currentBytes -= removed
	}
}

// dropOldestBudgetChunk accounts only for discarded entries, not the whole
// remaining store. Every history field is omitempty and follows version.
func dropOldestBudgetChunk[T any](items []T, field string) ([]T, int) {
	drop := min(max(len(items)/4, 1), 256, len(items))
	encoded, err := json.Marshal(items[:drop])
	if err != nil {
		return items, 0
	}
	clear(items[:drop])
	if drop == len(items) {
		return nil, len(encoded) + len(field) + len(`,"":`)
	}
	// Replace the encoded chunk's two brackets with the comma separating it
	// from the remaining array; its internal commas are already accounted.
	return items[drop:], len(encoded) - 1
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
	clear(items[:len(items)-max])
	return items[len(items)-max:]
}
