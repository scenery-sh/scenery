-- GENERATED: do not edit. Run `scripts/gen-auth-sqlc.sh` to refresh.

-- Add new schema named "onlava_auth"
CREATE SCHEMA "onlava_auth";
-- Create "auth_attempts" table
CREATE TABLE "onlava_auth"."auth_attempts" (
  "id" uuid NOT NULL,
  "purpose" text NOT NULL,
  "normalized_email" text NOT NULL DEFAULT '',
  "ip_hash" text NOT NULL DEFAULT '',
  "window_started_at" timestamptz NOT NULL DEFAULT now(),
  "attempt_count" integer NOT NULL DEFAULT 0,
  "last_attempt_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "auth_attempts_scope_key" to table: "auth_attempts"
CREATE UNIQUE INDEX "auth_attempts_scope_key" ON "onlava_auth"."auth_attempts" ("purpose", "normalized_email", "ip_hash");
-- Create "oauth_states" table
CREATE TABLE "onlava_auth"."oauth_states" (
  "id" uuid NOT NULL,
  "state_hash" text NOT NULL,
  "pkce_verifier" text NOT NULL,
  "nonce_hash" text NOT NULL DEFAULT '',
  "redirect_path" text NOT NULL DEFAULT '',
  "expires_at" timestamptz NOT NULL,
  "consumed_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "oauth_states_state_hash_key" to table: "oauth_states"
CREATE UNIQUE INDEX "oauth_states_state_hash_key" ON "onlava_auth"."oauth_states" ("state_hash");
-- Create "users" table
CREATE TABLE "onlava_auth"."users" (
  "id" uuid NOT NULL,
  "display_name" text NOT NULL DEFAULT '',
  "avatar_url" text NOT NULL DEFAULT '',
  "primary_email" text NOT NULL DEFAULT '',
  "normalized_primary_email" text NOT NULL DEFAULT '',
  "email_verified_at" timestamptz NULL,
  "disabled_at" timestamptz NULL,
  "can_impersonate_users" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "users_disabled_at_idx" to table: "users"
CREATE INDEX "users_disabled_at_idx" ON "onlava_auth"."users" ("disabled_at");
-- Create index "users_normalized_primary_email_key" to table: "users"
CREATE UNIQUE INDEX "users_normalized_primary_email_key" ON "onlava_auth"."users" ("normalized_primary_email") WHERE (normalized_primary_email <> ''::text);
-- Set comment to table: "users"
COMMENT ON TABLE "onlava_auth"."users" IS 'audit:row_changes';
-- Create "tenants" table
CREATE TABLE "onlava_auth"."tenants" (
  "id" uuid NOT NULL,
  "name" text NOT NULL,
  "deleted_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "tenants_deleted_at_idx" to table: "tenants"
CREATE INDEX "tenants_deleted_at_idx" ON "onlava_auth"."tenants" ("deleted_at");
-- Create index "tenants_name_idx" to table: "tenants"
CREATE INDEX "tenants_name_idx" ON "onlava_auth"."tenants" ("name");
-- Set comment to table: "tenants"
COMMENT ON TABLE "onlava_auth"."tenants" IS 'audit:row_changes';
-- Create "auth_events" table
CREATE TABLE "onlava_auth"."auth_events" (
  "id" uuid NOT NULL,
  "event_type" text NOT NULL,
  "user_id" uuid NULL,
  "actor_user_id" uuid NULL,
  "tenant_id" uuid NULL,
  "session_id" uuid NULL,
  "ip_hash" text NOT NULL DEFAULT '',
  "user_agent" text NOT NULL DEFAULT '',
  "metadata" jsonb NOT NULL DEFAULT '{}',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "auth_events_actor_user_id_fkey" FOREIGN KEY ("actor_user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "auth_events_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "onlava_auth"."tenants" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "auth_events_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL
);
-- Create index "auth_events_actor_user_id_idx" to table: "auth_events"
CREATE INDEX "auth_events_actor_user_id_idx" ON "onlava_auth"."auth_events" ("actor_user_id");
-- Create index "auth_events_created_at_idx" to table: "auth_events"
CREATE INDEX "auth_events_created_at_idx" ON "onlava_auth"."auth_events" ("created_at");
-- Create index "auth_events_user_id_idx" to table: "auth_events"
CREATE INDEX "auth_events_user_id_idx" ON "onlava_auth"."auth_events" ("user_id");
-- Create "auth_identities" table
CREATE TABLE "onlava_auth"."auth_identities" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "provider" text NOT NULL,
  "provider_subject" text NOT NULL,
  "email" text NOT NULL DEFAULT '',
  "normalized_email" text NOT NULL DEFAULT '',
  "password_hash" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "auth_identities_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "auth_identities_provider_check" CHECK (provider = ANY (ARRAY['email'::text, 'google'::text]))
);
-- Create index "auth_identities_normalized_email_idx" to table: "auth_identities"
CREATE INDEX "auth_identities_normalized_email_idx" ON "onlava_auth"."auth_identities" ("normalized_email");
-- Create index "auth_identities_provider_subject_key" to table: "auth_identities"
CREATE UNIQUE INDEX "auth_identities_provider_subject_key" ON "onlava_auth"."auth_identities" ("provider", "provider_subject");
-- Create index "auth_identities_user_id_idx" to table: "auth_identities"
CREATE INDEX "auth_identities_user_id_idx" ON "onlava_auth"."auth_identities" ("user_id");
-- Set comment to table: "auth_identities"
COMMENT ON TABLE "onlava_auth"."auth_identities" IS 'audit:row_changes';
-- Create "one_time_tokens" table
CREATE TABLE "onlava_auth"."one_time_tokens" (
  "id" uuid NOT NULL,
  "purpose" text NOT NULL,
  "token_hash" text NOT NULL,
  "user_id" uuid NULL,
  "tenant_id" uuid NULL,
  "email" text NOT NULL DEFAULT '',
  "normalized_email" text NOT NULL DEFAULT '',
  "metadata" jsonb NOT NULL DEFAULT '{}',
  "expires_at" timestamptz NOT NULL,
  "consumed_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "one_time_tokens_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "onlava_auth"."tenants" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "one_time_tokens_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "one_time_tokens_purpose_email_idx" to table: "one_time_tokens"
CREATE INDEX "one_time_tokens_purpose_email_idx" ON "onlava_auth"."one_time_tokens" ("purpose", "normalized_email");
-- Create index "one_time_tokens_token_hash_key" to table: "one_time_tokens"
CREATE UNIQUE INDEX "one_time_tokens_token_hash_key" ON "onlava_auth"."one_time_tokens" ("token_hash");
-- Set comment to table: "one_time_tokens"
COMMENT ON TABLE "onlava_auth"."one_time_tokens" IS 'audit:row_changes';
-- Create "organization_memberships" table
CREATE TABLE "onlava_auth"."organization_memberships" (
  "id" uuid NOT NULL,
  "tenant_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "role" text NOT NULL DEFAULT 'member',
  "disabled_at" timestamptz NULL,
  "invited_by_user_id" uuid NULL,
  "invited_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "organization_memberships_invited_by_user_id_fkey" FOREIGN KEY ("invited_by_user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "organization_memberships_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "onlava_auth"."tenants" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "organization_memberships_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "organization_memberships_role_check" CHECK (role = ANY (ARRAY['owner'::text, 'member'::text]))
);
-- Create index "organization_memberships_active_user_tenant_key" to table: "organization_memberships"
CREATE UNIQUE INDEX "organization_memberships_active_user_tenant_key" ON "onlava_auth"."organization_memberships" ("user_id", "tenant_id") WHERE (disabled_at IS NULL);
-- Create index "organization_memberships_tenant_id_idx" to table: "organization_memberships"
CREATE INDEX "organization_memberships_tenant_id_idx" ON "onlava_auth"."organization_memberships" ("tenant_id");
-- Create index "organization_memberships_user_id_idx" to table: "organization_memberships"
CREATE INDEX "organization_memberships_user_id_idx" ON "onlava_auth"."organization_memberships" ("user_id");
-- Set comment to table: "organization_memberships"
COMMENT ON TABLE "onlava_auth"."organization_memberships" IS 'audit:row_changes';
-- Create "refresh_sessions" table
CREATE TABLE "onlava_auth"."refresh_sessions" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "token_hash" text NOT NULL,
  "previous_token_hash" text NOT NULL DEFAULT '',
  "previous_token_expires_at" timestamptz NULL,
  "active_tenant_id" uuid NULL,
  "expires_at" timestamptz NOT NULL,
  "rotated_at" timestamptz NULL,
  "revoked_at" timestamptz NULL,
  "revoked_reason" text NOT NULL DEFAULT '',
  "user_agent" text NOT NULL DEFAULT '',
  "ip_hash" text NOT NULL DEFAULT '',
  "actor_user_id" uuid NULL,
  "impersonation_id" uuid NULL,
  "impersonation_reason" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "refresh_sessions_active_tenant_id_fkey" FOREIGN KEY ("active_tenant_id") REFERENCES "onlava_auth"."tenants" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "refresh_sessions_actor_user_id_fkey" FOREIGN KEY ("actor_user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "refresh_sessions_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "onlava_auth"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "refresh_sessions_active_tenant_id_idx" to table: "refresh_sessions"
CREATE INDEX "refresh_sessions_active_tenant_id_idx" ON "onlava_auth"."refresh_sessions" ("active_tenant_id");
-- Create index "refresh_sessions_token_hash_key" to table: "refresh_sessions"
CREATE UNIQUE INDEX "refresh_sessions_token_hash_key" ON "onlava_auth"."refresh_sessions" ("token_hash");
-- Create index "refresh_sessions_user_id_idx" to table: "refresh_sessions"
CREATE INDEX "refresh_sessions_user_id_idx" ON "onlava_auth"."refresh_sessions" ("user_id");
-- Set comment to table: "refresh_sessions"
COMMENT ON TABLE "onlava_auth"."refresh_sessions" IS 'audit:row_changes';
