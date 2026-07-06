# scenery Standard Auth

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

scenery currently provides only framework-level authentication plumbing: it parses `//scenery:authhandler`, decodes auth params from requests, invokes the app-provided handler, and exposes the resulting request auth through `scenery.sh/auth.UserID()` and `scenery.sh/auth.Data()`. The actual product auth used by ONLV lives in the adjacent ONLV app repository: JWT signing and validation, refresh sessions, email/password signup and login, Google OAuth, organizations, invitations, impersonation, dev bootstrap tokens, HCL schema, and sqlc queries.

The goal of this plan is to move that auth implementation into scenery as a standard scenery auth module. ONLV should consume the top-level scenery surface and stop owning auth business logic or auth database code. After this plan, ONLV should not maintain its own `auth` service package, `users.DevBootstrap`, auth HCL schema, auth sqlc queries, generated auth DB package, password hashing, JWT parsing, refresh-session rotation, OAuth state storage, or organization/invite/impersonation logic.

The target architecture is:

```text
ONLV .scenery.json / minimal app config
        |
        v
scenery build/codegen standard auth module
        |
        v
scenery.sh/auth public surface
        |
        v
scenery-owned auth service implementation
        |
        v
auth/db/schema.hcl + auth/db/queries.sql + auth/db/gen via sqlc
        |
        v
PostgreSQL schema owned by scenery auth
```

The user-visible behavior should remain stable for ONLV: existing frontend calls such as `/auth/signup/email`, `/auth/login/email`, `/auth/refresh`, `/auth/logout`, `/auth/me`, organization endpoints, Google OAuth endpoints, impersonation endpoints, and local `/users/dev-bootstrap` should continue to work or be intentionally renamed with an explicit migration. The preferred outcome is no frontend route churn except regenerated TypeScript client types if names or payload structs change.

## Progress

- [x] (2026-05-08 20:56Z) Created this ExecPlan and assigned historical ID 0006.
- [x] (2026-05-08 20:56Z) Audited the split between scenery framework auth and ONLV product auth. scenery currently has `auth/auth.go`, `runtime/server.go`, `runtime/registry.go`, parser/codegen support for app auth handlers, and no HCL/sqlc auth schema. ONLV currently has auth implementation under `auth/`, dev bootstrap under `users/dev_auth.go`, schema under `users/db/schema.hcl`, queries under `auth/db/queries.sql`, and generated sqlc packages under `auth/db/gen` and `users/db/gen`.
- [x] (2026-05-08 20:56Z) Confirmed ONLV uses Atlas HCL plus sqlc through `atlas.hcl`, `sqlc.yaml`, and `just gen-sqlc` / `just sqlc-schema`. scenery does not yet have equivalent Atlas/sqlc repo tooling.
- [x] (2026-05-08 21:39Z) Implemented scenery auth DB schema/tooling: `auth/db/schema.hcl`, `auth/db/queries.sql`, `auth/db/gen/*`, `atlas.hcl`, `sqlc.yaml`, and `scripts/gen-auth-sqlc.sh`. The schema uses a single scenery-owned `scenery_auth` PostgreSQL schema and generated `authdb` package.
- [x] (2026-05-08 21:39Z) Moved and generalized the ONLV auth service into `scenery.sh/auth`: JWT validation, password hashing, email signup/login, refresh sessions, organizations, invites, impersonation, Google OAuth, auth events, and local dev bootstrap. The concrete request auth payload is `auth.AuthData`; `auth.CurrentAuthData()` avoids app-side type assertions.
- [x] (2026-05-08 21:39Z) Added standard auth module support to `.scenery.json`, generated app startup, inspect output, and TypeScript client generation. Apps can enable standard auth with `"auth": {"enabled": true}` and do not need wrapper endpoints.
- [x] (2026-05-08 21:39Z) Added `testdata/apps/standard-auth` and an integration test proving built-in `/users/dev-bootstrap` mints a token accepted by a normal `//scenery:api auth` endpoint.
- [x] (2026-05-08 21:51Z) Updated ONLV to enable scenery standard auth from `.scenery.json`, switched app services from `clean.tech/auth` to `scenery.sh/auth`, removed ONLV-owned auth service files, removed `users.DevBootstrap`, removed auth sqlc generation, and regenerated the app TypeScript client.
- [x] (2026-05-08 21:51Z) Validated the ONLV cutover with `go test ./...`, `scenery check --json`, `scenery inspect routes --json`, `scenery harness --json --write`, `bun run build`, `bun run build:electron`, and a runtime smoke test of `/users/dev-bootstrap`.
- [x] (2026-05-08 22:45Z) Added `docs/runbooks/standard-auth-migration.md` for copying existing `users` / `tenants` auth state into `scenery_auth` before any production rollout that must preserve existing users and sessions.

## Surprises & Discoveries

- scenery already has a public package named `auth` with functions `UserID()` and `Data()`. Go package identifiers share one namespace, so the moved auth package cannot also define `type UserID` or `type Data`. The new concrete auth payload type should use a distinct name such as `AuthData`, while preserving `auth.UserID()` and `auth.Data()` for framework compatibility.
- ONLV auth is not isolated to the `auth/` directory. Other ONLV services import `clean.tech/auth` for request auth data and generated auth DB types. Examples include `contacts`, `tasks`, `invoices`, `tenants`, `console`, `sync`, and `pkg/db/audit_context.go`.
- ONLV auth email delivery imports `clean.tech/mail`. scenery cannot import ONLV mail. Auth email delivery must become a Scenery-level interface, event hook, SMTP/webhook option, or a small ONLV adapter that contains no auth business logic.
- ONLV uses schema names `users` and `tenants`. A standard scenery module should not bake ONLV-specific schema names into a public framework. The scenery-owned schema should use a Scenery name, for example `scenery_auth`, and ONLV needs an explicit data migration.
- ONLV currently uses `github.com/golang-jwt/jwt/v5` directly. scenery does not have that dependency. Adding it as a direct scenery dependency is justified if the standard auth module owns JWT signing and validation.
- scenery currently has no `justfile`, `atlas.hcl`, or `sqlc.yaml`. Adding HCL/sqlc "the same way as ONLV" means creating equivalent repo tooling, not only copying generated Go files.
- Atlas `schema inspect --env dev` inspects the configured target database, not the HCL source. For repeatable generation from source HCL in this repo, `scripts/gen-auth-sqlc.sh` uses `atlas schema inspect --url file://auth/db/schema.hcl --dev-url docker://postgres/18/dev`.
- The ONLV auth schema used `uuidv7()` defaults that depend on ONLV database utilities. The scenery auth schema removes those defaults because the standard auth service already generates UUIDs in Go and should not require ONLV helper functions.
- A pure startup-time DB connection would make local dev-token auth impossible without a database. The standard module now connects lazily for DB-backed endpoints, while `/users/dev-bootstrap` and JWT validation can work without opening PostgreSQL. Current contract note: when `dev_bootstrap.default_user_email` is configured, `/users/dev-bootstrap` opens standard auth lazily and creates the configured default tenant, verified user, and owner membership on first use when missing.
- ONLV still has an app-owned `tenants` package for non-auth application configuration and domain table foreign keys. The auth-owned membership and organization endpoints moved to scenery standard auth; the generated client no longer exposes ONLV-local tenant mutation endpoints.
- `bun run typecheck` in `apps/app` is still blocked by the existing `apps/viewer` / duplicate `@types/three` mismatch. `bun run build` and `bun run build:electron` passed after the auth client regeneration.

## Decision Log

- Decision: Implement auth as a Scenery standard module, not as copied ONLV source.
  Rationale: The user wants ONLV to use only the top surface and remove local auth logic. Keeping endpoint wrappers and SQL in ONLV would preserve the maintenance burden this plan is meant to eliminate.
  Date/Author: 2026-05-08 / Codex

- Decision: Keep the public package path `scenery.sh/auth` as the top surface.
  Rationale: That package already represents scenery request auth. Extending it is easier for app authors than introducing a second auth package. Existing `auth.UserID()` and `auth.Data()` must remain usable.
  Date/Author: 2026-05-08 / Codex

- Decision: Use explicit concrete type names such as `AuthData`, `AuthUser`, `AuthOrganization`, and `AuthSession`, not `Data` or `UserID`.
  Rationale: `auth.Data()` and `auth.UserID()` already exist as functions in the same Go package. Reusing those names for types is impossible without breaking the package API.
  Date/Author: 2026-05-08 / Codex

- Decision: Use a Scenery-owned PostgreSQL schema, tentatively `scenery_auth`, for the standard auth module.
  Rationale: scenery should not ship framework auth backed by ONLV-specific schemas named `users` and `tenants`. ONLV should migrate existing data to the scenery-owned schema as part of this work.
  Date/Author: 2026-05-08 / Codex

- Decision: Use Atlas HCL and sqlc in scenery, matching ONLV's style.
  Rationale: The user explicitly requested HCL and sqlc in the same way as ONLV. sqlc keeps database access typed without an ORM, and HCL gives a reviewable schema source of truth.
  Date/Author: 2026-05-08 / Codex

- Decision: Add codegen/build support for the standard auth module instead of requiring each app to manually re-declare every auth endpoint.
  Rationale: Manual wrappers would still leave ONLV owning auth endpoint shape and maintenance. The desired ONLV state is configuration plus top-surface use, not duplicated endpoint declarations.
  Date/Author: 2026-05-08 / Codex

- Decision: Treat email sending as a pluggable integration point.
  Rationale: scenery auth needs to send verification, reset, and invite emails, but it cannot depend on ONLV's `mail` package. A no-op local default plus a configured adapter/hook keeps auth portable.
  Date/Author: 2026-05-08 / Codex

- Decision: Generate auth schema SQL from HCL using `atlas schema inspect --url file://...`, not by inspecting the local dev database.
  Rationale: The repository needs deterministic generated schema artifacts independent of whatever schemas happen to be present in a developer's local PostgreSQL instance.
  Date/Author: 2026-05-08 / Codex

- Decision: Remove `uuidv7()` defaults from the standard auth schema.
  Rationale: scenery standard auth should not depend on an app-owned database utility function. The service passes UUIDs explicitly to all inserted auth rows.
  Date/Author: 2026-05-08 / Codex

- Decision: Make the standard auth database pool lazy.
  Rationale: Dev bootstrap and JWT validation are useful in local fixtures without a database. DB-backed endpoints still fail clearly if no database URL is configured.
  Date/Author: 2026-05-08 / Codex

- Decision: Remove ONLV-local tenant mutation endpoints during the auth cutover.
  Rationale: Organization creation, switching, member management, and tenant-scoped auth state are now owned by scenery standard auth. Keeping parallel ONLV tenant mutation endpoints would keep membership logic and auth DB coupling in the app.
  Date/Author: 2026-05-08 / Codex

- Decision: Defer existing-production-user data migration to an explicit rollout task instead of hiding it inside the code cutover.
  Rationale: Local dev and fresh DB usage can rely on `auth.auto_bootstrap_database`, but preserving existing users/sessions requires an operator-visible copy/verify step from legacy `users` and `tenants` schemas to `scenery_auth`.
  Date/Author: 2026-05-08 / Codex

## Outcomes & Retrospective

The scenery standard-auth module is implemented and ONLV now consumes it through the scenery top surface. ONLV no longer declares its own `auth` service, `//scenery:authhandler`, `/users/dev-bootstrap`, auth sqlc queries, generated auth DB package, password hashing, JWT parsing, refresh-session rotation, OAuth state storage, or organization/invite/impersonation endpoint logic.

Validation completed:

- `go test ./...` in `/path/to/scenery`
- `go install ./cmd/scenery` in `/path/to/scenery`
- `scenery harness self --json --write` in `/path/to/scenery`
- `scenery check --app-root testdata/apps/standard-auth --json`
- `scenery gen client --app-root testdata/apps/standard-auth --lang typescript`
- `go test ./...` in `/Users/petrbrazdil/Repos/onlv`
- `scenery check --json --app-root /Users/petrbrazdil/Repos/onlv`
- `scenery inspect routes --json --app-root /Users/petrbrazdil/Repos/onlv`
- `scenery harness --json --write --app-root /Users/petrbrazdil/Repos/onlv`
- `bun run build` and `bun run build:electron` in `/Users/petrbrazdil/Repos/onlv/apps/app`
- Runtime smoke test: ONLV `scenery run` served standard `users.DevBootstrap`; an authenticated request entered the console handler with `uid=dev-user`.

Remaining rollout work: production deployments that need existing user/session preservation must execute and verify `docs/runbooks/standard-auth-migration.md` before removing legacy tables from any production database. The code path no longer depends on the legacy ONLV auth package.

## Context and Orientation

Primary repository: `scenery.sh`, local path `/path/to/scenery`.

Consumer repository for migration: ONLV app, local path `/Users/petrbrazdil/Repos/onlv`. ONLV uses `replace scenery.sh => ../scenery`, so it can validate local scenery changes before publishing.

Relevant scenery files:

- `auth/auth.go`: current public auth context helpers.
- `runtime/server.go`: request authentication, auth handler invocation, auth param decoding.
- `runtime/registry.go`: endpoint and auth handler registration.
- `internal/parse/parser.go`: parses `//scenery:authhandler`.
- `internal/codegen/generator.go`: emits endpoint and auth handler registration.
- `internal/clientgen/typescript*.go`: emits TypeScript client auth header behavior and endpoint client methods.
- `internal/inspect/inspect.go` and `internal/devmeta/meta.go`: app metadata and inspect output.
- `pgxpool/pgxpool.go`: scenery-instrumented pgx pool wrapper.
- `runtime/secrets.go`: `.env` and environment secret loading.
- `docs/local-contract.md`, `ARCHITECTURE.md`, `SKILL.md`, `AGENTS.md`: public documentation surfaces to update if auth becomes stable scenery behavior.

Relevant ONLV files to move or remove:

- `auth/auth.go`: JWT validation and `//scenery:authhandler`.
- `auth/service.go`, `auth/sessions.go`, `auth/organizations.go`, `auth/impersonation.go`, `auth/google.go`, `auth/passwords.go`, `auth/tokens.go`, `auth/mail.go`, `auth/config.go`, `auth/helpers.go`.
- `auth/db/queries.sql`, `auth/db/gen/*`.
- `users/dev_auth.go`.
- `users/db/schema.hcl`, `users/db/queries.sql`, `users/db/gen/*`, if no remaining ONLV-owned users service needs them.
- `atlas.hcl`, `sqlc.yaml`, `justfile`: remove auth/users entries after scenery owns auth schema generation.
- `apps/app/src/auth/*`, `apps/app/src/app-client.ts`: update only as required by generated client or cookie/name changes.
- ONLV service imports of `clean.tech/auth` in `contacts`, `tasks`, `invoices`, `tenants`, `console`, `sync`, and `pkg/db/audit_context.go`.

Current ONLV auth endpoint inventory:

- `//scenery:authhandler` `auth.AuthHandler`
- `POST /users/dev-bootstrap`
- `POST /auth/signup/email`
- `POST /auth/login/email`
- `POST /auth/refresh`
- `POST /auth/logout`
- `GET /auth/me`
- `POST /auth/email-verification/confirm`
- `POST /auth/email-verification/resend`
- `POST /auth/password-reset/request`
- `POST /auth/password-reset/confirm`
- `GET /auth/google/start` raw
- `GET /auth/google/callback` raw
- `GET /auth/organizations`
- `POST /auth/organizations`
- `POST /auth/organizations/switch`
- `PATCH /auth/organizations/:tenantID`
- `DELETE /auth/organizations/:tenantID`
- `GET /auth/organizations/:tenantID/members`
- `POST /auth/organizations/:tenantID/invites`
- `POST /auth/invites/accept`
- `PATCH /auth/organizations/:tenantID/members/:userID`
- `POST /auth/organizations/:tenantID/members/disable`
- `POST /auth/impersonation/start`
- `POST /auth/impersonation/stop`

## Milestones

Milestone 1: Define the stable scenery auth contract.

Inventory ONLV auth behavior, request and response payloads, frontend expectations, DB schema, and service imports. Decide which endpoint paths are preserved exactly, which public Go types are exposed from `scenery.sh/auth`, and how ONLV config enables the standard auth module. The output is a written contract in this ExecPlan plus updated docs when implemented.

Milestone 2: Add scenery HCL/sqlc infrastructure for auth.

Create `auth/db/schema.hcl`, `auth/db/queries.sql`, generated `auth/db/gen/*`, `sqlc.yaml`, `atlas.hcl`, and small scripts or documented commands equivalent to ONLV's `just sqlc-schema` and `just gen-sqlc`. The schema source of truth is HCL. Generated schema SQL and generated sqlc Go code are tracked if that matches the repository's policy.

Milestone 3: Move auth logic into scenery.

Port ONLV auth logic into scenery with app-specific names removed. Keep behavior but generalize brand text, cookie names, redirect URLs, OAuth options, email sender behavior, rate limits, token TTLs, and local-dev gating. Use `auth.AuthData` or equivalent as the concrete request auth payload returned by the standard auth handler.

Milestone 4: Make auth a generated standard module.

Extend app config parsing, build/codegen, runtime registration, inspect output, and TypeScript client generation so an app can enable standard auth without writing wrapper endpoint methods. The generated app binary should register the built-in auth handler and endpoints as part of normal scenery generated code.

Milestone 5: Add fixture apps and tests.

Add a Scenery fixture app that enables standard auth, uses a temporary PostgreSQL database when `SCENERY_TEST_DATABASE_URL` is set, and validates dev bootstrap, email signup, login, refresh, logout, current user, organizations, and auth context access from another service. Add unit tests for token parsing, password hashing, refresh-session rotation, config defaults, codegen, clientgen, and inspect output.

Milestone 6: Migrate ONLV to scenery auth.

Update ONLV `.scenery.json` and imports to use standard scenery auth. Remove or retire local auth/users DB ownership and endpoint logic. Add a database migration path from existing ONLV `users` / `tenants` schema data into the scenery auth schema. Regenerate the app TypeScript client and verify the app login/dev-token flows in the Browser.

Milestone 7: Documentation, cleanup, and release readiness.

Update scenery docs and ONLV `AGENTS.md` with the new auth surface, migration commands, debug commands, and psql notes. Remove stale ONLV auth docs. Run full validation in both repositories and record final outcomes.

## Plan of Work

Start in the scenery repository. Do not delete ONLV auth first. The safe sequence is to make scenery own auth in a fixture, prove it, migrate ONLV, then remove ONLV auth code.

First, define the public auth configuration. A proposed config shape is:

```json
{
  "auth": {
    "enabled": true,
    "database_url_env": "DatabaseURL",
    "refresh_cookie_name": "scenery_refresh",
    "public_app_url_env": "PublicAppURL",
    "api_base_url_env": "APIBaseURL",
    "email_from_env": "AuthEmailFrom",
    "google_oauth": {
      "enabled": true,
      "client_id_env": "GoogleOAuthClientID",
      "client_secret_env": "GoogleOAuthClientSecret"
    },
    "dev_bootstrap": {
      "enabled": true
    }
  }
}
```

The exact JSON can change during implementation, but it must live in scenery-owned config parsing and be documented in `docs/local-contract.md` if it becomes stable. ONLV can initially configure `refresh_cookie_name` to the existing cookie name if needed, then later move to a Scenery default.

Next, add HCL/sqlc in scenery. Use a Scenery schema name such as `scenery_auth`. The schema should cover users, tenants or organizations, identities, memberships, invites or one-time tokens, refresh sessions, OAuth states, auth events, and any rate-limit storage that remains in the standard auth module. Port ONLV schema intent, but remove ONLV-specific names and comments unless they are generic. Keep check constraints for provider and role values as text checks, not PostgreSQL enums, to keep option changes simple.

Then port the service logic. Prefer a clean implementation over a blind copy. Preserve security semantics from ONLV: HMAC JWT validation with required expiration, signed access token generation, refresh token hashing and rotation with replay protection, Argon2id password hashing, local-only dev bootstrap, one-time token consumption, organization membership checks, impersonation actor tracking, and Google OAuth PKCE/state validation. Keep secrets and redirect behavior configurable. Avoid importing ONLV packages.

Then integrate the standard module with build/codegen. The generated app should know standard auth endpoints exist for inspect and client generation. Avoid a runtime-only registration path that makes endpoints invisible to `scenery inspect`, generated TypeScript, and the development dashboard. If implementation pressure makes a runtime-only prototype tempting, keep it behind a branch or prototype package and do not migrate ONLV until inspect/clientgen see the endpoints.

Finally migrate ONLV. Switch ONLV services that currently type-assert `pulseauth.Data()` to `*clean.tech/auth.Data` so they instead use either `*sceneryauth.AuthData` or helper functions from the new top surface. Update audit context to understand the new auth data type. Remove ONLV auth endpoints only after `scenery check` still reports the expected routes and the generated app client still includes the auth service methods.

## Concrete Steps

1. In `/path/to/scenery`, create scenery auth DB tooling:

   - Add `atlas.hcl` with a dev env that can inspect/apply `auth/db/schema.hcl`.
   - Add `sqlc.yaml` with a PostgreSQL entry for `auth/db/gen/schema.sql` and `auth/db/queries.sql`, generating package `authdb` to `auth/db/gen`.
   - Add `auth/db/schema.hcl`.
   - Add `auth/db/queries.sql`.
   - Add a small script under `scripts/` for regenerating `auth/db/gen/schema.sql` from Atlas and then running `sqlc generate`, unless a repo-level task runner is added.

2. Generate and commit auth DB artifacts:

   ```sh
   # from /path/to/scenery
   atlas schema inspect --env dev --schema scenery_auth --format '{{ sql . }}' > auth/db/gen/schema.sql
   sqlc generate
   gofmt -w auth db internal
   ```

   Adjust the commands to match the final scripts. Do not hand-edit generated sqlc files.

3. Extend `scenery.sh/auth`:

   - Keep existing `UID`, `UserID()`, `Data()`, and `WithContext(...)` behavior.
   - Add concrete types with non-conflicting names, for example `AuthData`, `AuthUser`, `AuthOrganization`, `AuthSessionResponse`, `EmailSignupRequest`, `EmailLoginRequest`, `RefreshRequest`, `PasswordResetRequest`, `OrganizationRequest`, and impersonation request/response types.
   - Add a service/manager type that accepts a `pgx` pool or a connection-string option and owns auth operations.
   - Add a typed helper such as `CurrentAuthData() (*AuthData, bool)` so app code does not type-assert from `Data()` manually.

4. Port and generalize service logic:

   - Move JWT generation and validation from ONLV `auth/auth.go`.
   - Move password hashing from ONLV `auth/passwords.go`.
   - Move session/login/signup/refresh/logout from ONLV `auth/sessions.go`.
   - Move Google OAuth from ONLV `auth/google.go`.
   - Move organizations, invites, and impersonation from ONLV `auth/organizations.go` and `auth/impersonation.go`.
   - Move token helpers from ONLV `auth/tokens.go`.
   - Replace ONLV mail dependency with an interface or configured sender.
   - Replace ONLV-specific strings such as "ONLV" in email subjects and redirect logic with configurable product/app names.

5. Add standard-module config and codegen support:

   - Extend `.scenery.json` parsing in `internal/app` to include auth config.
   - Extend `internal/model.App` or a parallel standard-module model to represent generated auth endpoints and the generated auth handler.
   - Extend `internal/codegen/generator.go` to register standard auth endpoints and the standard auth handler.
   - Extend `internal/inspect`, `internal/devmeta`, and TypeScript client generation so standard auth endpoints appear in inspect JSON and `apps/app/src/app-client.ts`.
   - Ensure observability names are stable, probably service `auth` and endpoint names matching current ONLV names where possible.

6. Add scenery tests and fixtures:

   - Add a fixture under `testdata/apps/auth-platform` with `.scenery.json` enabling auth.
   - Add fixture service endpoints that prove another service can read current auth via `auth.CurrentAuthData()` or `auth.UserID()`.
   - Add unit tests for tokens, password hashing, refresh rotation, OAuth state validation, config parsing, codegen output, clientgen output, and inspect output.
   - Add PostgreSQL integration tests skipped unless `SCENERY_TEST_DATABASE_URL` is set.

7. Migrate ONLV:

   - Update `/Users/petrbrazdil/Repos/onlv/.scenery.json` to enable scenery auth.
   - Add any minimal ONLV auth configuration needed for email sending, product name, redirects, cookie name, Google credentials, and dev bootstrap defaults.
   - Migrate existing data from ONLV `users` and `tenants` schemas into `scenery_auth`. Keep a rollback path until the migrated login flow is verified.
   - Replace ONLV imports of `clean.tech/auth` with `scenery.sh/auth` types/helpers or a tiny local alias package only if absolutely needed during migration.
   - Remove ONLV `auth/`, `users/dev_auth.go`, and auth/users HCL/sqlc entries after routes and tests pass.
   - Regenerate `apps/app/src/app-client.ts` with `just gen-app-client`.

8. Update docs:

   - scenery `ARCHITECTURE.md`
   - scenery `docs/local-contract.md`
   - scenery `SKILL.md`
   - ONLV `AGENTS.md`
   - Any ONLV frontend auth docs if present

## Validation and Acceptance

scenery validation:

```sh
# from /path/to/scenery
gofmt -w auth internal runtime testdata
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
scenery check --app-root testdata/apps/auth-platform --json
```

PostgreSQL auth integration validation:

```sh
# from /path/to/scenery
SCENERY_TEST_DATABASE_URL='postgres://...' go test ./auth ./internal/... -run Auth
```

Use the final package list after implementation; the key requirement is that auth integration tests run against a real PostgreSQL database and skip only when the database URL is unset.

ONLV validation:

```sh
# from /Users/petrbrazdil/Repos/onlv
scenery check --json
just gen-app-client
go test ./...
scenery harness --json --write
```

Frontend validation:

```sh
# from /Users/petrbrazdil/Repos/onlv/apps/app
bun run typecheck
bun run build
```

If `bun run typecheck` is blocked by the existing `apps/viewer` / `@types/three` mismatch when run through a broader command, record that explicitly and still run the app-local build/typecheck command that isolates the auth change.

Browser acceptance:

- Start ONLV through `scenery dev`.
- Open `https://app.onlv.localhost/dev-auth?redirect=/dev/data&user=dev-token&tenant=00000000-0000-0000-0000-000000000001`.
- Confirm `/users/dev-bootstrap` mints a token only in local mode.
- Confirm authenticated API calls include the scenery auth data.
- Confirm email signup/login, refresh, logout, `/auth/me`, organization switch, and impersonation flows still work in ONLV app.
- Confirm the local dev-token flow still works after a full browser reload.

This plan is complete only when:

- scenery owns auth logic and auth DB schema/query code.
- ONLV no longer has auth business logic or auth-owned DB schema/query code.
- ONLV services use the scenery auth top surface for current user/tenant/actor state.
- Generated TypeScript client contains standard auth endpoints without ONLV declaring those endpoints manually.
- All listed validation either passes or has a documented, unrelated blocker with evidence.

## Idempotence and Recovery

Do not delete ONLV auth code before scenery auth passes fixture tests and ONLV can run with the standard module enabled. Keep ONLV cleanup in a separate commit from the initial scenery auth implementation if possible.

Schema generation should be repeatable. HCL is the source of truth. Generated SQL and sqlc output should be regenerated from commands, not edited manually.

Database migration should be additive first:

- Create `scenery_auth` schema and tables.
- Copy ONLV data from old schemas into new tables.
- Verify row counts and representative login/refresh flows.
- Switch ONLV to standard scenery auth.
- Keep old schemas untouched until a later cleanup confirms rollback is no longer needed.

If migration fails halfway, rerun the migration in an idempotent mode using `INSERT ... ON CONFLICT ... DO UPDATE` or truncate only the new `scenery_auth` schema in a disposable/local database. Never drop ONLV production auth tables as part of the first implementation.

If standard-module codegen proves too large, pause before ONLV migration. A manual wrapper prototype can be used only in a fixture to validate service logic, but ONLV should not be migrated until generated standard auth endpoints are visible to `scenery inspect` and client generation.

## Artifacts and Notes

Expected scenery artifacts:

- `auth/db/schema.hcl`
- `auth/db/queries.sql`
- `auth/db/gen/schema.sql`
- `auth/db/gen/*.go`
- `atlas.hcl`
- `sqlc.yaml`
- `scripts/gen-auth-sqlc.sh` or equivalent
- `testdata/apps/auth-platform/.scenery.json`
- `testdata/apps/auth-platform/...`
- Updated `auth/auth.go` and new auth implementation files
- Updated parser/config/codegen/inspect/clientgen tests
- Updated docs and harness output

Expected ONLV cleanup artifacts:

- Updated `.scenery.json`
- Removed or emptied `auth/` business logic after migration
- Removed `users/dev_auth.go`
- Removed `auth/db` and `users/db` ownership if no remaining ONLV-owned user service needs them
- Updated `atlas.hcl`, `sqlc.yaml`, and `justfile`
- Updated imports in app services from `clean.tech/auth` to scenery auth surface
- Regenerated `apps/app/src/app-client.ts`
- Updated `.scenery/harness/latest.json`

Open questions to resolve during implementation:

- Should the default refresh cookie name be `scenery_refresh`, with ONLV temporarily configuring `onlv_refresh`, or should the endpoint continue accepting both during migration?
- Should auth email delivery use a Go interface configured by generated code, a Scenery event hook, SMTP settings, or a generic HTTP webhook?
- Should ONLV's existing `users` / `tenants` data be copied into `scenery_auth`, or should the first local migration use a fresh auth database for development and a separate production migration plan?
- Should standard auth endpoints be always available when auth is enabled, or should `.scenery.json` allow disabling email/password, Google, organizations, impersonation, or dev bootstrap independently?

## Interfaces and Dependencies

Proposed public Go surface in `scenery.sh/auth`:

```go
// Existing context helpers remain.
type UID string
func UserID() (UID, bool)
func Data() any
func WithContext(ctx context.Context, uid UID, data any) context.Context

// New concrete standard-auth payload.
type AuthData struct {
    UserID string
    TenantID string
    SessionID string
    ActorUserID string
    ImpersonationID string
}

func CurrentAuthData() (*AuthData, bool)
```

The exact request and response type names should be chosen during implementation, but they must avoid conflicts with existing package-level functions.

Proposed app config surface in `.scenery.json`:

```json
{
  "auth": {
    "enabled": true,
    "database_url_env": "DatabaseURL",
    "refresh_cookie_name": "scenery_refresh",
    "dev_bootstrap": { "enabled": true },
    "google_oauth": { "enabled": true }
  }
}
```

Stable endpoint surface should preserve the current ONLV paths where practical. If any endpoint path changes, update `docs/local-contract.md`, the generated TypeScript client tests, and ONLV frontend code in the same implementation milestone.

Runtime dependencies:

- `github.com/jackc/pgx/v5` and scenery's `pgxpool` wrapper for PostgreSQL access.
- `golang.org/x/crypto/argon2` for password hashing; this dependency already exists indirectly and should become direct if used by public auth code.
- `github.com/golang-jwt/jwt/v5` or an equivalent small JWT implementation for access tokens. If adding this dependency, document the payoff in the commit and keep usage narrow.

Development dependencies:

- Atlas CLI for HCL schema inspection/apply, matching ONLV's current workflow.
- sqlc CLI for generated typed database access.

Non-goals for the first implementation:

- Hosted/cloud identity provider integration beyond the existing Google OAuth behavior.
- A full permissions/roles framework outside the current organization owner/member and impersonation behavior.
- A new frontend auth UI.
- Deleting ONLV production auth data during the first migration.
- Making scenery depend on ONLV packages.
