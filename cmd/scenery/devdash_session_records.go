package main

import (
	"context"
	"database/sql"
	"sort"

	"scenery.sh/internal/devdash"
)

func devdashAppRecordForRuntime(ctx context.Context, store *devdash.Store, appID, sessionID, appRoot string) (devdash.AppRecord, bool, error) {
	if sessionID != "" {
		record, err := store.GetAppForSession(ctx, appID, sessionID)
		if err == nil {
			return record, true, nil
		}
		if err != sql.ErrNoRows {
			return devdash.AppRecord{}, false, err
		}
	}
	if appRoot != "" {
		records, err := store.ListAppSessions(ctx)
		if err != nil {
			return devdash.AppRecord{}, false, err
		}
		var matches []devdash.AppRecord
		for _, candidate := range records {
			if candidate.ID == appID && cleanAbsPath(candidate.Root) == cleanAbsPath(appRoot) && candidate.SessionID != "" {
				matches = append(matches, candidate)
			}
		}
		if len(matches) > 0 {
			sort.SliceStable(matches, func(i, j int) bool {
				if matches[i].Running != matches[j].Running {
					return matches[i].Running
				}
				return matches[i].UpdatedAt.After(matches[j].UpdatedAt)
			})
			return matches[0], true, nil
		}
	}
	record, err := store.GetApp(ctx, appID)
	if err != nil {
		return devdash.AppRecord{}, false, err
	}
	return record, false, nil
}
