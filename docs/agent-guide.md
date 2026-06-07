# onlava Agent Guide

This guide is for AI agents using onlava or changing onlava. It explains how to combine repo-local instructions, the installable skill, CLI JSON, onlava capabilities, and app-local instructions.

For exact command grammar and schemas, use `docs/local-contract.md`. For app recipes, use `docs/app-development-cookbook.md`. For onlava repo edits, `AGENTS.md` is the first file to read.

## Source Of Truth Order

Use this order when instructions overlap:

1. Current implementation and tests.
2. Machine-readable CLI output: `onlava inspect ... --json`, `onlava check --json`, `onlava logs --jsonl`, and `onlava harness ... --json`.
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
onlava logs --session current --jsonl --limit 200
onlava traces list --json --session current --since 15m --slowest
onlava metrics list --json --session current --since 1h
```

Before finishing app work:

```sh
onlava check --json
go test ./...
onlava harness --json --write
```

Before finishing onlava repo work:

```sh
go test ./...
go install ./cmd/onlava
onlava system toolchain verify --json
onlava harness self --json --write
```

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

Use app-local `AGENTS.md` first, then this skill/guide.

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
| Inspect managed local tools | `onlava system toolchain list --json`, `onlava system toolchain verify --json` |
| Install managed local tools | `onlava system toolchain sync --json` or `onlava system toolchain sync --tool <name> --json` |
| Inspect local HTTPS edge | `onlava system edge status --json` |
| Run app validation snapshot | `onlava harness --json --write` |
| Run repo validation snapshot | `onlava harness self --json --write` |
| Follow logs | `onlava logs --jsonl --session current --limit 200` |
| Inspect traces/metrics | `onlava traces list --json --session current`, `onlava metrics list --json --session current` |
| Generate TypeScript client | `onlava generate client --lang typescript --output <path>` |
| Run configured generation | `onlava generate --dry-run --json`, then `onlava generate` |
| Apply configured DB lifecycle | `onlava db apply --json` |
| Apply service seed data | `onlava db seed --json` |
| Setup local DB lifecycle | `onlava db setup --json` |
| Connect to managed Postgres | `onlava db psql` |
| Run repo-local task | `onlava task list`, `onlava task run <name>` |
| Run app-local code task | `onlava task list --json`, `onlava task run <domain>:<name> -- [args...]` |

When local dev fails because the host may be missing Go, disk space, memory, or optional tools, run `onlava doctor --json` first. Stay on onlava command surfaces for ordinary app work. Inspect managed dnsmasq, Caddy, Grafana, Victoria, Temporal CLI, Postgres, or Electric details only when intentionally debugging the substrate. Shared substrate failures are visible in `onlava ps --json` under `substrates`, including exit metadata and stdout/stderr log paths. Managed Postgres substrate rows describe the reusable physical server only; per-session database URL/name values are exposed through session env. Electric remains session-scoped and is published as a session backend, not a global substrate. Do not install global binaries as a hidden fix; use `onlava system edge dns install` for wildcard local DNS, `onlava system edge install` for Caddy, `onlava system toolchain sync --json` for managed app-root tools, set documented per-tool env overrides for tools that have them, or document the configured external service.

Use non-JSON output only for human inspection.

## Runtime Command Choice

- Use `onlava up` to run the app session and expose capabilities for local development, debugging, agents, dashboard, logs, traces, metrics, managed dev services, and frontend routing.
- Use `onlava up --detach` when the local agent should keep the dev session running.
- Use `onlava system edge dns install`, `onlava system edge privileged install`, `onlava system edge install`, and `onlava system edge trust` when the browser needs trusted wildcard local HTTPS on `127.0.0.1:443`; dnsmasq owns wildcard local DNS, the privileged helper owns that port, forwards raw TCP to user-owned Caddy, and the edge syncs managed dnsmasq/Caddy as needed.
- Use `onlava logs --follow` to follow a current detached or agent session.
- Use `onlava down` to stop a session; add `--db`, `--state`, or `--all` only when destructive cleanup is intended.
- Use `onlava serve` for headless API-role execution. Do not expect dashboard, proxy, watch mode, or dev/admin endpoints.
- Use `onlava worker` for worker-role execution of native Temporal workers and cron.
- Use `onlava build` for a deployable binary artifact.
- Use `onlava generate` for configured file-producing generators. `onlava generate sqlc` is generated-source work only; it must not apply schema or seed data.
- Use `onlava db apply` to mutate schema/app database setup only. Use `onlava db seed` to apply service-local initial data only; changed previously-applied seeds and destructive seed SQL fail closed with path/line diagnostics. Use `onlava db setup` for the one-command local setup path: apply then seed. `onlava up` runs that setup lifecycle before app startup when DB setup inputs exist, and skips it on ordinary rebuilds until `database.apply` config or seed file hashes change.
- Use `onlava task list`, `onlava task inspect <target>`, and `onlava task run <target>` for configured repo tasks and app-local code tasks. Configured tasks use plain names; code tasks use `<domain>:<name>`, and task arguments must appear after `--`.
- Use `onlava task run <name>` only for repo-local workflows that are not core onlava lifecycle commands.

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
onlava harness self --json
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
onlava traces list --json --session current --since 15m --slowest
onlava metrics list --json --session current --since 1h
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
