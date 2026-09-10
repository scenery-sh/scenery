# Integration Recovery Boundaries

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as work proceeds.

## Purpose / Big Picture

Address the review of Scenery f5f98934 and ONLV 040064ad: authenticate DevTools,
retain storage cleanup under disk pressure, and inspect the retained framework
across a specification change. This plan follows [PLANS.md](../../PLANS.md).

## Progress

- [x] (2026-09-11) Confirmed the three source boundaries.
- [x] (2026-09-11) Restore DevTools authentication; real Chrome proves five trust/host boundaries.
- [x] (2026-09-11) Add bounded non-spilling reclamation on scratch ENOSPC/EDQUOT; targeted tests, repository tests, lint and storage fixture pass.
- [x] (2026-09-11) Route runtime inspection through the retained producer; valid-B and malformed-config inspection/control pass.
- [x] (2026-09-11) Publish corrected Scenery de2d8102, pin ONLV to v0.3.7-0.20260910222939-de2d81028baf, and pass both integration proofs.

## Surprises & Discoveries

The ONLV launcher marks runtime inspection as lifecycle but subsequently routes
it through desired-producer transition logic. The strict decoder is correct for
the recorded producer; the launcher selection is the inconsistent boundary.

The first storage-probe run passed its real-process storage checks but failed
the knowledge check because this plan omitted the living-document statement.
After adding it, the full verifier and the repeated storage probe passed with
existing freshness/architecture warnings. The default cached-suite duration
warning is advisory and is not isolated-root timing certification.

The merge failure test composes a real two-run merge-output write failure with
the reclamation failure seam. It avoids hundreds of filesystem operations in
an ordinary Go test; it is not a claim that a real filesystem was filled.

## Decision Log

- 2026-09-11, Codex: use upstream persisted DevTools trust with explicit local
  hosts, preserving authentication. Keep candidate artifact decoding strict.
- 2026-09-11, Codex: retry only pre-callback scratch space/quota failures with
  bounded ordered rescans. Never retry a deletion callback or hide corruption.

## Outcomes & Retrospective

The three reviewed boundaries are fixed without compatibility decoders or new
dependencies. DevTools uses first-code authentication and persisted browser
trust; five actual browser/server checks pass. Scratch space/quota failures
preserve normal selection revisions, live payloads and partial-progress truth.
Retained runtime inspection uses A even after B is selected and desired config
is malformed. The 4070 fixture was restored with its data; the independent
published checkout used port 4263 and passed API, Chrome and restart acceptance.

Scenery implementation is published as de2d8102. ONLV implementation is committed
as 93c6c276; 0694aa5c adds an explicit A/B specification inequality assertion.
Final documentation commits do not alter those runtime inputs. Full release,
benchmarks, all-root timing and unrelated frontend visual QA were not selected.

## Context and Orientation

Scenery `internal/storagefs/reclaim.go` selects material before durable deletion.
`ordered.go` owns scratch sorting. ONLV `scripts/scenery` chooses the producer;
`development/prove-integration-boundaries.ts` verifies published and retained
environments. ONLV `apps/nextnext/vite.config.ts` configures DevTools trust.

## Milestones

First restore browser trust, then implement disk-pressure fallback and producer
dispatch. Finally verify the published dependency in an isolated ONLV fixture.

## Plan of Work

Use existing disk IO failure injection to cover scratch creation, extension and
merge failures. Compare the selection digest with normal sorting, preserve live
objects and check partial deletion results. Add runtime framework inspection to
both valid-new-config and malformed-config phases of the retained proof.

## Concrete Steps

From Scenery run `go test ./internal/storagefs ./internal/build ./cmd/scenery`,
`go test ./...`, `golangci-lint run ./...`, and
`go run ./scripts/verify --summary --write`. Run the external storage boundary
with `go run ./scripts/verify --probe storage --summary --write`.
From ONLV run `bun test development`, the development TypeScript check,
`just check-app nextnext`, `just check-harness`, and `just repo-harness`.

## Validation and Acceptance

Scratch ENOSPC and EDQUOT must preserve normal preview identity and permit
reclamation without deleting live payloads. Other failures propagate. DevTools
must reject unauthorized RPC and foreign origins, while trusted reload works.
Run `bun development/prove-integration-boundaries.ts --published` in a fresh
replacement-free checkout and `--retained` in the owned 4070 fixture. Both must
pass; the latter must inspect A after B has a different spec and malformed config.
Release certification and timing audits are unselected, not claimed passed.

## Idempotence and Recovery

Preserve fixture data and restore temporary config/module edits in finally.
Remove incomplete sorter runs before bounded rescanning. Keep maintenance
ownership and all durable deletion ordering unchanged.

## Artifacts and Notes

Integration evidence remains in each fixture's ignored `.scenery/harness/`.

- `go test ./internal/storagefs ./internal/build ./cmd/scenery`: pass.
- `go test ./...`: pass.
- `golangci-lint run ./...`: pass, zero issues.
- `go run ./scripts/verify --summary --write`: pass with warnings.
- `go run ./scripts/verify --probe storage --summary --write`: pass with warnings
  after the plan-statement correction; storage fixture passed both attempts.
- ONLV `bun test development`: 12 tests pass.
- ONLV `apps/nextnext/node_modules/.bin/tsc -p development/tsconfig.json --noEmit`:
  pass; `bash -n scripts/scenery`: pass.
- ONLV `just check-app nextnext`, `just check-harness`, `just repo-harness`: pass.
- ONLV `node --experimental-strip-types development/prove-devtools-auth.ts`:
  five checks pass; evidence `.scenery/harness/devtools-auth.json`.
- Fresh ONLV `bun development/prove-integration-boundaries.ts --published`:
  pass from `/private/tmp/onlv-published-recovery-de2d`, without a replacement or
  previous framework selection. Includes `framework use`, `framework inspect`,
  `bun development/prepare.ts`, `check`, `build --development --verify-generation
  --target development`, `just validate development`, `just feature projects`,
  `just feature ahjs`, and `just smoke`. Evidence `published-checkout.json`.
- Existing ONLV `bun development/prove-integration-boundaries.ts --retained`:
  pass at `/Users/petrbrazdil/Repos/onlv-coherent-task-environments`, port 4070.
  Evidence `integration-boundaries.json` records runtime inspection after both
  module/config changes and malformed config, candidate refusal and shutdown.

## Interfaces and Dependencies

No new public artifact decoder, environment knob or dependency is required.
ONLV consumes the corrected published Scenery pseudo-version after verification.
