package main

import (
	"context"
	"testing"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/devreport"
)

// Host application logs join the supervisor's events in time order exactly
// once, without moving the development event cursor.
func TestHostLogsJoinDevelopmentEventsOnce(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	supervisor := func(_ context.Context, query devdash.DevEventQuery) ([]devdash.DevEvent, error) {
		if query.AfterID >= 7 {
			return nil, nil
		}
		return []devdash.DevEvent{{ID: 7, Message: "build succeeded", CreatedAt: start.Add(2 * time.Second)}}, nil
	}
	host := []workerLogEntry{{EventID: 1, LogEvent: devreport.LogEvent{Message: "starting request", Timestamp: start.Add(time.Second)}}}
	list := withHostLogs(supervisor, func(context.Context) ([]workerLogEntry, error) { return host, nil })
	first, err := list(context.Background(), devdash.DevEventQuery{})
	if err != nil || len(first) != 2 || first[0].Message != "starting request" || first[0].ID != 0 || first[1].ID != 7 {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	next, err := list(context.Background(), devdash.DevEventQuery{AfterID: 7})
	if err != nil || len(next) != 0 {
		t.Fatalf("next page = %+v, %v", next, err)
	}
}
