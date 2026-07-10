# scenery Agent Instructions

This file is the repo-local operating manual for AI agents changing `scenery.sh`.

Optimize for agents: prefer concise rules, exact commands, and machine-readable contracts over long prose.


## Core Model

- Scenery runs my app runtime.
- Scenery gives me capabilities.
- Scenery lets agents inspect and act safely.
- Scenery hides the substrate unless I intentionally debug the substrate.

## Instruction Layers

Use the narrowest current source of truth that applies:

1. `AGENTS.md` gives repo-local rules for changing scenery itself.
2. `SKILL.md` is the installable skill for agents working inside any scenery app.
3. `docs/agent-guide.md` explains agent workflows, generated artifacts, and client-app integration.
4. `docs/local-contract.md` is the contract for CLI grammar, JSON schemas, artifact paths, and stability labels.
5. `docs/app-development-cookbook.md` gives practical app-building recipes.
6. `scenery inspect ... --json`, schemas under `docs/schemas/`, and harness command outputs are stronger than old prose when they disagree. Generated files under `.scenery/gen/` are cache, not an API.

When implementation and docs disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift, owner, and intended resolution path.

## AGENTS Hierarchy

`AGENTS.md` files are scoped operating contracts for the subtree that contains them.

Before editing non-trivial changes:

1. Read this root `AGENTS.md`.
2. Identify the files and directories you expect to touch.
3. For each target path, walk from the repository root to that path and read every `AGENTS.md` found along the way.
4. Use the closest `AGENTS.md` for local details; parent `AGENTS.md` files still apply for repo-wide rules.
5. When docs conflict, current implementation, tests, CLI JSON output, schemas, and the narrower `AGENTS.md` control local details, but child docs must not weaken root engineering rules, public contracts, generated-artifact rules, or validation requirements.
6. Do not rely on memory. Re-check the applicable instruction chain in the current session before editing.

After meaningful changes:

- Update the nearest owning `AGENTS.md` when the change alters durable purpose, ownership, workflow, generated paths, validation commands, quality rules, required inputs/outputs, side effects, or future agent behavior for that subtree.
- Update this root `AGENTS.md` when the change alters repo-wide rules, instruction layering, public scenery behavior, validation policy, or the child index.
- Keep `docs/knowledge.json`, docs indexes, `SKILL.md`, `docs/agent-guide.md`, and child `AGENTS.md` files synchronized when the same contract is affected.
- Do not update instruction docs for small implementation-only edits that do not change future agent behavior; still report that instruction docs were intentionally left unchanged.

Child `AGENTS.md` files:

- Add one only when a directory becomes a durable boundary with its own purpose, contracts, workflow, verification, or quality standards.
- Keep child docs short and operational. Put broad scenery rules here; put concrete local commands and exceptions in the child.
- Preferred section order for new child docs: Purpose, Ownership, Local Contracts, Work Guidance, Verification, Child Agent Index.

### Child Agent Index

- `apps/consolenext/AGENTS.md` owns the Vite/React Astryx + StyleX dashboard and frontend validation commands.
- `internal/edge/AGENTS.md` owns the managed Caddy edge process lifecycle and its real-process validation.
- `internal/generateddata/AGENTS.md` owns model-derived schema, seed, generated web, and drift lifecycles.
- `internal/testsuite/AGENTS.md` owns content-addressed Go test binaries, fresh test execution, and Go JSON event output.

## Agent skills

### Browser automation

When browser interaction is needed, use the `chrome:control-chrome` skill by default unless the user explicitly asks for a different browser surface or tool.

### Issue tracker

Issues and product specs for this repo live in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the default five-role triage label vocabulary. See `docs/agents/triage-labels.md`.

### Domain docs

Use a multi-context domain docs layout with root `CONTEXT-MAP.md` plus per-context `CONTEXT.md` files. See `docs/agents/domain.md`.

## Current Mental Model

scenery is a Go-native service runtime and local development platform. Think in app roots, app runtimes, and capability surfaces first; Victoria, agent routing, generated cache files, hidden ports, and local stores are substrate details unless the task is explicitly debugging that substrate.

- App roots are marked by `.scenery.json`; `.config.json` is accepted as an app config alias when `.scenery.json` is absent.
- Go source is the app model: services, endpoints, auth handlers, middleware, durable tasks, cron jobs, and generated clients are discovered from code.
- `scenery task run <domain>:<name> -- [args...]` runs an app-local code task.
- `scenery worker` builds once and starts a worker-role runtime for durable tasks and cron.
- `scenery up` starts the app root's one live dev runtime: supervised app process, file watching, dashboard, API explorer, logs, traces, metrics, managed dev services, and optional frontend routing.
- Public and auth endpoints are externally reachable. Private endpoints are internal-only and must be called through generated helpers.
- Typed endpoints decode path/query/header/cookie/body inputs into Go values and encode typed responses.
- Generated internal calls preserve route, private access, auth context, tracing, and error semantics.

Do not revive deprecated non-scenery APIs, legacy directive spellings, or compatibility aliases unless an active plan explicitly requires compatibility.

## Engineering Rules

- Prefer the Go standard library. Add dependencies only when the payoff is clear and the maintenance surface is justified.
- Keep public surface small, current, and singular. Remove obsolete spellings instead of carrying compatibility shims.
- Keep `internal/app` free of the PostgreSQL driver layer; deterministic database/schema/env naming belongs in `internal/postgresname`.
- Keep `golang.org/x/tools/go/packages` inside `internal/parse`; `internal/model` exposes only model-owned analysis data.
- Do not add new environment-variable knobs by default. Prefer explicit CLI flags, config files, or existing contracts; add an env var only when the human explicitly asks for one or an active ExecPlan records why flags/config are insufficient.
- Preserve scenery-native naming: `.scenery.json`, `//scenery:*`, and `scenery.sh/...`. Treat `.config.json` as a supported config-file alias, not as the preferred spelling in new docs or examples.
- Keep generated app models and machine-readable JSON contracts stable. If a JSON shape changes, update schemas, docs, tests, and harness expectations together.
- Do not commit machine-local state or generated cache output from `.scenery/`, Victoria, node modules, coverage, `.DS_Store`, or local environment files.

## Before Making Changes

For any non-trivial task:

```sh
scenery inspect docs --json
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
scenery version --json
scenery doctor --json
scenery check --json
scenery inspect app|routes|services|endpoints|build|paths|docs --json
scenery traces list --json
scenery metrics list --json
scenery logs --jsonl --limit 200
scenery harness --json --write
scenery harness self --summary --write
scenery upgrade --json
```

Use `scenery doctor --json` before expensive troubleshooting when the failure may be local environment readiness: missing or old Go, low disk or memory, absent optional tools, or an app root that is not discoverable.

Use runtime commands according to intent:

```text
scenery up [--app-root <path>] [--json] [--detach] [--wait ready|registered]
scenery logs --follow [--app-root <path>] [--jsonl]
scenery down [--app-root <path>] [--db] [--state] [--all] [--json]
scenery task list [--app-root <path>] [--json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery worker [--app-root <path>] [--env <name>]
scenery build [--app-root <path>] [-o <path>]
scenery test [--app-root <path>] [go test flags/packages...]
scenery generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
scenery db list|path|shell|apply|seed|setup|reset|drop|snapshot [--app-root <path>]
```

`scenery up` is the preferred local loop for agents because it runs the app root's one live dev runtime and exposes safe capabilities: dashboard, logs, traces, metrics, routed local URLs, and managed dev services. Use a Git worktree for another live code copy. `scenery task` is for configured tasks and app-local code tasks.

## Documentation Update Rules

When changing behavior, update all affected layers in one change:

- `docs/local-contract.md` for CLI grammar, JSON schemas, artifact paths, and stability semantics.
- `docs/agent-guide.md` for agent workflows and client-app integration.
- `SKILL.md` for concise portable instructions used inside target apps.
- `README.md` for human-facing overview and install/run examples.
- `docs/app-development-cookbook.md` for practical app recipes.
- `docs/environment.md` for scenery-owned env vars.
- `docs/environment.registry.json` for the machine-readable env registry enforced by self-harness. Do not add production env usage unless the user explicitly asks for env or an active ExecPlan records the exception.
- `docs/knowledge.json` when adding, removing, or materially changing indexed docs.
- `docs/plans/active.md` and `docs/knowledge.json` together when adding or activating an ExecPlan, until active plan indexing is generated by the toolchain.

If a historical product note appears in an ExecPlan, do not silently rewrite it into current contract prose. Add a short "current contract lives in ..." note or update the docs index/knowledge metadata instead.

## Validation Matrix

For ordinary scenery repo changes:

```sh
go test ./...
go test ./cmd/scenery
```

Do not run `go install ./cmd/scenery` during agent validation unless the human
explicitly asks. Multiple worktrees share the same installed `scenery` path; use
self-harness' worktree-local `.scenery/harness/bin/scenery` build instead.

For substantial scenery repo changes:

```sh
scenery harness self --summary --write
```

Self-harness timing keeps a seven-second optimization target separate from its
operational lanes: cached and fresh runs use 12-second and 18-second advisory
budgets, while release mode enforces 30 seconds. Package and test timing
warnings require isolated confirmation; inspect the timing artifact before
treating contended full-suite elapsed values as regressions. The Go timing step
uses `-p 8`, selected from repeated measurements on the maintainer machine.

For target app changes:

```sh
scenery check --json
go test ./...
scenery harness --json --write
```

For generated TypeScript client changes:

```sh
scenery inspect endpoints --json
scenery generate client --lang typescript --output <expected-output>
```

For dashboard UI changes:

```sh
cd apps/consolenext
bun run lint
bun run typecheck
bun run build
cd ../..
scenery harness self --summary --write
```

For `ui/` registry changes, run `bun run typecheck` and `bun run test` from `ui/`.

For browser/dashboard validation when relevant:

```sh
scenery harness ui --json --write
```

If a command cannot be run in the current environment, say exactly which command was skipped and why.

## App-Local Instructions For Clients

The installable scenery skill is necessary but not enough for client repositories such as `github.com/pbrazdil/onlv`.

Client apps should keep a small app-local `AGENTS.md` that records only app-specific facts:

- app root and app config path (`.scenery.json` or `.config.json`)
- frontend roots and generated client output paths
- required local environment names without values
- standard validation commands for that app
- whether agents should use `scenery up --detach`, generated TypeScript client, or direct CLI JSON
- product/domain invariants that scenery cannot know

Do not copy the whole scenery skill into the client app. Keep the shared scenery behavior in `SKILL.md` and the app-specific policy in the client's `AGENTS.md`.

## Public Surface Checklist

When editing source that changes the public app model, confirm the docs and tests cover:

- `//scenery:api public|auth|private [raw] [path=/...] [method=...]`
- `//scenery:service`
- `//scenery:authhandler`
- request tags: `json`, `header`, `query`, `qs`, `cookie`
- response tag: `scenery:"httpstatus"`
- public packages: `scenery`, `auth`, `errs`, `middleware`, `durable`, `cron`, `db`, `et`
- standard auth configuration and generated endpoints
- private/internal call behavior
- worker, durable, cron, middleware, and generated TypeScript client behavior when touched

## Repository Hygiene

- Keep changes small and explicit.
- Prefer tests at stable boundaries: parser validation, codegen golden output, runtime HTTP behavior, CLI JSON contracts, schemas, and fixture apps.
- Keep large files split. Non-generated source over 2500 lines should fail self-harness architecture checks; non-generated source over 1000 lines should be treated as a warning to split soon.
- Do not bypass UI boundaries. The dashboard under `apps/consolenext/` follows its local Astryx + StyleX contract. The reusable component registry under `ui/` follows `docs/ui-agent-contract.md`.
