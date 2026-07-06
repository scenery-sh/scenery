package store

const schemaVersion = 1

const initSchemaSQL = `
CREATE SCHEMA IF NOT EXISTS scenery;

CREATE TABLE IF NOT EXISTS scenery.durable_schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS scenery.durable_tasks (
  service TEXT NOT NULL,
  name TEXT NOT NULL,
  version INTEGER NOT NULL DEFAULT 1,
  handler_ref TEXT NOT NULL,
  input_codec TEXT NOT NULL DEFAULT 'json',
  result_codec TEXT NOT NULL DEFAULT 'json',
  default_timeout_ms INTEGER NOT NULL DEFAULT 60000,
  default_lease_ms INTEGER NOT NULL DEFAULT 60000,
  max_attempts INTEGER NOT NULL DEFAULT 1,
  retry_initial_ms INTEGER NOT NULL DEFAULT 1000,
  retry_max_ms INTEGER NOT NULL DEFAULT 60000,
  retry_backoff REAL NOT NULL DEFAULT 2.0,
  retry_jitter REAL NOT NULL DEFAULT 0.1,
  requirements_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service, name)
);

CREATE TABLE IF NOT EXISTS scenery.durable_jobs (
  service TEXT NOT NULL,
  id TEXT NOT NULL,
  task_name TEXT NOT NULL,
  task_version INTEGER NOT NULL DEFAULT 1,
  state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
  dedupe_key TEXT,
  priority INTEGER NOT NULL DEFAULT 0,
  run_after TIMESTAMPTZ NOT NULL DEFAULT now(),
  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 1,
  timeout_at TIMESTAMPTZ,
  deadline_at TIMESTAMPTZ,
  lease_id TEXT,
  lease_owner TEXT,
  lease_token_hash TEXT,
  lease_until TIMESTAMPTZ,
  input_codec TEXT NOT NULL DEFAULT 'json',
  input_blob BYTEA NOT NULL,
  result_codec TEXT,
  result_blob BYTEA,
  error_codec TEXT,
  error_blob BYTEA,
  requirements_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  labels_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  memo_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  PRIMARY KEY (service, id),
  FOREIGN KEY (service, task_name) REFERENCES scenery.durable_tasks(service, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS durable_jobs_dedupe_idx
ON scenery.durable_jobs(service, task_name, dedupe_key)
WHERE dedupe_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS durable_jobs_ready_idx
ON scenery.durable_jobs(service, state, run_after, priority DESC, created_at, id);

CREATE INDEX IF NOT EXISTS durable_jobs_task_state_idx
ON scenery.durable_jobs(service, task_name, state, created_at);

CREATE INDEX IF NOT EXISTS durable_jobs_lease_idx
ON scenery.durable_jobs(service, lease_owner, lease_until);

CREATE TABLE IF NOT EXISTS scenery.durable_job_events (
  seq BIGSERIAL PRIMARY KEY,
  service TEXT NOT NULL,
  job_id TEXT NOT NULL,
  attempt INTEGER,
  event_type TEXT NOT NULL,
  payload_codec TEXT NOT NULL DEFAULT 'json',
  payload_blob BYTEA,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (service, job_id) REFERENCES scenery.durable_jobs(service, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS durable_job_events_job_seq_idx
ON scenery.durable_job_events(service, job_id, seq);

CREATE INDEX IF NOT EXISTS durable_job_events_type_idx
ON scenery.durable_job_events(service, event_type, created_at);

CREATE TABLE IF NOT EXISTS scenery.durable_job_steps (
  service TEXT NOT NULL,
  job_id TEXT NOT NULL,
  step_key TEXT NOT NULL,
  step_version INTEGER NOT NULL DEFAULT 1,
  state TEXT NOT NULL CHECK (state IN ('started', 'succeeded', 'failed', 'skipped')),
  attempt INTEGER NOT NULL DEFAULT 0,
  input_hash TEXT,
  idempotency_key TEXT NOT NULL,
  result_codec TEXT,
  result_blob BYTEA,
  error_codec TEXT,
  error_blob BYTEA,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service, job_id, step_key),
  FOREIGN KEY (service, job_id) REFERENCES scenery.durable_jobs(service, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS scenery.durable_job_signals (
  service TEXT NOT NULL,
  job_id TEXT NOT NULL,
  name TEXT NOT NULL,
  dedupe_key TEXT NOT NULL,
  payload_codec TEXT NOT NULL DEFAULT 'json',
  payload_blob BYTEA NOT NULL,
  consumed_at TIMESTAMPTZ,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service, job_id, name, dedupe_key),
  FOREIGN KEY (service, job_id) REFERENCES scenery.durable_jobs(service, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS scenery.durable_schedules (
  service TEXT NOT NULL,
  id TEXT NOT NULL,
  task_name TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  spec_codec TEXT NOT NULL DEFAULT 'json',
  spec_blob BYTEA NOT NULL,
  overlap_policy TEXT NOT NULL CHECK (overlap_policy IN ('skip', 'buffer_one', 'buffer_all', 'allow_all')) DEFAULT 'skip',
  catchup_window_ms INTEGER NOT NULL DEFAULT 60000,
  next_fire_at TIMESTAMPTZ,
  last_fire_at TIMESTAMPTZ,
  input_codec TEXT NOT NULL DEFAULT 'json',
  input_blob BYTEA NOT NULL DEFAULT '{}'::bytea,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (service, id),
  FOREIGN KEY (service, task_name) REFERENCES scenery.durable_tasks(service, name)
);

CREATE TABLE IF NOT EXISTS scenery.durable_worker_tokens (
  service TEXT NOT NULL,
  id TEXT NOT NULL,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  scopes_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ,
  disabled_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  PRIMARY KEY (service, id)
);
`
