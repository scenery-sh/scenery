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

Self-harness uses Go's native test result cache by default, including substantial
final and release validation. `--fresh-tests` is reserved for explicit fresh
measurement or nondeterminism investigation; that lane reuses content-addressed
linked test binaries, executes test bodies with `-test.count=1`, and uses the
locally measured package parallelism of three. Cached and fresh lanes have a
five-second advisory budget and target; release keeps its 30-second enforced
budget. Only explicit fresh runs perform isolated timing confirmation.

Use this before large edits and after fixes when an agent needs a single machine-readable status snapshot.

Recommended agent loop:

```text
scenery doctor -o json
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
scenery harness ui -o json --write
```

For managed database changes, the default self-harness runs the live
Postgres service probe when Docker is reachable (and records an explicit
skip when it is not):

```text
scenery harness self -o json --write
```

Use `--quick` only when you intentionally need the smaller self-harness loop
without live branch-substrate coverage.

The command runs:

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
as beta diagnostic inputs for agents. Their schema versions are useful for
automation, but their rollup and backend-selection semantics are internal and unstable
API yet; see [local-contract.md](local-contract.md).

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
read for each failure, exact rerun commands, changed-area recommended commands,
relevant active ExecPlans, recent failed harness artifacts, docs freshness, and
risk classification across runtime, CLI contract, dashboard, schema, release,
and ONLV-impacting changes.

For the scenery repo itself, `scenery harness self --summary --write` prints the
compact `scenery.harness.self.summary` decision packet and writes:

```text
<repo-root>/.scenery/harness/self-latest.json
<repo-root>/.scenery/harness/self-summary-latest.json
```

Use `scenery harness self -o json --write` only when stdout must contain the
full `scenery.harness.self` archive. Agents should prefer artifacts and focused
inspect commands over pasting `.scenery/harness/self-latest.json` into chat.

The self harness validates the local scenery development loop:

- `go test ./cmd/scenery ./internal/devdash ./runtime`
- docs knowledge base integrity through `docs/knowledge.json`
- local markdown links and schema JSON syntax
- `scenery inspect docs --all -o json`
- docs review-due and stale summaries from `scenery inspect docs --all -o json`
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
- Repository validation instructions must not recommend `go install ./cmd/scenery`; the knowledge-contract step reserves shared CLI installation for an explicit human request.
- When docs and behavior disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift.

## Doc Gardening

Run `scenery inspect docs --for-path <path> -o json` before non-trivial repo
changes. It reuses the changed-area router to return applicable instruction
scopes, owning sections, active plans, schemas, and verification commands.
Use `scenery inspect docs --review-due -o json` to choose cleanup work and
`--all` only for complete catalog validation. `scenery harness self --summary
--write` includes the same docs freshness signals in its summaries.

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

Warnings:

- non-generated source/code files over 1000 lines; Markdown docs are not subject to line-count size checks
- cgo imports, because they require native build handling
- `.DS_Store` files found in the working tree
The dependency allowlist is intentionally small and lives in code next to the check. New direct dependencies should be rare and must include the reason they justify the added maintenance surface.

## Non-Goals

- The harness is not a CI replacement.
- It does not run external services by itself.
- It does not invent architecture rules. Add new checks only when the repo has a concrete invariant worth enforcing.
