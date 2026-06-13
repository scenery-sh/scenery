# scenery Agent Guide

This guide is for AI agents using scenery or changing scenery. It explains how to combine repo-local instructions, the installable skill, CLI JSON, scenery capabilities, and app-local instructions.

For exact command grammar and schemas, use `docs/local-contract.md`. For app recipes, use `docs/app-development-cookbook.md`. For scenery repo edits, `AGENTS.md` is the first file to read.

## Source Of Truth Order

Use this order when instructions overlap:

1. Current implementation and tests.
2. Machine-readable CLI output: `scenery inspect ... --json`, `scenery check --json`, `scenery logs --jsonl`, scoped observability query commands, and `scenery harness ... --json`.
3. JSON schemas in `docs/schemas/`.
4. `docs/local-contract.md`.
5. This guide.
6. `SKILL.md`.
7. README and completed ExecPlans.

If old prose disagrees with current JSON or tests, fix the drift. Do not add legacy aliases unless an active plan explicitly requires compatibility.

## Agent Fast Path

Inside a Scenery app:

```sh
scenery doctor --json
scenery check --json
scenery inspect app --json
scenery inspect routes --json
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery inspect wire --json
scenery system toolchain verify --json
```

During local debugging:

```sh
scenery up
scenery inspect observability --json
scenery logs --jsonl --limit 200
scenery logs query --json --since 15m --query 'error OR panic'
scenery traces list --json --since 15m --slowest
scenery metrics list --json --since 1h
scenery metrics query --json --since 15m --step 5s --promql 'scenery_request_duration_seconds'
```

Before finishing app work:

```sh
scenery check --json
go test ./...
scenery harness --json --write
scenery validate quick --json --write
```

Before finishing scenery repo work:

```sh
go test ./...
go test ./cmd/scenery
scenery harness self --summary --write
```

The self-harness Go test steps use the Go test result cache by default; add
`--fresh-tests` only when a no-result-cache `-count=1` run is required.

Do not run `go install ./cmd/scenery` during agent validation unless a human
explicitly asks. Multiple worktrees share the installed `scenery` binary; the
self-harness builds `.scenery/harness/bin/scenery` inside the current worktree for
freshness checks.

When a command cannot be run, report the exact command and environmental reason.

## Working In The scenery Repo

Read:

```text
AGENTS.md
docs/local-contract.md
docs/agent-guide.md
docs/plans/active.md
docs/tech-debt.md
```

Use `scenery inspect docs --json` when the binary is available. It reports indexed docs, missing docs, stale docs, plan paths, discovered `AGENTS.md` scopes, and Child Agent Index drift.

Keep these layers synchronized when behavior changes:

- CLI grammar and JSON contracts: `docs/local-contract.md`, schemas, tests.
- Agent workflows: `docs/agent-guide.md`, `AGENTS.md`, `SKILL.md`.
- Human overview: `README.md`.
- App recipes: `docs/app-development-cookbook.md`.
- Environment variables: `docs/environment.md` for humans and `docs/environment.registry.json` for the self-harness contract.
- Indexed docs metadata: `docs/knowledge.json`.

## Working In A Scenery App

Use app-local `AGENTS.md` first, then this skill/guide. In target apps, read the root `AGENTS.md` and every child `AGENTS.md` on the path to files you expect to touch before editing non-trivial changes.

A target app should not copy all scenery docs. It should define only app-specific rules:

````md
# <app> Agent Instructions

Use the scenery skill for shared scenery behavior.

## App Roots
- scenery app root: `<path>`
- frontend root: `<path>`
- generated client: `<path>`

## Local Loop
```sh
scenery up --detach
scenery logs --follow
```

## Validation
```sh
scenery check --json
go test ./...
scenery harness --json --write
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

That small file gives agents what the reusable scenery skill cannot know: where the app lives, how frontends are wired, what generated files are committed, which validations matter, and which product invariants must be preserved.

## Is The Skill Enough For Client Apps?

No. The skill is necessary shared context, but it is intentionally generic.

Use three layers in client apps such as `github.com/pbrazdil/onlv`:

1. **Installable scenery skill** for the scenery app model, CLI, validation, and generated client workflow.
2. **App-local `AGENTS.md`** for app root, frontend roots, generated output paths, required environment names, app-required build flags, test commands, UI conventions, product invariants, and deployment assumptions.
3. **Machine-readable scenery commands** for the current app shape: `scenery inspect ... --json`.

This avoids duplicating stale runtime documentation into every client app while still giving agents the local context they need.

When an app needs Go build tags or other app-owned build-time flags, prefer `.scenery.json` `build.go_flags` such as `["-tags=roofmapnet_native"]` over asking every agent to export `GOFLAGS` before `scenery up`, `scenery check`, or `scenery test`.

## CLI Surfaces For Agents

Prefer JSON when output will feed another tool or decision.

| Intent | Command |
| --- | --- |
| Check local environment readiness | `scenery doctor --json` |
| Validate app model | `scenery check --json` |
| Inspect app/root/config | `scenery inspect app --json` |
| Inspect routes/endpoints/services | `scenery inspect routes --json`, `scenery inspect endpoints --json`, `scenery inspect services --json` |
| Inspect static model/view IR | `scenery inspect models --json`, `scenery inspect views --json` |
| Inspect generated client/wire | `scenery inspect wire --json` |
| Inspect build/cache paths | `scenery inspect build --json`, `scenery inspect paths --json` |
| Inspect generator graph | `scenery inspect generators --json` |
| Inspect docs knowledge base | `scenery inspect docs --json` |
| Inspect CLI command manifest | `scenery help --json` |
| Inspect managed local tools | `scenery system toolchain list --json`, `scenery system toolchain verify --json` |
| Install managed local tools | `scenery system toolchain sync --json` or `scenery system toolchain sync --tool <name> --json` |
| Inspect local HTTPS edge | `scenery system edge status --json` |
| Run app validation snapshot | `scenery harness --json --write` |
| Run app quality gate | `scenery validate quick --json --write`, `scenery validate changed --json --write`, or `scenery validate full --json --write` |
| Inspect app validation gates | `scenery inspect validation --json`, `scenery validate graph full --json` |
| Run repo validation snapshot | `scenery harness self --summary --write` |
| Follow logs | `scenery logs --jsonl --limit 200` |
| Query logs | `scenery logs query --json --query 'error OR panic'` |
| Inspect observability | `scenery inspect observability --json` |
| Inspect traces/metrics | `scenery traces list --json`, `scenery metrics list --json` |
| Query metrics | `scenery metrics query --json --promql 'scenery_request_duration_seconds'` |
| Generate TypeScript client | `scenery generate client --lang typescript --output <path>` |
| Run configured generation | `scenery generate --dry-run --json`, then `scenery generate` |
| Generate desired model schema | `scenery generate data --dry-run --json` |
| Check generated schema drift | `scenery db diff --generated --json` |
| Apply configured DB lifecycle | `scenery db apply --json` |
| Apply service seed data | `scenery db seed --json` |
| Setup local DB lifecycle | `scenery db setup --json` |
| Connect to managed Postgres | `scenery db psql` |
| Inspect Postgres branch pin | `scenery db branch status --json` |
| Pin this worktree to a Postgres branch | `scenery db branch checkout <name> --json` |
| Inspect local Postgres branch leases | `scenery db branch list --json` |
| Create a code worktree with a Postgres branch pin | `scenery worktree create <name> --json` |
| Human dev runtime status | `scenery ps` |
| Machine dev runtime status | `scenery ps --json` |
| Run repo-local task | `scenery task list`, `scenery task run <name>` |
| Run app-local code task | `scenery task list --json`, `scenery task run <domain>:<name> -- [args...]` |

Generated model CRUD endpoints are beta. They appear in `scenery inspect endpoints --json`
and `scenery inspect routes --json` with `"generated": true`; generated stores
use the app database selected by the configured app database URL env, defaulting
to `DatabaseURL`, or Scenery's managed database env.
Generated CRUD endpoints default to `auth` for every action; the beta DSL has no
implicit public read or public mutation surface.
Generated CRUD route bases are service-scoped as `/<service>/<table>` so `model.Table(...)`
remains a database-table decision rather than a public route shortcut, and generated
routes fail `scenery check` when they collide with handwritten or generated routes.
Typed `model.Seed(...)` rows generate `.scenery/gen/db/<service>/seed.sql` and
are consumed by `scenery db seed`. Configured frontends with static collection
pages receive beta generated packages under `.scenery/gen/web/<frontend>/` with
runtime adapter factories and route registration helpers for app-owned
Electric/TanStack/layout-kit wiring.
Tenant-shaped generated CRUD uses the convention `TenantID`/`tenant_id` field:
generated endpoints are auth-only, generated SQL is scoped to the active standard-auth
tenant, and create/patch payload types do not expose `tenant_id`. Tenant fields
may be `string`, a named string type, or `github.com/google/uuid.UUID`; other
tenant field types fail parse/check with an explicit diagnostic.

When local dev fails because the host may be missing Go, disk space, memory, Docker engine readiness, or optional tools, run `scenery doctor --json` first. Stay on scenery command surfaces for ordinary app work. Use `scenery help --json` for machine-readable command discovery, `scenery help all` for the grouped human command reference, and `scenery ps --json` when dev runtime status will feed another tool. Inspect managed dnsmasq, Caddy, Grafana, Victoria, Temporal CLI, Postgres, or Electric details only when intentionally debugging the substrate. Shared substrate failures are visible in `scenery ps --json` under `substrates`, including exit metadata and stdout/stderr log paths. Managed Postgres substrate rows describe the reusable physical server only; per-runtime database URL/name values are exposed through the dev runtime environment. Postgres branch leases live under the agent Postgres state root, and `.scenery/worktree-db.json` pins the current app root or worktree to a branch database. Electric remains scoped to the dev runtime and is published as an internal backend, not a global substrate. Do not install global binaries as a hidden fix; use `scenery system edge dns install` for wildcard local DNS, `scenery system edge install` for Caddy, `scenery system toolchain sync --json` for managed app-root tools, set documented per-tool env overrides for tools that have them, or document the configured external service.

Use non-JSON output only for human inspection.

## Runtime Command Choice

- Use `scenery up` to run the app root's one live dev runtime and expose capabilities for local development, debugging, agents, dashboard, logs, traces, metrics, managed dev services, and frontend routing. Use a Git worktree for another live code copy.
- During a watcher rebuild restart, the runtime drains in-flight streaming raw responses (SSE/long-poll) by canceling their request contexts so they end with a clean terminator, and the agent router answers requests for a restarting backend with `503` plus `Retry-After: 1` instead of `502`; clients should treat that as a brief retryable window.
- Use `scenery up --detach` when the local agent should keep that dev runtime running in the background.
- Use `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, and `scenery system edge trust` when the browser needs trusted wildcard local HTTPS on `127.0.0.1:443`; dnsmasq owns wildcard local DNS, the privileged helper owns that port, forwards raw TCP to user-owned Caddy, and the edge syncs managed dnsmasq/Caddy as needed. If an app explicitly configures `proxy.route_base_domain`, `scenery up` requires that edge path and fails loudly with DNS, privileged listener, Caddy, and router diagnostics instead of publishing internal `:9440` router URLs as user-facing session routes.
- Use `scenery logs --follow` to follow the current app root's detached or agent-backed runtime.
- Use `scenery down` to stop the current app root's dev runtime; add `--db`, `--state`, or `--all` only when destructive cleanup is intended.
- Use `scenery serve` for headless API-role execution. Do not expect dashboard, proxy, watch mode, or dev/admin endpoints.
- Use `scenery worker` for worker-role execution of native Temporal workers and cron.
- Use `scenery build` for a deployable binary artifact.
- Use `scenery generate` for configured file-producing generators. `scenery generate sqlc` is generated-source work only; it must not apply schema or seed data. `scenery generate data --dry-run --json` writes desired static-model Atlas HCL under `.scenery/gen/db/<service>/schema.hcl`, seed SQL under `.scenery/gen/db/<service>/seed.sql`, and beta generated frontend packages under `.scenery/gen/web/<frontend>/` without mutating databases. Generated model DB artifacts use the app-owned `<service>` schema and schema-qualified tables consistently across HCL, seed SQL, CRUD SQL, and Electric shape metadata; generated Atlas resource labels are also schema-qualified so app-owned schemas can coexist with handwritten multi-schema HCL. Use `scenery db diff --generated --json` to compare generated schema with app-owned `SERVICE/db/schema.hcl`.
- Use `scenery db apply` to mutate schema/app database setup only. Use `scenery db seed` to apply service-local initial data only; changed previously-applied seeds and destructive seed SQL fail closed with path/line diagnostics. Use `scenery db setup` for the one-command local setup path: apply then seed. `scenery up` runs that setup lifecycle before app startup when DB setup inputs exist, and skips it on ordinary rebuilds until `database.apply` config or seed file hashes change.
- Use `scenery db postgres status --json`, `scenery db postgres start --json`, `scenery db postgres stop --json`, `scenery db branch status --json`, and `scenery db branch list --json` for managed Postgres branch work. Branch pins live at `.scenery/worktree-db.json`; Scenery-owned local branch leases live in `branches.json` under the agent Postgres state root. The phase-one branch provider supports local database isolation through `branch_strategy: "template_database"`: checkout creates or reuses a branch database from the parent template database and records a ready endpoint without persisting raw connection URLs. `reset` recreates the branch from the parent template, `delete` drops the branch database and removes its lease, `expire` updates lease metadata, `prune` removes expired non-current branch databases when the Postgres admin substrate is reachable, and `restore` currently maps to template reset. `scenery up`, `scenery db psql`, DB setup, and Electric consume ready branch endpoints and fail explicitly when the lease is missing, expired, protected, or endpoint-less. The default `scenery harness self --json --write` path includes the live Postgres branch lifecycle proof; use `--quick` for the smaller self-harness mode.
- Use `scenery task list`, `scenery task inspect <target>`, and `scenery task run <target>` for configured repo tasks and app-local code tasks. Configured tasks use plain names; code tasks use `<domain>:<name>`, and task arguments must appear after `--`.
- Use `scenery task run <name>` only for repo-local workflows that are not core scenery lifecycle commands.
- Use `scenery validate` for app-owned quality gates defined in `.scenery.json`. `scenery harness` remains the framework-owned app-model proof; `scenery validate quick|changed|full --json --write` runs app-specific tasks/profiles and writes `.scenery/harness/validation/latest.json`.

## Generated And Cache Artifacts

Agents should use command JSON as the integration surface:

```text
scenery inspect app --json
scenery inspect routes --json
scenery inspect services --json
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery inspect wire --json
scenery inspect build --json
scenery harness --json
scenery validate quick --json
scenery harness self --summary
```

Generated repo-local files may exist after inspect/build/harness commands produce them:

- `.scenery/gen/models.json` and `.scenery/gen/views.json` cache the static model/page IR consumed by `scenery inspect models|views --json`. `.scenery/gen/db/<service>/schema.hcl` and `.scenery/gen/db/<service>/seed.sql` are disposable generated data artifacts. `.scenery/gen/web/<frontend>/` is the beta hidden generated package for static model/view frontends; app frontends should consume it through a local TypeScript alias such as `@scenery/generated` and provide `@scenery/layout-kit` plus declared slot components. The generated package exports typed rows, Electric shape definitions, collection descriptors, runtime adapter factories, route factories, and `registerGeneratedRoutes` helpers; app code still owns the production router, Electric client, TanStack DB instance, and layout-kit implementation.

```text
<app-root>/.scenery/
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

Treat these files as internal cache or local snapshot artifacts. Do not read `.scenery/gen/*` directly unless debugging scenery generation.

Do not edit generated artifacts by hand. Regenerate them with scenery commands.

## TypeScript Client Integration

Use generated TypeScript clients when frontends or client apps need a typed route-aware API surface.

Recommended workflow:

```sh
scenery inspect endpoints --json
scenery inspect wire --json
scenery generate client --lang typescript --output <frontend-or-package-path>/scenery-client.ts
```

Client apps should commit generated clients only if that is their established workflow. If committed, app-local `AGENTS.md` must state the output path and require regeneration after endpoint or wire changes.

Generated TypeScript `WithMeta` methods expose response headers, status, the raw `Response`, and parsed `txid` metadata from `X-Txid`/`X-TXID`. For Electric-backed mutations, keep the phases separate: first handle the successful API response as the committed mutation, then call `observeAPIResponseTxid(response, collection.utils.awaitTxId, context)` or an equivalent app-local observer. If the observer fails or times out, the generated client throws `SyncObservationError` with `kind: "sync_observation_failure"` and `mutation_committed: true`, so UI and agents do not report the committed mutation itself as rolled back.

### ONLV Electric Txid Validation

Use these notes when validating the ONLV task-creation txid case against a local scenery checkout:

```sh
cd /Users/petrbrazdil/Repos/onlv
scenery inspect app --json
scenery inspect routes --json
scenery inspect services --json
scenery system edge dns install
scenery system edge privileged install
scenery system edge install
scenery system edge trust
scenery up --detach
scenery logs --jsonl --limit 200
```

Expected evidence:
- `scenery inspect routes --json` includes the task mutation route and `/sync/:table_name` routes.
- `scenery inspect services --json` includes the task service and standard auth services needed by Pulse.
- Creating a task through Pulse returns HTTP 2xx and `X-Txid`/`X-TXID` in the API response headers.
- If Electric/TanStack `awaitTxId` observes the txid, the UI clears the pending task state normally.
- If Electric observation times out or returns a substrate error such as Postgres lock acquisition timeout, the app reports a sync observation failure with txid, app/session, API URL, Electric URL or stream context, and observer error details; it must not say the API mutation failed or was rolled back.

Generated clients are the application-code integration surface. Agents should use CLI JSON and dashboard APIs for inspection and debugging.

## Environment

- List required environment names in docs; never include values.
- Do not add new scenery-owned production env vars unless the user explicitly asks for one or an active ExecPlan records the exception. Prefer `.scenery.json`, CLI flags, or checked-in manifests, and update `docs/environment.registry.json` when env is truly required.
- Process environment wins over local files.
- Local startup expects app-root `.env` for `scenery up`, local `scenery serve`, local `scenery task run`, and local `scenery worker`.
- `.env.local` is optional and overrides `.env` only when the parent process did not already define a key.
- `scenery serve --env production` can use process environment without a `.env` file.
- Secret-bearing files are not copied into build workspaces.

## Debugging Playbooks

Build or parse failure:

```sh
scenery check --json
scenery inspect app --json
scenery inspect paths --json
```

Route or endpoint mismatch:

```sh
scenery inspect routes --json
scenery inspect endpoints --json
```

Auth failure:

```sh
scenery inspect endpoints --json
scenery logs --jsonl --limit 200
```

Slow or failing request:

```sh
scenery inspect observability --json
scenery logs query --json --since 15m --query 'error OR panic'
scenery traces list --json --since 15m --slowest
scenery metrics list --json --since 1h
scenery metrics query --json --since 15m --step 5s --promql 'scenery_request_duration_seconds'
```

Generated client mismatch:

```sh
scenery inspect endpoints --json
scenery inspect wire --json
scenery generate client --lang typescript --output <expected-output>
```

Dashboard UI change:

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
scenery harness ui --json --write
```

## Keeping Agent Docs Fresh

When adding or changing an agent-facing behavior, include one of these updates in the same PR:

- Update `SKILL.md` when an app agent needs to know the behavior.
- Update `AGENTS.md` when a Scenery-repo agent needs to obey the behavior.
- Update this guide when the behavior affects client-app integration or cross-repo agent workflows.
- Update `docs/local-contract.md` when the behavior affects CLI grammar, JSON schemas, artifact paths, or stability status.
- Update `docs/knowledge.json` when adding or materially changing indexed docs.

Prefer deleting stale duplicated details over adding another copy. Link to the contract where exact grammar matters.
