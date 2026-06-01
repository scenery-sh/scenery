# onlava Agent Guide

This guide is for AI agents using onlava or changing onlava. It explains how to combine repo-local instructions, the installable skill, CLI JSON, MCP, generated artifacts, and app-local instructions.

For exact command grammar and schemas, use `docs/local-contract.md`. For app recipes, use `docs/app-development-cookbook.md`. For onlava repo edits, `AGENTS.md` is the first file to read.

## Source Of Truth Order

Use this order when instructions overlap:

1. Current implementation and tests.
2. Machine-readable CLI output: `onlava inspect ... --json`, `onlava check --json`, `onlava logs --jsonl`, and `onlava harness ... --json`.
3. JSON schemas in `docs/schemas/`.
4. `docs/local-contract.md`.
5. This guide.
6. `SKILL.md`.
7. README and historical PRDs.

If old prose disagrees with current JSON or tests, fix the drift. Do not add legacy aliases unless an active plan explicitly requires compatibility.

## Agent Fast Path

Inside an onlava app:

```sh
onlava check --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava inspect wire --json
```

During local debugging:

```sh
onlava dev
onlava logs --session current --jsonl --limit 200
onlava inspect traces --json --session current --since 15m --slowest
onlava inspect metrics --json --session current --since 1h
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
- Agent workflows and MCP: `docs/agent-guide.md`, `AGENTS.md`, `SKILL.md`.
- Human overview: `README.md`.
- App recipes: `docs/app-development-cookbook.md`.
- Environment variables: `docs/environment.md`.
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
onlava dev --detach
onlava attach
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

1. **Installable onlava skill** for the onlava app model, CLI, MCP, validation, and generated client workflow.
2. **App-local `AGENTS.md`** for app root, frontend roots, generated output paths, required environment names, test commands, UI conventions, product invariants, and deployment assumptions.
3. **Machine-readable onlava artifacts** for the current app shape: `onlava inspect ... --json` and `.onlava/gen/*.json`.

This avoids duplicating stale runtime documentation into every client app while still giving agents the local context they need.

## CLI Surfaces For Agents

Prefer JSON when output will feed another tool or decision.

| Intent | Command |
| --- | --- |
| Validate app model | `onlava check --json` |
| Inspect app/root/config | `onlava inspect app --json` |
| Inspect routes/endpoints/services | `onlava inspect routes --json`, `onlava inspect endpoints --json`, `onlava inspect services --json` |
| Inspect generated client/wire | `onlava inspect wire --json` |
| Inspect build/cache paths | `onlava inspect build --json`, `onlava inspect paths --json` |
| Inspect generator graph | `onlava inspect generators --json` |
| Inspect docs knowledge base | `onlava inspect docs --json` |
| Run app validation snapshot | `onlava harness --json --write` |
| Run repo validation snapshot | `onlava harness self --json --write` |
| Follow logs | `onlava logs --jsonl --session current --limit 200` |
| Inspect traces/metrics | `onlava inspect traces --json --session current`, `onlava inspect metrics --json --session current` |
| Generate TypeScript client | `onlava gen client --lang typescript --output <path>` |
| Run configured generation | `onlava generate --dry-run --json`, then `onlava generate` |
| Sync configured dev DB | `onlava db sync` |
| Run repo-local task | `onlava task list`, `onlava task run <name>` |
| Connect to managed Postgres | `onlava db psql` |

Use non-JSON output only for human inspection.

## Runtime Command Choice

- Use `onlava dev` for local development, debugging, agent sessions, dashboard, MCP, logs, traces, metrics, managed dev services, and frontend routing.
- Use `onlava dev --detach` when the local agent should keep the dev session running.
- Use `onlava attach` to follow a current detached or agent session.
- Use `onlava down` to stop a session; add `--db`, `--state`, or `--all` only when destructive cleanup is intended.
- Use `onlava run` for headless API-role execution. Do not expect dashboard, MCP, proxy, watch mode, or dev/admin endpoints.
- Use `onlava worker` for worker-role execution of native Temporal workers and cron.
- Use `onlava build` for a deployable binary artifact.
- Use `onlava generate` for configured file-producing generators. It is separate from `onlava db sync`, which can mutate the configured development database before refreshing dependent SQLC artifacts.
- Use `onlava task run <name>` only for repo-local workflows that are not core onlava lifecycle commands.

## MCP For Agents

`onlava dev` exposes a development MCP server using SSE. The startup banner prints the `MCP SSE URL`. With the local agent router active, session manifests also expose a session-scoped MCP route.

Use MCP for interactive local app inspection through the dev runtime. It is useful when a dev session is already running and the agent host supports MCP.

Current tool surface from `cmd/onlava/mcp.go`:

| Tool | Use |
| --- | --- |
| `get_services` | List services and endpoints from dashboard metadata. |
| `get_middleware` | List registered middleware. |
| `get_auth_handlers` | List auth handler metadata. |
| `get_cronjobs` | List cron jobs from metadata. |
| `call_endpoint` | Invoke an onlava endpoint through the local dev runtime. |
| `get_traces` | List recent trace summaries. |
| `get_trace_spans` | Fetch spans/events for trace IDs. |
| `get_databases` | Describe the discovered PostgreSQL database with redacted URL. |
| `query_database` | Run SQL against the discovered development database. |
| `get_metadata` | Return the dashboard metadata snapshot. |
| `get_src_files` | Read selected source files from the active app root. |
| `get_secrets` | List referenced environment names and availability. |

Unsupported compatibility stubs may appear for storage, cache, metrics, and docs. Treat a `{ "supported": false }` tool result as a hard no-op, not as a failure of the app.

Use CLI JSON rather than MCP for CI, release gates, code review evidence, and durable artifacts. MCP is a session convenience surface; CLI JSON and schemas are the stable contract.

## Generated Artifacts

Agents can read generated repo-local files after inspect/build/harness commands produce them:

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

These mirror `onlava inspect ... --json` outputs and are useful when an agent needs stable snapshots without scraping console output.

Do not edit generated artifacts by hand. Regenerate them with onlava commands.

## TypeScript Client Integration

Use generated TypeScript clients when frontends or client apps need a typed route-aware API surface.

Recommended workflow:

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava gen client --lang typescript --output <frontend-or-package-path>/onlava-client.ts
```

Client apps should commit generated clients only if that is their established workflow. If committed, app-local `AGENTS.md` must state the output path and require regeneration after endpoint or wire changes.

MCP is not a replacement for generated clients. MCP is for agents and development tooling; generated clients are for application code.

## Environment

- List required environment names in docs; never include values.
- Process environment wins over local files.
- Local startup expects app-root `.env` for `onlava dev`, local `onlava run`, and local `onlava worker`.
- `.env.local` is optional and overrides `.env` only when the parent process did not already define a key.
- `onlava run --env production` can use process environment without a `.env` file.
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
onlava inspect traces --json --session current --since 15m --slowest
onlava inspect metrics --json --session current --since 1h
```

Generated client mismatch:

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava gen client --lang typescript --output <expected-output>
```

Dashboard UI change:

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
onlava harness ui --json
```

## Keeping Agent Docs Fresh

When adding or changing an agent-facing behavior, include one of these updates in the same PR:

- Update `SKILL.md` when an app agent needs to know the behavior.
- Update `AGENTS.md` when an onlava-repo agent needs to obey the behavior.
- Update this guide when the behavior affects MCP, client-app integration, or cross-repo agent workflows.
- Update `docs/local-contract.md` when the behavior affects CLI grammar, JSON schemas, artifact paths, or stability status.
- Update `docs/knowledge.json` when adding or materially changing indexed docs.

Prefer deleting stale duplicated details over adding another copy. Link to the contract where exact grammar matters.
