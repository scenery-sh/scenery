# Impersonation target provisioning

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds. Maintain it according to `PLANS.md`.

## Purpose / Big Picture

Let an authorized application administrator impersonate a business user who does not yet have a Google or password identity. Scenery will prepare an unverified, provider-neutral auth user and tenant membership, then use its existing short-lived audited impersonation session. Preparing a target must not create a provider identity, grant normal login, or weaken ordinary email verification.

Completion is observable when a privileged actor can prepare and impersonate an existing or new auth target, an unverified target cannot sign in normally, a verified Google callback can later attach to a prepared identity, nested or unauthorized impersonation fails, and normal users remain unable to prepare targets.

## Progress

- [x] (2026-08-02 12:30Z) Audited the standard auth user, membership, Google-link, session, and impersonation paths.
- [x] (2026-08-02 22:27Z) Implemented the public target-preparation API and unverified-target impersonation rules.
- [x] (2026-08-02 22:27Z) Preserved normal Google and email verification semantics and added focused real-Postgres tests.
- [x] (2026-08-02 22:27Z) Updated current auth documentation, generated sqlc output, and passed the full Go suite and self-harness.
- [x] (2026-08-02 22:27Z) Released v0.3.5 from commit `83530468` and consumed it from Platform.

## Surprises & Discoveries

- Observation: `StartImpersonation` already creates actor-attributed short-lived sessions but calls `ensureActiveTenant`, which rejects unverified users.
  Evidence: `auth/standard_impersonation.go` and `auth/standard_service.go`.
- Observation: Google linking currently rejects every existing unverified user, so a provider-free prepared user needs a narrow later-link path without allowing an unverified password identity to bypass verification.
  Evidence: `auth/standard_google.go`.

## Decision Log

- Decision: prepared users remain unverified and have no auth identity.
  Rationale: impersonation must not manufacture proof of email ownership or login credentials.
  Date/Author: 2026-08-02 / OpenAI.
- Decision: Google may attach to an unverified prepared user only when that user has zero provider identities.
  Rationale: a verified Google assertion can safely complete the prepared profile, while an unverified email/password signup must still complete its own verification.
  Date/Author: 2026-08-02 / OpenAI.
- Decision: target preparation is a supported in-process public auth API, not application SQL against Scenery tables.
  Rationale: Scenery owns auth users, tenants, memberships, and auth audit events.
  Date/Author: 2026-08-02 / OpenAI.

## Outcomes & Retrospective

Released in Scenery v0.3.5 at commit `83530468`. `auth.PrepareImpersonationTarget` now creates or resolves an unverified provider-free user, ensures an active membership in the actor's current tenant, and emits an auth event. Existing impersonation sessions accept that target only for the exact prepared tenant and retain actor attribution; nested and self impersonation fail closed. A later verified Google callback may claim the prepared user only while it still has zero provider identities, so ordinary unverified password users cannot bypass verification.

Focused real-Postgres tests covered privileged preparation/start, non-privileged denial, zero provider identities, active membership, session actor/effective identity, and verified Google attachment. `go test ./...` and `.scenery/harness/bin/scenery harness self --summary --write` passed. Platform then completed a browser start/navigate/stop cycle against an unlinked business user and confirmed the prepared user remained unverified with zero provider identities and no live impersonation session after stopping.

## Context and Orientation

The repository is `/Users/petrbrazdil/Repos/scenery`. Standard auth lives under `auth/`. `StartImpersonation` and `StopImpersonation` are existing typed routes. `auth.CurrentUser`, lifecycle APIs, and permission-checker APIs are the supported application boundary. Generated auth SQL lives under `auth/db/gen/` and is refreshed with `scripts/gen-auth-sqlc.sh`.

## Milestones

First add a public `PrepareImpersonationTarget` API that authorizes the current actor, resolves or creates a provider-free user by normalized email, and ensures membership in the actor's current tenant. Then allow only an actor-attributed impersonation session to use an unverified target with that exact active membership. Finally let verified Google attach to a provider-free prepared user while preserving the existing failure for unverified password users.

## Plan of Work

Add the minimal public request/result types and implementation in `auth/`. Reuse existing database queries where possible and add only the identity-count query needed to distinguish a provider-free prepared user. Record an auth event when a target is prepared. Keep start/stop HTTP contracts unchanged.

Add focused unit and real-Postgres tests for authorization, idempotency, membership, no provider identity, unverified impersonation, nested denial, and later Google linking. Update the standard-auth application contract documentation.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    ./scripts/gen-auth-sqlc.sh
    go test ./auth
    go test ./...
    .scenery/harness/bin/scenery harness self --summary --write
    git diff --check

Refresh `.scenery/harness/agent-context.json` and run the exact changed-area recommended-command union before completion.

## Validation and Acceptance

Tests must prove a non-privileged actor cannot prepare a target; a privileged actor can prepare the same target idempotently; no provider identity is created; the target receives only the actor tenant membership; the prepared target can be impersonated but cannot obtain a normal session; nested impersonation is denied; and verified Google later attaches to the provider-free target while an unverified password user remains rejected.

The complete Go suite and self-harness must pass. The released version and commit must be recorded in Outcomes.

## Idempotence and Recovery

Preparation resolves by normalized email and uses the existing unique user-email and active-membership constraints. Retrying returns the same user. A failed application-side business link may leave a harmless provider-free auth user; retrying converges on it. No rollback may delete a user that has acquired a provider identity or session.

## Artifacts and Notes

Keep test transcripts and temporary release evidence under `/tmp`. Do not store tokens, cookies, or user secrets.

## Interfaces and Dependencies

The public API accepts an optional existing auth user ID plus business display name and email, derives the tenant and actor from current `AuthData`, and returns `UserProfile`. It depends only on standard auth's existing user, membership, event, and session stores. Applications must still call `StartImpersonation` to issue the session.
