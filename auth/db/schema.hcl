schema "onlava_auth" {
}

table "tenants" {
  schema = schema.onlava_auth
  comment = "audit:row_changes"

  column "id" {
    type    = uuid
    null    = false
  }

  column "name" {
    type = text
    null = false
  }

  column "deleted_at" {
    type = timestamptz
    null = true
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  column "updated_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "tenants_name_idx" {
    columns = [column.name]
  }

  index "tenants_deleted_at_idx" {
    columns = [column.deleted_at]
  }
}

table "users" {
  schema = schema.onlava_auth
  comment = "audit:row_changes"

  column "id" {
    type    = uuid
    null    = false
  }

  column "display_name" {
    type    = text
    null    = false
    default = ""
  }

  column "avatar_url" {
    type    = text
    null    = false
    default = ""
  }

  column "primary_email" {
    type    = text
    null    = false
    default = ""
  }

  column "normalized_primary_email" {
    type    = text
    null    = false
    default = ""
  }

  column "email_verified_at" {
    type = timestamptz
    null = true
  }

  column "disabled_at" {
    type = timestamptz
    null = true
  }

  column "can_impersonate_users" {
    type    = boolean
    null    = false
    default = false
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  column "updated_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "users_normalized_primary_email_key" {
    unique  = true
    columns = [column.normalized_primary_email]
    where   = "normalized_primary_email <> ''"
  }

  index "users_disabled_at_idx" {
    columns = [column.disabled_at]
  }
}

table "auth_identities" {
  schema = schema.onlava_auth
  comment = "audit:row_changes"

  column "id" {
    type    = uuid
    null    = false
  }

  column "user_id" {
    type = uuid
    null = false
  }

  column "provider" {
    type = text
    null = false
  }

  column "provider_subject" {
    type = text
    null = false
  }

  column "email" {
    type    = text
    null    = false
    default = ""
  }

  column "normalized_email" {
    type    = text
    null    = false
    default = ""
  }

  column "password_hash" {
    type    = text
    null    = false
    default = ""
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  column "updated_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "auth_identities_provider_subject_key" {
    unique  = true
    columns = [column.provider, column.provider_subject]
  }

  index "auth_identities_user_id_idx" {
    columns = [column.user_id]
  }

  index "auth_identities_normalized_email_idx" {
    columns = [column.normalized_email]
  }

  check "auth_identities_provider_check" {
    expr = "provider IN ('email', 'google')"
  }

  foreign_key "auth_identities_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_delete   = CASCADE
  }
}

table "organization_memberships" {
  schema = schema.onlava_auth
  comment = "audit:row_changes"

  column "id" {
    type    = uuid
    null    = false
  }

  column "tenant_id" {
    type = uuid
    null = false
  }

  column "user_id" {
    type = uuid
    null = false
  }

  column "role" {
    type    = text
    null    = false
    default = "member"
  }

  column "disabled_at" {
    type = timestamptz
    null = true
  }

  column "invited_by_user_id" {
    type = uuid
    null = true
  }

  column "invited_at" {
    type = timestamptz
    null = true
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  column "updated_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "organization_memberships_active_user_tenant_key" {
    unique  = true
    columns = [column.user_id, column.tenant_id]
    where   = "disabled_at IS NULL"
  }

  index "organization_memberships_tenant_id_idx" {
    columns = [column.tenant_id]
  }

  index "organization_memberships_user_id_idx" {
    columns = [column.user_id]
  }

  check "organization_memberships_role_check" {
    expr = "role IN ('owner', 'member')"
  }

  foreign_key "organization_memberships_tenant_id_fkey" {
    columns     = [column.tenant_id]
    ref_columns = [table.tenants.column.id]
    on_delete   = CASCADE
  }

  foreign_key "organization_memberships_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_delete   = CASCADE
  }

  foreign_key "organization_memberships_invited_by_user_id_fkey" {
    columns     = [column.invited_by_user_id]
    ref_columns = [table.users.column.id]
    on_delete   = SET_NULL
  }
}

table "refresh_sessions" {
  schema = schema.onlava_auth
  comment = "audit:row_changes"

  column "id" {
    type    = uuid
    null    = false
  }

  column "user_id" {
    type = uuid
    null = false
  }

  column "token_hash" {
    type = text
    null = false
  }

  column "previous_token_hash" {
    type    = text
    null    = false
    default = ""
  }

  column "previous_token_expires_at" {
    type = timestamptz
    null = true
  }

  column "active_tenant_id" {
    type = uuid
    null = true
  }

  column "expires_at" {
    type = timestamptz
    null = false
  }

  column "rotated_at" {
    type = timestamptz
    null = true
  }

  column "revoked_at" {
    type = timestamptz
    null = true
  }

  column "revoked_reason" {
    type    = text
    null    = false
    default = ""
  }

  column "user_agent" {
    type    = text
    null    = false
    default = ""
  }

  column "ip_hash" {
    type    = text
    null    = false
    default = ""
  }

  column "actor_user_id" {
    type = uuid
    null = true
  }

  column "impersonation_id" {
    type = uuid
    null = true
  }

  column "impersonation_reason" {
    type    = text
    null    = false
    default = ""
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  column "updated_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "refresh_sessions_token_hash_key" {
    unique  = true
    columns = [column.token_hash]
  }

  index "refresh_sessions_user_id_idx" {
    columns = [column.user_id]
  }

  index "refresh_sessions_active_tenant_id_idx" {
    columns = [column.active_tenant_id]
  }

  foreign_key "refresh_sessions_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_delete   = CASCADE
  }

  foreign_key "refresh_sessions_active_tenant_id_fkey" {
    columns     = [column.active_tenant_id]
    ref_columns = [table.tenants.column.id]
    on_delete   = SET_NULL
  }

  foreign_key "refresh_sessions_actor_user_id_fkey" {
    columns     = [column.actor_user_id]
    ref_columns = [table.users.column.id]
    on_delete   = SET_NULL
  }
}

table "one_time_tokens" {
  schema = schema.onlava_auth
  comment = "audit:row_changes"

  column "id" {
    type    = uuid
    null    = false
  }

  column "purpose" {
    type = text
    null = false
  }

  column "token_hash" {
    type = text
    null = false
  }

  column "user_id" {
    type = uuid
    null = true
  }

  column "tenant_id" {
    type = uuid
    null = true
  }

  column "email" {
    type    = text
    null    = false
    default = ""
  }

  column "normalized_email" {
    type    = text
    null    = false
    default = ""
  }

  column "metadata" {
    type    = jsonb
    null    = false
    default = sql("'{}'::jsonb")
  }

  column "expires_at" {
    type = timestamptz
    null = false
  }

  column "consumed_at" {
    type = timestamptz
    null = true
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "one_time_tokens_token_hash_key" {
    unique  = true
    columns = [column.token_hash]
  }

  index "one_time_tokens_purpose_email_idx" {
    columns = [column.purpose, column.normalized_email]
  }

  foreign_key "one_time_tokens_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_delete   = CASCADE
  }

  foreign_key "one_time_tokens_tenant_id_fkey" {
    columns     = [column.tenant_id]
    ref_columns = [table.tenants.column.id]
    on_delete   = CASCADE
  }
}

table "oauth_states" {
  schema = schema.onlava_auth

  column "id" {
    type    = uuid
    null    = false
  }

  column "state_hash" {
    type = text
    null = false
  }

  column "pkce_verifier" {
    type = text
    null = false
  }

  column "nonce_hash" {
    type    = text
    null    = false
    default = ""
  }

  column "redirect_path" {
    type    = text
    null    = false
    default = ""
  }

  column "expires_at" {
    type = timestamptz
    null = false
  }

  column "consumed_at" {
    type = timestamptz
    null = true
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "oauth_states_state_hash_key" {
    unique  = true
    columns = [column.state_hash]
  }
}

table "auth_attempts" {
  schema = schema.onlava_auth

  column "id" {
    type    = uuid
    null    = false
  }

  column "purpose" {
    type = text
    null = false
  }

  column "normalized_email" {
    type    = text
    null    = false
    default = ""
  }

  column "ip_hash" {
    type    = text
    null    = false
    default = ""
  }

  column "window_started_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  column "attempt_count" {
    type    = integer
    null    = false
    default = 0
  }

  column "last_attempt_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "auth_attempts_scope_key" {
    unique  = true
    columns = [column.purpose, column.normalized_email, column.ip_hash]
  }
}

table "auth_events" {
  schema = schema.onlava_auth

  column "id" {
    type    = uuid
    null    = false
  }

  column "event_type" {
    type = text
    null = false
  }

  column "user_id" {
    type = uuid
    null = true
  }

  column "actor_user_id" {
    type = uuid
    null = true
  }

  column "tenant_id" {
    type = uuid
    null = true
  }

  column "session_id" {
    type = uuid
    null = true
  }

  column "ip_hash" {
    type    = text
    null    = false
    default = ""
  }

  column "user_agent" {
    type    = text
    null    = false
    default = ""
  }

  column "metadata" {
    type    = jsonb
    null    = false
    default = sql("'{}'::jsonb")
  }

  column "created_at" {
    type    = timestamptz
    null    = false
    default = sql("now()")
  }

  primary_key {
    columns = [column.id]
  }

  index "auth_events_created_at_idx" {
    columns = [column.created_at]
  }

  index "auth_events_user_id_idx" {
    columns = [column.user_id]
  }

  index "auth_events_actor_user_id_idx" {
    columns = [column.actor_user_id]
  }

  foreign_key "auth_events_user_id_fkey" {
    columns     = [column.user_id]
    ref_columns = [table.users.column.id]
    on_delete   = SET_NULL
  }

  foreign_key "auth_events_actor_user_id_fkey" {
    columns     = [column.actor_user_id]
    ref_columns = [table.users.column.id]
    on_delete   = SET_NULL
  }

  foreign_key "auth_events_tenant_id_fkey" {
    columns     = [column.tenant_id]
    ref_columns = [table.tenants.column.id]
    on_delete   = SET_NULL
  }
}
