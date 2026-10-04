package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	defer func() { _ = rows.Close() }()
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("durable store: read due schedules: %w", err)
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
