package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

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
	defer func() { _ = rows.Close() }()
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
