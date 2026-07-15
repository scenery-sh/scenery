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
6. `ARCHITECTURE.md` is the stable code map: the central `.scn` → compiler → generation → runtime flow, package boundaries, and architecture invariants. Read it before deciding where a change belongs.
7. `scenery inspect ... -o json`, schemas under `docs/schemas/`, and harness command outputs are stronger than old prose when they disagree. Generated files under `.scenery/gen/` are cache, not an API.

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

- `apps/console/AGENTS.md` owns the Vite/React Astryx + StyleX dashboard and frontend validation commands.
- `internal/agent/AGENTS.md` owns local agent protocol state, machine ownership records, and their durable identity migrations.
- `internal/compiler/AGENTS.md` owns source loading, validation, expansion, and immutable compiler results.
- `internal/contractagent/AGENTS.md` owns compiled-graph JSON-RPC capabilities and evolution dispatch.
- `internal/deployplan/AGENTS.md` owns deployment plans, provider coordination, and crash-safe application.
- `internal/edge/AGENTS.md` owns the managed Caddy edge process lifecycle and its real-process validation.
- `internal/evolution/AGENTS.md` owns semantic comparison, source mutation planning, approvals, and revision-bound receipts.
- `internal/generate/AGENTS.md` owns Go, TypeScript, OpenAPI, and runtime-composition generation and atomic artifact writes.
- `internal/graph/AGENTS.md` owns canonical resources, graph views, provenance, and general revision hashing.
- `internal/machine/AGENTS.md` owns singular CLI JSON/JSONL envelopes, exact machine revisions, producer identity, and strict current decoding.
- `internal/scn/AGENTS.md` owns `.scn` source discovery, safe filesystem access, parsing, positions, lossless CSTs, and canonical formatting.
- `internal/spec/AGENTS.md` owns the current resource/source-schema and diagnostic catalog, canonical JSON, and content revisions.
- `internal/testsuite/AGENTS.md` owns explicit fresh execution from content-addressed Go test binaries and Go JSON event output.
- `internal/workspacetx/AGENTS.md` owns crash-safe source transaction metadata, ownership checks, and recovery before compiler reads.
- `docs/spec/AGENTS.md` owns the evolving current specification set and conformance update rules.

## Agent skills

### Browser automation

When browser interaction is needed, use the `chrome:control-chrome` skill by default unless the user explicitly asks for a different browser surface or tool.

### Issue tracker

Issues and product specs for this repo live as Markdown under `.scratch/<feature>/`; external PRs are not a triage surface. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the default five-role triage vocabulary as local issue status values. See `docs/agents/triage-labels.md`.

### Domain docs

Use a multi-context domain docs layout with root `CONTEXT-MAP.md` plus per-context `CONTEXT.md` files. See `docs/agents/domain.md`.

## Current Mental Model

scenery is a Go-native service runtime and local development platform. Think in app roots, app runtimes, and capability surfaces first; Victoria, agent routing, generated cache files, hidden ports, and local stores are substrate details unless the task is explicitly debugging that substrate.

- App roots are marked by `.scenery.json`.
- `.scn` source is the singular current app model. Root `scenery.scn` installs package-local `scenery.package.scn` modules; Go source implements the generated native contracts but is not scanned for declarations.
- Generated Go contracts, adapters, composition, descriptors, and entrypoints live in external build/editor caches. Successful compilation maintains an ownership-verified, locally excluded root `go.work` for raw Go/editor resolution; source materialization is explicit export mode only.
- The compiler exposes source/effective/expanded graphs and separate workspace, contract, implementation, deployment, and artifact revisions. Source retains authored expressions, effective resolves inputs/defaults/patches, expanded adds generators, and every provenance key is an RFC 6901 pointer into that view's resource spec.
- `scenery task run <domain>:<name> -- [args...]` runs an app-local code task.
- `scenery worker` builds once and starts a worker-role runtime for declared durable executions and schedules.
- `scenery up` starts the app root's one live dev runtime: supervised app process, file watching, dashboard, API explorer, logs, traces, metrics, managed dev services, and optional frontend routing. Detached `--wait ready` returns only after every advertised route and one declared frontend asset are reachable. Re-running `scenery up` while a verified live owner already runs the same app root succeeds instead of failing: the human foreground form reports that runtime and attaches to its logs (Ctrl+C detaches without stopping it), `-o jsonl` reports and exits `0`, and detached reruns apply the requested wait readiness to the existing owner and set `already_running` in the JSON result. While that supervisor remains live, shared Victoria observability is probed and recovered as one managed stack; failed recovery is always surfaced as a degraded error rather than hidden behind verbose output.
- `scenery deploy <ssh-target>` is beta single-server source sync: the target must be allowed by `deploy.ssh`, OpenSSH owns connection configuration, rsync honors `.gitignore` and preserves remote `.env` and `.scenery` state, and the remote current Scenery binary stops then restarts the app with readiness waiting. Brief downtime is intentional.
- The local agent and managed edge are single-owner processes. Startup fails closed when a verified owner holds the runtime lock, reaps only same-user stale owners whose process fingerprints still match, and `scenery doctor` reports duplicate owners or foreign listeners on Scenery-owned ports. On machines configured with `scenery deploy setup`, the agent is continuously supervised by the `dev.scenery.agent` launchd LaunchAgent: LaunchAgent installs bootstrap the job (plist presence alone is not installation), teardown boots it out before removal, every agent start path cooperates with the supervisor instead of racing its KeepAlive respawn, and `scenery deploy status` reports supervision truth under `agent_supervisor` and is not `ready` without a loaded supervisor.
- Portable snapshots can be verified without a target app or stopped runtime. Scheduled retention, off-machine copy, and restore drills remain operator-owned through `scripts/snapshot-backup.sh` plus the host scheduler.
- `scenery system agent restart` restarts only the local control plane and router. Registered shared substrate processes survive; destructive shutdown stays with substrate-specific commands and verified lifecycle owners.
- Every CLI invocation best-effort appends one coarse, argument-free usage record to `~/.scenery/telemetry.jsonl`; telemetry write failures never affect the command result.
- Route manifests expose the API, Scenery-owned runtime/dashboard surfaces where appropriate, and configured frontends; arbitrary backend names do not receive reserved route behavior. `dev.routing.domain` optionally serves the whole path-mode session at a single-owner branded origin per Git worktree (`https://<branch>-<domain>`, bare on `main`) through the managed edge with operator-owned DNS (loopback or Cloudflare-proxied static IP, SSL mode Full); unreadiness degrades to localhost URLs with a warning, never a failed `scenery up`. `dev.routing.expose` opt-in narrows the domain origin to listed routes (localhost always serves everything), and `frontends.<name>.serve: "production"` builds that frontend and serves its `dist/` from a Scenery-internal static server with watcher-triggered in-place rebuilds.
- Public and auth endpoints are externally reachable. Private endpoints are internal-only and must be called through generated helpers.
- Typed endpoints decode path/query/header/cookie/body inputs into Go values and encode typed responses.
- Terminal HTTP path tails use `{name...}` plus one typed `path_tail` mapping under the HTTP codec/runtime contract. They capture zero or more complete segments with exact/literal/parameter/tail precedence, strict one-time segment decoding, ordinary typed Go inputs, and independently encoded TypeScript segments.
- Generated internal calls preserve route, private access, auth context, tracing, and error semantics.
- Constructors receive typed `scenery.sh/datasource` and `scenery.sh/object` capabilities; built-in CRUD, fixtures, views, pages, and renderers stay in the same generated application composition.
- Agent capabilities expose exact `resource_create_kinds`; `scenery schema` / `schema.get` provide the recursive authored shape, and semantic creation must reject unadvertised kinds instead of guessing blocks, labels, or source destinations.
- Mutation plans normalize typed values/references and resolved kind/schema identities before hashing. Planning retains the exact canonical plan under app-local trusted state, and apply rejects caller-recomputed plans before trusting expiry, approvals, operations, edits, or provider actions. Approval-bearing migration transitions use `--out <plan>` followed by `migrate apply <plan>` so the detached token binds the exact issued plan instead of a replanned expiry. Semantic renames emit revision-bound, digest-checked plan/apply receipts, including migration-manifest references and containing-module descendants; later diffs load matching app-local receipts or accept `--rename-receipts` explicitly.

Scenery does not have legacy support. It has **one rolling Scenery specification, one compiler, one runtime path, and one machine protocol—the ones shipped by the current Scenery binary.**

Do not add deprecated APIs, compatibility aliases, old decoders, or fallback runtime paths.

## Engineering Rules

- Prefer the Go standard library. Add dependencies only when the payoff is clear and the maintenance surface is justified.
- Keep public surface small, current, and singular. Remove obsolete spellings instead of carrying compatibility shims.
- Keep `internal/app` free of the PostgreSQL driver layer; deterministic database/schema/env naming belongs in `internal/postgresname`.
- Keep `golang.org/x/tools/go/packages` inside `internal/parse`; `internal/model` exposes only model-owned analysis data.
- Do not add new environment-variable knobs by default. Prefer explicit CLI flags, config files, or existing contracts; add an env var only when the human explicitly asks for one or an active ExecPlan records why flags/config are insufficient.
- Preserve scenery-native naming: `scenery.scn`, `scenery.package.scn`, `.scenery.json`, and `scenery.sh/...`.
- Keep generated app models and machine-readable JSON contracts stable. If a JSON shape changes, update schemas, docs, tests, and harness expectations together.
- Keep module sources inside the non-symlink app workspace and every generated output beneath a declared managed root; top-level generation is one artifact-set transaction.
- Declare every service, operation, binding, durable execution, schedule, data resource, page, renderer, and middleware identity in `.scn`. Go package comments and package-init builders do not register application behavior. If `external_name` preserves an existing durable task name while its persisted input changes, increment `revision` and drain or migrate active rows first.
- Keep every diagnostic in the checked-in current specification catalog with one stable identity. Request-protocol failures use SCN8000-range codes; SCN9000-range codes are internal-only and must carry an opaque report token with a sanitized public message.
- Do not commit machine-local state or generated cache output from `.scenery/`, Victoria, node modules, coverage, `.DS_Store`, or local environment files.

## Before Making Changes

For any non-trivial task:

```sh
scenery inspect docs -o json
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
scenery version -o json
scenery doctor -o json
scenery check -o json
scenery inspect app|routes|services|endpoints|build|paths|docs -o json
scenery traces list -o json
scenery metrics list -o json
scenery logs -o jsonl --limit 200
scenery harness -o json --write
scenery harness self --summary --write
scenery upgrade -o json
```

For current Scenery apps, prefer the current protocol and immutable transaction surfaces:

```text
scenery fmt --check -o json
scenery check -o json
scenery compile --view expanded -o json
scenery list|get|explain|graph ... -o json
scenery diff --semantic BASE TARGET [--rename-receipts <change-plan-or-receipt.json>] -o json
scenery generate --check -o json
scenery changes plan|apply ... -o json
scenery deploy plan|apply ... -o json
```

`-o json` selects the singular `scenery.cli` envelope. `-o jsonl` emits `scenery.cli.event` envelopes for streaming commands. Both carry exact schema/spec revisions and producer identity; command-specific schemas live under the envelope `data` field.

Use `scenery doctor -o json` before expensive troubleshooting when the failure may be local environment readiness: missing or old Go, low disk or memory, absent optional tools, or an app root that is not discoverable.

Use runtime commands according to intent:

```text
scenery up [--app-root <path>] [-o jsonl] [--detach] [--wait ready|registered]
scenery logs --follow [--app-root <path>] [-o jsonl]
scenery down [--app-root <path>] [--db] [--state] [--all] [-o json]
scenery task list [--app-root <path>] [-o json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [-o json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery worker [--app-root <path>] [--env <name>]
scenery build [--app-root <path>] [--target <go-target>] [--output <path>] [-o human|json]
scenery test [--app-root <path>] [go test flags/packages...]
scenery generate --target typescript_client.<name> [--check] [--app-root <path>] -o json
scenery db list|shell|apply|seed|setup|reset|drop [--app-root <path>]
scenery db seed [--app-root <path>] [--env <name>] [--dry-run] [-o json]
scenery snapshot save --output <file.zip> [--db] [--storage] [--app-root <path>] [-o json]
scenery snapshot verify --input <file.zip> [-o json]
scenery snapshot load --input <file.zip> [--db] [--storage] --mode overwrite|merge [--on-conflict fail|skip|overwrite] [--yes] [--dry-run] [--app-root <path>] [-o json]
```

`scenery up` is the preferred local loop for agents because it runs the app root's one live dev runtime and exposes safe capabilities: dashboard, logs, traces, metrics, routed local URLs, and managed dev services. Use a Git worktree for another live code copy. `scenery task` runs app-local code tasks declared by their `<domain>:<name>` path.

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

Run a single test with `go test ./<package> -run '<TestName>'`, for example
`go test ./cmd/scenery -run TestWriteDetachedDevResultJSON`.

Use Go's test result cache for ordinary, focused, and substantial final
validation. Pass `-count=1` only when explicitly measuring fresh execution or
investigating nondeterminism. `scenery harness self --fresh-tests` is the
explicit fresh self-harness lane.

Do not run `go install ./cmd/scenery` during agent validation unless the human
explicitly asks. Multiple worktrees share the same installed `scenery` path; use
self-harness' worktree-local `.scenery/harness/bin/scenery` build instead.

For substantial scenery repo changes:

```sh
scenery harness self --summary --write
```

Self-harness timing keeps a five-second optimization target separate from its
operational lanes: cached and fresh runs use five-second advisory budgets,
while release mode enforces 30 seconds. Only explicit `--fresh-tests` runs use
isolated timing confirmation. That fresh lane uses package parallelism three,
selected from repeated measurements on the maintainer machine.

For target app changes:

```sh
scenery check -o json
go test ./...
scenery harness -o json --write
```

For generated TypeScript client changes:

```sh
scenery inspect endpoints -o json
scenery generate --target typescript_client.<name> --check -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
```

For dashboard UI changes:

```sh
cd apps/console
bun run lint
bun run typecheck
bun run build
cd ../..
scenery harness self --summary --write
```

For `ui/` registry changes, run `bun run typecheck` and `bun run test` from `ui/`.

For browser/dashboard validation when relevant:

```sh
scenery harness ui -o json --write
```

If a command cannot be run in the current environment, say exactly which command was skipped and why.

## App-Local Instructions For Clients

The installable scenery skill is necessary but not enough for client repositories such as `github.com/pbrazdil/onlv`.

Client apps should keep a small app-local `AGENTS.md` that records only app-specific facts:

- app root and `.scenery.json` config path
- frontend roots and generated client output paths
- required local environment names without values
- standard validation commands for that app
- whether agents should use `scenery up --detach`, generated TypeScript client, or direct CLI JSON
- product/domain invariants that scenery cannot know

Do not copy the whole scenery skill into the client app. Keep the shared scenery behavior in `SKILL.md` and the app-specific policy in the client's `AGENTS.md`.

## Public Surface Checklist

When editing source that changes the public app model, confirm the docs and tests cover:

- services, operations, executions, HTTP/internal/CLI bindings, authentication, authorization, and middleware resources
- CLI bindings, including generated help/completion, typed input, trusted context, delivery, outcomes, and exit codes
- `std.type.unit`, data sources, entities/views/CRUD/fixtures, pages/renderers, and typed constructor capability injection
- generated contract input/outcome types and explicit `.scn` HTTP request/response mappings
- public packages: `scenery`, `auth`, `errs`, `durable`, `db`, `datasource`, `object`, `storage`
- standard auth configuration and generated endpoints
- private/internal call behavior
- worker, durable, schedule, middleware, and generated TypeScript client behavior when touched

## Repository Hygiene

- Keep changes small and explicit.
- Prefer tests at stable boundaries: parser validation, codegen golden output, runtime HTTP behavior, CLI JSON contracts, schemas, and fixture apps.
- Keep large files split. Non-generated source over 2500 lines should fail self-harness architecture checks; non-generated source over 1000 lines should be treated as a warning to split soon.
- Do not bypass UI boundaries. The dashboard under `apps/console/` follows its local Astryx + StyleX contract. The reusable component registry under `ui/` follows `docs/ui-agent-contract.md`.
