# Standard Auth Production Migration Runbook

This runbook preserves existing app-owned auth data when moving a production app to scenery standard auth.

It is intentionally operator-driven. Do not hide this migration inside app startup: existing users, tenants, memberships, password hashes, refresh sessions, and pending invite/reset tokens are production state and should be copied, counted, verified, and backed up explicitly.

## When To Use This

Use this runbook before enabling this config for a production database that already has app-owned auth tables:

```json
{
  "auth": {
    "enabled": true,
    "auto_bootstrap_database": true
  }
}
```

Fresh local/dev databases do not need this. They can let scenery bootstrap the standard-auth tables and then create users normally.

## Target Schema

scenery standard auth owns managed Postgres tables in the `scenery` schema with the `scenery_auth_` prefix:

```text
scenery_auth_tenants
scenery_auth_users
scenery_auth_auth_identities
scenery_auth_organization_memberships
scenery_auth_refresh_sessions
scenery_auth_one_time_tokens
scenery_auth_oauth_states
scenery_auth_auth_attempts
scenery_auth_auth_events
```

Preserve:

- `tenants`
- `users`
- `auth_identities`, especially email password hashes and Google identities
- `organization_memberships`
- active and recently rotated `refresh_sessions` if you want existing browser sessions to survive
- unexpired `one_time_tokens` for email verification, password reset, and invites if the legacy app has equivalent data

Usually do not preserve:

- `oauth_states`, because they are short lived and users can restart OAuth
- `auth_attempts`, because rate-limit windows are intentionally transient
- expired or consumed one-time tokens
- revoked or expired refresh sessions unless audit requirements say otherwise

## Preconditions

1. Freeze auth writes or put the app in maintenance mode.
2. Take a database backup.
3. Deploy a Scenery build that contains standard auth support, but do not route production traffic to it yet.
4. Use the same JWT secret and refresh cookie name that the old app used if existing access/refresh sessions must remain valid.
5. Bootstrap the target schema on a copy of production first:

   ```sh
   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f auth/db/gen/schema.sql
   ```

6. Confirm the target schema exists:

   ```sql
   select table_schema, table_name
   from information_schema.tables
   where table_schema = 'scenery'
     and table_name like 'scenery_auth_%'
   order by table_name;
   ```

## Staging Views

Create staging views that normalize the old app schema into the target shape. If your legacy tables already have these names and columns, the views can be simple `select *` projections. If names differ, adapt only this staging section and keep the copy SQL unchanged.

```sql
create schema if not exists auth_migration_legacy;
```

Required staging views:

```text
auth_migration_legacy.tenants
  id uuid
  name text
  deleted_at timestamptz
  created_at timestamptz
  updated_at timestamptz

auth_migration_legacy.users
  id uuid
  display_name text
  avatar_url text
  primary_email text
  normalized_primary_email text
  email_verified_at timestamptz
  disabled_at timestamptz
  can_impersonate_users boolean
  created_at timestamptz
  updated_at timestamptz

auth_migration_legacy.auth_identities
  id uuid
  user_id uuid
  provider text
  provider_subject text
  email text
  normalized_email text
  password_hash text
  created_at timestamptz
  updated_at timestamptz

auth_migration_legacy.organization_memberships
  id uuid
  tenant_id uuid
  user_id uuid
  role text
  disabled_at timestamptz
  invited_by_user_id uuid
  invited_at timestamptz
  created_at timestamptz
  updated_at timestamptz

auth_migration_legacy.refresh_sessions
  id uuid
  user_id uuid
  token_hash text
  previous_token_hash text
  previous_token_expires_at timestamptz
  active_tenant_id uuid
  expires_at timestamptz
  rotated_at timestamptz
  revoked_at timestamptz
  revoked_reason text
  user_agent text
  ip_hash text
  actor_user_id uuid
  impersonation_id uuid
  impersonation_reason text
  created_at timestamptz
  updated_at timestamptz

auth_migration_legacy.one_time_tokens
  id uuid
  purpose text
  token_hash text
  user_id uuid
  tenant_id uuid
  email text
  normalized_email text
  metadata jsonb
  expires_at timestamptz
  consumed_at timestamptz
  created_at timestamptz
```

Example view shape:

```sql
create or replace view auth_migration_legacy.users as
select
  id,
  coalesce(display_name, '') as display_name,
  coalesce(avatar_url, '') as avatar_url,
  coalesce(primary_email, email, '') as primary_email,
  coalesce(normalized_primary_email, lower(email), '') as normalized_primary_email,
  email_verified_at,
  disabled_at,
  coalesce(can_impersonate_users, false) as can_impersonate_users,
  coalesce(created_at, now()) as created_at,
  coalesce(updated_at, now()) as updated_at
from legacy_users.users;
```

## Dry-Run Checks

Run these checks on the staging views before copying:

```sql
select 'users' as table_name, count(*) from auth_migration_legacy.users
union all select 'tenants', count(*) from auth_migration_legacy.tenants
union all select 'auth_identities', count(*) from auth_migration_legacy.auth_identities
union all select 'organization_memberships', count(*) from auth_migration_legacy.organization_memberships
union all select 'active_refresh_sessions', count(*) from auth_migration_legacy.refresh_sessions where expires_at > now() and revoked_at is null
union all select 'unexpired_one_time_tokens', count(*) from auth_migration_legacy.one_time_tokens where expires_at > now() and consumed_at is null;
```

```sql
select normalized_primary_email, count(*)
from auth_migration_legacy.users
where normalized_primary_email <> ''
group by normalized_primary_email
having count(*) > 1;

select provider, provider_subject, count(*)
from auth_migration_legacy.auth_identities
group by provider, provider_subject
having count(*) > 1;

select user_id, tenant_id, count(*)
from auth_migration_legacy.organization_memberships
where disabled_at is null
group by user_id, tenant_id
having count(*) > 1;
```

All duplicate queries must return zero rows before migration.

## Copy Data

Run the copy in one transaction. Keep the order: tenants, users, identities, memberships, refresh sessions, one-time tokens, audit events.

```sql
begin;

insert into scenery.scenery_auth_tenants (id, name, deleted_at, created_at, updated_at)
select id, name, deleted_at, created_at, updated_at
from auth_migration_legacy.tenants
on conflict (id) do update set
  name = excluded.name,
  deleted_at = excluded.deleted_at,
  updated_at = greatest(scenery.scenery_auth_tenants.updated_at, excluded.updated_at);

insert into scenery.scenery_auth_users (
  id,
  display_name,
  avatar_url,
  primary_email,
  normalized_primary_email,
  email_verified_at,
  disabled_at,
  can_impersonate_users,
  created_at,
  updated_at
)
select
  id,
  display_name,
  avatar_url,
  primary_email,
  normalized_primary_email,
  email_verified_at,
  disabled_at,
  can_impersonate_users,
  created_at,
  updated_at
from auth_migration_legacy.users
on conflict (id) do update set
  display_name = excluded.display_name,
  avatar_url = excluded.avatar_url,
  primary_email = excluded.primary_email,
  normalized_primary_email = excluded.normalized_primary_email,
  email_verified_at = excluded.email_verified_at,
  disabled_at = excluded.disabled_at,
  can_impersonate_users = excluded.can_impersonate_users,
  updated_at = greatest(scenery.scenery_auth_users.updated_at, excluded.updated_at);

insert into scenery.scenery_auth_auth_identities (
  id,
  user_id,
  provider,
  provider_subject,
  email,
  normalized_email,
  password_hash,
  created_at,
  updated_at
)
select
  id,
  user_id,
  provider,
  provider_subject,
  email,
  normalized_email,
  password_hash,
  created_at,
  updated_at
from auth_migration_legacy.auth_identities
on conflict (provider, provider_subject) do update set
  user_id = excluded.user_id,
  email = excluded.email,
  normalized_email = excluded.normalized_email,
  password_hash = excluded.password_hash,
  updated_at = greatest(scenery.scenery_auth_auth_identities.updated_at, excluded.updated_at);

insert into scenery.scenery_auth_organization_memberships (
  id,
  tenant_id,
  user_id,
  role,
  disabled_at,
  invited_by_user_id,
  invited_at,
  created_at,
  updated_at
)
select
  id,
  tenant_id,
  user_id,
  case when role in ('owner', 'member') then role else 'member' end,
  disabled_at,
  invited_by_user_id,
  invited_at,
  created_at,
  updated_at
from auth_migration_legacy.organization_memberships
on conflict (id) do update set
  tenant_id = excluded.tenant_id,
  user_id = excluded.user_id,
  role = excluded.role,
  disabled_at = excluded.disabled_at,
  invited_by_user_id = excluded.invited_by_user_id,
  invited_at = excluded.invited_at,
  updated_at = greatest(scenery.scenery_auth_organization_memberships.updated_at, excluded.updated_at);

insert into scenery.scenery_auth_refresh_sessions (
  id,
  user_id,
  token_hash,
  previous_token_hash,
  previous_token_expires_at,
  active_tenant_id,
  expires_at,
  rotated_at,
  revoked_at,
  revoked_reason,
  user_agent,
  ip_hash,
  actor_user_id,
  impersonation_id,
  impersonation_reason,
  created_at,
  updated_at
)
select
  id,
  user_id,
  token_hash,
  coalesce(previous_token_hash, ''),
  previous_token_expires_at,
  active_tenant_id,
  expires_at,
  rotated_at,
  revoked_at,
  coalesce(revoked_reason, ''),
  coalesce(user_agent, ''),
  coalesce(ip_hash, ''),
  actor_user_id,
  impersonation_id,
  coalesce(impersonation_reason, ''),
  created_at,
  updated_at
from auth_migration_legacy.refresh_sessions
where expires_at > now()
on conflict (id) do update set
  token_hash = excluded.token_hash,
  previous_token_hash = excluded.previous_token_hash,
  previous_token_expires_at = excluded.previous_token_expires_at,
  active_tenant_id = excluded.active_tenant_id,
  expires_at = excluded.expires_at,
  rotated_at = excluded.rotated_at,
  revoked_at = excluded.revoked_at,
  revoked_reason = excluded.revoked_reason,
  updated_at = greatest(scenery.scenery_auth_refresh_sessions.updated_at, excluded.updated_at);

insert into scenery.scenery_auth_one_time_tokens (
  id,
  purpose,
  token_hash,
  user_id,
  tenant_id,
  email,
  normalized_email,
  metadata,
  expires_at,
  consumed_at,
  created_at
)
select
  id,
  purpose,
  token_hash,
  user_id,
  tenant_id,
  coalesce(email, ''),
  coalesce(normalized_email, ''),
  coalesce(metadata, '{}'::jsonb),
  expires_at,
  consumed_at,
  created_at
from auth_migration_legacy.one_time_tokens
where expires_at > now()
on conflict (token_hash) do nothing;

insert into scenery.scenery_auth_auth_events (
  id,
  event_type,
  user_id,
  actor_user_id,
  tenant_id,
  session_id,
  ip_hash,
  user_agent,
  metadata,
  created_at
)
select
  gen_random_uuid(),
  'legacy_migration',
  null,
  null,
  null,
  null,
  '',
  '',
  jsonb_build_object(
    'source', 'legacy auth migration',
    'migrated_at', now()
  ),
  now();

commit;
```

If `gen_random_uuid()` is not available, enable `pgcrypto` for the migration database or replace that one ID with a generated UUID from the operator shell.

## Verification

Compare source and target counts:

```sql
select 'users' as table_name,
  (select count(*) from auth_migration_legacy.users) as source_count,
  (select count(*) from scenery.scenery_auth_users) as target_count
union all select 'tenants',
  (select count(*) from auth_migration_legacy.tenants),
  (select count(*) from scenery.scenery_auth_tenants)
union all select 'auth_identities',
  (select count(*) from auth_migration_legacy.auth_identities),
  (select count(*) from scenery.scenery_auth_auth_identities)
union all select 'organization_memberships',
  (select count(*) from auth_migration_legacy.organization_memberships),
  (select count(*) from scenery.scenery_auth_organization_memberships);
```

Validate foreign keys and active membership invariants:

```sql
select count(*) as identities_without_users
from scenery.scenery_auth_auth_identities i
left join scenery.scenery_auth_users u on u.id = i.user_id
where u.id is null;

select count(*) as memberships_without_users
from scenery.scenery_auth_organization_memberships m
left join scenery.scenery_auth_users u on u.id = m.user_id
where u.id is null;

select count(*) as memberships_without_tenants
from scenery.scenery_auth_organization_memberships m
left join scenery.scenery_auth_tenants t on t.id = m.tenant_id
where t.id is null;
```

All three counts must be `0`.

Then smoke test against the new app build:

```sh
scenery check -o json
scenery inspect routes -o json
```

In staging, verify:

- login with an existing email/password user
- refresh with an existing browser refresh cookie if sessions are preserved
- `/auth/me`
- organization list and switch
- owner-only organization member mutation
- impersonation, if enabled
- Google OAuth sign-in, if enabled

## Cutover

1. Keep auth writes frozen.
2. Run the staging-view copy against production.
3. Run verification SQL.
4. Deploy the scenery-standard-auth app build.
5. Route traffic to the new build.
6. Watch auth errors, refresh errors, and organization membership errors.
7. Keep legacy tables read-only until you have completed at least one normal refresh-session TTL window.

## Rollback

If verification fails before cutover, roll back the transaction and keep the old app running.

If cutover fails after traffic moves:

1. Route traffic back to the old app.
2. Keep the `scenery.scenery_auth_*` tables for investigation; do not drop them immediately.
3. Compare `scenery.scenery_auth_auth_events` and app logs to find the failing surface.
4. Fix staging views or config, then rerun the migration from a restored copy or after truncating only the `scenery.scenery_auth_*` tables in dependency order.

## Notes

- Password hashes are copied as opaque strings. scenery verifies Argon2id hashes and can upgrade hash parameters on successful login.
- Refresh-session preservation only works when the new app can parse the same refresh cookie shape and uses the same refresh cookie name.
- Access JWTs only survive cutover if the new app uses the same JWT secret and compatible claims. If not, users will need refresh or login.
- Directly editing this data with SQL is production-sensitive. Use explicit SQL, backups, and verification queries.
