package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Options struct {
	Synchronous string
}

type Store struct {
	Service string
	Path    string

	db *sql.DB
}

func Open(ctx context.Context, service, path string, opts Options) (*Store, error) {
	service, err := NormalizeServiceName(service)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("durable store: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("durable store: create parent directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("durable store: open sqlite: %w", err)
	}
	store := &Store{Service: service, Path: path, db: db}
	if err := store.init(ctx, opts); err != nil {
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

func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) init(ctx context.Context, opts Options) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA synchronous = " + synchronousPragma(opts.Synchronous),
	}
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("durable store: apply %s: %w", stmt, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin migration: %w", err)
	}
	if _, err := tx.ExecContext(ctx, initSchemaSQL); err != nil {
		return rollback(tx, fmt.Errorf("durable store: apply schema: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, checksum)
VALUES (?, ?, ?)
ON CONFLICT(version) DO NOTHING
`, schemaVersion, "init", "durable-store-v1"); err != nil {
		return rollback(tx, fmt.Errorf("durable store: record schema migration: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO meta (key, value)
VALUES
  ('schema_version', ?),
  ('service_name', ?),
  ('created_by', 'scenery')
ON CONFLICT(key) DO UPDATE SET
  value = excluded.value,
  updated_at = CURRENT_TIMESTAMP
`, fmt.Sprint(schemaVersion), s.Service); err != nil {
		return rollback(tx, fmt.Errorf("durable store: write metadata: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit migration: %w", err)
	}
	return nil
}

func synchronousPragma(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "full":
		return "FULL"
	case "normal":
		return "NORMAL"
	case "off":
		return "OFF"
	default:
		return "FULL"
	}
}

func rollback(tx *sql.Tx, err error) error {
	if rbErr := tx.Rollback(); rbErr != nil {
		return errors.Join(err, rbErr)
	}
	return err
}

type TaskDeclaration struct {
	Name             string
	Version          int
	HandlerRef       string
	InputCodec       string
	ResultCodec      string
	DefaultTimeoutMS int
	DefaultLeaseMS   int
	MaxAttempts      int
	RetryInitialMS   int
	RetryMaxMS       int
	RetryBackoff     float64
	RetryJitter      float64
	RequirementsJSON string
}

func (s *Store) ReconcileTasks(ctx context.Context, tasks []TaskDeclaration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin task reconciliation: %w", err)
	}
	for _, task := range tasks {
		task = normalizeTask(task)
		if task.Name == "" {
			return rollback(tx, fmt.Errorf("durable store: task name is required"))
		}
		if task.HandlerRef == "" {
			return rollback(tx, fmt.Errorf("durable store: task %q handler ref is required", task.Name))
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO tasks (
  name,
  version,
  handler_ref,
  input_codec,
  result_codec,
  default_timeout_ms,
  default_lease_ms,
  max_attempts,
  retry_initial_ms,
  retry_max_ms,
  retry_backoff,
  retry_jitter,
  requirements_json,
  updated_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(name) DO UPDATE SET
  version = excluded.version,
  handler_ref = excluded.handler_ref,
  input_codec = excluded.input_codec,
  result_codec = excluded.result_codec,
  default_timeout_ms = excluded.default_timeout_ms,
  default_lease_ms = excluded.default_lease_ms,
  max_attempts = excluded.max_attempts,
  retry_initial_ms = excluded.retry_initial_ms,
  retry_max_ms = excluded.retry_max_ms,
  retry_backoff = excluded.retry_backoff,
  retry_jitter = excluded.retry_jitter,
  requirements_json = excluded.requirements_json,
  enabled = 1,
  updated_at = CURRENT_TIMESTAMP
`, task.Name, task.Version, task.HandlerRef, task.InputCodec, task.ResultCodec, task.DefaultTimeoutMS, task.DefaultLeaseMS, task.MaxAttempts, task.RetryInitialMS, task.RetryMaxMS, task.RetryBackoff, task.RetryJitter, task.RequirementsJSON); err != nil {
			return rollback(tx, fmt.Errorf("durable store: reconcile task %q: %w", task.Name, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit task reconciliation: %w", err)
	}
	return nil
}

func normalizeTask(task TaskDeclaration) TaskDeclaration {
	task.Name = strings.TrimSpace(task.Name)
	task.HandlerRef = strings.TrimSpace(task.HandlerRef)
	if task.Version == 0 {
		task.Version = 1
	}
	if task.InputCodec == "" {
		task.InputCodec = "json"
	}
	if task.ResultCodec == "" {
		task.ResultCodec = "json"
	}
	if task.DefaultTimeoutMS == 0 {
		task.DefaultTimeoutMS = 60000
	}
	if task.DefaultLeaseMS == 0 {
		task.DefaultLeaseMS = 60000
	}
	if task.MaxAttempts == 0 {
		task.MaxAttempts = 1
	}
	if task.RetryInitialMS == 0 {
		task.RetryInitialMS = 1000
	}
	if task.RetryMaxMS == 0 {
		task.RetryMaxMS = 60000
	}
	if task.RetryBackoff == 0 {
		task.RetryBackoff = 2
	}
	if task.RetryJitter == 0 {
		task.RetryJitter = 0.1
	}
	if task.RequirementsJSON == "" {
		task.RequirementsJSON = "{}"
	}
	return task
}

type StartRequest struct {
	ID               string
	TaskName         string
	TaskVersion      int
	DedupeKey        string
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
	InputCodec string
	InputBlob  []byte
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
		job, ok, err := jobByDedupeKey(ctx, tx, req.DedupeKey)
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
INSERT INTO jobs (
  id,
  task_name,
  task_version,
  state,
  dedupe_key,
  priority,
  input_codec,
  input_blob,
  requirements_json,
  labels_json,
	memo_json,
  max_attempts,
	created_by
)
VALUES (?, ?, ?, 'queued', NULLIF(?, ''), ?, ?, ?, ?, ?, ?, COALESCE((SELECT max_attempts FROM tasks WHERE name = ?), 1), ?)
`, req.ID, req.TaskName, req.TaskVersion, req.DedupeKey, req.Priority, req.InputCodec, req.InputBlob, req.RequirementsJSON, req.LabelsJSON, req.MemoJSON, req.TaskName, req.CreatedBy); err != nil {
		return Job{}, rollback(tx, fmt.Errorf("durable store: insert job %q: %w", req.ID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, event_type, payload_codec, payload_blob)
VALUES (?, 'job.created', 'json', x'7b7d')
`, req.ID); err != nil {
		return Job{}, rollback(tx, fmt.Errorf("durable store: append job.created event: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return Job{}, fmt.Errorf("durable store: commit start job: %w", err)
	}
	return Job{ID: req.ID, TaskName: req.TaskName, State: "queued", DedupeKey: req.DedupeKey}, nil
}

func (s *Store) LeaseReadyJob(ctx context.Context, workerID, leaseID string) (LeasedJob, bool, error) {
	return s.LeaseReadyJobWithToken(ctx, workerID, leaseID, "")
}

func (s *Store) LeaseReadyJobWithToken(ctx context.Context, workerID, leaseID, tokenHash string) (LeasedJob, bool, error) {
	workerID = strings.TrimSpace(workerID)
	leaseID = strings.TrimSpace(leaseID)
	if workerID == "" {
		return LeasedJob{}, false, fmt.Errorf("durable store: worker id is required")
	}
	if leaseID == "" {
		return LeasedJob{}, false, fmt.Errorf("durable store: lease id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LeasedJob{}, false, fmt.Errorf("durable store: begin lease job: %w", err)
	}
	var job LeasedJob
	err = tx.QueryRowContext(ctx, `
SELECT id, task_name, attempt + 1, input_codec, input_blob
FROM jobs
WHERE state = 'queued' AND run_after <= CURRENT_TIMESTAMP
ORDER BY priority DESC, created_at, id
LIMIT 1
`).Scan(&job.ID, &job.TaskName, &job.Attempt, &job.InputCodec, &job.InputBlob)
	if errors.Is(err, sql.ErrNoRows) {
		if commitErr := tx.Commit(); commitErr != nil {
			return LeasedJob{}, false, fmt.Errorf("durable store: commit empty lease: %w", commitErr)
		}
		return LeasedJob{}, false, nil
	}
	if err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: select ready job: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE jobs
SET state = 'running',
    attempt = ?,
    lease_id = ?,
    lease_owner = ?,
    lease_token_hash = ?,
    lease_until = datetime('now', '+60 seconds'),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND state = 'queued'
`, job.Attempt, leaseID, workerID, strings.TrimSpace(tokenHash), job.ID); err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: mark job %q running: %w", job.ID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_attempts (job_id, attempt, worker_id, lease_id, state)
VALUES (?, ?, ?, ?, 'running')
`, job.ID, job.Attempt, workerID, leaseID); err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: record job %q attempt: %w", job.ID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, attempt, event_type, payload_codec, payload_blob)
VALUES (?, ?, 'job.leased', 'json', x'7b7d')
`, job.ID, job.Attempt); err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: append job.leased event: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO leases (id, job_id, worker_id, token_hash, expires_at, state)
VALUES (?, ?, ?, ?, datetime('now', '+60 seconds'), 'active')
ON CONFLICT(id) DO UPDATE SET
  job_id = excluded.job_id,
  worker_id = excluded.worker_id,
  token_hash = excluded.token_hash,
  expires_at = excluded.expires_at,
  released_at = NULL,
  state = 'active'
`, leaseID, job.ID, workerID, strings.TrimSpace(tokenHash)); err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: record job %q lease: %w", job.ID, err))
	}
	if err := tx.Commit(); err != nil {
		return LeasedJob{}, false, fmt.Errorf("durable store: commit lease job: %w", err)
	}
	job.LeaseID = leaseID
	return job, true, nil
}

func (s *Store) CompleteJob(ctx context.Context, jobID string, resultBlob []byte) error {
	return s.finishJob(ctx, jobID, "", "", "succeeded", "json", resultBlob, nil)
}

func (s *Store) CompleteLeasedJob(ctx context.Context, jobID, workerID, leaseID string, resultBlob []byte) error {
	return s.finishJob(ctx, jobID, workerID, leaseID, "succeeded", "json", resultBlob, nil)
}

func (s *Store) FailJob(ctx context.Context, jobID string, errorBlob []byte) error {
	return s.failOrRetryJob(ctx, jobID, "", "", errorBlob)
}

func (s *Store) FailLeasedJob(ctx context.Context, jobID, workerID, leaseID string, errorBlob []byte) error {
	return s.failOrRetryJob(ctx, jobID, workerID, leaseID, errorBlob)
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]JobDetail, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_name, state, COALESCE(dedupe_key, ''), attempt, max_attempts, created_at, updated_at, COALESCE(completed_at, ''), COALESCE(error_codec, ''), error_blob
FROM jobs
ORDER BY created_at DESC, id DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, fmt.Errorf("durable store: list jobs: %w", err)
	}
	defer rows.Close()
	var jobs []JobDetail
	for rows.Next() {
		var job JobDetail
		if err := rows.Scan(&job.ID, &job.TaskName, &job.State, &job.DedupeKey, &job.Attempt, &job.MaxAttempts, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt, &job.ErrorCodec, &job.ErrorBlob); err != nil {
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
SELECT id, task_name, state, COALESCE(dedupe_key, ''), attempt, max_attempts, created_at, updated_at, COALESCE(completed_at, ''), COALESCE(error_codec, ''), error_blob
FROM jobs
WHERE id = ?
`, jobID).Scan(&job.ID, &job.TaskName, &job.State, &job.DedupeKey, &job.Attempt, &job.MaxAttempts, &job.CreatedAt, &job.UpdatedAt, &job.CompletedAt, &job.ErrorCodec, &job.ErrorBlob)
	if errors.Is(err, sql.ErrNoRows) {
		return JobDetail{}, false, nil
	}
	if err != nil {
		return JobDetail{}, false, fmt.Errorf("durable store: get job %q: %w", jobID, err)
	}
	return job, true, nil
}

func (s *Store) JobEvents(ctx context.Context, jobID string) ([]JobEvent, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, fmt.Errorf("durable store: job id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, job_id, COALESCE(attempt, 0), event_type, payload_codec, payload_blob, created_at
FROM job_events
WHERE job_id = ?
ORDER BY seq
`, jobID)
	if err != nil {
		return nil, fmt.Errorf("durable store: list job %q events: %w", jobID, err)
	}
	defer rows.Close()
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
	if err := tx.QueryRowContext(ctx, `SELECT attempt FROM jobs WHERE id = ?`, jobID).Scan(&attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q: %w", jobID, err))
	}
	res, err := tx.ExecContext(ctx, `
UPDATE jobs
SET state = 'canceled',
    lease_id = NULL,
    lease_owner = NULL,
    lease_token_hash = NULL,
    lease_until = NULL,
    completed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND state IN ('queued', 'running', 'sleeping', 'waiting', 'failed')
`, jobID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: cancel job %q: %w", jobID, err))
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		return rollback(tx, fmt.Errorf("durable store: job %q cannot be canceled", jobID))
	}
	if attempt > 0 {
		if _, err := tx.ExecContext(ctx, `
UPDATE job_attempts
SET state = 'canceled', finished_at = CURRENT_TIMESTAMP
WHERE job_id = ? AND attempt = ? AND state = 'running'
`, jobID, attempt); err != nil {
			return rollback(tx, fmt.Errorf("durable store: cancel job %q attempt: %w", jobID, err))
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE leases
SET state = 'canceled', released_at = CURRENT_TIMESTAMP
WHERE job_id = ? AND state = 'active'
`, jobID); err != nil {
		return rollback(tx, fmt.Errorf("durable store: cancel job %q leases: %w", jobID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, attempt, event_type, payload_codec, payload_blob)
VALUES (?, ?, 'job.canceled', 'json', x'7b7d')
`, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.canceled event: %w", err))
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
	if err := tx.QueryRowContext(ctx, `SELECT attempt FROM jobs WHERE id = ?`, jobID).Scan(&attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q: %w", jobID, err))
	}
	res, err := tx.ExecContext(ctx, `
UPDATE jobs
SET state = 'queued',
    run_after = CURRENT_TIMESTAMP,
    max_attempts = CASE WHEN max_attempts <= attempt THEN attempt + 1 ELSE max_attempts END,
    error_codec = NULL,
    error_blob = NULL,
    lease_id = NULL,
    lease_owner = NULL,
    lease_token_hash = NULL,
    lease_until = NULL,
    completed_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND state IN ('failed', 'canceled')
`, jobID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: retry job %q: %w", jobID, err))
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		return rollback(tx, fmt.Errorf("durable store: job %q cannot be retried", jobID))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, attempt, event_type, payload_codec, payload_blob)
VALUES (?, ?, 'job.retry_requested', 'json', x'7b7d')
`, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.retry_requested event: %w", err))
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
FROM job_steps
WHERE job_id = ? AND step_key = ?
`, jobID, key).Scan(&step.State, &step.ResultCodec, &step.ResultBlob, &step.ErrorCodec, &step.ErrorBlob)
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
INSERT INTO job_steps (job_id, step_key, state, idempotency_key, result_codec, result_blob, error_codec, error_blob, updated_at)
VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, CASE WHEN ? IS NULL THEN NULL ELSE 'text' END, ?, CURRENT_TIMESTAMP)
ON CONFLICT(job_id, step_key) DO UPDATE SET
  state = excluded.state,
  result_codec = excluded.result_codec,
  result_blob = excluded.result_blob,
  error_codec = excluded.error_codec,
  error_blob = excluded.error_blob,
  updated_at = CURRENT_TIMESTAMP
`, jobID, key, state, jobID+":"+key, resultCodec, resultBlob, errorBlob, errorBlob); err != nil {
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
INSERT INTO job_signals (job_id, name, dedupe_key, payload_codec, payload_blob)
VALUES (?, ?, ?, 'json', ?)
ON CONFLICT(job_id, name, dedupe_key) DO NOTHING
`, jobID, name, dedupeKey, payload); err != nil {
		return rollback(tx, fmt.Errorf("durable store: signal job %q: %w", jobID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, event_type, payload_codec, payload_blob)
VALUES (?, 'job.signaled', 'json', ?)
`, jobID, payload); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.signaled event: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit signal job: %w", err)
	}
	return nil
}

func (s *Store) HeartbeatJob(ctx context.Context, jobID, workerID, leaseID string) error {
	jobID = strings.TrimSpace(jobID)
	workerID = strings.TrimSpace(workerID)
	leaseID = strings.TrimSpace(leaseID)
	if jobID == "" || workerID == "" || leaseID == "" {
		return fmt.Errorf("durable store: job id, worker id, and lease id are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin heartbeat job: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
UPDATE jobs
SET lease_until = datetime('now', '+60 seconds'), updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND state = 'running' AND lease_owner = ? AND lease_id = ?
`, jobID, workerID, leaseID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: heartbeat job %q: %w", jobID, err))
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		return rollback(tx, fmt.Errorf("durable store: lease %q does not own job %q", leaseID, jobID))
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE job_attempts
SET heartbeat_at = CURRENT_TIMESTAMP
WHERE job_id = ? AND lease_id = ? AND worker_id = ? AND state = 'running'
`, jobID, leaseID, workerID); err != nil {
		return rollback(tx, fmt.Errorf("durable store: heartbeat job %q attempt: %w", jobID, err))
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE leases
SET expires_at = datetime('now', '+60 seconds')
WHERE id = ? AND job_id = ? AND worker_id = ? AND state = 'active'
`, leaseID, jobID, workerID); err != nil {
		return rollback(tx, fmt.Errorf("durable store: heartbeat job %q lease: %w", jobID, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit heartbeat job: %w", err)
	}
	return nil
}

func (s *Store) finishJob(ctx context.Context, jobID, workerID, leaseID, state, resultCodec string, resultBlob, errorBlob []byte) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("durable store: job id is required")
	}
	workerID = strings.TrimSpace(workerID)
	leaseID = strings.TrimSpace(leaseID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin finish job: %w", err)
	}
	var attempt int
	if err := tx.QueryRowContext(ctx, `
SELECT attempt FROM jobs
WHERE id = ? AND (? = '' OR (state = 'running' AND lease_owner = ? AND lease_id = ?))
`, jobID, workerID, workerID, leaseID).Scan(&attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q attempt: %w", jobID, err))
	}
	res, err := tx.ExecContext(ctx, `
UPDATE jobs
SET state = ?,
    result_codec = NULLIF(?, ''),
    result_blob = ?,
    error_codec = CASE WHEN ? IS NULL THEN NULL ELSE 'text' END,
    error_blob = ?,
    lease_id = NULL,
    lease_owner = NULL,
    lease_token_hash = NULL,
    lease_until = NULL,
    completed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND (? = '' OR (state = 'running' AND lease_owner = ? AND lease_id = ?))
`, state, resultCodec, resultBlob, errorBlob, errorBlob, jobID, workerID, workerID, leaseID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: mark job %q %s: %w", jobID, state, err))
	}
	changed, _ := res.RowsAffected()
	if changed == 0 {
		return rollback(tx, fmt.Errorf("durable store: lease %q does not own job %q", leaseID, jobID))
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE job_attempts
SET state = ?, finished_at = CURRENT_TIMESTAMP, error_codec = CASE WHEN ? IS NULL THEN NULL ELSE 'text' END, error_blob = ?
WHERE job_id = ? AND attempt = ?
`, state, errorBlob, errorBlob, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: finish job %q attempt: %w", jobID, err))
	}
	eventType := "job." + state
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, attempt, event_type, payload_codec, payload_blob)
VALUES (?, ?, ?, 'json', x'7b7d')
`, jobID, attempt, eventType); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append %s event: %w", eventType, err))
	}
	if leaseID != "" {
		leaseState := state
		if leaseState == "succeeded" {
			leaseState = "completed"
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE leases
SET state = ?, released_at = CURRENT_TIMESTAMP
WHERE id = ? AND job_id = ?
`, leaseState, leaseID, jobID); err != nil {
			return rollback(tx, fmt.Errorf("durable store: release lease %q: %w", leaseID, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit finish job: %w", err)
	}
	return nil
}

func (s *Store) failOrRetryJob(ctx context.Context, jobID, workerID, leaseID string, errorBlob []byte) error {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return fmt.Errorf("durable store: job id is required")
	}
	workerID = strings.TrimSpace(workerID)
	leaseID = strings.TrimSpace(leaseID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin fail job: %w", err)
	}
	var attempt, maxAttempts, retryInitialMS, retryMaxMS int
	var retryBackoff float64
	if err := tx.QueryRowContext(ctx, `
SELECT j.attempt, j.max_attempts, t.retry_initial_ms, t.retry_max_ms, t.retry_backoff
FROM jobs j
JOIN tasks t ON t.name = j.task_name
WHERE j.id = ? AND (? = '' OR (j.state = 'running' AND j.lease_owner = ? AND j.lease_id = ?))
`, jobID, workerID, workerID, leaseID).Scan(&attempt, &maxAttempts, &retryInitialMS, &retryMaxMS, &retryBackoff); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q retry policy: %w", jobID, err))
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE job_attempts
SET state = 'failed', finished_at = CURRENT_TIMESTAMP, error_codec = 'text', error_blob = ?
WHERE job_id = ? AND attempt = ?
`, errorBlob, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: fail job %q attempt: %w", jobID, err))
	}
	if attempt < maxAttempts {
		delaySeconds := retryDelaySeconds(attempt, retryInitialMS, retryMaxMS, retryBackoff)
		if _, err := tx.ExecContext(ctx, `
UPDATE jobs
SET state = 'queued',
    run_after = datetime('now', printf('+%f seconds', ?)),
    error_codec = 'text',
    error_blob = ?,
    lease_id = NULL,
    lease_owner = NULL,
    lease_token_hash = NULL,
    lease_until = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
`, delaySeconds, errorBlob, jobID); err != nil {
			return rollback(tx, fmt.Errorf("durable store: requeue job %q: %w", jobID, err))
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, attempt, event_type, payload_codec, payload_blob)
VALUES (?, ?, 'job.retry_scheduled', 'json', x'7b7d')
`, jobID, attempt); err != nil {
			return rollback(tx, fmt.Errorf("durable store: append job.retry_scheduled event: %w", err))
		}
		if leaseID != "" {
			if _, err := tx.ExecContext(ctx, `
UPDATE leases
SET state = 'failed', released_at = CURRENT_TIMESTAMP
WHERE id = ? AND job_id = ?
`, leaseID, jobID); err != nil {
				return rollback(tx, fmt.Errorf("durable store: release retry lease %q: %w", leaseID, err))
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("durable store: commit retry job: %w", err)
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE jobs
SET state = 'failed',
    error_codec = 'text',
    error_blob = ?,
    lease_id = NULL,
    lease_owner = NULL,
    lease_token_hash = NULL,
    lease_until = NULL,
    completed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
`, errorBlob, jobID); err != nil {
		return rollback(tx, fmt.Errorf("durable store: mark job %q failed: %w", jobID, err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO job_events (job_id, attempt, event_type, payload_codec, payload_blob)
VALUES (?, ?, 'job.failed', 'json', x'7b7d')
`, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.failed event: %w", err))
	}
	if leaseID != "" {
		if _, err := tx.ExecContext(ctx, `
UPDATE leases
SET state = 'failed', released_at = CURRENT_TIMESTAMP
WHERE id = ? AND job_id = ?
`, leaseID, jobID); err != nil {
			return rollback(tx, fmt.Errorf("durable store: release failed lease %q: %w", leaseID, err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit fail job: %w", err)
	}
	return nil
}

type WorkerTokenRequest struct {
	ID         string
	Name       string
	Secret     string
	ScopesJSON string
	ExpiresAt  time.Time
}

type WorkerToken struct {
	ID         string
	Name       string
	TokenHash  string
	ScopesJSON string
}

func (s *Store) UpsertSchedule(ctx context.Context, id, taskName string, every time.Duration, input []byte) error {
	id = strings.TrimSpace(id)
	taskName = strings.TrimSpace(taskName)
	if id == "" || taskName == "" {
		return fmt.Errorf("durable store: schedule id and task name are required")
	}
	if every <= 0 {
		return fmt.Errorf("durable store: schedule interval must be positive")
	}
	if len(input) == 0 {
		input = []byte(`{}`)
	}
	everyMS := int(every / time.Millisecond)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO schedules (id, task_name, spec_codec, spec_blob, catchup_window_ms, next_fire_at, input_codec, input_blob, updated_at)
VALUES (?, ?, 'json', x'7b7d', ?, CURRENT_TIMESTAMP, 'json', ?, CURRENT_TIMESTAMP)
ON CONFLICT(id) DO UPDATE SET
  task_name = excluded.task_name,
  catchup_window_ms = excluded.catchup_window_ms,
  input_blob = excluded.input_blob,
  enabled = 1,
  updated_at = CURRENT_TIMESTAMP
`, id, taskName, everyMS, input); err != nil {
		return fmt.Errorf("durable store: upsert schedule %q: %w", id, err)
	}
	return nil
}

func (s *Store) RunDueSchedules(ctx context.Context, now time.Time) ([]Job, error) {
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_name, catchup_window_ms, input_blob
FROM schedules
WHERE enabled = 1 AND next_fire_at IS NOT NULL AND next_fire_at <= CURRENT_TIMESTAMP
ORDER BY next_fire_at, id
LIMIT 50
`)
	if err != nil {
		return nil, fmt.Errorf("durable store: list due schedules: %w", err)
	}
	type dueSchedule struct {
		ID      string
		Task    string
		EveryMS int
		Input   []byte
	}
	var due []dueSchedule
	for rows.Next() {
		var item dueSchedule
		if err := rows.Scan(&item.ID, &item.Task, &item.EveryMS, &item.Input); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("durable store: scan due schedule: %w", err)
		}
		due = append(due, item)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("durable store: close due schedules: %w", err)
	}
	var jobs []Job
	for _, item := range due {
		job, err := s.Start(ctx, StartRequest{
			ID:        fmt.Sprintf("sched_%s_%d", item.ID, now.UnixNano()),
			TaskName:  item.Task,
			InputBlob: item.Input,
			CreatedBy: "schedule:" + item.ID,
		})
		if err != nil {
			return jobs, err
		}
		jobs = append(jobs, job)
		delaySeconds := float64(item.EveryMS) / 1000
		if _, err := s.db.ExecContext(ctx, `
UPDATE schedules
SET last_fire_at = CURRENT_TIMESTAMP,
    next_fire_at = datetime('now', printf('+%f seconds', ?)),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
`, delaySeconds, item.ID); err != nil {
			return jobs, fmt.Errorf("durable store: advance schedule %q: %w", item.ID, err)
		}
	}
	return jobs, nil
}

func WorkerTokenHash(secret string) string {
	sum := sha256.Sum256([]byte("scenery durable worker token\x00" + strings.TrimSpace(secret)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateWorkerToken(ctx context.Context, req WorkerTokenRequest) (WorkerToken, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.Secret = strings.TrimSpace(req.Secret)
	req.ScopesJSON = strings.TrimSpace(req.ScopesJSON)
	if req.ID == "" || req.Name == "" || req.Secret == "" {
		return WorkerToken{}, fmt.Errorf("durable store: token id, name, and secret are required")
	}
	if req.ScopesJSON == "" {
		req.ScopesJSON = "{}"
	}
	tokenHash := WorkerTokenHash(req.Secret)
	var expires any
	if !req.ExpiresAt.IsZero() {
		expires = req.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO worker_tokens (id, name, token_hash, scopes_json, expires_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  token_hash = excluded.token_hash,
  scopes_json = excluded.scopes_json,
  expires_at = excluded.expires_at,
  disabled_at = NULL
`, req.ID, req.Name, tokenHash, req.ScopesJSON, expires); err != nil {
		return WorkerToken{}, fmt.Errorf("durable store: create worker token %q: %w", req.ID, err)
	}
	return WorkerToken{ID: req.ID, Name: req.Name, TokenHash: tokenHash, ScopesJSON: req.ScopesJSON}, nil
}

func (s *Store) AuthenticateWorkerToken(ctx context.Context, secret string) (WorkerToken, bool, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return WorkerToken{}, false, nil
	}
	tokenHash := WorkerTokenHash(secret)
	var token WorkerToken
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, token_hash, scopes_json
FROM worker_tokens
WHERE token_hash = ?
  AND disabled_at IS NULL
  AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
`, tokenHash).Scan(&token.ID, &token.Name, &token.TokenHash, &token.ScopesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkerToken{}, false, nil
	}
	if err != nil {
		return WorkerToken{}, false, fmt.Errorf("durable store: authenticate worker token: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE worker_tokens SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?
`, token.ID); err != nil {
		return WorkerToken{}, false, fmt.Errorf("durable store: update worker token last used: %w", err)
	}
	return token, true, nil
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

func jobByDedupeKey(ctx context.Context, tx *sql.Tx, dedupeKey string) (Job, bool, error) {
	var job Job
	err := tx.QueryRowContext(ctx, `
SELECT id, task_name, state, COALESCE(dedupe_key, '')
FROM jobs
WHERE dedupe_key = ?
`, dedupeKey).Scan(&job.ID, &job.TaskName, &job.State, &job.DedupeKey)
	if err == nil {
		return job, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return Job{}, false, fmt.Errorf("durable store: query dedupe key: %w", err)
}
