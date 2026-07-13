# Bounded Refresh-Cookie Compatibility

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current through the follow-up
release that removes the temporary compatibility behavior.

## Purpose / Big Picture

Preserve standard-auth sessions created with the former fixed default
`onlv_refresh` after the current cookie became `scenery_refresh`. During one
bounded transition, requests read the current cookie first and the known former
default only when the current cookie is absent; all successful session flows
issue only the current name, and logout clears both names independently. After
the existing 30-day refresh-session population has aged out, remove the legacy
read and clear behavior before the release following the corrective release.

## Progress

- [x] 2026-07-13 - Read repository instructions, the approved corrective plan, current auth request/response paths, strict config parsing, affected docs, and documentation freshness output.
- [x] 2026-07-13 - Implement the fixed current-first read order and two-cookie logout clearing without changing public response or config types.
- [x] 2026-07-13 - Add focused cookie behavior, repeated-header transport, and removed-config rejection coverage.
- [x] 2026-07-13 - Synchronize the local contract, migration runbook, environment reference/registry, active historical plan notes, completed-plan correction, and knowledge indexes.
- [x] 2026-07-13 - Run formatting, focused and full Go tests, vet, docs inspection, live HTTP verification, and self-harness.
- [ ] Follow-up release - Remove `onlv_refresh` request selection, logout clearing, tests, and transition documentation before the release after the corrective release.

## Surprises & Discoveries

- Initial `go run ./cmd/scenery inspect docs -o json` reported no missing or stale documents, but the standard-auth migration runbook was review-due as of 2026-07-07.
- All login/session creation paths already share `refreshCookie`, and refresh rotation calls the same fixed current-name builder directly. No issuance path needs a compatibility branch.
- Go's `net/http` cookie parser skips malformed cookie pairs, so the resolver also records raw cookie-name presence. This prevents an unparseable `scenery_refresh` value from silently switching to `onlv_refresh`.
- `PUBLIC_APP_URL` remained in the environment registry and scanner after its auth fallback consumer was removed. The corrective documentation sync removes that stale alias while retaining current `SCENERY_PUBLIC_APP_URL` and frontend `API_BASE_URL` contracts.
- Live verification through a disposable standard-auth runtime proved legacy-only refresh returns 200 and issues only `scenery_refresh`; empty and malformed current cookies return `refresh session is missing`, an invalid current token returns `refresh session is invalid`, and legacy logout returns two ordered clearing headers. The temporary module needed `go mod tidy` before launch because an external module does not inherit the repository's `go.sum`.

## Decision Log

- Decision: Accept exactly `scenery_refresh` and the former fixed default `onlv_refresh`, in that order, with presence of the current cookie preventing all fallback.
  Rationale: This preserves known sessions without reopening configurable cookie naming or allowing validation-dependent credential switching.
  Date/Author: 2026-07-13 / user-approved plan and Codex.
- Decision: Keep `LogoutResponse.SetCookie string` as the current-cookie clear and hold only the additional legacy clear in private response state.
  Rationale: HTTP requires distinct `Set-Cookie` field values, while direct callers retain the existing public Go and JSON contract.
  Date/Author: 2026-07-13 / user-approved plan and Codex.
- Decision: Treat raw current-cookie name presence as authoritative when `net/http` rejects its value.
  Rationale: Selection must never become validation-dependent; malformed current credentials fail closed instead of switching identities through a valid legacy cookie.
  Date/Author: 2026-07-13 / approved implementation plan and Claude.
- Decision: The removal release may not ship until 30 days after publication of the corrective release; record the publication date here when known.
  Rationale: The session TTL is 30 days, so a relative "next release" condition alone could retire compatibility too early.
  Date/Author: 2026-07-13 / approved implementation plan and Claude.

## Outcomes & Retrospective

The corrective implementation is complete and validated. Existing sessions under the former fixed `onlv_refresh` name can refresh into the canonical `scenery_refresh` cookie; current-cookie presence prevents validation-dependent fallback; logout clears both names independently; and removed config/env naming fields remain rejected. Focused and full Go validation, vet, docs inspection, self-harness, and a disposable live HTTP flow all passed.

The plan remains active only for the bounded removal milestone. Record the corrective release publication date here when it ships, wait at least 30 days, then remove `onlv_refresh` selection/clearing and transition documentation before the following release.

## Context and Orientation

`auth/standard_config.go` owns the fixed cookie names. `auth/standard.go`
decodes HTTP refresh-cookie requests and encodes typed outcomes.
`auth/standard_sessions.go` resolves direct-call refresh parameters and returns
logout outcomes. `auth/standard_service.go` builds issuance and clearing cookie
strings. `internal/app/root.go` strictly rejects fields not present in the
`.scenery.json` config structs; `internal/app/root_test.go` owns regression
coverage for that boundary.

The current public cookie is `scenery_refresh`. The only legacy cookie accepted
by this transition is `onlv_refresh`. A cookie is selected by presence, not by
whether its value later parses or authenticates successfully.

## Milestones

1. Centralize the closed current-first cookie order and use it for HTTP and direct service reads.
2. Keep issuance current-only and encode logout clears as two independent headers with identical scope.
3. Lock behavior with focused tests and synchronize operator-facing contracts.
4. No earlier than 30 days after the corrective release publication date recorded in this plan, remove the temporary legacy name before the following release.

## Plan of Work

Add one private ordered cookie-name set and one request-header lookup helper.
Reuse them from both existing read paths. Refactor only the clearing builder to
take a name, keep issuance fixed to `scenery_refresh`, and add private logout
state for the second header. Extract the current typed outcome encoder so tests
can prove repeated `Set-Cookie` values. Add table-driven strict-config tests and
update only the documentation surfaces named by this plan.

## Concrete Steps

1. Add the fixed legacy name and current-first accepted-name array in `auth/standard_config.go`, with a removal pointer to this plan.
2. Add a presence-sensitive cookie lookup helper in `auth/standard.go`; use it in HTTP decode and `refreshTokenFromParams` while retaining explicit non-empty parameter precedence.
3. Build current-first clearing cookies in `auth/standard_service.go`, preserve `LogoutResponse.SetCookie`, and add each logout cookie separately in the typed outcome encoder.
4. Add `auth/standard_cookie_test.go`, extend `internal/app/root_test.go` for all ten removed auth fields, and add a real repeated-`Set-Cookie` transport test in `runtime/server_test.go`.
5. Update `docs/local-contract.md`, `docs/runbooks/standard-auth-migration.md`, `docs/environment.md`, `docs/environment.registry.json`, current orientation in plan 0099, `docs/plans/completed.md`, `docs/plans/active.md`, and `docs/knowledge.json`.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/scenery` without installing a shared binary or
starting a dev server:

    gofmt -w auth/standard_config.go auth/standard.go auth/standard_sessions.go auth/standard_service.go auth/standard_cookie_test.go internal/app/root_test.go internal/envpolicy/scan.go runtime/server_test.go
    go test ./auth ./internal/app ./internal/envpolicy ./runtime
    go test ./...
    go test ./cmd/scenery
    go vet ./...
    go run ./cmd/scenery inspect docs -o json
    go run ./cmd/scenery harness self --summary --write

Acceptance requires current-only, legacy-only, both-present, empty/invalid
current, and explicit-parameter selection coverage; current-only issuance; two
separate current-first logout `Set-Cookie` values with matching attributes; and
strict rejection of `auth.refresh_cookie_name` plus every removed auth/Google
`*_env` field.

## Idempotence and Recovery

All source edits are Git-tracked and can be reapplied safely. Focused tests do
not require a live database. If a validation command fails, fix the owning
change and rerun it; do not alter config, skip tests, install a shared CLI, or
start a dev runtime to bypass a failure.

## Artifacts and Notes

Durable evidence is the focused test file, strict-config table, repeated-header
runtime test, live HTTP verification capture, final tracked diff searches, docs
inspection output, self-harness summary, and the corrective implementation
report. The temporary `onlv_refresh` production references must remain limited
to the closed compatibility set until the follow-up removal.

## Interfaces and Dependencies

Use `net/http` cookie parsing and existing runtime contract response types. Add
no dependency, public field, config/schema field, environment alias, codegen
surface, or compatibility name beyond the fixed `onlv_refresh` exception.
