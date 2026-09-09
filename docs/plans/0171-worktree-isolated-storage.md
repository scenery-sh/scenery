# Worktree-isolated, filesystem-only storage

This ExecPlan is a living document. Maintain Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective according to `PLANS.md`.

Execution started 2026-09-08 on clean baseline
`71848cca11cb39641e94fe8ce2c16c91d019d824`, local branch
`feat/worktree-isolated-storage`. The proposal below is the approved design;
execution evidence in this document supersedes its initial not-yet-run status.

Status: completed locally on 2026-09-09. M0–M8 and S01–S24 have the evidence
recorded below: filesystem ownership/publication, SDK/CLI/HTTP, snapshots/recovery,
legacy export and the minimal existing-console storage page. Native storage proof
passes on macOS and the isolated Linux fixture; all 17 worktree cases pass after
the explicitly approved A9 acceptance correction. No real user data was migrated,
and no commit, push, shared installation or release was performed.

Repository: scenery-sh/scenery.
Source-review baseline: f24e334fb93df1bd92221761e1ee3f46acc7f994 (main, September 8, 2026), from the preceding review. Recheck the implementation at execution time; this revision does not assert that the repository has remained unchanged.

Revision: September 8, 2026 — replaces the preceding SQLite-based proposal. No SQLite, other embedded database, application-database metadata tables, or new storage dependency is authorized. This file supersedes the earlier plan, including its dependency installation, catalog transactions, indexed queries, counters, and tests.

Install this document under the next unused permanent number in docs/plans/, using the slug worktree-isolated-storage; register it in docs/plans/active.md and docs/knowledge.json. If the earlier proposal has already been registered as an active plan, update that same active file instead of allocating a competing plan. Do not rewrite a completed historical plan.

All new layouts, interfaces, flags, and assertions below are proposed work. Execute the milestones in order and update the living evidence. The plan does not authorize modifying pbrazdil/onlv, migrating real user data, installing a shared binary, pushing to GitHub, or releasing.

## Purpose / Big Picture

An agent should enter a worktree, obtain isolated file state, load an explicit database/files fixture when needed, experiment, and restore that fixture without affecting another worktree. SDK, CLI, HTTP, and console must agree on the same logical (store, tenant, key).

Keep ordinary filesystem storage. Store each payload as an immutable file. Publish one bounded JSON reference containing that payload’s identity and all object metadata through atomic file replacement. Use real cross-process filesystem locks to evaluate conditional writes. There is no mutable global object catalog, secondary index, transactional counter, general-purpose transaction log, or storage server.

The correctness boundary is one object. Whole-namespace replacement is a separate offline snapshot operation. Recursive deletion is explicitly a sequence of object deletions, not an undocumented multi-object transaction. A failed bulk operation must report partial or unknown completion honestly.

The main speed improvements are removing full-payload hashing from Head, List, and the prelude to Get; keeping output bounded; and avoiding another worktree’s scope. Filesystem listings may still scan metadata in the selected store/tenant. Do not disguise a full scan behind a promise of indexed or constant-time pagination.

## Progress

- [x] 2026-09-08: Authored the original source-grounded storage plan; implementation not performed.
- [x] 2026-09-08: Revised the design to filesystem-only storage after the user rejected a new SQLite dependency.
- [x] 2026-09-08 21:25 UTC: M0 — registered the revision, inventoried current consumers, and passed baseline quick verification; no database dependency or superseded implementation is present.
- [x] 2026-09-09: M1 — worktree ownership and stable maintenance locks; linked-root isolation, branch retention, down/prune/Git removal and orphan purge passed the storage probe.
- [x] 2026-09-09: M2 — immutable payloads and atomic references; injected publication failures and macOS/Linux process-crash fixtures passed.
- [x] 2026-09-09: M3 — logical tenant scope, bounded metadata-only pages and optional complete-scan totals; focused invariant tests passed.
- [x] 2026-09-09: M4 — SDK, CLI, private proxy, public HTTP and selector-bound destructive previews use the new filesystem protocol.
- [x] 2026-09-09: M5 — logical archives, pinned recovery barrier, independent clone/copy materialization; managed DB/files interruption and resume passed.
- [x] 2026-09-09: M6 — source-read-only converter, dry-run and runbook; synthetic export/current verification/import and source preservation passed.
- [x] 2026-09-09: M7 — schemas/docs/generated fixtures and the minimal existing-console storage page completed; Chrome tuple and scope acceptance passed.
- [x] 2026-09-09: M8 — mandatory command/probe union, all 17 worktree cases, S01–S24 evidence and owned runtime cleanup completed.

Replace planned dates with actual execution timestamps. At every stopping point record the last completed step, failing assertions, owned temporary resources, and next action.

## Surprises & Discoveries

- 2026-09-09 / Final closure: The earlier open console and A9 checkpoints below
  are historical and resolved. Chrome proved uploads/downloads, isolated sibling
  and tenant selections, honest optional totals and visible stale object/preview
  conflicts. A9 rejected the incompatible home with SCN8003 without allocation,
  then passed separate-home native migration and coexistence in one disposable
  daemon: 299 sibling requests, zero failures, preserved source/owners/grants/
  extensions and verified sandbox removal. All A1–A17 passed.
- 2026-09-09 / Approved A9 update: The developer explicitly approved replacing
  obsolete success-through-an-incompatible-home acceptance with fail-closed
  SCN8003 allocation proof and separate-home coexistence/native migration in
  the same disposable daemon. Preserve the immutable baseline, old agent/server/
  volume records, data/role/grant/extension checks, sibling sentinel and checked
  sandbox cleanup. Product migration guards and decoders remain unchanged.
  Current runbook and verifier contracts are updated; completed plan 0167 stays
  immutable. The revised probe is not passed until its native execution finishes.
- 2026-09-09 / Goal continuation and console scope: The persisted objective
  explicitly retains S06/S24. Interpret "no new storage dashboard is required"
  as no separate dashboard or unrelated redesign, not removal of the required
  storage acceptance. Implement one minimal page in the existing console with
  existing Astryx components and dashboard transport. Metadata uses storage RPC;
  file bytes stream over the same local dashboard listener with same-origin
  checks and a required custom request header. Every request pins the selected
  registered app's worktree/incarnation/generation; no generated API is added.
  The previous scope question is no longer treated as a blocker to this bounded
  implementation. No storage Chrome acceptance is claimed until it actually runs.
- 2026-09-09 / Final non-console checkpoint: The storage probe now also proves
  snapshot verify/dry-run create no target home, mixed unconditional put/delete
  process races, same-root branch retention, failed download preservation,
  down/default prune/state prune retention, real Git worktree removal, orphan
  totals and approved orphan purge without damaging the sibling. Native legacy
  conversion runs a built one-shot exporter and the prepared current CLI;
  source inode, mode, mtime, bytes and logical metadata are preserved.
- 2026-09-09 / Linux native checkpoint: Cross-built `testdata/storageprobe`
  actually ran inside an owned arm64 Linux container with no network or host
  mounts. It passed stream/task cross-process exclusion, killed upload,
  killed staged restore/pinned resume, HTTP HEAD/range, one native clone and
  one forced copy followed by source eviction. Container removal was checked.
  This is Linux process/filesystem evidence, not full Linux CLI/worktree proof
  or power-loss testing. Image and binary digest are recorded below.
- 2026-09-09 / Managed writer exclusion: The postgres probe runs public
  `db shell --app-root <root> reports -c <bounded pg_sleep query>`, observes its
  active query, then requires combined capture to fail with SCN8003 and no
  output. The shell finishes and is reaped; the subsequent combined round-trip
  and interrupted DB-complete/storage-pending recovery both pass.
- 2026-09-09 / Historical worktree failure: After refreshing the committed
  worktree-postgres TypeScript metadata fixture, `--probe worktree` reached A9
  but the immutable old runtime registry has spec revision
  `ca92e9336c4af2fa74594291ddac6ed1156ca89b4bad876cca06f31a4f35255f`
  while the candidate uses `7c49f13c1eb4b52f9866cadca408c3901ed953bce55f9e0736d9c13c17728185`.
  The candidate worktree retained `sql_allocation_checked=false`; its later
  allocator rejected checkout execution provenance with SCN8003. The storage
  change did not weaken this migration guard or add a predecessor decoder.
  A9 is failed, not waived or counted as passed. The sandbox was removed;
  private failure artifacts remain under the owned probe root.
- 2026-09-09 / Existing console proof: The root-level UI command cannot discover
  an app because the Scenery repository has no `.scenery.json`. Running against
  an authored disposable storage-basic app with its own agent home passed all
  existing dashboard routes in Chrome. `/tmp` alias selection initially caused
  dashboard control-plane HTTP 401; canonical `/private/tmp` selection passed.
  This path-alias issue is recorded, not fixed by the storage change. Screenshots
  and JSON evidence are retained under `.scenery/harness/storage-browser/`.
  The runtime stopped, process absence was checked and the temporary app removed.
- 2026-09-09 / Concurrent work preserved: In addition to the initial unrelated
  canonicalization, PostgreSQL tool and generated TypeScript changes, another
  writer changed `internal/parse/analysis.go` and `cmd/scenery/doctor_assistant*`.
  Those changes were not reverted or attributed to this storage implementation.

- 2026-09-09 / Integrated validation checkpoint: `go test ./...` and
  `go test -race ./...` passed before the final lint cleanups; the lint cleanup
  then passed `golangci-lint run ./...` with zero issues. The storage probe's
  native fixture passed SIGKILL during upload, SIGKILL after staged restore and
  pinned resume, stream/task cross-process capture exclusion, real HTTP
  HEAD/range, one native clone and one forced copy followed by source eviction.
  These results are macOS-only and do not prove power-loss behavior. The
  PostgreSQL probe now also uses its existing source-overlay checkpoint variant
  to kill after actual pg_restore completion but before storage publication.
  It passed blocked startup/stat, rejection of wrong class/digest and combined
  merge, and pinned recovery with the ordinary prepared product binary.
  The obsolete PostgreSQL fixture placed archive outputs inside agent state;
  app roots/archives now live beside the separate owned agent home. Database
  and process cleanup passed. The full worktree probe is the next running lane.
  `internal/parse/analysis.go` is another concurrent edit outside this task.
- 2026-09-09 / Native storage checkpoint: The old copied-root/shared-cell probe
  has been replaced with a disposable Git repository and linked worktree using
  one owned agent home. Public CLI proof passes unallocated inspection/listing
  without state, independent write/delete/purge, SDK-task to CLI tuple identity,
  four-process create-only and same-version races (including first allocation),
  snapshot restore into a retired incarnation and source archive eviction, and
  clean runtime restart. Native snapshot staging reported three actual clones
  and zero copies on this macOS host. Probe resources are removed only after
  checked process cleanup; no user fixture runtime or shared agent is used.
  These are partial S02-S04/S06/S08/S17/S21 assertions, not full S01-S24 closure.
- 2026-09-09 / Native race discovery: Ordinary CLI puts incorrectly reacquired
  the nonblocking worktree allocation lock on an already-ready namespace,
  returning SCN9000 to some conditional contenders. Existing namespaces now
  bind/check readiness and use their normal namespace/object leases; concurrent
  first allocation waits cancellably for the worktree operation lock. Both
  process races pass after the repair. The storage fixture now carries the
  repository's existing x/sys v0.46.0 indirect requirement/checksums; the root
  module dependency set is unchanged.
- 2026-09-09 / Failure-contract audit: Purge retirement is nullable on uncertain
  owner publication, with a matching cleanup schema/revision and pinned-retry
  test. Bulk delete/reclaim now use the checked contract's `uncertain` spelling.
  Object descriptors reserve cursor capacity so a committed tuple cannot make
  listing unable to progress. Inactive-generation cleanup is tested after
  interrupted reference removal. Snapshot commands and retained archive copying
  now observe cancellation; managed-storage headless workers hold lifetime
  maintenance leases, and managed database shells hold operation ownership.
  Existing concurrent PostgreSQL Docker/psql changes were preserved.
- 2026-09-09 / M7 scope decision pending: Step 7 explicitly forbids inventing a
  generated storage API; the required native/house fixture regeneration ran
  (native metadata/descriptor refreshed; house unchanged). Because there is no
  existing console storage page, the developer has been asked whether to add a
  minimal page in the existing dashboard or leave S24 outside this change.
  No new generated API or console page has been added.
- 2026-09-09 / Latest verification checkpoint: after the current storage schema
  fixtures and `$defs` references were corrected, `go test ./...` passes and
  `go run ./scripts/verify --quick --summary --write` passes with the same 42
  knowledge / 23 architecture warnings. This refresh includes the new exporter,
  clone/reallocation preparation code and public recovery projection. Browser
  helper/console implementation, native probes, timing and full release lanes
  remain uncompleted and are not implied by the quick result.
- 2026-09-09 / M7 contract checkpoint: Current object/list/delete/cleanup,
  inspect and snapshot response schemas now describe canonical scopes and
  explicit preview/recovery/method results; obsolete status/webui schemas and
  dispatch help are removed. The singular payload registry uses recalculated
  complete-schema revisions. The verifier now exercises storage response
  fixtures instead of leaving those schemas untested. Its first run exposed
  an unsupported old `#/properties/object` reference and missing schema copies
  in its isolated test; both were repaired using `$defs` and current fixtures.
  Focused machine/CLI/verifier tests pass. Final quick/native proof remains
  pending. Living storage/snapshot/config/environment guidance and the
  installable skill are updated; detailed migration remains in one runbook.
  The skill validator could not run with the system Python (missing PyYAML),
  and the bundled dependency lookup did not return; no dependency was installed.
- 2026-09-09 / Concurrent unrelated edits: In addition to the existing canonical
  JSON fix, another writer changed PostgreSQL shell/tool orchestration and
  repeated-response-header decoding plus its generated fixtures/conformance
  test. These changes are preserved and excluded from this task's authorship.
  Regeneration must retain their current renderer output rather than reverting
  it. No subagent was launched by this task.
- 2026-09-09 / Browser-client source correction: This execution baseline has no
  generated storage client implementation in `internal/generate`; the old
  documentation's `client.storage` claim is stale, like the absent console
  storage page. Per Step 7's explicit scope restriction, do not add a generated
  storage surface. Run the required fixture regeneration and record its actual
  diff; the earlier checkpoint's proposed compiler capability/helper addition
  was incorrect and has not been implemented.
- 2026-09-09 / M6 source checkpoint: `scripts/storage-export-legacy` now has
  a one-shot converter isolated from runtime dispatch. It reads legacy cell
  directories or explicitly decoded pre-cutover ZIPs, rejects missing/ambiguous
  metadata and reserved identities, streams current logical output, verifies
  the complete temporary archive and refuses existing output replacement.
  Directory input requires explicit all-writers-quiesced confirmation. Focused
  source-preservation, tenant mapping, malformed metadata, quiescence and unsafe
  file tests pass. Full native publication/failure evidence remains M8.
  The old snapshot writer did not record ZIP payload modification times, so
  those archives cannot recover the original object timestamps. Current policy
  fails closed and directs operators to the original directory; the operator
  question about an explicitly supplied replacement time remains unanswered.
- 2026-09-09 / M5 materialization and retirement checkpoint: Native Darwin
  `fclonefileat` and Linux `FICLONE` adapters now feed the same target digest,
  length and durability checks as streaming copy, with actual method counters.
  Unsupported/cross-device cloning selects copy; permission and I/O failures
  remain errors. Snapshot application first extracts a private disposable
  source under one lifetime file lease; its eviction cannot retain target
  references. Focused source lifecycle and injected clone tests pass; native
  clone capability and forced-copy process evidence remain unverified.
  Explicit managed overwrite can now stage a fresh incarnation after purge;
  old handles remain bound to the retired allocation and lock inodes survive.
- 2026-09-09 / Restore preparation recovery: An interrupted generation-directory
  creation previously left an incomplete inactive directory that conservative
  reclamation could not classify. Restore now records one exact `preparing`
  generation before its creation. A pinned retry may rebuild only that private
  generation before any database mutation. The old owner pointer never moves
  during preparation; ordinary access remains blocked until completion. This
  deliberately extends the existing recovery barrier to staging failures,
  including initial reallocation, instead of inferring repair authority from
  a directory name. Current schema/docs and native failure-cut proof remain
  part of M7/M8; the checkpoint does not mark those complete.
- 2026-09-09 / Verification checkpoint: `go test ./...` passes after converting
  the affected current-config and storage adapter fixtures. The quick verifier
  passes with 42 knowledge and 23 architecture warnings from the baseline.
  Its prepared worktree-local binary is available for the upcoming explicit
  probes. This does not validate the still-unimplemented clone/export/UI work
  or replace S01-S24 process and browser acceptance. Native HTTP/Unix-socket
  object tests are assigned to the storage probe; in-process adapter tests and
  storagefs protocol tests provide the ordinary test coverage.
- 2026-09-09 / M4-M5 checkpoint: The CLI now builds with logical ZIP snapshots
  supplied by `internal/snapshotarchive`, streamed per-store JSONL inventories,
  whole-archive SHA-256 pinning, and one opened input retained through apply.
  Storage capture owns live, operation and exclusive maintenance locks. A
  storage-only merge stages its effective state; combined overwrite stages
  storage before the existing PostgreSQL owner runs. Its bounded restore marker
  survives database uncertainty and generation-switch uncertainty. Focused
  archive round-trip, path-replacement, duplicate, malformed-input and restore
  failure-cut tests pass. This is not native process/crash acceptance. Native
  clone/copy materialization, retired reallocation, exporter, current schemas,
  old CLI/runtime test conversion and browser acceptance remain incomplete.
- 2026-09-09 / Current config: `storage.cell_id` and `storage.share` are removed
  from authored config and its schema. Their explicit rejection carries a
  migration diagnostic. Shared-cell runtime resolution is removed; a small
  detection-only slug function recognizes the former app-derived location.
  Explicit custom legacy cells are inputs to the forthcoming exporter, not
  active runtime aliases.
- 2026-09-08 22:20 UTC / Implementation checkpoint: The new `internal/storagefs`
  core now implements strict ownership, independent-descriptor OS locks,
  immutable versions/reference publication, scoped metadata-only keyset pages,
  exact explicit totals, selector-bound recursive deletion, conservative
  reclamation, and recorded purge retirement. SDK local storage and private
  proxy adapters are being cut over; the old sidecar SDK implementation and
  physical tenant-prefix wrapper are removed. `PutFile` is now only a package
  helper, and conditional delete has explicit options. Focused storagefs,
  storage, atomicfile, and spec tests passed; a development CLI build passed.
  Whole CLI/runtime test compilation is intentionally not yet green because
  pre-cutover storage fixtures still name removed response/config fields.
  Snapshot code still needs its M5 replacement; no milestone or S01-S24
  integration acceptance is claimed from these component tests.
- 2026-09-08 / Current-source inventory correction: `apps/console/src` has no
  storage view at this execution baseline. S24 therefore needs a minimal
  storage page inside the existing console's navigation/components, not a
  separate dashboard or visual redesign. The existing public generated client
  helpers still need their M7 metadata/precondition update.
- 2026-09-08 / HTTP boundary: Metadata now travels as one bounded base64url JSON
  map so HTTP header canonicalization cannot change metadata key identity.
  Range serving opens only the requested bytes and retries metadata races
  without reading discarded payload prefixes. The shared transport errors use
  registered SCN8006-SCN8010 identities and preserve partial-progress details.
- 2026-09-08 / M1 implementation: Added the namespace authority/lock core and
  separate worktree-aware resolver without directing the old sidecar backend
  into the new layout. Runtime adapters will switch together with the immutable
  backend in M4. Focused authority tests and `go test ./cmd/scenery` pass.
  Native file synchronization made one initial unit root approach 100 ms;
  protocol unit tests now inject the synchronization/replacement boundaries,
  while real durability and cross-process proof remain assigned to `--probe storage`.
  M1 remains in progress: runtime startup/task/worker cutover and retained-orphan
  command integration are not yet implemented.
- 2026-09-08 / Execution: No earlier worktree-isolated storage plan is registered;
  0170 is the highest existing permanent number, so this revision is 0171.
  `go.mod`/`go.sum` contain no sqlite/modernc/bbolt/badger/pebble dependency.
  Initial quick verification found only a missing living-document statement in
  this newly registered proposal; that statement is now added. The baseline
  also carries 42 knowledge and 23 architecture warnings, not new code failures.
  The consumer inventory is retained in `.scenery/harness/storage-m0-consumers.txt`.
At the reviewed baseline, cross-worktree file sharing is deliberate: Config.StorageCellID() selects an explicit cell ID or app ID; resolveStorageCellPlan selects the shared home directory. The existing probe verifies visibility across two copied fixture roots. Replace that assertion with isolation rather than preserving it as a regression requirement. R2 R6 R14

Canonical worktree identity and retained ownership already exist. PathsForWorktree hashes the canonical absolute app root; different branches at the same root do not constitute different owners. Reuse this implementation. R1 R15

The application SDK applies tenant scoping while CLI construction opens a raw local store. The current cleanup implementation removes the entire shared cell. These are scope-contract problems, not reasons to add a database. R3 R5

The current local backend hashes payloads during metadata reads and publishes data and sidecar metadata separately. Its create-only lock is process-local. The replacement must solve cross-process publication, not just move the same race into another directory. R4

The prior proposal selected a new database dependency without the user’s agreement. That choice is withdrawn. The reviewed module file has no SQLite driver, and this change must not add one or repurpose application PostgreSQL for storage metadata. R12

A combined snapshot restore can finish its database stage before failing its storage stage. This plan retains explicit roll-forward recovery and a runtime-startup barrier; filesystem-only storage does not make PostgreSQL and file publication a single transaction. R7

## Decision Log

|Date / author              |Decision                                                                      |Reason                                                                                       |
|---------------------------|------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------|
|2026-09-08 / user direction|No new database for file storage.                                             |Preserve dependency and operational simplicity.                                              |
|2026-09-08 / plan author   |Immutable payload plus one authoritative JSON reference per logical object.   |Bytes and metadata become visible at one reference-publication boundary.                     |
|2026-09-08 / plan author   |Stable namespace maintenance lock and short reference-mutation lock.          |Coordinate all processes without a lock service or process-local-only mutex.                 |
|2026-09-08 / plan author   |No global mutable index, monotonic namespace counter, or exact cached totals. |Avoid recreating a transactional database in JSON files.                                     |
|2026-09-08 / plan author   |Metadata scan plus bounded keyset output for listings.                        |Honest initial complexity; no payload reads and no unbounded model response.                 |
|2026-09-08 / plan author   |Destructive preview tokens fingerprint the selected references, not a counter.|Avoid an independently updated revision file that can disagree with objects.                 |
|2026-09-08 / plan author   |Bulk deletion may partially complete on interruption.                         |Do not introduce a generic multi-object journal to preserve an unnecessary atomicity promise.|
|2026-09-08 / plan author   |New worktrees start empty; fixtures are explicit and immutable.               |No hidden attachment to another worktree’s live data.                                        |
|2026-09-08 / plan author   |Combined DB/files restoration is offline, overwrite-only, and recoverable.    |An uncertain SQL merge is not automatically replay-safe.                                     |
|2026-09-09 / implementation|Reclaim inactive references before their payloads, synchronizing each removal; retain empty generation control directories until purge.|An interrupted reclaim remains structurally valid without another cleanup journal or guessing missing references.|
|2026-09-09 / explicit user approval|A9 must reject an incompatible shared home with SCN8003, then prove migration/coexistence in separate homes on the same disposable daemon.|Keep the current migration guard and immutable baseline; preserve source, ownership, grants, extensions, sibling sentinel and cleanup assertions.|

A design change requires a dated rationale and updated acceptance. The implementing agent must not substitute another embedded key/value store, invent an indexing engine, or add a new package dependency to evade the no-database decision.

## Outcomes & Retrospective

Completed M0–M8 locally on baseline `71848cca11cb39641e94fe8ce2c16c91d019d824`
plus the current uncommitted implementation. Unrelated concurrent edits were
preserved. No commit, push, shared install, release, ONLV edit or real storage
migration was performed. Storage remains filesystem-only, with immutable object
versions, metadata-only bounded output, explicit scope and offline pinned recovery.

### Validation receipt (2026-09-09)

| Command / proof | Observed result |
|---|---|
| `go test ./internal/storagefs ./internal/atomicfile ./internal/snapshotarchive ./storage ./runtime ./cmd/scenery` (affected commands also run separately) | Passed; publication, leases, metadata, pagination, recovery, HTTP and CLI invariants. |
| `go test ./internal/agent ./internal/edge ./internal/app ./internal/storageconfig ./internal/machine ./internal/spec ./internal/generate ./internal/compiler` | Passed; storageconfig has no test files. |
| `go test ./scripts/storage-export-legacy-src ./scripts/verify` | Passed; exporter grammar and source-only conversion plus verifier contracts. |
| Both mandatory `generate --target typescript_client.public_api` commands for native and house | Passed; native metadata refreshed, house subsequently unchanged. Existing unrelated generated runtime changes preserved. |
| Same generation for `testdata/apps/worktree-postgres` | Passed; refreshed stale metadata/descriptor that initially blocked the worktree probe. |
| `go test ./...` | Passed; rerun after relevant changes. |
| `go test -race ./...` | Passed; rerun after relevant changes. |
| `golangci-lint run ./...` | Passed with zero issues. |
| `(cd apps/console && bun run lint && bun run typecheck && bun run build)` | Passed; existing bundle-size warning remains. |
| `./scripts/build-dashboard-ui-embed.sh` | Passed. |
| `go run ./scripts/verify --summary --write` | Passed with existing knowledge/architecture warnings and advisory cached-suite wall-time warning under concurrent validation. This is not a fresh all-root timing claim. |
| `go run ./scripts/verify --quick --summary --write` | Passed after the final documentation/affected-test update; known knowledge/architecture warnings remain. |
| `go run ./scripts/verify --probe storage --summary --write` | Passed; complete owned cleanup. Final receipt copied to `.scenery/harness/storage-final-probe.json`. |
| `go run ./scripts/verify --probe postgres --summary --write` | Passed, including active managed shell exclusion and real DB-complete interruption/pinned recovery; owned clusters cleaned. |
| `go run ./scripts/verify --probe snapshot-backup --summary --write` | Passed. |
| `go run ./scripts/verify --probe ui --summary --write` | Passed dashboard builds, TS client conformance/typecheck and catalog typecheck. |
| `go run ./scripts/verify --probe fixtures --summary --write` | Passed; fixture matrix regenerated basic/storage authored projections as needed. |
| `.scenery/harness/bin/scenery harness ui --app-root <canonical disposable app> -o json --write` | Passed existing dashboard Chrome routes. Bare repository-root invocation failed app discovery; noncanonical `/tmp` invocation failed 401. Neither is reported as a pass. |
| `go run ./scripts/verify --probe worktree --summary --write` | Passed A1–A17 after the approved A9 correction; 299 sibling requests/zero failures, source and ownership preserved, checked cleanup. Receipt: `.scenery/harness/storage-final-worktree-probe.json`. |
| Chrome storage acceptance on two disposable linked worktrees | Passed S06/S24; byte-identical download, metadata, tenant/worktree isolation, explicit totals, stale object and preview conflicts, fresh preview deletion. Receipt/screenshots: `.scenery/harness/storage-browser-current/receipt.md`. |
| Linux `testdata/storageprobe` build and actual isolated-container execution | Passed; native clone=1, forced copy=1, process/crash/HTTP assertions passed; container absence confirmed. |
| `git diff --check`; `git diff -- go.mod go.sum` | Passed; root dependency diff empty. storage-basic only records the root's already-present indirect x/sys version/checksums. |

The Linux native binary SHA-256 was
`8e0e4fb0847e3d419dc1efe5d842b01d16fdcd7e60dba3bb013bcbbbf1bbfeaa`;
the existing arm64 image was
`sha256:6def1cb8d5ffa3443c527419cc13f395ab328c27bf90fcb1e80831aae4103bc3`.
It ran the probe entrypoint only, not a PostgreSQL server, with `--network none`
and no host bind mounts. Power-loss testing, full release certification,
`scripts/release-gate.sh`, fresh all-root timing and benchmarks were not selected.

### Completed assertion evidence and limits

All rows passed at their stated boundary. Test filenames below are under
`internal/storagefs/` unless qualified; native evidence is in the storage,
PostgreSQL and worktree receipts listed above.

| Assertions | Evidence |
|---|---|
| S01 | Empty root `go.mod`/`go.sum` diff; filesystem-only implementation review. |
| S02 | `namespace_test.go`; native unallocated inspection and snapshot verify/dry-run without target-home creation. |
| S03–S05 | Native linked Git worktrees: write/delete/purge isolation, same-root restart/branch retention, down/prune/Git removal and orphan purge. |
| S06 | SDK-task/CLI tuple proof, proxy/public HTTP tests and Chrome upload/stat/download parity. |
| S07 | `namespace_test.go`, `list_test.go`, SDK/runtime HTTP scope tests and dashboard strict request/pin tests. |
| S08 | `object_test.go` and native create-only, same-version and mixed unconditional put/delete process races. |
| S09–S10 | Immutable stream/ETag and zero-payload metadata tests; native HTTP HEAD/range. |
| S11–S12 | `list_test.go` bounded candidate/page/delimiter/cursor tests; Chrome default unknown and explicit complete-scan totals. |
| S13 | `publication_failure_test.go`: staged-file sync, post-rename destination/staging sync, reference publication; object rename/sync tests and native killed upload. |
| S14 | Maintenance/reclaim failure tests and native stream/task exclusion; stable locks retained through restore/purge. |
| S15 | `maintenance_test.go`, `maintenance_failure_test.go`; Chrome stale preview rejection and fresh selection deletion. |
| S16 | Failed CLI download preservation, failed/cancelled upload and stream-open lease-release tests; Chrome byte comparison. |
| S17 | Logical archive tests and native export/import, fresh versions, sibling isolation and retired-incarnation restore. |
| S18–S19 | PostgreSQL probe managed-writer exclusion, DB-complete interruption, startup barrier and exact pinned resume. |
| S20 | Restore/reallocation/purge owner-publication cuts and native killed staged restore/pinned recovery. |
| S21 | Clone fallback/verification tests; actual macOS and Linux clone plus forced-copy proof and source eviction. |
| S22 | Exporter tests and native synthetic source-preserving export/verify/import. |
| S23 | Retired surface rejection tests, schema verifier, fixture regeneration and current contract review. |
| S24 | Chrome screenshots/receipt, final embedded bundle `index-C1dS837C.js`; tenant/worktree reset, totals, upload/download and stale conflicts. |

The fresh `inspect storage` browser URL was checked to select session
`main-791b7b` and `page=Storage`. Both browser-fixture runtimes reported stopped
with empty sessions; OS process inspection found no fixture children. The owned
`/private/tmp/scenery-storage-console-KDapcc` directory was removed after copying
evidence. Native probes separately verified their owned resource cleanup.

Failure evidence covers the concrete injected synchronization, rename,
reference/owner publication and recovery cuts, plus process termination; it is
not arbitrary filesystem fault or power-loss proof. Linux evidence is narrower
than the full macOS CLI/storage/SQL workflow. Full release certification, release
shell gate, fresh all-root timing and benchmarks were explicitly not selected.

At completion, record actual final revision, delivered invariants, platform evidence, migrations exercised on disposable data, remaining limitations, and unexecuted acceptance. Distinguish source review, injected-failure tests, process-crash tests, and power-loss testing. None is interchangeable with the others.

## Context and Orientation

A worktree is the canonical app root already recognized by Scenery, including a standalone app directory. A namespace is the retained file state of that root. An incarnation is a random allocation identity that changes after an explicit purge/reallocation. A generation is a complete filesystem state staged for snapshot replacement. An object reference is the single authoritative JSON file selecting one immutable payload and its complete metadata. These are internal identities, not new public selectors.

Read AGENTS.md, PLANS.md, affected sections of ARCHITECTURE.md, docs/local-contract.md, docs/agent-guide.md, docs/tech-debt.md, and docs/plans/active.md. Apply relevant child AGENTS.md files, particularly internal/agent, internal/machine, internal/spec, internal/generate, scripts/verify, and apps/console. Do not spawn subagents or install a shared binary. R8 R9

|Area                    |Existing edit points                                                                                                                                                |
|------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|Ownership               |`internal/agent/worktree_paths.go`, `worktree_record.go`, `worktree_server.go`; `cmd/scenery/worktree_db_server.go:commandWorktreePaths`                            |
|Storage resolution      |`cmd/scenery/storage_cell.go:resolveStorageCellPlan`; `internal/app/root.go:StorageConfig`, `StorageCellID`; `internal/storageconfig/`                              |
|SDK/backend             |`storage/storage.go`, `runtime.go`, `scope.go`, `keys.go`, `errors.go`, `http.go`                                                                                   |
|CLI and runtime adapters|`cmd/scenery/storage.go`, `inspect.go`, `storage_proxy.go`, `help.go`; `runtime/storage_http.go`                                                                    |
|Snapshot/lifecycle      |`cmd/scenery/snapshot_cli.go`, `snapshot_archive.go`, remaining `snapshot_*.go`, `worktree_down.go`, `worktree_prune.go`; `scripts/snapshot-backup.sh`              |
|Proof and consumers     |`scripts/verify/harness_self_storage.go`, `harness_probes.go`, `testdata/apps/storage-basic`; storage schemas, generated clients, console consumers discovered in M0|

Use internal/storagefs for the concrete implementation. It must not import CLI orchestration, application database provisioners, or the verifier. The public storage package adapts its values to that implementation without an import cycle. Extract current snapshot format/streaming helpers to internal/snapshotarchive only to share them with the explicit exporter; leave SQL lifecycle in its existing owner.

## Milestones

M0 — Baseline and inventory

Register this revision, inspect current Git state, identify all consumers, and record baseline failures. Prove no SQLite implementation was accidentally taken from the superseded plan. Do not remove an unrelated dependency or another agent’s work automatically.

M1 — Worktree ownership

Resolve storage through canonical worktree identity. Implement read-only discovery, explicit allocation, stable lock files, retained-orphan inspection, and namespace-incarnation checks. No object operation may fall back to the shared legacy cell.

M2 — Atomic object publication

Implement immutable payloads, complete JSON references, cross-process preconditions, consistent streams, and safe explicit reclamation. There must be one current local backend after cutover.

M3 — Logical scope and bounded inspection

Make tenant selection identical across SDK and adapters. Replace payload-hashing and tenant scan-from-page-one wrappers with one metadata enumeration implementation. Document selected-partition scan cost instead of claiming a secondary index.

M4 — Agent surface

Keep the object verbs, add explicit scope/preconditions, consolidate discovery, and implement safe download output and selector-bound destructive previews. Remove obsolete aliases and the duplicate mandatory PutFile interface method.

M5 — Snapshots and fixtures

Export logical objects, stage complete replacement generations, and recover interrupted DB/files overwrite while blocking normal operation. Materialize immutable fixtures independently in each worktree, using copy-on-write only as an optimization with copy fallback.

M6 — Explicit migration

Implement a one-shot, source-read-only export of old shared cells or old snapshot archives. Current runtime code never opens a legacy layout. Exercise only synthetic data.

M7 — Contracts and console

Update current schemas, diagnostics, config examples, generated consumers, and existing console storage views. No UI redesign or new storage dashboard is required.

M8 — Integrated proof

Run all mandatory commands and selected boundary probes, record cleanup, and close the plan only after required acceptance. Keep release certification and resource benchmarking separate.

## Plan of Work

1. Namespace ownership and layout

Use the existing worktree key and retained home:

```text
<home>/worktrees/<existing-worktree-key>/
  worktree.json                       # existing root authority, schema preserved
  storage/
    owner.json                        # app/root/user/incarnation/active generation
    maintenance.lock                  # stable inode, outside replaceable data
    mutation.lock                     # stable inode, serializes reference changes
    operation.json                    # only for pending namespace restore/purge
    generations/<generation-id>/
      generation.json                 # current format and ownership identity
      refs/<store-token>/<tenant-token>/<shard>/<key-hash>.json
      versions/<store-token>/<tenant-token>/<shard>/<key-hash>/<version-id>.data
      staging/<upload-id>

<home>/agent/storage/<legacy-cell>/    # untouched; never a current runtime root
```

The reference contains store, tenant-or-empty, exact logical key, immutable version ID, SHA-256, full object length, content type, bounded user metadata, opaque ETag, and modification time. Validate its current format and tuple against the requested address. It must never contain an arbitrary absolute payload path.

Use domain-separated SHA-256 of length-framed identifiers for filesystem tokens; for example, distinct domains for store, tenant, and key. The unscoped tenant token is distinct from every tenant digest. Use lowercase hexadecimal and a fixed shard prefix. Validate original identities in the reference rather than treating a hash collision as authorization. Return an error on a mismatched existing tuple, never overwrite it.

This mapping allows a, a/b, differently cased keys, and distinct UTF-8 keys to coexist without relying on host filename interpretation. User keys are not filesystem paths. Preserve logical path validation; bound key length to 4096 UTF-8 bytes, tenant IDs to 256 bytes, content types to 255 bytes, encoded user metadata to 16 KiB, and a complete reference to 64 KiB. Keep existing store-name constraints.

owner.json binds app ID, canonical root, worktree key, local user, incarnation, lifecycle state, and active generation. Use strict current identity handling and checked atomic writes. Separate storage ownership avoids an unrelated migration of retained PostgreSQL credentials in worktree.json. Allocate an ordinary worktree record when needed, but do not allocate SQL or mark SQL setup checked merely because files are used.

Change every storage resolver caller to pass actual app-root/worktree context. Remove authored storage.share and storage.cell_id; reject them with a migration diagnostic. Preserve storage.default, stores, access policy, tenant policy, and size limits. Never edit a user config automatically.

Inspection opens only existing records and lock files; it must not allocate anything. An unallocated declared namespace is uninitialized; its list is empty and get/stat are not-found. An allocated owner missing its generation, required directories, or lock files is corrupt/incomplete, not a fresh empty namespace.

When legacy evidence exists and no current namespace has been initialized, ordinary startup/put must fail with a migration-required diagnostic instead of silently presenting empty files. Explicit snapshot import may establish the new namespace. An operator deliberately choosing empty state must import a verified same-app empty snapshot, created using the existing save workflow in a disposable namespace. Do not add an implicit-empty fallback or remove legacy evidence to bypass the precondition.

Retain files across down, down --state, normal prune, branch changes, and Git worktree removal. Do not broaden --all to include file destruction. A reused canonical root with retained ownership is not a new empty namespace. Purging an orphan requires an exact absolute app root and verified retained authority, not current app-name or config matching.

Externally supplied headless local roots remain explicit. They use the same filesystem object format but do not acquire managed purge authority or silently redirect into a development worktree. Current local readers must reject an old external layout with the migration instruction.

2. Locks, publication, and read consistency

Use two OS-backed reader/writer locks per namespace. maintenance.lock is shared by ordinary object operations and exclusive for generation switching, reclamation, capture, and purge. mutation.lock is exclusive for reference publication/deletion and shared for a stable metadata scan or destructive preview. Acquire locks on independent, correctly owned file descriptions with context-aware nonblocking retries; never silently upgrade a shared lock. Do not reuse one file description in a way that makes concurrent goroutines bypass mutual exclusion. Reuse the project’s process-lock implementation where its guarantees fit. E3

Global order: worktree live lock when needed, worktree operation lock for retained-state changes, namespace maintenance lock, namespace mutation lock. Ordinary object operations take neither worktree lock. Already-locked helpers receive the existing lease and must not reacquire it. Never unlink or replace a lock inode during cleanup or restore.

A Put follows these steps:

1. Validate scope, key, size/metadata limits, and mutually exclusive preconditions. Acquire shared maintenance ownership and bind incarnation/generation.
2. Stream to an exclusive unique staging file; compute SHA-256 once and enforce the size limit while reading. Do not buffer the body. Observe cancellation between reads and propagate transport cancellation.
3. Flush and close the payload; publish its new, non-reused version filename within the same filesystem. Make the payload and required directory entries durable before referencing them. A partial staging file is never visible as an object.
4. Acquire the exclusive mutation lock. Recheck owner/generation and read the current reference. Evaluate IfNoneMatch or IfMatch here, including conflicts with deletes and unconditional puts from other processes.
5. Encode the complete new reference into a unique sibling temporary file, flush and close it, then atomically replace the reference and synchronize its parent directory. Release the mutation lock only after the publication outcome is established.
6. Return success after the required durability steps. Do not immediately delete the prior payload. If a failure occurs after replacement might have happened, report an uncertain outcome and retain both payloads for subsequent inspection/reclamation.

The single-reference rename is the visibility boundary; it is not a two-file overwrite of mutable data plus mutable sidecar. Atomic replacement and directory durability are distinct requirements. Keep all replacement paths on the same filesystem and implement checked file/directory synchronization on each supported platform. Do not treat rename alone, or ignored sync errors, as durable publication. E1 E2

There is no additional namespace revision, counter file, retired queue, or global list to update after the reference. That is precisely how this design avoids a hidden multi-file commit problem.

Head reads the reference, not the payload body. Get resolves the reference once, opens exactly that immutable version, checks descriptor size, and returns metadata for the same version. Keep the shared maintenance lease until the returned stream closes; all success/error paths close descriptors and release leases. A delete/replacement never edits the opened payload. Missing or truncated referenced payloads fail as corruption. Same-length out-of-band edits require explicit integrity verification; do not claim they are detected without reading bytes.

Object.SizeBytes always describes full object size. Derive transfer length separately for ranged reads and correct Content-Range. Cover zero-size objects, suffix/range handling at the HTTP boundary, offset at EOF, excessive length, negative values, HEAD without body, and unsatisfiable HTTP ranges. Head/List must read zero payload bytes, and a range download must not pre-read the entire object to calculate an ETag.

Delete checks any expected ETag under the mutation lock, removes the reference, and synchronizes its directory. The payload remains reclaimable. Unconditional absence is idempotent success; conditional absence is a precondition failure. Fresh opaque ETags change on every replacement, even with identical bytes or only metadata changes; SHA-256 remains a separate content digest.

Use traversal-resistant root-relative filesystem operations for reference, version, staging, and archive access. Hashing keys is not a substitute for guarding directory/symlink boundaries or verifying ownership. Review native-call escape paths explicitly. E4

3. Metadata listings, statistics, and tenant parity

Represent tenant selection separately from the logical key. Go SDK auth context/WithTenantID, CLI --tenant, and trusted runtime scope must reach the same backend tuple. A scoped store without a tenant fails; specifying a tenant for an unscoped store is invalid. Remove the raw physical-prefix fallback.

External application HTTP derives tenancy from authenticated context, never a user-supplied tenant override header. The private Unix proxy may carry trusted scope, but validates namespace binding and store policy. Preserve private/auth/public access controls, including denial of private stores on external routes. R5 R13

Listing acquires shared maintenance and mutation leases for one scan of the selected store/tenant’s committed references. It filters logical prefixes and optional / delimiter, not payload directory contents. Use streaming directory enumeration and a bounded candidate heap/set retaining only the smallest requested entries after the cursor, plus lookahead. Do not collect every key or use WalkDir/ReadDir in a manner that accidentally sorts or materializes an unbounded directory. Count common prefixes and objects together, deduplicate selected prefixes, and test their ordering and byte-budget behavior.

Complexity is explicit: a page can inspect all N references in its selected partition. Work is proportional to that scan, while result memory is bounded by the requested page and reference-size limits. This is not an indexed seek. Do not add a B-tree, JSON index, journal, or persistent cursor cache in this change. Check cancellation while enumerating; interruption returns an error, not a fake complete page.

Use default limit 100, maximum 1000, and a 64 KiB encoded page-data budget. Ordinary listings omit user metadata; stat returns bounded complete metadata. Determine continuation from actual lookahead; return no cursor at the exact end, including prefix-only pages and a result exactly equal to the item limit. Account for the encoded cursor in the budget and ensure one maximum-sized descriptor can always make progress.

A strict opaque cursor binds namespace incarnation, generation, store, tenant, prefix, delimiter, and last emitted logical entry/type. Reject malformed or differently scoped cursors rather than falling back to a raw key. Cursors are not access credentials. Within stable data, pages enumerate exactly once; across calls with concurrent writes, an insertion behind the cursor may require a fresh list. Restore/purge invalidates old cursors. There is no persistent snapshot service.

Default inspect storage reports ownership, readiness, store policy, and browser link without scanning all objects. Exact object count/bytes are unknown, not zero, unless explicitly requested with --stats. That option scans committed references under the same locks, reads no payload bytes, and reports totals only after a complete scan. Do not maintain asynchronously repaired or transactionally coupled counters. Update console expectations for absent totals.

4. CLI and deletion semantics

Target grammar:

```text
scenery inspect storage [--stats] [--app-root <path>] [-o human|json]
scenery storage ls <store> [--tenant <id>] [--prefix <prefix>] [--delimiter /] [--cursor <cursor>] [--limit <n>] [--app-root <path>] -o json
scenery storage stat <store> <key> [--tenant <id>] [--app-root <path>] -o json
scenery storage put <store> <key> <file|-> [--tenant <id>] [--if-absent | --if-match <etag>] [--content-type <type>] [--metadata <json-file>] [--app-root <path>] -o json
scenery storage get <store> <key> --output <file> [--tenant <id>] [--app-root <path>] -o json
scenery storage rm <store> <key> [--tenant <id>] [--if-match <etag>] [--app-root <path>] -o json
scenery storage rm <store> <prefix> --recursive [--tenant <id>] [--dry-run | --yes --expect-revision <digest>] [--app-root <path>] -o json
scenery storage cleanup [--purge] [--dry-run | --yes --expect-revision <digest>] [--app-root <path>] -o json
```

Remove storage status and storage webui dispatch, schemas, help, examples, and living-doc aliases. Discovery is inspect storage. Do not create storage branch/clone/reset/copy/sync command families or an agent MCP surface.

JSON responses carry resolved app/root/worktree, namespace incarnation/generation, and store/tenant when selected. Never expose internal encoded paths as the next action. put ... - consumes stdin; stdout remains metadata. get stages a sibling output file, closes and validates transfer length, then replaces the destination only after success. Never truncate an existing output on a failed download. Reject outputs inside managed storage/control paths.

For recursive deletion, preview is default. Return selection count/bytes, a bounded sample, and selection_revision: SHA-256 over a canonically ordered representation of the operation, normalized selector, scope, incarnation/generation, and selected references including ETags. This is an optimistic-concurrency digest, not a monotonic namespace counter or approval token. Scanning/fingerprinting costs must be documented; preview output remains bounded.

Apply reacquires shared maintenance plus exclusive mutation ownership, recomputes the selection, and compares the digest before deleting anything. A stale or differently scoped selector fails without mutation. After validation, delete references sequentially under the same lock. Interruption or I/O failure can leave a subset deleted: report confirmed progress and whether completion is uncertain, preserve payloads, and require a fresh preview before retry. Never claim bulk atomicity or blindly reuse the old token. A subsequently completed upload may create a new object after this operation; deletion does not cancel future writes.

DeletePrefix in the trusted SDK has the same per-object durability and possible partial completion, without CLI preview policy. Its documentation and errors must not imply transactional rollback. Empty CLI recursive prefixes are rejected; namespace destruction uses explicit purge.

storage cleanup only reclaims proven-unreferenced material; it does not remove live references. Preview/application computes a fingerprint of the actual eligible material. Apply takes exclusive maintenance ownership, verifies no pending restore, checks all relevant references and ownership, synchronizes reference directories before reclaiming versions, and never deletes unknown/corrupt material by inference. Group versions per logical key, read its current reference, and retain the referenced version; no global refcount table is needed. If reference validation fails, stop cleanup without treating that reference as absent.

cleanup --purge additionally requires a stopped verified worktree, with a live-lock acquisition that fails instead of implicitly stopping it. Preview fingerprints the selected namespace. Apply durably publishes the retired/purged owner state before reclaiming its generations. Keep stable lock files and the retired incarnation record. Old handles/tokens cannot delete or recreate a later allocation. External roots and legacy cells are never eligible for managed purge. If purge requires a recovery marker, bind it to the approved incarnation, generation, and selection digest before retiring the owner. A matching retry may only finish that recorded retirement/reclamation; it must not evaluate the old approval against a new allocation. A logical purge may be complete while physical reclamation needs retry; report those separately.

Use existing machine envelopes and diagnostic registration: invalid input 2; scope/ownership/configuration/precondition/recovery conflicts 3; unavailable capability 4; permission denial 5; internal errors 10 with opaque report tokens. Map corresponding HTTP conflicts consistently, including If-Match and create-only behavior. Do not use generic string matching to classify new errors.

5. Snapshots, recovery, and fixture reuse

Extend existing snapshot save/verify/load. Add --expect-sha256 <digest> to verify/load for pinned fixtures. Do not add a registry, download service, or automatic startup hydration.

The portable archive stores logical identities, metadata, and payload bytes. It does not contain locks, owner files, internal reference paths, version directories, staging material, or removed database-catalog artifacts. Retain bounded root manifests; for large object inventories use checksummed streamed per-store manifests. Validate schema identity, app ID, duplicate archive entries and logical tuples, path safety, sizes, digests, metadata bounds, and store policy before changing the target.

Import assigns fresh target ETags and a new generation. Worktree source identity is provenance, not a prohibition on importing into a different worktree of the same app. Dry-run and verify do not allocate target state or recover pending operations. Hold the same opened, validated input through apply; do not verify one file and then reopen an untrusted replacement by pathname.

For storage capture require a stopped source runtime and hold its live lock and exclusive maintenance lease. Capture database and files inside that same boundary when both are requested. Start only an already-owned managed database when necessary; do not allocate one to make backup succeed. Keep SQL-only behavior outside this change. Every Scenery-owned task/worker/direct storage write must participate in the appropriate exclusion mechanism.

Do not claim to exclude arbitrary external SQL clients or filesystem writers. A coherent combined fixture requires the supported managed owner and documented exclusion of those writers. Reject externally owned combined capture in this implementation rather than introducing an unverified assume-consistent option. scripts/snapshot-backup.sh must report the new storage precondition and never silently stop applications.

For storage replacement, stage a complete generation with references and independently owned payload files; validate and synchronize it before atomically updating the active-generation field in the owner. Storage-only merge stages the effective result and uses one generation switch, rather than editing the live tree piecemeal.

For combined DB/files overwrite, persist a bounded operation.json before the first irreversible change. It binds target/incarnation, archive digest, selected classes, staged generation, and phase. Stage/verify storage first, restore the database using its existing owner, then switch the storage generation, record completion durably, and retire the marker. An interruption retains the marker. Normal startup, object reads/writes, capture, and purge must fail with the exact resume instruction while it is pending. Only diagnostic inspection and the matching restore-resume path are allowed.

Resume requires the same archive digest and ownership. Repeat an idempotent overwrite database restore when completion is uncertain; never guess from file timestamps or clear a marker because a directory exists. Reject combined merge before mutation; it is not replay-safe merely because files can be replaced. This is one bounded lifecycle recovery record, not a general-purpose object transaction journal.

A fixture is a checksum-pinned immutable archive. A verified extracted fixture may be retained in an owner-controlled, digest-named directory with an atomic ready marker, using existing snapshot extraction logic. Keep this disposable: target namespaces receive independent clone-or-copy payloads, never symlinks, hardlinks, or live references into another worktree/cache. Cache eviction must hold the source materialization lease and must not damage already initialized worktrees. No global live blob pool, reference counter, or cache service is permitted.

Use native copy-on-write cloning only on platforms/filesystems where it succeeds; ordinary streaming copy is the required fallback. Record which method actually ran. Cross-filesystem or unsupported-clone errors select copy; permission/corruption and other real failures must not be hidden. Do not shell out to platform-dependent cp flags. Linux’s whole-file reflink operation is a possible implementation; validate platform-specific primitives before use. E5

6. Legacy-data preservation

Never automatically migrate, delete, rename, or attach a shared legacy cell. Implement scripts/storage-export-legacy as a one-shot source-read-only converter, not a legacy backend option. It reads the old raw payload/sidecar layout or a pre-cutover archive, maps known tenant prefixes back to explicit logical tuples, and emits the current logical snapshot format.

Require explicit source quiescence for directory export. Reject malformed sidecars, ambiguous reserved keys, unsafe paths, duplicate identities, and invalid lengths; do not invent lost metadata. Build the output beside its destination, verify it completely, then publish it. Failed export cannot publish a valid-looking partial archive or alter source bytes/permissions. A read may affect filesystem access times; source-preservation assertions should not falsely require atime stability.

Document the operator sequence: stop all legacy writers; export/verify; preserve the source and backup; remove retired config fields intentionally; load into an explicitly selected worktree; verify logical object and database references before resuming use. Source rollback does not make an old binary compatible with the new filesystem layout. Do not introduce an implicit old decoder to make rollback appear safe.

7. Console and current contracts

Update the existing console storage view to display worktree and tenant, use scope-bound query-cache keys, handle unknown totals, and offer explicit totals computation. Preserve upload/list/download functionality and show stale-version conflicts and recursive previews. Do not expose raw store directories as the normal interface or let a public tenant header become an administrative override.

Update current config/CLI/snapshot/environment schemas, machine identity registrations, diagnostic catalog, SKILL.md, README, local contract, agent guide, cookbook, architecture map, and knowledge index. Remove obsolete SCENERY_STORAGE_CELL_ID uses if no current consumer needs them; use the existing structured storage runtime configuration for scope, not new environment knobs.

Regenerate clients through their owning generators. Do not hand-edit generated output or modify immutable completed plans to erase historical sharing behavior. Add the new migration runbook and explicit listing/bulk-delete limitations to living docs.

## Concrete Steps

All commands below run from the Scenery repository root unless a subshell shows another working directory. Use the current checkout; do not assume a developer-specific absolute path.

Step 0 — Register and inventory

Record git status --short and git rev-parse HEAD. Allocate a plan number only if this plan has not already been registered. Read the rule files and run:

```sh
rg -n 'StorageCellID|resolveStorageCellPlan|storageStoreForCLI|SCENERY_STORAGE_CELL_ID' cmd internal storage runtime scripts testdata docs SKILL.md README.md
rg -n 'NewLocalStore|PutFile|DeletePrefix|IfNoneMatch|__scenery/tenants' storage runtime cmd internal scripts testdata
rg -n 'storage (status|webui)|storage\.status|storage\.webui' cmd internal apps docs scripts SKILL.md README.md
rg -n 'storage|Storage' apps/console/src internal/generate
rg -n 'sqlite|modernc|bbolt|badger|pebble' go.mod go.sum

go run ./scripts/verify --quick --summary --write
```

An empty dependency search is expected, not a shell-script failure. If the superseded design was partially implemented, inventory it and replace only the storage work; do not erase unrelated changes. Do not run any go get command from the old plan.

Step 1 — Ownership and exclusion

Implement the worktree-aware resolver, storage-specific owner, non-creating read paths, incarnation checks, and stable lock order. Add tests for root aliases, two roots, path reuse, orphan selection, old-cell detection, owner corruption, and missing generation. Wire task/worker paths so none bypass maintenance ownership.

```sh
go test ./internal/agent
go test ./internal/edge
go test ./cmd/scenery
```

Step 2 — Filesystem implementation

Create internal/storagefs with focused files for format/paths, references, locks, payload streaming, listing, and maintenance. Keep interfaces concrete and small. Implement the publication sequence before removing the old backend. Inject filesystem and synchronization boundaries for deterministic error tests; do not add production test flags or turn off sync globally.

Test every cut point: staging, payload flush/rename, reference flush/rename, reference-directory sync, stream open, reference deletion, and physical reclamation. Ordinary tests use bounded in-process fixtures/fakes; real process/filesystem durability boundaries belong in the selected storage probe.

```sh
go test ./internal/storagefs
go test ./storage
```

Step 3 — Scope, listing, and SDK

Replace encoded tenant-prefix policy with explicit tuple selection, implement opaque ETags and conditional Put/Delete, metadata-only Head/List, coherent ranged reads, strict cursors, and explicit totals. Remove PutFile from Store and update all implementations/test doubles to use the existing package helper.

```sh
go test ./internal/storagefs
go test ./internal/storageconfig
go test ./storage
go test ./runtime
go test ./cmd/scenery
```

Step 4 — CLI and lifecycle cutover

Implement target grammar, metadata-only stdin/stdout behavior, atomic download destination, previews, partial bulk-delete reports, explicit reclaim/purge, and current diagnostics. Delete status/webui aliases and shared-cell runtime paths. Update up, retained lifecycle, proxy, tasks, workers, and external local-root handling together.

```sh
go test ./internal/app
go test ./internal/machine
go test ./internal/spec
go test ./cmd/scenery
go test ./runtime
go run ./scripts/verify --quick --summary --write
```

Step 5 — Snapshots and fixtures

Implement logical archive helpers, checksum pinning, read-only validation, complete generation staging, owner-pointer switching, and DB/files recovery. Reuse existing SQL ownership; do not build a second provisioner. Add clone-or-copy source leases and independent fixture materialization. Update backup behavior.

```sh
go test ./internal/snapshotarchive
go test ./internal/storagefs
go test ./cmd/scenery
go test ./scripts/verify
./scripts/build-dashboard-ui-embed.sh
go run ./scripts/verify --probe storage --summary --write
go run ./scripts/verify --probe postgres --summary --write
go run ./scripts/verify --probe snapshot-backup --summary --write
```

Step 6 — Migration proof

Create the converter and docs/runbooks/worktree-storage-migration.md. Required converter grammar:

```text
scripts/storage-export-legacy --source <absolute-cell> --app-id <id> --dry-run
scripts/storage-export-legacy --source <absolute-cell> --app-id <id> --confirm-source-quiesced --output <archive.zip>
scripts/storage-export-legacy --input <old-archive.zip> --output <current-archive.zip>
```

The storage probe creates actual disposable paths, executes conversion, verifies with the prepared current binary, imports into a new scope, and checks source preservation. Do not substitute real user data for a fixture.

```sh
go test ./scripts/storage-export-legacy-src
go test ./internal/snapshotarchive
go test ./scripts/verify
go run ./scripts/verify --probe storage --summary --write
```

Step 7 — Docs, consumers, and console

Update all current contracts and consumers, regenerate fixtures, and perform the existing UI checks. No unrelated UI redesign is authorized.

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go test ./internal/generate
go test ./internal/compiler
(cd apps/console && bun run lint && bun run typecheck && bun run build)
./scripts/build-dashboard-ui-embed.sh
go run ./scripts/verify --quick --summary --write
.scenery/harness/bin/scenery harness ui -o json --write
```

Run the stated regeneration commands even if inventory shows no emitted storage helper: record that finding and the clean generated diff. Do not invent an extra generated storage surface to satisfy the plan.

Step 8 — Integrated acceptance and closure

Run the union below after the final changes, review the diff for scope/dependency creep, and update progress, outcomes, indexes, and knowledge metadata. Report exact failures and unexecuted boundaries. Do not mark a milestone complete from compilation alone.

## Validation and Acceptance

Required assertion inventory

|ID |Required observable result                                                                                                                                                                     |
|---|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
|S01|No database dependency, application SQL metadata table, global mutable object index, or dependency pin from the superseded plan is introduced.                                                 |
|S02|Inspect/list of unallocated storage and snapshot dry-run/verify create no target directory, record, lock, cache, or service. Missing allocated state is not mistaken for empty state.          |
|S03|Two actual Git worktrees sharing one agent home can use the same store/tenant/key independently; writes/deletes/purge in A cannot damage B.                                                    |
|S04|Restart/branch change at the same canonical root retains files; a different root isolates them; path reuse respects retained authority.                                                        |
|S05|`down`, state cleanup, normal prune, and Git worktree removal preserve durable storage; orphan inspection and owner-checked purge still work.                                                  |
|S06|SDK, CLI, private proxy, public HTTP, and console resolve identical logical tuple semantics.                                                                                                   |
|S07|Missing/wrong tenant, forged external tenant headers, unsafe keys, private access, wrong incarnation, and malformed/cross-scope cursors fail correctly.                                        |
|S08|Competing processes doing create-only writes have exactly one success; same-version conditional competitors cannot both commit. Test races with unconditional puts/deletes too.                |
|S09|Metadata, ETag, full size, and returned bytes always belong to one immutable version, including metadata-only replacement and an overwrite-and-revert sequence.                                |
|S10|Metadata reads consume zero payload bytes; ranged GET reads no unrelated body bytes, and HEAD emits no body.                                                                                   |
|S11|Stable-data pages are deterministic, obey item/byte bounds, handle delimiter-only pages, and have no false final cursor. Large-scope enumeration retains bounded page candidates, not all keys.|
|S12|Default inspect does not scan objects or invent totals; explicit stats are exact only after a complete metadata scan, with no durable counters.                                                |
|S13|Failure at each publication/sync boundary exposes only a complete old/new object or absence; uncertainty is reported and referenced payloads are not reclaimed.                                |
|S14|Cleanup retains referenced versions and active streams/uploads; corrupt references stop reclamation; lock inode identity survives cleanup/restore/purge.                                       |
|S15|Stale/differently scoped destructive previews fail before deletion. Bulk interruption reports partial/unknown completion, and retry requires a fresh selection.                                |
|S16|Failed download does not truncate the previous output; failed upload never publishes a partial object; cancellation releases resources.                                                        |
|S17|Snapshot export/import preserves logical tuple, metadata, size, and digest; fresh import versions invalidate old ETags/cursors and remain isolated in sibling worktrees.                       |
|S18|Combined managed DB/files capture excludes supported writers; live or unsupported external combined capture is rejected before publishing a misleading fixture.                                |
|S19|Interruption after DB restore blocks startup/ordinary storage until the same pinned archive completes recovery; mismatched resume and combined merge fail before mutation.                     |
|S20|Snapshot and generation-switch failures preserve recoverable ownership and never select a generation merely by newest timestamp.                                                               |
|S21|Clone and forced copy materialization produce identical logical state; source/cache eviction cannot damage initialized worktrees; unsupported clone falls back honestly.                       |
|S22|Legacy export/import preserves synthetic source contents/permissions, detects malformed metadata, and does not publish a partial archive.                                                      |
|S23|Retired config/commands have no active aliases; schemas, diagnostics, examples, fixture generators, and JSON scope fields agree.                                                               |
|S24|Chrome acceptance shows worktree/tenant scope, no stale cross-scope cached entries, honest optional totals, upload/download, previews, and visible stale-version errors.                       |

S03 must use a disposable Git repository with two linked worktrees, not merely two copied directories. The verifier owns all probe homes, files, processes, sockets, caches, databases, and Git roots and fails if its cleanup fails. Use production/public boundaries; do not add hidden product test endpoints or environment switches for failure injection.

Fast invariant tests use bounded in-process seams and preserve the repo’s exact-root timing policy. Run cross-process lock and crash checks in the existing --probe storage; add DB integration to existing postgres/worktree probes. Process-crash evidence does not prove power-loss behavior.

Mandatory command union

Expected changed classes include Go packages, CLI JSON contracts, runtime/release-sensitive code, generated consumers, dashboard, and documentation. Refresh .scenery/harness/agent-context.json after final edits and run the exact union in changed_area.recommended_commands, plus these named boundaries. The existing probe catalog owns the command IDs. R9 R10 R11

From repository root:

```sh
./scripts/build-dashboard-ui-embed.sh

go test ./internal/storagefs
go test ./internal/snapshotarchive
go test ./internal/agent
go test ./internal/edge
go test ./internal/app
go test ./internal/storageconfig
go test ./internal/machine
go test ./internal/spec
go test ./storage
go test ./runtime
go test ./cmd/scenery
go test ./internal/generate
go test ./internal/compiler
go test ./scripts/storage-export-legacy-src
go test ./scripts/verify

go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json

go test ./...
go test -race ./...
golangci-lint run ./...

(cd apps/console && bun run lint && bun run typecheck && bun run build)
./scripts/build-dashboard-ui-embed.sh
go run ./scripts/verify --summary --write
.scenery/harness/bin/scenery harness ui -o json --write

go run ./scripts/verify --probe storage --summary --write
go run ./scripts/verify --probe worktree --summary --write
go run ./scripts/verify --probe postgres --summary --write
go run ./scripts/verify --probe snapshot-backup --summary --write
go run ./scripts/verify --probe ui --summary --write
go run ./scripts/verify --probe fixtures --summary --write

git diff --check
git diff -- go.mod go.sum
```

Do not rerun an identical already-satisfied command at an unchanged final revision merely for more output. After any subsequent relevant edit, rerun its affected proof. Missing prerequisites produce an explicitly blocked acceptance item, not a skipped-pass. Do not weaken the verifier, add timing exceptions, hide work in TestMain, or delete protective assertions to make the branch green.

Full release certification (scripts/release-gate.sh), fresh all-root timing audits, and benchmarks are not selected by this plan. Record them as not selected. They require a separate explicit human request, not an automatic extra gate.

Platform conditions are exact: execute native clone success on every available supported platform/filesystem. If the OS or filesystem does not support cloning, retain the returned capability/error evidence, run the forced-copy proof, and report native clone as unverified on that host. Run macOS/Linux cross-process lock and publication proofs before claiming both platforms verified; a non-host build alone is insufficient.

For browser acceptance use Chrome with the two disposable scopes, preserving screenshots or a trace. If browser tooling is absent, record S24 as blocked with the missing capability; a TypeScript build does not replace it.

Direct fixture workflow

The integration probe must construct a disposable storage-basic app from the current fixture, remove retired config fields in that disposable copy, and use its actual absolute root and owned agent home. This bash example assumes the probe supplies SCENERY_AGENT_HOME, FIXTURE_ROOT, and EVIDENCE_DIR; it intentionally refuses to run without them:

```sh
set -eu
: "${SCENERY_AGENT_HOME:?probe-owned home required}"
: "${FIXTURE_ROOT:?disposable fixture root required}"
: "${EVIDENCE_DIR:?probe-owned output directory required}"
SCENERY_BIN="$(pwd)/.scenery/harness/bin/scenery"
mkdir -p "$EVIDENCE_DIR"
printf 'isolated storage\n' > "$EVIDENCE_DIR/input.txt"

"$SCENERY_BIN" inspect storage --app-root "$FIXTURE_ROOT" -o json
"$SCENERY_BIN" storage put app probe/a.txt "$EVIDENCE_DIR/input.txt" \
  --tenant storage-probe --if-absent --app-root "$FIXTURE_ROOT" -o json
"$SCENERY_BIN" storage stat app probe/a.txt --tenant storage-probe \
  --app-root "$FIXTURE_ROOT" -o json
"$SCENERY_BIN" storage get app probe/a.txt --tenant storage-probe \
  --output "$EVIDENCE_DIR/output.txt" --app-root "$FIXTURE_ROOT" -o json
cmp "$EVIDENCE_DIR/input.txt" "$EVIDENCE_DIR/output.txt"
"$SCENERY_BIN" snapshot save --storage --output "$EVIDENCE_DIR/files.zip" \
  --app-root "$FIXTURE_ROOT" -o json
"$SCENERY_BIN" snapshot verify --input "$EVIDENCE_DIR/files.zip" -o json
```

This workflow does not create a DB/files consistency proof; S18/S19 require the managed SQL fixture and named probes above. It does not authorize migrating the user’s actual storage.

## Idempotence and Recovery

Allocation records an explicit intent and completes only the same verified owner. Decode failures never authorize an empty replacement. Purge retains authority and a retired incarnation so stale handles cannot revive it.

A payload without a committed reference is not a visible object. An ambiguous write outcome requires inspecting the current reference; a repeated create-only request may legitimately observe that the first attempt succeeded. Never delete a payload just because its caller received an error.

Before physical reclamation, obtain exclusive maintenance ownership, verify references, and make the observed reference-directory state durable. Do not delete a payload needed by a pending restore or active stream. Preserve unknown material for diagnosis instead of treating it as garbage. Interrupted reclaim is safely repeated from a newly verified selection.

Bulk deletion is not atomic. After failure, inspect remaining references and preview again. Do not create a generic replay journal, transaction receipt database, or rollback engine solely for this operation.

Generation replacement and combined restore have a bounded lifecycle marker. Only the recorded digest/target may resume it. No live runtime or ordinary storage path may ignore pending recovery. Retain old generations until the new owner reference and completion are durable; never infer recovery from directory names or timestamps.

Legacy export is read-only on the source and atomically publishes verified output. Preserve original data and a tested portable backup before any real operator cutover. Reverting code is not a data migration.

## Artifacts and Notes

Handoff must include the living plan, actual source diff, migration runbook/exporter, current schemas/docs, generated fixture changes, dependency-diff evidence, S01–S24 assertion results, probe cleanup, and browser evidence. State which platforms and failure boundaries were actually exercised.

Do not commit payloads, runtime owner/reference files, fixture caches, user archives, credentials, generated caches, or node modules. Put evidence under the existing .scenery/harness/ structure or owned temporary roots.

The main deliberate limitations are selected-partition metadata scans, no constant-time exact totals, no generic multi-object transactions, no shared mutable cross-worktree namespace, and no global live-blob deduplication. These are design boundaries, not unfinished database features.

## Interfaces and Dependencies

Keep the app-facing interface storage-oriented:

```go
// Proposed target interface; update every implementation and caller together.
type Store interface {
    Put(context.Context, string, io.Reader, PutOptions) (*Object, error)
    Get(context.Context, string, GetOptions) (io.ReadCloser, *Object, error)
    Head(context.Context, string) (*Object, error)
    List(context.Context, ListOptions) (*ListPage, error)
    Delete(context.Context, string, DeleteOptions) error
    DeletePrefix(context.Context, string) error
}

// Add IfMatch to existing PutOptions; preserve ContentType, Metadata,
// and IfNoneMatch. IfMatch and IfNoneMatch are mutually exclusive.
type DeleteOptions struct {
    IfMatch string
}
```

Retain the package-level storage.PutFile helper and implement it using Put; remove only the duplicate interface obligation. Keep cleanup, preview/application, ownership, and snapshot administration in internal code, not new public Store methods.

internal/storagefs owns the concrete reference/payload format, locks, conditions, metadata scanning, and reclamation. internal/agent owns retained root authority. CLI/runtime adapters own config and auth selection. Existing database owners continue to own application SQL and snapshot restoration; file storage does not depend on them. internal/snapshotarchive owns portable format helpers only.

Use the Go standard library and dependencies already in the repository, including existing platform primitives where appropriate. No new module dependency, SQLite driver, alternate embedded store, application SQL metadata table, new daemon/port, environment knob, S3 emulator, network filesystem, or backend-selection matrix is introduced. Do not implement a custom B-tree, write-ahead log, global JSON catalog, or transactional counter set.

## Source references

Repository observations refer to the preceding review at the pinned baseline, not a new claim about latest main:

Technical references inform implementation constraints, not an assertion that the proposed backend has been tested:

- [Apple XNU clonefile(2)](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/clonefile.2): descriptor-based source cloning, independent copy-on-write mutations, atomic destination creation, and distinct unsupported/cross-filesystem versus real failure codes. Checked September 9, 2026.
- [Linux man-pages FICLONE](https://man7.org/linux/man-pages/man2/ioctl_ficlonerange.2.html): whole-file descriptor reflinking and filesystem/error semantics. The current adapter conservatively does not mask ambiguous `EINVAL` or `EBADF` as success. Linux native proof remains separate from macOS evidence. Checked September 9, 2026.
