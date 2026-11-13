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
	pruneOrphanedAppModelRefs(state)
}

func storeSizeBreakdown(state *storeState) map[string]int {
	if state == nil {
		return nil
	}
	parts := map[string]any{
		"apps":           state.Apps,
		"app_sessions":   state.AppSessions,
		"app_model_refs": state.AppModelRefs,
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
