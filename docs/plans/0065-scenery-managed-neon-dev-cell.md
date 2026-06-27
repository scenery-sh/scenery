# Scenery-Managed Neon Dev Cell and Branch Isolation

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Scenery already owns local development substrates such as the HTTPS edge,
Grafana, Victoria, legacy async runtime, managed Postgres, and sync. This plan extends
that model to a local/self-hosted Neon development cell: a shared branchable
Postgres substrate that Scenery installs, starts, inspects, and wires into app
sessions without asking users or agents to maintain Neon Docker Compose files or
copy connection strings.

The desired user path is worktree-native. A developer or agent can create a code
worktree, start the app session, and receive an isolated database branch tied to
that worktree or session. The app still sees the existing managed database env
contract, especially `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, and
`SCENERY_MANAGED_DATABASE_NAME`; only the provider behind the managed Postgres
surface changes.

A successful implementation makes this kind of flow routine:

```sh
scenery worktree create pricing-agent
cd ../onlv-pricing-agent
scenery up
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
    "psql_command": "scenery db psql",
    "reset_command": "scenery db branch reset",
    "logs_command": "scenery logs --source neon"
  }
}
```

The user-provided target prose uses `scenery dev`. The current checked-in contract
uses `scenery up` for app sessions and `scenery serve` for headless API runtime.
This plan therefore integrates Neon with `scenery up` first. If maintainers want
`scenery dev` as a new alias or spelling, that CLI naming decision must be made
explicitly in the local contract in the same implementation step.

## Progress

- [x] 2026-06-07: Created plan `0065-scenery-managed-neon-dev-cell.md` without renumbering historical plan IDs.
- [x] 2026-06-07: Linked plan 0065 from `docs/plans/active.md`.
- [x] 2026-06-07: Indexed plan 0065 in `docs/knowledge.json`.
- [x] 2026-06-07: Validated contract-only plan creation with `jq empty docs/knowledge.json`, `scenery inspect docs --json`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Defined the first config, CLI, JSON schema, image manifest, and state-file contracts. `dev.services.postgres.kind: "neon"` now decodes with self-hosted branch fields; `scenery db neon install|status|logs|restart|uninstall` and `scenery db branch status|list` were registered in the first slice; schemas cover `.scenery/worktree-db.json`, Neon status, and branch status/list; optional unstable Neon/compute/MinIO image refs are in `scenery.toolchain.json`.
- [x] 2026-06-08: Validated the first implementation slice with `jq empty`, focused Go tests, `go test ./...`, `go install ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, temporary `scenery db neon install/status --json`, temporary `scenery db branch status/list --json`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Implemented local branch-pin checkout and destructive-command guards. `scenery db branch checkout <name> --json` writes `<app-root>/.scenery/worktree-db.json` with sanitized branch names, stable `br-local-*` IDs, and ignored local state; `reset --yes` and `delete <name> --force` now validate parent/current branch safety before returning the not-yet-implemented backend mutation error.
- [x] 2026-06-08: Validated the branch-pin slice with `jq empty`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, installed-binary branch checkout/status/reset smoke, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Implemented app-session branch-pin resolution for Neon configs. `scenery up` now reuses an existing `.scenery/worktree-db.json` pin or derives one from `branch_policy` and `branch_name_template`, writes it, and initially failed explicitly on the missing Neon backend connection. That failure mode was superseded by the later ready-lease endpoint consumption slice below.
- [x] 2026-06-08: Implemented the first Neon dev-cell substrate status slice. `scenery db neon install --json` writes generated `cell.json` and labeled `compose.generated.yml`; `status --json` now checks generated files, Docker availability, optional local image presence, and labeled component/container state without starting the cell or claiming branch-backed connectivity.
- [x] 2026-06-08: Validated the dev-cell status slice with `jq empty`, focused Neon CLI tests including a fake-Docker probe, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added the first branch-provider boundary. `scenery db branch status --json` now reports `backend_status` separately from local pin status, with Scenery-created local pins marked `pending`; checkout/reset/delete route through a provider interface so future backend mutations do not blur local lease files with real Neon branches.
- [x] 2026-06-08: Validated the branch-provider boundary with `jq empty`, focused branch/provider tests, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test -count=1 ./cmd/scenery`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Implement a branch provider and worktree/session branch lease manager.
- [x] 2026-06-08: Integrate Neon branch leases with `scenery up`, DB apply/seed/setup, `scenery db psql`, and sync.
- [x] 2026-06-08: Added `scenery worktree create|list|remove` for the first worktree workflow slice. `create` runs `git worktree add -b`, writes the target worktree's local Neon branch pin when the app declares Neon, and emits `scenery.worktree.create.v1`; `list` emits `scenery.worktree.list.v1`; `remove --db` removes the local branch pin before `git worktree remove` and emits `scenery.worktree.remove.v1`. Backend Neon branch creation/deletion remains pending.
- [x] 2026-06-08: Validated the app-session and worktree slices with `jq empty`, `go test ./cmd/scenery -run 'TestWorktree|TestParseWorktree'`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Implemented the local Neon branch lease registry slice. `branches.json` under the agent Neon substrate records Scenery-owned pending leases; checkout/session/worktree pin writes upsert it; existing pins heal into it; `db branch list` reads it; `expire --after` and `prune --older-than` update/prune local metadata only. Backend branch mutation remains pending.
- [x] 2026-06-08: Validated the local lease registry slice with `jq empty`, focused Neon/branch/worktree tests, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Extended the self-hosted branch provider inspection path to read the local lease registry. `scenery db branch status --json` now distinguishes checkout-created pending leases, hand-written or orphaned missing leases, and expired leases without mutating backend Neon branches.
- [x] 2026-06-08: Validated the provider-inspection slice with `jq empty`, focused branch status tests, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Wired local Neon lease cleanup into `scenery down`. For Neon configs, `down --db` removes only the current non-parent local branch lease from `branches.json`, and `down --state` removes only the app root's `.scenery/worktree-db.json` pin in addition to ordinary session state cleanup.
- [x] 2026-06-08: Validated the local down-cleanup slice with `jq empty`, focused cleanup tests, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added a self-harness local Neon lifecycle step. It creates two Neon-enabled Git worktrees in a temp repo, checks distinct local branch pins and leases, exercises expire/prune, verifies local `down --db` and `down --state` cleanup helpers, and asserts reset/delete destructive guards. This covers local branch state safety only; real backend DB lifecycle and sync slot isolation still require the branch provider.
- [x] 2026-06-08: Validated the local Neon harness slice with focused harness/branch/worktree tests, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Implemented ready-lease endpoint consumption without claiming branch creation. Self-hosted branch inspection now treats a local lease as ready only when endpoint metadata exists, `scenery db branch status --json` emits that redacted endpoint as `connection`, and `scenery up`, DB setup, `scenery db psql`, and sync synthesize process-local `DatabaseURL` values from the ready endpoint while failing explicitly for pending, missing, expired, or endpoint-less leases.
- [x] 2026-06-08: Validated the ready-lease endpoint consumption slice with `jq empty`, focused ready-lease tests, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added the `scenery db branch restore --at <timestamp-or-lsn> --yes` and `scenery db branch diff <branch>` command guards. They now validate required arguments, local pin presence, destructive restore confirmation, and parent-branch protection before returning explicit provider-not-implemented errors.
- [x] 2026-06-08: Implemented bounded `scenery db neon restart`. It restarts existing Scenery-owned Neon containers visible to Docker, updates `cell.json`, and reports post-restart status, while still failing explicitly when no generated containers exist instead of trying to start the dev cell.
- [x] 2026-06-08: Validated the restore/diff/restart slice with focused Neon CLI tests, `jq empty`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added stable reserved Neon dev-cell port metadata to `cell.json`, `scenery db neon status --json`, the generated Compose file, and `scenery.db.neon.status.v1`. These are redacted loopback debug ports only; they do not imply that the dev cell is running.
- [x] 2026-06-08: Validated the reserved-port metadata slice with focused Neon status tests, `jq empty`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Hardened Neon dev-cell status readiness. Running components now get loopback listener checks for their reserved ports, Docker-unhealthy components are degraded, and closed required listeners prevent the generated dev-cell status from reporting `ready`.
- [x] 2026-06-08: Validated the dev-cell listener readiness slice with focused Neon status tests, `jq empty`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Made `scenery db branch list --json` lease-aware. It still includes the existing `branches` pin list, and now also emits `leases` with status, timestamps, and optional redacted endpoint metadata so agents can inspect ready/pending/expired local leases without reading `branches.json` directly.
- [x] 2026-06-08: Validated the lease-aware branch-list slice with focused branch list/status/prune tests, `jq empty`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added explicit generated dev-cell lifecycle commands. `scenery db neon start --json` runs the generated Docker Compose project with `docker compose -f <compose.generated.yml> -p scenery-neon up -d` and then reports probed status; `scenery db neon stop --json` stops the same project without removing local state or branch leases.
- [x] 2026-06-08: Validated the generated dev-cell start/stop slice with focused Neon lifecycle tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Hardened self-harness coverage for the generated Neon dev-cell lifecycle. The `neon local branch lifecycle` step now uses a deterministic fake Docker command to verify that `install`, `start`, and `stop` exercise the generated Compose project with the stable `scenery-neon` project name before continuing through worktree pin and local lease checks.
- [x] 2026-06-08: Validated the self-harness lifecycle coverage slice with focused harness tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added the provider ensure boundary for branch resolution. `scenery db branch checkout` and `scenery up` session pin resolution now call `neonBranchProvider.EnsureBranch`; the current self-hosted provider uses that boundary to renew the local lease and report pending/ready registry status without claiming real Neon branch creation.
- [x] 2026-06-08: Validated the provider ensure boundary with focused branch/session tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Implemented local pending-lease deletion for the self-hosted provider. `scenery db branch delete <name>` now removes matching non-ready Scenery-owned local leases after parent/current branch guards, removes the current worktree pin only for forced current-branch deletion, and still refuses ready leases with the backend-not-implemented error.
- [x] 2026-06-08: Validated pending-lease deletion with focused delete/guard tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added branch-provider prerequisite status for the generated dev-cell. Pending self-hosted leases now report a missing dev-cell as `backend_status: "missing"` and installed-but-not-ready dev-cell state as `backend_status: "pending"` with an explicit status message, instead of flattening every local lease to a generic pending backend.
- [x] 2026-06-08: Validated branch-provider prerequisite status with focused checkout/session/status tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Added generated dev-cell prerequisite preflight for self-hosted branch mutations. `reset`, `restore`, and `diff` now report a missing local Neon dev-cell before returning the generic backend mutation/diff placeholder, while earlier parent/current/destructive guards still win first.
- [x] 2026-06-08: Validated the mutation prerequisite slice with focused reset/restore/diff tests, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Protected ready parent-branch leases from runtime consumption. Branch status now reports `backend_status: "protected"` without connection metadata for ready parent leases, and the shared connection resolver refuses to synthesize app-session, `db psql`, DB setup, or sync `DatabaseURL` values for the parent branch.
- [x] 2026-06-08: Validated the protected-parent slice with focused branch-status and database URL resolver tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Made `scenery db branch list --json` provider-normalize lease status before serialization, so protected parent leases report `status: "protected"` and suppress endpoint metadata just like branch status output.
- [x] 2026-06-08: Validated the provider-normalized branch-list slice with focused branch-list/status/expire/prune tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`. One self-harness run hit transient Go-test failures in `TestSceneryDevDashboardNotificationsAndRoutes` and `TestDBNeonStatusProbesDockerHealth`; direct reruns and a serial self-harness rerun passed.
- [x] 2026-06-08: Hardened local lease ownership before backend mutation lands. Provider inspection/listing now ignores foreign local leases, checkout refuses to reuse a matching foreign project/branch lease before writing `.scenery/worktree-db.json`, and expire/prune/delete/down cleanup paths touch only Scenery-owned leases.
- [x] 2026-06-08: Validated the local lease ownership slice with focused foreign-lease checkout/list/prune tests, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go test -count=1 -json ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install -x ./cmd/scenery`, `jq empty`, `scenery inspect docs --json`, `scenery system toolchain verify --json --images`, `git diff --check`, and `scenery harness self --json --write`. Two earlier self-harness runs hit the same transient Go-test failures in `TestSceneryDevDashboardNotificationsAndRoutes` and `TestDBNeonStatusProbesDockerHealth`; direct focused reruns, the exact `go test -count=1 -json ./...` command, and the final self-harness rerun passed.
- [x] 2026-06-08: Added a local-postgres-branch development fallback driver boundary. When `SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` points at an absolute executable, checkout/session ensure invokes the driver with branch identity arguments and records a ready lease from returned endpoint JSON; ready reset, restore, and delete delegate to the same driver. Without the driver, the provider keeps the existing pending/local-only behavior and explicit mutation placeholders.
- [x] 2026-06-08: Validated the local-postgres-branch driver boundary with focused fake-driver branch tests, `jq empty`, `scenery inspect docs --json`, `git diff --check`, `go test -count=1 ./cmd/scenery`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install ./cmd/scenery`, `scenery system toolchain verify --json --images`, and `scenery harness self --json --write`. An initial self-harness run failed because `cmd/scenery/db_neon.go` exceeded the hard line-count limit after the driver slice; the driver adapter was split into `cmd/scenery/db_neon_driver.go`, the unused pending provider shim was removed, and the rerun passed.
- [x] 2026-06-08: Hardened self-harness coverage for the local-postgres-branch driver path. The Neon lifecycle step now installs a fake `SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER`, verifies ready `backend_status` and redacted endpoint metadata, checks managed Neon `DatabaseURL` env injection, checks sync resolves the same ready branch URL, and verifies ready branch deletion delegates to the driver.
- [x] 2026-06-08: Validated the harness fake-driver coverage with focused Neon harness tests, `git diff --check`, `jq empty`, `scenery inspect docs --json`, `go test ./cmd/scenery ./internal/app ./internal/toolchain`, `go test ./...`, `go build -o <tmp>/scenery ./cmd/scenery`, `go install ./cmd/scenery`, `scenery system toolchain verify --json --images`, and `scenery harness self --json --write`.
- [x] 2026-06-08: Ported the PR 94 local-postgres-branch driver follow-on slice. Local-postgres-branch driver-backed checkout now creates restore-point metadata, reset/restore append restore-point records, `restore --json` emits `scenery.db.branch.restore.v1`, local-postgres-branch driver-backed `diff --json` emits `scenery.db.branch.diff.v1`, and `testdata/apps/neon-basic` covers a loadable Neon fixture app. JSON schemas and the local contract now include branch restore/diff and restore-point persistence.
- [x] 2026-06-08: Split the branch executable boundary into truthful backend names. `SCENERY_DEV_NEON_SELFHOST_DRIVER` now selects the actual `neon-selfhost` branch driver and is preferred when configured; `SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` remains the local Postgres-shaped development fallback; tests use explicitly named fake drivers.
- [x] 2026-06-08: Addressed cleanup-safety review feedback. Neon uninstall now falls back to removing Scenery-labeled containers when `cell.json` is corrupt or Compose is missing and keeps local state if teardown fails; branch prune is scoped to the current Neon project; Neon `down --db` removes the selected session lease when session metadata exists; `worktree create` rolls back Git worktrees if Neon pin creation fails; and `worktree remove --db` verifies the target is a registered Git worktree before deleting local `.scenery` state.
- [x] 2026-06-09: Follow-on plan `docs/plans/0070-toolchain-managed-neon-selfhost-driver.md` absorbed the real-provider milestone. The built-in `neon-selfhost-driver` can now bootstrap pageserver tenant/timeline metadata and start or reuse branch compute containers from generated templates, while real Docker-backed readiness and sync isolation proof remain tracked in plan 0070.
- [x] 2026-06-09: Hardened default self-harness coverage for real branch-local DB lifecycle and sync isolation. The Docker-backed Neon proof now checks ready branch worktrees, isolated branch writes, reset, restore, schema diff, delete, managed app env, managed sync branch URL resolution, and distinct sync replication stream IDs, slot names, and Postgres application names across branch worktrees.

## Surprises & Discoveries

- 2026-06-08: `scenery inspect docs --json` reports `summary.review_due_count: 0`; no doc gardening was required before implementing the first Neon slice.
- 2026-06-08: The first implemented substrate slice intentionally creates generated `cell.json` and `compose.generated.yml` but reports the cell as installed, not ready. This avoids pretending branch-backed `scenery up` exists before the branch provider and health checks land.
- 2026-06-08: A local `checkout` command can safely write the worktree pin before the Neon backend exists. Reset/delete should validate destructive intent now but still return a backend-not-implemented error so users and agents do not mistake a file-only change for storage mutation.
- 2026-06-08: Neon Local (`neondatabase/neon_local`) is a cloud Neon proxy that can create or connect to cloud branches through a local Postgres endpoint. It is useful future source material for a cloud-hosted mode, but it requires Neon credentials and is not the self-hosted dev-cell target of this ExecPlan.
- 2026-06-08: Branch lease resolution can be made useful before database connectivity lands. Existing pins must win, manual policy must require explicit checkout, worktree policy can use the default `{app}/{git_branch}` template, and session policy needs a `{app}/{session}` default so autonomous sessions do not collide.
- 2026-06-08: `git worktree remove` refuses to remove a worktree with generated local state unless Scenery cleans up `.scenery/worktree-db.json` first. The first `worktree remove --db` implementation removes only that local pin state and leaves real backend branch deletion for the branch provider.
- 2026-06-08: Neon dev-cell health can be useful before startup exists. Status can inspect Docker, optional images, generated files, and labeled containers without coupling app sessions to a partially implemented branch provider.
- 2026-06-08: Branch status needs two concepts: local pin state and backend branch state. Keeping `backend_status` separate lets agents safely use local worktree pins now while refusing to assume the Neon branch exists.
- 2026-06-08: Existing local dev-cell state may already contain a legacy `scenery.db.neon.branches.v1` branch map. The registry reader migrates that shape in memory into the new `scenery.db.branch.registry.v1` lease list before the next checkout, expire, prune, or install write.
- 2026-06-08: Ready endpoint metadata is a useful provider boundary before backend branch creation exists. The registry can carry redacted `host`/`port`/`database`/`role`/`sslmode` data so runtime consumers can connect without writing raw URLs to branch pins, branch status JSON, or global lease state.
- 2026-06-08: A configured local-postgres-branch driver is a development fallback only: it can prove Scenery pin/lease/env semantics against local Postgres-shaped endpoints, but it is not the actual Neon dev-cell backend. The real self-hosted Neon backend must be named `neon-selfhost` when it lands.
- 2026-06-08: Cleanup commands that share a global Neon registry need both ownership and scope checks. `prune` is project-scoped, `down --db` is session-scoped when session pins exist, and worktree cleanup must prove the Git target before deleting local `.scenery` state.
- 2026-06-08: It is useful for generated Neon state to reserve loopback debug ports before startup exists, but the status contract must phrase those as metadata. A port number in `cell.json` is not a health check and must not be treated as a live listener.
- 2026-06-08: Docker `Up` is not enough for Neon readiness. Status needs an additional loopback listener probe for each running required component so fake or half-started containers do not look ready.
- 2026-06-07: `scenery inspect docs --json` reports `summary.review_due_count: 9`; `PLANS.md` itself is review-due, but its required ExecPlan section contract is still usable and enforced by self-harness.
- 2026-06-07: Neon CLI docs state that `neon checkout` requires neonctl 2.22.2+, resolves a branch by name or ID, and heals the local `.neon` file by writing `projectId`, `branchId`, and `orgId` when available. This validates the local branch-pin primitive that Scenery should internalize in `.scenery/worktree-db.json` rather than exposing directly.
- 2026-06-07: Neon CLI branch docs list branch `list`, `create`, `reset`, `restore`, `rename`, `schema-diff`, `set-default`, `set-expiration`, `add-compute`, `delete`, and `get`; create supports named branches, parent branch/timestamp/LSN, `--schema-only`, compute options, and expiration. Scenery can map those into a smaller app-session-safe command surface.
- 2026-06-07: The official Neon repo's `docker-compose/README.md` says its Compose configuration is for testing Docker images and is "not intended for deploying a usable system"; it also says to use `cargo neon` for a development environment. The Compose topology is useful source material, not a supported user contract.
- 2026-06-07: The upstream Compose file includes MinIO, a bucket creation helper, pageserver, three safekeepers, storage broker, and a compute wrapper exposing Postgres on `55433`. Scenery must decide whether to adapt this test topology, use a newer supported self-hosted path if one exists, or explicitly document the dev-cell limitations before implementation.

## Decision Log

- Decision: Treat Neon as a provider under the existing `dev.services.postgres` beta surface, not as a separate top-level `.scenery.json` subsystem.
  Rationale: Current local contract already defines `dev.services` as the Scenery-owned substrate surface and managed Postgres already injects `DatabaseURL` into app/setup/worker environments.
  Date/Author: 2026-06-07 / Codex

- Decision: Store global Neon cell metadata separately from app/worktree/session branch leases.
  Rationale: Existing managed Postgres keeps physical substrate metadata separate from session database records. Neon must do the same so branch IDs and connection URLs do not leak into global machine state or `.env` files.
  Date/Author: 2026-06-07 / Codex

- Decision: Use `.scenery/worktree-db.json` as the worktree-local branch pin and keep connection strings process-injected.
  Rationale: This mirrors the useful `neon checkout` pin behavior while preserving Scenery's authority over session env injection and avoiding stale or leaked `.env` database URLs.
  Date/Author: 2026-06-07 / Codex

- Decision: Protect the parent branch by default and require explicit destructive intent for reset/delete operations.
  Rationale: Branchable dev databases are valuable because they make parallel work safe. Accidentally mutating or deleting the parent would break that safety invariant.
  Date/Author: 2026-06-07 / Codex

- Decision: Do not call the feature production self-hosted Neon.
  Rationale: The target is a Scenery-managed Neon dev cell for local and agent development. Upstream Docker Compose material is not presented as a production deployment contract.
  Date/Author: 2026-06-07 / Codex

## Outcomes & Retrospective

Completed on 2026-06-09.

The local Neon dev-cell surface now has the end-to-end shape described by this
plan: config, generated cell state, branch pins and leases, worktree commands,
ready branch endpoint consumption by app sessions and sync, parent/foreign
lease safety, branch lifecycle mutations, and default self-harness coverage.
Follow-on plans 0070, 0071, and 0072 completed the built-in driver, bind-mounted
storage, and project-tenant mapping slices that absorbed this plan's remaining
real-provider milestone.

## Context and Orientation

Start with these files and commands:

- `AGENTS.md` for repo-local rules and validation expectations.
- `PLANS.md` for ExecPlan structure.
- `docs/local-contract.md` for current CLI grammar, JSON contracts, toolchain rules, managed Postgres/sync behavior, and current `scenery up` session semantics.
- `docs/schemas/scenery.config.v1.schema.json` for `.scenery.json` validation.
- `docs/schemas/scenery.toolchain.v1.schema.json` and `scenery.toolchain.json` for managed image/tool entries.
- `docs/agent-guide.md` and `SKILL.md` for agent-facing app-session behavior.
- `docs/plans/0041-agent-managed-postgres-and-sync.md` for current managed Postgres/sync lifecycle, env injection, and substrate/session split.
- `docs/plans/0063-db-lifecycle-split.md` for DB apply, seed, and setup ordering.
- `cmd/scenery/dev_services.go`, `cmd/scenery/dev_substrate_manager.go`, `cmd/scenery/dev_supervisor.go`, `cmd/scenery/db_setup.go`, `cmd/scenery/db_seed.go`, and `cmd/scenery/psql.go` for current managed DB startup and CLI command plumbing.
- `scenery inspect docs --json` before implementation to catch review-due docs and drift.

External source material that must be re-verified at implementation time:

- Neon CLI checkout docs: `neon checkout` pins branch context in `.neon` and requires neonctl 2.22.2+.
- Neon CLI branch docs: branch create/reset/restore/diff/expiration/delete operations and JSON output contracts.
- Neon architecture docs: compute is ephemeral, storage is durable via safekeepers, pageserver, and object storage; branch/restore operations are copy-on-write metadata operations.
- Neon repo `docker-compose/README.md` and `docker-compose/docker-compose.yml`: useful topology reference with an explicit warning that the Compose setup is for testing images, not a usable deployment.

Current Scenery constraints:

- App roots are marked by `.scenery.json`.
- `scenery up` starts the app session with managed services, setup, routes, dashboard, logs, traces, and metrics.
- `scenery serve` is headless API runtime and must not be expected to expose the dev cell, dashboard, proxy, or watch behavior.
- Managed Postgres currently defaults to version `18` and `isolation: "database"`; other isolation modes are rejected until implemented.
- Managed Postgres currently injects `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, and `SCENERY_MANAGED_DATABASE_NAME` into managed app/setup/worker environments.
- Managed sync receives routed `SYNC_URL`/`SCENERY_SYNC_URL` and must not collide across parallel sessions.
- Toolchain entries live in `scenery.toolchain.json`; optional unstable image refs are allowed outside strict verification while digest pinning is still being migrated.

## Milestones

1. Contract First: `.scenery.json`, state files, CLI grammar, JSON schemas, docs, and toolchain manifest entries describe Neon dev-cell behavior before runtime code depends on it.
2. Neon Dev Cell Substrate: Scenery can install, start, inspect, log, restart, and uninstall a shared local Neon dev cell without exposing raw upstream Compose as a user-maintained workflow.
3. Branch Provider: Scenery can create, checkout, reset, restore, delete, expire, inspect, and prune Neon branches through a provider interface with worktree/session/manual branch policies.
4. App Session Integration: `scenery up` resolves the right branch lease, injects managed database env values, runs DB apply/seed/setup against that branch, starts sync against that branch, and starts the app session.
5. Worktree Workflow: `scenery worktree create/list/remove` couples Git worktrees with Scenery-owned branch pins and safe cleanup.
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
      "sync": {
        "kind": "sync"
      }
    }
  }
}
```

Supported branch policies:

- `manual`: only explicit checkout or `--db-branch` chooses the branch.
- `worktree`: one DB branch per Git worktree or Git branch.
- `session`: one DB branch per Scenery app session.

Default policy should be `worktree` for interactive human development. For
autonomous agent sessions, prefer `session` unless the app config explicitly
chooses `worktree` or `manual`.

Global substrate state should live under the local agent home, for example:

```text
~/.scenery/agent/substrates/neon/
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
.scenery/worktree-db.json
```

with a schema like:

```json
{
  "schema_version": "scenery.db.branch.v1",
  "provider": "neon-selfhost",
  "project": "onlv",
  "parent_branch": "main",
  "branch": "onlv/pricing-agent",
  "branch_id": "br-local-01J...",
  "database": "onlv",
  "role": "cloud_admin",
  "session_id": "pricing-agent",
  "worktree_root": "/Users/pbrazdil/dev/onlv-pricing-agent",
  "created_by": "scenery",
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

Finally, wire branch leases into app sessions. `scenery up` should resolve the DB
branch in this order:

1. explicit `--db-branch` if the CLI introduces it;
2. `.scenery/worktree-db.json`;
3. Git worktree branch name;
4. Git branch name;
5. session id;
6. app id fallback.

When the branch is ready, run the existing DB lifecycle against it: apply first,
then seed, then `dev.setup`, then app/worker/frontend/sync startup. Preserve
current env precedence and redaction behavior.

## Concrete Steps

1. Re-run `scenery inspect docs --json`; record review-due docs or contract drift in this plan if relevant.
2. Update `docs/local-contract.md` with `dev.services.postgres.kind = "neon"`, `mode = "self-hosted"`, `isolation = "branch"`, branch policies, worktree DB pin path, safe destructive semantics, and JSON command contracts.
3. Update `docs/schemas/scenery.config.v1.schema.json` so unknown Neon fields fail closed and valid Neon config passes.
4. Add `docs/schemas/scenery.db.branch.v1.schema.json` for `.scenery/worktree-db.json`.
5. Add or update schemas for `scenery db branch status --json`, `scenery db branch list --json`, `scenery db neon status --json`, and any `scenery up --json`/`scenery status --json` database fields touched by the implementation.
6. Add Neon and compute-node image entries to `scenery.toolchain.json`; add MinIO only if the selected dev-cell topology needs it.
7. Decide and document whether command spelling is `scenery up` only, `scenery dev` alias, or a clean rename with compatibility handling. Do not leave both as ambiguous active contracts.
8. Add a Neon dev-cell package or cohesive module near the existing dev-service code. If a new package is introduced, keep `cmd/scenery` command handlers thin.
9. Implement image/toolchain resolution for Neon images using the existing managed toolchain rules and Docker/Podman availability checks.
10. Implement stable port allocation and global cell state under the local agent substrate directory.
11. Generate `compose.generated.yml` and component config from code. Treat upstream Neon Compose as source material only.
12. Implement start/stop/restart/uninstall operations for MinIO/object storage, pageserver, safekeepers, storage broker, proxy/compute, and any selected control-plane helpers.
13. Implement health checks for pageserver HTTP, safekeepers, compute/proxy Postgres readiness, and object storage readiness.
14. Implement `scenery db neon install --json`, `scenery db neon start --json`, `scenery db neon status --json`, `scenery db neon logs`, `scenery db neon stop --json`, `scenery db neon restart`, and `scenery db neon uninstall --destroy-data` or the equivalent `scenery substrate` command shape if the CLI decision changes.
15. Implement the branch provider interface and Neon self-hosted provider.
16. Implement branch name derivation from `{app}`, `{git_branch}`, `{worktree}`, and `{session}` with sanitization and collision handling.
17. Implement `.scenery/worktree-db.json` read/write/heal logic and schema validation.
18. Implement `scenery db branch`, `list`, `status --json`, `checkout <name>`, `reset`, `delete <name>`, `prune`, `restore --at`, `diff`, and `expire --after` according to the accepted CLI surface.
19. Gate destructive operations: protect parent branch, refuse deleting the current branch without force/confirmation, and require `--yes` for non-interactive destructive commands.
20. Integrate branch resolution with `scenery up` before DB apply/seed/setup and app child startup.
21. Preserve managed env injection: app/setup/worker receive `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, and `SCENERY_MANAGED_DATABASE_NAME`; do not make `.env` or `DATABASE_URL` authoritative.
22. Attach sync to the current branch and ensure replication stream/slot naming stays session- or branch-scoped.
23. Implement `scenery worktree create <name>`, `scenery worktree list --json`, and `scenery worktree remove <name> [--db]`.
24. Make `worktree create` run `git worktree add`, derive/create the Neon branch, write the worktree-local DB pin, and print the next command.
25. Add fixture apps: `testdata/apps/neon-basic`, `testdata/apps/neon-sync`, `testdata/apps/neon-worktrees`, and `testdata/apps/neon-agent-parallel`, or a smaller equivalent fixture set if tests share helpers without losing coverage.
26. Add harness cases for two simultaneous worktrees, same parent with different branches, branch-local `db apply`, branch-local seed fingerprinting, reset-to-parent, current-branch delete refusal, Scenery-owned prune behavior, sync slot isolation, `scenery down` not stopping the shared cell, `scenery down --db` removing only the current branch lease, and `scenery down --state` removing local branch pins.
27. Update `docs/agent-guide.md`, `SKILL.md`, `README.md`, `docs/app-development-cookbook.md`, `docs/environment.md`, and `docs/environment.registry.json` only after behavior works and the smoke tests pass.
28. Update `docs/knowledge.json` if new schemas/docs/plans need to be discoverable before generated indexing exists.
29. Run the validation commands and record outcomes, failures, or skipped commands in this plan.

## Validation and Acceptance

Acceptance criteria:

- `.scenery.json` accepts a Neon self-hosted branch-isolated managed Postgres config and rejects unsupported Neon modes, unknown fields, and unsupported isolation values.
- `scenery.toolchain.json` exposes pinned or explicitly unstable Neon image artifacts through `scenery system toolchain list|sync|verify --images --json`.
- `scenery db neon install --json` creates generated substrate files, starts the dev cell, and records only physical cell metadata in the global substrate record.
- `scenery db neon status --json` reports component state, health, redacted ports, version/source, and log paths without per-session database URLs.
- `.scenery/worktree-db.json` pins provider/project/parent/branch/branch_id/database/role/session/worktree/ttl and validates against `scenery.db.branch.v1`.
- `scenery db branch status --json` reports current branch lease, parent, provider, DB env name, reset command, and redacted connection state.
- `scenery db psql` connects to the current worktree/session branch.
- `scenery up` creates or reuses the right branch according to the branch policy, runs apply/seed/setup against that branch, starts the app, and injects the existing managed database env names.
- sync attaches to the current branch and parallel sessions do not collide on replication slots or streams.
- `scenery worktree create pricing-agent` creates a Git worktree, creates or reuses the matching DB branch, writes the worktree-local branch pin, and prints the next command.
- `scenery db branch reset` resets only the current non-parent branch to its parent and cannot mutate the parent by default.
- `scenery db branch delete` refuses the current branch unless explicitly forced and refuses parent branch deletion.
- `scenery db branch prune` deletes only expired Scenery-owned branches and leaves user/foreign branches alone.
- `scenery down` stops the app session but not the shared Neon dev cell by default.
- `scenery down --db` removes only the current branch lease/branch according to the documented destructive policy.
- `scenery down --state` can remove the local branch pin without destroying unrelated global substrate state.

Validation commands:

```sh
jq empty docs/knowledge.json
scenery inspect docs --json
scenery system toolchain verify --json --images
scenery db neon install --json
scenery db neon status --json
scenery db branch status --json
scenery worktree create neon-smoke --from main
scenery up --app-root ../<created-worktree> --json --detach
scenery db psql --app-root ../<created-worktree>
scenery db branch reset --app-root ../<created-worktree> --yes
scenery down --app-root ../<created-worktree> --db --state
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
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
scenery inspect docs --json
scenery harness self --json --write
```

## Idempotence and Recovery

All substrate commands must be restartable. If install fails halfway, rerunning
`scenery db neon install --json` should inspect existing state, reuse healthy
components, replace only broken generated files, and report unrecoverable
component failures with log paths. Generated Compose files may be overwritten by
Scenery; user edits to those generated files are not part of the supported
contract.

Branch creation must be safe to retry. If a branch name exists, inspect ownership
metadata before reuse. If `.scenery/worktree-db.json` references a deleted branch,
heal it by recreating the branch only when policy allows; otherwise report a JSON
error with the explicit checkout/create command.

Destructive recovery rules:

- Never reset, delete, expire, or prune the parent branch.
- Never delete foreign branches that lack Scenery ownership metadata.
- Refuse current-branch deletion unless forced and documented.
- Keep branch connection URLs out of `.env`, global substrate records, logs, and
  non-redacted JSON.
- If sync startup fails after the DB branch is ready, leave the branch lease
  for inspection and make `scenery down --db` or branch delete perform cleanup.

## Artifacts and Notes

Expected checked-in artifacts:

- `docs/plans/0065-scenery-managed-neon-dev-cell.md`
- `docs/local-contract.md` updates for config, CLI, JSON, and state paths
- `docs/schemas/scenery.config.v1.schema.json`
- `docs/schemas/scenery.db.branch.v1.schema.json`
- Any new command-result schemas for Neon/branch status
- `scenery.toolchain.json` Neon image entries
- Fixture apps and focused tests when implementation begins

Expected generated/local artifacts, not committed:

- `~/.scenery/agent/substrates/neon/cell.json`
- `~/.scenery/agent/substrates/neon/compose.generated.yml`
- `~/.scenery/agent/substrates/neon/*` component data and logs
- `.scenery/worktree-db.json` inside target app worktrees unless a fixture explicitly checks in a sanitized example
- `.scenery/harness/self-latest.json` from self-harness validation

Open questions to resolve during implementation:

- Whether to introduce `scenery dev` as an alias for `scenery up`, keep `scenery up` only, or rename the local-session command with an explicit migration.
- Whether the first dev cell should adapt the upstream test Compose topology, use `cargo neon`, or wait for a more stable upstream self-hosting surface.
- Whether the local dev cell needs MinIO initially, or whether another object storage strategy is available and simpler for macOS/Linux agents.
- Whether branch IDs in a self-hosted local cell can use upstream Neon ID formats or should use Scenery-local IDs with a `br-local-` prefix.
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

- `.scenery/worktree-db.json` with `schema_version: "scenery.db.branch.v1"`.
- Global Neon cell state under the local agent substrate directory with physical component metadata only.

New or changed CLI surface, subject to the `scenery up` versus `scenery dev` naming decision:

- `scenery db psql`
- `scenery db branch`
- `scenery db branch list [--json]`
- `scenery db branch checkout <name>`
- `scenery db branch reset [--yes]`
- `scenery db branch delete <name> [--force]`
- `scenery db branch prune [--older-than <duration>] [--json]`
- `scenery db branch status --json`
- `scenery db branch restore --at <timestamp-or-lsn> [--yes]`
- `scenery db branch diff <branch>`
- `scenery db branch expire --after <duration>`
- `scenery db neon install --json`
- `scenery db neon start --json`
- `scenery db neon status --json`
- `scenery db neon stop --json`
- `scenery db neon restart`
- `scenery db neon logs`
- `scenery db neon uninstall --destroy-data`
- `scenery worktree create <name> [--from <branch>]`
- `scenery worktree list --json`
- `scenery worktree remove <name> [--db]`

Runtime dependencies:

- Docker or Podman for the initial image-backed dev cell unless implementation chooses a different self-hosting path.
- Neon images and compute-node images declared in `scenery.toolchain.json`.
- Optional MinIO image if object storage is part of the first cell topology.
- Existing Scenery managed toolchain, substrate registry, app-session supervisor, DB lifecycle, and sync process/container support.
