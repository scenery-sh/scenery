package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"

	"scenery.sh/internal/postgresdb"
)

type Options struct{}

type Store struct {
	Service     string
	DatabaseURL string

	db    *sql.DB
	owned bool

	notifications *notificationHub
	watchMu       sync.Mutex
	watch         *watcher
	closed        bool
}

func Open(ctx context.Context, service, databaseURL string, _ Options) (*Store, error) {
	service, err := NormalizeServiceName(service)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("durable store: DATABASE_URL is required")
	}
	db, err := postgresdb.Open(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("durable store: open Postgres DATABASE_URL: %w", err)
	}
	s := &Store{Service: service, DatabaseURL: databaseURL, db: db, owned: true}
	if err := s.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	s.notifications = newNotificationHub(func(ctx context.Context, channels []string, dispatch func(string, string)) error {
		return listenNotifications(ctx, databaseURL, channels, dispatch)
	})
	return s, nil
}

func (s *Store) ForService(service string) (*Store, error) {
	service, err := NormalizeServiceName(service)
	if err != nil {
		return nil, err
	}
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("durable store: base store is not open")
	}
	return &Store{Service: service, DatabaseURL: s.DatabaseURL, db: s.db, notifications: s.notifications}, nil
}

func NormalizeServiceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("durable store: service name is required")
	}
	if strings.ContainsAny(name, `\:`) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("durable store: service name %q contains an unsafe character", name)
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("durable store: service name %q contains an unsafe path segment", name)
		}
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
				continue
			}
			return "", fmt.Errorf("durable store: service name %q contains unsupported character %q", name, r)
		}
	}
	return strings.Join(parts, "-"), nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.stopWatch()
	if !s.owned {
		return nil
	}
	s.notifications.close()
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) init(ctx context.Context) error {
	return postgresdb.Migrate(ctx, s.db, "scenery.durable.store", func(ctx context.Context, tx *sql.Tx) error {
		// Once this version is installed, startup must not acquire DDL locks on
		// tables that another service is already using for task reconciliation.
		var ledgerExists bool
		if err := tx.QueryRowContext(ctx, `SELECT to_regclass('scenery.durable_schema_migrations') IS NOT NULL`).Scan(&ledgerExists); err != nil {
			return fmt.Errorf("durable store: inspect schema ledger: %w", err)
		}
		if ledgerExists {
			var installed bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
SELECT 1 FROM scenery.durable_schema_migrations WHERE version = $1 AND checksum = $2
)`, schemaVersion, "durable-postgres-v2").Scan(&installed); err != nil {
				return fmt.Errorf("durable store: inspect schema version: %w", err)
			}
			if installed {
				return nil
			}
		}
		if _, err := tx.ExecContext(ctx, initSchemaSQL); err != nil {
			return fmt.Errorf("durable store: apply schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_schema_migrations (version, name, checksum)
VALUES ($1, $2, $3)
ON CONFLICT(version) DO NOTHING
`, schemaVersion, "retention", "durable-postgres-v2"); err != nil {
			return fmt.Errorf("durable store: record schema migration: %w", err)
		}
		return nil
	})
}

func rollback(tx *sql.Tx, err error) error {
	if rbErr := tx.Rollback(); rbErr != nil {
		return errors.Join(err, rbErr)
	}
	return err
}

type StartRequest struct {
	ID               string
	TaskName         string
	TaskVersion      int
	DedupeKey        string
	ConcurrencyKey   string
	Priority         int
	InputCodec       string
	InputBlob        []byte
	RequirementsJSON string
	LabelsJSON       string
	MemoJSON         string
	CreatedBy        string
}

type Job struct {
	ID        string
	TaskName  string
	State     string
	DedupeKey string
}

type JobDetail struct {
	ID          string
	TaskName    string
	State       string
	DedupeKey   string
	Attempt     int
	MaxAttempts int
	CreatedAt   string
	UpdatedAt   string
	CompletedAt string
	ResultCodec string
	ResultBlob  []byte
	ErrorCodec  string
	ErrorBlob   []byte
}

type JobEvent struct {
	Seq          int64
	JobID        string
	Attempt      int
	EventType    string
	PayloadCodec string
	PayloadBlob  []byte
	CreatedAt    string
}

type StepResult struct {
	State       string
	ResultCodec string
	ResultBlob  []byte
	ErrorCodec  string
	ErrorBlob   []byte
}

type LeasedJob struct {
	ID         string
	TaskName   string
	Attempt    int
	LeaseID    string
	LeaseMS    int
	TimeoutMS  int
	InputCodec string
	InputBlob  []byte
	MemoJSON   string
	// WakeAfter is local acquisition guidance for an empty lease, never wire data.
	WakeAfter time.Duration `json:"-"`
}

func (s *Store) Start(ctx context.Context, req StartRequest) (Job, error) {
	req = normalizeStart(req)
	if req.ID == "" {
		return Job{}, fmt.Errorf("durable store: job id is required")
	}
	if req.TaskName == "" {
		return Job{}, fmt.Errorf("durable store: task name is required")
	}
	if len(req.InputBlob) == 0 {
		return Job{}, fmt.Errorf("durable store: input blob is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, fmt.Errorf("durable store: begin start job: %w", err)
	}
	if req.DedupeKey != "" {
		if _, err := tx.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET dedupe_key = NULL, dedupe_expires_at = NULL, updated_at = now()
WHERE service = $1 AND task_name = $2 AND dedupe_key = $3
  AND dedupe_expires_at IS NOT NULL AND dedupe_expires_at <= now()
`, s.Service, req.TaskName, req.DedupeKey); err != nil {
			return Job{}, rollback(tx, fmt.Errorf("durable store: expire deduplication key: %w", err))
		}
		job, ok, err := s.jobByDedupeKey(ctx, tx, req.TaskName, req.DedupeKey)
		if err != nil {
			return Job{}, rollback(tx, err)
		}
		if ok {
			if err := tx.Commit(); err != nil {
				return Job{}, fmt.Errorf("durable store: commit existing dedupe job: %w", err)
			}
			return job, nil
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_jobs (
  service, id, task_name, task_version, state, dedupe_key, priority,
  dedupe_expires_at, concurrency_key,
  input_codec, input_blob, requirements_json, labels_json, memo_json,
  max_attempts, success_retention_ms, failure_retention_ms, created_by
)
VALUES ($1, $2, $3, $4, 'queued', NULLIF($5, ''), $6,
  CASE WHEN $5 = '' THEN NULL ELSE now() + (COALESCE((SELECT deduplication_retention_ms FROM scenery.durable_tasks WHERE service = $1 AND name = $3 AND version = $4), 604800000) * interval '1 millisecond') END,
  NULLIF($7, ''), $8, $9, $10::jsonb, $11::jsonb, $12::jsonb,
  COALESCE((SELECT max_attempts FROM scenery.durable_tasks WHERE service = $1 AND name = $3), 1),
  COALESCE((SELECT success_retention_ms FROM scenery.durable_tasks WHERE service = $1 AND name = $3), 604800000),
  COALESCE((SELECT failure_retention_ms FROM scenery.durable_tasks WHERE service = $1 AND name = $3), 2592000000), $13)
`, s.Service, req.ID, req.TaskName, req.TaskVersion, req.DedupeKey, req.Priority, req.ConcurrencyKey, req.InputCodec, req.InputBlob, req.RequirementsJSON, req.LabelsJSON, req.MemoJSON, req.CreatedBy); err != nil {
		return Job{}, rollback(tx, fmt.Errorf("durable store: insert job %q: %w", req.ID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, event_type, payload_codec, payload_blob)
VALUES ($1, $2, 'job.created', 'json', '{}'::bytea)
`, s.Service, req.ID); err != nil {
		return Job{}, rollback(tx, fmt.Errorf("durable store: append job.created event: %w", err))
	}
	if err := notifyTx(ctx, tx, s.Service, notification{Kind: "queued", Task: req.TaskName}); err != nil {
		return Job{}, rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, fmt.Errorf("durable store: commit start job: %w", err)
	}
	return Job{ID: req.ID, TaskName: req.TaskName, State: "queued", DedupeKey: req.DedupeKey}, nil
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]JobDetail, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_name, state, COALESCE(dedupe_key, ''), attempt, max_attempts,
       created_at::text, updated_at::text, COALESCE(completed_at::text, ''),
       COALESCE(result_codec, ''), result_blob, COALESCE(error_codec, ''), error_blob
FROM scenery.durable_jobs
WHERE service = $1
ORDER BY created_at DESC, id DESC
LIMIT $2
`, s.Service, limit)
	if err != nil {
		return nil, fmt.Errorf("durable store: list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var jobs []JobDetail
	for rows.Next() {
		var job JobDetail
		if err := rows.Scan(&job.ID, &job.TaskName, &job.State, &job.DedupeKey, &job.Attempt, &job.MaxAttempts, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt, &job.ResultCodec, &job.ResultBlob, &job.ErrorCodec, &job.ErrorBlob); err != nil {
			return nil, fmt.Errorf("durable store: scan job: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("durable store: list jobs rows: %w", err)
	}
	return jobs, nil
}

func (s *Store) GetJob(ctx context.Context, jobID string) (JobDetail, bool, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return JobDetail{}, false, fmt.Errorf("durable store: job id is required")
	}
	var job JobDetail
	err := s.db.QueryRowContext(ctx, `
SELECT id, task_name, state, COALESCE(dedupe_key, ''), attempt, max_attempts,
       created_at::text, updated_at::text, COALESCE(completed_at::text, ''),
       COALESCE(result_codec, ''), result_blob, COALESCE(error_codec, ''), error_blob
FROM scenery.durable_jobs
WHERE service = $1 AND id = $2
`, s.Service, jobID).Scan(&job.ID, &job.TaskName, &job.State, &job.DedupeKey, &job.Attempt, &job.MaxAttempts, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt, &job.ResultCodec, &job.ResultBlob, &job.ErrorCodec, &job.ErrorBlob)
	if errors.Is(err, sql.ErrNoRows) {
		return JobDetail{}, false, nil
	}
	if err != nil {
		return JobDetail{}, false, fmt.Errorf("durable store: get job %q: %w", jobID, err)
	}
	return job, true, nil
}

func (s *Store) PurgeExpiredJobs(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
DELETE FROM scenery.durable_jobs
WHERE service = $1 AND completed_at IS NOT NULL
  AND (
    (state = 'succeeded' AND success_retention_ms > 0 AND completed_at <= now() - (success_retention_ms * interval '1 millisecond'))
    OR
    (state IN ('failed', 'canceled') AND failure_retention_ms > 0 AND completed_at <= now() - (failure_retention_ms * interval '1 millisecond'))
  )
`, s.Service)
	if err != nil {
		return 0, fmt.Errorf("durable store: purge expired jobs: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("durable store: purge expired jobs rows affected: %w", err)
	}
	return count, nil
}

func (s *Store) JobEvents(ctx context.Context, jobID string) ([]JobEvent, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("durable store: job id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, job_id, COALESCE(attempt, 0), event_type, payload_codec, payload_blob, created_at::text
FROM scenery.durable_job_events
WHERE service = $1 AND job_id = $2
ORDER BY seq
`, s.Service, jobID)
	if err != nil {
		return nil, fmt.Errorf("durable store: list job %q events: %w", jobID, err)
	}
	defer func() { _ = rows.Close() }()
	var events []JobEvent
	for rows.Next() {
		var event JobEvent
		if err := rows.Scan(&event.Seq, &event.JobID, &event.Attempt, &event.EventType, &event.PayloadCodec, &event.PayloadBlob, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("durable store: scan job event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("durable store: list job events rows: %w", err)
	}
	return events, nil
}

func (s *Store) CancelJob(ctx context.Context, jobID string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("durable store: job id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin cancel job: %w", err)
	}
	var attempt int
	var taskName string
	if err := tx.QueryRowContext(ctx, `SELECT attempt, task_name FROM scenery.durable_jobs WHERE service = $1 AND id = $2 FOR UPDATE`, s.Service, jobID).Scan(&attempt, &taskName); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q: %w", jobID, err))
	}
	res, err := tx.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET state = 'canceled', lease_id = NULL, lease_owner = NULL, lease_token_hash = NULL,
    lease_until = NULL, completed_at = now(), updated_at = now()
WHERE service = $1 AND id = $2 AND state IN ('queued', 'running', 'failed')
`, s.Service, jobID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: cancel job %q: %w", jobID, err))
	}
	if changed, err := res.RowsAffected(); err != nil {
		return rollback(tx, fmt.Errorf("durable store: cancel job %q rows affected: %w", jobID, err))
	} else if changed != 1 {
		return rollback(tx, fmt.Errorf("durable store: job %q cannot be canceled", jobID))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, attempt, event_type, payload_codec, payload_blob)
VALUES ($1, $2, $3, 'job.canceled', 'json', '{}'::bytea)
`, s.Service, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.canceled event: %w", err))
	}
	if err := notifyTx(ctx, tx, s.Service, notification{Kind: "done", Task: taskName, Job: jobID}); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit cancel job: %w", err)
	}
	return nil
}

func (s *Store) RetryJob(ctx context.Context, jobID string) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("durable store: job id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin retry job: %w", err)
	}
	var attempt int
	var taskName string
	if err := tx.QueryRowContext(ctx, `SELECT attempt, task_name FROM scenery.durable_jobs WHERE service = $1 AND id = $2 FOR UPDATE`, s.Service, jobID).Scan(&attempt, &taskName); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q: %w", jobID, err))
	}
	res, err := tx.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET state = 'queued', run_after = now(),
    max_attempts = CASE WHEN max_attempts <= attempt THEN attempt + 1 ELSE max_attempts END,
    error_codec = NULL, error_blob = NULL, lease_id = NULL, lease_owner = NULL,
    lease_token_hash = NULL, lease_until = NULL, completed_at = NULL, updated_at = now()
WHERE service = $1 AND id = $2 AND state IN ('failed', 'canceled')
`, s.Service, jobID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: retry job %q: %w", jobID, err))
	}
	if changed, err := res.RowsAffected(); err != nil {
		return rollback(tx, fmt.Errorf("durable store: retry job %q rows affected: %w", jobID, err))
	} else if changed != 1 {
		return rollback(tx, fmt.Errorf("durable store: job %q cannot be retried", jobID))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, attempt, event_type, payload_codec, payload_blob)
VALUES ($1, $2, $3, 'job.retry_requested', 'json', '{}'::bytea)
`, s.Service, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.retry_requested event: %w", err))
	}
	if err := notifyTx(ctx, tx, s.Service, notification{Kind: "queued", Task: taskName}); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit retry job: %w", err)
	}
	return nil
}

func (s *Store) GetStep(ctx context.Context, jobID, key string) (StepResult, bool, error) {
	jobID = strings.TrimSpace(jobID)
	key = strings.TrimSpace(key)
	if jobID == "" || key == "" {
		return StepResult{}, false, fmt.Errorf("durable store: job id and step key are required")
	}
	var step StepResult
	err := s.db.QueryRowContext(ctx, `
SELECT state, COALESCE(result_codec, ''), result_blob, COALESCE(error_codec, ''), error_blob
FROM scenery.durable_job_steps
WHERE service = $1 AND job_id = $2 AND step_key = $3
`, s.Service, jobID, key).Scan(&step.State, &step.ResultCodec, &step.ResultBlob, &step.ErrorCodec, &step.ErrorBlob)
	if errors.Is(err, sql.ErrNoRows) {
		return StepResult{}, false, nil
	}
	if err != nil {
		return StepResult{}, false, fmt.Errorf("durable store: get step %q for job %q: %w", key, jobID, err)
	}
	return step, true, nil
}

func (s *Store) SaveStep(ctx context.Context, jobID, key, state, resultCodec string, resultBlob, errorBlob []byte) error {
	jobID = strings.TrimSpace(jobID)
	key = strings.TrimSpace(key)
	state = strings.TrimSpace(state)
	if jobID == "" || key == "" || state == "" {
		return fmt.Errorf("durable store: job id, step key, and state are required")
	}
	if resultCodec == "" {
		resultCodec = "json"
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO scenery.durable_job_steps (service, job_id, step_key, state, idempotency_key, result_codec, result_blob, error_codec, error_blob, updated_at)
VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, CASE WHEN $8::bytea IS NULL THEN NULL ELSE 'text' END, $8, now())
ON CONFLICT(service, job_id, step_key) DO UPDATE SET
  state = excluded.state,
  result_codec = excluded.result_codec,
  result_blob = excluded.result_blob,
  error_codec = excluded.error_codec,
  error_blob = excluded.error_blob,
  updated_at = now()
`, s.Service, jobID, key, state, jobID+":"+key, resultCodec, resultBlob, errorBlob); err != nil {
		return fmt.Errorf("durable store: save step %q for job %q: %w", key, jobID, err)
	}
	return nil
}

func (s *Store) SignalJob(ctx context.Context, jobID, name, dedupeKey string, payload []byte) error {
	jobID = strings.TrimSpace(jobID)
	name = strings.TrimSpace(name)
	dedupeKey = strings.TrimSpace(dedupeKey)
	if dedupeKey == "" {
		dedupeKey = name
	}
	if jobID == "" || name == "" {
		return fmt.Errorf("durable store: job id and signal name are required")
	}
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin signal job: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_signals (service, job_id, name, dedupe_key, payload_codec, payload_blob)
VALUES ($1, $2, $3, $4, 'json', $5)
ON CONFLICT(service, job_id, name, dedupe_key) DO NOTHING
`, s.Service, jobID, name, dedupeKey, payload); err != nil {
		return rollback(tx, fmt.Errorf("durable store: signal job %q: %w", jobID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, event_type, payload_codec, payload_blob)
VALUES ($1, $2, 'job.signaled', 'json', $3)
`, s.Service, jobID, payload); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.signaled event: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit signal job: %w", err)
	}
	return nil
}

func retryDelaySeconds(attempt, initialMS, maxMS int, backoff float64) float64 {
	if initialMS <= 0 {
		initialMS = 1000
	}
	if maxMS <= 0 {
		maxMS = 60000
	}
	if backoff <= 0 {
		backoff = 2
	}
	delay := float64(initialMS) * math.Pow(backoff, float64(max(0, attempt-1)))
	if delay > float64(maxMS) {
		delay = float64(maxMS)
	}
	return delay / 1000
}

func normalizeStart(req StartRequest) StartRequest {
	req.ID = strings.TrimSpace(req.ID)
	req.TaskName = strings.TrimSpace(req.TaskName)
	req.DedupeKey = strings.TrimSpace(req.DedupeKey)
	req.ConcurrencyKey = strings.TrimSpace(req.ConcurrencyKey)
	if req.TaskVersion == 0 {
		req.TaskVersion = 1
	}
	if req.InputCodec == "" {
		req.InputCodec = "json"
	}
	if req.RequirementsJSON == "" {
		req.RequirementsJSON = "{}"
	}
	if req.LabelsJSON == "" {
		req.LabelsJSON = "{}"
	}
	if req.MemoJSON == "" {
		req.MemoJSON = "{}"
	}
	return req
}

func (s *Store) jobByDedupeKey(ctx context.Context, tx *sql.Tx, taskName, dedupeKey string) (Job, bool, error) {
	var job Job
	err := tx.QueryRowContext(ctx, `
SELECT id, task_name, state, COALESCE(dedupe_key, '')
FROM scenery.durable_jobs
WHERE service = $1 AND task_name = $2 AND dedupe_key = $3
`, s.Service, taskName, dedupeKey).Scan(&job.ID, &job.TaskName, &job.State, &job.DedupeKey)
	if err == nil {
		return job, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return Job{}, false, fmt.Errorf("durable store: query dedupe key: %w", err)
}
