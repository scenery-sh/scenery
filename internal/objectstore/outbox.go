package objectstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type outboxDraft struct {
	TenantID      string
	ObjectID      string
	ObjectName    string
	RecordID      string
	Action        string
	ActorID       string
	SchemaVersion int64
	ChangedFields []string
	Before        Record
	After         Record
	Diff          Record
}

func (s *Store) insertOutbox(ctx context.Context, q Queryer, draft outboxDraft) (*Event, error) {
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	beforeData, err := json.Marshal(draft.Before)
	if err != nil {
		return nil, err
	}
	afterData, err := json.Marshal(draft.After)
	if err != nil {
		return nil, err
	}
	diffData, err := json.Marshal(draft.Diff)
	if err != nil {
		return nil, err
	}
	event := &Event{
		EventID:       id,
		TenantID:      draft.TenantID,
		ObjectID:      draft.ObjectID,
		Object:        draft.ObjectName,
		RecordID:      draft.RecordID,
		Action:        draft.Action,
		ActorID:       draft.ActorID,
		SchemaVersion: draft.SchemaVersion,
		ChangedFields: append([]string(nil), draft.ChangedFields...),
		Before:        cloneRecord(draft.Before),
		After:         cloneRecord(draft.After),
		Diff:          cloneRecord(draft.Diff),
		CreatedAt:     s.now(),
	}
	err = q.QueryRow(ctx, `
		insert into `+qualifiedIdent(MetadataSchema, "outbox_events")+` (
			id, tenant_id, object_id, object_name, record_id, action, actor_id,
			schema_version, changed_fields, before, after, diff, created_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12::jsonb, $13)
		returning seq, created_at
	`, event.EventID, event.TenantID, nullableUUID(event.ObjectID), event.Object, nullableUUID(event.RecordID),
		event.Action, event.ActorID, event.SchemaVersion, event.ChangedFields,
		string(beforeData), string(afterData), string(diffData), event.CreatedAt,
	).Scan(&event.Seq, &event.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert data outbox event: %w", err)
	}
	return event, nil
}

func (s *Store) eventsAfter(ctx context.Context, afterSeq int64, tenantIDs map[string]bool, limit int) ([]*Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query := `
		select seq, id::text, tenant_id::text, coalesce(object_id::text, ''), object_name,
		       coalesce(record_id::text, ''), action, actor_id, schema_version, changed_fields,
		       before, after, diff, created_at
		from ` + qualifiedIdent(MetadataSchema, "outbox_events") + `
		where seq > $1
		order by seq asc
		limit $2
	`
	args := []any{afterSeq, limit}
	if len(tenantIDs) > 0 {
		ids := make([]string, 0, len(tenantIDs))
		for id := range tenantIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		query = `
			select seq, id::text, tenant_id::text, coalesce(object_id::text, ''), object_name,
			       coalesce(record_id::text, ''), action, actor_id, schema_version, changed_fields,
			       before, after, diff, created_at
			from ` + qualifiedIdent(MetadataSchema, "outbox_events") + `
			where seq > $1 and tenant_id::text = any($3)
			order by seq asc
			limit $2
		`
		args = append(args, ids)
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []*Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type eventScanner interface {
	Scan(...any) error
}

func scanEvent(row eventScanner) (*Event, error) {
	var event Event
	var beforeData []byte
	var afterData []byte
	var diffData []byte
	if err := row.Scan(
		&event.Seq, &event.EventID, &event.TenantID, &event.ObjectID, &event.Object,
		&event.RecordID, &event.Action, &event.ActorID, &event.SchemaVersion,
		&event.ChangedFields, &beforeData, &afterData, &diffData, &event.CreatedAt,
	); err != nil {
		return nil, err
	}
	event.Before = decodeRecordJSON(beforeData)
	event.After = decodeRecordJSON(afterData)
	event.Diff = decodeRecordJSON(diffData)
	return &event, nil
}

func decodeRecordJSON(data []byte) Record {
	if len(data) == 0 || strings.EqualFold(string(data), "null") {
		return nil
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{"_decode_error": err.Error()}
	}
	return record
}
