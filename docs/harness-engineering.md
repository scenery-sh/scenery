# onlava Harness Engineering

onlava treats agent support as a runtime feature, not as prompt folklore.

The harness contract gives Codex and other agents a short feedback loop:

1. discover the app through stable inspect command output
2. compile the generated runtime exactly like `onlava serve` would
3. report diagnostics as structured JSON
4. expose inspect outputs and artifact paths without scraping terminal text
5. persist the latest harness result when requested

## Command

```text
onlava harness [--app-root <path>] [--json] [--write]
onlava harness self [--repo-root <path>] [--summary|--json|--json=summary|--json=full] [--write]
onlava harness ui [--app-root <path>] [--dashboard-url <url>] [--headed] [--json] [--write]
onlava inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]
```

Use this before large edits and after fixes when an agent needs a single machine-readable status snapshot.

Recommended agent loop:

```text
onlava doctor --json
onlava harness self --quick --summary --write
cat .onlava/harness/agent-context.json
# implement
onlava harness self --summary --write
```

For release-risk changes, also run:

```text
onlava harness self --release --summary --write
scripts/release-gate.sh
```

For dashboard route or UI behavior changes, also run:

```text
onlava harness ui --json --write
```

The command runs:

- `onlava check --json`
- `onlava inspect app --json`
- `onlava inspect routes --json`
- `onlava inspect services --json`
- `onlava inspect endpoints --json`
- `onlava inspect wire --json`
- `onlava inspect build --json`
- `onlava inspect paths --json`
- `onlava traces list --json`
- `onlava metrics list --json`
- `onlava inspect docs --json`

`onlava traces list --json` and `onlava metrics list --json` are included
as beta diagnostic inputs for agents. Their schema versions are useful for
automation, but their rollup and backend-selection semantics are not stable v0
API yet; see [local-contract.md](local-contract.md).

`onlava harness ui --json` is the implemented browser-backed dashboard route
check. It starts a temporary dashboard target unless `--dashboard-url` is
provided, visits stable dashboard routes, runs route-specific semantic journeys,
checks durable `data-onlava-ui` markers, and writes screenshots, DOM snapshots,
console, and network artifacts under `.onlava/harness/ui/`. The route journeys
prove behavior such as API Explorer endpoint/form rendering, service metadata,
trace empty/table/detail states, database availability or intentional empty
states, cron status, and temporal/worker status cards.

`onlava inspect harness --json` reads the latest app, self, and UI harness
outputs from `.onlava/harness/` and returns their artifacts plus normalized
evidence records. Focused drill-down commands read bounded topic detail without
opening the full archive:

```text
onlava inspect harness artifact test-timing --json
onlava inspect harness artifact drift --json
onlava inspect harness diagnostics --severity warning --json
onlava inspect harness timing --top 10 --json
```

## Output

JSON output conforms to:

- [onlava.harness.result.v1.schema.json](schemas/onlava.harness.result.v1.schema.json)
- [onlava.harness.artifact.v1.schema.json](schemas/onlava.harness.artifact.v1.schema.json)
- [onlava.inspect.harness.v1.schema.json](schemas/onlava.inspect.harness.v1.schema.json)
- [onlava.harness.ui.v1.schema.json](schemas/onlava.harness.ui.v1.schema.json)
- [onlava.harness.ui.dom.v1.schema.json](schemas/onlava.harness.ui.dom.v1.schema.json)

When `--write` is present, onlava writes:

```text
<app-root>/.onlava/harness/latest.json
```

That file is intentionally stable. Agents should use it as the latest local validation snapshot instead of guessing from cache directories or parsing human logs.

Every failed or expensive step should include an `evidence` object with the
command, cwd, start time, duration, exit code, stdout/stderr tails, artifact
references, and a copy-pasteable `repro_command`. When `--write` is present,
large evidence payloads such as Go test JSONL are written under:

```text
<root>/.onlava/harness/artifacts/<run-id>/
```

The same evidence model is shared by the app harness, self-harness, UI harness,
release gate, and future ONLV gates so agents can inspect failures without
scraping terminal scrollback.

When `onlava harness ui --json --write` is present, the browser harness writes:

```text
<app-root>/.onlava/harness/ui/latest.json
<app-root>/.onlava/harness/ui/screenshots/<route>.png
<app-root>/.onlava/harness/ui/dom/<route>.json
<app-root>/.onlava/harness/ui/console.jsonl
<app-root>/.onlava/harness/ui/network.jsonl
```

The DOM snapshots are compact semantic snapshots of elements carrying
`data-onlava-ui`, not full HTML dumps. They exist so agents can reproduce,
repair, restart, and verify browser behavior from machine-readable route state.

The self-harness writes `.onlava/harness/agent-context.json` as the default
handoff file for agents. It includes current failing steps, the first file to
read for each failure, exact rerun commands, changed-area recommended commands,
relevant active ExecPlans, recent failed harness artifacts, docs freshness, and
risk classification across runtime, CLI contract, dashboard, schema, release,
and ONLV-impacting changes.

For the onlava repo itself, `onlava harness self --summary --write` prints the
compact `onlava.harness.self.summary.v1` decision packet and writes:

```text
<repo-root>/.onlava/harness/self-latest.json
<repo-root>/.onlava/harness/self-summary-latest.json
```

Use `onlava harness self --json=full --write` only when stdout must contain the
full `onlava.harness.self.v1` archive. Agents should prefer artifacts and focused
inspect commands over pasting `.onlava/harness/self-latest.json` into chat.

The self harness validates the local onlava development loop:

- `go test ./cmd/onlava ./internal/devdash ./runtime`
- docs knowledge base integrity through `docs/knowledge.json`
- local markdown links and schema JSON syntax
- `onlava inspect docs --json`
- docs review-due and stale summaries from `onlava inspect docs --json`
- architecture checks for dependency policy, package boundaries, generated-file hygiene, and oversized source files
- parallel dev-session safety and local Neon generated dev-cell start/stop plus branch pin/lease lifecycle safety
- dashboard UI typecheck and build
- dashboard build freshness
- worktree-local `go build -o .onlava/harness/bin/onlava ./cmd/onlava`
- local `.onlava/harness/bin/onlava` freshness against repo sources

The default self-harness still runs the complete Go suite and writes
`.onlava/harness/test-timing-latest.json`, but the wall-clock duration budget is
advisory. Timing overages are warnings so ordinary feature work is not blocked by
machine and scheduler variance. Release-mode self-harness may enforce the total
duration budget when maintainers intentionally want a hard speed gate.

## Design Rules

- Keep `AGENTS.md` short. It should point to source-of-truth docs instead of becoming an encyclopedia.
- Prefer stable JSON commands over terminal scraping.
- Inspect commands are the API; generated files are cache.
- Put remediation text in diagnostics so agents know what to do next.
- Promote repeated review feedback into docs, schemas, or mechanical checks.
- When docs and behavior disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift.

## Doc Gardening

Run `onlava inspect docs --json` before non-trivial repo changes and use its
`summary.review_due_count`, document-level `review_due`, and `stale` fields to
choose small cleanup work. `onlava harness self --summary --write` includes the
same docs knowledge signals in its summaries, so review-due documentation is
visible during ordinary validation instead of being hidden in prose.

Keep `docs/knowledge.json` aligned with agent-facing source-of-truth docs. Until
active ExecPlan indexing is generated by the toolchain, every active ExecPlan in
`docs/plans/active.md` must also have a document entry in `docs/knowledge.json`.

Use the same rule for all drift: when docs and behavior disagree, the same PR
must either fix the affected docs or open/update an ExecPlan that records the
drift, owner, and intended resolution path.

## Architecture Checks

`onlava harness self` includes a fast `architecture checks` step.

Hard failures:

- direct Go dependencies must be listed in the self-harness allowlist with a concrete rationale
- forbidden CLI/router/color framework imports are rejected in source
- packages outside `cmd/onlava` may not import `github.com/pbrazdil/onlava/cmd/onlava`
- required generated/vendored ignore markers must exist in `.gitignore` and `.gitattributes`
- non-generated source/code files over 2500 lines are rejected; Markdown docs are not subject to line-count size checks
- UI code must use the onlava `@onlava` shadcn registry namespace and wrapper script, registry items must declare safe source/target files, and screens must not import legacy `components/ui`, vendor shadcn, Radix, or low-level styling utilities directly

Warnings:

- non-generated source/code files over 1000 lines; Markdown docs are not subject to line-count size checks
- cgo imports, because they require native build handling
- `.DS_Store` files found in the working tree
- long or advanced `className` literals, including common expression forms such as `cn(...)`, template literals, and conditional literals, outside onlava primitives/layouts/vendor while existing dashboard screens are migrated
The dependency allowlist is intentionally small and lives in code next to the check. New direct dependencies should be rare and must include the reason they justify the added maintenance surface.

## Non-Goals

- The harness is not a CI replacement.
- It does not run external services by itself.
- It does not invent architecture rules. Add new checks only when the repo has a concrete invariant worth enforcing.
