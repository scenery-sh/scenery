# Neon Selfhost Project-Tenant Mapping

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Onlava's `dev.services.postgres.project` must map to a Neon project boundary. In
the self-hosted Neon driver, that means each Onlava Neon project gets its own
Neon tenant. A branch such as `onlv/pricing-agent` belongs to the `onlv` project
tenant; another app or project using the same branch label must never share
tenant state, timeline state, backend metadata, restore state, or cleanup scope
with `onlv`.

Today the selfhost backend state is still shaped around one top-level
`tenant_id` plus a single `branches` map. The current `BackendState` has
`TenantID`, `DefaultPGVersion`, and `Branches` at the top level. That shape makes
the first project to touch the dev cell implicitly own the tenant for later
projects. The driver also derives tenant IDs only when the top-level tenant is
empty.

This plan migrates the selfhost driver to an explicit project model:

```json
{
  "schema_version": "onlava.db.neon.selfhost.backend.v2",
  "provider": "neon-selfhost",
  "projects": {
    "onlv": {
      "tenant_id": "...",
      "default_pg_version": 16,
      "branches": {
        "br-local-...": {
          "project": "onlv",
          "branch": "onlv/pricing-agent",
          "timeline_id": "...",
          "parent_timeline_id": "...",
          "compute_container": "onlava-neon-compute-onlv-abc123",
          "host": "127.0.0.1",
          "port": 55441,
          "database": "onlv",
          "role": "cloud_admin",
          "status": "ready"
        }
      }
    }
  }
}
```

The outcome: two Onlava projects can safely use the same branch name, worktree
name, or session template without cross-tenant collisions. `onlava db branch
prune`, `delete`, `reset`, `restore`, `diff`, `down --db`, and compute cleanup
must all operate inside the selected project tenant.

## Progress

- [x] 2026-06-09: Created this ExecPlan as `docs/plans/0072-neon-project-tenants.md`.
- [x] 2026-06-09: Linked this plan from `docs/plans/active.md`.
- [x] 2026-06-09: Indexed this plan in `docs/knowledge.json`.
- [x] Implement backend state v2 with `projects[project].tenant_id` and project-local branch maps.
- [x] Add migration from the existing top-level `tenant_id` / `branches` backend state into v2.
- [x] Update tenant/timeline lifecycle to resolve the current project first.
- [x] Keep branch compute container names and Docker labels project/branch-ID-safe, and add tenant labels where useful for inspection.
- [x] Add project-aware branch port allocation across all projects.
- [x] Add focused tests for two projects using the same branch label.
- [x] Extend the default real Neon selfhost harness to prove tenant separation.
- [x] Update docs and schemas.

## Surprises & Discoveries

- 2026-06-09: The current backend state has one top-level `tenant_id` and a global branch map. Evidence: `internal/neonselfhost/state.go` defines `BackendState.TenantID`, `BackendState.DefaultPGVersion`, and `BackendState.Branches`.
- 2026-06-09: The current backend ID derivation sets `state.TenantID` only when it is empty. Evidence: `internal/neonselfhost/pageserver.go` derives `state.TenantID` from the first project-like input when the field is blank.
- 2026-06-09: Branch compute container identity has already been made safer than the original bug report: current code derives container names from project plus branch ID suffix and labels fresh compute containers with `onlava.project`, `onlava.branch_id`, and `onlava.branch`. This plan must preserve that work while moving the tenant and branch maps to project scope.
- 2026-06-09: The real selfhost proof is now part of default non-quick `onlava harness self`; older opt-in `--with-neon-selfhost` references are stale and must not be reintroduced.
- 2026-06-09: Go map values cannot be returned as mutable project pointers, so the project resolver returns a project value plus key and callers write the updated project back into `state.Projects`.
- 2026-06-09: `onlava.db.neon.status.v1` can remain the status envelope version because the backend summary change is additive: it now accepts backend schema v2 and optional `project_count` / `projects`.

## Decision Log

- Decision: An Onlava Neon project maps to a Neon tenant in the self-hosted backend.
  Rationale: Project is the stable isolation boundary. Branches and worktrees are cheap children under that project; separate Onlava apps/projects must not share tenant/timeline state.
  Date/Author: 2026-06-09 / pbrazdil + agent

- Decision: Backend state moves from top-level `tenant_id` / `branches` to `projects[project].tenant_id` / `projects[project].branches`.
  Rationale: This represents the actual model directly and avoids hidden project filtering over a global branch map.
  Date/Author: 2026-06-09 / pbrazdil + agent

- Decision: Compute container identity remains project plus branch ID, with labels for project, branch ID, branch name, and tenant where available.
  Rationale: Branch names are not globally unique. Two projects can both have `feature/foo`. Docker container names are global on the host, so names must not be branch-label-only.
  Date/Author: 2026-06-09 / pbrazdil + agent

- Decision: Keep public Onlava branch IDs stable.
  Rationale: `.onlava/worktree-db.json`, `branches.json`, restore points, and user-facing branch status should not churn because the backend tenant shape changes.
  Date/Author: 2026-06-09 / pbrazdil + agent

## Outcomes & Retrospective

The backend state now writes `onlava.db.neon.selfhost.backend.v2`, migrates v1
on read, and scopes ensure/reset/restore/delete/diff to the selected project.
Status JSON reports project summaries, compute labels include tenant IDs, and
the default real Neon selfhost harness includes a two-project same-branch tenant
separation proof.

## Context and Orientation

Start with these files:

- `internal/neonselfhost/state.go`: defines v2 `BackendState`, `BackendProject`, `BackendBranch`, v1 migration, `ReadBackendState`, `WriteBackendState`, and host-global `AllocateBranchPort`.
- `internal/neonselfhost/pageserver.go`: derives project tenant/timeline IDs and calls the pageserver tenant/timeline APIs.
- `internal/neonselfhost/lifecycle.go`: handles reset, restore, delete, diff, backend branch lookup, and branch metadata derivation.
- `internal/neonselfhost/compute.go`: starts and inspects branch compute containers.
- `internal/neonselfhost/postgres.go`: verifies Postgres readiness and creates the requested database.
- `cmd/onlava/db_neon.go`: emits backend summary in `onlava db neon status --json`.
- `cmd/onlava/db_neon_pin.go`: builds the public worktree pin and already has a stable `Project` field.
- `cmd/onlava/harness_neon.go`: contains the real selfhost harness. It proves two worktrees in one project and two separate projects using the same branch label.

Definitions:

- Onlava Neon project: `dev.services.postgres.project`, normalized through existing config/pin logic.
- Neon tenant: the pageserver tenant that owns timelines for one Onlava Neon project.
- Backend branch: driver-owned metadata for a branch under a project tenant.
- Public branch lease: Onlava-owned `branches.json` / `.onlava/worktree-db.json` metadata consumed by CLI status and app sessions.
- Backend state: driver-owned `backend.json`.

## Milestones

### Milestone 1: Backend state v2 data model

Introduce v2 backend state types while preserving migration compatibility.

Target shape:

```go
const BackendSchemaVersion = "onlava.db.neon.selfhost.backend.v2"

type BackendState struct {
    SchemaVersion string                    `json:"schema_version"`
    Provider      string                    `json:"provider"`
    Projects      map[string]BackendProject `json:"projects"`
    UpdatedAt     string                    `json:"updated_at,omitempty"`
}

type BackendProject struct {
    TenantID         string                   `json:"tenant_id"`
    DefaultPGVersion int                      `json:"default_pg_version"`
    Branches         map[string]BackendBranch `json:"branches"`
    UpdatedAt        string                   `json:"updated_at,omitempty"`
}
```

Keep a legacy reader for the current v1 shape:

```go
type legacyBackendStateV1 struct {
    SchemaVersion    string                   `json:"schema_version"`
    Provider         string                   `json:"provider"`
    TenantID         string                   `json:"tenant_id"`
    DefaultPGVersion int                      `json:"default_pg_version"`
    Branches         map[string]BackendBranch `json:"branches"`
    UpdatedAt        string                   `json:"updated_at,omitempty"`
}
```

Migration rule:

- If reading v1, create `projects`.
- Group branches by `branch.Project`.
- If a branch has empty `Project`, place it under a deterministic legacy project key and report that in backend status.
- For the project that previously owned `state.TenantID`, preserve that tenant ID.
- For any other project, derive a deterministic tenant ID with `stableHexID("tenant:" + project)`.
- Return v2 in memory.
- On the next write, persist v2.

### Milestone 2: Project resolver

Add helper functions:

```go
func projectKey(value string) string
func ensureBackendProject(state *BackendState, project string, pgVersion int) *BackendProject
func backendProjectForOptions(state *BackendState, opts branchActionOptions) (*BackendProject, string, error)
func backendBranchForOptions(project *BackendProject, projectName string, opts branchActionOptions) BackendBranch
```

Rules:

- `projectKey` must match the normalized project value already used by worktree pins.
- Empty project is invalid for driver actions. The driver already requires `--project`; keep that fail-closed behavior.
- Every ensure/reset/restore/delete/diff action must resolve exactly one project before reading or mutating branch state.

### Milestone 3: Tenant/timeline creation per project

Change `ensureBackendIDs` to operate on `BackendProject`, not global
`BackendState`.

Current behavior:

```go
state.TenantID = stableHexID("tenant:" + project)
```

New behavior:

```go
project.TenantID = stableHexID("tenant:" + projectName)
```

Timeline ID derivation should continue to include project. For clarity:

```go
parentTimelineID = stableHexID("parent:" + projectName + ":" + parentBranch)
branchTimelineID = stableHexID("timeline:" + projectName + ":" + branchID)
```

Reset/restore replacement timelines should continue to include project and
branch ID.

### Milestone 4: Project-safe compute identity

Current code already derives compute container names from project plus branch ID
suffix. Preserve that invariant while moving backend branches into project-local
maps.

Expected pattern:

```text
onlava-neon-compute-<safe project>-<short branch id>
```

Example:

```text
onlava-neon-compute-onlv-a1b2c3d4
```

Keep or add Docker labels when starting compute:

```text
onlava.substrate=neon
onlava.component=compute
onlava.project=<project>
onlava.branch=<branch>
onlava.branch_id=<branch_id>
onlava.tenant_id=<tenant_id>
```

Keep existing cleanup label `onlava.substrate=neon` so uninstall still removes
all Onlava-owned Neon containers. The added labels are for inspection and future
targeted cleanup.

### Milestone 5: Project-local branch maps with host-global port allocation

Change `AllocateBranchPort` so it sees all ports across all projects, not just a
project's branches, because ports are host-global.

New signature:

```go
func AllocateBranchPort(state BackendState, project string, branchID string) (int, error)
```

Rules:

- If this project/branch already has a port, reuse it.
- Otherwise collect used ports from every project and branch.
- Derive the preferred port from `project + "\x00" + branchID`.
- Resolve collisions by scanning forward within the configured range.
- If the range is exhausted, return an error rather than reusing a port.

This requires changing `AllocateBranchPort` from returning only `int` to
returning `(int, error)` or adding a checked wrapper.

### Milestone 6: Project-scoped delete/reset/restore/diff

Update all mutation paths to use project-local branches:

- `ensurePendingBranch`
- `replaceBackendBranchTimeline`
- `deleteBackendBranch`
- `diffReadyBranches`
- `findBackendBranch`
- `inspectBackendStatus`

`diffReadyBranches` should only find target branches in the current project
unless the CLI later adds explicit cross-project syntax. For now:

```bash
onlava db branch diff main
```

means "diff against branch `main` inside the same Onlava Neon project."

If a target branch exists in another project only, return:

```text
neon-selfhost-driver diff could not find target backend branch "main" in project "onlv"
```

### Milestone 7: Status and schema updates

Update driver status summary to report projects.

Current summary shape reports top-level tenant, branch count, and compute count.

New summary:

```json
{
  "schema_version": "onlava.db.neon.selfhost.backend.v2",
  "present": true,
  "project_count": 2,
  "branch_count": 4,
  "compute_count": 4,
  "projects": [
    {
      "project": "onlv",
      "tenant_id": "...",
      "branch_count": 2,
      "compute_count": 2,
      "statuses": {
        "ready": 2
      }
    }
  ]
}
```

Update:

- `docs/schemas/onlava.db.neon.selfhost.backend.v1.schema.json` or add a v2 schema.
- `docs/schemas/onlava.db.neon.status.v1.schema.json` if the status payload shape changes.
- `docs/local-contract.md`.
- `README.md`.
- `docs/plans/0070-toolchain-managed-neon-selfhost-driver.md` with a note that plan 0072 supersedes the single-tenant backend assumption.

Prefer adding a v2 backend schema while keeping `onlava.db.neon.status.v1`
stable if possible. Status can add optional project fields without breaking v1
if schema allows it; if not, update schema carefully.

### Milestone 8: Tests and harness

Add focused tests in `internal/neonselfhost`.

Required tests:

1. Reading v1 backend state migrates to v2 projects.
2. Two projects with the same branch name produce different tenant IDs.
3. Two projects with the same branch name produce different compute container names.
4. Two projects with a same branch ID suffix collision still get distinct ports or fail clearly.
5. `diff` does not cross project boundaries.
6. `delete` only deletes the branch inside the current project.
7. `reset` preserves project tenant and parent timeline for that project.
8. `restore` branches from the correct project tenant.

Extend the default real selfhost harness:

- Create two separate app roots:
  - project `neon-selfhost-project-a`
  - project `neon-selfhost-project-b`
- Use the same worktree/branch label in both, for example `same-branch`.
- Run `onlava db branch checkout same-branch --json` for both.
- Assert:
  - backend status is ready for both.
  - backend JSON has two project entries.
  - tenant IDs differ.
  - compute container names differ.
  - host ports differ.
  - creating a table in project A does not appear in project B.
  - deleting branch in project A does not delete project B's branch metadata or compute container.
  - `diff same-branch` from project A never resolves project B.

## Plan of Work

Start by adding the v2 types and migration reader in
`internal/neonselfhost/state.go`. Keep the old struct local to the reader so the
rest of the package only works with v2 after the first milestone.

Then change the lifecycle code to always enter through a project resolver. This
is safer than trying to thread `project` manually through each map lookup. The
invariant should become: no driver action mutates a branch without first
selecting a `BackendProject`.

Next, verify compute identity remains project-safe and add `onlava.tenant_id`
labels where the tenant is available. Compute containers are Docker-global;
project-local backend maps are not enough by themselves.

After that, update status and schemas. Keep the public `branches.json` lease
behavior unchanged. The public Onlava lease already has `Project`, so the
visible CLI behavior should remain stable while the backend implementation
becomes safer.

Finally, extend the real selfhost harness. This should be the acceptance gate.

## Concrete Steps

1. Create `docs/plans/0072-neon-project-tenants.md` with this plan.
2. Add it to `docs/plans/active.md` near plans 0071/0070.
3. Add it to `docs/knowledge.json`.
4. In `internal/neonselfhost/state.go`, replace `BackendState.TenantID`, `BackendState.DefaultPGVersion`, and `BackendState.Branches` with `Projects map[string]BackendProject`.
5. Add legacy v1 read support in `ReadBackendState`.
6. Add `NewBackendState` that initializes `Projects`.
7. Add `NewBackendProject(project string, pgVersion int)`.
8. Update `WriteBackendState` to always write v2.
9. Update `AllocateBranchPort` to scan all projects.
10. Update `backendBranchFromOptions` to become project-aware.
11. Update `ensureBackendIDs` to set `project.TenantID`.
12. Update `ensurePageserverBackend` to accept `project BackendProject` or `tenantID string`.
13. Update `replaceBackendBranchTimeline` to resolve project before branch.
14. Update `deleteBackendBranch` to delete only from the resolved project.
15. Update `diffReadyBranches` to search only the resolved project.
16. Update `inspectBackendStatus` and `buildNeonBackendStatus`.
17. Update schemas.
18. Update docs.
19. Add unit tests.
20. Extend the default real `onlava harness self` Neon proof.
21. Run validation.

## Validation and Acceptance

Run these from the Onlava repo root:

```bash
jq empty docs/knowledge.json docs/environment.registry.json onlava.toolchain.json docs/schemas/*.json
go test ./internal/neonselfhost
go test ./cmd/onlava
go test ./...
go build -o "$(mktemp -d)/onlava" ./cmd/onlava
onlava inspect docs --json
onlava system toolchain verify --json --images
go run ./cmd/onlava harness self --summary --write
```

Run the real selfhost acceptance gate:

```bash
go run ./cmd/onlava harness self --json --write
```

Acceptance criteria:

- Default non-quick self-harness passes, including the real Docker-backed Neon selfhost proof.
- `backend.json` is v2 after any write.
- Existing v1 `backend.json` migrates on read/write without losing branches.
- Two different Onlava projects with the same branch name get different tenant IDs.
- Two different Onlava projects with the same branch name get different compute container names.
- Project A branch delete/reset/restore/diff never mutates project B.
- Status JSON reports project counts and per-project tenant IDs without raw database URLs.
- Public `onlava db branch status --json` and `onlava db branch list --json` remain compatible for existing app/worktree flows.

Do not run `go install ./cmd/onlava` during agent validation unless the human
explicitly asks. Multiple worktrees share the same installed `onlava` path; use
`go build` or the source-built `go run ./cmd/onlava harness self ...` path for
this plan.

## Idempotence and Recovery

All migration logic must be safe to run repeatedly. Reading v2 should return v2
unchanged. Reading v1 should produce the same v2 shape every time until it is
written.

If a command fails after creating a tenant but before writing backend state, the
next ensure should derive the same project tenant ID and treat pageserver
tenant/timeline creation as idempotent.

If a command fails after starting compute but before writing backend state, the
next ensure may find an existing container. Because the compute container name
includes project and branch ID, it can safely inspect or replace that container
without colliding with another project.

If `backend.json` becomes corrupt, existing destructive uninstall behavior should
still be able to remove Onlava-labeled containers. Do not make uninstall depend
on successfully parsing v2 backend state.

If v1 migration encounters branches with empty project, place them under a
deterministic legacy project key and add a warning message in backend status. Do
not silently drop them.

## Artifacts and Notes

Files expected to change:

- `internal/neonselfhost/state.go`
- `internal/neonselfhost/pageserver.go`
- `internal/neonselfhost/lifecycle.go`
- `internal/neonselfhost/compute.go`
- `internal/neonselfhost/driver.go`
- `internal/neonselfhost/state_test.go`
- `internal/neonselfhost/driver_test.go`
- `cmd/onlava/db_neon.go`
- `cmd/onlava/harness_neon.go`
- `docs/schemas/onlava.db.neon.selfhost.backend.v1.schema.json` or a new v2 schema
- `docs/schemas/onlava.db.neon.status.v1.schema.json`
- `docs/local-contract.md`
- `README.md`
- `docs/knowledge.json`
- `docs/plans/active.md`

Potential new schema:

```text
docs/schemas/onlava.db.neon.selfhost.backend.v2.schema.json
```

Potential old-schema policy:

- Keep v1 schema for historical/debugging reference if already indexed.
- Use v2 for new writes.

## Interfaces and Dependencies

The built-in driver interface remains unchanged:

```bash
onlava internal neon-selfhost-driver ensure --project <project> --parent-branch <branch> --branch <branch> --branch-id <id> --database <db> --role <role> --json
onlava internal neon-selfhost-driver reset ...
onlava internal neon-selfhost-driver restore --at <lsn-or-rfc3339> ...
onlava internal neon-selfhost-driver delete ...
onlava internal neon-selfhost-driver diff --target <branch> ...
```

External drivers selected with `ONLAVA_DEV_NEON_SELFHOST_DRIVER` receive the same
branch JSON contract. The meaning of `--project` tightens from "metadata
attached to a branch" to "the Neon project/tenant boundary." This is a semantic
tightening, not a CLI grammar change.

The public Onlava files remain:

```text
<app-root>/.onlava/worktree-db.json
<agent-home>/agent/substrates/neon/branches.json
<agent-home>/agent/substrates/neon/backend.json
```

`worktree-db.json` and `branches.json` stay public Onlava state. `backend.json`
stays driver-owned implementation state.

External runtime dependencies remain:

- Docker
- `psql`
- `pg_dump` for schema diff
- Pinned Neon, compute-node, MinIO, and mc images from `onlava.toolchain.json`
- Built-in `onlava internal neon-selfhost-driver`, with optional external override through `ONLAVA_DEV_NEON_SELFHOST_DRIVER`

The user-facing invariant after this plan:

> `dev.services.postgres.project` is the Neon project. In self-hosted Neon, that
> project owns exactly one tenant, and all branches for that project live under
> that tenant.
