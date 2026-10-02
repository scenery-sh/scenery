package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const idleReconciliationDelay = 10 * time.Second

// nextTaskWakeDelay runs only after an empty acquisition. Notifications wake
// new/completed work immediately; known retries, leases and timeouts supply
// their own deadlines. Reconciliation bounds missed-notification progress.
func (s *Store) nextTaskWakeDelay(ctx context.Context, tx *sql.Tx, taskName string) (time.Duration, error) {
	var seconds float64
	err := tx.QueryRowContext(ctx, `
SELECT COALESCE(EXTRACT(EPOCH FROM (min(CASE
  WHEN state = 'queued' AND run_after > now() THEN run_after
  WHEN state = 'running' THEN LEAST(lease_until, timeout_at)
END) - now())), $3)
FROM scenery.durable_jobs
WHERE service = $1 AND task_name = $2 AND state IN ('queued', 'running')
`, s.Service, taskName, idleReconciliationDelay.Seconds()).Scan(&seconds)
	if err != nil {
		return 0, fmt.Errorf("durable store: next task wake: %w", err)
	}
	return taskWakeDelay(seconds), nil
}

func taskWakeDelay(seconds float64) time.Duration {
	// A due row may be locked by another acquisition. Avoid a zero-delay loop.
	return time.Duration(min(max(seconds, 0.01), idleReconciliationDelay.Seconds()) * float64(time.Second))
}
