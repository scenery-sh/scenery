# scenery Agent Instructions

Use Czech with developers unless requested otherwise; keep code, comments,
tests, docs, product copy and commits in English. Scenery runs the app runtime,
gives capabilities, lets agents inspect and act safely, and hides the substrate
unless intentionally debugging it. Keep solutions simple and task-scoped.

## Instruction Layers

Read root and applicable child `AGENTS.md` once per task; reread only when scope,
content or missing context requires it. Children own their subtrees but cannot
weaken repo-wide engineering, public contracts or generated-artifact rules.

Use `scenery inspect docs --for-path <path>... -o json` to locate applicable
instructions, owning sections and cumulative validation commands. Add
`--include-text` for bounded excerpts; read beyond any explicitly truncated text
when that contract matters. A known local edit needs no whole-repo map.
`--review-due` selects gardening; `--all` selects the catalog.

Use `ARCHITECTURE.md` for boundaries, `docs/local-contract.md` and checked schemas
for CLI/JSON/artifacts, `docs/agent-guide.md` for runtime/app workflows, `SKILL.md`
for agents inside apps, and the cookbook for recipes. Read relevant active plans
and, before substantial refactoring, `docs/tech-debt.md`. `PLAN.md` is the roadmap;
complex multi-hour or multi-subsystem work uses `PLANS.md`, small fixes do not.
Implementation, tests and current schemas establish behavior without waiving
normative requirements. Correct disagreements in affected docs or record owner
and resolution in an active plan. `.scenery/gen/` is cache, not an API.

### Child Agent Index

- `scripts/verify/AGENTS.md`
- `examples/webhook-inbox/AGENTS.md`
- `internal/app/AGENTS.md`
- `internal/appconfig/AGENTS.md`
- `internal/parse/AGENTS.md`
- `internal/agent/AGENTS.md`
- `internal/compiler/AGENTS.md`
- `internal/contractagent/AGENTS.md`
- `internal/deployplan/AGENTS.md`
- `internal/devprocess/AGENTS.md`
- `internal/edge/AGENTS.md`
- `internal/evolution/AGENTS.md`
- `internal/feature/AGENTS.md`
- `internal/generate/AGENTS.md`
- `internal/graph/AGENTS.md`
- `internal/harnessevidence/AGENTS.md`
- `internal/harnessreport/AGENTS.md`
- `internal/machine/AGENTS.md`
- `internal/repoinfo/AGENTS.md`
- `internal/scn/AGENTS.md`
- `internal/spec/AGENTS.md`
- `internal/stateupgrade/AGENTS.md`
- `internal/testsuite/AGENTS.md`
- `internal/uireport/AGENTS.md`
- `internal/workspacetx/AGENTS.md`
- `testdata/apps/worktree-postgres/AGENTS.md`
- `docs/spec/AGENTS.md`
- `ui/AGENTS.md`
- `ui/components/AGENTS.md`

Add children only for durable ownership boundaries. Their Purpose, Ownership,
Local Contracts, Work Guidance and Verification explain scope; keep detailed
rules in the owning references.

## Autonomy Policy

Continue authorized reading, local edits, builds, cached tests and worktree
`scenery up`, including corrections caused by the change. Ask only for a material
unresolved scope choice or missing authorization; do not ask again for permission
already granted.

- Do not spawn subagents unless the human explicitly asks; skills cannot grant permission.
- Validate with `.scenery/harness/bin/scenery`. Do not run `go install ./cmd/scenery` unless the human explicitly requests installation; worktrees share that installed path.
- Preserve unrelated dirty work and verified runtime/data ownership. Branch switches and worktree removal do not authorize data deletion. Never adopt/reset mismatched resources; follow the owning recovery/migration runbook.
- Add no environment-variable knobs unless requested or an active plan explains why flags/config are insufficient.
- Browse interactively only through native ChatGPT/Codex Chrome controls, using the running profile in the background. Use `chrome:control-chrome` when installed; never standalone browser MCPs or separate profiles.

## Engineering Rules

- Prefer Go's standard library and a small public surface; justify new dependency maintenance costs.
- Keep one rolling specification, compiler, runtime and machine protocol. Remove obsolete spellings without compatibility aliases, old decoders or fallback paths. Preserve `app.scn`, `package.scn`, `.scenery.json`, `scenery.sh/...`.
- Declare service, operation, binding, durable-work, schedule, data, page, renderer and middleware identities in `.scn`; domain UI stays in app-owned slots. Changed persisted input with retained durable `external_name` needs a new `revision` and drained/migrated active rows.
- Change JSON/model contracts with schemas, docs, tests and harness expectations. Use the checked diagnostic catalog: SCN8000-range request failures; SCN9000-range internal failures expose sanitized messages and opaque report tokens only.
- Keep module sources in the non-symlink workspace and generated output beneath declared managed roots. Generate one artifact-set transaction. App-imported Go projections stay in the existing module, ignored by default; never synthesize editor modules or manage root workfiles.
- Follow nearest ownership rules and the [Public Surface Checklist](docs/agent-guide.md#public-surface-checklist) for public-model changes.
- Never commit `.scenery/`, Victoria state, node modules, coverage, `.DS_Store` or local environment files. Keep changes explicit.
- Prefer parser, codegen, runtime HTTP, CLI JSON, schema and fixture boundary tests. Non-generated source over 1,000 lines warns; over 2,500 fails. Instruction budgets are 2,500 root / 800 child words; move detail instead of increasing limits.

## CLI And Client Apps

`.scenery.json` identifies an app root. Prefer `-o json` / `-o jsonl` with exact
revisions and producer identity. Discover grammar through `scenery help <command>
-o json`, runtime URLs through `scenery ps -o json`, and use `scenery up` for the
live loop. Run doctor for unknown readiness or failed prerequisites.

Client `AGENTS.md` contains app/config paths, frontend/client roots, env names
without values, checks, loop and product invariants; do not copy this manual or
the shared skill. Use the [client guide](docs/agent-guide.md#client-app-instructions)
and [application matrix](docs/agent-guide.md#application-validation-and-completion).
Scenery has no dashboard UI; developer tooling uses generated `dev-runtime.ts`.
Issues/specs live in `.scratch/<feature>/`; follow [issue workflow](docs/agents/issue-tracker.md),
[triage vocabulary](docs/agents/triage-labels.md) and [domain guidance](docs/agents/domain.md).

## Documentation Update Rules

Only humans may modify `VNEXT.md`.

Behavior changes update every affected owning
layer; implementation-only edits need no instruction-doc update. Follow the
[owning-document table](docs/agent-guide.md#repository-documentation-updates).
Index changed docs/plans in `docs/knowledge.json` and active plans in
`docs/plans/active.md`. Keep active Progress (including Resume here), discoveries,
decisions and outcomes current. Completed/deprecated numbered plans are immutable
history without freshness reviews; later guidance belongs in living docs/indexes.

## Validation Matrix

Registered feature worktrees use focused development checks while working;
[local feature landing](docs/feature-workflow.md) owns the cumulative validation
of the combined candidate. A ready label is not acceptance. For completion or
landing, select quick/full **after editing** from the cumulative
[repository matrix](docs/harness-engineering.md#repository-validation-matrix).
Documentation-only edits select quick; source and contract changes retain the
matrix's cumulative requirements.
Run full directly when required. The selected `--write` run publishes an immutable
archive: read `agent-context-summary.json` first, then complete the
`agent-context.json` changed-area command union, reusing successful same-input,
same-scope checks. Reclassify additional edits; pre-edit proof is insufficient.
Always run `golangci-lint run ./...`; suppress only narrowly justified intentional
violations. Keep Go's test cache enabled; `-count=1` / `--fresh-tests` require explicit
fresh measurement or nondeterminism investigation. Every exact top-level Go test
root must remain below repeated isolated 100ms p95 without exceptions; aim at
50–60ms bodies. Processes, tools, services, network and OS proof belong in named
integration probes, not hidden setup/TestMain costs. Quick/default/race are
service-free. Changed external boundaries need their named probes and cleanup.
Release certification uses only `scripts/release-gate.sh`; benchmarks/all-root
timing audits require an explicit human request. Generator/UI changes retain the
matrix's exact regeneration/TypeScript checks.

## Completion Contract

Continue through required validation and change-caused failures. Define the
observable scenario before runtime/UI work. Demonstrate the
requested runtime/UI scenario and current served identity; a patch/build alone
is insufficient. Keep independent blockers explicit without expanding scope.
Report behavior, commands/results, skipped checks with reasons, remaining risks
and intentionally unchanged docs. Planned/skipped/warning-only proof is not a pass.
