package main

import (
	"context"
	"io"
	"time"

	"scenery.sh/internal/devdash"
)

var resolveLogsVictoriaStackFunc = resolveLogsVictoriaStack

func followDevEventBackend(ctx context.Context, stdout io.Writer, backend *victoriaStack, appID, appRoot, sessionID string, opts logsOptions, items []devdash.DevEvent) error {
	lastID := int64(0)
	for _, item := range items {
		if item.ID > lastID {
			lastID = item.ID
		}
		if err := writeDevEventOutput(stdout, appID, appRoot, item, opts.JSONL); err != nil {
			return err
		}
	}
	if !opts.Follow {
		return nil
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			query := logsDevEventQuery(opts, appID, sessionID)
			query.AfterID = lastID
			items, err := backend.ListDevEvents(ctx, query)
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.ID > lastID {
					lastID = item.ID
				}
				if err := writeDevEventOutput(stdout, appID, appRoot, item, opts.JSONL); err != nil {
					return err
				}
			}
		}
	}
}
