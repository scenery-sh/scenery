# Truthful Runtime Diagnostics and Safe Recovery

This ExecPlan is a living document and must be updated as work proceeds.
Completed on 2026-09-07; retained as an immutable historical record after closure.

## Purpose / Big Picture

Finish the correctness work following detached-startup diagnostic propagation:
classify expected configuration/capability failures, remove unsafe container
recovery advice, state the limits of agent-home isolation, and make doctor and
harness summaries describe the proof actually performed. Do not redesign
substrate ownership or add another process abstraction.

## Progress

- [x] 2026-09-07: Confirmed the current checkout is clean at `60f7b313` and the
  detached-startup/optional-dotenv fixes are present.
- [x] 2026-09-07: Located remaining raw configuration errors, the `docker rm -f`
  recovery suggestion, Docker-only doctor proof, and hidden human harness warnings.
- [x] 2026-09-07: Implement narrow source-boundary classification and safe recovery checks.
- [x] 2026-09-07: Add targeted failure/redaction/no-mutation and summary tests.
- [x] 2026-09-07: Update current contracts, pass full validation and release proof,
  and close with explicit warnings and the optional external-app skip.

## Surprises & Discoveries

- Doctor's contract explicitly forbids database connections. Its Postgres check
  must truthfully report prerequisite scope, not grow into another runtime owner.
- JSON self-harness summaries already distinguish warnings; the human renderer
  currently drops that distinction. Reuse the existing summary classification.
- A different agent home still uses the global `scenery-postgres` container name.
  Current port-mismatch detection occurs only after a 30-second readiness wait.

## Decision Log

- 2026-09-07, Codex: Keep the existing diagnostic carrier and SCN8000 transport
  codes. Known invalid inputs are preconditions; unavailable Docker is a
  capability failure. Never classify arbitrary stderr by forwarding it publicly.
- 2026-09-07, Codex: Reject conflicting container port bindings before starting
  an existing container. Do not remove containers/volumes, adopt foreign state,
  rewrite credentials, or introduce a new ownership record.
- 2026-09-07, Codex: Keep doctor read-only and label its proof as prerequisites.
  Surface warnings and selected mode in human harness output using existing data.

## Outcomes & Retrospective

Completed the narrow correctness work without new dependencies, environment
knobs, ownership records or process abstractions. Expected startup input errors
use SCN8003, unavailable capabilities use SCN8004, and public database/Docker
errors omit credentials and raw command output. Existing conflicting running or
stopped containers fail after read-only inspection, before any start or mutation.
Matching port bindings are not proof of exclusive ownership.

Doctor reports prerequisite scope without connecting to databases; human harness
output preserves warning/skip evidence and selected-mode scope. The optional
external-app release check now reports a skip instead of a misleading pass.

Validation completed:

- `go test ./cmd/scenery ./internal/doctor ./internal/agent ./internal/edge`: pass.
- `go test ./...`: pass, also rerun by release harness and release gate.
- `golangci-lint run ./...`: pass, zero issues after correcting three initial
  lint findings; the release gate rerun also passed.
- `go build -o .scenery/harness/bin/scenery ./cmd/scenery`: pass.
- `.scenery/harness/bin/scenery harness self --quick --summary --write`: pass
  with warnings; selected classes were `cli-json-contract`, `go-package` and
  `release-sensitive-or-runtime`.
- `.scenery/harness/bin/scenery harness self --release --summary --write`: pass
  with warnings, including full race tests, native detached startup failures and
  success, PostgreSQL, parallel worktree databases and process-boundary probes.
  This supersedes the separately recommended default self-harness run; the gate
  also ran default mode successfully.
- Twelve new/changed exact test roots, each in 20 isolated test-binary processes:
  240 passing runs; maximum reported p95 30ms and maximum sample 30ms (Go's
  verbose elapsed-time resolution is 10ms). Both test binaries were built with
  `go test -c`; no test cache reused these executions.
- `scripts/release-gate.sh`: pass, including clean-checkout install, UI build,
  fixture startup, router safety and artifact hygiene. Logs:
  `.scenery/release-gate/20260907T104036Z`. External-app smoke was explicitly
  skipped because `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` was unset.
- `.scenery/harness/bin/scenery doctor --app-root testdata/apps/basic -o json`:
  expected exit 3, with one `app.editor_workspace` precondition (`managed go.work
  is missing`), not SCN9000. No fixture generation was performed for this check.
- `git diff --check` and `bash -n scripts/release-gate.sh`: pass.

Remaining limits are explicit: agent-home isolation does not isolate global
Docker state, doctor is not runtime proof, and the optional external-app lane
was not exercised. The release harness reported 43 document-freshness warnings
and 28 architecture warnings, including the touched `watch.go` and
`harness_self.go` size warnings; no validation errors. These unrelated broader
cleanup areas were not expanded into this correctness change. No shared CLI
installation, commit, push or destructive shared-container operation was done.

## Context and Orientation

`cmd/scenery/appenv.go`, `dev_services_postgres.go`, and startup command helpers
own source errors. `cli_diagnostic_error.go` and `dev_detach_startup.go` already
preserve structured diagnostics across processes. `internal/doctor` probes
environment prerequisites; `cmd/scenery/doctor.go` renders the result.
`harness_self_summary.go` owns the existing compact summary model, while
`harness_self.go` renders human output. `scripts/release-gate.sh` has an optional
external-app lane whose skip must not print as a passed check.

## Milestones

1. Expected errors remain actionable and redacted through the existing protocol.
2. Container mismatch is non-mutating and recovery advice respects other owners.
3. Prerequisite, skipped, warning, and runtime proof are not conflated.

## Plan of Work

Patch source errors and the existing container preflight. Add small injected
Docker/doctor tests and extend diagnostic round-trip coverage; retain the existing
native detached and PostgreSQL release probes rather than adding broad smoke
suites. Update agent guidance, local contract and the knowledge index.

## Concrete Steps

From the repository root run affected-package tests during editing. Build only
the worktree-local CLI with
`go build -o .scenery/harness/bin/scenery ./cmd/scenery`.
Refresh the oracle using
`.scenery/harness/bin/scenery harness self --quick --summary --write` and run the
exact union of its recommended commands and the validation below.

## Validation and Acceptance

Expected classes: `go-package`, `cli-json-contract`, `release-sensitive-or-runtime`.
From the repository root run `go test ./cmd/scenery`, `go test ./internal/doctor`,
`go test ./...`, `golangci-lint run ./...`,
`.scenery/harness/bin/scenery harness self --summary --write`,
`.scenery/harness/bin/scenery harness self --release --summary --write`, and
`scripts/release-gate.sh`. Release supersedes the default self-harness run.
Measure new exact Go test roots in 20 isolated repetitions; p95 must be below
100ms. The existing release detached probe provides fixture-app command proof.
The external-app gate may be skipped only when
`SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` is unset; report that explicitly.

Acceptance covers missing/malformed environment values, unavailable capability,
diagnostic identity and redaction, conflicting running/stopped container bindings
with only inspect calls, honest doctor scope, and visible skipped harness proof.
Retain existing success/retry and detached-before/after-registration tests.

## Idempotence and Recovery

Tests use injected dependencies or uniquely named disposable release resources.
Never remove/reconfigure the shared Postgres container or restart shared agents.
Preserve unrelated working-tree changes. No shared CLI install, commit, or push
is part of this task unless explicitly requested.

## Artifacts and Notes

Machine-local evidence belongs under `.scenery/harness/` and
`.scenery/release-gate/`, not in Git. Record final commands, outcomes, skips and
any unresolved limitations here before completion.

## Interfaces and Dependencies

Use only the standard library and existing repository packages. Prefer existing
diagnostic/check/summary fields; add no environment knobs, dependencies, legacy
decoders, new ownership state, or general process framework.
