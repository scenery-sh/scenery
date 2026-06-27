# Built-In Neon Selfhost Driver

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Scenery already has the contract boundary for branch-isolated Neon development:
`dev.services.postgres.kind: "neon"`, local worktree branch pins, an
Scenery-owned lease registry, generated dev-cell state, `scenery db neon
install|start|status`, and an executable branch-driver interface selected by
`SCENERY_DEV_NEON_SELFHOST_DRIVER`. The missing piece is the real backend. Today
that executable is an env-var-only escape hatch, and without it Scenery can only
create pending local leases.

This plan makes the real self-hosted Neon branch driver a first-class built-in
Scenery runtime surface. A normal developer or agent should not need to set
`SCENERY_DEV_NEON_SELFHOST_DRIVER` by hand. The default path should be:

```sh
scenery system toolchain sync --tool neon-selfhost --images --json
scenery db neon install --json
scenery db neon start --json
scenery db branch checkout feature/foo --json
scenery up
```

and the worktree happy path should become:

```sh
scenery worktree create pricing-agent
cd ../onlv-pricing-agent
scenery up
```

`scenery up` should create or reuse a real self-hosted Neon branch, start a
branch compute endpoint, run DB lifecycle work against that branch, start
sync against the same branch, and keep the parent branch protected.

This plan is a follow-on to `docs/plans/0065-scenery-managed-neon-dev-cell.md`.
Plan 0065 established the local contract, branch pins, lease registry, generated
dev-cell scaffolding, driver delegation, and cleanup safety. This plan narrows
the remaining work to the built-in `neon-selfhost-driver`, real
tenant/timeline/compute lifecycle, and opt-in harness proof against an actual
local Neon cell.

## Progress

- [x] 2026-06-09: Created ExecPlan `0070-toolchain-managed-neon-selfhost-driver.md` from the requested Neon driver brief.
- [x] 2026-06-09: Linked plan 0070 from `docs/plans/active.md`.
- [x] 2026-06-09: Indexed plan 0070 in `docs/knowledge.json`.
- [x] 2026-06-09: Added the first driver slice backed by `internal/neonselfhost`; it exposes `capabilities --json`, `status --json`, strict schemas for capabilities/backend state, and a pending `ensure` response while real branch lifecycle is still unimplemented.
- [x] 2026-06-09: Added source build support to the managed toolchain manifest/store and regenerated `internal/toolchain/manifest_gen.go` for other Scenery-owned binaries.
- [x] 2026-06-09: Wired `scenery db neon install --json` to record `cell.json.driver`, include driver status in `scenery.db.neon.status.v1`, and resolve branch drivers in the order explicit env, installed cell built-in or legacy path, then local Postgres-shaped fallback.
- [x] 2026-06-09: Added driver-owned backend state helpers for future `backend.json` lifecycle: schema/version validation, atomic writes, branch metadata structs, stable branch-port allocation from `branch_id`, existing-port reuse, and collision avoidance.
- [x] 2026-06-09: Replaced the generated Compose placeholder with a storage-cell topology: MinIO, bucket init, storage broker, pageserver config, three safekeepers, compute templates, and empty `backend.json`. Static app compute is no longer part of the generated Compose project; branch compute remains driver-owned.
- [x] 2026-06-09: Taught the managed driver `ensure` path to write idempotent pending branch metadata into `backend.json`, including deterministic branch port and compute container identity, while still returning public `pending` until real Neon timeline/compute startup exists.
- [x] 2026-06-09: Added stateful driver `reset`, `restore`, and `delete` lifecycle slices. Reset/restore preserve the public branch ID and persisted port while replacing pending timeline metadata; delete removes driver-owned branch metadata and removes the known compute container when Docker can do so safely.
- [x] 2026-06-09: Implemented driver schema-only `diff` for ready backend branches. It locates the current and target branches in `backend.json`, requires both to be ready, runs `pg_dump --schema-only --no-owner --no-privileges` against each endpoint, and returns schema diff text through the existing driver JSON shape.
- [x] 2026-06-09: Added recorded-compute readiness to driver `ensure`. If `backend.json` already has the requested branch with a reachable host/port endpoint, `ensure` now marks that backend branch ready and returns redacted endpoint metadata; otherwise it keeps the branch pending.
- [x] 2026-06-09: Validated the current implementation with `go test ./internal/neonselfhost ./cmd/scenery`, `jq empty` on changed JSON/schema files, `git diff --check`, `go test ./...`, and `scenery harness self --summary --write`. Self-harness passed with warnings only for the existing review-due UI doc, large-file warnings, and slow-test timing warnings.
- [x] 2026-06-09: Added the first real `ensure` lifecycle bridge. The driver now derives stable tenant/timeline IDs, calls the pageserver HTTP API to create or reuse the tenant, parent timeline, and branch timeline when the generated storage cell is reachable, writes `starting` backend state, and starts or reuses a Docker branch compute container from the generated compute templates on the persisted loopback port.
- [x] 2026-06-09: Validated the tenant/timeline/compute startup slice with focused `internal/neonselfhost` ensure tests, `go test ./internal/neonselfhost ./cmd/scenery`, `jq empty` on changed JSON/schema files, `git diff --check`, `go test ./...`, and `scenery harness self --summary --write`. Self-harness passed with warnings only for the existing review-due UI doc, large-file warnings, and slow-test timing warnings.
- [x] 2026-06-09: Tightened driver readiness from TCP-only to SQL-ready. `ensure` now requires `psql` to verify the compute endpoint, creates the requested database when it is missing, and keeps the public lease pending with an actionable message until SQL readiness and database setup pass.
- [x] 2026-06-09: Validated the SQL-ready branch endpoint slice with focused `internal/neonselfhost` and `cmd/scenery` tests, `jq empty` on changed JSON/schema files, `git diff --check`, `go test ./...`, and `scenery harness self --summary --write`. Self-harness passed with warnings only for the existing review-due UI doc, large-file warnings, and slow-test timing warnings.
- [x] 2026-06-09: Made the generated storage-cell topology boot against real Docker Neon images by generating `pageserver_config/identity.toml`, aligning pageserver config with upstream emergency-mode Docker Compose settings, and replacing the compute template's `nc` dependency with Bash TCP probing.
- [x] 2026-06-09: Completed the real branch compute readiness loop. `worktree create` now runs the existing branch-provider ensure boundary for auto-pinned Neon worktrees; the managed selfhost driver starts fresh branch compute containers, verifies Postgres with non-interactive credentials, creates the requested database, and returns ready endpoint metadata with a password-bearing managed `DatabaseURL` while keeping public endpoint metadata redacted.
- [x] 2026-06-09: Added real Neon self-harness coverage and promoted it into the default non-quick self-harness path. The harness now proves managed driver installation, generated storage-cell startup, two ready worktree branches, branch data isolation, managed `scenery db psql`, managed app env, sync DB URL resolution, reset, restore, schema diff, delete, and cleanup of driver-owned compute containers.
- [x] 2026-06-09: Promoted driver reset/restore from backend metadata placeholders to pageserver timeline mutations. Reset removes the old compute endpoint, creates a replacement timeline from the parent branch timeline, and restarts compute on the persisted port when possible; restore accepts LSN refs directly or resolves RFC3339 timestamps through pageserver before creating the replacement timeline.
- [x] 2026-06-09: Closed the reset/restore validation loop with focused `internal/neonselfhost` and `cmd/scenery` tests, `go test ./...`, `jq empty` on changed JSON/schema files, `git diff --check`, and `scenery harness self --json --write`. The real harness passed with warnings only for the existing review-due UI doc, large-file warnings, and slow-test timing warnings.
- [x] 2026-06-09: Folded the driver into the main `scenery` CLI as `scenery internal neon-selfhost-driver`, removed the source-built `neon-selfhost-driver` toolchain artifact and standalone `cmd/scenery-neon-selfhost-driver`, and updated `scenery db neon install --json` to record `cell.json.driver.kind: "builtin"` while preserving explicit external-driver and legacy `cell.json.driver.path` support.
- [x] 2026-06-09: Added the `neon-selfhost` umbrella image artifact to the toolchain manifest so `scenery system toolchain sync --tool neon-selfhost --images --json` selects the storage-cell, compute-node, MinIO, and MinIO client images. Unknown `--tool` selectors now fail closed instead of returning an empty successful status.
- [x] 2026-06-09: Made branch compute container identities collision-safe across projects by deriving new names from sanitized project plus public branch ID suffix, while labeling fresh compute containers with `scenery.project`, `scenery.branch_id`, and `scenery.branch`.
- [x] 2026-06-09: Updated `README.md`, `SKILL.md`, `docs/agent-guide.md`, and `docs/local-contract.md` to mark real selfhost branch compute creation and default harness coverage as implemented while keeping sync slot lifecycle hardening and release-grade driver distribution as experimental.
- [x] 2026-06-09: Added advisory file locking for concurrent local Neon mutation paths. The built-in driver now holds `<neon-root>/backend.lock` across backend read/port allocation/mutation/write/compute startup, and Scenery lease registry mutations hold `<neon-root>/branches.lock`.
- [x] 2026-06-09: Removed the public `--with-neon-selfhost` self-harness flag. Default, race, and release self-harness modes now run the Docker-backed Neon lifecycle proof; `--quick` remains the smaller non-Docker loop.
- [x] 2026-06-09: Closed the remaining sync isolation proof in the real Neon self-harness. The harness now checks that parallel branch worktrees resolve distinct Neon branch `DatabaseURL`s and distinct managed sync replication stream IDs, replication slot names, and Postgres application names.

## Surprises & Discoveries

- 2026-06-09: `scenery inspect docs --json` reports 43 documents, 1 review-due document, and 0 stale documents. The only review-due document is `docs/ui-agent-contract.md`, which is unrelated to this database/toolchain plan.
- 2026-06-09: The current checked-in branch provider already prefers `SCENERY_DEV_NEON_SELFHOST_DRIVER` over `SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER`, records ready lease endpoints from driver JSON, protects parent branch consumption, and delegates reset/restore/delete/diff when a driver is configured. The missing behavior is a default built-in driver plus a real self-hosted Neon implementation.
- 2026-06-09: `cmd/scenery/db_neon.go` and the surrounding Neon command files are already large enough that this feature should add `internal/neonselfhost/` and small CLI integration files instead of continuing to grow the existing command file.
- 2026-06-09: The `neon-selfhost-driver` must return a successful `pending` JSON result for `ensure` until real tenant/timeline/compute lifecycle lands. Otherwise, simply enabling the skeleton driver would turn branch checkout from the existing pending-backend behavior into a hard failure.
- 2026-06-09: Upstream Neon's Docker example still includes a static compute wrapper, but Scenery's branch-isolation contract needs Compose to own only the shared storage cell. The generated compute template files are now driver inputs rather than a started Compose service.
- 2026-06-09: A recorded reachable branch compute is enough for the driver to prove and return a ready endpoint, which gives `scenery up`, `db psql`, DB lifecycle, and sync the same redacted endpoint contract before the driver can create computes itself.
- 2026-06-09: The pageserver OpenAPI states that `POST /v1/tenant/{tenant_id}/timeline` can recreate the same timeline successfully when parameters match, and that callers should retry timeline creation until success for durability. The driver now uses that idempotent shape for parent and branch timeline bootstrap.
- 2026-06-09: TCP readiness is too weak for app-session consumption because an open port can precede SQL readiness or target database creation. The managed driver now treats `psql` verification and database creation as the ready boundary.
- 2026-06-09: The Neon pageserver container requires both `pageserver.toml` and `identity.toml`; without the identity file it exits before the HTTP API is usable. The generated pageserver config also needs the upstream Docker Compose emergency-mode fields when no storage controller is present.
- 2026-06-09: Branch compute containers are driver-owned and live outside Compose, so `scenery db neon uninstall --destroy-data` must remove remaining Scenery-labeled Neon containers after Compose teardown. Otherwise stale compute containers can be reused across generated cell roots with old mounted scripts.
- 2026-06-09: The compute image used by upstream Docker Compose does not include `nc`, so generated compute startup scripts must avoid depending on netcat. Bash `/dev/tcp` probing is sufficient because the script already runs under Bash.
- 2026-06-09: Ready selfhost endpoints require a password-bearing DSN for non-interactive `psql` and sync consumption. Scenery keeps public endpoint metadata redacted, but synthesizes `postgres://cloud_admin:***@...` in managed `DatabaseURL` values for the `neon-selfhost-driver` source.
- 2026-06-09: Reset/restore need to distinguish the durable Scenery parent branch from the pageserver ancestor used for a specific restore. Restore may branch from the previous branch timeline at an LSN/timestamp, but later reset should still target the configured parent branch timeline.
- 2026-06-09: A source-built driver artifact is not enough for prebuilt CLI users because `go build ./cmd/scenery-neon-selfhost-driver` only works inside the Scenery source checkout. Folding the driver into `scenery` keeps the branch-driver contract without requiring a second release artifact.
- 2026-06-09: The toolchain manifest needs an image umbrella separate from the built-in branch driver. `neon-selfhost` selects the Docker images, while `scenery internal neon-selfhost-driver` is the executable driver surface.
- 2026-06-09: Branch-name-only compute container names collide across apps sharing the same Neon cell. The driver must treat `branch_id` as the unique branch identity and expose project/branch labels for Docker inspection.
- 2026-06-09: Atomic replacement alone is not enough for `backend.json` or `branches.json`; concurrent read-modify-write operations need advisory locks or one agent can overwrite another agent's branch update.

## Decision Log

- Decision: Fold `neon-selfhost-driver` into the main `scenery` CLI as `scenery internal neon-selfhost-driver`.
  Rationale: The driver is part of Scenery's built-in self-hosted Neon dev-cell behavior, and source-built-only toolchain installation fails for normal prebuilt CLI users inside app repositories. Keeping the JSON command shape under `scenery internal` preserves the tested driver contract while removing the second-binary packaging problem.
  Date/Author: 2026-06-09 / Codex

- Decision: Keep `SCENERY_DEV_NEON_SELFHOST_DRIVER` as the highest-priority override, but make it an explicit development/testing escape hatch instead of the normal user path.
  Rationale: Agents and users should get the built-in driver through `scenery db neon install`, while tests and local experiments still need a direct executable override.
  Date/Author: 2026-06-09 / Codex

- Decision: Keep `SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` as the last fallback only.
  Rationale: It is useful for fast local and harness tests because it returns Postgres-shaped branch endpoints, but it is not the actual Neon dev-cell backend and must not masquerade as one.
  Date/Author: 2026-06-09 / Codex

- Decision: Store backend tenant, timeline, compute, and port metadata in a driver-owned `backend.json`, separate from public Scenery branch leases.
  Rationale: `branches.json` is the Scenery registry consumed by `db branch status/list` and app-session env resolution. Neon internals such as tenant IDs, timeline IDs, compute container names, and tombstones belong behind the driver boundary.
  Date/Author: 2026-06-09 / Codex

- Decision: Run real Docker/Neon lifecycle coverage in default, race, and release self-harness modes, while keeping `--quick` as the smaller non-Docker loop.
  Rationale: The branch driver is now built into the CLI and the self-hosted Neon proof is the default correctness contract for this provider; agents still need a quick mode when live Docker substrates are intentionally out of scope.
  Date/Author: 2026-06-09 / Codex

## Outcomes & Retrospective

Completed on 2026-06-09.

The self-hosted Neon driver is now built into the main `scenery` CLI, recorded by
`scenery db neon install --json`, and used by normal branch checkout/worktree/app
session flows without manually setting `SCENERY_DEV_NEON_SELFHOST_DRIVER`.
Generated local storage-cell state starts through Scenery-owned Docker Compose,
while branch compute endpoints remain driver-owned and SQL-ready before app
consumption. Reset, restore, delete, and schema diff operate behind the existing
Scenery branch safety gates. Default, race, and release self-harness modes run
the real Docker-backed Neon lifecycle proof; `--quick` keeps the smaller
non-Docker path.

## Context and Orientation

Start with these files and surfaces:

- `docs/plans/0065-scenery-managed-neon-dev-cell.md` for the current Neon contract, implemented local branch lease behavior, and remaining branch-provider gaps.
- `docs/local-contract.md` for CLI grammar, JSON schemas, generated state paths, driver env vars, and current Neon/sync behavior.
- `README.md`, `docs/agent-guide.md`, `SKILL.md`, and `docs/local-contract.md` for the current done-vs-experimental status of built-in selfhost branch creation, opt-in harness coverage, sync slot lifecycle hardening, and driver distribution.
- `docs/environment.md` and `docs/environment.registry.json` for the approved env-var registry.
- `scenery.toolchain.json`, `internal/toolchain/`, and `cmd/scenery/toolchain.go` for managed binary/image artifacts.
- `cmd/scenery/db_neon.go` for generated dev-cell install/start/status/logs/stop/restart/uninstall.
- `cmd/scenery/db_neon_driver.go` for the current executable driver contract.
- `cmd/scenery/db_neon_provider.go` for provider delegation, ready lease consumption, parent protection, and no-driver placeholder errors.
- `cmd/scenery/db_branch.go`, `cmd/scenery/db_neon_state.go`, `cmd/scenery/db_neon_restore_points.go`, and `cmd/scenery/worktree.go` for branch pin, lease, restore-point, and worktree behavior.
- `cmd/scenery/dev_services.go`, `cmd/scenery/db_setup.go`, `cmd/scenery/db_seed.go`, `cmd/scenery/psql.go`, and sync startup code for runtime consumption of ready branch endpoints.
- `cmd/scenery/harness_neon.go` for the existing fake-driver self-harness coverage.

Current behavior to preserve:

- App roots are marked by `.scenery.json`.
- `scenery up` is the app-session command; `scenery serve` remains headless API runtime and must not start dev-cell behavior.
- `dev.services.postgres.kind: "neon"` accepts `mode: "self-hosted"` and `isolation: "branch"` only.
- `.scenery/worktree-db.json` is the app/worktree-local branch pin.
- `~/.scenery/agent/substrates/neon/branches.json`, or the equivalent under `SCENERY_AGENT_HOME`, is the Scenery-owned local branch lease registry.
- Ready branch endpoints are stored as redacted host/port/database/role/sslmode metadata, not raw URLs.
- Parent branch leases are protected from app-session consumption.
- `SCENERY_DEV_NEON_SELFHOST_DRIVER` selects the real Neon backend when explicitly set.
- `SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` is a local development fallback, not the self-hosted Neon backend.

The new driver should own these local implementation files under the Neon
substrate root:

```text
cell.json
compose.generated.yml
pageserver_config/pageserver.toml
pageserver_config/identity.toml
compute_templates/config.json
compute_templates/compute.sh
backend.json
branches.json
restore-points.json
logs/
```

`branches.json` and `restore-points.json` remain Scenery public/local contracts.
`backend.json`, pageserver config, compute templates, and backend logs are
driver implementation state.

## Milestones

1. Built-In Driver Contract: expose `neon-selfhost-driver` through `scenery internal neon-selfhost-driver`, define capabilities/status schemas, and keep the existing branch-driver result shape compatible.
2. Real Dev-Cell Topology: generate a working local Neon storage cell with MinIO, bucket init, storage broker, pageserver, three safekeepers, and no static app compute as the branch substrate.
3. Tenant and Parent Bootstrap: driver `ensure` can create or reuse tenant/main timeline state and a protected parent compute endpoint for admin/bootstrap checks.
4. Branch Ensure and Runtime Consumption: driver `ensure` creates branch timelines and compute endpoints, returns ready endpoint metadata, and lets `scenery up`, DB lifecycle, `db psql`, and sync consume non-parent ready branches.
5. Branch Mutations: driver implements reset, delete, restore, and schema diff behind the existing Scenery guards.
6. Status and Debugging: `scenery db neon status --json` reports driver installation, capabilities, backend counts, and actionable degraded states without leaking raw connection URLs.
7. Harness Promotion: default, race, and release self-harness modes prove real Docker Neon branch isolation and sync stream/slot isolation; `--quick` keeps fake-driver coverage for the smaller non-Docker loop.

## Plan of Work

First add the driver as a real built-in CLI concept. Keep backend-specific implementation in `internal/neonselfhost/`, expose it through `scenery internal neon-selfhost-driver`, and keep it able to answer `capabilities --json` and `status --json`. Add schemas for driver capabilities and backend state. The self-hosted Neon branch driver is not a toolchain artifact.

Then wire install and resolution. `scenery db neon install --json` should record the built-in driver and generated state in `cell.json`:

```json
{
  "provider": "neon-selfhost",
  "driver": {
    "kind": "builtin",
    "tool": "neon-selfhost-driver",
    "path": "/usr/local/bin/scenery",
    "version": "dev"
  }
}
```

Driver lookup order should be:

```text
1. SCENERY_DEV_NEON_SELFHOST_DRIVER
2. cell.json driver.kind=builtin
3. legacy cell.json driver.path
4. SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER
5. none, with a clear action to install/start the Neon dev cell
```

Next replace placeholder Compose generation with a working local Neon storage
cell. The generated Compose should start MinIO, a bucket initialization helper,
storage broker, pageserver, and three safekeepers. The driver, not the static
Compose file, should create compute containers per branch:

```text
scenery-neon-compute-onlv-example
scenery-neon-compute-onlv-2f944e601abc
scenery-neon-compute-api-7e592dd6e9de
```

The driver should persist backend metadata separately from Scenery leases:

Current contract note: plan 0072 supersedes this original single-tenant example.
New writes use `scenery.db.neon.selfhost.backend.v2` with
`projects[project].tenant_id` and project-local branch maps; v1 remains a
legacy migration input.

```json
{
  "schema_version": "scenery.db.neon.selfhost.backend.v1",
  "tenant_id": "9ef87a5bf0d92544f6fafeeb3239695c",
  "default_pg_version": 16,
  "branches": {
    "br-local-example": {
      "project": "onlv",
      "branch": "onlv/pricing-agent",
      "timeline_id": "b3b863fa45fa9e57e615f9f2d944e601",
      "parent_timeline_id": "de200bd42b49cc1814412c7e592dd6e9",
      "endpoint_id": "onlv-pricing-agent",
      "compute_container": "scenery-neon-compute-onlv-example",
      "host": "127.0.0.1",
      "port": 55441,
      "database": "onlv",
      "role": "cloud_admin",
      "status": "ready"
    }
  }
}
```

Implement `ensure` as an idempotent operation. It should install/start/check the
dev cell, ensure the tenant exists, ensure the parent timeline exists, ensure
the target timeline exists, ensure the branch compute container exists, wait for
Postgres readiness, create the requested database and role if needed, and return
endpoint metadata. Re-running `ensure` must not reset branch data. Allocate a
stable default branch port from `branch_id`:

```text
base: 55440
port = base + hash(branch_id) % 1000
```

If a port collides, allocate the next free port and persist the chosen port in
`backend.json`.

Implement destructive and comparison actions after ensure works. `reset` must
preserve the public Scenery branch ID while replacing the backend timeline and
old compute endpoint.
`delete` must stop and remove branch compute containers and remove or tombstone
backend metadata without leaving app-connectable endpoints behind. `restore`
should support raw LSN inputs and RFC3339 timestamps resolved through the
pageserver path.
`diff` should start with schema diff only, using `pg_dump --schema-only
--no-owner --no-privileges` against current and target branches and returning a
unified diff through the existing `scenery.db.branch.diff.v1` surface.

Finally harden status, docs, and harnesses. `scenery db neon status --json`
should stop saying backend branch integration is pending when the real driver is
installed and healthy. It should report driver status, capabilities, path,
version, tenant ID, branch count, compute count, component health, and next
action. Default self-harness runs the real Docker Neon lifecycle proof, while
`--quick` keeps the fake-driver path fast. Cover two worktrees, two branches,
managed sync branch URL resolution, distinct sync stream/slot identity,
isolated writes, and safe cleanup.

## Concrete Steps

1. Run `scenery inspect docs --json` and review `summary.review_due_count`, `review_due`, and `stale` fields before implementation.
2. Add `internal/neonselfhost/` and expose it through `scenery internal neon-selfhost-driver` with `capabilities --json`, `status --json`, argument parsing, strict JSON output, and focused tests.
3. Add `docs/schemas/scenery.db.neon.driver.capabilities.v1.schema.json` and `docs/schemas/scenery.db.neon.selfhost.backend.v1.schema.json`; include them in schema inventory/harness checks.
4. Keep self-hosted Neon image refs in `scenery.toolchain.json`, regenerate `internal/toolchain/manifest_gen.go` using the existing generator, and keep toolchain tests focused on external artifacts/images.
5. Teach `scenery db neon install --json` to record the built-in driver and write `cell.json.driver`; keep install idempotent and honest when downloads/images are unavailable.
6. Add driver lookup from explicit env, installed cell built-in or legacy `cell.json.driver.path`, then the local Postgres-shaped fallback.
7. Replace generated Neon Compose/config output with the working storage-cell topology and update status probes for required containers/listeners.
8. Implement `backend.json` read/write helpers with ownership checks, schema versioning, atomic writes, and corruption errors that tell users how to recover.
9. Implement driver `ensure` for parent and branch timelines, compute startup, readiness polling, database/role creation, endpoint JSON, and restore-point creation.
10. Implement `reset`, `delete`, `restore`, and schema-only `diff`, reusing existing Scenery safety checks and preserving public `br-local-*` branch identity.
11. Update `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, `docs/environment.md`, `docs/environment.registry.json`, `docs/app-development-cookbook.md`, and `docs/knowledge.json` for the built-in driver behavior.
12. Add real Neon self-harness coverage to default/race/release modes and keep quick fake-driver coverage.
13. Update plan 0065 progress or outcomes to point at this plan when the implementation absorbs its remaining real-provider milestone.

## Validation and Acceptance

For plan-only edits, validate with:

```sh
jq empty docs/knowledge.json
scenery inspect docs --json
git diff --check
```

For the first built-in driver slice, validate with:

```sh
go test ./cmd/scenery ./internal/toolchain ./internal/neonselfhost
go test ./...
go build -o "$(mktemp -d)/scenery" ./cmd/scenery
scenery internal neon-selfhost-driver capabilities --json
scenery inspect docs --json
git diff --check
```

Do not run `go install ./cmd/scenery` during agent validation unless the human
explicitly asks; multiple worktrees share the same installed `scenery` path.

For real dev-cell and branch lifecycle slices, validate with focused tests and
the default Docker-backed harness:

```sh
go test ./cmd/scenery ./internal/neonselfhost
go test ./...
scenery db neon install --json
scenery db neon start --json
scenery db neon status --json
scenery db branch checkout feature/x --json
scenery db psql -- -c "select 1"
scenery db branch reset --yes
scenery db branch restore --at <ref> --yes --json
scenery db branch diff main --json
scenery db branch delete feature/x --force
scenery harness self --json --write
```

The implementation is accepted when:

- A fresh machine can install/start the Neon dev cell through Scenery commands without manually setting `SCENERY_DEV_NEON_SELFHOST_DRIVER`.
- `scenery db neon status --json` reports the managed driver, capabilities, backend counts, component health, and clear next actions.
- `scenery db branch checkout <branch> --json` returns a ready non-parent lease with endpoint metadata after the dev cell is ready.
- `scenery up`, DB apply/seed/setup, `scenery db psql`, and sync consume the branch endpoint and refuse protected parent branches.
- Two worktrees can run against separate Neon branches without database or sync slot/publication collision.
- Reset, restore, delete, and schema diff work behind existing destructive guards.
- Default self-harness remains deterministic with the real Neon proof enabled; `--quick` remains available for the smaller non-Docker loop.

## Idempotence and Recovery

All generated local substrate files must be safe to regenerate. `scenery db neon
install --json` may update `cell.json`, Compose/config files, and driver paths,
but it must not delete branch leases, backend timelines, compute containers, or
restore points unless the user explicitly runs an uninstall or destructive
branch command.

Driver `ensure` must be idempotent. If a branch, timeline, compute container,
database, role, or port already exists and matches `backend.json`, reuse it. If
metadata exists but the compute container is missing, recreate the compute on
the persisted port. If a persisted port is occupied by an unrelated process,
report a clear degraded status and recovery action before allocating a new port.

`reset`, `restore`, and `delete` must keep the current safety gates in the main
CLI: parent branch protection, current branch force rules, and `--yes` for
destructive reset/restore. Backend failures should leave enough metadata for
the next run to inspect and either retry or tombstone safely. A half-deleted
branch must not leave an app-connectable compute endpoint behind.

If `backend.json` is corrupt, `status` should report the corruption and point to
the file path. Recovery should prefer a non-destructive repair command or
regeneration path. Manual deletion should be documented only for dev-cell state
that can be safely recreated.

## Artifacts and Notes

Expected new or changed files include:

```text
cmd/scenery/internal.go
cmd/scenery/db_neon_toolchain.go
internal/neonselfhost/cell.go
internal/neonselfhost/compose.go
internal/neonselfhost/pageserver.go
internal/neonselfhost/compute.go
internal/neonselfhost/driver.go
internal/neonselfhost/state.go
docs/schemas/scenery.db.neon.driver.capabilities.v1.schema.json
docs/schemas/scenery.db.neon.selfhost.backend.v1.schema.json
scenery.toolchain.json
internal/toolchain/manifest_gen.go
docs/local-contract.md
docs/agent-guide.md
SKILL.md
docs/environment.md
docs/environment.registry.json
docs/app-development-cookbook.md
docs/knowledge.json
docs/plans/0065-scenery-managed-neon-dev-cell.md
```

The branch driver command contract should stay compatible with the current main
CLI runner:

```sh
scenery internal neon-selfhost-driver capabilities --json
scenery internal neon-selfhost-driver status --json
scenery internal neon-selfhost-driver ensure --project onlv --parent-branch main --branch onlv/pricing-agent --branch-id br-local-example --database onlv --role cloud_admin --ttl 168h --json
scenery internal neon-selfhost-driver reset --project onlv --parent-branch main --branch onlv/pricing-agent --branch-id br-local-example --database onlv --role cloud_admin --json
scenery internal neon-selfhost-driver restore --project onlv --parent-branch main --branch onlv/pricing-agent --branch-id br-local-example --database onlv --role cloud_admin --at <ref> --json
scenery internal neon-selfhost-driver diff --project onlv --parent-branch main --branch onlv/pricing-agent --branch-id br-local-example --database onlv --role cloud_admin --target main --json
scenery internal neon-selfhost-driver delete --project onlv --parent-branch main --branch onlv/pricing-agent --branch-id br-local-example --database onlv --role cloud_admin --json
```

Ready output should continue to look like:

```json
{
  "status": "ready",
  "message": "self-hosted Neon branch ready",
  "endpoint": {
    "host": "127.0.0.1",
    "port": 55441,
    "database": "onlv",
    "role": "cloud_admin",
    "sslmode": "disable",
    "source": "neon-selfhost-driver"
  },
  "restore_point": {
    "ref": "0/16F9A70",
    "branch_id": "br-local-example",
    "branch": "onlv/pricing-agent",
    "project": "onlv",
    "database_name": "onlv",
    "source": "branch-created",
    "created_at": "2026-06-09T00:00:00Z"
  }
}
```

## Interfaces and Dependencies

Primary CLI surfaces:

- `scenery system toolchain list|sync|verify [--json] [--tool neon-selfhost] [--images]`
- `scenery system toolchain sync --tool neon-selfhost --images --json`
- `scenery db neon install|start|status|logs|stop|restart|uninstall [--json]`
- `scenery db branch checkout|status|list|reset|restore|diff|delete|expire|prune`
- `scenery worktree create|list|remove`
- `scenery up`, `scenery down --db`, `scenery down --state`
- `scenery db psql`
- `scenery harness self --json --write`

Primary JSON contracts and state files:

- `scenery.toolchain.v1`
- `scenery.toolchain.status.v1`
- `scenery.db.neon.status.v1`
- `scenery.db.branch.status.v1`
- `scenery.db.branch.list.v1`
- `scenery.db.branch.restore.v1`
- `scenery.db.branch.diff.v1`
- `scenery.db.branch.registry.v1`
- `scenery.db.neon.restore_points.v1`
- `scenery.db.neon.driver.capabilities.v1`
- `scenery.db.neon.selfhost.backend.v1`
- `.scenery/worktree-db.json`
- `~/.scenery/agent/substrates/neon/cell.json`
- `~/.scenery/agent/substrates/neon/backend.json`

External runtime dependencies:

- Docker with Compose support for the local dev cell.
- Managed Docker images for Neon, Neon compute node, MinIO, and MinIO client as declared in `scenery.toolchain.json`.
- The built-in `scenery internal neon-selfhost-driver` command.
- `pg_dump` availability for schema diff, either from the compute image, managed toolchain, or a documented host dependency selected during implementation.

The hard dependency risk is Neon lifecycle semantics, not CLI parsing. Treat
this as a Scenery-managed local development substrate, not a production
self-hosted Neon platform.
