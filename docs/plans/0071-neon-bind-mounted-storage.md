# Bind-Mounted Neon Storage

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Onlava's self-hosted Neon dev cell currently stores much of its durable runtime
state in Docker-managed anonymous volumes. The Onlava substrate root is easy to
find at `~/.onlava/agent/substrates/neon`, but the actual MinIO, pageserver,
safekeeper, and storage-broker bytes live under Docker Desktop's internal
volume store. That makes local inspection, backup, and explicit destroy
semantics harder than they should be.

This plan moves Neon durable storage to Onlava-owned bind-mounted directories
under the existing Neon substrate root:

```text
~/.onlava/agent/substrates/neon/
  data/
    minio/
    pageserver/
    safekeeper-1/
    safekeeper-2/
    safekeeper-3/
    storage-broker/
```

The important product invariant is that this remains one shared local Neon
storage substrate per Onlava agent home. New Git worktrees do not receive
separate storage directories. A worktree gets only its local branch pin at
`<worktree>/.onlava/worktree-db.json`; Neon branch isolation lives in shared
pageserver timelines, safekeeper WAL, MinIO remote storage, `backend.json`, and
`branches.json`.

The user-visible result should be simple:

```sh
onlava db neon install --json
onlava db neon start --json
onlava worktree create pricing-agent --json
cd ../onlv-pricing-agent
onlava up
```

All worktrees share `~/.onlava/agent/substrates/neon/data`, while each worktree
resolves a distinct ready branch compute endpoint through its own pin and lease.

## Progress

- [x] 2026-06-09: Created ExecPlan `0071-neon-bind-mounted-storage.md` from the requested no-Docker-volumes storage design.
- [x] 2026-06-09: Linked plan 0071 from `docs/plans/active.md`.
- [x] 2026-06-09: Indexed plan 0071 in `docs/knowledge.json`.
- [x] 2026-06-09: Updated the plan to use a fresh-start cutover instead of preserving existing anonymous Docker volume data.
- [x] 2026-06-09: Implemented bind-mounted generated storage directories and Compose mounts for MinIO, pageserver, safekeepers, and storage broker.
- [x] 2026-06-09: Added start-time fresh-start guard behavior for existing Onlava Neon containers with Docker-managed `/data` volumes.
- [x] 2026-06-09: Updated status JSON/schema, uninstall semantics, docs, and tests for the explicit shared storage root.
- [x] 2026-06-09: Validated with focused Neon/worktree tests, `go test ./cmd/onlava`, `go test ./...`, docs inspection, JSON parsing, and diff whitespace checks.

## Surprises & Discoveries

- 2026-06-09: `onlava inspect docs --json` reports `summary.review_due_count: 1`; the only review-due document is `docs/ui-agent-contract.md`, unrelated to this storage plan.
- 2026-06-09: `onlava db neon status --json` on the current machine reports the active Neon root as `/Users/petrbrazdil/.onlava/agent/substrates/neon`, with `backend.json`, `branches.json`, `restore-points.json`, generated Compose, pageserver config, and compute templates under that root.
- 2026-06-09: `docker inspect` on the live `onlava-neon-*` containers shows anonymous Docker volumes mounted at `/data` for MinIO, pageserver, safekeepers, and storage broker. The pageserver also bind-mounts `pageserver_config` to `/data/.neon`, and branch compute containers only bind-mount generated compute templates.

## Decision Log

- Decision: Store Neon runtime data under `~/.onlava/agent/substrates/neon/data`, not under app roots or worktree roots.
  Rationale: The Neon dev cell is a shared local substrate. Worktrees isolate data through Neon timelines and branch leases, not by copying the entire storage cell.
  Date/Author: 2026-06-09 / Codex

- Decision: Keep branch compute containers ephemeral and do not add per-compute data directories unless a future Neon requirement proves they need durable local state.
  Rationale: The compute containers currently mount only generated config/scripts and expose a branch-specific Postgres endpoint. Durable database bytes belong to pageserver, safekeepers, and remote storage.
  Date/Author: 2026-06-09 / Codex

- Decision: Do not preserve existing anonymous Docker volume data in this plan.
  Rationale: The requested implementation can start fresh. Keeping the plan fresh-start only avoids a heavier data-copy workflow and makes the storage contract simpler. Existing anonymous-volume cells should require an explicit destroy/reinstall before switching to bind-mounted storage.
  Date/Author: 2026-06-09 / Codex

- Decision: `onlava db neon uninstall` should preserve `data/` by default, and `onlava db neon uninstall --destroy-data` should remove it.
  Rationale: Stopping or uninstalling generated runtime files should not destroy local databases unless the command name and flag clearly ask for that.
  Date/Author: 2026-06-09 / Codex

- Decision: New worktrees should only create or reuse `.onlava/worktree-db.json` pins and should not allocate storage directories.
  Rationale: A new worktree should inherit from its parent branch by creating a Neon branch timeline in shared storage, not by copying or duplicating the storage cell.
  Date/Author: 2026-06-09 / Codex

## Outcomes & Retrospective

Completed on 2026-06-09.

The self-hosted Neon dev cell now records `storage.mode: "bind"` and exposes
the shared storage root/data directories through `onlava.db.neon.status.v1`.
`onlava db neon install --json` creates the storage directories under the agent
home, and generated Compose bind-mounts each durable `/data` path from
`./data/...`.

`onlava db neon start --json` fails closed when old Onlava Neon containers still
use Docker-managed `/data` volumes. The user-visible recovery path is fresh
destroy/reinstall, not migration. `onlava db neon uninstall --json` now
preserves bind-mounted data by default; `--destroy-data` removes it.

Worktree behavior remains pin/lease based. Worktree creation does not allocate
or copy Neon storage roots.

## Context and Orientation

Start with these files:

- `cmd/onlava/db_neon.go` generates the Neon cell state, writes `cell.json`, writes `compose.generated.yml`, starts/stops/restarts/uninstalls the generated Compose project, and reports `onlava.db.neon.status.v1`.
- `cmd/onlava/db_neon_runtime.go` probes Docker images, containers, and loopback listeners for `onlava db neon status --json`.
- `cmd/onlava/db_neon_state.go` owns local Neon state file read/write helpers.
- `cmd/onlava/db_neon_pin.go`, `cmd/onlava/db_neon_provider.go`, `cmd/onlava/db_neon_restore_points.go`, and `cmd/onlava/worktree.go` own pins, branch leases, restore points, and worktree behavior.
- `internal/neonselfhost/` owns the driver-side `backend.json`, pageserver timeline lifecycle, compute startup, reset/restore/delete/diff behavior, and endpoint readiness.
- `docs/local-contract.md`, `docs/agent-guide.md`, `docs/app-development-cookbook.md`, `SKILL.md`, and `README.md` document the Neon contract and must change when the storage path contract changes.
- `docs/schemas/onlava.db.neon.status.v1.schema.json` may need a storage section if status begins exposing bind-mounted data directories as machine-readable fields.

Current generated files live under `neonSubstrateRoot()`, which resolves to
`<agent-home>/agent/substrates/neon` in normal operation. With the default
agent home, that is:

```text
/Users/petrbrazdil/.onlava/agent/substrates/neon
```

Current generated Compose mounts `./pageserver_config:/data/.neon`, but leaves
other `/data` paths to Docker-managed anonymous volumes. This plan changes the
Compose topology to explicitly bind-mount every durable `/data` path.

## Milestones

Milestone 1: Define storage layout and generated state.

Add storage path helpers for the Neon substrate root. `onlava db neon install`
must create `data/minio`, `data/pageserver`, `data/safekeeper-1`,
`data/safekeeper-2`, `data/safekeeper-3`, and `data/storage-broker` with stable
permissions. `cell.json` should record enough storage metadata for status and
runtime commands to know that the generated cell expects bind mounts.

Milestone 2: Generate Compose with explicit bind mounts.

Update `compose.generated.yml` so every service that writes durable data has an
explicit bind mount. MinIO mounts `./data/minio:/data`; pageserver mounts
`./data/pageserver:/data` plus `./pageserver_config:/data/.neon`; each
safekeeper mounts its own directory to `/data`; storage broker mounts
`./data/storage-broker:/data` if it continues to use a data directory. Generated
Compose must not rely on anonymous volumes for Onlava Neon services.

Milestone 3: Detect existing anonymous-volume cells and fail closed.

Before starting a bind-mounted cell, detect existing `onlava-neon-*` containers
with Docker-managed `/data` volumes. Do not silently mix old anonymous-volume
state with the new bind-mounted layout. Return a clear required action that the
user must stop and destroy the old Neon cell before starting fresh with
bind-mounted storage.

Milestone 4: Update lifecycle and cleanup behavior.

`onlava db neon uninstall` should stop/remove containers and generated runtime
files while preserving `data/` unless `--destroy-data` is present. With
`--destroy-data`, it should remove bind-mounted data directories in addition to
Onlava-owned containers. Cleanup must remain scoped to Onlava-owned Neon
containers and files.

Milestone 5: Preserve worktree branch behavior.

`onlava worktree create`, `onlava up`, `onlava db branch checkout`, and session
branch policies should continue to write only branch pins and leases. No
worktree-specific storage directory should be created. Add tests that prove a
new worktree reuses the same shared Neon storage root while receiving a distinct
branch ID and endpoint.

Milestone 6: Document and validate the contract.

Update docs and schemas so agents can discover where Neon bytes live. Add tests
that fail if generated Compose reintroduces anonymous volumes for durable Neon
services. Keep the opt-in real Docker-backed Neon harness working against the
bind-mounted storage layout.

## Plan of Work

First add small storage helpers rather than threading raw paths throughout the
Neon command code. A helper should return the stable data directory set from the
substrate root, and install should create those directories next to existing
generated files. This keeps the layout inspectable and makes tests simple.

Next update Compose generation in `cmd/onlava/db_neon.go`. The generated file
should be deterministic. For every service with a `/data` destination, the YAML
must include an explicit relative bind mount from `./data/...`. Avoid Docker
named volumes and anonymous volumes. The Compose file should remain portable
within the substrate root and should not contain absolute host paths unless the
existing generated style already requires them.

Then implement fresh-start detection. The safe default is fail-closed when an
existing anonymous-volume cell is found. A user or agent should get a precise
required action, such as running
`onlava db neon uninstall --destroy-data --json` and then reinstalling the dev
cell. The plan intentionally does not copy data out of existing Docker volumes.

After the guard exists, update lifecycle commands. `status --json` should make
the storage mode observable, either through an added `storage` object in
`onlava.db.neon.status.v1` or through generated file/status entries if that is
the narrower local pattern. `uninstall --destroy-data` must remove the bind data
root and can also remove Onlava-owned legacy containers/volumes as a destructive
fresh-start cleanup path.

Finally update docs, schemas, and harness coverage. The docs should make clear
that worktrees share the storage substrate and isolate through branches. The
harness should prove the generated Compose file uses bind mounts and that a
worktree branch does not allocate new storage roots.

## Concrete Steps

1. Inspect current generated Compose and state helpers:

   ```sh
   rg -n "compose.generated|defaultNeonCellState|writeNeonCellState|uninstall|destroy-data|docker inspect|Mounts" cmd/onlava internal/neonselfhost
   ```

2. Add storage helpers in the Neon command package:

   - `neonStorageRoot(root string) string`
   - `neonStorageDirs(root string) map[string]string`
   - `ensureNeonStorageDirs(root string) error`
   - Optional status helpers for detecting anonymous `/data` mounts.

3. Update install/state generation:

   - Create the `data/*` directories.
   - Add storage metadata to `cell.json` if needed.
   - Regenerate `compose.generated.yml` with explicit bind mounts.

4. Add tests around generated storage:

   - `onlava db neon install --json` creates every `data/*` directory.
   - Generated Compose includes each expected `./data/...:/data` mount.
   - Generated Compose does not define implicit Docker volumes for Neon durable services.

5. Add fresh-start guard behavior:

   - Detect legacy anonymous `/data` volumes from existing Onlava-labeled containers.
   - Return a clear required action if existing containers still use Docker-managed `/data` volumes.
   - Keep the recovery path explicit: stop/destroy the old generated cell, then reinstall fresh.

6. Update uninstall and destroy behavior:

   - Preserve `data/` by default.
   - Remove `data/` only for `--destroy-data`.
   - Keep cleanup scoped to Onlava-owned Neon containers and generated state.

7. Update worktree tests:

   - `worktree create` for a Neon app writes the target pin and does not create any per-worktree `data` directory.
   - Distinct worktree pins still map to distinct branch IDs and shared global lease storage.

8. Update docs and schemas:

   - `docs/local-contract.md`
   - `docs/agent-guide.md`
   - `docs/app-development-cookbook.md`
   - `SKILL.md`
   - `README.md`
   - `docs/schemas/onlava.db.neon.status.v1.schema.json` if status gains a storage object.

9. Validate with focused tests, full tests, docs inspection, and the opt-in real Neon harness when practical.

## Validation and Acceptance

Required local validation:

```sh
go test ./cmd/onlava -run 'TestDBNeon|TestWorktree|TestParseDBNeonArgs'
go test ./internal/neonselfhost
go test ./...
jq empty docs/knowledge.json
onlava inspect docs --json
git diff --check
```

Use a worktree-local build for manual smoke tests:

```sh
tmp="$(mktemp -d)"
go build -o "$tmp/onlava" ./cmd/onlava
"$tmp/onlava" db neon install --json
"$tmp/onlava" db neon status --json
```

Do not run `go install ./cmd/onlava` during agent validation unless the human
explicitly asks. If a human asks for installation, install only after the build
and test gates pass.

Manual or opt-in Docker-backed validation when practical:

```sh
onlava db neon status --json
onlava db neon start --json
onlava db branch status --json
onlava harness self --json --write --with-neon-selfhost
```

Acceptance criteria:

- Fresh `onlava db neon install --json` creates `data/minio`, `data/pageserver`,
  `data/safekeeper-1`, `data/safekeeper-2`, `data/safekeeper-3`, and
  `data/storage-broker` under the Neon substrate root.
- Generated Compose bind-mounts every durable `/data` path from `./data/...`.
- A fresh start does not create anonymous Docker volumes for Onlava Neon
  durable storage.
- Existing anonymous-volume installs fail closed with a clear fresh-start action.
- `uninstall` preserves `data/`; `uninstall --destroy-data` removes `data/`.
- New worktrees do not create storage directories and continue to isolate through
  branch pins, leases, timelines, and compute endpoints.

## Idempotence and Recovery

Install and Compose generation must be idempotent. Re-running `onlava db neon
install --json` should recreate missing generated files and missing storage
directories without deleting existing data.

If a generated Compose rewrite succeeds but startup fails, the user should be
able to run `onlava db neon status --json`, inspect `compose.generated.yml`, and
rerun `onlava db neon start --json` after fixing Docker or port readiness.

If `uninstall --destroy-data` removes containers but fails to delete `data/`,
rerunning the same command should retry the data removal without treating absent
containers as fatal.

## Artifacts and Notes

Expected final storage layout:

```text
~/.onlava/agent/substrates/neon/
  backend.json
  branches.json
  restore-points.json
  cell.json
  compose.generated.yml
  compute_templates/
  pageserver_config/
  logs/
  data/
    minio/
    pageserver/
    safekeeper-1/
    safekeeper-2/
    safekeeper-3/
    storage-broker/
```

Expected per-worktree state remains:

```text
<worktree>/.onlava/worktree-db.json
```

No app or worktree should receive a copy of `data/`. The storage root is shared
because Neon branches are logical timelines in the shared dev cell.

The current live machine has a ready Neon cell with anonymous Docker volumes.
This plan does not preserve that data. The cutover path is to stop/destroy the
old generated Neon cell and start fresh with bind-mounted storage.

Validation run on 2026-06-09:

```sh
go test ./cmd/onlava -run 'TestDBNeon|TestWorktreeCreateListAndRemoveWithNeonPin'
go test ./internal/neonselfhost
go test ./cmd/onlava
go test ./...
jq empty docs/knowledge.json docs/schemas/onlava.db.neon.status.v1.schema.json
onlava inspect docs --json
git diff --check
```

The opt-in real Docker-backed Neon proof was not run during this implementation
pass because the current live cell still uses legacy anonymous `/data` volumes,
and exercising the fresh-start path would require destructive
`onlava db neon uninstall --destroy-data` against that cell.

## Interfaces and Dependencies

Primary CLI interfaces:

- `onlava db neon install --json`
- `onlava db neon start --json`
- `onlava db neon status --json`
- `onlava db neon stop --json`
- `onlava db neon uninstall [--destroy-data] --json`
- `onlava worktree create|list|remove --json`
- `onlava db branch status|checkout|list|reset|restore|delete|diff --json`

Machine-readable contracts that may change:

- `onlava.db.neon.status.v1`
- `onlava.db.neon.cell.v1`
- Generated `compose.generated.yml`
- Generated state paths under `~/.onlava/agent/substrates/neon`

External dependencies:

- Docker and Docker Compose for the local Neon storage cell.
- Existing Neon images from `onlava.toolchain.json`.
- The source-built `neon-selfhost-driver` from plan 0070.
- MinIO remains the remote-storage stand-in used by self-hosted Neon; the change
  is where MinIO stores its `/data` on the host.

Non-goals:

- Do not create per-worktree Neon storage cells.
- Do not copy entire databases for worktree creation.
- Do not preserve data from existing anonymous Docker volumes.
- Do not change branch isolation semantics or endpoint redaction.
- Do not make cloud Neon support part of this storage change.
