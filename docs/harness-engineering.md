# scenery Harness Engineering

scenery treats agent support as a runtime feature, not as prompt folklore.

The harness contract gives Codex and other agents a short feedback loop:

1. discover the app through stable inspect command output
2. compile the generated runtime exactly like `scenery up` and `scenery build` would
3. report diagnostics as structured JSON
4. expose inspect outputs and artifact paths without scraping terminal text
5. persist the latest harness result when requested

## Command

```text
scenery harness [--app-root <path>] [-o json] [--write]
scenery harness self [--repo-root <path>] [--summary] [-o human|json] [--write] [--quick|--race|--release] [--fresh-tests]
scenery harness ui [--app-root <path>] [--dashboard-url <url>] [--headed] [-o json] [--write]
scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] -o json [--app-root <path>] [--repo-root <path>]
```

Self-harness uses Go's test result cache by default. Use `--fresh-tests` only
for explicit measurement or nondeterminism investigation. Every exact top-level
Go test root is subject to the root 100ms p95 policy; external-boundary proof
belongs in release probes. The exact lanes, budgets, confirmation algorithm,
and timing fields are owned by [the Local Contract](local-contract.md#harness-inspection-and-observability).

Use this before large edits and after fixes when an agent needs a single machine-readable status snapshot.

Recommended agent loop:

```text
scenery doctor -o json
.scenery/harness/bin/scenery harness self --quick --summary --write
cat .scenery/harness/agent-context.json
# implement
# refresh agent-context after editing, then run changed_area.recommended_commands
```

For a missing local binary or dashboard embed, follow
[Fresh Worktree Preflight](agent-guide.md#fresh-worktree-preflight).
Quick mode does not build the local CLI or provision console dependencies.

When `validation_classification` contains `release-sensitive-or-runtime`, also run:

```text
.scenery/harness/bin/scenery harness self --release --summary --write
scripts/release-gate.sh
```

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

For managed database changes, the default self-harness runs the live
Postgres service probe when Docker is reachable (and records an explicit
skip when it is not):

```text
.scenery/harness/bin/scenery harness self --summary --write
```

Use `--quick` only when you intentionally need the smaller self-harness loop
without live branch-substrate coverage.

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

For the scenery repo itself, `scenery harness self --summary --write` prints the
compact `scenery.harness.self.summary` decision packet and writes:

```text
<repo-root>/.scenery/harness/self-latest.json
<repo-root>/.scenery/harness/self-summary-latest.json
```

Use `scenery harness self -o json --write` only when stdout must contain the
full `scenery.harness.self` archive. Agents should prefer artifacts and focused
inspect commands over pasting `.scenery/harness/self-latest.json` into chat.

## Repository Self-Harness Checks

Every mode checks toolchain readiness, documentation/index integrity, Markdown
links, schema syntax, review state, changed-area selection, architecture,
contract drift, and schema conformance. The additional work depends on mode:

| Mode | Additional coverage |
|---|---|
| `--quick` | Cached affected-package tests; no local CLI build or console provisioning. |
| Default | Local CLI build/freshness, complete Go suite, vet, managed dev/database/storage probes, console dependencies, dashboard build/typecheck/freshness, generated-client conformance/typechecks, and fixture matrix. |
| `--race` | Default coverage plus the race shortlist. |
| `--release` | Default coverage plus external-boundary probes and enforced release budgets. |

The release edge-process step runs the published static frontend journey
against managed Caddy on disposable loopback ports, with local TLS issuance
and no system trust installation. It records `static_frontend.http_checks`
and raw traversal proof. Missing Caddy fails the step explicitly. Ordinary
edge tests retain renderer, publication, and injected-runner coverage.

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
`--all` only for complete catalog validation. `scenery harness self --summary
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

`scenery harness self` includes a fast `architecture checks` step.

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
