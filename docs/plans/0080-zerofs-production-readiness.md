# ZeroFS Production Readiness

> **Superseded by [plan 0091](0091-local-storage-and-zerofs-removal.md) (2026-07-02).** ZeroFS was removed from Scenery entirely; the local filesystem backend is now the production-supported storage kind. This plan is retained as history — the current storage contract lives in [docs/local-contract.md](../local-contract.md).

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery already has a beta storage capability backed first by managed ZeroFS in local development. Apps depend on `scenery.sh/storage`, receive Scenery capability metadata through `SCENERY_STORAGE_CONFIG`, and, in agent-backed dev sessions, talk to a Scenery-owned storage proxy that speaks to ZeroFS over a 9P Unix socket.

The purpose of this plan is to promote that existing design from beta/local migration support to production-ready storage for real Scenery app data. Production-ready means Scenery can recommend the capability for user/customer data with clear durability, recovery, auth, tenant isolation, secret handling, legal, cleanup, observability, and migration guarantees.

This plan intentionally keeps the current architecture:

```text
Scenery app code
  -> scenery.sh/storage
    -> Scenery runtime/proxy boundary
      -> ZeroFS 9P Unix socket or operator-owned storage proxy
        -> ZeroFS backend storage
```

Non-goals:

* Do not expose raw ZeroFS sockets, object roots, S3 credentials, or ZeroFS-specific APIs to app code.
* Do not revive an app-local drive/storage service pattern.
* Do not add NFS, NBD, kernel mounts, wildcard listeners, or privileged filesystem dependencies as the default production path.
* Do not replace the architecture unless a milestone proves the current proxy/9P boundary cannot meet the acceptance criteria.

## Progress

* [x] 2026-06-26: Reviewed GitHub `scenery-sh/scenery` `main` at `bc03ecef98e754a2dab671b8b73fac1d7f8dc089` and drafted this production-readiness plan.
* [x] 2026-06-26: Recorded this plan in `docs/plans/0080-zerofs-production-readiness.md`.
* [x] 2026-06-26: Registered the plan in `docs/plans/active.md` and `docs/knowledge.json`.
* [x] Keep storage documented as beta until all production-readiness gates in this plan pass.
* [x] Implement tenant scoping in the storage runtime/proxy path.
* [x] Make object metadata semantics durable and consistent.
* [x] Add atomic conditional write behavior.
* [x] Add crash/restart durability proof.
* [x] Add production runtime/proxy contract or fail-closed behavior.
* [x] Add lease-aware storage-cell cleanup and operator observability.
* [x] Record legal/licensing decision for ZeroFS distribution and production use.
* [x] Add migration/import/export/rollback proof.
* [x] Move storage out of beta only after final acceptance, scoped to the app-facing API/runtime routes with operator-provided proxy config.
* [x] 2026-06-26: Added headless runtime fail-closed behavior: `scenery serve` and standalone `scenery worker` now refuse declared storage unless an explicit `SCENERY_STORAGE_CONFIG` is already present, while `scenery up` dev sessions and non-session CLI/task fixture paths stay unchanged.
* [x] 2026-06-26: Added tenant-scoped runtime stores. `tenant_scoped` stores physically map visible keys under `__scenery/tenants/<tenant>/...`, external storage routes derive tenants from standard auth data, and private/internal calls fail closed unless a standard-auth context or `storage.WithTenantID` is present.
* [x] 2026-06-26: Fixed over-EOF range metadata for local runtime, CLI/local, and ZeroFS-backed stores so `Get` reports the actual returned length instead of the requested length.
* [x] 2026-06-26: Added durable sidecar metadata for file-backed local and ZeroFS-backed stores. `ContentType` and user metadata now round-trip through `Head`, `Get`, `List`, reserved runtime routes, and the managed storage proxy; sidecars are hidden from `List` and removed by `Delete`/`DeletePrefix`.
* [x] 2026-06-26: Added keyed `IfNoneMatch` write locking for runtime local, CLI/local, ZeroFS, and managed proxy paths; checked object and metadata fsync errors for the local filesystem backend; recorded that the managed ZeroFS/P9 backend cannot safely require fsync yet.
* [x] 2026-06-26: Tightened generated managed ZeroFS config handling: the run directory is `0700`, the TOML containing the local-dev encryption password is `0600`, local metadata sidecar deletes sync their parent directories, and the ZeroFS AGPL production gate is recorded in `docs/zerofs-legal.md`.
* [x] 2026-06-26: Extended the self-harness storage probe with live managed ZeroFS restart proof: write through the app route, interrupt the managed ZeroFS process, restart the dev runtime, and read the same object back through the app route.
* [x] 2026-06-26: Added the lease-aware cleanup/ops path: `scenery down` releases only the current session lease, `inspect storage`/`storage status` report lease ownership and liveness, and `scenery storage cleanup --json` is dry-run by default, refuses live leases, and deletes the storage cell only with `--yes`.
* [x] 2026-06-26: Recorded the current beta migration proof as Scenery storage CLI object/prefix import, export, metadata verification, and rollback (`put`, `ls`, `stat`, `get`, `rm --recursive`), plus self-harness cross-worktree object round-trip.
* [x] 2026-06-26: Moved Scenery-owned tenant and metadata physical prefixes from `.scenery/...` to `__scenery/...` after real ZeroFS returned `EREMCHG` (`remote address changed`) for hidden dot-prefixed object paths.
* [x] 2026-06-26: Confirmed live managed ZeroFS proof passes in `scenery harness self --summary --write`: the storage fixture writes through the app route, interrupts managed ZeroFS PID `43533`, restarts, and reads the same object back.
* [x] 2026-06-26: Closed the production scope by requiring headless storage configs to use `kind: "proxy"` with `proxy_socket`, rejecting local roots, documenting managed ZeroFS as beta local-dev/proof-only, and promoting only the app-facing storage API/runtime route contract for operator-provided proxy deployments.

## Surprises & Discoveries

* The local contract originally kept `scenery.sh/storage`, app storage declarations, `scenery inspect storage --json`, and `scenery storage ... --json` in the dev-only/beta surface while the storage runtime boundary and generated browser routes matured. Final promotion is narrower: app API/runtime routes are production-supported only with operator-provided proxy config; managed ZeroFS plus storage CLI/inspect remain beta local-dev/operator surfaces.
* The app config schema still permits only `kind: "zerofs"` for app-declared stores. Production deployments do not expose ZeroFS to app code; they provide the runtime proxy config through `SCENERY_STORAGE_CONFIG`.
* `tenant_scoped` was already present in config, runtime config, inspect output, docs, and fixtures, but initially had no enforcement in the store/proxy implementation. The final implementation enforces it with a Scenery-owned store wrapper.
* The ZeroFS adapter has the right broad shape: validated keys, temp object write, rename, 9P Unix socket transport, stream-first reads, atomic conditional creation, metadata persistence, and restart proof. Local filesystem backends check fsync errors; managed ZeroFS/P9 remains beta because fsync is not safe to require with the pinned ZeroFS artifact.
* `Head` and `List` compute SHA256 by reading whole objects. That is acceptable for small beta/dev stores but is a likely production scalability problem.
* The dev service writes a deterministic local-dev encryption password into the generated TOML. This must either remain dev-only or be replaced by production secret management.
* The generated ZeroFS TOML contains a local-dev encryption password and is now written as `0600` under a `0700` run directory.
* The CLI/non-session storage path currently uses a local directory backend under the shared storage-cell object directory. That is useful for fixtures but is not a production proof of ZeroFS semantics.
* The runtime store factory supports `local` and `proxy`, not a production ZeroFS service contract. Production must either supply an operator-owned proxy explicitly or Scenery must fail closed.
* The storage env builder is shared by `serve` and standalone `worker`; one headless guard covers both paths. Non-session `task` and storage CLI paths still use local storage-cell roots for dev fixtures and self-harness proof, not production runtime claims.
* Tenant scoping can live as one store wrapper over local/proxy runtime stores. The generic wrapper keeps caller-visible keys stable and uses a simple page scan for tenant cursors; backend-native tenant cursors can replace it later if pagination gets hot.
* Range metadata was inconsistent in file-backed stores: a range request beyond EOF returned the right bytes but reported the requested length. This is fixed independently from metadata sidecar persistence.
* HTTP transport canonicalizes metadata header names, so metadata sent through reserved routes/proxy uses canonical header-key casing such as `Source`. Direct Go store calls preserve the caller's map keys.
* P9 localfs does not support directory `FSync`; real ZeroFS 1.2.5 returns `EREMCHG` for object and directory `FSync` and can leave the resulting object handle unusable. The ZeroFS adapter therefore skips P9 fsync calls entirely. This keeps managed ZeroFS beta/local-dev-only; production storage uses an operator-provided proxy with its own durability contract.
* The pinned ZeroFS artifact is marked `AGPL-3.0-only` in `scenery.toolchain.json`; the current legal posture allows local-dev/proof work but blocks managed ZeroFS production recommendation until owner approval or replacement.
* The current Scenery migration surface is intentionally small: CLI import/export of objects and prefixes. A production backup/restore system belongs behind the operator-provided storage proxy.
* Real ZeroFS 1.2.5 returns `EREMCHG` (`remote address changed`) for dot-prefixed internal object paths such as `.scenery/tenants/...`; Scenery-owned storage internals now use `__scenery/...` physical prefixes.
* The same dot-prefix issue also affected temporary object names during managed ZeroFS writes; ZeroFS temp files now use `__scenery-put-*` before the final rename.

## Decision Log

* Decision: Managed ZeroFS remains beta/local-dev, while Scenery's app-facing storage API can be production-supported through an explicit operator-provided proxy config.
  Rationale: The AGPL and P9 durability gates block managed ZeroFS production recommendation, but the Scenery storage API and runtime proxy boundary can be safely promoted when production operators own the backend/proxy contract.
  Date/Author: 2026-06-26 / Scenery storage review.

* Decision: Keep the app-facing API as `scenery.sh/storage`, not `scenery.sh/zerofs`.
  Rationale: Apps should depend on a Scenery capability. ZeroFS is a backend/substrate detail.
  Date/Author: 2026-06-26 / Scenery storage review.

* Decision: Keep the current proxy/9P architecture and harden it.
  Rationale: The architecture already isolates ZeroFS and native dependencies from target apps. The missing work is proof, not a replacement storage architecture.
  Date/Author: 2026-06-26 / Scenery storage review.

* Decision: Enforce tenant scoping inside Scenery.
  Rationale: Caller-visible keys must not contain tenant IDs. Store implementations must map visible keys to Scenery-owned tenant namespaces when `tenant_scoped` is true.
  Date/Author: 2026-06-26 / Scenery storage review.

* Decision: Production runtime must fail closed without explicit storage configuration.
  Rationale: A production app with declared storage must not silently fall back to local directories, dev agent state, or raw ZeroFS paths.
  Date/Author: 2026-06-26 / Scenery storage review.

* Decision: Keep non-session storage CLI/task local fallback as a dev fixture path while failing closed for headless app runtimes.
  Rationale: Self-harness and CLI smoke tests need a cheap configured-store path; production risk is `serve`/`worker` silently starting app runtimes on local roots.
  Date/Author: 2026-06-26 / Codex.

* Decision: Enforce tenant scoping with a Scenery-owned store wrapper, not backend-specific tenant logic.
  Rationale: One wrapper covers local and proxy-backed ZeroFS paths while keeping app-visible keys unchanged.
  Date/Author: 2026-06-26 / Codex.

* Decision: Persist file-backed object metadata in Scenery-owned sidecar JSON.
  Rationale: It is the smallest backend-neutral path for local and ZeroFS 9P stores, keeps app keys unchanged, and avoids changing app-facing object APIs.
  Date/Author: 2026-06-26 / Codex.

* Decision: Use keyed in-process locks for `IfNoneMatch` write races.
  Rationale: It covers concurrent app goroutines and multiple clients of one Scenery storage proxy without introducing stale cross-process lock cleanup before the production proxy contract is finalized.
  Date/Author: 2026-06-26 / Codex.

* Decision: ZeroFS legal/licensing is a release gate.
  Rationale: The pinned ZeroFS artifact is AGPL-licensed in the toolchain manifest. Shipping or recommending it for production needs an explicit recorded legal/compliance decision.
  Date/Author: 2026-06-26 / Scenery storage review.

* Decision: Keep managed ZeroFS local-dev/proof-only until legal approval changes.
  Rationale: The smallest safe compliance posture is to record the AGPL gate and keep managed ZeroFS production promotion blocked instead of inventing a license workflow inside the runtime.
  Date/Author: 2026-06-26 / Codex.

* Decision: Reject explicit headless storage configs that use local roots.
  Rationale: "Explicit config" should not become a production local-directory fallback. Headless storage support is proxy-backed or it fails closed.
  Date/Author: 2026-06-26 / Codex.

## Outcomes & Retrospective

Outcome:

* Scenery storage can be recommended for production only as the app-facing API/runtime route contract backed by an explicit operator-provided `SCENERY_STORAGE_CONFIG` with proxy stores.
* Managed ZeroFS remains beta local-dev/proof tooling because of AGPL distribution posture and missing safe P9 fsync semantics.
* Headless `serve` and standalone `worker` fail closed without proxy-backed runtime config and reject local-root-backed config.
* Storage users have Scenery-owned tenant isolation, durable metadata, atomic conditional writes, range metadata fixes, restart proof, cleanup safety, import/export/rollback proof, and documented legal/ops boundaries.

## Context and Orientation

Start with these files:

```text
docs/local-contract.md
docs/app-development-cookbook.md
docs/environment.md
docs/environment.registry.json
docs/plans/0078-scenery-storage.md
docs/plans/completed.md
docs/schemas/scenery.config.v1.schema.json
docs/schemas/scenery.storage.inspect.v1.schema.json
scenery.toolchain.json
SKILL.md
```

Core storage API and runtime files:

```text
storage/storage.go
storage/runtime.go
storage/http.go
storage/keys.go
storage/errors.go
internal/storage/zerofs.go
internal/storage/local.go
internal/storage/memory.go
internal/storage/contract_test.go
internal/storageconfig/runtime.go
```

Managed ZeroFS and CLI files:

```text
cmd/scenery/dev_services_zerofs.go
cmd/scenery/dev_services_zerofs_evidence.go
cmd/scenery/dev_services_zerofs_test.go
cmd/scenery/storage_proxy.go
cmd/scenery/storage_proxy_test.go
cmd/scenery/storage.go
cmd/scenery/storage_test.go
cmd/scenery/inspect.go
cmd/scenery/inspect_storage_test.go
cmd/scenery/doctor.go
cmd/scenery/doctor_test.go
cmd/scenery/agent.go
cmd/scenery/help.go
cmd/scenery/harness_self_storage.go
```

Runtime/browser route files:

```text
runtime/storage_http.go
runtime/storage_http_test.go
runtime/router.go
runtime/current.go
internal/clientgen/typescript.go
```

App config and auth context files:

```text
internal/app/root.go
internal/app/root_test.go
auth/auth.go
internal/authbridge/authbridge.go
```

Fixture apps and generated-client smoke tests:

```text
testdata/apps/storage-basic/.scenery.json
testdata/apps/storage-basic/**
```

Read plan 0078 before editing. It is the historical implementation plan and contains useful decisions, but this plan supersedes its production-readiness gates.

## Milestones

### Milestone 1: Freeze production gate and docs

Before final promotion, storage remains beta until all gates pass. Documentation must be explicit that managed ZeroFS is not recommended for real customer data without an owner-approved legal/durability decision.

Acceptance:

* `docs/local-contract.md` distinguishes the production-supported operator-proxy storage runtime contract from beta managed ZeroFS and storage CLI/inspect surfaces.
* `docs/app-development-cookbook.md` says the current ZeroFS path is local-dev/beta unless production readiness is complete.
* `scenery check` or `scenery serve` gives a clear error when a production/headless runtime declares storage but lacks an explicit production storage config/proxy.
* `docs/knowledge.json` and plan index entries are updated.

### Milestone 2: Enforce tenant scoping and API semantics

Make `tenant_scoped` real.

Acceptance:

* Stores configured with `tenant_scoped: true` physically namespace object keys by the active tenant.
* Caller-visible keys are unchanged.
* Two authenticated tenants cannot list, read, overwrite, delete, or prefix-delete each other’s objects.
* Missing tenant context fails closed for tenant-scoped external/auth routes.
* Private/internal storage calls have an explicit documented tenant behavior.

### Milestone 3: Persist metadata and fix object operation semantics

Make `ContentType`, `Metadata`, `ETag`, `SHA256`, `ModifiedAt`, ranges, and list behavior consistent across memory, local, proxy, and ZeroFS-backed stores.

Acceptance:

* `Put` metadata is visible in `Head`, `Get`, and `List`.
* Metadata sidecars or indexes are not exposed as user objects.
* `Delete` and `DeletePrefix` remove metadata consistently.
* Range metadata reports the actual returned length, not an impossible length beyond EOF.
* `List` pagination remains deterministic.

### Milestone 4: Durability, restart, and concurrency proof

Prove that production claims survive crash/restart and concurrent writers.

Acceptance:

* Local filesystem writes check fsync errors. Managed ZeroFS/P9 writes do not rely on fsync until the backend exposes a safe durability primitive.
* Parent directories are synced after rename/delete where the platform supports it.
* `IfNoneMatch` is atomic across concurrent writers.
* Crash/restart integration test writes objects, kills Scenery/ZeroFS/proxy in controlled ways, restarts, and verifies list/stat/get checksums.
* Concurrency tests cover parallel put/get/list/delete under one tenant and two tenants.

### Milestone 5: Production runtime/proxy boundary

Add an explicit production path or fail closed.

Acceptance:

* Production/headless app runtime never defaults to dev agent storage.
* Apps can run with an explicit operator-provided Scenery storage proxy/config.
* The production app process sees Scenery capability metadata only, not raw ZeroFS sockets, object roots, or object-store credentials.
* Documentation explains how operators start/configure the production storage proxy or why storage is not supported in production yet.

### Milestone 6: Security, secrets, and legal gates

Lock down secret handling and record legal posture.

Acceptance:

* Secret-bearing files are `0600`; secret-bearing directories are `0700` where practical.
* Inspect JSON, storage status JSON, logs, failure evidence, dashboard events, and harness artifacts never contain raw secrets.
* The ZeroFS AGPL/commercial decision is recorded in a docs file and referenced by release/toolchain docs.
* CI or release validation fails if production ZeroFS support is marked ready without the legal decision.

### Milestone 7: Ops, cleanup, observability, and migration

Give operators boring tools.

Acceptance:

* `scenery storage cleanup --json` exists as the explicit storage-cell cleanup surface.
* Destructive cleanup refuses live leases by default and requires `--yes`.
* Inspect/status exposes readiness, leases, version, object usage, storage paths, log path, and last error without secrets.
* Migration docs include backup, import/export or dual-write validation, checksum verification, rollback, and tenant isolation proof.
* A fixture migration proof validates data equivalence before and after migration.

## Plan of Work

Work from the repo root.

Prefer small, boring changes. Avoid changing public app API shape unless a production gate cannot be satisfied otherwise.

Implementation order:

1. Add this plan and register it.
2. Keep docs honest: beta until proven.
3. Add fail-closed production behavior.
4. Implement tenant scoping.
5. Persist metadata/content-type.
6. Harden atomic write/delete behavior.
7. Add crash/restart and concurrency tests.
8. Add production proxy/config contract.
9. Lock down secrets and legal docs.
10. Add cleanup/observability commands.
11. Add migration proof.
12. Only then update docs to production-ready.

## Concrete Steps

### 1. Create and register this ExecPlan

Files:

```text
docs/plans/0080-zerofs-production-readiness.md
docs/plans/active.md
docs/knowledge.json
```

Actions:

* Add this file.
* Add an active-plan entry.
* Add a `docs/knowledge.json` entry with tags `plans`, `storage`, `zerofs`, `production`, `runtime`, and `security`.

Validation:

```sh
jq empty docs/knowledge.json
go run ./cmd/scenery inspect docs --json
git diff --check
```

### 2. Clarify beta and fail-closed production docs

Files:

```text
docs/local-contract.md
docs/app-development-cookbook.md
docs/environment.md
docs/environment.registry.json
SKILL.md
cmd/scenery/serve.go
cmd/scenery/storage.go
cmd/scenery/help.go
cmd/scenery/*_test.go
```

Actions:

* Document that managed ZeroFS is currently local-dev/beta.
* Document that production apps must use an explicit production storage config/proxy once implemented.
* Add or update help text so `scenery storage` surfaces do not imply production readiness.
* Add a runtime/headless guard: if storage is configured and no explicit production storage runtime is available, fail with an actionable error instead of silently using dev agent/local roots.
* Keep dev-mode behavior unchanged.

Validation:

```sh
go test ./cmd/scenery -run 'Storage|Serve|Help|Doctor'
go run ./cmd/scenery help storage --json
go run ./cmd/scenery inspect docs --json
```

### 3. Implement tenant scoping

Files:

```text
storage/runtime.go
storage/storage.go
storage/keys.go
storage/errors.go
storage/scope.go
internal/storage/contract_test.go
internal/storage/local.go
internal/storage/memory.go
internal/storage/zerofs.go
runtime/storage_http.go
runtime/storage_http_test.go
auth/auth.go
internal/authbridge/authbridge.go
cmd/scenery/storage_proxy.go
cmd/scenery/storage_proxy_test.go
```

Actions:

* Add a small Scenery-owned tenant scoping helper. Candidate API:

```go
package storage

func WithTenantID(ctx context.Context, tenantID string) context.Context
```

* In runtime HTTP storage routes, after authentication, derive tenant ID from auth data and put it into the context for tenant-scoped stores.
* In app endpoint calls, support deriving tenant ID from the current auth bridge when no explicit storage tenant context exists.
* Wrap runtime stores when `RuntimeStoreConfig.TenantScoped` is true.
* Physical key mapping should use a reserved namespace such as:

```text
__scenery/tenants/<escaped-or-hashed-tenant-id>/<caller-visible-key>
```

* Ensure list results return caller-visible keys, not physical tenant prefixes.
* Fail closed when `tenant_scoped` is true and the tenant is missing for external/auth traffic.
* Add tests for two tenants and no-tenant cases.

Focused validation:

```sh
go test ./storage ./internal/storage -run 'Tenant|Storage|Contract'
go test ./runtime -run 'Storage|Tenant|Auth'
go test ./cmd/scenery -run 'StorageProxy|Tenant|Storage'
```

### 4. Persist metadata and content type

Files:

```text
storage/storage.go
storage/http.go
internal/storage/memory.go
internal/storage/local.go
internal/storage/zerofs.go
internal/storage/contract_test.go
cmd/scenery/storage_proxy.go
runtime/storage_http.go
runtime/storage_http_test.go
cmd/scenery/storage_test.go
```

Actions:

* Decide on a metadata persistence mechanism that works over local and 9P. The boring path is a sidecar file under a Scenery-reserved namespace.
* Metadata writes must be atomic with object writes from the caller’s perspective.
* `Head`, `Get`, and `List` must return persisted content type and metadata.
* `Delete` and `DeletePrefix` must remove sidecars.
* Sidecars and reserved metadata directories must never appear in user `List` results.
* Restrict metadata keys that can become HTTP headers.
* Add tests for:

  * `Put` with content type and metadata.
  * overwrite changes metadata.
  * delete removes metadata.
  * prefix delete removes metadata.
  * sidecars are hidden from list.
  * proxy/runtime HTTP preserve metadata.

Focused validation:

```sh
go test ./storage ./internal/storage -run 'Metadata|ContentType|Storage|Contract'
go test ./runtime -run 'Storage|Metadata|HTTP'
go test ./cmd/scenery -run 'Storage|Proxy|Metadata'
```

### 5. Harden write, delete, range, and conditional semantics

Files:

```text
internal/storage/zerofs.go
internal/storage/local.go
internal/storage/memory.go
internal/storage/contract_test.go
storage/runtime.go
cmd/scenery/storage_proxy.go
cmd/scenery/storage_proxy_test.go
runtime/storage_http.go
runtime/storage_http_test.go
```

Actions:

* Check and return `FSync` errors.
* After rename/delete, fsync the parent directory where supported. If 9P cannot support directory fsync, document the limitation and cover it with a ZeroFS restart proof.
* Make `Put` with `IfNoneMatch` atomic across concurrent writers. Candidate boring implementation:

  * a Scenery-owned lock file per physical key;
  * or an exclusive create/rename sequence if 9P semantics prove sufficient;
  * tests must cover multiple goroutines and multiple proxy clients.
* Fix range metadata so requested length beyond EOF reports the actual returned length.
* Avoid `Head` followed by a separately opened `Get` becoming inconsistent under concurrent overwrite. Either document last-writer-wins semantics clearly or open/read metadata in a single consistent sequence.
* Clean stale temp files and stale lock files conservatively.

Focused validation:

```sh
go test ./internal/storage -run 'IfNoneMatch|Concurrent|Range|Durability|Contract' -count=10
go test ./cmd/scenery -run 'StorageProxy|Concurrent|Range' -count=10
go test ./runtime -run 'Storage|Range|Conditional' -count=10
```

### 6. Add crash/restart proof

Files:

```text
cmd/scenery/dev_services_zerofs_test.go
cmd/scenery/harness_self_storage.go
cmd/scenery/harness_self_storage_test.go
internal/storage/contract_test.go
testdata/apps/storage-basic/**
```

Actions:

* Add an integration test or self-harness step that runs only when the pinned ZeroFS toolchain artifact is available.
* Use an isolated `SCENERY_AGENT_HOME`.
* Start the fixture app with managed ZeroFS.
* Put several objects with known checksums and metadata.
* Kill/restart the app process, storage proxy, and ZeroFS process in controlled phases.
* Reattach via `scenery up`.
* Verify `list`, `stat`, `get`, metadata, tenant isolation, and checksums.
* Record skipped diagnostics when ZeroFS is unavailable.

Focused validation:

```sh
go test ./cmd/scenery -run 'ZeroFS|Crash|Restart|Harness|Storage'
SCENERY_AGENT_HOME="$(mktemp -d)" go run ./cmd/scenery harness self --summary --write
```

### 7. Add production storage proxy/config contract

Files:

```text
storage/runtime.go
internal/storageconfig/runtime.go
cmd/scenery/storage_proxy.go
cmd/scenery/storage.go
cmd/scenery/help.go
cmd/scenery/serve.go
docs/local-contract.md
docs/environment.md
docs/environment.registry.json
docs/schemas/*
cmd/scenery/*_test.go
```

Actions:

* Decide the first production contract:

  * either add a `scenery storage proxy serve --socket <path> --config <file> --json` command using the existing proxy code, or
  * explicitly fail closed and keep production storage unsupported until a later plan.
* If adding the proxy:

  * accept config by path, not raw secrets in argv;
  * support Unix socket transport from app process to proxy;
  * keep raw ZeroFS/S3 credentials in proxy env/config only;
  * add startup readiness and JSON status;
  * add systemd/container-friendly docs.
* Update `SCENERY_STORAGE_CONFIG` docs to distinguish dev-injected config from production operator config.
* Add tests proving `scenery serve` does not silently use dev local roots.

Focused validation:

```sh
go test ./cmd/scenery -run 'Storage|Proxy|Serve|Production|Help'
go test ./storage -run 'Runtime|Proxy|Config'
go run ./cmd/scenery help storage --json
```

### 8. Lock down secrets and legal docs

Files:

```text
cmd/scenery/dev_services_zerofs.go
cmd/scenery/dev_services_zerofs_evidence.go
cmd/scenery/inspect.go
cmd/scenery/harness_self_storage.go
docs/environment.md
docs/environment.registry.json
docs/legal/zerofs.md
docs/licenses/zerofs.md
scenery.toolchain.json
cmd/scenery/*_test.go
```

Actions:

* Change secret-bearing generated config permissions to `0600`.
* Prefer `0700` for storage cell `run` directories where practical.
* If ZeroFS supports reading encryption passwords from env or a separate file, avoid writing the password into the main TOML.
* Redact all service env values in inspect/status/harness/evidence.
* Add tests that grep generated JSON/log/evidence artifacts for known fake secrets and fail if found.
* Add a legal decision doc:

  * AGPL-only dev toolchain allowed/not allowed;
  * production distribution posture;
  * commercial license requirement if needed;
  * release gate owner.
* Update docs to say production promotion cannot happen without the legal decision.

Focused validation:

```sh
go test ./cmd/scenery -run 'ZeroFS|Secret|Redact|Evidence|Inspect|Harness'
grep -R "fake-zero-secret" .scenery/evidence .scenery/sessions 2>/dev/null && exit 1 || true
jq empty docs/knowledge.json
```

### 9. Add cleanup and observability

Files:

```text
cmd/scenery/storage.go
cmd/scenery/help.go
cmd/scenery/inspect.go
cmd/scenery/agent.go
docs/schemas/scenery.storage.cell.list.v1.schema.json
docs/schemas/scenery.storage.cell.status.v1.schema.json
docs/schemas/scenery.storage.cell.delete.v1.schema.json
docs/local-contract.md
docs/app-development-cookbook.md
cmd/scenery/storage_test.go
cmd/scenery/inspect_storage_test.go
```

Actions:

* Add lease-aware commands:

```text
scenery storage cell list --json
scenery storage cell status <cell-id> --json
scenery storage cell delete <cell-id> --yes --json
scenery storage cell prune --older-than <duration> --yes --json
```

* Default delete/prune to dry-run unless `--yes` is present.
* Refuse deletion when live leases exist unless a separate explicit `--force` exists and the command explains the risk.
* Surface:

  * cell ID;
  * substrate status;
  * lease count;
  * live lease count;
  * object/cache/run paths;
  * ZeroFS version/source;
  * log path;
  * last exit/error;
  * approximate object count/bytes where cheap.
* Add structured storage proxy events/metrics:

  * operation;
  * store;
  * status;
  * latency;
  * bytes;
  * key hash, not raw key, by default.

Focused validation:

```sh
go test ./cmd/scenery -run 'StorageCell|Storage|Inspect|Help|Down|Prune'
go run ./cmd/scenery storage cell list --json
```

### 10. Add migration proof and docs

Files:

```text
docs/app-development-cookbook.md
docs/local-contract.md
docs/storage-migration.md
testdata/apps/storage-basic/**
cmd/scenery/harness_self_storage.go
cmd/scenery/harness_self_storage_test.go
```

Actions:

* Document migration modes:

  * backup first;
  * bulk import;
  * checksum verification;
  * optional dual-write/read-compare window;
  * rollback to old store;
  * tenant-isolation verification.
* Add fixture data and a migration harness:

  * seed local fixture objects;
  * import into Scenery storage;
  * verify count, keys, checksums, metadata, ranges;
  * verify tenant separation;
  * export or read back to a separate directory and compare.
* Keep ONLV-specific notes out of the generic contract except as a historical example in plan notes.

Focused validation:

```sh
go test ./cmd/scenery -run 'StorageMigration|Harness|Storage'
go run ./cmd/scenery harness self --summary --write
```

### 11. Final production promotion

Files:

```text
docs/local-contract.md
docs/app-development-cookbook.md
docs/environment.md
docs/environment.registry.json
docs/plans/0080-zerofs-production-readiness.md
docs/plans/completed.md
docs/knowledge.json
SKILL.md
README.md
```

Actions:

* Update docs only after all gates pass.
* Move this plan to completed.
* Change storage classification from beta to production-supported with explicit scope.
* Record unsupported cases clearly, for example public object access policies if still future work.
* Preserve migration warnings and backup requirements.

Final validation:

```sh
jq empty docs/knowledge.json
go run ./cmd/scenery inspect docs --json
go run ./cmd/scenery check --json --app-root testdata/apps/storage-basic
go run ./cmd/scenery inspect storage --json --app-root testdata/apps/storage-basic
go test ./storage ./internal/storage
go test ./runtime -run 'Storage|Route|Auth|Tenant'
go test ./cmd/scenery -run 'Storage|ZeroFS|Inspect|Doctor|Down|Help|Harness|Migration'
go test ./internal/app
go test ./...
git diff --check
```

## Validation and Acceptance

Minimum acceptance before storage can leave beta:

* `jq empty docs/knowledge.json` passes.
* `go run ./cmd/scenery inspect docs --json` passes.
* `go test ./...` passes.
* `go test ./storage ./internal/storage` passes.
* `go test ./runtime -run 'Storage|Route|Auth|Tenant'` passes.
* `go test ./cmd/scenery -run 'Storage|ZeroFS|Inspect|Doctor|Down|Help|Harness|Migration'` passes.
* `git diff --check` passes.
* `go run ./cmd/scenery check --json --app-root testdata/apps/storage-basic` passes.
* `go run ./cmd/scenery inspect storage --json --app-root testdata/apps/storage-basic` emits schema-valid JSON.

ZeroFS-available acceptance:

```sh
agent_home="$(mktemp -d)"
SCENERY_AGENT_HOME="$agent_home" go run ./cmd/scenery up --app-root testdata/apps/storage-basic --json --detach
SCENERY_AGENT_HOME="$agent_home" go run ./cmd/scenery storage put app probe/prod.txt ./README.md --app-root testdata/apps/storage-basic --json
SCENERY_AGENT_HOME="$agent_home" go run ./cmd/scenery storage stat app probe/prod.txt --app-root testdata/apps/storage-basic --json
SCENERY_AGENT_HOME="$agent_home" go run ./cmd/scenery storage get app probe/prod.txt --output "$agent_home/prod.txt" --app-root testdata/apps/storage-basic --json
cmp ./README.md "$agent_home/prod.txt"
SCENERY_AGENT_HOME="$agent_home" go run ./cmd/scenery down --app-root testdata/apps/storage-basic --json
```

Production-readiness acceptance:

* Docs no longer call storage beta only after all of the following are true.
* `tenant_scoped` is enforced and tested with two tenants.
* `IfNoneMatch` is atomic across concurrent writers.
* Object metadata is durable and hidden sidecars are not listable.
* Crash/restart proof verifies object visibility and checksum correctness after restarting Scenery, the storage proxy, and ZeroFS.
* Secret redaction tests cover inspect JSON, status JSON, logs, dashboard events, harness artifacts, and failure evidence.
* ZeroFS legal/licensing decision is recorded.
* Production runtime either has a documented proxy/config path or fails closed with an explicit unsupported-production-storage error.
* Storage-cell cleanup commands are lease-aware, dry-run by default, and require `--yes` for destructive actions.
* Migration docs include backup, checksum verification, rollback, and tenant-isolation proof.

## Idempotence and Recovery

All changes in this plan must be re-runnable.

Storage config and docs:

* Re-running docs/schema updates must not change behavior.
* Unknown config fields remain rejected.
* Production support must not depend on dirty local state or untracked secrets.

Runtime/proxy startup:

* Starting a dev runtime with an existing healthy ZeroFS cell reuses the cell.
* Starting with stale sockets removes only Scenery-owned stale sockets after verifying the owner is gone.
* Starting with a corrupt or unreachable ZeroFS cell fails with a clear repair path and evidence artifact.
* Production runtime never silently falls back to dev local roots.

Object operations:

* `Put` without `IfNoneMatch` remains deterministic last-writer-wins.
* `Put` with `IfNoneMatch` must be atomic.
* Failed `Put` must not leave visible partial objects.
* Failed metadata write must not leave an object that claims metadata was persisted.
* `Delete` of a missing key returns typed not-found unless a future explicit ignore-missing flag exists.
* `DeletePrefix` must either complete or return enough detail to retry safely.

Cleanup:

* `scenery down` only releases the current session lease.
* Shared storage-cell data is never deleted by `down --state`.
* Storage-cell delete/prune requires explicit storage commands and `--yes`.
* Cleanup refuses live leases by default.
* Dry-run output is stable and machine-readable.

Migration:

* Migration proof can be rerun from scratch in an isolated agent home.
* Verification compares keys, bytes, checksums, metadata, and tenant visibility.
* Rollback instructions do not rely on hidden Scenery internals.

## Artifacts and Notes

Expected changed files:

```text
docs/plans/0080-zerofs-production-readiness.md
docs/plans/active.md
docs/plans/completed.md
docs/knowledge.json
docs/local-contract.md
docs/app-development-cookbook.md
docs/environment.md
docs/environment.registry.json
docs/zerofs-legal.md
docs/storage-migration.md
docs/schemas/scenery.storage.cell.list.v1.schema.json
docs/schemas/scenery.storage.cell.status.v1.schema.json
docs/schemas/scenery.storage.cell.delete.v1.schema.json
SKILL.md
README.md
storage/storage.go
storage/runtime.go
storage/http.go
storage/keys.go
storage/errors.go
storage/scope.go
internal/storage/zerofs.go
internal/storage/local.go
internal/storage/memory.go
internal/storage/contract_test.go
internal/storageconfig/runtime.go
runtime/storage_http.go
runtime/storage_http_test.go
runtime/current.go
auth/auth.go
internal/authbridge/authbridge.go
cmd/scenery/dev_services_zerofs.go
cmd/scenery/dev_services_zerofs_evidence.go
cmd/scenery/dev_services_zerofs_test.go
cmd/scenery/storage_proxy.go
cmd/scenery/storage_proxy_test.go
cmd/scenery/storage.go
cmd/scenery/storage_test.go
cmd/scenery/inspect.go
cmd/scenery/inspect_storage_test.go
cmd/scenery/doctor.go
cmd/scenery/doctor_test.go
cmd/scenery/help.go
cmd/scenery/serve.go
cmd/scenery/harness_self_storage.go
cmd/scenery/harness_self_storage_test.go
testdata/apps/storage-basic/**
```

Expected `docs/plans/completed.md` entry:

```markdown
- ExecPlan: [0080 ZeroFS Production Readiness](0080-zerofs-production-readiness.md)
```

Expected `docs/knowledge.json` entry:

```json
{
  "path": "docs/plans/0080-zerofs-production-readiness.md",
  "title": "ZeroFS Production Readiness",
  "kind": "execplan",
  "status": "completed",
  "owner": "scenery runtime / storage",
  "quality": "B",
  "last_reviewed": "2026-06-26",
  "review_after": "2026-07-26",
  "tags": ["plans", "storage", "zerofs", "production", "runtime", "security"]
}
```

Do not include raw secrets in any artifact. Use fake sentinel values in tests and assert they do not appear in inspect JSON, status JSON, logs, dashboard events, harness output, or failure evidence.

The production recommendation is scoped to the operator-provided proxy runtime contract after this plan’s final validation and acceptance section passes.

## Interfaces and Dependencies

Primary app-facing interface:

* `scenery.sh/storage` remains the only app storage API.
* `SCENERY_STORAGE_CONFIG` remains Scenery-injected capability metadata. Production/headless runtimes require an explicit operator-provided value and must not synthesize dev storage roots.
* Reserved runtime routes under `/__scenery/storage/<store>/...` are auth-gated object routes for browser clients and share the same production scope as `scenery.sh/storage`: operator-provided proxy config for headless runtimes.

Primary internal interfaces and contracts:

* `storage.Store`, `storage.Object`, `storage.PutOptions`, `storage.GetOptions`, and typed storage errors in `storage/`.
* Runtime storage config in `internal/storageconfig/runtime.go`.
* Runtime/browser storage routes in `runtime/storage_http.go` and generated TypeScript storage helpers.
* Managed ZeroFS lifecycle, storage proxy, and CLI storage commands under `cmd/scenery/`.
* App config storage declarations and schema entries under `internal/app/` and `docs/schemas/`.

Scenery-owned substrate interfaces:

* Managed ZeroFS is launched only by `scenery up` for local development and proof work.
* The managed ZeroFS TOML is a secret-bearing substrate file and is written `0600` under a `0700` run directory.
* App code and generated browser clients must not receive raw ZeroFS sockets, object roots, storage-cell roots, or object-store credentials.

Operational interfaces:

* `scenery inspect storage --json` and `scenery storage status --json` expose storage readiness, lease ownership, and lease liveness without raw secrets.
* `scenery storage cleanup --json` reports storage-cell cleanup as a dry run by default; `--yes` is required for deletion after live-lease verification.
* `scenery storage put|get|ls|stat|rm --json` is the current beta object import/export/rollback surface.
* `scenery down` releases only the current session's ZeroFS lease and preserves shared storage-cell data.

External dependencies:

* Use the existing Go standard library, existing Scenery packages, existing legacy async runtime/auth/database dependencies, and the pinned ZeroFS toolchain artifact already recorded in `scenery.toolchain.json`.
* Do not add a new storage backend, ORM, broker, filesystem mount dependency, or secret-management dependency unless this plan is updated with a concrete production gate that cannot be met otherwise.
* `scenery.toolchain.json` pins the managed ZeroFS artifact and records its `AGPL-3.0-only` license.
* `docs/zerofs-legal.md` is the release/legal gate for any future production recommendation.
* The self-harness live ZeroFS proof depends on the pinned `zerofs` toolchain artifact being available or syncable.
