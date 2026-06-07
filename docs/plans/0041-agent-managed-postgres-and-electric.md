# Agent Managed Postgres and Electric

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

agent-native local-dev removes fixed per-worktree Postgres and Electric ports by moving database-adjacent dev services behind the local onlava agent. ExecPlan 0040 added the `dev.services` config surface and shared substrate registry; this plan implements the managed database lifecycle behind that contract.

After this work, `onlava dev` can create or reuse an agent-owned local Postgres substrate, allocate an isolated database per agent session, expose the effective `DatabaseURL` to the app process, route Electric through the agent as a hidden per-session backend, and provide `onlava db reset`, `onlava db psql`, and snapshot commands that operate on the current session.

## Progress

* [x] 2026-05-26: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-27: Define the first `dev.services.postgres` runtime defaults and failure modes: version `18`, database isolation only, active agent session required, and `ONLAVA_DEV_POSTGRES_ADMIN_URL` required until local substrate startup exists.
* [x] 2026-05-27: Implement Postgres substrate registration/reuse for the explicit admin URL path without adding a mandatory Docker dependency to ordinary tests.
* [x] 2026-05-27: Implement local Postgres substrate startup without adding a mandatory Docker dependency to ordinary tests.
* [x] 2026-05-27: Create per-session databases and inject the effective `DatabaseURL` into app child environments for the explicit admin URL path.
* [x] 2026-05-27: Change declared managed Postgres to override local app DB env by default, with `ONLAVA_DEV_POSTGRES_EXTERNAL=1` as the explicit external-database escape hatch.
* [x] 2026-05-27: Implement `onlava db reset` for the current managed session database in the explicit admin URL path.
* [x] 2026-05-27: Implement snapshot create/restore commands for the current managed session database using `pg_dump` and `psql`.
* [x] 2026-05-27: Register Electric as a hidden per-session backend and route it through the agent when `ONLAVA_DEV_ELECTRIC_UPSTREAM` is provided.
* [x] 2026-05-27: Implement Electric process/container startup for `dev.services.electric`.
* [x] 2026-05-27: Make managed Electric use a deterministic session-scoped replication stream id so parallel sessions sharing one Postgres cluster do not collide on Electric replication slots.
* [x] 2026-05-27: Update docs, schemas, harness checks, and validation artifacts.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-27: The existing `cmd/onlava` targeted test set can hit Docker-dependent dashboard data coverage. A focused run for the new managed Postgres helpers passed, while a broader `go test ./cmd/onlava ./internal/agent ./internal/app` attempt timed out in `TestDashboardDataRPC` while Docker was unavailable.

* 2026-05-27: The least surprising first managed Postgres path is reuse of an explicit admin URL. This avoids silently depending on Docker/Homebrew/system Postgres and still gives agent-native local-dev its key per-session database semantics for environments that provide a local cluster.

* 2026-05-27: Electric can be routed through the agent without owning its process yet by registering an explicit `ONLAVA_DEV_ELECTRIC_UPSTREAM`. This matches the hidden-port direction while leaving container/process startup for the next slice.

* 2026-05-27: Snapshot create/restore can be implemented without a new dependency by shelling out to `pg_dump` and `psql`, matching the existing `onlava psql` helper's dependency posture.

* 2026-05-27: Once one session registers the explicit Postgres admin URL in the agent substrate registry, later sessions and DB commands can reuse that recorded substrate URL if the env var is absent.

* 2026-05-27: Local managed Postgres can run without Docker by using `initdb` and `postgres` from PATH or explicit binary env vars. The cluster binds through a private Unix socket under the agent directory, so there is no stable public Postgres port.

* 2026-05-27: Electric needs either an explicit upstream, a local binary, or an explicitly configured Docker image. The app receives only the routed `ELECTRIC_URL`; service-specific `dev.services.electric.env` values stay on the Electric process/container.

* 2026-05-27: ONLV requested Postgres 18 while the local `postgres` binary was 14. The managed substrate now uses Docker for the requested major version when Docker is available, records the actual source/version, and falls back to the local binary only when Docker is unavailable.

* 2026-05-27: The Postgres 18 Docker image rejects a direct mount at `/var/lib/postgresql/data`; mounting the parent `/var/lib/postgresql` matches the image's versioned data-directory convention. Electric also requires managed Postgres to start with `wal_level=logical`, `max_wal_senders`, and `max_replication_slots`.
* 2026-05-27: Electric's default replication stream id creates the cluster-wide slot `electric_slot_default`. Two onlava sessions sharing one managed Postgres cluster therefore collide even when they use different databases. Setting `ELECTRIC_REPLICATION_STREAM_ID` from the onlava session id gives each session its own Electric publication and slot names.

## Decision Log

* Decision: Keep the first Postgres implementation optional and explicitly local-dev only.
  Rationale: agent-native local-dev needs port isolation and repeatable local sessions, but onlava should not make Docker, Homebrew Postgres, or a specific system package manager mandatory for the whole CLI.
  Date/Author: 2026-05-26 / Codex

* Decision: When `dev.services.postgres` is declared, managed Postgres wins over local app DB URLs by default and exposes the session database through `DatabaseURL`.
  Rationale: The agent-native local-dev end state makes onlava own local dev services. Preserving stale `.env` DB URLs silently leaves apps on old or remote databases even though the repo has opted into managed session DBs. Developers who intentionally want an external DB can set `ONLAVA_DEV_POSTGRES_EXTERNAL=1`.
  Date/Author: 2026-05-27 / Codex

* Decision: Prefer local Postgres binaries over a Docker-managed cluster for the default startup path.
  Rationale: The agent-native local-dev contract requires removing stable per-worktree ports, not requiring Docker for all users. `initdb`/`postgres` plus a private Unix socket gives onlava an owned substrate with a smaller dependency surface.
  Date/Author: 2026-05-27 / Codex

* Decision: Prefer Docker when the configured Postgres major version does not match the local binary and Docker is available.
  Rationale: `dev.services.postgres.version` should mean the requested version when the host can provide it. This keeps ONLV on Postgres 18 while preserving the local-binary fallback for machines without Docker.
  Date/Author: 2026-05-27 / Codex

* Decision: Start Electric only from explicit local sources.
  Rationale: Automatically pulling an arbitrary Electric image would be surprising. `ONLAVA_DEV_ELECTRIC_UPSTREAM`, `ONLAVA_DEV_ELECTRIC_BIN`, or an explicit `dev.services.electric.image` keeps startup behavior visible.
* Decision: Default managed Electric's replication stream id to a sanitized onlava session identifier.
  Rationale: Postgres replication slot names are cluster-wide, while agent-native local-dev intentionally shares one local Postgres substrate across sessions. Session-scoped stream ids preserve parallel worktree isolation without requiring separate Postgres containers.
  Date/Author: 2026-05-27 / Codex

## Outcomes & Retrospective

Completed on 2026-05-27.

Shipped outcome:

* `dev.services.postgres` now creates or reuses a deterministic per-session database through an explicit admin URL, a reusable agent substrate, or local `initdb`/`postgres` binaries under the agent state directory.
* Managed Postgres uses database isolation, registers substrate metadata, overrides stale app DB URLs by default, and exposes session DB URLs through `DatabaseURL` to app processes and `onlava db ...` commands. Explicit external DBs are still possible with `ONLAVA_DEV_POSTGRES_EXTERNAL=1`.
* `onlava db reset` and `onlava db snapshot create|restore` target only the current managed session database.
* `dev.services.electric` now routes through the agent from an explicit upstream, a local binary, or an explicitly configured Docker image, while keeping service env separate from app env.
* Docker-backed managed Postgres uses the requested major version when the local binary does not match, starts with logical replication settings for Electric, and mounts Postgres 18 data at the parent Docker image data root.
* `docs/local-contract.md` and `docs/schemas/onlava.config.v1.schema.json` describe the current managed-service contract.

Validation:

```sh
go test -run 'Test(ManagedPostgres|PostgresDatabase|LocalPostgres|ResolveLocalPostgres|ManagedElectric|StartManagedElectric|PrepareDevAgentSession|ParseDB|DBCommand)' ./cmd/onlava
go test ./cmd/onlava ./internal/agent ./internal/app
go test ./...
python3 -m json.tool docs/schemas/onlava.config.v1.schema.json >/dev/null
go install ./cmd/onlava
onlava harness self --json --write
```

All validation commands passed. The self harness wrote `.onlava/harness/self-latest.json` and reported warnings for existing documentation freshness and large files, including the newly large `cmd/onlava/dev_services.go`, but no errors.

Follow-up validation after the ONLV integration fixes:

```sh
go test ./...
onlava harness self --json --write
```

Both commands passed on 2026-05-27. The live ONLV session also verified Postgres `server_version_num=180001`, `wal_level=logical`, and a migrated `audit.row_changes` table.

## Context and Orientation

Relevant files:

```text
docs/plans/0037-onlava-agent-mvp.md
docs/plans/0040-agent-shared-substrates-and-dev-services.md
internal/app/root.go
internal/agent/*
cmd/onlava/dev_supervisor.go
cmd/onlava/psql.go
cmd/onlava/dashboard.go
cmd/onlava/removed-agent-transport.go
docs/schemas/onlava.config.v1.schema.json
```

## Milestones

Milestone 1 freezes the managed Postgres config/defaults and effective environment behavior.

Milestone 2 implements an agent substrate for local Postgres with per-session database allocation and cleanup.

Milestone 3 adds `onlava db reset`, psql alignment, and snapshot/export/import commands.

Milestone 4 adds Electric as a hidden per-session backend routed by the agent.

Milestone 5 updates docs, schemas, self-harness checks, and practical integration coverage.

## Plan of Work

Start with explicit contracts and fake-backed unit tests. Prefer existing local Postgres binaries, an explicit admin URL, or an already registered agent substrate before introducing any container-based fallback. If a container fallback is added, keep it optional and isolate integration tests behind the existing test Postgres conventions.

## Concrete Steps

1. Add helpers that resolve `dev.services.postgres` and `dev.services.electric` into effective runtime plans.
2. Extend the agent substrate registry with Postgres cluster metadata and per-session database records.
3. Add a Postgres manager that can reuse an explicit/admin URL, reuse an existing local substrate, or start a local substrate when the required binary/runtime is available.
4. Create deterministic per-session database names from base app ID and session ID.
5. Inject `DatabaseURL` into app child env for managed-session databases unless `ONLAVA_DEV_POSTGRES_EXTERNAL=1` is set.
6. Implement `onlava db reset` and snapshot/export/import commands against the resolved session database.
7. Register Electric as a session backend with agent routes and effective env injection.
8. Add focused tests for config resolution, session DB naming, command dispatch, substrate persistence, and env precedence.

## Validation and Acceptance

Expected validation:

```sh
go test ./cmd/onlava ./internal/agent ./internal/app
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

Observable behavior:

* Two worktrees using managed Postgres do not share a database by default.
* `onlava dev` does not require fixed Postgres or Electric host ports.
* App child env receives a session-scoped `DatabaseURL` when managed Postgres is enabled.
* `onlava db psql` connects to the current session database.
* `onlava db reset` resets only the current session database.
* Electric is reachable through an agent-routed session URL without claiming a stable global host port.

## Idempotence and Recovery

The agent must never drop or kill a user-owned database unless it can prove the database was created by onlava for the current session or the user explicitly selected a destructive command for that session.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/onlava/db*.go
cmd/onlava/dev_supervisor.go
internal/agent/*
internal/app/root.go
docs/schemas/onlava.config.v1.schema.json
docs/local-contract.md
docs/plans/0041-agent-managed-postgres-and-electric.md
```

## Interfaces and Dependencies

The intended app config surface starts from the beta `dev.services` shape added in 0040:

```json
{
  "dev": {
    "services": {
      "postgres": {
        "kind": "postgres",
        "version": "18",
        "isolation": "database"
      },
      "electric": {
        "kind": "electric",
        "database": "postgres",
        "route": "electric"
      }
    }
  }
}
```

Implementation must keep external dependencies optional for ordinary CLI use. Tests should use fakes for lifecycle decisions and only use real Postgres/Electric processes in opt-in or already-established integration-test paths.

Current local substrate interface:

```sh
ONLAVA_DEV_POSTGRES_ADMIN_URL=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
ONLAVA_DEV_POSTGRES_INITDB=/opt/homebrew/opt/postgresql@18/bin/initdb
ONLAVA_DEV_POSTGRES_BIN=/opt/homebrew/opt/postgresql@18/bin/postgres
ONLAVA_DEV_ELECTRIC_UPSTREAM=http://127.0.0.1:3000
ONLAVA_DEV_ELECTRIC_BIN=/usr/local/bin/electric
```

When `dev.services.postgres` is declared, `onlava dev` creates/reuses a session database named from the base app ID plus session ID and injects `DatabaseURL` for the app child even if local env files contain older DB URLs. The admin cluster comes from `ONLAVA_DEV_POSTGRES_ADMIN_URL`, an already registered agent substrate, Docker for the requested version when available, or local `initdb`/`postgres` binaries under the agent state directory. Set `ONLAVA_DEV_POSTGRES_EXTERNAL=1` with `DatabaseURL` to keep an explicit external DB URL.

When `dev.services.electric` is declared, `onlava dev` registers `ONLAVA_DEV_ELECTRIC_UPSTREAM` directly or starts a hidden Electric process from `ONLAVA_DEV_ELECTRIC_BIN` or a configured `dev.services.electric.image` through Docker. The app receives `ELECTRIC_URL` with the agent-routed session URL; Electric service env values stay on the Electric process/container.
