package main

import (
	"context"
	"fmt"
	"strings"

	"scenery.sh/internal/devdash"
)

func (s *dashboardServer) listTraceSummaries(ctx context.Context, appID, sessionID string, limit int, messageID string) ([]*devdash.TraceSummary, error) {
	if victoria := s.dashboardVictoria(); victoria != nil {
		items, err := victoria.QueryTraceSummaries(ctx, devdash.TraceQuery{
			AppID:     appID,
			SessionID: sessionID,
			Limit:     limit,
		})
		if err != nil {
			return nil, err
		}
		if messageID != "" {
			items = filterTraceSummariesByMessageID(items, messageID)
		}
		return items, nil
	}
	return nil, fmt.Errorf("VictoriaTraces is unavailable")
}

func filterTraceSummariesByMessageID(items []*devdash.TraceSummary, messageID string) []*devdash.TraceSummary {
	if messageID == "" {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if item.MessageID != nil && strings.Contains(*item.MessageID, messageID) {
			out = append(out, item)
		}
	}
	return out
}
