package devdash

import (
	"context"
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

type ProcessEvent struct {
	ID          int64           `json:"id"`
	AppID       string          `json:"app_id"`
	Kind        string          `json:"kind"`
	PayloadJSON json.RawMessage `json:"payload_json"`
	CreatedAt   time.Time       `json:"created_at"`
}

const sqliteBusyTimeoutMS = 5_000
const SQLiteBusyTimeoutMS = sqliteBusyTimeoutMS

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

func StoreSQLiteDSN(dbPath string) string {
	return storeSQLiteDSN(dbPath)
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
			base_app_id text not null default '',
			runtime_app_id text not null default '',
			session_id text not null default '',
			name text not null,
			root text not null,
			listen_addr text not null default '',
			metadata_json text not null default '{}',
			api_encoding_json text not null default '{}',
			grafana_json text not null default '{}',
			running integer not null default 0,
			compiling integer not null default 0,
			compile_error text not null default '',
			pid text not null default '',
			updated_at text not null
		)`,
		`create table if not exists app_sessions (
			record_key text primary key,
			app_id text not null,
			base_app_id text not null default '',
			runtime_app_id text not null default '',
			session_id text not null default '',
			name text not null,
			root text not null,
			listen_addr text not null default '',
			metadata_json text not null default '{}',
			api_encoding_json text not null default '{}',
			grafana_json text not null default '{}',
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
			session_id text not null default '',
			pid text not null,
			stream text not null,
			output blob not null,
			created_at text not null
		)`,
		`create table if not exists trace_summaries (
			id integer primary key autoincrement,
			app_id text not null,
			session_id text not null default '',
			trace_id text not null,
			span_id text not null,
			started_at text not null,
			service_name text not null default '',
			endpoint_name text,
			is_root integer not null default 0,
			is_error integer not null default 0,
			duration_nanos integer not null default 0,
			summary_json text not null,
			unique(app_id, session_id, trace_id, span_id)
		)`,
		`create table if not exists trace_events (
			id integer primary key autoincrement,
			app_id text not null,
			session_id text not null default '',
			trace_id text not null,
			span_id text not null,
			event_id integer not null,
			event_time text not null,
			event_json text not null
		)`,
		`create table if not exists log_events (
			id integer primary key autoincrement,
			app_id text not null,
			session_id text not null default '',
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
	if err := s.ensureColumn(ctx, "apps", "grafana_json", `text not null default '{}'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "apps", "base_app_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "apps", "runtime_app_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "apps", "session_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "app_sessions", "grafana_json", `text not null default '{}'`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "app_sessions", "base_app_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "app_sessions", "runtime_app_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "app_sessions", "session_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "process_output", "session_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "trace_summaries", "session_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.migrateTraceSummariesSessionUniqueness(ctx); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "trace_events", "session_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "log_events", "session_id", `text not null default ''`); err != nil {
		return err
	}
	if err := s.migrateAppSessions(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) migrateTraceSummariesSessionUniqueness(ctx context.Context) error {
	ok, err := s.traceSummariesUniqueIndexIncludesSession(ctx)
	if err != nil || ok {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, `
		create table trace_summaries_new (
			id integer primary key autoincrement,
			app_id text not null,
			session_id text not null default '',
			trace_id text not null,
			span_id text not null,
			started_at text not null,
			service_name text not null default '',
			endpoint_name text,
			is_root integer not null default 0,
			is_error integer not null default 0,
			duration_nanos integer not null default 0,
			summary_json text not null,
			unique(app_id, session_id, trace_id, span_id)
		)
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		insert into trace_summaries_new (
			id, app_id, session_id, trace_id, span_id, started_at, service_name,
			endpoint_name, is_root, is_error, duration_nanos, summary_json
		)
		select
			id, app_id, session_id, trace_id, span_id, started_at, service_name,
			endpoint_name, is_root, is_error, duration_nanos, summary_json
		from trace_summaries
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `drop table trace_summaries`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `alter table trace_summaries_new rename to trace_summaries`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) traceSummariesUniqueIndexIncludesSession(ctx context.Context) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `pragma index_list(trace_summaries)`)
	if err != nil {
		return false, err
	}
	var uniqueIndexes []string
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return false, err
		}
		if unique != 0 {
			uniqueIndexes = append(uniqueIndexes, name)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, name := range uniqueIndexes {
		cols, err := s.indexColumns(ctx, name)
		if err != nil {
			return false, err
		}
		if equalStrings(cols, []string{"app_id", "session_id", "trace_id", "span_id"}) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) indexColumns(ctx context.Context, indexName string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `pragma index_info(`+indexName+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var seqno int
		var cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, "pragma table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("alter table %s add column %s %s", table, column, definition))
	return err
}

func (s *Store) migrateAppSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		insert or ignore into app_sessions (
			record_key, app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr,
			metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at
		)
		select
			case when session_id != '' then session_id else app_id end,
			app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr,
			metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at
		from apps
	`)
	return err
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
	if len(app.Grafana) == 0 {
		app.Grafana = json.RawMessage(`{}`)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		insert into apps (app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr, metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(app_id) do update set
			base_app_id = excluded.base_app_id,
			runtime_app_id = excluded.runtime_app_id,
			session_id = excluded.session_id,
			name = excluded.name,
			root = excluded.root,
			listen_addr = excluded.listen_addr,
			metadata_json = excluded.metadata_json,
			api_encoding_json = excluded.api_encoding_json,
			grafana_json = excluded.grafana_json,
			running = excluded.running,
			compiling = excluded.compiling,
			compile_error = excluded.compile_error,
			pid = excluded.pid,
			updated_at = excluded.updated_at
	`,
		app.ID,
		app.BaseAppID,
		app.RuntimeAppID,
		app.SessionID,
		app.Name,
		app.Root,
		app.ListenAddr,
		string(app.Metadata),
		string(app.APIEncoding),
		string(app.Grafana),
		boolToInt(app.Running),
		boolToInt(app.Compiling),
		app.CompileError,
		app.PID,
		app.UpdatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		insert into app_sessions (record_key, app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr, metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(record_key) do update set
			app_id = excluded.app_id,
			base_app_id = excluded.base_app_id,
			runtime_app_id = excluded.runtime_app_id,
			session_id = excluded.session_id,
			name = excluded.name,
			root = excluded.root,
			listen_addr = excluded.listen_addr,
			metadata_json = excluded.metadata_json,
			api_encoding_json = excluded.api_encoding_json,
			grafana_json = excluded.grafana_json,
			running = excluded.running,
			compiling = excluded.compiling,
			compile_error = excluded.compile_error,
			pid = excluded.pid,
			updated_at = excluded.updated_at
	`,
		appSessionRecordKey(app),
		app.ID,
		app.BaseAppID,
		app.RuntimeAppID,
		app.SessionID,
		app.Name,
		app.Root,
		app.ListenAddr,
		string(app.Metadata),
		string(app.APIEncoding),
		string(app.Grafana),
		boolToInt(app.Running),
		boolToInt(app.Compiling),
		app.CompileError,
		app.PID,
		app.UpdatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListApps(ctx context.Context) ([]AppRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		select app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr, metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at
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
		var metadata, apiEncoding, grafana string
		var running, compiling int
		var updatedAt string
		if err := rows.Scan(
			&app.ID,
			&app.BaseAppID,
			&app.RuntimeAppID,
			&app.SessionID,
			&app.Name,
			&app.Root,
			&app.ListenAddr,
			&metadata,
			&apiEncoding,
			&grafana,
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
		app.Grafana = json.RawMessage(grafana)
		app.Running = running == 1
		app.Compiling = compiling == 1
		app.Offline = !app.Running
		app.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		app.RouteID = app.ID
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

type appRecordScanner interface {
	Scan(dest ...any) error
}

func scanAppSessionRecord(row appRecordScanner) (AppRecord, error) {
	var app AppRecord
	var metadata, apiEncoding, grafana string
	var running, compiling int
	var updatedAt string
	if err := row.Scan(
		&app.RouteID,
		&app.ID,
		&app.BaseAppID,
		&app.RuntimeAppID,
		&app.SessionID,
		&app.Name,
		&app.Root,
		&app.ListenAddr,
		&metadata,
		&apiEncoding,
		&grafana,
		&running,
		&compiling,
		&app.CompileError,
		&app.PID,
		&updatedAt,
	); err != nil {
		return AppRecord{}, err
	}
	app.Metadata = json.RawMessage(metadata)
	app.APIEncoding = json.RawMessage(apiEncoding)
	app.Grafana = json.RawMessage(grafana)
	app.Running = running == 1
	app.Compiling = compiling == 1
	app.Offline = !app.Running
	app.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return app, nil
}

func appSessionRecordKey(app AppRecord) string {
	if app.RouteID != "" {
		return app.RouteID
	}
	if app.SessionID != "" {
		return app.SessionID
	}
	return app.ID
}

func (s *Store) ListAppSessions(ctx context.Context) ([]AppRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		select record_key, app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr, metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at
		from app_sessions
		order by running desc, name asc, session_id asc, updated_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []AppRecord
	for rows.Next() {
		app, err := scanAppSessionRecord(rows)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

func (s *Store) GetApp(ctx context.Context, appID string) (AppRecord, error) {
	var app AppRecord
	var metadata, apiEncoding, grafana string
	var running, compiling int
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
		select app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr, metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at
		from apps where app_id = ?
	`, appID).Scan(
		&app.ID,
		&app.BaseAppID,
		&app.RuntimeAppID,
		&app.SessionID,
		&app.Name,
		&app.Root,
		&app.ListenAddr,
		&metadata,
		&apiEncoding,
		&grafana,
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
	app.Grafana = json.RawMessage(grafana)
	app.Running = running == 1
	app.Compiling = compiling == 1
	app.Offline = !app.Running
	app.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	app.RouteID = app.ID
	return app, nil
}

func (s *Store) GetAppSession(ctx context.Context, routeID string) (AppRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		select record_key, app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr, metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at
		from app_sessions
		where record_key = ? or session_id = ?
		order by running desc, updated_at desc
		limit 1
	`, routeID, routeID)
	if err != nil {
		return AppRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return AppRecord{}, sql.ErrNoRows
	}
	app, err := scanAppSessionRecord(rows)
	if err != nil {
		return AppRecord{}, err
	}
	if err := rows.Err(); err != nil {
		return AppRecord{}, err
	}
	return app, nil
}

func (s *Store) GetAppForSession(ctx context.Context, appID, sessionID string) (AppRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		select record_key, app_id, base_app_id, runtime_app_id, session_id, name, root, listen_addr, metadata_json, api_encoding_json, grafana_json, running, compiling, compile_error, pid, updated_at
		from app_sessions
		where app_id = ? and session_id = ?
		order by running desc, updated_at desc
		limit 1
	`, appID, sessionID)
	if err != nil {
		return AppRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return AppRecord{}, sql.ErrNoRows
	}
	app, err := scanAppSessionRecord(rows)
	if err != nil {
		return AppRecord{}, err
	}
	if err := rows.Err(); err != nil {
		return AppRecord{}, err
	}
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

func (s *Store) ListProcessEvents(ctx context.Context, appID string, limit int) ([]ProcessEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		select id, app_id, kind, payload_json, created_at
		from process_events
		where app_id = ?
		order by id desc
		limit ?
	`, appID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []ProcessEvent
	for rows.Next() {
		var event ProcessEvent
		var payload string
		var created string
		if err := rows.Scan(&event.ID, &event.AppID, &event.Kind, &payload, &created); err != nil {
			return nil, err
		}
		event.PayloadJSON = append(json.RawMessage(nil), payload...)
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			event.CreatedAt = t
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) WriteProcessOutput(ctx context.Context, output ProcessOutput) error {
	if output.CreatedAt.IsZero() {
		output.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		insert into process_output (app_id, session_id, pid, stream, output, created_at)
		values (?, ?, ?, ?, ?, ?)
	`, output.AppID, output.SessionID, output.PID, output.Stream, output.Output, output.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListProcessOutput(ctx context.Context, appID string, limit int) ([]ProcessOutput, error) {
	return s.ListProcessOutputForSession(ctx, appID, "", limit)
}

func (s *Store) ListProcessOutputForSession(ctx context.Context, appID, sessionID string, limit int) ([]ProcessOutput, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `
		select id, app_id, session_id, pid, stream, output, created_at
		from process_output
		where app_id = ?
	`
	args := []any{appID}
	if sessionID != "" {
		query += ` and session_id = ?`
		args = append(args, sessionID)
	}
	query += ` order by id desc limit ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ProcessOutput
	for rows.Next() {
		var item ProcessOutput
		var createdAt string
		if err := rows.Scan(&item.ID, &item.AppID, &item.SessionID, &item.PID, &item.Stream, &item.Output, &createdAt); err != nil {
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
	return s.ListProcessOutputSinceForSession(ctx, appID, "", afterID, limit)
}

func (s *Store) ListProcessOutputSinceForSession(ctx context.Context, appID, sessionID string, afterID int64, limit int) ([]ProcessOutput, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `
		select id, app_id, session_id, pid, stream, output, created_at
		from process_output
		where app_id = ? and id > ?
	`
	args := []any{appID, afterID}
	if sessionID != "" {
		query += ` and session_id = ?`
		args = append(args, sessionID)
	}
	query += ` order by id asc limit ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ProcessOutput
	for rows.Next() {
		var item ProcessOutput
		var createdAt string
		if err := rows.Scan(&item.ID, &item.AppID, &item.SessionID, &item.PID, &item.Stream, &item.Output, &createdAt); err != nil {
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
		insert into trace_summaries (app_id, session_id, trace_id, span_id, started_at, service_name, endpoint_name, is_root, is_error, duration_nanos, summary_json)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(app_id, session_id, trace_id, span_id) do update set
			started_at = excluded.started_at,
			service_name = excluded.service_name,
			endpoint_name = excluded.endpoint_name,
			is_root = excluded.is_root,
			is_error = excluded.is_error,
			duration_nanos = excluded.duration_nanos,
			summary_json = excluded.summary_json
	`,
		summary.AppID,
		summary.SessionID,
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
	return s.ListTraceSummariesForSession(ctx, appID, "", limit, messageID)
}

func (s *Store) ListTraceSummariesForSession(ctx context.Context, appID, sessionID string, limit int, messageID string) ([]*TraceSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `
		select summary_json
		from trace_summaries
		where app_id = ? and is_root = 1
	`
	args := []any{appID}
	if sessionID != "" {
		query += ` and session_id = ?`
		args = append(args, sessionID)
	}
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
		if summary.SessionID == "" {
			summary.SessionID = sessionID
		}
		list = append(list, &summary)
	}
	return list, rows.Err()
}

func (s *Store) GetTraceSummaries(ctx context.Context, appID, traceID string) ([]*TraceSummary, error) {
	return s.GetTraceSummariesForSession(ctx, appID, "", traceID)
}

func (s *Store) GetTraceSummariesForSession(ctx context.Context, appID, sessionID, traceID string) ([]*TraceSummary, error) {
	query := `
		select summary_json
		from trace_summaries
		where app_id = ? and trace_id = ?
	`
	args := []any{appID, traceID}
	if sessionID != "" {
		query += ` and session_id = ?`
		args = append(args, sessionID)
	}
	query += ` order by is_root desc, started_at asc`
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
		if summary.SessionID == "" {
			summary.SessionID = sessionID
		}
		list = append(list, &summary)
	}
	return list, rows.Err()
}
