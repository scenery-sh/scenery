# onlava Agent Instructions

This file is the repo-local operating manual for AI agents changing `github.com/pbrazdil/onlava`.

Optimize for agents: prefer concise rules, exact commands, and machine-readable contracts over long prose.

## Core Model

- Onlava runs my app session.
- Onlava gives me capabilities.
- Onlava lets agents inspect and act safely.
- Onlava hides the substrate unless I intentionally debug the substrate.

## Instruction Layers

Use the narrowest current source of truth that applies:

1. `AGENTS.md` gives repo-local rules for changing onlava itself.
2. `SKILL.md` is the installable skill for agents working inside any onlava app.
3. `docs/agent-guide.md` explains agent workflows, generated artifacts, and client-app integration.
4. `docs/local-contract.md` is the contract for CLI grammar, JSON schemas, artifact paths, and stability labels.
5. `docs/app-development-cookbook.md` gives practical app-building recipes.
6. `onlava inspect ... --json`, schemas under `docs/schemas/`, and harness command outputs are stronger than old prose when they disagree. Generated files under `.onlava/gen/` are cache, not an API.

When implementation and docs disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift, owner, and intended resolution path.

## Agent skills

### Issue tracker

Issues and PRDs for this repo live in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the default five-role triage label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

Use a multi-context domain docs layout with root `CONTEXT-MAP.md` plus per-context `CONTEXT.md` files. See `docs/agents/domain.md`.

## Current Mental Model

onlava is a Go-native service runtime and local development platform. Think in app sessions and capability surfaces first; Grafana, Victoria, Temporal dev server, local proxying, generated cache files, hidden ports, and local stores are substrate details unless the task is explicitly debugging that substrate.

- App roots are marked by `.onlava.json`.
- Go source is the app model: services, endpoints, auth handlers, middleware, Temporal declarations, cron jobs, and generated clients are discovered from code.
- `onlava serve` builds once and starts a headless API-role runtime.
- `onlava task run <domain>:<name> -- [args...]` runs an app-local code task.
- `onlava worker` builds once and starts a worker-role runtime for cron and native Temporal workers.
- `onlava up` starts the app session: supervised app process, file watching, dashboard, API explorer, logs, traces, metrics, managed dev services, and optional frontend routing.
- Public and auth endpoints are externally reachable. Private endpoints are internal-only and must be called through generated helpers.
- Typed endpoints decode path/query/header/cookie/body inputs into Go values and encode typed responses.
- Generated internal calls preserve route, private access, auth context, tracing, and error semantics.

Do not revive deprecated non-onlava APIs, legacy directive spellings, or compatibility aliases unless an active plan explicitly requires compatibility.

## Engineering Rules

- Prefer the Go standard library. Add dependencies only when the payoff is clear and the maintenance surface is justified.
- Keep public surface small, current, and singular. Remove obsolete spellings instead of carrying compatibility shims.
- Preserve onlava-native naming: `.onlava.json`, `//onlava:*`, and `github.com/pbrazdil/onlava/...`.
- Keep generated app models and machine-readable JSON contracts stable. If a JSON shape changes, update schemas, docs, tests, and harness expectations together.
- Do not commit machine-local state or generated cache output from `.onlava/`, Grafana, Victoria, node modules, coverage, `.DS_Store`, or local environment files.

## Before Making Changes

For any non-trivial task:

```sh
onlava inspect docs --json
```

Use the output's `summary.review_due_count`, document-level `review_due`, and `stale` fields while choosing doc-gardening work.

Read the relevant files from that output, then check:

```text
docs/local-contract.md
docs/agent-guide.md
docs/plans/active.md
docs/tech-debt.md
```

For complex features, migrations, multi-hour work, or significant refactors, create or update an ExecPlan as described in `PLANS.md`.

- Store active plans under `docs/plans/<0000-short-slug>.md`.
- Link active plans from `docs/plans/active.md`.
- Keep Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective current.
- `PLAN.md` is the strategic roadmap. Do not treat it as an executable task plan.

## CLI Commands Agents Should Prefer

Use JSON surfaces for inspection and automation:

```text
onlava version --json
onlava doctor --json
onlava check --json
onlava inspect app|routes|services|endpoints|wire|build|paths|docs --json
onlava inspect temporal --json
onlava traces list --json
onlava metrics list --json
onlava logs --jsonl --limit 200
onlava harness --json --write
onlava harness self --json --write
```

Use `onlava doctor --json` before expensive troubleshooting when the failure may be local environment readiness: missing or old Go, low disk or memory, absent optional tools, or an app root that is not discoverable.

Use runtime commands according to intent:

```text
onlava up [--app-root <path>] [--session <id>|--new-session] [--json] [--detach]
onlava logs --follow [--app-root <path>] [--session current|<id>] [--jsonl]
onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all]
onlava serve [--app-root <path>] [--env <name>] [--log-format text|json]
onlava task list [--app-root <path>] [--json]
onlava task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
onlava task run <name> [--app-root <path>]
onlava task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
onlava worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>]
onlava build [--app-root <path>] [-o <path>]
onlava test [--app-root <path>] [go test flags/packages...]
onlava generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
onlava db psql|apply|seed|setup|reset|drop|snapshot [--app-root <path>]
```

`onlava up` is the preferred local loop for agents because it runs the app session and exposes safe capabilities: dashboard, logs, traces, metrics, session routing, and managed dev services. `onlava serve` is for headless API execution and must not be expected to expose dev/admin endpoints, dashboard, proxy, or watch behavior. `onlava task` is for configured tasks and app-local code tasks.

## Documentation Update Rules

When changing behavior, update all affected layers in one change:

- `docs/local-contract.md` for CLI grammar, JSON schemas, artifact paths, and stability semantics.
- `docs/agent-guide.md` for agent workflows and client-app integration.
- `SKILL.md` for concise portable instructions used inside target apps.
- `README.md` for human-facing overview and install/run examples.
- `docs/app-development-cookbook.md` for practical app recipes.
- `docs/environment.md` for onlava-owned env vars.
- `docs/environment.registry.json` for the machine-readable env registry enforced by self-harness. Do not add production env usage unless the user explicitly asks for env or an active ExecPlan records the exception.
- `docs/knowledge.json` when adding, removing, or materially changing indexed docs.
- `docs/plans/active.md` and `docs/knowledge.json` together when adding or activating an ExecPlan, until active plan indexing is generated by the toolchain.

If a PRD is historical, do not silently rewrite it into current contract prose. Add a short "current contract lives in ..." note or update the docs index/knowledge metadata instead.

## Validation Matrix

For ordinary onlava repo changes:

```sh
go test ./...
go install ./cmd/onlava
```

For substantial onlava repo changes:

```sh
onlava harness self --json --write
```

For target app changes:

```sh
onlava check --json
go test ./...
onlava harness --json --write
```

For generated TypeScript client changes:

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava generate client --lang typescript --output <expected-output>
```

For dashboard UI changes:

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
onlava harness self --json --write
```

For browser/dashboard validation when relevant:

```sh
onlava harness ui --json --write
```

If a command cannot be run in the current environment, say exactly which command was skipped and why.

## App-Local Instructions For Clients

The installable onlava skill is necessary but not enough for client repositories such as `github.com/pbrazdil/onlv`.

Client apps should keep a small app-local `AGENTS.md` that records only app-specific facts:

- app root and `.onlava.json` location
- frontend roots and generated client output paths
- required local environment names without values
- standard validation commands for that app
- whether agents should use `onlava up --detach`, generated TypeScript client, or direct CLI JSON
- product/domain invariants that onlava cannot know

Do not copy the whole onlava skill into the client app. Keep the shared onlava behavior in `SKILL.md` and the app-specific policy in the client's `AGENTS.md`.

## Public Surface Checklist

When editing source that changes the public app model, confirm the docs and tests cover:

- `//onlava:api public|auth|private [raw] [path=/...] [method=...]`
- `//onlava:service`
- `//onlava:authhandler`
- request tags: `json`, `header`, `query`, `qs`, `cookie`
- response tag: `onlava:"httpstatus"`
- public packages: `onlava`, `auth`, `errs`, `middleware`, `temporal`, `cron`, `pgxpool`, `et`
- standard auth configuration and generated endpoints
- private/internal call behavior
- worker, Temporal, cron, middleware, and generated TypeScript client behavior when touched

## Repository Hygiene

- Keep changes small and explicit.
- Prefer tests at stable boundaries: parser validation, codegen golden output, runtime HTTP behavior, CLI JSON contracts, schemas, and fixture apps.
- Keep large files split. Non-generated source over 2500 lines should fail self-harness architecture checks; non-generated source over 1000 lines should be treated as a warning to split soon.
- Do not bypass UI boundaries. Dashboard/app UI should compose from onlava primitives/layouts and approved `@onlava` registry items; read `docs/ui-agent-contract.md` before UI work.
