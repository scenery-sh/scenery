package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
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
	return &Store{Service: service, DatabaseURL: s.DatabaseURL, db: s.db}, nil
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
	if s == nil || s.db == nil || !s.owned {
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

func (s *Store) init(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin migration: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('scenery.durable.store', 0))`); err != nil {
		return rollback(tx, fmt.Errorf("durable store: acquire migration lock: %w", err))
	}
	if _, err := tx.ExecContext(ctx, initSchemaSQL); err != nil {
		return rollback(tx, fmt.Errorf("durable store: apply schema: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_schema_migrations (version, name, checksum)
VALUES ($1, $2, $3)
ON CONFLICT(version) DO NOTHING
`, schemaVersion, "retention", "durable-postgres-v2"); err != nil {
		return rollback(tx, fmt.Errorf("durable store: record schema migration: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit migration: %w", err)
	}
	return nil
}

func rollback(tx *sql.Tx, err error) error {
	if rbErr := tx.Rollback(); rbErr != nil {
		return errors.Join(err, rbErr)
	}
	return err
}

type TaskDeclaration struct {
	Name                     string
	Version                  int
	HandlerRef               string
	InputCodec               string
	ResultCodec              string
	DefaultTimeoutMS         int
	DefaultLeaseMS           int
	MaxAttempts              int
	RetryInitialMS           int
	RetryMaxMS               int
	RetryBackoff             float64
	RetryJitter              float64
	SuccessRetentionMS       int64
	FailureRetentionMS       int64
	MaxConcurrency           int
	DeduplicationRetentionMS int64
	DeduplicationConflict    string
	RequirementsJSON         string
}

func (s *Store) ReconcileTasks(ctx context.Context, tasks []TaskDeclaration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("durable store: begin task reconciliation: %w", err)
	}
	declared := make(map[string]int, len(tasks))
	for _, task := range tasks {
		task = normalizeTask(task)
		if task.Name == "" {
			return rollback(tx, fmt.Errorf("durable store: task name is required"))
		}
		if previous, exists := declared[task.Name]; exists && previous != task.Version {
			return rollback(tx, fmt.Errorf("durable store: task %q has conflicting revisions", task.Name))
		}
		declared[task.Name] = task.Version
	}
	rows, err := tx.QueryContext(ctx, `
SELECT task_name, task_version, count(*)
FROM scenery.durable_jobs
WHERE service = $1 AND state IN ('queued', 'running')
GROUP BY task_name, task_version
`, s.Service)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: inspect active task revisions: %w", err))
	}
	type activeRevision struct {
		name           string
		version, count int
	}
	var active []activeRevision
	for rows.Next() {
		var name string
		var version, count int
		if err := rows.Scan(&name, &version, &count); err != nil {
			_ = rows.Close()
			return rollback(tx, fmt.Errorf("durable store: scan active task revision: %w", err))
		}
		active = append(active, activeRevision{name: name, version: version, count: count})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return rollback(tx, fmt.Errorf("durable store: inspect active task revision rows: %w", err))
	}
	if err := rows.Close(); err != nil {
		return rollback(tx, fmt.Errorf("durable store: close active task revision rows: %w", err))
	}
	for _, item := range active {
		wanted, exists := declared[item.name]
		if !exists || wanted != item.version {
			return rollback(tx, fmt.Errorf("durable store: migration_required: task %q has %d active jobs at revision %d but runtime provides revision %d", item.name, item.count, item.version, wanted))
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scenery.durable_tasks SET enabled = false, updated_at = now() WHERE service = $1`, s.Service); err != nil {
		return rollback(tx, fmt.Errorf("durable store: disable previous task declarations: %w", err))
	}
	for _, task := range tasks {
		task = normalizeTask(task)
		if task.Name == "" {
			return rollback(tx, fmt.Errorf("durable store: task name is required"))
		}
		if task.HandlerRef == "" {
			return rollback(tx, fmt.Errorf("durable store: task %q handler ref is required", task.Name))
		}
		if task.DeduplicationConflict != "return_existing" {
			return rollback(tx, fmt.Errorf("durable store: task %q has unsupported deduplication conflict policy %q", task.Name, task.DeduplicationConflict))
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_tasks (
  service, name, version, handler_ref, input_codec, result_codec,
  default_timeout_ms, default_lease_ms, max_attempts, retry_initial_ms,
  retry_max_ms, retry_backoff, retry_jitter, success_retention_ms,
  failure_retention_ms, max_concurrency, deduplication_retention_ms,
  deduplication_conflict, requirements_json, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19::jsonb, now())
ON CONFLICT(service, name) DO UPDATE SET
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
  success_retention_ms = excluded.success_retention_ms,
  failure_retention_ms = excluded.failure_retention_ms,
  max_concurrency = excluded.max_concurrency,
  deduplication_retention_ms = excluded.deduplication_retention_ms,
  deduplication_conflict = excluded.deduplication_conflict,
  requirements_json = excluded.requirements_json,
  enabled = true,
  updated_at = now()
`, s.Service, task.Name, task.Version, task.HandlerRef, task.InputCodec, task.ResultCodec, task.DefaultTimeoutMS, task.DefaultLeaseMS, task.MaxAttempts, task.RetryInitialMS, task.RetryMaxMS, task.RetryBackoff, task.RetryJitter, task.SuccessRetentionMS, task.FailureRetentionMS, task.MaxConcurrency, task.DeduplicationRetentionMS, task.DeduplicationConflict, task.RequirementsJSON); err != nil {
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
	if task.SuccessRetentionMS == 0 {
		task.SuccessRetentionMS = int64((7 * 24 * time.Hour) / time.Millisecond)
	}
	if task.FailureRetentionMS == 0 {
		task.FailureRetentionMS = int64((30 * 24 * time.Hour) / time.Millisecond)
	}
	if task.DeduplicationRetentionMS == 0 {
		task.DeduplicationRetentionMS = task.SuccessRetentionMS
	}
	if task.DeduplicationConflict == "" {
		task.DeduplicationConflict = "return_existing"
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
	if err := s.recoverExpiredJobs(ctx, tx); err != nil {
		return LeasedJob{}, false, rollback(tx, err)
	}
	var job LeasedJob
	err = tx.QueryRowContext(ctx, `
SELECT j.id, j.task_name, j.attempt + 1, j.input_codec, j.input_blob, j.memo_json::text,
       CASE WHEN t.default_lease_ms > 0 THEN t.default_lease_ms ELSE 60000 END,
       CASE WHEN t.default_timeout_ms > 0 THEN t.default_timeout_ms ELSE 60000 END
FROM scenery.durable_jobs j
JOIN scenery.durable_tasks t ON t.service = j.service AND t.name = j.task_name AND t.version = j.task_version
WHERE j.service = $1 AND j.state = 'queued' AND j.run_after <= now() AND t.enabled
  AND (t.max_concurrency <= 0 OR (
    SELECT count(*) FROM scenery.durable_jobs running
    WHERE running.service = j.service AND running.task_name = j.task_name AND running.state = 'running'
      AND COALESCE(running.concurrency_key, '') = COALESCE(j.concurrency_key, '')
  ) < t.max_concurrency)
ORDER BY j.priority DESC, j.created_at, j.id
LIMIT 1
FOR UPDATE OF t, j SKIP LOCKED
`, s.Service).Scan(&job.ID, &job.TaskName, &job.Attempt, &job.InputCodec, &job.InputBlob, &job.MemoJSON, &job.LeaseMS, &job.TimeoutMS)
	if errors.Is(err, sql.ErrNoRows) {
		if commitErr := tx.Commit(); commitErr != nil {
			return LeasedJob{}, false, fmt.Errorf("durable store: commit empty lease: %w", commitErr)
		}
		return LeasedJob{}, false, nil
	}
	if err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: select ready job: %w", err))
	}
	res, err := tx.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET state = 'running', attempt = $1, lease_id = $2, lease_owner = $3,
    lease_token_hash = $4, lease_until = now() + ($5 * interval '1 millisecond'),
    timeout_at = now() + ($6 * interval '1 millisecond'), updated_at = now()
WHERE service = $7 AND id = $8 AND state = 'queued'
`, job.Attempt, leaseID, workerID, strings.TrimSpace(tokenHash), job.LeaseMS, job.TimeoutMS, s.Service, job.ID)
	if err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: mark job %q running: %w", job.ID, err))
	}
	if changed, err := res.RowsAffected(); err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: mark job %q running rows affected: %w", job.ID, err))
	} else if changed != 1 {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: mark job %q running affected %d rows", job.ID, changed))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, attempt, event_type, payload_codec, payload_blob)
VALUES ($1, $2, $3, 'job.leased', 'json', '{}'::bytea)
`, s.Service, job.ID, job.Attempt); err != nil {
		return LeasedJob{}, false, rollback(tx, fmt.Errorf("durable store: append job.leased event: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return LeasedJob{}, false, fmt.Errorf("durable store: commit lease job: %w", err)
	}
	job.LeaseID = leaseID
	return job, true, nil
}

func (s *Store) recoverExpiredJobs(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
UPDATE scenery.durable_jobs
SET state = CASE WHEN attempt < max_attempts THEN 'queued' ELSE 'failed' END,
    run_after = CASE WHEN attempt < max_attempts THEN now() ELSE run_after END,
    error_codec = 'text', error_blob = 'durable lease or timeout expired'::bytea,
    lease_id = NULL, lease_owner = NULL, lease_token_hash = NULL, lease_until = NULL,
    timeout_at = NULL,
    completed_at = CASE WHEN attempt < max_attempts THEN NULL ELSE now() END,
    updated_at = now()
WHERE service = $1 AND state = 'running'
  AND ((lease_until IS NOT NULL AND lease_until <= now()) OR (timeout_at IS NOT NULL AND timeout_at <= now()))
RETURNING id, attempt, state
`, s.Service)
	if err != nil {
		return fmt.Errorf("durable store: recover expired jobs: %w", err)
	}
	type recoveredJob struct {
		id      string
		attempt int
		state   string
	}
	var recovered []recoveredJob
	for rows.Next() {
		var item recoveredJob
		if err := rows.Scan(&item.id, &item.attempt, &item.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("durable store: scan expired job: %w", err)
		}
		recovered = append(recovered, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("durable store: recover expired job rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("durable store: close expired job rows: %w", err)
	}
	for _, item := range recovered {
		eventType := "job.lease_expired"
		if item.state == "failed" {
			eventType = "job.failed"
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, attempt, event_type, payload_codec, payload_blob)
VALUES ($1, $2, $3, $4, 'json', '{}'::bytea)
`, s.Service, item.id, item.attempt, eventType); err != nil {
			return fmt.Errorf("durable store: append %s event: %w", eventType, err)
		}
	}
	return nil
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
	defer rows.Close()
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
	if err := tx.QueryRowContext(ctx, `SELECT attempt FROM scenery.durable_jobs WHERE service = $1 AND id = $2 FOR UPDATE`, s.Service, jobID).Scan(&attempt); err != nil {
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
	if err := tx.QueryRowContext(ctx, `SELECT attempt FROM scenery.durable_jobs WHERE service = $1 AND id = $2 FOR UPDATE`, s.Service, jobID).Scan(&attempt); err != nil {
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

func (s *Store) HeartbeatJob(ctx context.Context, jobID, workerID, leaseID string) error {
	jobID = strings.TrimSpace(jobID)
	workerID = strings.TrimSpace(workerID)
	leaseID = strings.TrimSpace(leaseID)
	if jobID == "" || workerID == "" || leaseID == "" {
		return fmt.Errorf("durable store: job id, worker id, and lease id are required")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET lease_until = LEAST(COALESCE(timeout_at, 'infinity'::timestamptz), now() + (
  COALESCE((
    SELECT t.default_lease_ms
    FROM scenery.durable_tasks t
    WHERE t.service = scenery.durable_jobs.service AND t.name = scenery.durable_jobs.task_name
      AND t.version = scenery.durable_jobs.task_version
      AND t.default_lease_ms > 0
  ), 60000) * interval '1 millisecond'
)), updated_at = now()
WHERE service = $1 AND id = $2 AND state = 'running' AND lease_owner = $3 AND lease_id = $4
`, s.Service, jobID, workerID, leaseID)
	if err != nil {
		return fmt.Errorf("durable store: heartbeat job %q: %w", jobID, err)
	}
	if changed, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("durable store: heartbeat job %q rows affected: %w", jobID, err)
	} else if changed != 1 {
		return fmt.Errorf("durable store: lease %q does not own job %q", leaseID, jobID)
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
SELECT attempt FROM scenery.durable_jobs
WHERE service = $1 AND id = $2 AND ($3 = '' OR (state = 'running' AND lease_owner = $3 AND lease_id = $4))
  AND state = 'running'
FOR UPDATE
`, s.Service, jobID, workerID, leaseID).Scan(&attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q attempt: %w", jobID, err))
	}
	res, err := tx.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET state = $1, result_codec = NULLIF($2, ''), result_blob = $3,
    error_codec = CASE WHEN $4::bytea IS NULL THEN NULL ELSE 'text' END, error_blob = $4,
    lease_id = NULL, lease_owner = NULL, lease_token_hash = NULL, lease_until = NULL,
    completed_at = now(), updated_at = now()
WHERE service = $5 AND id = $6 AND state = 'running' AND ($7 = '' OR (lease_owner = $7 AND lease_id = $8))
`, state, resultCodec, resultBlob, errorBlob, s.Service, jobID, workerID, leaseID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: mark job %q %s: %w", jobID, state, err))
	}
	if changed, err := res.RowsAffected(); err != nil {
		return rollback(tx, fmt.Errorf("durable store: mark job %q %s rows affected: %w", jobID, state, err))
	} else if changed != 1 {
		return rollback(tx, fmt.Errorf("durable store: lease %q does not own job %q", leaseID, jobID))
	}
	eventType := "job." + state
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, attempt, event_type, payload_codec, payload_blob)
VALUES ($1, $2, $3, $4, 'json', '{}'::bytea)
`, s.Service, jobID, attempt, eventType); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append %s event: %w", eventType, err))
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
FROM scenery.durable_jobs j
JOIN scenery.durable_tasks t ON t.service = j.service AND t.name = j.task_name
WHERE j.service = $1 AND j.id = $2 AND j.state = 'running' AND ($3 = '' OR (j.lease_owner = $3 AND j.lease_id = $4))
FOR UPDATE OF j
`, s.Service, jobID, workerID, leaseID).Scan(&attempt, &maxAttempts, &retryInitialMS, &retryMaxMS, &retryBackoff); err != nil {
		return rollback(tx, fmt.Errorf("durable store: load job %q retry policy: %w", jobID, err))
	}
	if attempt < maxAttempts {
		delaySeconds := retryDelaySeconds(attempt, retryInitialMS, retryMaxMS, retryBackoff)
		res, err := tx.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET state = 'queued', run_after = now() + ($1 * interval '1 second'),
    error_codec = 'text', error_blob = $2,
    lease_id = NULL, lease_owner = NULL, lease_token_hash = NULL, lease_until = NULL,
    updated_at = now()
WHERE service = $3 AND id = $4 AND state = 'running' AND ($5 = '' OR (lease_owner = $5 AND lease_id = $6))
`, delaySeconds, errorBlob, s.Service, jobID, workerID, leaseID)
		if err != nil {
			return rollback(tx, fmt.Errorf("durable store: requeue job %q: %w", jobID, err))
		}
		if changed, err := res.RowsAffected(); err != nil {
			return rollback(tx, fmt.Errorf("durable store: requeue job %q rows affected: %w", jobID, err))
		} else if changed != 1 {
			return rollback(tx, fmt.Errorf("durable store: requeue job %q affected %d rows", jobID, changed))
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, attempt, event_type, payload_codec, payload_blob)
VALUES ($1, $2, $3, 'job.retry_scheduled', 'json', '{}'::bytea)
`, s.Service, jobID, attempt); err != nil {
			return rollback(tx, fmt.Errorf("durable store: append job.retry_scheduled event: %w", err))
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("durable store: commit retry job: %w", err)
		}
		return nil
	}
	res, err := tx.ExecContext(ctx, `
UPDATE scenery.durable_jobs
SET state = 'failed', error_codec = 'text', error_blob = $1,
    lease_id = NULL, lease_owner = NULL, lease_token_hash = NULL, lease_until = NULL,
    completed_at = now(), updated_at = now()
WHERE service = $2 AND id = $3 AND state = 'running' AND ($4 = '' OR (lease_owner = $4 AND lease_id = $5))
`, errorBlob, s.Service, jobID, workerID, leaseID)
	if err != nil {
		return rollback(tx, fmt.Errorf("durable store: mark job %q failed: %w", jobID, err))
	}
	if changed, err := res.RowsAffected(); err != nil {
		return rollback(tx, fmt.Errorf("durable store: mark job %q failed rows affected: %w", jobID, err))
	} else if changed != 1 {
		return rollback(tx, fmt.Errorf("durable store: mark job %q failed affected %d rows", jobID, changed))
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO scenery.durable_job_events (service, job_id, attempt, event_type, payload_codec, payload_blob)
VALUES ($1, $2, $3, 'job.failed', 'json', '{}'::bytea)
`, s.Service, jobID, attempt); err != nil {
		return rollback(tx, fmt.Errorf("durable store: append job.failed event: %w", err))
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
INSERT INTO scenery.durable_schedules (service, id, task_name, spec_codec, spec_blob, catchup_window_ms, next_fire_at, input_codec, input_blob, updated_at)
VALUES ($1, $2, $3, 'json', '{}'::bytea, $4, now(), 'json', $5, now())
ON CONFLICT(service, id) DO UPDATE SET
  task_name = excluded.task_name,
  catchup_window_ms = excluded.catchup_window_ms,
  input_blob = excluded.input_blob,
  enabled = true,
  updated_at = now()
`, s.Service, id, taskName, everyMS, input); err != nil {
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
FROM scenery.durable_schedules
WHERE service = $1 AND enabled = true AND next_fire_at IS NOT NULL AND next_fire_at <= now()
ORDER BY next_fire_at, id
LIMIT 50
`, s.Service)
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
UPDATE scenery.durable_schedules
SET last_fire_at = now(), next_fire_at = now() + ($1 * interval '1 second'), updated_at = now()
WHERE service = $2 AND id = $3
`, delaySeconds, s.Service, item.ID); err != nil {
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
		expires = req.ExpiresAt.UTC()
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO scenery.durable_worker_tokens (service, id, name, token_hash, scopes_json, expires_at)
VALUES ($1, $2, $3, $4, $5::jsonb, $6)
ON CONFLICT(service, id) DO UPDATE SET
  name = excluded.name,
  token_hash = excluded.token_hash,
  scopes_json = excluded.scopes_json,
  expires_at = excluded.expires_at,
  disabled_at = NULL
`, s.Service, req.ID, req.Name, tokenHash, req.ScopesJSON, expires); err != nil {
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
SELECT id, name, token_hash, scopes_json::text
FROM scenery.durable_worker_tokens
WHERE service = $1
  AND token_hash = $2
  AND disabled_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
`, s.Service, tokenHash).Scan(&token.ID, &token.Name, &token.TokenHash, &token.ScopesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkerToken{}, false, nil
	}
	if err != nil {
		return WorkerToken{}, false, fmt.Errorf("durable store: authenticate worker token: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE scenery.durable_worker_tokens SET last_used_at = now() WHERE service = $1 AND id = $2
`, s.Service, token.ID); err != nil {
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
