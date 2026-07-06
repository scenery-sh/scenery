CREATE SCHEMA IF NOT EXISTS scenery;

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_auth_attempts (
  id uuid PRIMARY KEY NOT NULL,
  purpose text NOT NULL,
  normalized_email text NOT NULL DEFAULT '',
  ip_hash text NOT NULL DEFAULT '',
  window_started_at timestamptz NOT NULL DEFAULT now(),
  attempt_count integer NOT NULL DEFAULT 0,
  last_attempt_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS auth_attempts_scope_key ON scenery.scenery_auth_auth_attempts (purpose, normalized_email, ip_hash);

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_oauth_states (
  id uuid PRIMARY KEY NOT NULL,
  state_hash text NOT NULL,
  pkce_verifier text NOT NULL,
  nonce_hash text NOT NULL DEFAULT '',
  redirect_path text NOT NULL DEFAULT '',
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS oauth_states_state_hash_key ON scenery.scenery_auth_oauth_states (state_hash);

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_users (
  id uuid PRIMARY KEY NOT NULL,
  display_name text NOT NULL DEFAULT '',
  avatar_url text NOT NULL DEFAULT '',
  primary_email text NOT NULL DEFAULT '',
  normalized_primary_email text NOT NULL DEFAULT '',
  email_verified_at timestamptz,
  disabled_at timestamptz,
  can_impersonate_users boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS users_disabled_at_idx ON scenery.scenery_auth_users (disabled_at);
CREATE UNIQUE INDEX IF NOT EXISTS users_normalized_primary_email_key ON scenery.scenery_auth_users (normalized_primary_email) WHERE normalized_primary_email <> '';

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_tenants (
  id uuid PRIMARY KEY NOT NULL,
  name text NOT NULL,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tenants_deleted_at_idx ON scenery.scenery_auth_tenants (deleted_at);
CREATE INDEX IF NOT EXISTS tenants_name_idx ON scenery.scenery_auth_tenants (name);

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_auth_events (
  id uuid PRIMARY KEY NOT NULL,
  event_type text NOT NULL,
  user_id uuid,
  actor_user_id uuid,
  tenant_id uuid,
  session_id uuid,
  ip_hash text NOT NULL DEFAULT '',
  user_agent text NOT NULL DEFAULT '',
  metadata bytea NOT NULL DEFAULT '{}'::bytea,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (actor_user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE SET NULL,
  FOREIGN KEY (tenant_id) REFERENCES scenery.scenery_auth_tenants (id) ON DELETE SET NULL,
  FOREIGN KEY (user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS auth_events_actor_user_id_idx ON scenery.scenery_auth_auth_events (actor_user_id);
CREATE INDEX IF NOT EXISTS auth_events_created_at_idx ON scenery.scenery_auth_auth_events (created_at);
CREATE INDEX IF NOT EXISTS auth_events_user_id_idx ON scenery.scenery_auth_auth_events (user_id);

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_auth_identities (
  id uuid PRIMARY KEY NOT NULL,
  user_id uuid NOT NULL,
  provider text NOT NULL CHECK (provider IN ('email', 'google')),
  provider_subject text NOT NULL,
  email text NOT NULL DEFAULT '',
  normalized_email text NOT NULL DEFAULT '',
  password_hash text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS auth_identities_normalized_email_idx ON scenery.scenery_auth_auth_identities (normalized_email);
CREATE UNIQUE INDEX IF NOT EXISTS auth_identities_provider_subject_key ON scenery.scenery_auth_auth_identities (provider, provider_subject);
CREATE INDEX IF NOT EXISTS auth_identities_user_id_idx ON scenery.scenery_auth_auth_identities (user_id);

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_one_time_tokens (
  id uuid PRIMARY KEY NOT NULL,
  purpose text NOT NULL,
  token_hash text NOT NULL,
  user_id uuid,
  tenant_id uuid,
  email text NOT NULL DEFAULT '',
  normalized_email text NOT NULL DEFAULT '',
  metadata bytea NOT NULL DEFAULT '{}'::bytea,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id) REFERENCES scenery.scenery_auth_tenants (id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS one_time_tokens_purpose_email_idx ON scenery.scenery_auth_one_time_tokens (purpose, normalized_email);
CREATE UNIQUE INDEX IF NOT EXISTS one_time_tokens_token_hash_key ON scenery.scenery_auth_one_time_tokens (token_hash);

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_organization_memberships (
  id uuid PRIMARY KEY NOT NULL,
  tenant_id uuid NOT NULL,
  user_id uuid NOT NULL,
  role text NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
  disabled_at timestamptz,
  invited_by_user_id uuid,
  invited_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (invited_by_user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE SET NULL,
  FOREIGN KEY (tenant_id) REFERENCES scenery.scenery_auth_tenants (id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS organization_memberships_active_user_tenant_key ON scenery.scenery_auth_organization_memberships (user_id, tenant_id) WHERE disabled_at IS NULL;
CREATE INDEX IF NOT EXISTS organization_memberships_tenant_id_idx ON scenery.scenery_auth_organization_memberships (tenant_id);
CREATE INDEX IF NOT EXISTS organization_memberships_user_id_idx ON scenery.scenery_auth_organization_memberships (user_id);

CREATE TABLE IF NOT EXISTS scenery.scenery_auth_refresh_sessions (
  id uuid PRIMARY KEY NOT NULL,
  user_id uuid NOT NULL,
  token_hash text NOT NULL,
  previous_token_hash text NOT NULL DEFAULT '',
  previous_token_expires_at timestamptz,
  active_tenant_id uuid,
  expires_at timestamptz NOT NULL,
  rotated_at timestamptz,
  revoked_at timestamptz,
  revoked_reason text NOT NULL DEFAULT '',
  user_agent text NOT NULL DEFAULT '',
  ip_hash text NOT NULL DEFAULT '',
  actor_user_id uuid,
  impersonation_id uuid,
  impersonation_reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (active_tenant_id) REFERENCES scenery.scenery_auth_tenants (id) ON DELETE SET NULL,
  FOREIGN KEY (actor_user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE SET NULL,
  FOREIGN KEY (user_id) REFERENCES scenery.scenery_auth_users (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS refresh_sessions_active_tenant_id_idx ON scenery.scenery_auth_refresh_sessions (active_tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS refresh_sessions_token_hash_key ON scenery.scenery_auth_refresh_sessions (token_hash);
CREATE INDEX IF NOT EXISTS refresh_sessions_user_id_idx ON scenery.scenery_auth_refresh_sessions (user_id);
