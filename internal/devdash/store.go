package devdash

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const sqliteBusyTimeoutMS = 5_000

func OpenStore(cacheRoot string) (*Store, error) {
	if cacheRoot == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cacheRoot = filepath.Join(dir, "onlava")
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(cacheRoot, "dev.db")
	db, err := sql.Open("sqlite", storeSQLiteDSN(dbPath))
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

func storeSQLiteDSN(dbPath string) string {
	return fmt.Sprintf(
		"file:%s?_pragma=busy_timeout%%3d%d&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		filepath.ToSlash(dbPath),
		sqliteBusyTimeoutMS,
	)
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
		`create table if not exists pubsub_snapshots (
			app_id text primary key,
			snapshot_json text not null default '{}',
			updated_at text not null
		)`,
		`create table if not exists pubsub_snapshot_history (
			id integer primary key autoincrement,
			app_id text not null,
			snapshot_json text not null default '{}',
			created_at text not null
		)`,
		`create table if not exists pubsub_messages (
			id integer primary key autoincrement,
			app_id text not null,
			message_id text not null,
			topic_name text not null,
			subscription_name text not null,
			service_name text not null default '',
			status text not null default 'queued',
			trace_id text not null default '',
			attempt integer not null default 0,
			payload_json text not null default 'null',
			result_json text not null default 'null',
			error_text text not null default '',
			deliveries integer not null default 0,
			inserted_at text not null,
			picked_up_at text not null default '',
			finished_at text not null default '',
			duration_ms real not null default 0,
			updated_at text not null,
			unique(app_id, message_id, subscription_name)
		)`,
		`create table if not exists pubsub_message_attempts (
			id integer primary key autoincrement,
			app_id text not null,
			message_id text not null,
			topic_name text not null,
			subscription_name text not null,
			service_name text not null default '',
			status text not null default 'processing',
			trace_id text not null default '',
			attempt integer not null default 1,
			payload_json text not null default 'null',
			result_json text not null default 'null',
			error_text text not null default '',
			deliveries integer not null default 0,
			inserted_at text not null,
			picked_up_at text not null default '',
			finished_at text not null default '',
			duration_ms real not null default 0,
			updated_at text not null,
			unique(app_id, message_id, subscription_name, attempt)
		)`,
		`create index if not exists idx_pubsub_snapshot_history_app_created
			on pubsub_snapshot_history (app_id, created_at)`,
		`create index if not exists idx_pubsub_messages_app_inserted
			on pubsub_messages (app_id, inserted_at desc)`,
		`create index if not exists idx_pubsub_messages_app_subscription_inserted
			on pubsub_messages (app_id, subscription_name, inserted_at desc)`,
		`create index if not exists idx_pubsub_messages_app_topic_inserted
			on pubsub_messages (app_id, topic_name, inserted_at desc)`,
		`create index if not exists idx_pubsub_message_attempts_lookup
			on pubsub_message_attempts (app_id, message_id, subscription_name, attempt desc)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "pubsub_messages", "trace_id", "text not null default ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "pubsub_messages", "attempt", "integer not null default 0"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, tableName, columnName, definition string) error {
	rows, err := s.db.QueryContext(ctx, "pragma table_info("+tableName+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notnull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &defaultVal, &pk); err != nil {
			return err
		}
		if name == columnName {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "alter table "+tableName+" add column "+columnName+" "+definition)
	return err
}

func (s *Store) UpsertPubSubSnapshot(ctx context.Context, snapshot PubSubSnapshot) error {
	if snapshot.AppID == "" {
		return errors.New("pubsub snapshot app id is required")
	}
	if len(snapshot.Topics) == 0 {
		snapshot.Topics = json.RawMessage(`[]`)
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = time.Now().UTC()
	}
	snapshot.UpdatedAt = snapshot.UpdatedAt.UTC()
	_, err := s.db.ExecContext(ctx, `
		insert into pubsub_snapshots (app_id, snapshot_json, updated_at)
		values (?, ?, ?)
		on conflict(app_id) do update set
			snapshot_json = excluded.snapshot_json,
			updated_at = excluded.updated_at
	`, snapshot.AppID, string(snapshot.Topics), snapshot.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		insert into pubsub_snapshot_history (app_id, snapshot_json, created_at)
		values (?, ?, ?)
	`, snapshot.AppID, string(snapshot.Topics), snapshot.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `
		delete from pubsub_snapshot_history
		where app_id = ? and created_at < ?
	`, snapshot.AppID, snapshot.UpdatedAt.Add(-48*time.Hour).Format(time.RFC3339Nano))
	return nil
}

func (s *Store) GetPubSubSnapshot(ctx context.Context, appID string) (PubSubSnapshot, error) {
	var data string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		select snapshot_json, updated_at
		from pubsub_snapshots
		where app_id = ?
	`, appID).Scan(&data, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PubSubSnapshot{
			AppID:     appID,
			Topics:    json.RawMessage(`[]`),
			UpdatedAt: time.Time{},
		}, nil
	}
	if err != nil {
		return PubSubSnapshot{}, err
	}
	ts, _ := time.Parse(time.RFC3339Nano, updatedAt)
	return PubSubSnapshot{
		AppID:     appID,
		Topics:    json.RawMessage(data),
		UpdatedAt: ts,
	}, nil
}

func (s *Store) ListPubSubSnapshots(ctx context.Context, appID string, since time.Time) ([]PubSubSnapshot, error) {
	if appID == "" {
		return nil, errors.New("pubsub snapshot app id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		select snapshot_json, created_at
		from pubsub_snapshot_history
		where app_id = ? and created_at >= ?
		order by created_at asc
	`, appID, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []PubSubSnapshot
	for rows.Next() {
		var data string
		var createdAt string
		if err := rows.Scan(&data, &createdAt); err != nil {
			return nil, err
		}
		ts, _ := time.Parse(time.RFC3339Nano, createdAt)
		snapshots = append(snapshots, PubSubSnapshot{
			AppID:     appID,
			Topics:    json.RawMessage(data),
			UpdatedAt: ts,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (s *Store) UpsertPubSubMessage(ctx context.Context, message PubSubMessage) error {
	if strings.TrimSpace(message.AppID) == "" {
		return errors.New("pubsub message app id is required")
	}
	if strings.TrimSpace(message.MessageID) == "" {
		return errors.New("pubsub message id is required")
	}
	if strings.TrimSpace(message.TopicName) == "" {
		return errors.New("pubsub message topic is required")
	}
	if strings.TrimSpace(message.SubscriptionName) == "" {
		return errors.New("pubsub message subscription is required")
	}
	if len(message.Payload) == 0 {
		message.Payload = json.RawMessage("null")
	}
	if len(message.Result) == 0 {
		message.Result = json.RawMessage("null")
	}
	if message.InsertedAt.IsZero() {
		message.InsertedAt = time.Now().UTC()
	}
	message.InsertedAt = message.InsertedAt.UTC()
	if !message.PickedUpAt.IsZero() {
		message.PickedUpAt = message.PickedUpAt.UTC()
	}
	if !message.FinishedAt.IsZero() {
		message.FinishedAt = message.FinishedAt.UTC()
	}
	updatedAt := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		insert into pubsub_messages (
			app_id, message_id, topic_name, subscription_name, service_name, status, trace_id, attempt,
			payload_json, result_json, error_text, deliveries, inserted_at, picked_up_at,
			finished_at, duration_ms, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(app_id, message_id, subscription_name) do update set
			topic_name = excluded.topic_name,
			service_name = excluded.service_name,
			status = excluded.status,
			trace_id = excluded.trace_id,
			attempt = excluded.attempt,
			payload_json = excluded.payload_json,
			result_json = excluded.result_json,
			error_text = excluded.error_text,
			deliveries = excluded.deliveries,
			inserted_at = excluded.inserted_at,
			picked_up_at = excluded.picked_up_at,
			finished_at = excluded.finished_at,
			duration_ms = excluded.duration_ms,
			updated_at = excluded.updated_at
	`,
		message.AppID,
		message.MessageID,
		message.TopicName,
		message.SubscriptionName,
		message.ServiceName,
		message.Status,
		message.TraceID,
		message.Attempt,
		string(message.Payload),
		string(message.Result),
		message.Error,
		message.Deliveries,
		message.InsertedAt.Format(time.RFC3339Nano),
		formatOptionalTime(message.PickedUpAt),
		formatOptionalTime(message.FinishedAt),
		message.DurationMS,
		updatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) UpsertPubSubMessageAttempt(ctx context.Context, attempt PubSubMessageAttempt) error {
	if strings.TrimSpace(attempt.AppID) == "" {
		return errors.New("pubsub message attempt app id is required")
	}
	if strings.TrimSpace(attempt.MessageID) == "" {
		return errors.New("pubsub message attempt id is required")
	}
	if strings.TrimSpace(attempt.SubscriptionName) == "" {
		return errors.New("pubsub message attempt subscription is required")
	}
	if attempt.Attempt <= 0 {
		attempt.Attempt = max(1, attempt.Deliveries)
	}
	if len(attempt.Payload) == 0 {
		attempt.Payload = json.RawMessage("null")
	}
	if len(attempt.Result) == 0 {
		attempt.Result = json.RawMessage("null")
	}
	if attempt.InsertedAt.IsZero() {
		attempt.InsertedAt = time.Now().UTC()
	}
	attempt.InsertedAt = attempt.InsertedAt.UTC()
	if !attempt.PickedUpAt.IsZero() {
		attempt.PickedUpAt = attempt.PickedUpAt.UTC()
	}
	if !attempt.FinishedAt.IsZero() {
		attempt.FinishedAt = attempt.FinishedAt.UTC()
	}
	updatedAt := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		insert into pubsub_message_attempts (
			app_id, message_id, topic_name, subscription_name, service_name, status, trace_id, attempt,
			payload_json, result_json, error_text, deliveries, inserted_at, picked_up_at,
			finished_at, duration_ms, updated_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(app_id, message_id, subscription_name, attempt) do update set
			topic_name = excluded.topic_name,
			service_name = excluded.service_name,
			status = excluded.status,
			trace_id = excluded.trace_id,
			payload_json = excluded.payload_json,
			result_json = excluded.result_json,
			error_text = excluded.error_text,
			deliveries = excluded.deliveries,
			inserted_at = excluded.inserted_at,
			picked_up_at = excluded.picked_up_at,
			finished_at = excluded.finished_at,
			duration_ms = excluded.duration_ms,
			updated_at = excluded.updated_at
	`,
		attempt.AppID,
		attempt.MessageID,
		attempt.TopicName,
		attempt.SubscriptionName,
		attempt.ServiceName,
		attempt.Status,
		attempt.TraceID,
		attempt.Attempt,
		string(attempt.Payload),
		string(attempt.Result),
		attempt.Error,
		attempt.Deliveries,
		attempt.InsertedAt.Format(time.RFC3339Nano),
		formatOptionalTime(attempt.PickedUpAt),
		formatOptionalTime(attempt.FinishedAt),
		attempt.DurationMS,
		updatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) MarkPubSubMessagesCleared(ctx context.Context, appID string, now time.Time) error {
	if strings.TrimSpace(appID) == "" {
		return errors.New("pubsub message app id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	resultJSON := `{"status":"cleared"}`
	_, err := s.db.ExecContext(ctx, `
		update pubsub_messages
		set status = 'cleared',
			result_json = ?,
			error_text = '',
			finished_at = ?,
			updated_at = ?
		where app_id = ?
		  and status in ('queued', 'processing', 'retrying')
	`, resultJSON, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), appID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		update pubsub_message_attempts
		set status = 'cleared',
			result_json = ?,
			error_text = '',
			finished_at = ?,
			updated_at = ?
		where app_id = ?
		  and status in ('processing', 'retrying')
	`, resultJSON, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), appID)
	return err
}

func (s *Store) ListPubSubMessages(ctx context.Context, appID string, since time.Time, topicName, subscriptionName, status string, limit int) ([]PubSubMessage, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, errors.New("pubsub message app id is required")
	}
	if limit <= 0 {
		limit = 500
	}
	query := `
		select app_id, message_id, topic_name, subscription_name, service_name, status, trace_id, attempt,
			payload_json, result_json, error_text, deliveries, inserted_at, picked_up_at,
			finished_at, duration_ms
		from pubsub_messages
		where app_id = ? and inserted_at >= ?
	`
	args := []any{appID, since.UTC().Format(time.RFC3339Nano)}
	if strings.TrimSpace(topicName) != "" {
		query += ` and topic_name = ?`
		args = append(args, topicName)
	}
	if strings.TrimSpace(subscriptionName) != "" {
		query += ` and subscription_name = ?`
		args = append(args, subscriptionName)
	}
	if strings.TrimSpace(status) != "" && status != "all" {
		query += ` and status = ?`
		args = append(args, status)
	}
	query += ` order by inserted_at desc, topic_name asc, subscription_name asc limit ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PubSubMessage
	for rows.Next() {
		var (
			item       PubSubMessage
			payload    string
			result     string
			insertedAt string
			pickedUpAt string
			finishedAt string
		)
		if err := rows.Scan(
			&item.AppID,
			&item.MessageID,
			&item.TopicName,
			&item.SubscriptionName,
			&item.ServiceName,
			&item.Status,
			&item.TraceID,
			&item.Attempt,
			&payload,
			&result,
			&item.Error,
			&item.Deliveries,
			&insertedAt,
			&pickedUpAt,
			&finishedAt,
			&item.DurationMS,
		); err != nil {
			return nil, err
		}
		item.Payload = json.RawMessage(payload)
		item.Result = json.RawMessage(result)
		item.InsertedAt, _ = time.Parse(time.RFC3339Nano, insertedAt)
		item.PickedUpAt, _ = parseOptionalTime(pickedUpAt)
		item.FinishedAt, _ = parseOptionalTime(finishedAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) ListPubSubMessageAttempts(ctx context.Context, appID, messageID, subscriptionName string) ([]PubSubMessageAttempt, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, errors.New("pubsub message attempt app id is required")
	}
	if strings.TrimSpace(messageID) == "" {
		return nil, errors.New("pubsub message id is required")
	}
	if strings.TrimSpace(subscriptionName) == "" {
		return nil, errors.New("pubsub message subscription is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		select app_id, message_id, topic_name, subscription_name, service_name, status, trace_id, attempt,
			payload_json, result_json, error_text, deliveries, inserted_at, picked_up_at, finished_at, duration_ms
		from pubsub_message_attempts
		where app_id = ? and message_id = ? and subscription_name = ?
		order by attempt desc, picked_up_at desc
	`, appID, messageID, subscriptionName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PubSubMessageAttempt
	for rows.Next() {
		var (
			item       PubSubMessageAttempt
			payload    string
			result     string
			insertedAt string
			pickedUpAt string
			finishedAt string
		)
		if err := rows.Scan(
			&item.AppID,
			&item.MessageID,
			&item.TopicName,
			&item.SubscriptionName,
			&item.ServiceName,
			&item.Status,
			&item.TraceID,
			&item.Attempt,
			&payload,
			&result,
			&item.Error,
			&item.Deliveries,
			&insertedAt,
			&pickedUpAt,
			&finishedAt,
			&item.DurationMS,
		); err != nil {
			return nil, err
		}
		item.Payload = json.RawMessage(payload)
		item.Result = json.RawMessage(result)
		item.InsertedAt, _ = time.Parse(time.RFC3339Nano, insertedAt)
		item.PickedUpAt, _ = parseOptionalTime(pickedUpAt)
		item.FinishedAt, _ = parseOptionalTime(finishedAt)
		items = append(items, item)
	}
	return items, rows.Err()
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

func (s *Store) ListProcessOutput(ctx context.Context, appID string, limit int) ([]ProcessOutput, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, app_id, pid, stream, output, created_at
		from process_output
		where app_id = ?
		order by id desc
		limit ?
	`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ProcessOutput
	for rows.Next() {
		var item ProcessOutput
		var createdAt string
		if err := rows.Scan(&item.ID, &item.AppID, &item.PID, &item.Stream, &item.Output, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

func (s *Store) ListProcessOutputSince(ctx context.Context, appID string, afterID int64, limit int) ([]ProcessOutput, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, app_id, pid, stream, output, created_at
		from process_output
		where app_id = ? and id > ?
		order by id asc
		limit ?
	`, appID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ProcessOutput
	for rows.Next() {
		var item ProcessOutput
		var createdAt string
		if err := rows.Scan(&item.ID, &item.AppID, &item.PID, &item.Stream, &item.Output, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		items = append(items, item)
	}
	return items, rows.Err()
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
