package victoria

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"scenery.sh/internal/devdash"
)

// GetTraceDetail filters every span independently; an ID alone never grants
// access to another application or worktree session in the shared backend.
func (s *Stack) GetTraceDetail(ctx context.Context, query devdash.TraceQuery) (*devdash.TraceDetail, error) {
	if query.AppID == "" || query.SessionID == "" || query.TraceID == "" {
		return nil, errors.New("trace detail requires application, session and trace identities")
	}
	result := &devdash.TraceDetail{TraceID: query.TraceID, Spans: []devdash.TraceSpanDetail{}}
	traces, err := getVictoriaJaegerTrace(ctx, s.BaseURL("traces"), query.TraceID)
	if err != nil {
		if errors.Is(err, errNoTraces) {
			return result, nil
		}
		return nil, err
	}
	for _, trace := range traces {
		for _, span := range trace.Spans {
			if !victoriaSpanAppMatches(query.AppID, trace, span) {
				continue
			}
			summary := traceSummaryFromVictoriaSpan(query.AppID, trace, span)
			if summary == nil || summary.TraceID != query.TraceID || summary.SessionID != query.SessionID || summary.StartedAt.Before(s.ClearedAt(query.AppID)) {
				continue
			}
			detail := devdash.TraceSpanDetail{TraceSummary: *summary, Events: []devdash.TraceDetailEvent{}}
			for _, entry := range span.Logs {
				fields := victoriaTagsMap(entry.Fields)
				data := map[string]any{}
				switch raw := fields["scenery.event"].(type) {
				case map[string]any:
					data = raw
				case string:
					if err := json.Unmarshal([]byte(raw), &data); err != nil {
						continue
					}
				}
				detail.Events = append(detail.Events, devdash.TraceDetailEvent{Time: time.UnixMicro(entry.Timestamp).UTC(), Name: stringTag(fields, "event"), Data: data})
			}
			result.Spans = append(result.Spans, detail)
		}
	}
	sort.SliceStable(result.Spans, func(i, j int) bool { return result.Spans[i].StartedAt.Before(result.Spans[j].StartedAt) })
	return result, nil
}

func victoriaSpanAppMatches(appID string, trace victoriaJaegerTrace, span victoriaJaegerSpan) bool {
	if appID == "" {
		return true
	}
	tags := victoriaTagsMap(span.Tags)
	return stringTag(tags, "scenery.application_id") == appID && trace.Processes[span.ProcessID].ServiceName == appID
}
