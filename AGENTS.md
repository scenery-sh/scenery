# scenery Agent Instructions

This file is the repo-local operating manual for AI agents changing `scenery.sh`.

Optimize for agents: prefer concise rules, exact commands, and machine-readable contracts over long prose. Read what covers the surface you are changing; do not preload everything.

## Core Model

- Scenery runs my app runtime.
- Scenery gives me capabilities.
- Scenery lets agents inspect and act safely.
- Scenery hides the substrate unless I intentionally debug the substrate.

## Instruction Layers

Use the narrowest current source of truth that applies:

1. `AGENTS.md` gives repo-local rules for changing scenery itself.
2. `SKILL.md` is the installable skill for agents working inside any scenery app.
3. `docs/agent-guide.md` explains agent workflows, generated artifacts, client-app integration, and the full repository mental model.
4. `docs/local-contract.md` is the contract for CLI grammar, JSON schemas, artifact paths, and stability labels; its table of contents supports reading sections selectively.
5. `docs/app-development-cookbook.md` gives practical app-building recipes.
6. `ARCHITECTURE.md` is the stable code map. Read it before deciding where a change belongs.
7. `scenery inspect ... -o json`, schemas under `docs/schemas/`, and harness command outputs are stronger than old prose when they disagree. Generated files under `.scenery/gen/` are cache, not an API.

When implementation and docs disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift, owner, and intended resolution path.

Before editing, read what covers the surface you are changing:

1. Any `AGENTS.md` between the repository root and the paths you will touch that you have not already applied (see AGENTS Hierarchy).
2. The `docs/local-contract.md` and `docs/agent-guide.md` sections for that surface when your change touches their contracts; `ARCHITECTURE.md` when deciding where a change belongs.
3. `docs/plans/active.md` when the area may have an active ExecPlan, and `docs/tech-debt.md` before large refactors.

For complex features, migrations, multi-hour work, or significant refactors, create or update an ExecPlan as described in `PLANS.md`: active plans live under `docs/plans/<0000-short-slug>.md`, linked from `docs/plans/active.md`, with Progress, Surprises & Discoveries, Decision Log, and Outcomes kept current. `PLAN.md` is the strategic roadmap, not an executable task plan. Use `scenery inspect docs --for-path <path> -o json` for task-scoped documentation discovery, `--review-due` for doc gardening, and `--all` only when the complete catalog is required.

## AGENTS Hierarchy

`AGENTS.md` files are scoped operating contracts for the subtree that contains them. The closest file controls local details; parents still apply for repo-wide rules, and child docs must not weaken root engineering rules, public contracts, generated-artifact rules, or validation requirements. When docs conflict, current implementation, tests, CLI JSON output, and schemas win.

Add a child `AGENTS.md` only when a directory becomes a durable boundary with its own purpose, contracts, workflow, verification, or quality standards. Keep child docs short and operational, preferring the section order: Purpose, Ownership, Local Contracts, Work Guidance, Verification, Child Agent Index.

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
- `internal/librarybuild/AGENTS.md` owns the fixed-platform c-shared library build matrix and portable artifact manifests.
- `internal/machine/AGENTS.md` owns singular CLI JSON/JSONL envelopes, exact machine revisions, producer identity, and strict current decoding.
- `internal/scn/AGENTS.md` owns `.scn` source discovery, safe filesystem access, parsing, positions, lossless CSTs, and canonical formatting.
- `internal/spec/AGENTS.md` owns the current resource/source-schema and diagnostic catalog, canonical JSON, and content revisions.
- `internal/testsuite/AGENTS.md` owns explicit fresh execution from content-addressed Go test binaries and Go JSON event output.
- `internal/uireport/AGENTS.md` owns read-only React design-system adherence scanning, source exclusions, metrics, and deterministic ranking.
- `internal/workspacetx/AGENTS.md` owns crash-safe source transaction metadata, ownership checks, and recovery before compiler reads.
- `docs/spec/AGENTS.md` owns the evolving current specification set and conformance update rules.
- `ui/AGENTS.md` owns the binary-embedded Astryx + StyleX component catalog materialized into React-enabled TypeScript clients.

## Autonomy Policy

Reading, `-o json` inspection, builds, cached tests, and `scenery up` in a worktree are safe by default. The following need explicit human direction or a recorded exception:

- Do not spawn subagents (background review/research/explore agents, multi-agent workflows) unless the human explicitly asks; do the reading and analysis in the main session.
- Do not run `go install ./cmd/scenery` during validation; worktrees share the installed path — use self-harness' worktree-local `.scenery/harness/bin/scenery`.
- Do not add environment-variable knobs unless the human explicitly asks or an active ExecPlan records why flags/config are insufficient.
- Browser work defaults to the `chrome:control-chrome` skill.
- Issues and product specs live as Markdown under `.scratch/<feature>/` (`docs/agents/issue-tracker.md`); use the default five-role triage vocabulary (`docs/agents/triage-labels.md`); domain docs follow root `CONTEXT-MAP.md` plus per-context `CONTEXT.md` (`docs/agents/domain.md`).

## Mental Model

scenery is a Go-native service runtime and local development platform. Think in app roots, app runtimes, and capability surfaces first; Victoria, agent routing, generated cache files, hidden ports, and local stores are substrate details unless the task is explicitly debugging that substrate.

- App roots are marked by `.scenery.json` with named `envs` (reserved default `local`). `.scn` source is the singular current app model; Go implements the generated native contracts but is not scanned for declarations.
- The compiler exposes source/effective/expanded graphs with separate workspace, contract, implementation, deployment, and artifact revisions; every provenance key is an RFC 6901 pointer into that view's resource spec.
- `scenery up` runs the app root's one live dev runtime; `scenery worker` runs the worker role; `scenery task run <domain>:<name>` runs app-local code tasks. The local agent and managed edge are single-owner processes that fail closed on ownership conflicts.
- Typed endpoints, CRUD with fingerprint-bound cursor pagination, and page macros (`table_page`, `content_page`, `workspace_page`, `detail_page`) expand to ordinary page/renderer resources. React-enabled clients materialize the binary-owned catalog, generated route tree, navigation, and shell; domain-specific UI stays in app-owned `react_component` slots, never in Scenery's catalog or compiler.
- Mutation, migration, and deploy plans are revision-bound and digest-checked; apply rejects caller-recomputed plans.

The full repository mental model — runtime/deploy lifecycle, plan and receipt semantics, page-macro capabilities, and the fully-generated-client rules — lives in `docs/agent-guide.md` § Working In The scenery Repository. Exact grammar and schemas live in `docs/local-contract.md`.

Scenery does not have legacy support. It has **one rolling Scenery specification, one compiler, one runtime path, and one machine protocol — the ones shipped by the current Scenery binary.** Do not add deprecated APIs, compatibility aliases, old decoders, or fallback runtime paths.

## Engineering Rules

- Prefer the Go standard library. Add dependencies only when the payoff is clear and the maintenance surface is justified.
- Keep public surface small, current, and singular. Remove obsolete spellings instead of carrying compatibility shims.
- Keep `internal/app` free of the PostgreSQL driver layer; deterministic database/schema/env naming belongs in `internal/postgresname`.
- Keep `golang.org/x/tools/go/packages` inside `internal/parse`; `internal/model` exposes only model-owned analysis data.
- Preserve scenery-native naming: `app.scn`, `package.scn`, `.scenery.json`, and `scenery.sh/...`.
- Keep generated app models and machine-readable JSON contracts stable. If a JSON shape changes, update schemas, docs, tests, and harness expectations together.
- Keep module sources inside the non-symlink app workspace and every generated output beneath a declared managed root; top-level generation is one artifact-set transaction.
- Declare every service, operation, binding, durable execution, schedule, data resource, page, renderer, and middleware identity in `.scn`. If `external_name` preserves an existing durable task name while its persisted input changes, increment `revision` and drain or migrate active rows first.
- Keep every diagnostic in the checked-in current specification catalog with one stable identity. Request-protocol failures use SCN8000-range codes; SCN9000-range codes are internal-only and must carry an opaque report token with a sanitized public message.
- Do not commit machine-local state or generated cache output from `.scenery/`, Victoria, node modules, coverage, `.DS_Store`, or local environment files.
- When editing source that changes the public app model, run the Public Surface Checklist in `docs/agent-guide.md`.

## CLI Surfaces

Prefer `-o json` (the singular `scenery.cli` envelope) and `-o jsonl` (streaming `scenery.cli.event` envelopes) for inspection and automation; both carry exact schema/spec revisions and producer identity, with command-specific schemas under the envelope `data` field. The full implemented grammar is in `docs/local-contract.md` § CLI Grammar; runtime-command selection guidance is in `docs/agent-guide.md` § Runtime Command Choice.

Daily drivers:

```sh
scenery check -o json
scenery compile --view expanded -o json
scenery inspect app|routes|services|endpoints|build|paths|docs -o json
scenery up [--env <name>] [--detach] [--wait ready]
scenery logs -o jsonl --limit 200
.scenery/harness/bin/scenery harness self --summary --write
```

`scenery up` is the preferred local loop: one live dev runtime with dashboard, logs, traces, metrics, routed local URLs, and managed dev services; use a Git worktree for another live code copy. Use `scenery doctor -o json` before expensive troubleshooting when the failure may be local environment readiness.

## Documentation Update Rules

When changing behavior, update every affected layer in the same change. Small implementation-only edits need no instruction-doc updates; still report that docs were intentionally left unchanged.

| What changed | Update |
|---|---|
| Subtree contracts, workflow, or verification | nearest owning `AGENTS.md` |
| Repo-wide rules, instruction layering, public behavior, validation policy, child index | root `AGENTS.md` |
| CLI grammar, JSON schemas, artifact paths, stability semantics | `docs/local-contract.md` (+ `docs/schemas/`) |
| Agent workflows, repo mental model, client-app integration | `docs/agent-guide.md` |
| Behavior agents rely on inside target apps | `SKILL.md` |
| Human-facing overview, install/run examples | `README.md` |
| Practical app recipes | `docs/app-development-cookbook.md` |
| Scenery-owned env vars | `docs/environment.md` + `docs/environment.registry.json` (self-harness enforced) |
| Adding, removing, or materially changing indexed docs | `docs/knowledge.json` |
| Adding or activating an ExecPlan | `docs/plans/active.md` + `docs/knowledge.json` |

If a historical product note appears in an ExecPlan, do not silently rewrite it into current contract prose; add a short "current contract lives in ..." note or update the docs index instead.

## Validation Matrix

Validation selection is calculated from changed paths and contract surfaces. Refresh `.scenery/harness/agent-context.json`, use `changed_area.validation_classes` to see which rows matched, and run the exact `changed_area.recommended_commands` union; when several rows match, all apply.

| Changed area | Minimum proof |
|---|---|
| Documentation only | `.scenery/harness/bin/scenery harness self --quick --summary --write` proves the knowledge index, links, referenced schemas, and commands |
| One Go package | `go test ./<package>`, then `go test ./...` before completion; for multiple packages, run every affected-package command before the repository suite |
| CLI JSON contract | `go test ./cmd/scenery`, `.scenery/harness/bin/scenery harness self --quick --summary --write` for schema validation, and the matching `docs/local-contract.md` update |
| Compiler or generator | affected-package tests, both committed fixture regeneration commands below, then `go test ./...` |
| UI catalog | `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`, `go test ./internal/generate`, and both consumer fixture regenerations below |
| Dashboard | `cd apps/console && bun run lint && bun run typecheck && bun run build`, then `.scenery/harness/bin/scenery harness ui -o json --write` |
| Release-sensitive or runtime | `.scenery/harness/bin/scenery harness self --summary --write`; its applicable real-process steps are required proof |

The full self-harness supersedes the quick self-harness when both would otherwise be selected. Any source, configuration, or fixture path not matched by a specialized row gets the deterministic fallback `go test ./...`. Target-app changes use `scenery check -o json`, `go test ./...`, and `scenery harness -o json --write`.

Rely on Go's test result cache; pass `-count=1` only when explicitly measuring fresh execution or investigating nondeterminism (`scenery harness self --fresh-tests` is the explicit fresh lane; see `docs/agent-guide.md` § Self-Harness Timing).

When touching `internal/compiler` or `internal/generate`, regenerate the committed fixture clients in the same change and commit the diff — stale fixtures fail `go test ./...` with SCN6204, and the diagnostic's `suggestions` carry the refresh command:

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
```

Fresh worktrees need one-time provisioning before UI and self-harness lanes pass; see `docs/agent-guide.md` § Fresh Worktree Preflight. If a command cannot be run in the current environment, say exactly which command was skipped and why.

## Completion Contract

- Lead with the actual outcome.
- Name the changed contract or behavior.
- Report every validation command and result.
- Name applicable validation that was skipped and why.
- Report remaining risk, unresolved decisions, or follow-up work.

## Client Repositories

Client apps (for example `github.com/pbrazdil/onlv`) keep a small app-local `AGENTS.md` recording only app-specific facts: app root and config path, frontend roots and generated client output paths, required env names without values, standard validation commands, the preferred agent loop, and product invariants scenery cannot know. Do not copy the scenery skill into client apps; shared behavior stays in `SKILL.md`. Details: `docs/agent-guide.md` § Client-App Instructions.

## Repository Hygiene

- Keep changes small and explicit.
- Prefer tests at stable boundaries: parser validation, codegen golden output, runtime HTTP behavior, CLI JSON contracts, schemas, and fixture apps.
- Keep large files split. Non-generated source over 2500 lines should fail self-harness architecture checks; over 1000 lines is a warning to split soon.
- Keep instruction docs lean. Self-harness warns when this root `AGENTS.md` exceeds 2500 words or a child `AGENTS.md` exceeds 800, and fails on intra-document anchor links that match no heading in knowledge entrypoint docs; move detail into `docs/agent-guide.md` or `docs/local-contract.md` instead of growing the budgets.
- Do not bypass UI boundaries. The dashboard under `apps/console/` follows its local Astryx + StyleX contract; the binary-owned catalog under `ui/` follows `ui/AGENTS.md`.
