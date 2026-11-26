package main

import (
	"encoding/json"
	"sort"
	"strings"

	"scenery.sh/internal/devdash"
)

func workerDevEvents(records []workerLogEntry, query devdash.DevEventQuery) []devdash.DevEvent {
	var events []devdash.DevEvent
	for _, record := range records {
		if record.EventID <= query.AfterID || record.Timestamp.Before(query.Since) ||
			query.Level != "" && record.Level != query.Level ||
			query.SourceID != "" && record.Service != query.SourceID ||
			query.Kind != "" && query.Kind != "app" ||
			query.Stream != "" && query.Stream != "all" && query.Stream != "stderr" ||
			query.Grep != "" && !strings.Contains(record.Message, query.Grep) {
			continue
		}
		fields, _ := json.Marshal(record.LogEvent)
		events = append(events, devdash.DevEvent{
			ID: record.EventID, AppID: record.AppID, SessionID: record.SessionID,
			Source: devdash.DevSource{ID: record.Service, Kind: "app", Name: record.Service, Role: record.Runtime, Stream: "stderr"},
			Level:  record.Level, Message: record.Message, Fields: fields, CreatedAt: record.Timestamp,
			Parse: devdash.DevEventParse{Format: "json", OK: true},
		})
	}
	if query.Limit > 0 && len(events) > query.Limit {
		if query.AfterID > 0 {
			return events[:query.Limit]
		}
		return events[len(events)-query.Limit:]
	}
	return events
}

func workerTraceSummaries(spans []devdash.TraceSpanDetail, query devdash.TraceQuery) []*devdash.TraceSummary {
	items := make([]*devdash.TraceSummary, 0, len(spans))
	for _, span := range spans {
		summary := &span.TraceSummary
		// A summary query lists requests; only one trace shows the spans within.
		if query.TraceID == "" && !summary.IsRoot ||
			summary.StartedAt.Before(query.Since) || summary.DurationNanos < query.MinDurationNanos ||
			query.ServiceName != "" && summary.ServiceName != query.ServiceName ||
			query.EndpointName != "" && (summary.EndpointName == nil || *summary.EndpointName != query.EndpointName) ||
			query.Status == "ok" && summary.IsError || query.Status == "error" && !summary.IsError {
			continue
		}
		items = append(items, summary)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].StartedAt.After(items[j].StartedAt) })
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return items
}
