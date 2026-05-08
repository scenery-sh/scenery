-- name: CreateUser :one
INSERT INTO onlava_auth.users (
  id,
  display_name,
  avatar_url,
  primary_email,
  normalized_primary_email,
  email_verified_at
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at
FROM onlava_auth.users
WHERE id = $1;

-- name: GetUserByNormalizedEmail :one
SELECT id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at
FROM onlava_auth.users
WHERE normalized_primary_email = $1;

-- name: MarkUserEmailVerified :one
UPDATE onlava_auth.users
SET email_verified_at = COALESCE(email_verified_at, now()),
    updated_at = now()
WHERE id = $1
RETURNING id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at;

-- name: UpdateUserProfileFromProvider :one
UPDATE onlava_auth.users
SET display_name = CASE WHEN $2::text <> '' THEN $2 ELSE display_name END,
    avatar_url = CASE WHEN $3::text <> '' THEN $3 ELSE avatar_url END,
    updated_at = now()
WHERE id = $1
RETURNING id, display_name, avatar_url, primary_email, normalized_primary_email, email_verified_at, disabled_at, can_impersonate_users, created_at, updated_at;

-- name: CreateAuthIdentity :one
INSERT INTO onlava_auth.auth_identities (
  id,
  user_id,
  provider,
  provider_subject,
  email,
  normalized_email,
  password_hash
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at;

-- name: GetAuthIdentityByProviderSubject :one
SELECT id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at
FROM onlava_auth.auth_identities
WHERE provider = $1
  AND provider_subject = $2;

-- name: GetEmailIdentityForLogin :one
SELECT id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at
FROM onlava_auth.auth_identities
WHERE provider = 'email'
  AND provider_subject = $1;

-- name: UpdateIdentityPasswordHash :one
UPDATE onlava_auth.auth_identities
SET password_hash = $2,
    updated_at = now()
WHERE id = $1
RETURNING id, user_id, provider, provider_subject, email, normalized_email, password_hash, created_at, updated_at;

-- name: CreateTenant :one
INSERT INTO onlava_auth.tenants (id, name)
VALUES ($1, $2)
RETURNING id, name, deleted_at, created_at, updated_at;

-- name: GetTenantByID :one
SELECT id, name, deleted_at, created_at, updated_at
FROM onlava_auth.tenants
WHERE id = $1;

-- name: UpdateTenantName :one
UPDATE onlava_auth.tenants
SET name = $2,
    updated_at = now()
WHERE id = $1
  AND deleted_at IS NULL
RETURNING id, name, deleted_at, created_at, updated_at;

-- name: SoftDeleteTenant :one
UPDATE onlava_auth.tenants
SET deleted_at = COALESCE(deleted_at, now()),
    updated_at = now()
WHERE id = $1
RETURNING id, name, deleted_at, created_at, updated_at;

-- name: CreateOrganizationMembership :one
INSERT INTO onlava_auth.organization_memberships (
  id,
  tenant_id,
  user_id,
  role,
  invited_by_user_id,
  invited_at
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, tenant_id) WHERE disabled_at IS NULL
DO UPDATE SET role = EXCLUDED.role,
              updated_at = now()
RETURNING id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at;

-- name: GetActiveMembership :one
SELECT id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at
FROM onlava_auth.organization_memberships
WHERE user_id = $1
  AND tenant_id = $2
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
FROM onlava_auth.organization_memberships AS m
JOIN onlava_auth.tenants AS t ON t.id = m.tenant_id
WHERE m.user_id = $1
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
FROM onlava_auth.organization_memberships AS m
JOIN onlava_auth.users AS u ON u.id = m.user_id
WHERE m.tenant_id = $1
ORDER BY lower(u.display_name), lower(u.primary_email), m.created_at;

-- name: CountActiveOwners :one
SELECT count(*)::int
FROM onlava_auth.organization_memberships
WHERE tenant_id = $1
  AND role = 'owner'
  AND disabled_at IS NULL;

-- name: UpdateMembershipRole :one
UPDATE onlava_auth.organization_memberships
SET role = $3,
    updated_at = now()
WHERE tenant_id = $1
  AND user_id = $2
  AND disabled_at IS NULL
RETURNING id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at;

-- name: DisableMembership :one
UPDATE onlava_auth.organization_memberships
SET disabled_at = COALESCE(disabled_at, now()),
    updated_at = now()
WHERE tenant_id = $1
  AND user_id = $2
RETURNING id, tenant_id, user_id, role, disabled_at, invited_by_user_id, invited_at, created_at, updated_at;

-- name: CreateRefreshSession :one
INSERT INTO onlava_auth.refresh_sessions (
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
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at;

-- name: GetRefreshSessionByID :one
SELECT id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at
FROM onlava_auth.refresh_sessions
WHERE id = $1;

-- name: RotateRefreshSession :one
UPDATE onlava_auth.refresh_sessions
SET previous_token_hash = token_hash,
    previous_token_expires_at = now() + ($3::bigint * interval '1 millisecond'),
    token_hash = $2,
    rotated_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at;

-- name: SetRefreshSessionTenant :one
UPDATE onlava_auth.refresh_sessions
SET active_tenant_id = $2,
    updated_at = now()
WHERE id = $1
  AND revoked_at IS NULL
RETURNING id, user_id, token_hash, previous_token_hash, previous_token_expires_at, active_tenant_id, expires_at, rotated_at, revoked_at, revoked_reason, user_agent, ip_hash, actor_user_id, impersonation_id, impersonation_reason, created_at, updated_at;

-- name: RevokeRefreshSession :exec
UPDATE onlava_auth.refresh_sessions
SET revoked_at = COALESCE(revoked_at, now()),
    revoked_reason = $2,
    updated_at = now()
WHERE id = $1;

-- name: RevokeUserRefreshSessions :exec
UPDATE onlava_auth.refresh_sessions
SET revoked_at = COALESCE(revoked_at, now()),
    revoked_reason = $2,
    updated_at = now()
WHERE user_id = $1
  AND revoked_at IS NULL;

-- name: CreateOneTimeToken :one
INSERT INTO onlava_auth.one_time_tokens (
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
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, purpose, token_hash, user_id, tenant_id, email, normalized_email, metadata, expires_at, consumed_at, created_at;

-- name: ConsumeOneTimeToken :one
UPDATE onlava_auth.one_time_tokens
SET consumed_at = now()
WHERE token_hash = $1
  AND purpose = $2
  AND consumed_at IS NULL
  AND expires_at > now()
RETURNING id, purpose, token_hash, user_id, tenant_id, email, normalized_email, metadata, expires_at, consumed_at, created_at;

-- name: CreateOAuthState :one
INSERT INTO onlava_auth.oauth_states (
  id,
  state_hash,
  pkce_verifier,
  nonce_hash,
  redirect_path,
  expires_at
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, state_hash, pkce_verifier, nonce_hash, redirect_path, expires_at, consumed_at, created_at;

-- name: ConsumeOAuthState :one
UPDATE onlava_auth.oauth_states
SET consumed_at = now()
WHERE state_hash = $1
  AND consumed_at IS NULL
  AND expires_at > now()
RETURNING id, state_hash, pkce_verifier, nonce_hash, redirect_path, expires_at, consumed_at, created_at;

-- name: UpsertAuthAttempt :one
INSERT INTO onlava_auth.auth_attempts (id, purpose, normalized_email, ip_hash, attempt_count)
VALUES ($1, $2, $3, $4, 1)
ON CONFLICT (purpose, normalized_email, ip_hash)
DO UPDATE SET attempt_count = CASE
                WHEN onlava_auth.auth_attempts.window_started_at < now() - interval '15 minutes' THEN 1
                ELSE onlava_auth.auth_attempts.attempt_count + 1
              END,
              window_started_at = CASE
                WHEN onlava_auth.auth_attempts.window_started_at < now() - interval '15 minutes' THEN now()
                ELSE onlava_auth.auth_attempts.window_started_at
              END,
              last_attempt_at = now()
RETURNING id, purpose, normalized_email, ip_hash, window_started_at, attempt_count, last_attempt_at;

-- name: CreateAuthEvent :exec
INSERT INTO onlava_auth.auth_events (
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
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);
