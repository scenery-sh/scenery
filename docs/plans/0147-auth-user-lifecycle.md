# Standard Auth User Lifecycle

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds. Maintain it according to `PLANS.md`.

## Purpose / Big Picture

Give applications a small supported way to complete company offboarding without querying or mutating Scenery-managed auth tables. An authorized application command can disable or re-enable one standard-auth user and revoke every refresh session for that user. Disabling is idempotent, revokes sessions atomically with the disable, and causes existing access tokens to fail on their next standard-auth user check. The API is provider-neutral and does not add an auth-administration framework, invitation system, or email/password onboarding.

Completion is observable when MicroGRID Platform can call the public API from its audited user lifecycle, disabled users cannot refresh or use protected application paths, enabling restores identity eligibility without restoring sessions, and a released Scenery version contains the API.

## Progress

- [x] 2026-07-31: Audited existing standard-auth users, refresh-session revocation, organization-member disable, current-user behavior, and first Platform consumer.
- [x] 2026-07-31: Implemented typed public lifecycle functions, generated sqlc queries, transactional revocation, access-token session validation, and focused database tests.
- [x] 2026-07-31: Documented the app-owned authorization boundary and provider-neutral semantics.
- [x] 2026-07-31: Passed focused live-Postgres lifecycle tests, auth race tests, the full Go suite, the release-mode self-harness, and Platform consumer compilation.
- [ ] Release Scenery and record the exact tag/commit.

## Surprises & Discoveries

- Observation: refresh-session revocation already exists internally for logout, password reset, replay defense, and impersonation, but there is no public per-user lifecycle surface.
  Evidence: `auth/standard_sessions.go` and `auth/db/queries.sql`.
- Observation: organization membership can be disabled independently, but the current Platform deployment has one business auth tenant and requests global company offboarding.
  Evidence: `auth/standard_organizations.go` and Platform ExecPlan 0071.
- Observation: access JWTs contain the refresh-session ID, so lifecycle revocation can invalidate already-issued access tokens without a new token blacklist.
  Evidence: `auth/standard_service.go`, `auth/standard_jwt.go`, and `auth/db/queries.sql`.
- Observation: impersonation sessions belong to the effective user and carry the real actor separately; revoking only sessions owned by the actor would allow an old impersonation token to revive after re-enabling.
  Evidence: `auth/standard_impersonation.go` and the `actor_user_id` refresh-session column.
- Observation: the repository release gate reaches a pre-existing repository-wide lint backlog unrelated to these changes, while the release-mode self-harness and changed auth surface pass.
  Evidence: `.scenery/release-gate/20260731T210030Z/go-lint.log`; no reported item is in a file changed by this plan.

## Decision Log

- Decision: expose `DisableUser`, `EnableUser`, and `RevokeUserSessions` using existing provider-neutral `auth.AuthUserID`.
  Rationale: standard auth already exposes the exact UUID identity type; no parallel provider-specific type is needed.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: `DisableUser` also revokes all active refresh sessions in the same database transaction.
  Rationale: disabling without revocation is an unsafe partial company-offboarding state; the standalone revoke function remains useful without disabling.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: these functions enforce input/configuration/storage invariants but not application business authorization.
  Rationale: the embedding application owns who may offboard users through its policy and audit command; Scenery owns auth-state mutation.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: enabling does not recreate sessions, memberships, or provider connections.
  Rationale: reactivation must require a fresh sign-in and must not silently restore unrelated access.
  Date/Author: 2026-07-31 / OpenAI.
- Decision: per-user revocation includes sessions where the user is either the effective user or the impersonation actor, and protected access tokens validate their referenced live session.
  Rationale: company offboarding must close both direct and delegated access immediately, and re-enabling must not reactivate an already-issued token.
  Date/Author: 2026-07-31 / OpenAI.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Repository root is `/Users/petrbrazdil/Repos/scenery`. `auth/db/queries.sql` owns standard-auth persistence; `auth/standard.go` owns process-global configuration/service state; `auth/current_user.go` demonstrates the public top-level accessor pattern; `auth/standard_sessions.go` contains existing transaction and session-revocation behavior; generated sqlc output lives under `auth/db/gen/`. The first consumer is `/Users/petrbrazdil/Repos/Micro/platform` plan 0071.

## Milestones

First add idempotent user enable/disable queries and transactional lifecycle functions with focused tests. Then add public documentation and consumer examples. Finally run full validation, prove the Platform integration, publish a release, and move this plan to the completed index.

## Plan of Work

Add sqlc queries that lock/load a user, set or clear `disabled_at`, and revoke every active refresh session with a sanitized nonblank reason. Implement public top-level functions in a focused auth file. `DisableUser` begins a transaction, verifies the user exists, disables it idempotently, revokes sessions, and commits. `EnableUser` clears the disabled state but does not touch memberships or sessions. `RevokeUserSessions` revokes sessions without changing the user.

Use stable coded errors: invalid UUID/reason is `invalid_argument`, missing user is `not_found`, disabled/enabled repetition succeeds, unavailable standard auth is `failed_precondition`, and database failures retain their cause. Add tests for idempotency, atomic rollback, reason persistence, missing users, invalid input, session revocation, and re-enable behavior. Update `SKILL.md`, `docs/agent-guide.md`, and `docs/app-development-cookbook.md` without exposing table names as an application contract.

## Concrete Steps

Run from `/Users/petrbrazdil/Repos/scenery`:

    just gen-auth-sqlc
    go test ./auth
    go test -race ./auth -run 'Test(DisableUser|EnableUser|RevokeUserSessions)' -count=1
    go test ./...
    .scenery/harness/bin/scenery harness self --summary --write
    git diff --check

If `just gen-auth-sqlc` is not the current generator target, inspect `Justfile` and run the exact existing auth sqlc target; do not hand-edit generated files.

## Validation and Acceptance

Tests must prove that disable plus revocation is one transaction, repeated disable/revoke/enable calls are safe, no token or provider subject is logged, refresh sessions become unusable, current-user lookup denies disabled users, enabling requires a new session, and unrelated users/sessions are unchanged. The Platform consumer must compile against a released version and browser acceptance must show immediate denial after company offboarding.

Run the changed-area oracle and every recommended command. Because this is public auth API and release-sensitive runtime work, the minimum union includes `go test ./auth`, `go test ./cmd/scenery`, `go test ./...`, the focused race command, and `.scenery/harness/bin/scenery harness self --summary --write`.

## Idempotence and Recovery

All lifecycle functions are idempotent. Transactions roll back disable and session revocation together. Re-enable never restores sessions, so recovery is explicit: enable the user and require a fresh sign-in. If the Platform business mutation succeeds but the Scenery call fails, Platform remains inactive and retrying the audited command completes auth offboarding safely.

## Artifacts and Notes

Keep temporary database transcripts and consumer proof under `/tmp`. Record only aggregate session counts, coded outcomes, and revision identifiers; never record tokens, hashes, passwords, Google subjects, or connection secrets.

## Interfaces and Dependencies

The intended public surface is:

```go
func auth.DisableUser(context.Context, auth.AuthUserID, string) error
func auth.EnableUser(context.Context, auth.AuthUserID) error
func auth.RevokeUserSessions(context.Context, auth.AuthUserID, string) error
```

It depends only on existing standard-auth configuration, sqlc persistence, `auth.AuthUserID`, and `scenery.sh/errs`. It adds no HTTP endpoint, generated TypeScript client, policy DSL, role model, provider-specific behavior, invitation system, or email/password onboarding.
