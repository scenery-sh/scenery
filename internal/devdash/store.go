package devdash

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func OpenStore(cacheRoot string) (*Store, error) {
	if cacheRoot == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cacheRoot = filepath.Join(dir, "pulse")
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(cacheRoot, "dev.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`create table if not exists apps (
			app_id text primary key,
			name text not null,
			root text not null,
			listen_addr text not null default '',
			metadata_json text not null default '{}',
			api_encoding_json text not null default '{}',
			running integer not null default 0,
			compiling integer not null default 0,
			compile_error text not null default '',
			pid text not null default '',
			updated_at text not null
		)`,
		`create table if not exists process_events (
			id integer primary key autoincrement,
			app_id text not null,
			kind text not null,
			payload_json text not null,
			created_at text not null
		)`,
		`create table if not exists process_output (
			id integer primary key autoincrement,
			app_id text not null,
			pid text not null,
			stream text not null,
			output blob not null,
			created_at text not null
		)`,
		`create table if not exists trace_summaries (
			id integer primary key autoincrement,
			app_id text not null,
			trace_id text not null,
			span_id text not null,
			started_at text not null,
			service_name text not null default '',
			endpoint_name text,
			is_root integer not null default 0,
			is_error integer not null default 0,
			duration_nanos integer not null default 0,
			summary_json text not null,
			unique(app_id, trace_id, span_id)
		)`,
		`create table if not exists trace_events (
			id integer primary key autoincrement,
			app_id text not null,
			trace_id text not null,
			span_id text not null,
			event_id integer not null,
			event_time text not null,
			event_json text not null
		)`,
		`create table if not exists log_events (
			id integer primary key autoincrement,
			app_id text not null,
			trace_id text not null default '',
			span_id text not null default '',
			level text not null,
			message text not null,
			attrs_json text not null default '{}',
			created_at text not null
		)`,
		`create table if not exists onboarding (
			name text primary key,
			set_at text not null
		)`,
		`create table if not exists stored_requests (
			app_id text not null,
			id text not null,
			title text not null default '',
			rpc_name text not null default '',
			svc_name text not null default '',
			shared integer not null default 0,
			data_json text not null default '{}',
			created_at text not null,
			updated_at text not null,
			primary key (app_id, id)
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertApp(ctx context.Context, app AppRecord) error {
	if app.UpdatedAt.IsZero() {
		app.UpdatedAt = time.Now().UTC()
	}
	if len(app.Metadata) == 0 {
		app.Metadata = json.RawMessage(`{}`)
	}
	if len(app.APIEncoding) == 0 {
		app.APIEncoding = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `
		insert into apps (app_id, name, root, listen_addr, metadata_json, api_encoding_json, running, compiling, compile_error, pid, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(app_id) do update set
			name = excluded.name,
			root = excluded.root,
			listen_addr = excluded.listen_addr,
			metadata_json = excluded.metadata_json,
			api_encoding_json = excluded.api_encoding_json,
			running = excluded.running,
			compiling = excluded.compiling,
			compile_error = excluded.compile_error,
			pid = excluded.pid,
			updated_at = excluded.updated_at
	`,
		app.ID,
		app.Name,
		app.Root,
		app.ListenAddr,
		string(app.Metadata),
		string(app.APIEncoding),
		boolToInt(app.Running),
		boolToInt(app.Compiling),
		app.CompileError,
		app.PID,
		app.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListApps(ctx context.Context) ([]AppRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		select app_id, name, root, listen_addr, metadata_json, api_encoding_json, running, compiling, compile_error, pid, updated_at
		from apps
		order by running desc, name asc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []AppRecord
	for rows.Next() {
		var app AppRecord
		var metadata, apiEncoding string
		var running, compiling int
		var updatedAt string
		if err := rows.Scan(
			&app.ID,
			&app.Name,
			&app.Root,
			&app.ListenAddr,
			&metadata,
			&apiEncoding,
			&running,
			&compiling,
			&app.CompileError,
			&app.PID,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		app.Metadata = json.RawMessage(metadata)
		app.APIEncoding = json.RawMessage(apiEncoding)
		app.Running = running == 1
		app.Compiling = compiling == 1
		app.Offline = !app.Running
		app.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

func (s *Store) GetApp(ctx context.Context, appID string) (AppRecord, error) {
	var app AppRecord
	var metadata, apiEncoding string
	var running, compiling int
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		select app_id, name, root, listen_addr, metadata_json, api_encoding_json, running, compiling, compile_error, pid, updated_at
		from apps where app_id = ?
	`, appID).Scan(
		&app.ID,
		&app.Name,
		&app.Root,
		&app.ListenAddr,
		&metadata,
		&apiEncoding,
		&running,
		&compiling,
		&app.CompileError,
		&app.PID,
		&updatedAt,
	)
	if err != nil {
		return AppRecord{}, err
	}
	app.Metadata = json.RawMessage(metadata)
	app.APIEncoding = json.RawMessage(apiEncoding)
	app.Running = running == 1
	app.Compiling = compiling == 1
	app.Offline = !app.Running
	app.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return app, nil
}

func (s *Store) WriteProcessEvent(ctx context.Context, appID, kind string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into process_events (app_id, kind, payload_json, created_at)
		values (?, ?, ?, ?)
	`, appID, kind, string(data), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) WriteProcessOutput(ctx context.Context, output ProcessOutput) error {
	if output.CreatedAt.IsZero() {
		output.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		insert into process_output (app_id, pid, stream, output, created_at)
		values (?, ?, ?, ?, ?)
	`, output.AppID, output.PID, output.Stream, output.Output, output.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) AppendTraceSummary(ctx context.Context, summary *TraceSummary) error {
	if summary == nil {
		return errors.New("trace summary is nil")
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	endpointName := nullableString(summary.EndpointName)
	_, err = s.db.ExecContext(ctx, `
		insert into trace_summaries (app_id, trace_id, span_id, started_at, service_name, endpoint_name, is_root, is_error, duration_nanos, summary_json)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(app_id, trace_id, span_id) do update set
			started_at = excluded.started_at,
			service_name = excluded.service_name,
			endpoint_name = excluded.endpoint_name,
			is_root = excluded.is_root,
			is_error = excluded.is_error,
			duration_nanos = excluded.duration_nanos,
			summary_json = excluded.summary_json
	`,
		summary.AppID,
		summary.TraceID,
		summary.SpanID,
		summary.StartedAt.UTC().Format(time.RFC3339Nano),
		summary.ServiceName,
		endpointName,
		boolToInt(summary.IsRoot),
		boolToInt(summary.IsError),
		summary.DurationNanos,
		string(data),
	)
	return err
}

func (s *Store) ListTraceSummaries(ctx context.Context, appID string, limit int, messageID string) ([]*TraceSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		select summary_json
		from trace_summaries
		where app_id = ? and is_root = 1
	`
	args := []any{appID}
	if messageID != "" {
		query += ` and summary_json like ?`
		args = append(args, "%"+messageID+"%")
	}
	query += ` order by started_at desc limit ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*TraceSummary
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var summary TraceSummary
		if err := json.Unmarshal([]byte(data), &summary); err != nil {
			return nil, err
		}
		summary.AppID = appID
		list = append(list, &summary)
	}
	return list, rows.Err()
}

func (s *Store) GetTraceSummaries(ctx context.Context, appID, traceID string) ([]*TraceSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		select summary_json
		from trace_summaries
		where app_id = ? and trace_id = ?
		order by is_root desc, started_at asc
	`, appID, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*TraceSummary
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var summary TraceSummary
		if err := json.Unmarshal([]byte(data), &summary); err != nil {
			return nil, err
		}
		summary.AppID = appID
		list = append(list, &summary)
	}
	return list, rows.Err()
}

func (s *Store) AppendTraceEvent(ctx context.Context, event *TraceEvent) error {
	if event == nil {
		return errors.New("trace event is nil")
	}
	body, err := marshalTraceEvent(event)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into trace_events (app_id, trace_id, span_id, event_id, event_time, event_json)
		values (?, ?, ?, ?, ?, ?)
	`, event.AppID, event.TraceID, event.SpanID, event.EventID, event.EventTime.UTC().Format(time.RFC3339Nano), string(body))
	return err
}

func (s *Store) GetTraceEvents(ctx context.Context, appID, traceID, spanID string) ([]*TraceEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		select event_json
		from trace_events
		where app_id = ? and trace_id = ? and span_id = ?
		order by event_id asc
	`, appID, traceID, spanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*TraceEvent
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var event TraceEvent
		if err := unmarshalTraceEvent([]byte(data), &event); err != nil {
			return nil, err
		}
		event.AppID = appID
		list = append(list, &event)
	}
	return list, rows.Err()
}

func (s *Store) WriteLogEvent(ctx context.Context, event *LogEvent) error {
	if event == nil {
		return errors.New("log event is nil")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	attrs, err := json.Marshal(event.Attrs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		insert into log_events (app_id, trace_id, span_id, level, message, attrs_json, created_at)
		values (?, ?, ?, ?, ?, ?, ?)
	`, event.AppID, event.TraceID, event.SpanID, event.Level, event.Message, string(attrs), event.Timestamp.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ClearTraces(ctx context.Context, appID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []string{
		`delete from trace_events where app_id = ?`,
		`delete from trace_summaries where app_id = ?`,
		`delete from log_events where app_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, appID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetOnboarding(ctx context.Context) (OnboardingState, error) {
	rows, err := s.db.QueryContext(ctx, `select name, set_at from onboarding`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	state := make(OnboardingState)
	for rows.Next() {
		var name, setAt string
		if err := rows.Scan(&name, &setAt); err != nil {
			return nil, err
		}
		ts, err := time.Parse(time.RFC3339Nano, setAt)
		if err != nil {
			return nil, fmt.Errorf("parse onboarding time for %s: %w", name, err)
		}
		state[name] = ts
	}
	return state, rows.Err()
}

func (s *Store) SetOnboarding(ctx context.Context, props []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, prop := range props {
		if prop == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			insert into onboarding (name, set_at) values (?, ?)
			on conflict(name) do update set set_at = excluded.set_at
		`, prop, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListStoredRequests(ctx context.Context, appID string) ([]StoredRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		select id, title, rpc_name, svc_name, shared, data_json
		from stored_requests
		where app_id = ?
		order by updated_at desc, id asc
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []StoredRequest
	for rows.Next() {
		var req StoredRequest
		var shared int
		var data string
		if err := rows.Scan(&req.ID, &req.Title, &req.RPC, &req.Svc, &shared, &data); err != nil {
			return nil, err
		}
		req.AppID = appID
		req.Shared = shared == 1
		if err := json.Unmarshal([]byte(data), &req.Data); err != nil {
			return nil, err
		}
		list = append(list, sanitizeStoredRequest(req))
	}
	return list, rows.Err()
}

func (s *Store) CreateStoredRequest(ctx context.Context, req StoredRequest) (StoredRequest, error) {
	if req.AppID == "" {
		return StoredRequest{}, errors.New("stored request app id is required")
	}
	req = sanitizeStoredRequest(req)
	if req.ID == "" {
		id, err := newStoredRequestID()
		if err != nil {
			return StoredRequest{}, err
		}
		req.ID = id
	}
	data, err := json.Marshal(req.Data)
	if err != nil {
		return StoredRequest{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
		insert into stored_requests (app_id, id, title, rpc_name, svc_name, shared, data_json, created_at, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, req.AppID, req.ID, req.Title, req.RPC, req.Svc, boolToInt(req.Shared), string(data), now, now)
	if err != nil {
		return StoredRequest{}, err
	}
	return req, nil
}

func (s *Store) UpdateStoredRequest(ctx context.Context, req StoredRequest) (StoredRequest, error) {
	if req.AppID == "" {
		return StoredRequest{}, errors.New("stored request app id is required")
	}
	if req.ID == "" {
		return StoredRequest{}, errors.New("stored request id is required")
	}
	req = sanitizeStoredRequest(req)
	data, err := json.Marshal(req.Data)
	if err != nil {
		return StoredRequest{}, err
	}
	result, err := s.db.ExecContext(ctx, `
		update stored_requests
		set title = ?, rpc_name = ?, svc_name = ?, shared = ?, data_json = ?, updated_at = ?
		where app_id = ? and id = ?
	`, req.Title, req.RPC, req.Svc, boolToInt(req.Shared), string(data), time.Now().UTC().Format(time.RFC3339Nano), req.AppID, req.ID)
	if err != nil {
		return StoredRequest{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return StoredRequest{}, err
	}
	if rows == 0 {
		return StoredRequest{}, sql.ErrNoRows
	}
	return req, nil
}

func (s *Store) DeleteStoredRequest(ctx context.Context, appID, id string) error {
	if appID == "" {
		return errors.New("stored request app id is required")
	}
	if id == "" {
		return errors.New("stored request id is required")
	}
	result, err := s.db.ExecContext(ctx, `
		delete from stored_requests
		where app_id = ? and id = ?
	`, appID, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func marshalTraceEvent(event *TraceEvent) ([]byte, error) {
	payload := map[string]any{
		"trace_id":   event.TraceID,
		"span_id":    event.SpanID,
		"event_id":   event.EventID,
		"event_time": event.EventTime.UTC().Format(time.RFC3339Nano),
		"event":      event.Event,
	}
	return json.Marshal(payload)
}

func unmarshalTraceEvent(data []byte, dst *TraceEvent) error {
	var raw struct {
		TraceID   string         `json:"trace_id"`
		SpanID    string         `json:"span_id"`
		EventID   uint64         `json:"event_id"`
		EventTime string         `json:"event_time"`
		Event     map[string]any `json:"event"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	dst.TraceID = raw.TraceID
	dst.SpanID = raw.SpanID
	dst.EventID = raw.EventID
	dst.Event = raw.Event
	if raw.EventTime != "" {
		dst.EventTime, _ = time.Parse(time.RFC3339Nano, raw.EventTime)
	}
	return nil
}

func sanitizeStoredRequest(req StoredRequest) StoredRequest {
	req.Data.PathParams = normalizeStoredRequestJSON(req.Data.PathParams)
	req.Data.Payload = normalizeStoredRequestJSON(req.Data.Payload)
	return req
}

func normalizeStoredRequestJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return append(json.RawMessage(nil), value...)
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return append(json.RawMessage(nil), value...)
	}
	return normalized
}

func newStoredRequestID() (string, error) {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("sr_%x", data[:]), nil
}
