# Local Filesystem Storage Promotion and Complete ZeroFS Removal

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery's storage capability (`scenery.sh/storage`) currently has two backends: a local filesystem store (`internal/storage/local.go`) and a ZeroFS-backed store (`internal/storage/zerofs.go`) that talks 9P over a Unix socket to a managed ZeroFS process spawned by `scenery up`.

The ZeroFS path was reviewed on 2026-07-02 and is not a credible production storage path for the intended deployment shape (one server, CRM/ERP-style document storage, optional S3 offsite copy):

* The adapter's `syncP9File`/`syncP9Dir` are intentional no-ops because ZeroFS 1.2.5 returns `EREMCHG` for FSync, so acknowledged writes have no durability barrier.
* `Head` computes ETags by SHA-256 hashing entire object bodies over 9P; `Get` therefore reads every object twice and `List` is O(total stored bytes) per call.
* The generated ZeroFS config hardcodes a deterministic local-dev encryption password and a 10GB disk cache, and has no S3 backend wiring at all.
* The pinned ZeroFS artifact is AGPL-3.0 and gated from production recommendation in `docs/zerofs-legal.md`.
* ZeroFS's encrypted LSM causes large on-disk amplification (observed: 2.3GB of objects using >10GB on disk) for zero benefit when a real local disk is available.

Meanwhile the local filesystem backend is the most correct backend in the repo: temp-file + rename atomic writes, checked fsync on objects, metadata sidecars, and directory syncs. The only thing keeping it out of production is a policy gate: `validateHeadlessStorageRuntimeConfig` in `cmd/scenery/storage.go` rejects every headless store kind except `"proxy"`.

This plan does two things in one coherent change series:

1. Promote the local filesystem backend to a production-supported storage kind for both app config (`storage.stores.<name>.kind: "local"`) and headless runtime config (`SCENERY_STORAGE_CONFIG` stores with `kind: "local"` and an explicit `root`), with documented offsite replication to S3 via `rclone sync` or `restic` on a timer (operator recipe, not a new Scenery subsystem).
2. Remove ZeroFS from the repository completely: the 9P adapter, the managed dev-service supervisor, the toolchain artifact, the `github.com/hugelgupf/p9` dependency, the doctor/inspect/harness/docs/schema surfaces, and the `zerofs` config kinds.

When this plan is done, `scenery up`, `scenery serve`, and `scenery worker` serve declared storage from a plain directory tree, the word "zerofs" no longer appears in the repository outside historical ExecPlans, and the docs describe a supported single-server production posture with async S3 replication.

## Progress

* [x] 2026-07-02: Reviewed the ZeroFS adapter, managed dev service, storage proxy, headless gate, and plan 0080; drafted this plan.
* [x] 2026-07-02: Registered this plan in `docs/plans/active.md` and `docs/knowledge.json`.
* [x] 2026-07-02: Milestone 1 — Config surface accepts `kind: "local"` (empty defaults to `local`); `zerofs` store kind and dev-service kind rejected; `scenery.config.v1` and `scenery.storage.inspect.v1` schemas and the `storage-basic` fixture updated.
* [x] 2026-07-02: Milestone 2 — Introduced `storageCellPlan`/`resolveStorageCellPlan` (`cmd/scenery/storage_cell.go`); managed storage proxy now backs each store with `LocalStore` under `<cell>/objects/<store>`; deleted the managed ZeroFS supervisor, evidence machinery, substrate/lease registration, readiness probe, Web UI route, and process-map entry; reshaped `inspect storage`/`storage status`/`cleanup` to report the cell path and per-store object counts/bytes.
* [x] 2026-07-02: Milestone 3 — `validateHeadlessStorageRuntimeConfig` accepts `kind: "local"` with an absolute `root` alongside `kind: "proxy"` with `proxy_socket`; fail-closed behavior preserved; ZeroFS wording removed.
* [x] 2026-07-02: Milestone 4 — Deleted `internal/storage/zerofs.go`, `cmd/scenery/dev_services_zerofs*.go`, the `zerofs` toolchain artifact (both manifests), the `github.com/hugelgupf/p9` dependency (`go mod tidy`), the doctor toolchain check, `SubstrateZeroFS`, lease messaging, the harness ZeroFS evidence example and p9 allowlist entry, the three `SCENERY_*ZEROFS*`/`CELL_ROOT` env registry+doc entries, `docs/schemas/scenery.dev.failure.v1.schema.json`, and `docs/zerofs-legal.md`.
* [x] 2026-07-02: Milestone 5 — Rewrote storage sections of `local-contract.md`, `agent-guide.md`, `SKILL.md`, `README.md`, `DSL.md`, `index.md`, `environment.md`, and `app-development-cookbook.md` (added the single-server S3 replication recipe with rclone/restic, restore drill, and sidecar note); marked plan 0080 superseded and added a historical note to 0078; synced `knowledge.json`.
* [x] 2026-07-02: Milestone 6 — `go test ./...`, `go build ./...`, `go vet ./...` green; `go mod tidy` idempotent with p9 removed; `scenery harness self --summary --write` passed (`storage-fixture-probe` PASS including the new local-backend `scenery up`→write→`down`→restart→read durability proof; `schema-validation` 16/16). Live CLI round-trip confirmed objects are plain files with sidecars and a 27-byte object occupies 8K on disk (no amplification).

## Surprises & Discoveries

* 2026-07-02: The dev storage proxy (`cmd/scenery/storage_proxy.go`, `startManagedStorageProxy`) is the only app-facing consumer of `NewZeroFSStore`. Swapping its store construction to `NewLocalStore` is the entire data-plane change for dev sessions; the proxy HTTP contract and `SCENERY_STORAGE_CONFIG` shape stay identical.
* 2026-07-02: The CLI/non-session path (`storage.go` around line 355) already uses `storagebackend.NewLocalStoreWithOptions` against `<cellRoot>/objects/<store>`; it only *validates* that the configured kind is `zerofs`. Dev CLI behavior is therefore already local-backed, which makes the migration story simpler than it looks.
* 2026-07-02: Existing dev data written through managed ZeroFS lives inside ZeroFS's encrypted LSM under `<agent>/storage/<cell>/objects` and is unreadable without the ZeroFS binary. Anyone with data worth keeping must export it (`scenery storage get`/`ls` while on a pre-removal build) before upgrading. This is a beta, local-dev-only surface, so the plan records the breaking change instead of building an automated migrator.

## Decision Log

* Decision: Promote the local filesystem backend to production-supported instead of hardening ZeroFS.
  Rationale: The local backend already has checked fsync, atomic rename, and sidecar metadata. Hardening ZeroFS would require fixing fsync, secret management, S3 wiring, Head/List performance, and clearing the AGPL gate — to arrive at a worse version of a directory tree for the single-server CRM/ERP workload this deployment targets.
  Date/Author: 2026-07-02 / storage review with repo owner.

* Decision: Remove ZeroFS completely rather than deprecating it in place.
  Rationale: Repo engineering rules require a small, singular public surface without compatibility shims. Keeping a beta backend with a known durability gap invites accidental production use, and the AGPL toolchain artifact carries ongoing legal overhead.
  Date/Author: 2026-07-02 / repo owner.

* Decision: Offsite S3 durability is an operator recipe (rclone/restic on a timer or post-write hook), not a new Scenery replication subsystem.
  Rationale: Prefer the standard library and existing tools; a documented `rclone sync` cron is the boring, proven pattern used by self-hosted DMS/CRM systems. A native S3 store backend remains a possible future plan, explicitly out of scope here.
  Date/Author: 2026-07-02 / repo owner.

* Decision: Keep the dev storage proxy and the `kind: "proxy"` headless contract.
  Rationale: The proxy is the tenant-scoping and access boundary and the production escape hatch for operators who want a remote storage runtime. Only its backing store changes.
  Date/Author: 2026-07-02 / storage review.

* Decision: No automated migration of existing ZeroFS-encrypted dev data.
  Rationale: Managed ZeroFS was documented beta/local-dev-only; data is exportable with the storage CLI on a pre-removal build. Building a one-shot LSM decryptor is not worth the surface.
  Date/Author: 2026-07-02 / repo owner.

## Outcomes & Retrospective

Completed 2026-07-02 in one pass. ZeroFS is gone from the repository (the only remaining `zerofs`/`ZeroFS` strings outside `docs/plans/` are the deliberate migration message in `internal/app/root.go`, the migration note in `docs/local-contract.md`, and plan metadata in `docs/knowledge.json`). Storage now serves from the local filesystem backend for both `scenery up` (via the in-process proxy over local-store roots) and headless runtimes (`SCENERY_STORAGE_CONFIG` stores of `kind: "local"` with an absolute `root`, or `kind: "proxy"`).

What went smoothly: the storage proxy was the only app-facing consumer of `NewZeroFSStore`, so repointing the data plane to `LocalStore` was a small, contained change; the local backend already had checked fsync and sidecar metadata, so no backend work was needed. The app-facing `scenery.sh/storage` API and the `/v1/stores/...` proxy HTTP contract were unchanged, so app code and the fixture needed no edits beyond the store `kind`.

Surprises during execution: the `scenery.down.v1` schema listed `storage_leases_removed` as required, so removing lease reporting required editing the schema and struct together; the `scenery.dev.failure.v1` evidence machinery turned out to be ZeroFS-only, so it was deleted wholesale (schema, harness example, and knowledge-index/link references) rather than kept; the knowledge harness validates that every indexed schema/doc file exists and that markdown links resolve, so deleting `zerofs-legal.md` and the dev-failure schema required scrubbing `index.md`, `local-contract.md`, `harness-engineering.md`, and `knowledge.json` in the same change.

Follow-ups (out of scope here): a native S3 store backend remains a possible future plan if a deployment outgrows single-server local storage; `cmd/scenery/dev_supervisor.go` (1957 lines) and `doctor.go` (1086) remain over the 1000-line architecture warning threshold — this change reduced both but did not split them.

## Context and Orientation

Terms:

* Storage cell: the Scenery-owned directory `<agent-home>/agent/storage/<cell-id>/` holding `objects/`, `cache/` (ZeroFS-only, dies with this plan), and `run/`. Cell ID comes from `cfg.StorageCellID()`.
* Managed storage proxy: an HTTP-over-Unix-socket server started by the dev supervisor (`cmd/scenery/storage_proxy.go`) exposing `/v1/stores/...`; app runtimes reach it via `SCENERY_STORAGE_CONFIG` stores of `kind: "proxy"`.
* Headless gate: `validateHeadlessStorageRuntimeConfig` in `cmd/scenery/storage.go`, which today rejects any headless store kind except `"proxy"` with the message "managed ZeroFS and local roots are dev-only".

Key files:

* `internal/storage/local.go` — the local backend being promoted (atomic writes, checked fsync, sidecars). Stays, gains no new features besides what tests require.
* `internal/storage/zerofs.go`, `internal/storage/contract_test.go` (ZeroFS cases) — the 9P adapter to delete.
* `cmd/scenery/dev_services_zerofs.go`, `dev_services_zerofs_evidence.go`, `dev_services_zerofs_test.go` — managed process supervisor, evidence artifacts, tests; all deleted. `dev_services.go` keeps a much smaller storage-cell plan (paths only, no process).
* `cmd/scenery/dev_supervisor.go` — starts/stops the ZeroFS service and registers the substrate; the "Starting ZeroFS storage service" phase becomes local-store preparation only (mkdir cell dirs, start proxy).
* `cmd/scenery/storage.go` — CLI store factory, `storageCapabilityEnv`, headless gate, storage web UI status; all reference the ZeroFS plan type and kind checks.
* `cmd/scenery/storage_proxy.go` — swap `NewZeroFSStore` for `NewLocalStore` per store at `<cellRoot>/objects/<store>`.
* `cmd/scenery/doctor.go` (`storage.zerofs_toolchain` check), `cmd/scenery/inspect.go` (dev-service and substrate reporting), `cmd/scenery/agent.go` (lease release messaging), `internal/agent/types.go` (`SubstrateZeroFS`).
* `cmd/scenery/harness_self_storage.go` (`runHarnessRealZeroFSProbe`), `harness_schema.go` (ZeroFS failure-evidence example), `harness_arch.go` (p9 dependency justification).
* `internal/app/root.go` — config validation: store kind must currently be `zerofs` (line ~551); dev-service kinds allow `zerofs`.
* `scenery.toolchain.json` and `internal/toolchain/scenery.toolchain.json` — the `zerofs` artifact entries.
* `go.mod` — `github.com/hugelgupf/p9 v0.4.1` becomes removable.
* Docs: `README.md`, `SKILL.md`, `DSL.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `docs/app-development-cookbook.md`, `docs/environment.md`, `docs/environment.registry.json`, `docs/index.md`, `docs/knowledge.json`, `docs/zerofs-legal.md`, `docs/schemas/scenery.config.v1.schema.json`, `docs/schemas/scenery.storage.inspect.v1.schema.json`.
* Fixture: `testdata/apps/storage-basic/.scenery.json` declares `kind: "zerofs"`.
* Historical plans 0048, 0059, 0078, 0080, 0081, 0087, 0088 mention ZeroFS. Per root `AGENTS.md`, do not rewrite history: add a short "superseded by plan 0094; current contract lives in docs/local-contract.md" note where a reader could otherwise mistake them for current contract (0078, 0080), and leave the rest untouched.

## Milestones

Each milestone leaves `go test ./...` green.

1. **Config surface.** `storage.stores.<name>.kind` accepts `"local"` and empty (defaulting to `local`); `"zerofs"` is rejected with a clear error naming this plan. The `zerofs` dev-service kind is removed, including the implicit `name == "storage"` default in `managedZeroFSDeclared`. JSON schema files updated. Fixture app config updated to `"kind": "local"`.
2. **Dev runtime on local stores.** The dev supervisor no longer spawns any storage process. It computes the storage cell paths (cell root, `objects/`), creates them, and starts the managed storage proxy backed by `storagebackend.NewLocalStoreWithOptions(name, filepath.Join(cellRoot, "objects", name), ...)`. The substrate registration, leases, readiness probe, web UI route, evidence artifacts, and `SCENERY_ZEROFS_WEBUI_URL` are removed (the storage cell is plain directories; there is nothing to lease or probe). `scenery storage cleanup`, `storage status`, and `inspect storage` are reshaped to report the cell path, per-store object counts/bytes, and configured stores instead of substrate/lease state; the inspect schema is updated in the same change.
3. **Headless promotion.** `validateHeadlessStorageRuntimeConfig` accepts stores of `kind: "local"` with a non-empty `root` (absolute path), alongside `kind: "proxy"` with `proxy_socket`. The fail-closed rule stays: declared storage plus missing `SCENERY_STORAGE_CONFIG` still refuses to start. Error text no longer mentions ZeroFS.
4. **ZeroFS removal sweep.** Delete `internal/storage/zerofs.go`, `cmd/scenery/dev_services_zerofs*.go`, the ZeroFS branches of `contract_test.go`, `storage_test.go`, `inspect_storage_test.go`, `root_test.go`; remove the toolchain artifact from both manifests; drop `github.com/hugelgupf/p9` from `go.mod` (`go mod tidy`); remove the doctor `storage.zerofs_toolchain` check, `SubstrateZeroFS`, lease-release messaging in `agent.go`, `harness_self_storage.go` real-ZeroFS probe, the `harness_schema.go` ZeroFS evidence example, and the `harness_arch.go` p9 entry; scrub `SCENERY_STORAGE_ZEROFS_CONFIG` / `SCENERY_ZEROFS_WEBUI_ADDR` / `SCENERY_ZEROFS_WEBUI_URL` from code and `docs/environment.registry.json` / `docs/environment.md`; delete `docs/zerofs-legal.md` (record the removal in `docs/knowledge.json`).
5. **Docs and replication recipe.** Update `docs/local-contract.md` (storage kinds, headless contract, stability: local promoted to supported), `docs/agent-guide.md`, `SKILL.md`, `README.md`, `DSL.md`, `docs/index.md`. Add a "Single-server production storage with offsite S3 replication" recipe to `docs/app-development-cookbook.md`: local store root layout, example `rclone sync <root> remote:bucket --transfers 8` systemd timer / cron line, restic alternative, restore drill, and the note that sidecar metadata files must be replicated together with objects.
6. **Self-harness proof.** Replace the real-ZeroFS restart probe in `harness_self_storage.go` with a local-backend proof: write through the app route, kill and restart the dev runtime, read the object back, and verify sidecar metadata round-trip. `scenery harness self --summary --write` passes.

## Plan of Work

Work bottom-up so each commit compiles: first widen config acceptance (Milestone 1), then repoint the dev data plane (Milestone 2) while the ZeroFS files still exist, then flip the headless gate (Milestone 3), then delete ZeroFS code and tooling in one sweep (Milestone 4) — deleting last avoids a broken intermediate tree. Docs and harness (Milestones 5–6) land with the sweep in the same PR series so the repo never documents a removed backend.

The storage proxy remains the only path app runtimes use in dev sessions, so tenant scoping (`tenant_scoped` wrapper) and `IfNoneMatch` locking keep working unchanged — both already wrap any `public.Store`, including `LocalStore`.

Existing-data note for anyone upgrading a machine with a populated ZeroFS cell: before pulling this change, run `scenery storage ls --json` and `scenery storage get` (or the app's export task) on the old build to export needed objects to plain files, then re-import with `scenery storage put` on the new build. Stale cells can be deleted afterwards with `scenery storage cleanup --yes` or by removing `<agent-home>/agent/storage/<cell-id>/`.

## Concrete Steps

All commands run from the repository root.

1. `internal/app/root.go`: change store-kind validation to accept `""` and `"local"` (normalize empty to `local`), reject everything else including `zerofs` with: `storage.stores.%s.kind %q is not supported; use "local" (ZeroFS was removed in plan 0094)`. Remove `"zerofs"` from the dev-service kind switch. Update `root_test.go` cases.
2. `docs/schemas/scenery.config.v1.schema.json`: update the store `kind` enum to `["local"]`; remove the `zerofs` dev-service kind. `testdata/apps/storage-basic/.scenery.json`: set `"kind": "local"` and delete the `dev.services` storage entry if it only existed for ZeroFS.
3. `cmd/scenery/dev_services.go` + new small helper (suggested: `storage_cell.go`): replace `managedZeroFSPlan` with a `storageCellPlan{CellID, CellRoot, ObjectsDir string}` resolved from `cfg.StorageCellID()` and `localagent` paths. Keep `resolveStorageCellPlan` signature-compatible with existing call sites in `storage.go`.
4. `cmd/scenery/storage_proxy.go`: build stores with `storagebackend.NewLocalStoreWithOptions(name, filepath.Join(plan.ObjectsDir, name), storagebackend.LocalStoreOptions{MaxObjectBytes: storeCfg.MaxObjectBytes})`; drop the 9P socket requirement and the `zerofs` kind check (kind is validated at config load now).
5. `cmd/scenery/dev_supervisor.go`: replace the `ensureManagedZeroFS` phase with `ensureStorageCell` (mkdir `objects/`, 0o755) and keep `startManagedStorageProxy` ordering. Delete the `zeroFS` field, detach logic, substrate upsert, and process-map entry.
6. `cmd/scenery/storage.go`: CLI store factory keeps its `LocalStore` construction but drops the `zerofs` kind check and the ZeroFS plan dependency; `storageCapabilityEnv` drops `SCENERY_ZEROFS_WEBUI_URL`; headless validation accepts `local` stores with absolute `root`; `buildStorageWebUIResponse` is removed or reshaped into a plain storage-status response (decide during implementation; record in Decision Log).
7. `cmd/scenery/inspect.go`, `doctor.go`, `agent.go`, `internal/agent/types.go`: remove substrate/lease/toolchain reporting for ZeroFS; `inspect storage` reports cell path, stores, readiness = directories exist. Update `docs/schemas/scenery.storage.inspect.v1.schema.json` and `inspect_storage_test.go` together.
8. Delete `internal/storage/zerofs.go`, `cmd/scenery/dev_services_zerofs.go`, `dev_services_zerofs_evidence.go`, `dev_services_zerofs_test.go`; prune ZeroFS cases from `internal/storage/contract_test.go` and `cmd/scenery/storage_test.go`.
9. Remove the `zerofs` artifact from `scenery.toolchain.json` and `internal/toolchain/scenery.toolchain.json`; remove p9 from `harness_arch.go`'s dependency allowlist; `go mod tidy` (drops `github.com/hugelgupf/p9`).
10. `cmd/scenery/harness_self_storage.go`: rewrite the probe per Milestone 6; `harness_schema.go`: replace the ZeroFS failure-evidence example with a neutral managed-dev-service example or delete it if the evidence type is now unused.
11. Env registry: remove `SCENERY_STORAGE_ZEROFS_CONFIG`, `SCENERY_ZEROFS_WEBUI_ADDR`, `SCENERY_ZEROFS_WEBUI_URL` from `docs/environment.registry.json` and `docs/environment.md`; grep for remaining consumers first.
12. Docs sweep per Milestone 5; delete `docs/zerofs-legal.md`; update `docs/knowledge.json` (remove the legal doc entry, add/refresh affected doc entries, mark plan 0080 superseded by 0094); add the supersession note at the top of `docs/plans/0080-zerofs-production-readiness.md` and a pointer in `0078-scenery-storage.md`.
13. Move this plan's entry and 0080's entry appropriately in `docs/plans/active.md`; when finished, update `docs/plans/completed.md`.
14. Final grep gate: `grep -ri zerofs --exclude-dir=.git --exclude-dir=docs/plans .` returns nothing (historical ExecPlans under `docs/plans/` are the only permitted matches).

## Validation and Acceptance

For every milestone:

    go test ./...
    go test ./cmd/scenery

After Milestone 4 (removal sweep):

    go build ./...
    go mod tidy && git diff --exit-code go.mod go.sum   # tidy is idempotent, p9 gone
    grep -ri zerofs --exclude-dir=.git --exclude-dir=docs/plans . ; test $? -eq 1

After Milestone 6, substantial-change validation:

    scenery harness self --summary --write

Behavioral acceptance:

* `scenery up` on `testdata/apps/storage-basic` starts with no storage substrate process; `scenery storage put/ls/stat/get/rm` round-trips objects and metadata; objects appear as plain files with sidecars under `<agent-home>/agent/storage/<cell>/objects/<store>/`.
* `scenery serve` with declared storage and no `SCENERY_STORAGE_CONFIG` still fails closed; with `SCENERY_STORAGE_CONFIG` containing a `kind: "local"` store with an absolute `root`, it serves storage endpoints and survives a kill/restart with data intact (fsync proof: object readable after `kill -9` of the serve process immediately after a successful put response).
* An uploaded corpus occupies on-disk bytes ≈ object bytes + sidecar bytes (no multi-gigabyte amplification).
* The cookbook rclone recipe, run against a local MinIO or real bucket, replicates the store root including sidecars, and a restore into an empty root serves the same objects.

Do not run `go install ./cmd/scenery`; use the self-harness worktree-local binary per root `AGENTS.md`.

## Idempotence and Recovery

Every step is ordinary Git-tracked code/doc editing; re-running a step overwrites the same files. The milestone ordering keeps the tree compiling: config widening and data-plane repointing land before any deletion. If Milestone 4 must be split, delete test files together with the code they test in the same commit. `go mod tidy` is idempotent. If the self-harness storage proof fails mid-rewrite, the old ZeroFS probe has already been deleted — fix forward; the probe fixture app (`testdata/apps/storage-basic`) is the reproduction environment. Exported dev data (see Plan of Work) is the recovery path for any machine whose old cell data mattered.

## Artifacts and Notes

* Review evidence (2026-07-02 session): `Get` double-read via SHA-256 `Head`, no-op fsync at `internal/storage/zerofs.go:725`, hardcoded cache `disk_size_gb = 10` and deterministic encryption password at `cmd/scenery/dev_services_zerofs.go:137` and `:156`, no S3 wiring in `managedZeroFSConfigTOML`. Observed in the field: 2.3GB of uploads consuming >10GB on disk and ~250MB/s local throughput.
* Plan 0080 (`docs/plans/0080-zerofs-production-readiness.md`) recorded the fsync, AGPL, and Head/List-SHA gates; this plan supersedes it by removing the backend instead of hardening it.
* The `zerofs` toolchain artifact (v1.2.5, AGPL-3.0-only) disappears from both toolchain manifests; `docs/zerofs-legal.md` is deleted since the legal posture becomes moot.

## Interfaces and Dependencies

* `scenery.sh/storage` (app-facing API) is unchanged: `Put`, `PutFile`, `Get`, `Head`, `List`, `Delete`, `DeletePrefix`, options, and error types stay identical. App code needs no changes beyond `.scenery.json` store `kind` moving from `zerofs` to `local` (or empty).
* `SCENERY_STORAGE_CONFIG` runtime schema: `kind` values become `"local"` (requires `root`) and `"proxy"` (requires `proxy_socket`). Update `docs/local-contract.md` and any schema fixtures in the same change.
* Storage proxy HTTP contract (`/v1/stores/...`) is unchanged.
* Removed dependency: `github.com/hugelgupf/p9`. Removed toolchain artifact: `zerofs`. Removed env vars: `SCENERY_STORAGE_ZEROFS_CONFIG`, `SCENERY_ZEROFS_WEBUI_ADDR`, `SCENERY_ZEROFS_WEBUI_URL`.
* External tools referenced by docs only (not shipped, not managed): `rclone`, `restic`.
