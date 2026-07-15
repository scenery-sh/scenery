# 0118 Runtime Infrastructure Consolidation and CLI Logic Extraction

- Status: active
- Owner: scenery runtime
- Created: 2026-07-15

This ExecPlan is a living document and must be updated as work proceeds.

## Purpose / Big Picture

Land seven codebase-audit findings in one campaign: two correctness fixes, two
hot-path performance fixes, shared infrastructure kernels, and extraction of
pure business logic out of `cmd/scenery` into testable `internal/` packages.
The observable result is unchanged CLI behavior with a real concurrency bug
fixed (Symphony store migration was not actually serialized across processes),
a faster dev write path (devdash no longer rewrites its whole store file per
log line), faster contract generation on large graphs, and six large
`cmd/scenery` files reduced below or near the 1000-line architecture warning
threshold with their logic behind package boundaries and unit tests.

## Context and Orientation

`cmd/scenery` is the CLI package; repo rules prefer thin CLI files with logic
in `internal/` packages. `internal/postgresdb` owns Postgres connections;
`internal/symphony` and `internal/durable/store` are Postgres-backed stores;
`internal/devdash` is a JSON-file store shared by the dev supervisor, agent
dashboard, and harness writers; `internal/agent` owns the single-owner local
agent registry; `internal/generate` renders Go/TypeScript contracts from the
immutable compiler result. The audit that produced this plan found seven
hand-rolled atomic-write helpers, five TCP probes, a session-scoped advisory
lock taken on a different connection than the DDL it guarded, swallowed
`json.Marshal` errors feeding durable registry state, synchronous full-store
persistence on the devdash hot path, and quadratic resource rescans in
contract generation.

## Plan of Work

1. Symphony migration advisory lock actually serializes concurrent openers
   through one shared `postgresdb.Migrate` / `postgresdb.MigrateStatements`
   primitive (transaction-scoped `pg_advisory_xact_lock`); the durable store
   adopts the same primitive; the `strings.Contains("duplicate column")`
   matching is replaced by `ADD COLUMN IF NOT EXISTS`.
2. Agent registry and state persistence propagate `json.Marshal` errors
   instead of silently writing partial durable routing state; the same fix
   applies to the dev port-lease legacy migration.
3. High-frequency devdash writers (`WriteProcessOutput`, `WriteProcessEvent`,
   `WriteDevEventReturningID`) persist through the coalesced deferred-save
   path instead of rewriting the whole store file per event under the shared
   lock. Low-frequency app-record mutations stay immediate. All real writer
   processes flush via `Store.Close` on shutdown.
4. `internal/atomicfile` is the single write-temp-then-rename implementation;
   the seven hand-rolled variants (testsuite, deployplan, evolution ×2, agent,
   generate, cmd/scenery, devdash) delegate to it with their original
   durability options preserved.
5. `internal/netprobe` is the single TCP dial-reachability / bind-free
   implementation; the five cmd/scenery probes delegate to it.
6. `internal/generate` builds one `resourceIndex` per generation pass
   (byAddress, contract imports, resources-by-module) and threads it through
   the ABI computation chain, removing per-module and per-reference full
   resource rescans (`resourcesByAddress` rebuilds in
   `normalizePackageABITypeReference` were the quadratic core).
7. Extract pure logic from oversized `cmd/scenery` files into internal
   packages: Caddyfile/dnsmasq/launchd logic from `edge.go` into
   `internal/edge`, the deploy diagnostics engine from `deploy.go` into
   `internal/deploydiag`, the doctor check catalog into `internal/doctor`,
   the Victoria substrate lifecycle into `internal/victoria`, the Codex
   app-server JSON-RPC client and workflow config parsing into
   `internal/symphony`, and the validation plan engine plus glob matcher into
   `internal/validation`. CLI files keep arg parsing, dispatch, and rendering.

Non-goals and standing decisions:

- No shared JSON file-store kernel across devdash and the agent registry: the
  local agent is a single-owner process, so the registry does not need
  devdash's external-change stamp refresh; forcing one kernel would couple
  two different ownership models. Recorded here so the audit finding is not
  re-litigated.
- No behavior change to CLI surfaces, JSON envelopes, or generated artifacts;
  golden generate tests must stay byte-identical.
- `legacyIdentityMigrationFiles` in `cmd/scenery/harness_arch.go` is an
  exact-file allowlist; extractions that move legacy-identity strings must
  update it in the same change.

## Milestones

1. Correctness fixes (items 1 and 2) with regression tests — done.
2. Performance fixes (items 3 and 6) with regression tests and byte-identical
   golden output — done.
3. Infrastructure kernels (items 4 and 5) with all call sites delegated —
   done.
4. CLI extractions (item 7), one package per extraction, each leaving
   `go build ./...` and `go test ./cmd/scenery` green — done.
5. Full-repo validation and plan closeout — in progress.

## Concrete Steps

Each milestone was executed as: read the owning files and their AGENTS.md
chain, move or fix code, move or add unit tests beside the new package, run
`goimports`, `go build ./...`, `go vet` on touched packages, and
`go test <touched packages> ./cmd/scenery -count=1` until green. Live
Postgres verification for item 1 used the managed `scenery-postgres`
container via a `SCENERY_TEST_DATABASE_URL` DSN and
`go test ./internal/symphony -run TestStoreConcurrentOpensMigrateOnce -count=3`.

## Validation and Acceptance

- `go test ./...` — full suite green (42 packages).
- `scenery harness self --summary --write` using the worktree-local
  `.scenery/harness/bin/scenery` binary rebuilt after the allowlist edits.
- Acceptance: no CLI grammar, JSON envelope, or generated-artifact change;
  `internal/generate` golden tests byte-identical; the new packages
  (`atomicfile`, `netprobe`, `deploydiag`, `doctor`, `victoria`,
  `validation`, plus additions to `edge` and `symphony`) carry the moved unit
  tests.

## Idempotence and Recovery

All changes are ordinary source edits in one working tree; re-running any
validation command is safe. The extractions were performed one package at a
time so the repo stayed buildable between steps; if an extraction is found
faulty, reverting that package's files and the corresponding `cmd/scenery`
call-site edits restores the previous behavior without touching the other
items.

## Artifacts and Notes

- `.scenery/harness/self-latest.json` — latest self-harness report.
- New packages: `internal/atomicfile`, `internal/netprobe`,
  `internal/deploydiag`, `internal/doctor`, `internal/victoria`,
  `internal/validation`; new files in `internal/edge` (caddyconfig.go,
  dns.go, launchd.go), `internal/symphony` (codexclient.go,
  workflowconfig.go), `internal/postgresdb` (migrate.go),
  `internal/generate` (resource_index.go).

## Interfaces and Dependencies

- `postgresdb.Migrate(ctx, db, lockKey, apply)` and
  `postgresdb.MigrateStatements(ctx, db, lockKey, stmts)` are the migration
  primitives for every Postgres-backed store.
- `atomicfile.Write(path, data, perm, Options{SyncFile, SyncDir})` is the
  single atomic file-replace implementation.
- `netprobe.DialReachable`, `netprobe.BindFree`, `netprobe.WaitBindFree` are
  the TCP probing primitives.
- `internal/deploydiag.BuildReport(ctx, Snapshot, Deps)` is the deploy
  diagnostics engine; cmd/scenery converts status at the boundary.
- `internal/victoria.Stack` owns the Victoria substrate lifecycle; cmd/scenery
  adapts `runConsole` to `victoria.Console`.
- No new third-party dependencies were added.

## Progress

- [x] 2026-07-15 postgresdb.Migrate primitive + symphony/durable adoption + concurrent-open regression test (live Postgres verified)
- [x] 2026-07-15 Marshal error propagation in internal/agent and cmd/scenery/dev_ports.go
- [x] 2026-07-15 devdash deferred persistence for high-frequency writers + regression test
- [x] 2026-07-15 internal/atomicfile + all seven variants delegated
- [x] 2026-07-15 internal/netprobe + all five probes delegated
- [x] 2026-07-15 generate resourceIndex threading; golden tests byte-identical
- [x] 2026-07-15 edge.go extraction into internal/edge (1933 → 1422 lines)
- [x] 2026-07-15 deploy.go diagnostics extraction into internal/deploydiag (1892 → 967 lines)
- [x] 2026-07-15 doctor.go extraction into internal/doctor (1399 → 436 lines)
- [x] 2026-07-15 victoria.go extraction into internal/victoria (1117 → 418 lines)
- [x] 2026-07-15 symphony runner client/config extraction into internal/symphony (1077 → 739 lines)
- [x] 2026-07-15 validate.go plan engine extraction into internal/validation (1188 → 689 lines)
- [x] 2026-07-15 Full validation: go test ./... green (42 packages); scenery harness self --summary --write fully ok (21/21 steps)

## Surprises & Discoveries

- The symphony migration lock bug was real but masked by idempotent DDL; the
  durable store already had the correct pattern to generalize.
- devdash's deferred-save machinery existed but was dead code; only the
  writers needed rerouting.
- The devdash flush-error test relied on a missing parent directory, which
  the unified atomicfile helper now creates; the test now blocks the path
  with a regular file instead.
- `internal/sqlitedb` is an empty package (no Go files); left for a separate
  cleanup decision.
- edge helper plist option parsing is half CLI flag decoding
  (`parseEdgeHelperArgs` uses the shared cmd/scenery flag grammar), so only
  the pure XML ProgramArguments extraction and run-invocation location moved
  to `internal/edge`; the flag decode stayed in `cmd/scenery` per the
  package's ownership contract.
- deploy helper drift (`HelperDrift`/`HelperDriftFor`) turned out to be a
  shared engine surface, not a status detail: `deploy resume` and `upgrade`
  JSON payloads both embed it, so it moved into `internal/deploydiag` with the
  report instead of staying CLI-local. The TLS probe implementation stayed
  edge-owned in `cmd/scenery`; deploydiag mirrors only the outcome strings as
  snapshot types and the CLI converts at the `Deps.TLSProbe` boundary.
- The self-harness architecture check runs inside the invoked binary, so
  allowlist edits in `cmd/scenery/harness_arch.go` require rebuilding
  `.scenery/harness/bin/scenery` before the check reflects them.

## Decision Log

- 2026-07-15 (agent): `postgresdb.Migrate` takes a closure; `MigrateStatements`
  covers plain DDL lists. Symphony's ALTER TABLE list moved into the same
  statement list with `IF NOT EXISTS`.
- 2026-07-15 (agent): atomicfile durability is opt-in (`SyncFile`, `SyncDir`)
  so codegen and devdash hot paths keep their no-fsync behavior while
  deployplan/evolution/agent keep full durability.
- 2026-07-15 (agent): deferred devdash persistence accepts a bounded (≤500ms)
  loss window on crash for observability data; supervisor and harness writers
  close the store on shutdown.
- 2026-07-15 (agent): extractions keep exported, doc-commented names in the
  new packages and update call sites directly; wrapper aliases in cmd/scenery
  only where a symbol had many call sites.

## Outcomes & Retrospective

All seven items landed 2026-07-15 with full validation green (`go test ./...`
42 packages ok; `scenery harness self --summary --write` 21/21 steps ok).
Net effect: one real cross-process locking bug fixed with a live-Postgres
regression test; durable registry writes can no longer silently persist
partial JSON; the devdash hot write path no longer rewrites the store file
per event; contract generation no longer rescans all resources per type
reference; seven atomic-write copies and five TCP probes became two shared
kernels; and ~3,400 lines of pure logic moved out of `cmd/scenery` into six
internal packages with their tests. Remaining follow-ups deliberately left
out: deleting the empty `internal/sqlitedb` package, and the file-size
warnings on other large files not in this plan's scope. Retrospective note:
running extractions as parallel subagents worked when file sets were
disjoint and only the orchestrator edited this plan; the one failure was an
external session limit, recovered by resuming the same agent.
