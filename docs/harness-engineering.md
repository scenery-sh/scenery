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
scenery harness [--app-root <path>] [--json] [--write]
scenery harness self [--repo-root <path>] [--summary|--json|--json=summary|--json=full] [--write] [--quick|--race|--release] [--fresh-tests]
scenery harness ui [--app-root <path>] [--dashboard-url <url>] [--headed] [--json] [--write]
scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]
```

Self-harness Go test steps always execute test bodies fresh with `-test.count=1`.
They reuse content-addressed linked test binaries, not Go test results, and run
the complete `./...` surface with the locally measured package parallelism of
three. `--fresh-tests` retains the explicit fresh timing-lane label; execution
semantics are already fresh in both lanes. Cached and fresh lanes have a
five-second advisory budget and target; release keeps its 30-second enforced
budget. Package/test overages still use isolated confirmation.

Use this before large edits and after fixes when an agent needs a single machine-readable status snapshot.

Recommended agent loop:

```text
scenery doctor --json
scenery harness self --quick --summary --write
cat .scenery/harness/agent-context.json
# implement
scenery harness self --summary --write
```

For release-risk changes, also run:

```text
scenery harness self --release --summary --write
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
scenery harness ui --json --write
```

For managed database changes, the default self-harness runs the live
Postgres service probe when Docker is reachable (and records an explicit
skip when it is not):

```text
scenery harness self --json --write
```

Use `--quick` only when you intentionally need the smaller self-harness loop
without live branch-substrate coverage.

The command runs:

- `scenery check --json`
- `scenery inspect app --json`
- `scenery inspect routes --json`
- `scenery inspect services --json`
- `scenery inspect endpoints --json`
- `scenery inspect build --json`
- `scenery inspect paths --json`
- `scenery traces list --json`
- `scenery metrics list --json`
- `scenery inspect docs --json`

`scenery traces list --json` and `scenery metrics list --json` are included
as beta diagnostic inputs for agents. Their schema versions are useful for
automation, but their rollup and backend-selection semantics are not stable v0
API yet; see [local-contract.md](local-contract.md).

`scenery harness ui --json` is the implemented browser-backed dashboard route
check. It starts a temporary dashboard target unless `--dashboard-url` is
provided, visits stable dashboard routes, runs route-specific semantic journeys,
checks durable `data-scenery-ui` markers, and writes screenshots, DOM snapshots,
console, and network artifacts under `.scenery/harness/ui/`. The route journeys
prove behavior such as API Explorer endpoint/form rendering, service metadata,
trace empty/table/detail states, database availability or intentional empty
states, cron status, and durable/worker status cards.

`scenery inspect harness --json` reads the latest app, self, and UI harness
outputs from `.scenery/harness/` and returns their artifacts plus normalized
evidence records. Focused drill-down commands read bounded topic detail without
opening the full archive:

```text
scenery inspect harness artifact test-timing --json
scenery inspect harness artifact drift --json
scenery inspect harness diagnostics --severity warning --json
scenery inspect harness timing --top 10 --json
```

## Output

JSON output conforms to:

- [scenery.harness.result.v1.schema.json](schemas/scenery.harness.result.v1.schema.json)
- [scenery.harness.artifact.v1.schema.json](schemas/scenery.harness.artifact.v1.schema.json)
- [scenery.inspect.harness.v1.schema.json](schemas/scenery.inspect.harness.v1.schema.json)
- [scenery.harness.ui.v1.schema.json](schemas/scenery.harness.ui.v1.schema.json)
- [scenery.harness.ui.dom.v1.schema.json](schemas/scenery.harness.ui.dom.v1.schema.json)

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

When `scenery harness ui --json --write` is present, the browser harness writes:

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
read for each failure, exact rerun commands, changed-area recommended commands,
relevant active ExecPlans, recent failed harness artifacts, docs freshness, and
risk classification across runtime, CLI contract, dashboard, schema, release,
and ONLV-impacting changes.

For the scenery repo itself, `scenery harness self --summary --write` prints the
compact `scenery.harness.self.summary.v1` decision packet and writes:

```text
<repo-root>/.scenery/harness/self-latest.json
<repo-root>/.scenery/harness/self-summary-latest.json
```

Use `scenery harness self --json=full --write` only when stdout must contain the
full `scenery.harness.self.v1` archive. Agents should prefer artifacts and focused
inspect commands over pasting `.scenery/harness/self-latest.json` into chat.

The self harness validates the local scenery development loop:

- `go test ./cmd/scenery ./internal/devdash ./runtime`
- docs knowledge base integrity through `docs/knowledge.json`
- local markdown links and schema JSON syntax
- `scenery inspect docs --json`
- docs review-due and stale summaries from `scenery inspect docs --json`
- architecture checks for dependency policy, package boundaries, generated-file hygiene, and oversized source files
- parallel dev-session safety plus managed Postgres database isolation
  (distinct per-worktree databases and database URLs) when Docker is
  reachable
- dashboard UI typecheck and build
- dashboard build freshness
- worktree-local `go build -o .scenery/harness/bin/scenery ./cmd/scenery`
- local `.scenery/harness/bin/scenery` freshness against repo sources

The default self-harness still runs the complete Go suite and writes
`.scenery/harness/test-timing-latest.json`, but the cached and fresh wall-clock
duration budgets are advisory. The artifact records the full-suite duration
separately from isolated confirmation time and keeps contended observations
separate from confirmed slow tests. Release mode enforces the 30-second total
budget when maintainers intentionally want a hard speed gate.

## Design Rules

- Keep `AGENTS.md` short. It should point to source-of-truth docs instead of becoming an encyclopedia.
- Prefer stable JSON commands over terminal scraping.
- Inspect commands are the API; generated files are cache.
- Put remediation text in diagnostics so agents know what to do next.
- Promote repeated review feedback into docs, schemas, or mechanical checks.
- When docs and behavior disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift.

## Doc Gardening

Run `scenery inspect docs --json` before non-trivial repo changes and use its
`summary.review_due_count`, document-level `review_due`, and `stale` fields to
choose small cleanup work. `scenery harness self --summary --write` includes the
same docs knowledge signals in its summaries, so review-due documentation is
visible during ordinary validation instead of being hidden in prose.

Keep `docs/knowledge.json` aligned with agent-facing source-of-truth docs. Until
active ExecPlan indexing is generated by the toolchain, every active ExecPlan in
`docs/plans/active.md` must also have a document entry in `docs/knowledge.json`.

Use the same rule for all drift: when docs and behavior disagree, the same PR
must either fix the affected docs or open/update an ExecPlan that records the
drift, owner, and intended resolution path.

## Architecture Checks

`scenery harness self` includes a fast `architecture checks` step.

Hard failures:

- direct Go dependencies must be listed in the self-harness allowlist with a concrete rationale
- forbidden CLI/router/color framework imports are rejected in source
- packages outside `cmd/scenery` may not import `scenery.sh/cmd/scenery`
- required generated/vendored ignore markers must exist in `.gitignore` and `.gitattributes`
- non-generated source/code files over 2500 lines are rejected; Markdown docs are not subject to line-count size checks
- UI code must use the scenery `@scenery` shadcn registry namespace and wrapper script, registry items must declare safe source/target files, and screens must not import legacy `components/ui`, vendor shadcn, Radix, or low-level styling utilities directly

Warnings:

- non-generated source/code files over 1000 lines; Markdown docs are not subject to line-count size checks
- cgo imports, because they require native build handling
- `.DS_Store` files found in the working tree
- long or advanced `className` literals, including common expression forms such as `cn(...)`, template literals, and conditional literals, outside scenery primitives/layouts/vendor while existing dashboard screens are migrated
The dependency allowlist is intentionally small and lives in code next to the check. New direct dependencies should be rare and must include the reason they justify the added maintenance surface.

## Non-Goals

- The harness is not a CI replacement.
- It does not run external services by itself.
- It does not invent architecture rules. Add new checks only when the repo has a concrete invariant worth enforcing.
