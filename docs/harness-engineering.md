# scenery Harness Engineering

scenery treats agent support as a runtime feature, not as prompt folklore.

The harness contract gives Codex and other agents a short feedback loop:

1. discover the app through stable inspect command output
2. compile the generated runtime exactly like `scenery up` and `scenery build` would
3. report diagnostics as structured JSON
4. expose inspect outputs and artifact paths without scraping terminal text
5. persist the latest harness result when requested

## Command

The repository-only command is `go run ./scripts/verify`, run from the Scenery
checkout. It owns repository checks, release probes and timing enforcement;
the installed product cannot run them. `scenery harness` validates an app,
`scenery harness ui` drives its dashboard, and `scenery inspect harness` reads
bounded existing reports. The `scenery.harness.self` report names and paths
remain current data contracts, not an executable product subcommand.

```text
scenery harness [--app-root <path>] [-o json] [--write]
go run ./scripts/verify [--repo-root <path>] [--summary] [-o human|json] [--write] [--quick|--race|--release|--probe <id>...|--benchmark worktree-cost] [--fresh-tests]
scenery harness ui [--app-root <path>] [--dashboard-url <url>] [--headed] [-o json] [--write]
scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] -o json [--app-root <path>] [--repo-root <path>]
```

Self-harness uses Go's test result cache by default. Use `--fresh-tests` only
for explicit measurement or nondeterminism investigation. Every exact top-level
Go test root is subject to the root 100ms p95 policy; external-boundary proof
belongs in explicit probes. The exact lanes, budgets, confirmation algorithm,
and timing fields are owned by [the Local Contract](local-contract.md#harness-inspection-and-observability).

Use this before large edits and after fixes when an agent needs a single machine-readable status snapshot.

`ok` and summary `can_proceed` cover the selected checks, not unselected modes
or skipped proof. Human output preserves the JSON warning classification and
selected mode. Docker-unavailable database probes are explicit skips, never
database readiness/isolation proof; the shell gate likewise prints an unset
external-app lane as skipped rather than passed. Doctor is environment
preflight evidence, not application readiness. Inspect the warning details
before treating a passing run as sufficient for the current acceptance scope.

Recommended agent loop:

```text
scenery doctor -o json
go run ./scripts/verify --quick --summary --write
cat .scenery/harness/agent-context.json
# implement
# refresh agent-context after editing, then run changed_area.recommended_commands
```

For a missing local binary or dashboard embed, follow
[Fresh Worktree Preflight](agent-guide.md#fresh-worktree-preflight).
Every mode builds the prepared worktree-local CLI. Default, quick and race modes
do not provision console dependencies or run live-runtime probes.

When a change alters an external boundary, run its named probe from the catalog
below. For example, auth changes select `--probe auth`; worktree runtime and
managed SQL ownership changes select `--probe worktree`. These runs do not
repeat the complete Go suite. Record the selected IDs and boundary.

For an explicit release workflow, run only:

```text
scripts/release-gate.sh
```

The source-only snapshot includes tracked working-tree content and nonignored
new source files, excluding tracked deletions and ignored generated caches.
It proves the current authored working tree, not an unchanged committed HEAD.
The shell delegates the common Go/race/UI/release checks once to the repository
verifier and retains lint, source-snapshot build, and its distinct binary/router/
artifact probes. By default those binary probes use the verifier's prepared
product; an explicit `SCENERY_BIN` remains the separately selected shell target.
The source-snapshot build is disposable and no step installs a CLI.
Dashboard embed preparation precedes the product build; the verifier then
rebuilds source assets to compare them with that product's real HTTP hash.
Those preparation and freshness boundaries are distinct, not duplicate UI gates.
Its headless fixture probes use `scenery build
--target development --output <binary> -o json` and launch that binary with `SCENERY_LISTEN_ADDR`;
they do not start a development session. Readiness requires HTTP 200 and fails
early if the child exits. Cleanup stops children before removing their files.
The optional external-app check is explicitly skipped unless an app root is
supplied through the existing gate configuration.

The `--probe worktree` acceptance probe (also included in release) creates real Git worktrees
and tests managed PostgreSQL ownership, typed lending races, lifecycle and crash
recovery, external sharing, inert restores, optional Victoria recovery, local
versus public edge exposure, and genuinely different control-protocol binaries.
Its A16 case also exercises the public same-schema retained-state upgrade,
including stale-approval refusal, exact metadata backup and unchanged SQL data;
the storage probe verifies unchanged object payload hashes across that upgrade.
Its pre-cutover lane additionally requires the pinned Docker-in-Docker and Go
images; the historical binary runs only on that disposable nested daemon,
without a host Docker socket or source bind mount. No global developer cluster
is used. The same lane rehearses the
[native migration runbook](runbooks/worktree-postgres-migration.md).

Functional worktree proof runs A1–A17. A18 is separate:
`go run ./scripts/verify --benchmark worktree-cost --summary --write` runs only
on an explicit human measurement request, never as part of default or release.
The resource-cost lane runs three repetitions each of 1, 5 and 10 SQL-backed
worktrees with the real default Victoria profile. It records per-root and cohort
cold/warm serving times, native process RSS/CPU separately from Docker container
memory/CPU, fixed-window idle/load samples, disk usage and hardware/daemon
identity. Cold means fresh roots and cohort Go cache, not flushed host/module
or toolchain caches. Background developer workloads are not stopped, shared
pages can appear in multiple RSS values, and no capacity ceiling is inferred.

Keep the release guard strict, but make the strictness land on Scenery-owned
release safety: contracts, schemas, release artifacts, fixture runtimes, route
isolation, and managed-substrate semantics. Nondeterministic external host or
client-app substrate readiness must be reported as explicit evidence with
phase/session/substrate context; it should not masquerade as a core release
safety failure unless the release gate is intentionally validating that boundary.

For dashboard route or UI behavior changes, also run:

```text
scenery harness ui -o json --write
```

For changes to the PostgreSQL service probe's schema/durable/reset/snapshot
boundary, run the full selected database proof (missing Docker fails):

```text
go run ./scripts/verify --probe postgres --summary --write
```

Use `--quick` for cached affected-package checks, default for the complete
cached Go suite and vet, and `--probe` for a changed external boundary.

## Explicit Probe Catalog

`--probe <id>` is repeatable and selects the union in catalog order. Unknown
IDs, duplicate IDs, conflicting modes, and `--fresh-tests` combined with
`--probe` or `--benchmark` fail before builds or provisioning. Reports use
mode `probe` or `benchmark`, retain each selected step, and are not full
release certification. Failed steps identify their focused rerun command.

| ID | External boundary |
|---|---|
| `parallel-runtime` | Parallel runtime/session isolation |
| `postgres` | Full PostgreSQL service, durable step persistence/replay, reset and snapshot proof |
| `ui` | Dashboard build/freshness and TypeScript conformance/typechecks |
| `fixtures` | Fixture generation/compilation matrix |
| `storage` | Storage CLI, routes, restart persistence and a fresh tagged 260-entry disk-pressure reclamation/resume integration test |
| `core-separation` | Product/verifier dependency and source-only boundaries |
| `capability-authority` | Runtime capability authority |
| `auth` | All 15 database/OAuth lifecycle journeys |
| `worktree` | Functional A1–A17 worktree runtime/SQL ownership |
| `agent-restart` | Local-agent restart |
| `assistant-init` | Assistant initialization |
| `assistant-runtime` | Assistant production runtime |
| `build-info` | Build identity freshness |
| `cli-process` | CLI exit and telemetry |
| `dev-follower` | Development follower process |
| `dev-process` | Managed child-process lifecycle |
| `dev-lock` | Named process locks |
| `dev-cleanup` | Session cleanup |
| `inspect-go` | Go-package documentation inspection |
| `toolchain-build` | Source toolchain builds |
| `worktree-git` | Git worktree lifecycle |
| `edge` | Caddy/publication HTTP and TLS behavior |
| `generation` | Generated-package/source-only compilation |
| `native-contract` | Native contract application |
| `snapshot-backup` | Snapshot backup process |
| `typescript` | TypeScript checker |
| `code-task` | Code-task process |
| `victoria` | Victoria process lifecycle |
| `desktop` | Desktop process |
| `deploy-ssh` | SSH deployment process |
| `validation-git` | Changed-file Git validation |
| `test-cache` | Fresh test-binary cache lifecycle |

Full release selects every functional catalog entry once plus the full race
suite. Resource benchmarks and all-root timing audits are explicit measurement
workflows; neither is automatically selected for runtime edits.

## App Harness Checks

`scenery harness` composes:

- `scenery check -o json`
- `scenery inspect app -o json`
- `scenery inspect routes -o json`
- `scenery inspect services -o json`
- `scenery inspect endpoints -o json`
- `scenery inspect build -o json`
- `scenery inspect paths -o json`
- `scenery traces list -o json`
- `scenery metrics list -o json`
- `scenery inspect docs --all -o json`

`scenery traces list -o json` and `scenery metrics list -o json` are included
as beta diagnostic inputs for agents. Their exact schema revisions support
automation, but their rollup and backend-selection semantics remain internal
and unstable; see [local-contract.md](local-contract.md).

`scenery harness ui -o json` is the implemented browser-backed dashboard route
check. It starts a temporary dashboard target unless `--dashboard-url` is
provided, visits stable dashboard routes, runs route-specific semantic journeys,
checks durable `data-scenery-ui` markers, and writes screenshots, DOM snapshots,
console, and network artifacts under `.scenery/harness/ui/`. The route journeys
prove behavior such as API Explorer endpoint/form rendering, service metadata,
trace empty/table/detail states, database availability or intentional empty
states, cron status, and durable/worker status cards.

`scenery inspect harness -o json` reads the latest app, self, and UI harness
outputs from `.scenery/harness/` and returns their artifacts plus normalized
evidence records. Focused drill-down commands read bounded topic detail without
opening the full archive:

```text
scenery inspect harness artifact test-timing -o json
scenery inspect harness artifact drift -o json
scenery inspect harness diagnostics --severity warning -o json
scenery inspect harness timing --top 10 -o json
```

## Output

JSON output conforms to:

- [scenery.harness.result.schema.json](schemas/scenery.harness.result.schema.json)
- [scenery.harness.artifact.schema.json](schemas/scenery.harness.artifact.schema.json)
- [scenery.inspect.harness.schema.json](schemas/scenery.inspect.harness.schema.json)
- [scenery.harness.ui.schema.json](schemas/scenery.harness.ui.schema.json)
- [scenery.harness.ui.dom.schema.json](schemas/scenery.harness.ui.dom.schema.json)

When `--write` is present, scenery writes:

```text
<app-root>/.scenery/harness/latest.json
```

That file is intentionally stable. Agents should use it as the latest local validation snapshot instead of guessing from cache directories or parsing human logs.

Every failed or expensive step should include an `evidence` object with the
command, cwd, start time, duration, exit code, stdout/stderr tails, artifact
references, and a copy-pasteable `repro_command`. When `--write` is present,
large evidence payloads such as Go test JSONL are written under:

```text
<root>/.scenery/harness/artifacts/<run-id>/
```

The same evidence model is shared by the app harness, self-harness, UI harness,
and release gate so agents can inspect failures without scraping terminal
scrollback.

When `scenery harness ui -o json --write` is present, the browser harness writes:

```text
<app-root>/.scenery/harness/ui/latest.json
<app-root>/.scenery/harness/ui/screenshots/<route>.png
<app-root>/.scenery/harness/ui/dom/<route>.json
<app-root>/.scenery/harness/ui/console.jsonl
<app-root>/.scenery/harness/ui/network.jsonl
```

The DOM snapshots are compact semantic snapshots of elements carrying
`data-scenery-ui`, not full HTML dumps. They exist so agents can reproduce,
repair, restart, and verify browser behavior from machine-readable route state.

The self-harness writes `.scenery/harness/agent-context.json` as the default
handoff file for agents. It includes current failing steps, the first file to
read for each failure, exact rerun commands, deterministic validation classes,
their changed-area command union, relevant active ExecPlans, recent failed
harness artifacts, docs freshness, and separate risk classification.

For the scenery repo itself, `go run ./scripts/verify --summary --write` prints the
compact `scenery.harness.self.summary` decision packet and writes:

```text
<repo-root>/.scenery/harness/self-latest.json
<repo-root>/.scenery/harness/self-summary-latest.json
```

Use `go run ./scripts/verify -o json --write` only when stdout must contain the
full `scenery.harness.self` archive. Agents should prefer artifacts and focused
inspect commands over pasting `.scenery/harness/self-latest.json` into chat.

## Repository Self-Harness Checks

Every mode checks toolchain readiness, documentation/index integrity, Markdown
links, schema syntax, review state, changed-area selection, architecture,
contract drift, and schema conformance. The additional work depends on mode:

| Mode | Additional coverage |
|---|---|
| `--quick` | Cached affected-package tests; prepared local CLI build/freshness, no console provisioning. |
| Default | Complete cached Go suite and vet; no runtime/database/UI/fixture probes. |
| `--race` | Default coverage plus the race shortlist. |
| `--release` | Default coverage plus every functional probe, full race suite and enforced release budgets; no resource benchmark. |
| `--probe <id>` | Only selected external probes after common checks; no full Go suite. |
| `--benchmark worktree-cost` | Only A18 resource measurement after common checks; no functional probe set or full Go suite. |

The release edge-process step runs the published static frontend journey
against managed Caddy on disposable loopback ports, with local TLS issuance
and no system trust installation. It records `static_frontend.http_checks`
and raw traversal proof. Missing Caddy fails the step explicitly. Ordinary
edge tests retain renderer, publication, and injected-runner coverage.

The core-separation release step builds the verifier from source without tests
or generated app caches, rejects repository execution in the product dependency
closure, checks actual stale/matched product dashboard bundles, and exercises
app commands after removing verifier/test-engine sources from its disposable
SDK. It also proves invalid old grammar, unavailable-toolchain diagnostics and
real 20-process rejection of a deliberately slow disposable test root.

The generation release probe includes a source-only external Git checkout using
the candidate CLI and an explicit matching local framework replacement. It
records raw Go package/module resolution, tidy normalization, no-network tidy
after prewarming, generated mtimes, Git noise, freshness and automatic build
preparation under `.scenery/harness/ordinary-go-contracts/`. Failure retains the
disposable fixture for diagnosis. It does not prove a published-dependency lane
or managed-runtime resource isolation.

Managed database probes create their own tmpfs container and retain the exact
Docker-created ID for cleanup; names/ports/seeded state do not authorize deletion.
They create no named data volume and never clean the shared server by name.

`--fresh-tests` changes the Go execution/timing lane; it is independent of the
mode selection. Runtime and UI probes retain explicit skip diagnostics when
their required services or tools are unavailable. Read the resulting artifact
before calling a run complete; a successful subset does not prove skipped work.

## Design Rules

- Keep `AGENTS.md` short. It should point to source-of-truth docs instead of becoming an encyclopedia.
- Prefer stable JSON commands over terminal scraping.
- Inspect commands are the API; generated files are cache.
- Put remediation text in diagnostics so agents know what to do next.
- Promote repeated review feedback into docs, schemas, or mechanical checks.
- Repository validation instructions must not recommend `go install ./cmd/scenery`; the knowledge-contract step reserves shared CLI installation for an explicit human request.
- When docs and behavior disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift.

## Doc Gardening

Run `scenery inspect docs --for-path <path> -o json` before non-trivial repo
changes. It reuses the changed-area router to return applicable instruction
scopes, owning sections, active plans, schemas, and verification commands.
Use `scenery inspect docs --review-due -o json` to choose cleanup work and
`--all` only for complete catalog validation. `go run ./scripts/verify --summary
--write` includes the same docs freshness signals in its summaries.
Scheduled freshness covers living contracts, instructions, schemas, and active
plans. Completed numbered ExecPlans are immutable history and never enter the
review-due queue; request them explicitly with `--status completed`, `--all`,
or their direct path. Broken links from the completed index, stale knowledge
metadata that flags a contradiction, and completed plans referenced from the
active index remain actionable knowledge-contract signals.

Keep `docs/knowledge.json` aligned with agent-facing source-of-truth docs. Until
active ExecPlan indexing is generated by the toolchain, every active ExecPlan in
`docs/plans/active.md` must also have a document entry in `docs/knowledge.json`.

## Architecture Checks

`go run ./scripts/verify` includes a fast `architecture checks` step.

Hard failures:

- direct Go dependencies must be listed in the self-harness allowlist with a concrete rationale
- forbidden CLI/router/color framework imports are rejected in source
- packages outside `cmd/scenery` may not import `scenery.sh/cmd/scenery`
- required generated/vendored ignore markers must exist in `.gitignore` and `.gitattributes`
- non-generated source/code files over 2500 lines are rejected; Markdown docs are not subject to line-count size checks

Warnings:

- non-generated source/code files over 1000 lines; Markdown docs are not subject to line-count size checks
- cgo imports, because they require native build handling
- `.DS_Store` files found in the working tree
The dependency allowlist is intentionally small and lives in code next to the check. New direct dependencies should be rare and must include the reason they justify the added maintenance surface.

## Non-Goals

- The harness is not a CI replacement.
- Quick validation does not require live application services. Full and release
  modes may provision disposable managed services and must clean up what they own.
- It does not invent architecture rules. Add new checks only when the repo has a concrete invariant worth enforcing.
