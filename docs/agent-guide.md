# onlava Agent Guide

This guide is for AI agents using onlava or changing onlava. It explains how to combine repo-local instructions, the installable skill, CLI JSON, onlava capabilities, and app-local instructions.

For exact command grammar and schemas, use `docs/local-contract.md`. For app recipes, use `docs/app-development-cookbook.md`. For onlava repo edits, `AGENTS.md` is the first file to read.

## Source Of Truth Order

Use this order when instructions overlap:

1. Current implementation and tests.
2. Machine-readable CLI output: `onlava inspect ... --json`, `onlava check --json`, `onlava logs --jsonl`, scoped observability query commands, and `onlava harness ... --json`.
3. JSON schemas in `docs/schemas/`.
4. `docs/local-contract.md`.
5. This guide.
6. `SKILL.md`.
7. README and completed ExecPlans.

If old prose disagrees with current JSON or tests, fix the drift. Do not add legacy aliases unless an active plan explicitly requires compatibility.

## Agent Fast Path

Inside an onlava app:

```sh
onlava doctor --json
onlava check --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava inspect wire --json
onlava system toolchain verify --json
```

During local debugging:

```sh
onlava up
onlava inspect observability --json --session current
onlava logs --session current --jsonl --limit 200
onlava logs query --json --session current --since 15m --query 'error OR panic'
onlava traces list --json --session current --since 15m --slowest
onlava metrics list --json --session current --since 1h
onlava metrics query --json --session current --since 15m --step 5s --promql 'onlava_request_duration_seconds'
```

Before finishing app work:

```sh
onlava check --json
go test ./...
onlava harness --json --write
onlava validate quick --json --write
```

Before finishing onlava repo work:

```sh
go test ./...
go test ./cmd/onlava
onlava harness self --summary --write
```

Do not run `go install ./cmd/onlava` during agent validation unless a human
explicitly asks. Multiple worktrees share the installed `onlava` binary; the
self-harness builds `.onlava/harness/bin/onlava` inside the current worktree for
freshness checks.

When a command cannot be run, report the exact command and environmental reason.

## Working In The onlava Repo

Read:

```text
AGENTS.md
docs/local-contract.md
docs/agent-guide.md
docs/plans/active.md
docs/tech-debt.md
```

Use `onlava inspect docs --json` when the binary is available. It reports indexed docs, missing docs, stale docs, and plan paths.

Keep these layers synchronized when behavior changes:

- CLI grammar and JSON contracts: `docs/local-contract.md`, schemas, tests.
- Agent workflows: `docs/agent-guide.md`, `AGENTS.md`, `SKILL.md`.
- Human overview: `README.md`.
- App recipes: `docs/app-development-cookbook.md`.
- Environment variables: `docs/environment.md` for humans and `docs/environment.registry.json` for the self-harness contract.
- Indexed docs metadata: `docs/knowledge.json`.

## Working In An onlava App

Use app-local `AGENTS.md` first, then this skill/guide. In target apps, read the root `AGENTS.md` and every child `AGENTS.md` on the path to files you expect to touch before editing non-trivial changes.

A target app should not copy all onlava docs. It should define only app-specific rules:

````md
# <app> Agent Instructions

Use the onlava skill for shared onlava behavior.

## App Roots
- onlava app root: `<path>`
- frontend root: `<path>`
- generated client: `<path>`

## Local Loop
```sh
onlava up --detach
onlava logs --follow
```

## Validation
```sh
onlava check --json
go test ./...
onlava harness --json --write
<frontend-test-command>
```

## App Invariants
- product/domain rule 1
- product/domain rule 2

## Environment
Required names only, no values:
- DatabaseURL
- <APP_ENV_NAME>
````

That small file gives agents what the reusable onlava skill cannot know: where the app lives, how frontends are wired, what generated files are committed, which validations matter, and which product invariants must be preserved.

## Is The Skill Enough For Client Apps?

No. The skill is necessary shared context, but it is intentionally generic.

Use three layers in client apps such as `github.com/pbrazdil/onlv`:

1. **Installable onlava skill** for the onlava app model, CLI, validation, and generated client workflow.
2. **App-local `AGENTS.md`** for app root, frontend roots, generated output paths, required environment names, test commands, UI conventions, product invariants, and deployment assumptions.
3. **Machine-readable onlava commands** for the current app shape: `onlava inspect ... --json`.

This avoids duplicating stale runtime documentation into every client app while still giving agents the local context they need.

## CLI Surfaces For Agents

Prefer JSON when output will feed another tool or decision.

| Intent | Command |
| --- | --- |
| Check local environment readiness | `onlava doctor --json` |
| Validate app model | `onlava check --json` |
| Inspect app/root/config | `onlava inspect app --json` |
| Inspect routes/endpoints/services | `onlava inspect routes --json`, `onlava inspect endpoints --json`, `onlava inspect services --json` |
| Inspect generated client/wire | `onlava inspect wire --json` |
| Inspect build/cache paths | `onlava inspect build --json`, `onlava inspect paths --json` |
| Inspect generator graph | `onlava inspect generators --json` |
| Inspect docs knowledge base | `onlava inspect docs --json` |
| Inspect CLI command manifest | `onlava help --json` |
| Inspect managed local tools | `onlava system toolchain list --json`, `onlava system toolchain verify --json` |
| Install managed local tools | `onlava system toolchain sync --json`, `onlava system toolchain sync --tool <name> --json`, or for Neon images `onlava system toolchain sync --tool neon-selfhost --images --json` |
| Inspect local HTTPS edge | `onlava system edge status --json` |
| Run app validation snapshot | `onlava harness --json --write` |
| Run app quality gate | `onlava validate quick --json --write`, `onlava validate changed --json --write`, or `onlava validate full --json --write` |
| Inspect app validation gates | `onlava inspect validation --json`, `onlava validate graph full --json` |
| Run repo validation snapshot | `onlava harness self --summary --write` |
| Follow logs | `onlava logs --jsonl --session current --limit 200` |
| Query logs | `onlava logs query --json --session current --query 'error OR panic'` |
| Inspect observability | `onlava inspect observability --json --session current` |
| Inspect traces/metrics | `onlava traces list --json --session current`, `onlava metrics list --json --session current` |
| Query metrics | `onlava metrics query --json --session current --promql 'onlava_request_duration_seconds'` |
| Generate TypeScript client | `onlava generate client --lang typescript --output <path>` |
| Run configured generation | `onlava generate --dry-run --json`, then `onlava generate` |
| Apply configured DB lifecycle | `onlava db apply --json` |
| Apply service seed data | `onlava db seed --json` |
| Setup local DB lifecycle | `onlava db setup --json` |
| Connect to managed Postgres | `onlava db psql` |
| Inspect Neon dev-cell state | `onlava db neon status --json` |
| Inspect Neon branch pin | `onlava db branch status --json` |
| Pin this worktree to a Neon branch | `onlava db branch checkout <name> --json` |
| Inspect local Neon lease registry | `onlava db branch list --json` |
| Create a code worktree with a Neon pin | `onlava worktree create <name> --json` |
| Human session status | `onlava ps` |
| Machine session status | `onlava ps --json` |
| Run repo-local task | `onlava task list`, `onlava task run <name>` |
| Run app-local code task | `onlava task list --json`, `onlava task run <domain>:<name> -- [args...]` |

When local dev fails because the host may be missing Go, disk space, memory, Docker engine readiness, or optional tools, run `onlava doctor --json` first. Stay on onlava command surfaces for ordinary app work. Use `onlava help --json` for machine-readable command discovery, `onlava help all` for the grouped human command reference, and `onlava ps --json` when session status will feed another tool. Inspect managed dnsmasq, Caddy, Grafana, Victoria, Temporal CLI, Postgres, Neon, or Electric details only when intentionally debugging the substrate. Shared substrate failures are visible in `onlava ps --json` under `substrates`, including exit metadata and stdout/stderr log paths. Managed Postgres substrate rows describe the reusable physical server only; per-session database URL/name values are exposed through session env. The first Neon slices expose generated dev-cell state, the shared bind-mounted storage root under the agent home, `.onlava/worktree-db.json` branch-pin inspection, and the local `branches.json` lease registry; `onlava up` can resolve/write that pin and can run only when a provider has marked the lease ready with endpoint metadata. Electric remains session-scoped and is published as a session backend, not a global substrate. Do not install global binaries as a hidden fix; use `onlava system edge dns install` for wildcard local DNS, `onlava system edge install` for Caddy, `onlava system toolchain sync --json` for managed app-root tools, set documented per-tool env overrides for tools that have them, or document the configured external service.

Use non-JSON output only for human inspection.

## Runtime Command Choice

- Use `onlava up` to run the app session and expose capabilities for local development, debugging, agents, dashboard, logs, traces, metrics, managed dev services, and frontend routing.
- Use `onlava up --detach` when the local agent should keep the dev session running.
- Use `onlava system edge dns install`, `onlava system edge privileged install`, `onlava system edge install`, and `onlava system edge trust` when the browser needs trusted wildcard local HTTPS on `127.0.0.1:443`; dnsmasq owns wildcard local DNS, the privileged helper owns that port, forwards raw TCP to user-owned Caddy, and the edge syncs managed dnsmasq/Caddy as needed. If an app explicitly configures `proxy.route_base_domain`, `onlava up` requires that edge path and fails loudly with DNS, privileged listener, Caddy, and router diagnostics instead of publishing internal `:9440` router URLs as user-facing session routes.
- Use `onlava logs --follow` to follow a current detached or agent session.
- Use `onlava down` to stop a session; add `--db`, `--state`, or `--all` only when destructive cleanup is intended.
- Use `onlava serve` for headless API-role execution. Do not expect dashboard, proxy, watch mode, or dev/admin endpoints.
- Use `onlava worker` for worker-role execution of native Temporal workers and cron.
- Use `onlava build` for a deployable binary artifact.
- Use `onlava generate` for configured file-producing generators. `onlava generate sqlc` is generated-source work only; it must not apply schema or seed data.
- Use `onlava db apply` to mutate schema/app database setup only. Use `onlava db seed` to apply service-local initial data only; changed previously-applied seeds and destructive seed SQL fail closed with path/line diagnostics. Use `onlava db setup` for the one-command local setup path: apply then seed. `onlava up` runs that setup lifecycle before app startup when DB setup inputs exist, and skips it on ordinary rebuilds until `database.apply` config or seed file hashes change.
- Use `onlava db neon status --json`, `onlava db neon start --json`, `onlava db neon stop --json`, `onlava db branch status --json`, and `onlava db branch list --json` for the current Neon contract slice: generated local dev-cell state with shared bind-mounted storage metadata, built-in `neon-selfhost-driver` status, backend project/branch/compute counts, Docker/image/container health probes, listener checks for running storage-cell components, generated Compose lifecycle, worktree branch-pin inspection, and Onlava-owned local lease registry inspection. Backend state and lease registry mutations are serialized by advisory `backend.lock` and `branches.lock` files under the Neon substrate root. The generated Compose project includes MinIO, bucket init, storage broker, pageserver, and three safekeepers; durable `/data` paths are bind-mounted under the shared agent-home `substrates/neon/data/` root, while branch compute endpoints are driver-owned and are not a static Compose service. `onlava db neon start --json` fails closed when existing Onlava Neon containers still expose Docker-managed `/data` volumes; the supported path is a fresh `uninstall --destroy-data`, install, and start, not migration. `onlava db neon install --json` records the built-in `onlava internal neon-selfhost-driver` in `cell.json.driver`; explicit `ONLAVA_DEV_NEON_SELFHOST_DRIVER` still wins, legacy `cell.json.driver.path` is honored, and `ONLAVA_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` remains the local Postgres-shaped development fallback. The built-in driver records backend metadata in `backend.json` using `projects[project].tenant_id` and project-local branch maps, migrates legacy top-level tenant/branch state on read, bootstraps pageserver tenant/timeline metadata for the selected project when the generated storage cell is reachable, starts or reuses a branch compute container named from project plus branch ID suffix on the persisted loopback port when Docker and compute templates are available, labels fresh compute containers with project, tenant ID, branch ID, and branch name, returns public `pending` while the endpoint is not SQL-ready, and returns `ready` only after `psql` verifies the Postgres endpoint and creates the requested database when missing. Checkout refuses to reuse matching foreign local leases. `delete` can remove pending local leases after the documented parent/current guards and delegates ready branch deletion to the configured driver; the built-in driver handles reset/restore by replacing pageserver timelines and branch compute in the selected project, delete as stateful project-local backend metadata plus compute removal, and `diff` for ready backend branches in the same project through schema-only `pg_dump`. `expire`, same-project `prune`, and selected-session `onlava down --db` update only Onlava-owned local registry metadata; `onlava down --state` removes the local worktree pin; and `onlava worktree create <name> --json` can create a Git worktree, write the target pin for Neon apps, run the branch-provider ensure boundary, and roll the worktree back if pin or ensure fails without allocating a per-worktree Neon data root. `onlava up`, `onlava db psql`, and Electric can consume a non-parent ready lease endpoint and fail explicitly while the lease is not ready or protected. Built-in selfhost branch compute creation, SQL readiness, reset/restore, delete, schema diff, and default self-harness coverage are implemented; still experimental are Electric slot/publication lifecycle hardening and release-grade driver distribution beyond the current built-in CLI plus image toolchain contract. The default `onlava harness self --json --write` path includes the real Docker-backed Neon proof; use `--quick` for the smaller non-Docker self-harness mode.
- Use `onlava task list`, `onlava task inspect <target>`, and `onlava task run <target>` for configured repo tasks and app-local code tasks. Configured tasks use plain names; code tasks use `<domain>:<name>`, and task arguments must appear after `--`.
- Use `onlava task run <name>` only for repo-local workflows that are not core onlava lifecycle commands.
- Use `onlava validate` for app-owned quality gates defined in `.onlava.json`. `onlava harness` remains the framework-owned app-model proof; `onlava validate quick|changed|full --json --write` runs app-specific tasks/profiles and writes `.onlava/harness/validation/latest.json`.

## Generated And Cache Artifacts

Agents should use command JSON as the integration surface:

```text
onlava inspect app --json
onlava inspect routes --json
onlava inspect services --json
onlava inspect endpoints --json
onlava inspect wire --json
onlava inspect build --json
onlava harness --json
onlava validate quick --json
onlava harness self --summary
```

Generated repo-local files may exist after inspect/build/harness commands produce them:

```text
<app-root>/.onlava/
  gen/
    app.json
    routes.json
    services.json
    endpoints.json
    wire/capabilities.json
    manifest.json
  build/latest.json
  harness/latest.json
  harness/validation/latest.json
```

Treat these files as internal cache or local snapshot artifacts. Do not read `.onlava/gen/*` directly unless debugging onlava generation.

Do not edit generated artifacts by hand. Regenerate them with onlava commands.

## TypeScript Client Integration

Use generated TypeScript clients when frontends or client apps need a typed route-aware API surface.

Recommended workflow:

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava generate client --lang typescript --output <frontend-or-package-path>/onlava-client.ts
```

Client apps should commit generated clients only if that is their established workflow. If committed, app-local `AGENTS.md` must state the output path and require regeneration after endpoint or wire changes.

Generated TypeScript `WithMeta` methods expose response headers, status, the raw `Response`, and parsed `txid` metadata from `X-Txid`/`X-TXID`. For Electric-backed mutations, keep the phases separate: first handle the successful API response as the committed mutation, then call `observeAPIResponseTxid(response, collection.utils.awaitTxId, context)` or an equivalent app-local observer. If the observer fails or times out, the generated client throws `SyncObservationError` with `kind: "sync_observation_failure"` and `mutation_committed: true`, so UI and agents do not report the committed mutation itself as rolled back.

### ONLV Electric Txid Validation

Use these notes when validating the ONLV task-creation txid case against a local onlava checkout:

```sh
cd /Users/petrbrazdil/Repos/onlv
onlava inspect app --json
onlava inspect routes --json
onlava inspect services --json
onlava system edge dns install
onlava system edge privileged install
onlava system edge install
onlava system edge trust
onlava up --detach
onlava logs --session current --jsonl --limit 200
```

Expected evidence:
- `onlava inspect routes --json` includes the task mutation route and `/sync/:table_name` routes.
- `onlava inspect services --json` includes the task service and standard auth services needed by Pulse.
- Creating a task through Pulse returns HTTP 2xx and `X-Txid`/`X-TXID` in the API response headers.
- If Electric/TanStack `awaitTxId` observes the txid, the UI clears the pending task state normally.
- If Electric observation times out or returns a substrate error such as Postgres lock acquisition timeout, the app reports a sync observation failure with txid, app/session, API URL, Electric URL or stream context, and observer error details; it must not say the API mutation failed or was rolled back.

Generated clients are the application-code integration surface. Agents should use CLI JSON and dashboard APIs for inspection and debugging.

## Environment

- List required environment names in docs; never include values.
- Do not add new onlava-owned production env vars unless the user explicitly asks for one or an active ExecPlan records the exception. Prefer `.onlava.json`, CLI flags, or checked-in manifests, and update `docs/environment.registry.json` when env is truly required.
- Process environment wins over local files.
- Local startup expects app-root `.env` for `onlava up`, local `onlava serve`, local `onlava task run`, and local `onlava worker`.
- `.env.local` is optional and overrides `.env` only when the parent process did not already define a key.
- `onlava serve --env production` can use process environment without a `.env` file.
- Secret-bearing files are not copied into build workspaces.

## Debugging Playbooks

Build or parse failure:

```sh
onlava check --json
onlava inspect app --json
onlava inspect paths --json
```

Route or endpoint mismatch:

```sh
onlava inspect routes --json
onlava inspect endpoints --json
```

Auth failure:

```sh
onlava inspect endpoints --json
onlava logs --jsonl --limit 200
```

Slow or failing request:

```sh
onlava inspect observability --json --session current
onlava logs query --json --session current --since 15m --query 'error OR panic'
onlava traces list --json --session current --since 15m --slowest
onlava metrics list --json --session current --since 1h
onlava metrics query --json --session current --since 15m --step 5s --promql 'onlava_request_duration_seconds'
```

Generated client mismatch:

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava generate client --lang typescript --output <expected-output>
```

Dashboard UI change:

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
onlava harness ui --json --write
```

## Keeping Agent Docs Fresh

When adding or changing an agent-facing behavior, include one of these updates in the same PR:

- Update `SKILL.md` when an app agent needs to know the behavior.
- Update `AGENTS.md` when an onlava-repo agent needs to obey the behavior.
- Update this guide when the behavior affects client-app integration or cross-repo agent workflows.
- Update `docs/local-contract.md` when the behavior affects CLI grammar, JSON schemas, artifact paths, or stability status.
- Update `docs/knowledge.json` when adding or materially changing indexed docs.

Prefer deleting stale duplicated details over adding another copy. Link to the contract where exact grammar matters.
