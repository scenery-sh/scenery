package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LeaseReadyJob leases the next ready job of one task. Each task is leased
// separately, so a long attempt of one task never delays another task.
func (s *Store) LeaseReadyJob(ctx context.Context, workerID, leaseID, taskName string) (LeasedJob, bool, error) {
	return s.LeaseReadyJobWithToken(ctx, workerID, leaseID, taskName, "")
}

func (s *Store) LeaseReadyJobWithToken(ctx context.Context, workerID, leaseID, taskName, tokenHash string) (LeasedJob, bool, error) {
	workerID = strings.TrimSpace(workerID)
	leaseID = strings.TrimSpace(leaseID)
	taskName = strings.TrimSpace(taskName)
	if workerID == "" {
		return LeasedJob{}, false, fmt.Errorf("durable store: worker id is required")
	}
	if leaseID == "" {
		return LeasedJob{}, false, fmt.Errorf("durable store: lease id is required")
	}
	if taskName == "" {
		return LeasedJob{}, false, fmt.Errorf("durable store: task name is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LeasedJob{}, false, fmt.Errorf("durable store: begin lease job: %w", err)
	}
	if err := s.recoverExpiredJobs(ctx, tx, taskName); err != nil {
		return LeasedJob{}, false, rollback(tx, err)
	}
	var job LeasedJob
	err = tx.QueryRowContext(ctx, `
SELECT j.id, j.task_name, j.attempt + 1, j.input_codec, j.input_blob, j.memo_json::text,
       CASE WHEN t.default_lease_ms > 0 THEN t.default_lease_ms ELSE 60000 END,
       CASE WHEN t.default_timeout_ms > 0 THEN t.default_timeout_ms ELSE 60000 END
FROM scenery.durable_jobs j
JOIN scenery.durable_tasks t ON t.service = j.service AND t.name = j.task_name AND t.version = j.task_version
WHERE j.service = $1 AND j.task_name = $2 AND j.state = 'queued' AND j.run_after <= now() AND t.enabled
  AND (t.max_concurrency <= 0 OR (
    SELECT count(*) FROM scenery.durable_jobs running
    WHERE running.service = j.service AND running.task_name = j.task_name AND running.state = 'running'
      AND COALESCE(running.concurrency_key, '') = COALESCE(j.concurrency_key, '')
  ) < t.max_concurrency)
ORDER BY j.priority DESC, j.created_at, j.id
LIMIT 1
FOR UPDATE OF t, j SKIP LOCKED
`, s.Service, taskName).Scan(&job.ID, &job.TaskName, &job.Attempt, &job.InputCodec, &job.InputBlob, &job.MemoJSON, &job.LeaseMS, &job.TimeoutMS)
	if errors.Is(err, sql.ErrNoRows) {
		delay, err := s.nextTaskWakeDelay(ctx, tx, taskName)
		if err != nil {
			return LeasedJob{}, false, rollback(tx, err)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return LeasedJob{}, false, fmt.Errorf("durable store: commit empty lease: %w", commitErr)
		}
		return LeasedJob{WakeAfter: delay}, false, nil
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

func (s *Store) recoverExpiredJobs(ctx context.Context, tx *sql.Tx, taskName string) error {
	rows, err := tx.QueryContext(ctx, `
UPDATE scenery.durable_jobs
SET state = CASE WHEN attempt < max_attempts THEN 'queued' ELSE 'failed' END,
    run_after = CASE WHEN attempt < max_attempts THEN now() ELSE run_after END,
    error_codec = 'text', error_blob = 'durable lease or timeout expired'::bytea,
    lease_id = NULL, lease_owner = NULL, lease_token_hash = NULL, lease_until = NULL,
    timeout_at = NULL,
    completed_at = CASE WHEN attempt < max_attempts THEN NULL ELSE now() END,
    updated_at = now()
WHERE service = $1 AND task_name = $2 AND state = 'running'
  AND ((lease_until IS NOT NULL AND lease_until <= now()) OR (timeout_at IS NOT NULL AND timeout_at <= now()))
RETURNING id, attempt, state
`, s.Service, taskName)
	if err != nil {
		return fmt.Errorf("durable store: recover expired jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
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
	var taskName string
	if err := tx.QueryRowContext(ctx, `
SELECT attempt, task_name FROM scenery.durable_jobs
WHERE service = $1 AND id = $2 AND ($3 = '' OR (state = 'running' AND lease_owner = $3 AND lease_id = $4))
  AND state = 'running'
FOR UPDATE
`, s.Service, jobID, workerID, leaseID).Scan(&attempt, &taskName); err != nil {
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
	if err := notifyTx(ctx, tx, s.Service, notification{Kind: "done", Task: taskName, Job: jobID}); err != nil {
		return rollback(tx, err)
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
	var taskName string
	if err := tx.QueryRowContext(ctx, `
SELECT j.attempt, j.max_attempts, t.retry_initial_ms, t.retry_max_ms, t.retry_backoff, j.task_name
FROM scenery.durable_jobs j
JOIN scenery.durable_tasks t ON t.service = j.service AND t.name = j.task_name
WHERE j.service = $1 AND j.id = $2 AND j.state = 'running' AND ($3 = '' OR (j.lease_owner = $3 AND j.lease_id = $4))
FOR UPDATE OF j
`, s.Service, jobID, workerID, leaseID).Scan(&attempt, &maxAttempts, &retryInitialMS, &retryMaxMS, &retryBackoff, &taskName); err != nil {
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
		// The retry runs later; task loops wake on their fallback timer, waiters see the new attempt.
		if err := notifyTx(ctx, tx, s.Service, notification{Kind: "requeued", Task: taskName, Job: jobID}); err != nil {
			return rollback(tx, err)
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
	if err := notifyTx(ctx, tx, s.Service, notification{Kind: "done", Task: taskName, Job: jobID}); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("durable store: commit fail job: %w", err)
	}
	return nil
}
