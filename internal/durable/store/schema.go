package store

const schemaVersion = 1

const initSchemaSQL = `
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tasks (
  name TEXT PRIMARY KEY,
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
  requirements_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,
  task_name TEXT NOT NULL,
  task_version INTEGER NOT NULL DEFAULT 1,
  state TEXT NOT NULL CHECK (
    state IN (
      'queued',
      'running',
      'sleeping',
      'waiting',
      'succeeded',
      'failed',
      'canceled'
    )
  ),
  dedupe_key TEXT UNIQUE,
  priority INTEGER NOT NULL DEFAULT 0,
  run_after TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  attempt INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 1,
  timeout_at TEXT,
  deadline_at TEXT,
  lease_id TEXT,
  lease_owner TEXT,
  lease_token_hash TEXT,
  lease_until TEXT,
  input_codec TEXT NOT NULL DEFAULT 'json',
  input_blob BLOB NOT NULL,
  result_codec TEXT,
  result_blob BLOB,
  error_codec TEXT,
  error_blob BLOB,
  requirements_json TEXT NOT NULL DEFAULT '{}',
  labels_json TEXT NOT NULL DEFAULT '{}',
  memo_json TEXT NOT NULL DEFAULT '{}',
  created_by TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at TEXT,
  FOREIGN KEY (task_name) REFERENCES tasks(name)
);

CREATE INDEX IF NOT EXISTS jobs_ready_idx
ON jobs(state, run_after, priority, created_at);

CREATE INDEX IF NOT EXISTS jobs_task_state_idx
ON jobs(task_name, state, created_at);

CREATE INDEX IF NOT EXISTS jobs_lease_idx
ON jobs(lease_owner, lease_until);

CREATE INDEX IF NOT EXISTS jobs_dedupe_idx
ON jobs(dedupe_key);

CREATE TABLE IF NOT EXISTS job_attempts (
  job_id TEXT NOT NULL,
  attempt INTEGER NOT NULL,
  worker_id TEXT,
  lease_id TEXT,
  started_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  heartbeat_at TEXT,
  finished_at TEXT,
  state TEXT NOT NULL CHECK (
    state IN ('running', 'succeeded', 'failed', 'expired', 'canceled')
  ),
  error_codec TEXT,
  error_blob BLOB,
  PRIMARY KEY (job_id, attempt),
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS job_events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id TEXT NOT NULL,
  attempt INTEGER,
  event_type TEXT NOT NULL,
  payload_codec TEXT NOT NULL DEFAULT 'json',
  payload_blob BLOB,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS job_events_job_seq_idx
ON job_events(job_id, seq);

CREATE INDEX IF NOT EXISTS job_events_type_idx
ON job_events(event_type, created_at);

CREATE TABLE IF NOT EXISTS job_steps (
  job_id TEXT NOT NULL,
  step_key TEXT NOT NULL,
  step_version INTEGER NOT NULL DEFAULT 1,
  state TEXT NOT NULL CHECK (
    state IN ('started', 'succeeded', 'failed', 'skipped')
  ),
  attempt INTEGER NOT NULL DEFAULT 0,
  input_hash TEXT,
  idempotency_key TEXT NOT NULL,
  result_codec TEXT,
  result_blob BLOB,
  error_codec TEXT,
  error_blob BLOB,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (job_id, step_key),
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS job_signals (
  job_id TEXT NOT NULL,
  name TEXT NOT NULL,
  dedupe_key TEXT NOT NULL,
  payload_codec TEXT NOT NULL DEFAULT 'json',
  payload_blob BLOB NOT NULL,
  consumed_at TEXT,
  received_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (job_id, name, dedupe_key),
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS schedules (
  id TEXT PRIMARY KEY,
  task_name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  spec_codec TEXT NOT NULL DEFAULT 'json',
  spec_blob BLOB NOT NULL,
  overlap_policy TEXT NOT NULL CHECK (
    overlap_policy IN (
      'skip',
      'buffer_one',
      'buffer_all',
      'allow_all'
    )
  ) DEFAULT 'skip',
  catchup_window_ms INTEGER NOT NULL DEFAULT 60000,
  next_fire_at TEXT,
  last_fire_at TEXT,
  input_codec TEXT NOT NULL DEFAULT 'json',
  input_blob BLOB NOT NULL DEFAULT x'7b7d',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (task_name) REFERENCES tasks(name)
);

CREATE TABLE IF NOT EXISTS workers (
  id TEXT PRIMARY KEY,
  token_id TEXT,
  deployment_id TEXT,
  build_id TEXT,
  hostname TEXT,
  pid INTEGER,
  region TEXT,
  labels_json TEXT NOT NULL DEFAULT '{}',
  subscriptions_json TEXT NOT NULL DEFAULT '[]',
  first_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  disabled_at TEXT
);

CREATE TABLE IF NOT EXISTS worker_tokens (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  scopes_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TEXT,
  disabled_at TEXT,
  last_used_at TEXT
);

CREATE TABLE IF NOT EXISTS leases (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  worker_id TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  acquired_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TEXT NOT NULL,
  released_at TEXT,
  state TEXT NOT NULL CHECK (
    state IN ('active', 'completed', 'failed', 'expired', 'canceled')
  ),
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS locks (
  name TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`
