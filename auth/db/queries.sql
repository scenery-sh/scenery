-- name: CreateUser :one
INSERT INTO scenery_auth_users (
  id,
  display_name,
  avatar_url,
  primary_email,
  normalized_primary_email,
  email_verified_at
)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at
FROM scenery_auth_users
WHERE id = ?;

-- name: GetUserByNormalizedEmail :one
SELECT id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at
FROM scenery_auth_users
WHERE normalized_primary_email = ?;

-- name: MarkUserEmailVerified :one
UPDATE scenery_auth_users
SET email_verified_at = COALESCE(email_verified_at, CURRENT_TIMESTAMP),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at;

-- name: UpdateUserProfileFromProvider :one
UPDATE scenery_auth_users
SET display_name = CASE WHEN sqlc.arg(display_name) <> '' THEN sqlc.arg(display_name) ELSE display_name END,
    avatar_url = CASE WHEN sqlc.arg(avatar_url) <> '' THEN sqlc.arg(avatar_url) ELSE avatar_url END,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at;

-- name: CreateAuthIdentity :one
INSERT INTO scenery_auth_auth_identities (
  id,
  user_id,
  provider,
  provider_subject,
  email,
  normalized_email,
  password_hash
)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at;

-- name: GetAuthIdentityByProviderSubject :one
SELECT id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at
FROM scenery_auth_auth_identities
WHERE provider = ?
  AND provider_subject = ?;

-- name: GetEmailIdentityForLogin :one
SELECT id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at
FROM scenery_auth_auth_identities
WHERE provider = 'email'
  AND provider_subject = ?;

-- name: UpdateIdentityPasswordHash :one
UPDATE scenery_auth_auth_identities
SET password_hash = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at;

-- name: CreateTenant :one
INSERT INTO scenery_auth_tenants (id, name)
VALUES (?, ?)
RETURNING id, name, deleted_at, created_at, updated_at;

-- name: GetTenantByID :one
SELECT id, name, deleted_at, created_at, updated_at
FROM scenery_auth_tenants
WHERE id = ?;

-- name: UpdateTenantName :one
UPDATE scenery_auth_tenants
SET name = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND deleted_at IS NULL
RETURNING id, name, deleted_at, created_at, updated_at;

-- name: SoftDeleteTenant :one
UPDATE scenery_auth_tenants
SET deleted_at = COALESCE(deleted_at, CURRENT_TIMESTAMP),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, name, deleted_at, created_at, updated_at;

-- name: CreateOrganizationMembership :one
INSERT INTO scenery_auth_organization_memberships (
  id,
  tenant_id,
  user_id,
  role,
  invited_by_user_id,
  invited_at
)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, tenant_id) WHERE disabled_at IS NULL
DO UPDATE SET role = EXCLUDED.role,
              updated_at = CURRENT_TIMESTAMP
RETURNING id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at;

-- name: GetActiveMembership :one
SELECT id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at
FROM scenery_auth_organization_memberships
WHERE user_id = ?
  AND tenant_id = ?
  AND disabled_at IS NULL;

-- name: ListUserMemberships :many
SELECT
  m.id,
  m.tenant_id,
  m.user_id,
  m.role,
  m.disabled_at,
  m.invited_by_user_id,
  m.invited_at,
  m.created_at,
  m.updated_at,
  t.name AS tenant_name,
  t.deleted_at AS tenant_deleted_at
FROM scenery_auth_organization_memberships AS m
JOIN scenery_auth_tenants AS t ON t.id = m.tenant_id
WHERE m.user_id = ?
  AND m.disabled_at IS NULL
  AND t.deleted_at IS NULL
ORDER BY lower(t.name), t.name, m.tenant_id;

-- name: ListTenantMembers :many
SELECT
  m.id,
  m.tenant_id,
  m.user_id,
  m.role,
  m.disabled_at,
  m.invited_by_user_id,
  m.invited_at,
  m.created_at,
  m.updated_at,
  u.display_name,
  u.primary_email,
  u.avatar_url,
  u.disabled_at AS user_disabled_at
FROM scenery_auth_organization_memberships AS m
JOIN scenery_auth_users AS u ON u.id = m.user_id
WHERE m.tenant_id = ?
ORDER BY lower(u.display_name), lower(u.primary_email), m.created_at;

-- name: CountActiveOwners :one
SELECT count(*)
FROM scenery_auth_organization_memberships
WHERE tenant_id = ?
  AND role = 'owner'
  AND disabled_at IS NULL;

-- name: UpdateMembershipRole :one
UPDATE scenery_auth_organization_memberships
SET role = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE tenant_id = ?
  AND user_id = ?
  AND disabled_at IS NULL
RETURNING id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at;

-- name: DisableMembership :one
UPDATE scenery_auth_organization_memberships
SET disabled_at = COALESCE(disabled_at, CURRENT_TIMESTAMP),
    updated_at = CURRENT_TIMESTAMP
WHERE tenant_id = ?
  AND user_id = ?
RETURNING id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at;

-- name: CreateRefreshSession :one
INSERT INTO scenery_auth_refresh_sessions (
  id,
  user_id,
  token_hash,
  active_tenant_id,
  expires_at,
  user_agent,
  ip_hash,
  actor_user_id,
  impersonation_id,
  impersonation_reason
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at;

-- name: GetRefreshSessionByID :one
SELECT id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at
FROM scenery_auth_refresh_sessions
WHERE id = ?;

-- name: RotateRefreshSession :one
UPDATE scenery_auth_refresh_sessions
SET previous_token_hash = token_hash,
    previous_token_expires_at = datetime(CURRENT_TIMESTAMP, '+' || (sqlc.arg(grace_ms) / 1000) || ' seconds'),
    token_hash = sqlc.arg(token_hash),
    rotated_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at;

-- name: SetRefreshSessionTenant :one
UPDATE scenery_auth_refresh_sessions
SET active_tenant_id = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND revoked_at IS NULL
RETURNING id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at;

-- name: RevokeRefreshSession :exec
UPDATE scenery_auth_refresh_sessions
SET revoked_at = COALESCE(revoked_at, CURRENT_TIMESTAMP),
    revoked_reason = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: RevokeUserRefreshSessions :exec
UPDATE scenery_auth_refresh_sessions
SET revoked_at = COALESCE(revoked_at, CURRENT_TIMESTAMP),
    revoked_reason = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE user_id = ?
  AND revoked_at IS NULL;

-- name: CreateOneTimeToken :one
INSERT INTO scenery_auth_one_time_tokens (
  id,
  purpose,
  token_hash,
  user_id,
  tenant_id,
  email,
  normalized_email,
  metadata,
  expires_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, purpose, token_hash, user_id, tenant_id, email, normalized_email, metadata, expires_at, consumed_at, created_at;

-- name: ConsumeOneTimeToken :one
UPDATE scenery_auth_one_time_tokens
SET consumed_at = CURRENT_TIMESTAMP
WHERE token_hash = ?
  AND purpose = ?
  AND consumed_at IS NULL
  AND expires_at > CURRENT_TIMESTAMP
RETURNING id, purpose, token_hash, user_id, tenant_id, email, normalized_email, metadata, expires_at, consumed_at, created_at;

-- name: CreateOAuthState :one
INSERT INTO scenery_auth_oauth_states (
  id,
  state_hash,
  pkce_verifier,
  nonce_hash,
  redirect_path,
  expires_at
)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, state_hash, pkce_verifier, nonce_hash, redirect_path, expires_at, consumed_at, created_at;

-- name: ConsumeOAuthState :one
UPDATE scenery_auth_oauth_states
SET consumed_at = CURRENT_TIMESTAMP
WHERE state_hash = ?
  AND consumed_at IS NULL
  AND expires_at > CURRENT_TIMESTAMP
RETURNING id, state_hash, pkce_verifier, nonce_hash, redirect_path, expires_at, consumed_at, created_at;

-- name: UpsertAuthAttempt :one
INSERT INTO scenery_auth_auth_attempts (id, purpose, normalized_email, ip_hash, attempt_count)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT (purpose, normalized_email, ip_hash)
DO UPDATE SET attempt_count = CASE
                WHEN scenery_auth_auth_attempts.window_started_at < datetime(CURRENT_TIMESTAMP, '-15 minutes') THEN 1
                ELSE scenery_auth_auth_attempts.attempt_count + 1
              END,
              window_started_at = CASE
                WHEN scenery_auth_auth_attempts.window_started_at < datetime(CURRENT_TIMESTAMP, '-15 minutes') THEN CURRENT_TIMESTAMP
                ELSE scenery_auth_auth_attempts.window_started_at
              END,
              last_attempt_at = CURRENT_TIMESTAMP
RETURNING id, purpose, normalized_email, ip_hash, window_started_at, attempt_count, last_attempt_at;

-- name: CreateAuthEvent :exec
INSERT INTO scenery_auth_auth_events (
  id,
  event_type,
  user_id,
  actor_user_id,
  tenant_id,
  session_id,
  ip_hash,
  user_agent,
  metadata
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
