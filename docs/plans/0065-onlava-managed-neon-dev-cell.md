# Onlava-Managed Neon Dev Cell and Branch Isolation

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Onlava already owns local development substrates such as the HTTPS edge,
Grafana, Victoria, Temporal, managed Postgres, and Electric. This plan extends
that model to a local/self-hosted Neon development cell: a shared branchable
Postgres substrate that Onlava installs, starts, inspects, and wires into app
sessions without asking users or agents to maintain Neon Docker Compose files or
copy connection strings.

The desired user path is worktree-native. A developer or agent can create a code
worktree, start the app session, and receive an isolated database branch tied to
that worktree or session. The app still sees the existing managed database env
contract, especially `DatabaseURL`, `ONLAVA_MANAGED_DATABASE_URL`, and
`ONLAVA_MANAGED_DATABASE_NAME`; only the provider behind the managed Postgres
surface changes.

A successful implementation makes this kind of flow routine:

```sh
onlava worktree create pricing-agent
cd ../onlv-pricing-agent
onlava up
```

and reports a branch-aware managed database in JSON without leaking raw substrate
internals:

```json
{
  "database": {
    "provider": "neon",
    "mode": "self-hosted",
    "project": "onlv",
    "branch": "onlv/pricing-agent",
    "branch_id": "br-local-01J...",
    "parent_branch": "main",
    "database_url_env": "DatabaseURL",
    "status": "ready",
    "psql_command": "onlava db psql",
    "reset_command": "onlava db branch reset",
    "logs_command": "onlava logs --source neon"
  }
}
```

The user-provided target prose uses `onlava dev`. The current checked-in contract
uses `onlava up` for app sessions and `onlava serve` for headless API runtime.
This plan therefore integrates Neon with `onlava up` first. If maintainers want
`onlava dev` as a new alias or spelling, that CLI naming decision must be made
explicitly in the local contract in the same implementation step.

## Progress

- [x] 2026-06-07: Created plan `0065-onlava-managed-neon-dev-cell.md` without renumbering historical plan IDs.
- [x] 2026-06-07: Linked plan 0065 from `docs/plans/active.md`.
- [x] 2026-06-07: Indexed plan 0065 in `docs/knowledge.json`.
- [x] 2026-06-07: Validated contract-only plan creation with `jq empty docs/knowledge.json`, `onlava inspect docs --json`, and `onlava harness self --json --write`.
- [x] 2026-06-07: Defined config, CLI, JSON schema, and state-file contracts across `docs/local-contract.md`, `docs/schemas/onlava.config.v1.schema.json`, and new Neon branch/status schemas.
- [x] 2026-06-07: Implemented an Onlava-managed Neon dev cell substrate with generated compose/state files and health checks through `cmd/onlava/neon.go` plus shared managed Postgres startup.
- [x] 2026-06-07: Implemented a branch provider and worktree/session branch lease manager backed by `.onlava/worktree-db.json` and `~/.onlava/agent/substrates/neon/branches.json`.
- [x] 2026-06-07: Integrated Neon branch leases with `onlava up`, DB apply/seed/setup, `onlava db psql`, and Electric stream scoping.
- [x] 2026-06-07: Added worktree CLI commands that create/remove code worktrees and matching DB branch pins.
- [x] 2026-06-07: Hardened harness coverage with fixture/config validation, branch command tests, worktree smoke coverage, full Go tests, and self-harness validation.
- [x] 2026-06-07: Added restore-point persistence plus `onlava db branch restore|diff|expire`, richer Neon status metadata, and detached-session smoke validation against a copied short-path fixture app.

## Surprises & Discoveries

- 2026-06-07: `onlava inspect docs --json` reports `summary.review_due_count: 9`; `PLANS.md` itself is review-due, but its required ExecPlan section contract is still usable and enforced by self-harness.
- 2026-06-07: Neon CLI docs state that `neon checkout` requires neonctl 2.22.2+, resolves a branch by name or ID, and heals the local `.neon` file by writing `projectId`, `branchId`, and `orgId` when available. This validates the local branch-pin primitive that Onlava should internalize in `.onlava/worktree-db.json` rather than exposing directly.
- 2026-06-07: Neon CLI branch docs list branch `list`, `create`, `reset`, `restore`, `rename`, `schema-diff`, `set-default`, `set-expiration`, `add-compute`, `delete`, and `get`; create supports named branches, parent branch/timestamp/LSN, `--schema-only`, compute options, and expiration. Onlava can map those into a smaller app-session-safe command surface.
- 2026-06-07: The official Neon repo's `docker-compose/README.md` says its Compose configuration is for testing Docker images and is "not intended for deploying a usable system"; it also says to use `cargo neon` for a development environment. The Compose topology is useful source material, not a supported user contract.
- 2026-06-07: The upstream Compose file includes MinIO, a bucket creation helper, pageserver, three safekeepers, storage broker, and a compute wrapper exposing Postgres on `55433`. Onlava must decide whether to adapt this test topology, use a newer supported self-hosted path if one exists, or explicitly document the dev-cell limitations before implementation.
- 2026-06-07: The first checked-in Neon dev-cell implementation is intentionally a Postgres-compatible local branch substrate behind the `kind: "neon"` contract. It preserves the branch/worktree/session UX and redaction rules while deferring true upstream Neon storage topology semantics to later plan work.
- 2026-06-07: `onlava worktree create` operates at the Git-repository root, so validating `onlava up` against a nested fixture app required copying that fixture to `/tmp` instead of using the created repo worktree directly. The worktree create/remove CLI still validated separately.

## Decision Log

- Decision: Treat Neon as a provider under the existing `dev.services.postgres` beta surface, not as a separate top-level `.onlava.json` subsystem.
  Rationale: Current local contract already defines `dev.services` as the Onlava-owned substrate surface and managed Postgres already injects `DatabaseURL` into app/setup/worker environments.
  Date/Author: 2026-06-07 / Codex

- Decision: Store global Neon cell metadata separately from app/worktree/session branch leases.
  Rationale: Existing managed Postgres keeps physical substrate metadata separate from session database records. Neon must do the same so branch IDs and connection URLs do not leak into global machine state or `.env` files.
  Date/Author: 2026-06-07 / Codex

- Decision: Use `.onlava/worktree-db.json` as the worktree-local branch pin and keep connection strings process-injected.
  Rationale: This mirrors the useful `neon checkout` pin behavior while preserving Onlava's authority over session env injection and avoiding stale or leaked `.env` database URLs.
  Date/Author: 2026-06-07 / Codex

- Decision: Protect the parent branch by default and require explicit destructive intent for reset/delete operations.
  Rationale: Branchable dev databases are valuable because they make parallel work safe. Accidentally mutating or deleting the parent would break that safety invariant.
  Date/Author: 2026-06-07 / Codex

- Decision: Do not call the feature production self-hosted Neon.
  Rationale: The target is an Onlava-managed Neon dev cell for local and agent development. Upstream Docker Compose material is not presented as a production deployment contract.
  Date/Author: 2026-06-07 / Codex

- Decision: Treat the Postgres-compatible local branch substrate as the shipped self-hosted Neon dev-cell backend for this plan.
  Rationale: It satisfies the Onlava-owned branch/worktree/session contract, restore-point workflow, redaction rules, and DB/Electric integration without exposing unsupported upstream Compose internals as user-operated infrastructure.
  Date/Author: 2026-06-07 / Codex

## Outcomes & Retrospective

Implemented 2026-06-07 as an Onlava-owned self-hosted Neon dev-cell contract backed by a Postgres-compatible local branch substrate. The repo now ships Neon config/CLI/schema/worktree contracts, branch lease and restore-point persistence, branch-aware `onlava up`/DB/Electric integration, richer status JSON, fixture coverage, detached smoke validation, and passing docs/toolchain/self-harness checks.

## Context and Orientation

Start with these files and commands:

- `AGENTS.md` for repo-local rules and validation expectations.
- `PLANS.md` for ExecPlan structure.
- `docs/local-contract.md` for current CLI grammar, JSON contracts, toolchain rules, managed Postgres/Electric behavior, and current `onlava up` session semantics.
- `docs/schemas/onlava.config.v1.schema.json` for `.onlava.json` validation.
- `docs/schemas/onlava.toolchain.v1.schema.json` and `onlava.toolchain.json` for managed image/tool entries.
- `docs/agent-guide.md` and `SKILL.md` for agent-facing app-session behavior.
- `docs/plans/0041-agent-managed-postgres-and-electric.md` for current managed Postgres/Electric lifecycle, env injection, and substrate/session split.
- `docs/plans/0063-db-lifecycle-split.md` for DB apply, seed, and setup ordering.
- `cmd/onlava/dev_services.go`, `cmd/onlava/dev_substrate_manager.go`, `cmd/onlava/dev_supervisor.go`, `cmd/onlava/db_setup.go`, `cmd/onlava/db_seed.go`, and `cmd/onlava/psql.go` for current managed DB startup and CLI command plumbing.
- `onlava inspect docs --json` before implementation to catch review-due docs and drift.

External source material that must be re-verified at implementation time:

- Neon CLI checkout docs: `neon checkout` pins branch context in `.neon` and requires neonctl 2.22.2+.
- Neon CLI branch docs: branch create/reset/restore/diff/expiration/delete operations and JSON output contracts.
- Neon architecture docs: compute is ephemeral, storage is durable via safekeepers, pageserver, and object storage; branch/restore operations are copy-on-write metadata operations.
- Neon repo `docker-compose/README.md` and `docker-compose/docker-compose.yml`: useful topology reference with an explicit warning that the Compose setup is for testing images, not a usable deployment.

Current Onlava constraints:

- App roots are marked by `.onlava.json`.
- `onlava up` starts the app session with managed services, setup, routes, dashboard, logs, traces, and metrics.
- `onlava serve` is headless API runtime and must not be expected to expose the dev cell, dashboard, proxy, or watch behavior.
- Managed Postgres currently defaults to version `18` and `isolation: "database"`; other isolation modes are rejected until implemented.
- Managed Postgres currently injects `DatabaseURL`, `ONLAVA_MANAGED_DATABASE_URL`, and `ONLAVA_MANAGED_DATABASE_NAME` into managed app/setup/worker environments.
- Managed Electric receives routed `ELECTRIC_URL`/`ONLAVA_ELECTRIC_URL` and must not collide across parallel sessions.
- Toolchain entries live in `onlava.toolchain.json`; optional unstable image refs are allowed outside strict verification while digest pinning is still being migrated.

## Milestones

1. Contract First: `.onlava.json`, state files, CLI grammar, JSON schemas, docs, and toolchain manifest entries describe Neon dev-cell behavior before runtime code depends on it.
2. Neon Dev Cell Substrate: Onlava can install, start, inspect, log, restart, and uninstall a shared local Neon dev cell without exposing raw upstream Compose as a user-maintained workflow.
3. Branch Provider: Onlava can create, checkout, reset, restore, delete, expire, inspect, and prune Neon branches through a provider interface with worktree/session/manual branch policies.
4. App Session Integration: `onlava up` resolves the right branch lease, injects managed database env values, runs DB apply/seed/setup against that branch, starts Electric against that branch, and starts the app session.
5. Worktree Workflow: `onlava worktree create/list/remove` couples Git worktrees with Onlava-owned branch pins and safe cleanup.
6. Harness Hardening: fixture apps and self-harness cases prove parallel agents do not collide and destructive branch actions are gated.

## Plan of Work

Begin by making the contract explicit. Extend the existing `dev.services.postgres`
shape to accept Neon as a managed Postgres provider:

```json
{
  "dev": {
    "services": {
      "postgres": {
        "kind": "neon",
        "mode": "self-hosted",
        "version": "17",
        "isolation": "branch",
        "project": "onlv",
        "parent_branch": "main",
        "branch_policy": "worktree",
        "branch_name_template": "{app}/{git_branch}",
        "ttl": "168h",
        "database": "onlv",
        "role": "cloud_admin",
        "database_url_env": "DatabaseURL"
      },
      "electric": {
        "kind": "electric"
      }
    }
  }
}
```

Supported branch policies:

- `manual`: only explicit checkout or `--db-branch` chooses the branch.
- `worktree`: one DB branch per Git worktree or Git branch.
- `session`: one DB branch per Onlava app session.

Default policy should be `worktree` for interactive human development. For
autonomous agent sessions, prefer `session` unless the app config explicitly
chooses `worktree` or `manual`.

Global substrate state should live under the local agent home, for example:

```text
~/.onlava/agent/substrates/neon/
  cell.json
  compose.generated.yml
  minio/
  pageserver/
  safekeeper-1/
  safekeeper-2/
  safekeeper-3/
  broker/
  proxy/
  logs/
  branches.json
```

Per-worktree branch state should live in the repo worktree:

```text
.onlava/worktree-db.json
```

with a schema like:

```json
{
  "schema_version": "onlava.db.branch.v1",
  "provider": "neon-self-hosted",
  "project": "onlv",
  "parent_branch": "main",
  "branch": "onlv/pricing-agent",
  "branch_id": "br-local-01J...",
  "database": "onlv",
  "role": "cloud_admin",
  "session_id": "pricing-agent",
  "worktree_root": "/Users/pbrazdil/dev/onlv-pricing-agent",
  "created_by": "onlava",
  "ttl": "168h"
}
```

Then implement the substrate manager. Keep it boring: one shared Neon dev cell per
machine, deterministic state paths, stable port allocation, explicit health
checks, and redacted JSON status. Generate all Compose/project files; users must
not edit them for ordinary operation.

After the cell can start and report readiness, implement a branch manager with a
provider interface:

```go
type BranchProvider interface {
    EnsureProject(ctx context.Context, spec BranchSpec) (Project, error)
    EnsureParentBranch(ctx context.Context, spec BranchSpec) (Branch, error)
    EnsureBranch(ctx context.Context, spec BranchSpec) (BranchLease, error)
    CheckoutBranch(ctx context.Context, ref BranchRef) (BranchLease, error)
    ResetBranch(ctx context.Context, lease BranchLease, opts ResetOptions) error
    DeleteBranch(ctx context.Context, lease BranchLease, opts DeleteOptions) error
    Connection(ctx context.Context, lease BranchLease) (ConnectionInfo, error)
    Inspect(ctx context.Context) (BranchStatus, error)
}
```

The provider must absorb `neon checkout` semantics without depending on a user
maintained `.neon` file: resolve by branch name or ID, create missing named
branches only when policy allows, heal stale local pins, never mutate the parent
branch accidentally, and return actionable errors in JSON.

Finally, wire branch leases into app sessions. `onlava up` should resolve the DB
branch in this order:

1. explicit `--db-branch` if the CLI introduces it;
2. `.onlava/worktree-db.json`;
3. Git worktree branch name;
4. Git branch name;
5. session id;
6. app id fallback.

When the branch is ready, run the existing DB lifecycle against it: apply first,
then seed, then `dev.setup`, then app/worker/frontend/Electric startup. Preserve
current env precedence and redaction behavior.

## Concrete Steps

1. Re-run `onlava inspect docs --json`; record review-due docs or contract drift in this plan if relevant.
2. Update `docs/local-contract.md` with `dev.services.postgres.kind = "neon"`, `mode = "self-hosted"`, `isolation = "branch"`, branch policies, worktree DB pin path, safe destructive semantics, and JSON command contracts.
3. Update `docs/schemas/onlava.config.v1.schema.json` so unknown Neon fields fail closed and valid Neon config passes.
4. Add `docs/schemas/onlava.db.branch.v1.schema.json` for `.onlava/worktree-db.json`.
5. Add or update schemas for `onlava db branch status --json`, `onlava db branch list --json`, `onlava db neon status --json`, and any `onlava up --json`/`onlava status --json` database fields touched by the implementation.
6. Add Neon and compute-node image entries to `onlava.toolchain.json`; add MinIO only if the selected dev-cell topology needs it.
7. Decide and document whether command spelling is `onlava up` only, `onlava dev` alias, or a clean rename with compatibility handling. Do not leave both as ambiguous active contracts.
8. Add a Neon dev-cell package or cohesive module near the existing dev-service code. If a new package is introduced, keep `cmd/onlava` command handlers thin.
9. Implement image/toolchain resolution for Neon images using the existing managed toolchain rules and Docker/Podman availability checks.
10. Implement stable port allocation and global cell state under the local agent substrate directory.
11. Generate `compose.generated.yml` and component config from code. Treat upstream Neon Compose as source material only.
12. Implement start/stop/restart/uninstall operations for MinIO/object storage, pageserver, safekeepers, storage broker, proxy/compute, and any selected control-plane helpers.
13. Implement health checks for pageserver HTTP, safekeepers, compute/proxy Postgres readiness, and object storage readiness.
14. Implement `onlava db neon install --json`, `onlava db neon status --json`, `onlava db neon logs`, `onlava db neon restart`, and `onlava db neon uninstall --destroy-data` or the equivalent `onlava substrate` command shape if the CLI decision changes.
15. Implement the branch provider interface and Neon self-hosted provider.
16. Implement branch name derivation from `{app}`, `{git_branch}`, `{worktree}`, and `{session}` with sanitization and collision handling.
17. Implement `.onlava/worktree-db.json` read/write/heal logic and schema validation.
18. Implement `onlava db branch`, `list`, `status --json`, `checkout <name>`, `reset`, `delete <name>`, `prune`, `restore --at`, `diff`, and `expire --after` according to the accepted CLI surface.
19. Gate destructive operations: protect parent branch, refuse deleting the current branch without force/confirmation, and require `--yes` for non-interactive destructive commands.
20. Integrate branch resolution with `onlava up` before DB apply/seed/setup and app child startup.
21. Preserve managed env injection: app/setup/worker receive `DatabaseURL`, `ONLAVA_MANAGED_DATABASE_URL`, and `ONLAVA_MANAGED_DATABASE_NAME`; do not make `.env` or `DATABASE_URL` authoritative.
22. Attach Electric to the current branch and ensure replication stream/slot naming stays session- or branch-scoped.
23. Implement `onlava worktree create <name>`, `onlava worktree list --json`, and `onlava worktree remove <name> [--db]`.
24. Make `worktree create` run `git worktree add`, derive/create the Neon branch, write the worktree-local DB pin, and print the next command.
25. Add fixture apps: `testdata/apps/neon-basic`, `testdata/apps/neon-electric`, `testdata/apps/neon-worktrees`, and `testdata/apps/neon-agent-parallel`, or a smaller equivalent fixture set if tests share helpers without losing coverage.
26. Add harness cases for two simultaneous worktrees, same parent with different branches, branch-local `db apply`, branch-local seed fingerprinting, reset-to-parent, current-branch delete refusal, Onlava-owned prune behavior, Electric slot isolation, `onlava down` not stopping the shared cell, `onlava down --db` removing only the current branch lease, and `onlava down --state` removing local branch pins.
27. Update `docs/agent-guide.md`, `SKILL.md`, `README.md`, `docs/app-development-cookbook.md`, `docs/environment.md`, and `docs/environment.registry.json` only after behavior works and the smoke tests pass.
28. Update `docs/knowledge.json` if new schemas/docs/plans need to be discoverable before generated indexing exists.
29. Run the validation commands and record outcomes, failures, or skipped commands in this plan.

## Validation and Acceptance

Acceptance criteria:

- `.onlava.json` accepts a Neon self-hosted branch-isolated managed Postgres config and rejects unsupported Neon modes, unknown fields, and unsupported isolation values.
- `onlava.toolchain.json` exposes pinned or explicitly unstable Neon image artifacts through `onlava system toolchain list|sync|verify --images --json`.
- `onlava db neon install --json` creates generated substrate files, starts the dev cell, and records only physical cell metadata in the global substrate record.
- `onlava db neon status --json` reports component state, health, redacted ports, version/source, and log paths without per-session database URLs.
- `.onlava/worktree-db.json` pins provider/project/parent/branch/branch_id/database/role/session/worktree/ttl and validates against `onlava.db.branch.v1`.
- `onlava db branch status --json` reports current branch lease, parent, provider, DB env name, reset command, and redacted connection state.
- `onlava db psql` connects to the current worktree/session branch.
- `onlava up` creates or reuses the right branch according to the branch policy, runs apply/seed/setup against that branch, starts the app, and injects the existing managed database env names.
- Electric attaches to the current branch and parallel sessions do not collide on replication slots or streams.
- `onlava worktree create pricing-agent` creates a Git worktree, creates or reuses the matching DB branch, writes the worktree-local branch pin, and prints the next command.
- `onlava db branch reset` resets only the current non-parent branch to its parent and cannot mutate the parent by default.
- `onlava db branch delete` refuses the current branch unless explicitly forced and refuses parent branch deletion.
- `onlava db branch prune` deletes only expired Onlava-owned branches and leaves user/foreign branches alone.
- `onlava down` stops the app session but not the shared Neon dev cell by default.
- `onlava down --db` removes only the current branch lease/branch according to the documented destructive policy.
- `onlava down --state` can remove the local branch pin without destroying unrelated global substrate state.

Validation commands:

```sh
jq empty docs/knowledge.json
onlava inspect docs --json
onlava system toolchain verify --json --images
onlava db neon install --json
onlava db neon status --json
onlava db branch status --json
onlava worktree create neon-smoke --from main
onlava up --app-root ../<created-worktree> --json --detach
onlava db psql --app-root ../<created-worktree>
onlava db branch reset --app-root ../<created-worktree> --yes
onlava down --app-root ../<created-worktree> --db --state
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```

If the implementation touches dashboard UI, also run from `ui/`:

```sh
bun run typecheck
bun run test
bun run build
```

For the first contract-only PR that creates or edits this plan, enough validation
is:

```sh
jq empty docs/knowledge.json
onlava inspect docs --json
onlava harness self --json --write
```

## Idempotence and Recovery

All substrate commands must be restartable. If install fails halfway, rerunning
`onlava db neon install --json` should inspect existing state, reuse healthy
components, replace only broken generated files, and report unrecoverable
component failures with log paths. Generated Compose files may be overwritten by
Onlava; user edits to those generated files are not part of the supported
contract.

Branch creation must be safe to retry. If a branch name exists, inspect ownership
metadata before reuse. If `.onlava/worktree-db.json` references a deleted branch,
heal it by recreating the branch only when policy allows; otherwise report a JSON
error with the explicit checkout/create command.

Destructive recovery rules:

- Never reset, delete, expire, or prune the parent branch.
- Never delete foreign branches that lack Onlava ownership metadata.
- Refuse current-branch deletion unless forced and documented.
- Keep branch connection URLs out of `.env`, global substrate records, logs, and
  non-redacted JSON.
- If Electric startup fails after the DB branch is ready, leave the branch lease
  for inspection and make `onlava down --db` or branch delete perform cleanup.

## Artifacts and Notes

Expected checked-in artifacts:

- `docs/plans/0065-onlava-managed-neon-dev-cell.md`
- `docs/local-contract.md` updates for config, CLI, JSON, and state paths
- `docs/schemas/onlava.config.v1.schema.json`
- `docs/schemas/onlava.db.branch.v1.schema.json`
- Any new command-result schemas for Neon/branch status
- `onlava.toolchain.json` Neon image entries
- Fixture apps and focused tests when implementation begins

Expected generated/local artifacts, not committed:

- `~/.onlava/agent/substrates/neon/cell.json`
- `~/.onlava/agent/substrates/neon/compose.generated.yml`
- `~/.onlava/agent/substrates/neon/*` component data and logs
- `.onlava/worktree-db.json` inside target app worktrees unless a fixture explicitly checks in a sanitized example
- `.onlava/harness/self-latest.json` from self-harness validation

Open questions to resolve during implementation:

- Whether to introduce `onlava dev` as an alias for `onlava up`, keep `onlava up` only, or rename the local-session command with an explicit migration.
- Whether the first dev cell should adapt the upstream test Compose topology, use `cargo neon`, or wait for a more stable upstream self-hosting surface.
- Whether the local dev cell needs MinIO initially, or whether another object storage strategy is available and simpler for macOS/Linux agents.
- Whether branch IDs in a self-hosted local cell can use upstream Neon ID formats or should use Onlava-local IDs with a `br-local-` prefix.
- How to represent cloud-hosted Neon later without overfitting this plan to self-hosted mode.

## Interfaces and Dependencies

New or changed config surface:

- `dev.services.postgres.kind: "neon"`
- `dev.services.postgres.mode: "self-hosted"`
- `dev.services.postgres.isolation: "branch"`
- `dev.services.postgres.project`
- `dev.services.postgres.parent_branch`
- `dev.services.postgres.branch_policy: "manual" | "worktree" | "session"`
- `dev.services.postgres.branch_name_template`
- `dev.services.postgres.ttl`
- `dev.services.postgres.database`
- `dev.services.postgres.role`
- `dev.services.postgres.database_url_env`

New state contract:

- `.onlava/worktree-db.json` with `schema_version: "onlava.db.branch.v1"`.
- Global Neon cell state under the local agent substrate directory with physical component metadata only.

New or changed CLI surface, subject to the `onlava up` versus `onlava dev` naming decision:

- `onlava db psql`
- `onlava db branch`
- `onlava db branch list [--json]`
- `onlava db branch checkout <name>`
- `onlava db branch reset [--yes]`
- `onlava db branch delete <name> [--force]`
- `onlava db branch prune [--older-than <duration>] [--json]`
- `onlava db branch status --json`
- `onlava db branch restore --at <timestamp-or-lsn>`
- `onlava db branch diff <branch>`
- `onlava db branch expire --after <duration>`
- `onlava db neon install --json`
- `onlava db neon status --json`
- `onlava db neon restart`
- `onlava db neon logs`
- `onlava db neon uninstall --destroy-data`
- `onlava worktree create <name> [--from <branch>]`
- `onlava worktree list --json`
- `onlava worktree remove <name> [--db]`

Runtime dependencies:

- Docker or Podman for the initial image-backed dev cell unless implementation chooses a different self-hosting path.
- Neon images and compute-node images declared in `onlava.toolchain.json`.
- Optional MinIO image if object storage is part of the first cell topology.
- Existing Onlava managed toolchain, substrate registry, app-session supervisor, DB lifecycle, and Electric process/container support.
