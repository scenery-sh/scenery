# scenery Agent Instructions

Repo-local rules for changing Scenery. Keep instructions concise, task-scoped,
and grounded in current contracts.

## Communication And Repository Language

Use Czech with developers unless they request another language. Keep repository
artifacts in English: docs, plans, code, comments, tests, product copy and commits.

## Core Model

- Scenery runs my app runtime.
- Scenery gives me capabilities.
- Scenery lets agents inspect and act safely.
- Scenery hides the substrate unless I intentionally debug the substrate.

## Instruction Layers

Read the root and child `AGENTS.md` scopes covering the files being changed.
Read them once per task; reread when scope/content changes or context is missing.
Use other sources for the decision at hand:

- `ARCHITECTURE.md`: package ownership and service boundaries.
- `docs/local-contract.md` and checked schemas: changed CLI, JSON, artifacts or
  public behavior; read the owning sections.
- `docs/agent-guide.md`: runtime workflows, generation and client integration.
- `SKILL.md`: the installable entrypoint for agents inside Scenery apps.
- `docs/app-development-cookbook.md`: practical app recipes.
- `docs/plans/active.md`: active work affecting the area; `docs/tech-debt.md`
  before substantial refactors. Complex, multi-hour or multi-subsystem work
  uses `PLANS.md`; small fixes do not need an ExecPlan. `PLAN.md` is the roadmap.

Use `scenery inspect docs --for-path <path> -o json` when locating applicable
sections or validation commands. A known typo or local edit does not require a
whole-repo map. `docs/index.md` maps docs; `docs/knowledge.json` indexes them.
`--review-due` selects gardening work; `--all` requests the complete catalog.

Implementation, tests, current JSON and schemas establish implemented behavior.
They do not waive normative requirements. Fix affected docs with the change or
record disagreement, owner and resolution in an active ExecPlan. Generated
`.scenery/gen/` files are cache, not an API.

## AGENTS Hierarchy

Child instructions own their subtree; parents retain repo-wide authority.
Children may supplement verification but cannot weaken root engineering rules,
public contracts or generated-artifact requirements. Add a child only for a
durable ownership boundary. Prefer Purpose, Ownership, Local Contracts, Work
Guidance, Verification and Child Agent Index as useful section headings.

### Child Agent Index

- `scripts/verify/AGENTS.md` owns repository verification, release probes, and exact-root timing enforcement outside the product CLI.

- `apps/console/AGENTS.md` owns the Vite/React Astryx + StyleX dashboard and frontend validation commands.
- `examples/webhook-inbox/AGENTS.md` owns the independent durable webhook example and its isolated native proof.
- `internal/app/AGENTS.md` owns app discovery, configuration and pure SQL requirement supply.
- `internal/parse/AGENTS.md` owns Go package analysis and model-owned ABI data.
- `internal/agent/AGENTS.md` owns local agent protocol state, machine ownership records, and their durable identity migrations.
- `internal/nativebuilddriver/AGENTS.md` owns captured stock-Go recipes, retained input-domain validation, and direct compiler/linker execution.
- `internal/compiler/AGENTS.md` owns source loading, validation, expansion, and immutable compiler results.
- `internal/contractagent/AGENTS.md` owns compiled-graph JSON-RPC capabilities and evolution dispatch.
- `internal/deployplan/AGENTS.md` owns deployment plans, provider coordination, and crash-safe application.
- `internal/devprocess/AGENTS.md` owns child-process readiness, cancellation, signals, and bounded output capture.
- `internal/edge/AGENTS.md` owns the managed Caddy edge process lifecycle and its real-process validation.
- `internal/evolution/AGENTS.md` owns semantic comparison, source mutation planning, approvals, and revision-bound receipts.
- `internal/generate/AGENTS.md` owns Go, TypeScript, OpenAPI, and runtime-composition generation and atomic artifact writes.
- `internal/graph/AGENTS.md` owns canonical resources, graph views, provenance, and general revision hashing.
- `internal/harnessevidence/AGENTS.md` owns bounded evidence artifacts and explicit writes, without verification execution.
- `internal/harnessreport/AGENTS.md` owns shared report data and pure bounded summaries.
- `internal/machine/AGENTS.md` owns singular CLI JSON/JSONL envelopes, exact machine revisions, producer identity, and strict current decoding.
- `internal/repoinfo/AGENTS.md` owns read-only knowledge data and the singular path classification table.
- `internal/scn/AGENTS.md` owns `.scn` source discovery, safe filesystem access, parsing, positions, lossless CSTs, and canonical formatting.
- `internal/spec/AGENTS.md` owns the current resource/source-schema and diagnostic catalog, canonical JSON, and content revisions.
- `internal/stateupgrade/AGENTS.md` owns explicit same-schema retained-metadata transactions, private backups and interruption recovery.
- `internal/testsuite/AGENTS.md` owns explicit fresh execution from content-addressed Go test binaries and Go JSON event output.
- `internal/uireport/AGENTS.md` owns read-only React design-system adherence scanning, source exclusions, metrics, and deterministic ranking.
- `internal/workspacetx/AGENTS.md` owns crash-safe source transaction metadata, ownership checks, and recovery before compiler reads.
- `testdata/apps/worktree-postgres/AGENTS.md` owns the authored worktree-runtime SQL acceptance fixture and explicit external proof.
- `docs/spec/AGENTS.md` owns the evolving current specification set and conformance update rules.
- `ui/AGENTS.md` owns the binary-embedded Astryx + StyleX component catalog materialized into React-enabled TypeScript clients.
- `ui/components/AGENTS.md` owns reusable request-state, shell/navigation, table, workspace, and detail-page component behavior.

## Autonomy Policy

Continue authorized reading, local edits, builds, cached tests and `scenery up`
in a worktree, including correcting failures caused by the change. Do not ask
again for permission already granted. Ask only for a material unresolved scope
choice or an action whose explicit authorization is still missing.

- Do not spawn subagents unless the human explicitly asks. Skills cannot grant
  delegation permission.
- Validation uses the worktree-local `.scenery/harness/bin/scenery`. Do not run `go install ./cmd/scenery` during validation; installation needs an explicit
  human request because worktrees share the installed path.
- Preserve unrelated dirty work and verified runtime/data ownership. Worktree
  removal does not authorize data deletion; do not adopt or reset mismatched
  resources. Follow the owning runbook for authorized recovery or migration.
- Do not add environment-variable knobs unless the human asks or an active
  ExecPlan records why flags/config are insufficient.
- Interactive browsing uses native ChatGPT/Codex Chrome controls and the running
  profile, in the background. Use `chrome:control-chrome` when installed; never
  standalone browser MCPs or separate agent profiles.

## Engineering Rules

- Prefer the Go standard library and a small public surface. New dependencies
  need a clear payoff and justified maintenance cost.
- Keep one rolling Scenery specification, compiler, runtime and machine protocol.
  Remove obsolete spellings; no compatibility aliases, old decoders or fallback
  runtime paths. Preserve `app.scn`, `package.scn`, `.scenery.json`, `scenery.sh/...`.
- Declare services, operations, bindings, durable work, schedules, data, pages,
  renderers and middleware identities in `.scn`. Domain UI stays in app-owned
  slots. A retained durable `external_name` with changed persisted input needs
  a new `revision` and drained or migrated active rows.
- Keep JSON/model contracts stable; change schemas, docs, tests and harness
  expectations together. Diagnostics belong in the current checked catalog:
  SCN8000-range for request failures; SCN9000-range internal failures expose
  only sanitized messages and opaque report tokens.
- Keep module sources inside the non-symlink workspace and generated output
  beneath declared managed roots. Generation is one artifact-set transaction.
  Application-imported Go projections stay in the existing module and are
  ignored by default; never synthesize editor modules or manage root workfiles.
- Follow package-specific boundaries in the nearest instructions and architecture.
  Changes to the public app model require the
  [Public Surface Checklist](docs/agent-guide.md#public-surface-checklist).
- Do not commit `.scenery/`, Victoria state, node modules, coverage, `.DS_Store`
  or local environment files. Preserve unrelated edits and keep changes explicit.
- Prefer tests at parser, codegen, runtime HTTP, CLI JSON, schema and fixture
  boundaries. Non-generated source over 2500 lines fails architecture checks;
  over 1000 warns. Root instructions over 2500 words or child instructions over
  800 warn; move detail into its owning reference instead of raising budgets.

## CLI And Client Apps

`.scenery.json` identifies an app root. Prefer `-o json` (`scenery.cli`) and
`-o jsonl` (`scenery.cli.event`), with exact revisions and producer identity.
Use `scenery help <command> -o json` for grammar and `scenery up` for the local
loop. Diagnose environment readiness with `scenery doctor -o json` before
expensive troubleshooting; discover runtime URLs through `scenery ps -o json`.

Client repositories keep a small app-local `AGENTS.md`: app/config paths,
frontend and generated-client roots, required env names without values,
validation commands, preferred loop and product invariants. Do not copy this
manual or the shared skill. See [client instructions](docs/agent-guide.md#client-app-instructions).

Issues and product specs live under `.scratch/<feature>/`; use the
[issue workflow](docs/agents/issue-tracker.md),
[triage vocabulary](docs/agents/triage-labels.md), and
[domain guidance](docs/agents/domain.md) for those tasks.

## Documentation Update Rules

Only humans may modify `VNEXT.md`.

Update every affected layer with behavior changes; implementation-only edits need no instruction-doc change.

| Changed surface | Owning document |
|---|---|
| Subtree ownership, workflow or checks | nearest `AGENTS.md` |
| Repo-wide rules or validation | root `AGENTS.md` |
| CLI, JSON, artifact paths or stability | `docs/local-contract.md` and schemas |
| Agent workflows or app integration | `docs/agent-guide.md` and relevant skill route |
| Human overview or examples | `README.md` or `docs/app-development-cookbook.md` |
| Scenery env vars | `docs/environment.md` and `docs/environment.registry.json` |
| Indexed docs or active ExecPlans | `docs/knowledge.json`; plans also `docs/plans/active.md` |

Keep active plans' Progress, Surprises & Discoveries, Decision Log and Outcomes
current. Completed numbered plans are immutable history, without freshness
reviews. Put later guidance or contradictions in living docs, the completed
index or knowledge metadata. Historical notes are not current contracts.

## Validation Matrix

After editing, select quick or full from the changed areas below. Run full
instead of quick when any area requires it; do not run quick first merely to
choose full. The selected `--write` run refreshes
`.scenery/harness/agent-context.json`; inspect its current
`changed_area.validation_classes` and fulfill the union in
`changed_area.recommended_commands`. If new changes add a class, complete its
additional checks. A pre-edit snapshot cannot prove the final change.

Every verifier builds the worktree-local product binary. See
[Fresh Worktree Preflight](docs/agent-guide.md#fresh-worktree-preflight).
Repository verification belongs to `scripts/verify`, not the application CLI.

| Changed area | Minimum proof |
|---|---|
| Documentation only | `go run ./scripts/verify --quick --summary --write` |
| Go package(s) | affected `go test ./<package>` commands, then `go test ./...` |
| CLI JSON contract | `go test ./cmd/scenery`, quick verifier, matching `docs/local-contract.md` update |
| Compiler or generator | affected tests, both fixture regenerations below, then `go test ./...` |
| UI catalog | `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`, `go test ./internal/generate`, both fixture regenerations |
| Dashboard | `cd apps/console && bun run lint && bun run typecheck && bun run build`, then `.scenery/harness/bin/scenery harness ui -o json --write` |
| Release-sensitive or runtime | `go run ./scripts/verify --summary --write`; named probes for changed external boundaries |

Matches are cumulative; unmatched source/config/fixtures require `go test ./...`.
A successful verifier step satisfies the same required check for the same
inputs and scope, including full's repository Go suite. Reuse that evidence;
repeat or broaden checks only for new changes, failures or unresolved concerns.
Run `golangci-lint run ./...`; `.golangci.yml` owns the explicit correctness set.
`errorlint` checks wrapped-error comparisons/assertions, without mandating API
wrapping. Suppress only a narrowly justified intentional rule violation.

Quick/default/race are service-free. Changed external boundaries require their
named `--probe <id>` from the [catalog](docs/harness-engineering.md#explicit-probe-catalog),
including assertion and cleanup evidence. Release certification is explicit:
run only `scripts/release-gate.sh` (one `--release`). Benchmarks and all-root
timing audits require an explicit human request.

Keep Go's test cache enabled. `-count=1` and `--fresh-tests` are for explicit fresh
measurement or nondeterminism investigation. **Every exact top-level Go test
root must remain below repeated isolated 100ms p95, without exceptions**; aim
for 50–60ms bodies. Real processes, toolchains, services, network and OS-boundary
proof belong in explicit integration probes, with ordinary in-process coverage.
Do not hide cost in TestMain, subtests, setup, shared fixtures or exception lists.

Compiler/generator changes regenerate and include both committed clients:

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
```

Target-app changes use `scenery check -o json`, `go test ./...`, and
`scenery harness -o json --write`, plus the app's declared checks and acceptance.
Follow `apps/console/AGENTS.md` for dashboard work and `ui/AGENTS.md` for the
binary-owned catalog; do not bypass their boundaries.

## Completion Contract

For implementation requests, continue through required validation and fixes
caused by the change. Completion means the requested behavior is demonstrated;
a first patch or successful build alone does not finish a runtime/UI task.
Define the observable scenario before such work and verify the current served
identity. Stay within the authorized scope; report independent blockers without
silently expanding the assignment.

Report the actual outcome, changed behavior, every validation command/result,
applicable skipped checks and reasons, and remaining risks or decisions. State
when documentation was intentionally unchanged. Planned or skipped checks are
unverified, not passes.
