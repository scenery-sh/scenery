# Application Permission Checks in Standard Auth

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds. Maintain it
according to `PLANS.md`.

## Purpose / Big Picture

Add one small, reusable authorization seam to scenery.sh/auth:

```go
allowed, err := auth.HasPermissions(ctx, "Fleet")
```

A Scenery application registers one checker. Scenery obtains the current
standard-auth context and delegates the requested names to that checker.
Permission names are opaque, case-sensitive, and application-owned. Scenery does
not define roles, wildcards, read/write/delete semantics, a policy language,
permission tables, or a cache in this plan.

The same API must work for Google and email/password. Authorization begins from
the resulting authenticated user, not from the provider used to authenticate.

Also add `auth.CurrentUser(ctx)` so an application can read the live verified
standard-auth profile without querying Scenery-managed tables. This is needed by
the first consumer, MicroGRID Platform, to link a verified login to an existing
business profile.

Completion is observable when:

- an app registers one checker with `auth.SetPermissionChecker`;
- `auth.HasPermissions` supplies the current `*auth.AuthData` and all requested
  names in one call;
- missing auth, missing configuration, invalid input, denial, and checker errors
  fail closed predictably;
- `auth.CurrentUser` returns the live `UserProfile`;
- existing auth, organization, session, Google, email/password, impersonation,
  and `data.StandardAuthPermissions` behavior remains unchanged.

## Progress

- [x] 2026-07-31: Audited current auth context, standard-auth profile access,
      the earlier data-specific permission adapter, docs, validation rules, and
      plan numbering.
- [x] 2026-07-31: Drafted this plan only; no repository files changed.
- [x] 2026-07-31: Implemented and focused-tested the permission checker surface.
- [x] 2026-07-31: Implemented and focused-tested `CurrentUser`.
- [x] 2026-07-31: Updated the public app-development docs and added a compiling
      package example.
- [x] 2026-07-31: Ran the harness-selected validation union, the auth-focused
      race detector, and closed the plan.

## Surprises & Discoveries

- Observation: `data.StandardAuthPermissions` already connects standard-auth
  tenant context to data-platform object/field/row checks, but auth has no
  general application permission call.
  Evidence: `docs/plans/0021-auth-data-tenant-permissions.md`.
- Observation: `AuthData` is already provider-neutral and contains the IDs a
  permission checker needs.
  Evidence: `auth/auth.go` and `auth/standard_jwt.go`.
- Observation: `UserProfile` already contains the live profile fields required
  by applications, but only endpoint methods currently return it.
  Evidence: `auth/standard_service.go`.
- Observation: this feature is an in-process Go API. It needs no `.scn` resource,
  compiler change, generated client, or auth schema migration.
- Observation: the historical `data.StandardAuthPermissions` plan remains in
  the completed-plan record, but the current repository no longer contains the
  old `data` or `internal/objectstore` packages. The current equivalents are
  typed `datasource` and `object` capabilities, neither of which this change
  touches.
  Evidence: `rg -n "StandardAuthPermissions" .` returns only historical docs;
  `go list ./data ./internal/objectstore` reports that both directories are
  absent.
- Observation: `runtime.CurrentAuth` is request-ambient, while `auth.WithContext`
  already constructs explicit auth-bearing contexts for internal use. The new
  functions first honor the ambient `CurrentAuthData` and then use the exact
  standard-auth data attached by `auth.WithContext`, which makes direct internal
  calls and focused tests preserve the same identity.
  Evidence: `runtime/current.go` and `auth/auth.go`.

## Decision Log

- Decision: ship exactly one checker interface, one setter, one checking
  function, and one current-user accessor:

  ```go
  type PermissionChecker interface {
      HasPermissions(context.Context, *AuthData, ...string) (bool, error)
  }

  func SetPermissionChecker(PermissionChecker)
  func HasPermissions(context.Context, ...string) (bool, error)
  func CurrentUser(context.Context) (UserProfile, error)
  ```

  Rationale: this is the smallest useful surface for a database-backed
  application policy and the requested call shape.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: `HasPermissions` uses all-of semantics and calls the checker once
  with the complete request.
  Rationale: it is predictable and permits one database query. Any-of behavior
  can be expressed with separate calls.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: names are opaque and case-sensitive to Scenery.
  Rationale: applications own their permission vocabulary. Do not invent a
  cross-application canon before multiple consumers demonstrate one.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: zero names or an empty name is `InvalidArgument`; no auth is
  `Unauthenticated`; no checker is `FailedPrecondition`; denial is `false, nil`.
  Rationale: programming/configuration/session failures must not look like a
  successful authorization result.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: do not add `RequirePermissions`, middleware, decorators, role
  helpers, wildcards, caching, resource attributes, or persistence.
  Rationale: none is required by the first consumer, and all can be added later
  without changing this interface.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: `CurrentUser` loads the live standard-auth user row and rejects a
  missing or disabled user.
  Rationale: applications must not trust client-supplied email, and loading the
  whole `/auth/me` bootstrap is unnecessary.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: a missing current user is `Unauthenticated`, a disabled current user
  is `PermissionDenied`, and unavailable standard-auth configuration is
  `FailedPrecondition`; query errors pass through unchanged.
  Rationale: deleted identities invalidate authentication, disabled identities
  retain standard auth's existing forbidden-user semantics, configuration
  failure is distinct from denial, and storage failures remain diagnosable.
  Date/Author: 2026-07-31 / OpenAI.

## Outcomes & Retrospective

Completed 2026-07-31.

Scenery now exposes `PermissionChecker`, `SetPermissionChecker`,
`HasPermissions`, and `CurrentUser` from `scenery.sh/auth`. Permission checks
validate names before lookup, obtain the current provider-neutral `AuthData`,
invoke the configured checker once with all exact names, and return its
grant/denial/error unchanged. The checker setter is protected by an `RWMutex`,
supports replacement, and accepts nil to restore the unconfigured state.

The stable fail-closed codes are:

- `InvalidArgument` for zero permission names or a blank name;
- `Unauthenticated` for missing auth, an invalid auth user ID, or an auth user
  row that no longer exists;
- `FailedPrecondition` for a missing checker or unavailable standard-auth
  configuration;
- `PermissionDenied` for a disabled current user;
- `false, nil` for an ordinary permission denial.

Checker and user-query errors pass through unchanged. `CurrentUser` performs
one `GetUserByID` query, maps the live row, and has no session, tenant, or token
side effects. Focused tests cover grant, denial, exact batched names, complete
auth and impersonation data, invalid input, missing auth/configuration/checker,
checker and database errors, verified/unverified profiles, missing/disabled
users, setter replacement/reset, and concurrent reads and writes. The compiling
public example is `auth/permissions_example_test.go`.

Public guidance changed in `SKILL.md`, `docs/agent-guide.md`, and
`docs/app-development-cookbook.md`. No `.scn`, compiler, generated client,
database schema, endpoint, or generated artifact changed.

The harness selected `cli-json-contract`, `go-package`, and
`release-sensitive-or-runtime`, with this command union:

```sh
.scenery/harness/bin/scenery harness self --summary --write
go test ./...
go test ./auth
go test ./cmd/scenery
```

All selected commands passed. The focused race command
`go test -race ./auth -run 'Test(HasPermissions|SetPermissionChecker|CurrentUser)' -count=1`
also passed. The historical data-package check was replaced by
`go test ./datasource ./object`, which passed, because those are the current
packages and the former `data`/`internal/objectstore` paths no longer exist.

The first downstream consumer is tracked by MicroGRID Platform ExecPlan
`docs/agent/exec-plans/active/0070-identity-linking-scenery-permissions.md`.
That separate consumer plan is not yet implemented and has no commit or PR to
record; this Scenery plan supplies its complete prerequisite API without
changing the Platform repository.

## Context and Orientation

Repository root:

```text
/Users/petrbrazdil/Repos/scenery
```

Relevant files:

- `auth/auth.go`: current auth context helpers.
- `auth/standard_jwt.go`: provider-neutral `AuthData`.
- `auth/standard_service.go`: `Service`, `UserProfile`, and auth DB access.
- `auth/standard.go`: standard-auth registration and global state.
- `auth/db/queries.sql`: generated user queries.
- `data/`: existing data-specific permission adapter.
- `SKILL.md`, `docs/agent-guide.md`, and
  `docs/app-development-cookbook.md`: app-facing documentation.
- `docs/plans/active.md` and `docs/knowledge.json`: active plan indexes.

One Scenery application runs per process, so one process-global checker is
sufficient. Registration must be concurrency-safe and documented as startup
configuration.

## Milestones

### Milestone 1 — Permission API

Add the interface, setter, implementation, and focused tests.

### Milestone 2 — Current user profile

Add the top-level live-profile accessor and focused tests.

### Milestone 3 — Documentation and validation

Add one compiling app example, run the repository validation union, and record
the first consumer.

## Plan of Work

Create `auth/permissions.go`.

Use a small `sync.RWMutex` or equivalent race-free primitive. The setter replaces
the current checker; nil clears it for tests. Document that applications set it
before `runtime.Main` serves requests.

`HasPermissions` must:

1. reject an empty request or empty/whitespace-only name;
2. obtain `CurrentAuthData`;
3. return `Unauthenticated` when absent;
4. return `FailedPrecondition` when no checker is configured;
5. invoke the checker once with the current auth data and all exact names;
6. return the checker result/error without normalization, retries, caching, or
   additional policy interpretation.

Create `auth/current_user.go`. `CurrentUser(ctx)` should obtain the current auth
data, parse the user UUID with existing helpers, acquire the standard-auth
service, call `GetUserByID`, reject a disabled row, and return `mapUser(row)`.
Do not call `Me`, rotate a session, create a tenant, or issue a token.

Add focused tests covering:

- grant and denial;
- several names in one checker call;
- exact name preservation;
- invalid input;
- absent auth;
- absent checker;
- checker error propagation;
- live verified/unverified profiles;
- disabled/missing users and database errors;
- safe setter replacement/reset.

Add one concise public example:

```go
type appPermissions struct{}

func (appPermissions) HasPermissions(
    ctx context.Context,
    data *auth.AuthData,
    permissions ...string,
) (bool, error) {
    // Query application-owned permission data with data.UserID.
    return true, nil
}

func init() {
    auth.SetPermissionChecker(appPermissions{})
}
```

State explicitly that the checker owns storage and semantics and that the API is
provider-neutral.

Do not modify auth tables, endpoint registration, `.scenery.json`, compiler
models, `.scn` grammar, generated clients, or the data-platform permission
adapter.

## Concrete Steps

From the repository root:

```sh
cd /Users/petrbrazdil/Repos/scenery
git status --short
```

Check in and index the plan:

```sh
$EDITOR docs/plans/0146-auth-permission-checker.md
$EDITOR docs/plans/active.md
$EDITOR docs/knowledge.json
```

Implement and format:

```sh
gofmt -w auth/permissions.go auth/current_user.go \
  auth/permissions_test.go auth/current_user_test.go
```

Run targeted checks:

```sh
go test ./auth -run 'Test(HasPermissions|CurrentUser)' -count=1
go test ./auth
go test ./datasource ./object
```

Update only the app-facing docs that describe the new API:

```sh
$EDITOR SKILL.md
$EDITOR docs/agent-guide.md
$EDITOR docs/app-development-cookbook.md
```

Refresh the harness context and print the exact selected commands:

```sh
.scenery/harness/bin/scenery harness self --quick --summary --write
python3 - <<'PY'
import json
from pathlib import Path
data = json.loads(Path(".scenery/harness/agent-context.json").read_text())
changed = data.get("changed_area", {})
print(changed.get("validation_classes", []))
print(*changed.get("recommended_commands", []), sep="\n")
PY
```

Run that exact union. The expected minimum is:

```sh
go test ./auth
go test ./...
.scenery/harness/bin/scenery harness self --summary --write
git diff --check
```

At completion, update this file, move its active index entry to
`docs/plans/completed.md`, update `docs/knowledge.json`, and treat the completed
plan as immutable.

## Validation and Acceptance

The change is accepted when:

- grant, denial, all-of batching, invalid input, missing auth, missing checker,
  and checker errors match the documented behavior;
- the checker receives the same user/tenant/session/actor/impersonation IDs as
  `CurrentAuthData`;
- `CurrentUser` returns live verified-email state for both Google and
  email/password accounts;
- `CurrentUser` rejects disabled/missing users and has no session or tenant side
  effects;
- existing auth, datasource, and object capability tests pass unchanged;
- the public example compiles;
- `go test ./...`, the harness-selected commands, and the full self-harness pass;
- no generated output changes unless the harness identifies a real fixture that
  must be regenerated.

## Idempotence and Recovery

This is additive and has no database migration.

`SetPermissionChecker` is safe to call repeatedly and nil restores the
unconfigured state. Production apps configure it once before serving.

Permission checks are read-only. Retry safety depends only on the application
checker, which the docs require to be read-only.

The permission API and `CurrentUser` can be reverted independently because they
do not change stored data or wire protocols.

## Artifacts and Notes

Record:

- Focused auth tests: passed.
- Auth-focused race detector: passed.
- Broad repository tests: passed.
- Harness validation classes: `cli-json-contract`, `go-package`, and
  `release-sensitive-or-runtime`; the exact selected union is recorded in
  Outcomes & Retrospective.
- Public example: `auth/permissions_example_test.go`.
- First downstream consumer: MicroGRID Platform ExecPlan 0070; no consumer
  commit or PR exists yet.

Keep temporary transcripts under `/tmp`.

## Interfaces and Dependencies

New stable surface:

```go
package auth

type PermissionChecker interface {
    HasPermissions(context.Context, *AuthData, ...string) (bool, error)
}

func SetPermissionChecker(PermissionChecker)
func HasPermissions(context.Context, ...string) (bool, error)
func CurrentUser(context.Context) (UserProfile, error)
```

Scenery owns auth-context lookup, checker invocation, and stable fail-closed
errors. The application owns permission identifiers, storage, role/membership
resolution, and denial messages.

The first intended consumer is Platform ExecPlan
`0070-identity-linking-scenery-permissions.md`. Runtime consumer code must not
query `scenery.scenery_auth_*` tables directly.
