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
- [ ] Route runtime inspection through the retained producer and prove it.
- [ ] Publish corrected Scenery, update ONLV pin, run both integration proofs.

## Surprises & Discoveries

The ONLV launcher marks runtime inspection as lifecycle but subsequently routes
it through desired-producer transition logic. The strict decoder is correct for
the recorded producer; the launcher selection is the inconsistent boundary.

## Decision Log

- 2026-09-11, Codex: use upstream persisted DevTools trust with explicit local
  hosts, preserving authentication. Keep candidate artifact decoding strict.
- 2026-09-11, Codex: retry only pre-callback scratch space/quota failures with
  bounded ordered rescans. Never retry a deletion callback or hide corruption.

## Outcomes & Retrospective

Not yet completed.

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

## Interfaces and Dependencies

No new public artifact decoder, environment knob or dependency is required.
ONLV consumes the corrected published Scenery pseudo-version after verification.
