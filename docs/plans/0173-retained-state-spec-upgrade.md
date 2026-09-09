# Explicit retained-state specification upgrade

This ExecPlan is a living document. Maintain Progress, Surprises & Discoveries,
Decision Log, and Outcomes as work proceeds, according to [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Allow an operator to upgrade an existing stopped worktree's retained metadata
after an unrelated Scenery specification change, without changing its canonical
root, database allocation, credentials, storage incarnation/generation or object
content. Ordinary readers remain strictly current and read-only. This completes
the ONLV runtime cutover left open by plan 0172; it is not migration from the
former shared PostgreSQL server or shared object-cell format.

## Progress

- [x] (2026-09-09 21:25Z) Receive explicit approval for a supported in-place
  upgrade and coordinated ONLV main integration; inspect live source and state.
- [x] (2026-09-09 21:27Z) Confirm ONLV main and origin already match `805ecc63`
  and contain the URL and DevTools/Oxc changes; preserve remaining dirty work.
- [x] (2026-09-09 21:32Z) Confirm the compatible CLI still reads 551 objects /
  1,618,434,235 bytes with unchanged storage identity; current CLI rejects the
  old worktree specification with SCN8003.
- [x] (2026-09-09 22:08Z) Implement the explicit preview/apply contract, payload validation,
  revision-bound private backups and interruption-safe completion.
- [x] (2026-09-09 22:08Z) Pass affected-package tests, `go test ./...`, lint,
  full default verifier and skill validation; observe a non-mutating ONLV
  preview selecting 556 metadata files.
- [x] (2026-09-09 22:10Z) Pass native storage proof, including public same-schema
  upgrade and unchanged payloads. Move unit-test fsync proof to native probes;
  focused new unit roots now report 0-60ms (not an all-root p95 audit).
- [x] (2026-09-09 22:18Z) Refresh the stale worktree PostgreSQL client fixture
  and pass native storage plus all A1-A17 worktree cases, including same-schema
  upgrade, unchanged data and verified owned-resource cleanup.
- [x] (2026-09-09 22:19Z) Save and independently verify the compatible-binary
  ONLV snapshot; record 116 tables / 67,684 rows and 551 objects /
  1,618,434,235 bytes. ONLV main and origin now match clean `50b5d394`.
- [x] (2026-09-09 22:21Z) Repeat the repository Go suite and lint successfully
  after the final code changes; update current agent and operator guidance.
- [ ] Install a committed, published Scenery main.
- [ ] Back up ONLV, execute the reviewed in-place upgrade, refresh disposable
  toolchain/build artifacts, and verify its real runtime and preserved data.
- [ ] Publish only task-owned ONLV handoff changes and close plans 0172/0173
  after real application acceptance, keeping unrelated work intact.

## Surprises & Discoveries

The global specification changed from `7c49f13c...` to `a3a617ba...` for HTTP
directory groups, while the private worktree/storage payload schemas did not.
Current worktree, storage owner/generation and object-reference readers reject
the old specification. Toolchain install receipts are also current-only, but
their existing `scenery system toolchain sync` repairs disposable installs;
they do not belong in the retained-data migration transaction.

The Git blocker recorded in plan 0172 was resolved independently before this
work resumed: ONLV merge `805ecc63` is already on origin/main. Do not replay that
merge or sweep remaining Designer, Sales or theme changes into this task.

The stopped worktree session registry also carries specification identity and
must participate in the same transaction. Its historical nested payload stays
unchanged, and verified recorded live processes block the operation. Existing
control directories/files can be 0755/0644 beneath the 0700 root; require owned,
non-symlink, non-other-writable descendants while keeping backups owner-only.

Real fsync made several new transaction unit roots exceed 100ms in an ordinary
run. Unit tests now inject durability boundaries while preserving atomic-file
and interruption semantics; the public native CLI probes exercise real fsync.
Owned unpublished owner/generation metadata temp files do not block retries.

## Decision Log

- Decision: Add `scenery worktree upgrade` with read-only preview by default and
  explicit `--yes --expect-revision <digest>` application.
  Rationale: Updating retained state is an operator action, not a side effect of
  `up`, `inspect`, ordinary decoding or client regeneration.
  Date/author: 2026-09-09, Petr and Codex.
- Decision: Support only identical artifact kind/schema and valid current
  payload invariants; replace specification/producer identity, not payload.
  Rationale: Structural or semantic data-format changes require their own
  migration. Retained-state semantic changes must rotate the owning descriptor
  even when field names are unchanged. No compatibility runtime decoder is added.
  Date/author: 2026-09-09, Codex.
- Decision: Keep a root-bound, owner-only transaction with exact before bytes,
  expected after bytes, checksums and durable progress; block ordinary worktree
  access while it is incomplete. Acquire the existing live/operation locks and
  storage maintenance exclusion before planning or applying.
  Rationale: Stale previews, concurrent writers, interrupted publication and
  unsupported/corrupt records must fail without inventing ownership or data.
  Date/author: 2026-09-09, Codex.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

`internal/agent/worktree_record.go` owns retained root and PostgreSQL authority;
`worktree_paths.go` owns its locks. `internal/storagefs` owns the anchored private
namespace, owner/generation records, references and durable I/O. `internal/machine`
owns exact identity validation. `cmd/scenery/worktree.go` dispatches the worktree
CLI, while the command-data registry and checked schemas own JSON output.

The narrow transaction helper belongs in `internal/stateupgrade`; it publishes
already domain-validated metadata beneath one verified private worktree root.
It must not discover app identity, convert database content or own ordinary
runtime decoding. Agent/storage owners validate payloads before that helper is
given any changes. `scripts/verify` owns real-process acceptance.

ONLV stays at `/Users/petrbrazdil/Repos/onlv`, using sibling `../scenery` in
`go.mod`, with its browser origin at `http://localhost:4920`. The installed
starting producer is `2bf13111`. A compatible pre-upgrade binary is retained in
the private in-place migration backup; use it for old-state inspection/backups,
not as an alternate production path.

## Milestones

1. Strict same-schema preparation and ownership-preserving transaction tests.
2. Preview/apply JSON contract with stale-plan, unsafe-path and recovery proof.
3. Public native upgrade proof, published binary, and verified ONLV cutover.

## Plan of Work

Add a pure explicit identity-upgrade helper without weakening `DecodeArtifact`.
Retained owners prepare validated changes and reject schema changes, malformed
headers/payloads, foreign ownership, unsafe files, non-ready storage and pending
restore/purge operations. Inventory metadata in retained generations without
rewriting any object version payload. Preview neither creates state nor writes
backups. Its digest binds the complete selected metadata and target identity.

Apply reacquires stopped-owner and mutation exclusion, compares the complete
preview, durably backs up exact metadata, writes a pending journal, and applies
atomic per-file replacements. Publish worktree identity before storage metadata
so an old CLI cannot restart into partially upgraded state; a pending journal
blocks current readers. Retry accepts only each recorded before or after digest,
completes the same transaction, and retains backups. No silent rollback or
automatic historical-format import is introduced.

Keep public JSON summaries bounded and free of credentials/object contents.
Refresh disposable toolchain installs with the existing command after state
upgrade. Capture a private current baseline/backup with the compatible binary
before applying to ONLV; compare data/ownership before and after, then test real
grouped catalog requests and unchanged root-package APIs at the preserved origin.

## Concrete Steps

From the Scenery root:

    go test ./internal/machine ./internal/stateupgrade ./internal/agent ./internal/storagefs ./cmd/scenery ./scripts/verify
    go test ./internal/edge
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write
    go run ./scripts/verify --probe worktree --probe storage --summary --write

Inspect the changed-area union and run any additional named command it selects.
After exact-path staging, commit/push main and use the previously authorized
`go install ./cmd/scenery`; verify installed producer against HEAD.

From ONLV after preserving a compatible-binary backup and data inventory:

    scenery worktree upgrade -o json
    scenery worktree upgrade --yes --expect-revision <reviewed-digest> -o json
    scenery inspect storage --stats -o json
    scenery system toolchain sync --tool node -o json
    scenery generate -o json
    scenery check -o json
    scenery generate --check -o json
    go test ./...
    just repo-harness
    scenery build --target development --output .scenery/build/clean-tech-verified -o json
    scenery harness -o json --write
    scenery up --detach --wait ready -o json

Run lint/typecheck/build in both declared client roots if regeneration changes
their tracked clients. Record unchanged generation as the explicit skip evidence
otherwise. Exercise live AHJ/Tariff reads, catalog navigation and an unchanged
root-package API; do not substitute standalone mocked tests for that acceptance.

## Validation and Acceptance

Expected classes are CLI JSON, machine artifacts and runtime/storage. The full
self-harness supersedes quick. Named `worktree` and `storage` probes must retain
their existing assertions, add the public upgrade/restart boundary and clean up
only their owned resources. Ordinary tests remain in-process and each exact root
must remain below the existing 100ms budget. No benchmark, all-root timing audit,
deployment or full release certification is selected by this request.

Prove: preview has zero filesystem writes; current-only reads still reject old
specs; equal-schema upgrade preserves payload/credentials/ETags/content and root,
incarnation and generation; schema drift, invalid producer, malformed payload,
stale revision, foreign/nonprivate/symlink state, live owner and interrupted
storage operations fail closed. Interrupted backup/publication resumes only its
original checksummed input and never exposes an apparently empty new allocation.

ONLV acceptance requires current CLI state inspection, successful readiness and
live browser/API reads, preserved baseline data/identities, fresh projections,
and published task-owned changes. Record independent concurrent failures without
repairing unrelated UI work or claiming it was validated by this migration.

## Idempotence and Recovery

Do not mutate ONLV state until disposable failure-cut tests and native proof
pass. Keep the matching old binary and independent backup outside the checkout.
Never change agent home, root, app ID, database name, Docker ownership or storage
namespace to evade identity checks. Preview again after any source change; a
recorded interrupted transaction resumes with its original digest and matching
target CLI. Unknown bytes or a different target specification block resumption.
Retain transaction backups after completion; data retirement is separately
authorized. Do not start the old runtime after an applied upgrade.

## Artifacts and Notes

At 2026-09-09 21:32Z the old compatible CLI reports worktree key
`dbe32ecb1fa34268659d51ec368c56d33664eb026d04224f738bb7fd7b30f14c`, incarnation
`bac96b3fe6b291f45d8b3678929b00d9`, generation
`95c8a9657260be37519cf3278674ed4a`, 551 objects and 1,618,434,235 bytes. These are
observations to recheck, not a backup or complete database proof.

The independent snapshot at
`/Users/petrbrazdil/Backups/onlv/2026-09-10-spec-upgrade/pre-upgrade.zip`
verified SHA-256
`10423e008eee0aa2d0e84dfa6e6262b2f1dcdc9f65d1f2ba9ec17af633e1839d`.
The adjacent private `database-before.json` records per-table row counts and
logical checksums, sequence state, roles, grants and extensions. This fresh
67,684-row baseline supersedes the earlier migration's 64,951-row observation;
the application legitimately changed data between those captures.

## Interfaces and Dependencies

No new external dependency or environment variable is needed. The command adds
one checked current JSON payload. Private transaction metadata stays owner-only
and contains no app-data migration semantics. Domain ownership remains in agent
and storagefs; strict ordinary decoders and the one current runtime stay intact.
