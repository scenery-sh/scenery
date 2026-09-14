# Agent Instruction Evaluation

This implements the six accepted recommendations in
[Plan 0191](plans/0191-task-scoped-agent-instructions.md). Measurements use
captured before bytes and the current worktree. Task exercises ran in this
main session, without subagents or changes to a user's application.

## Instruction Context

Counts are whitespace-separated words, including Markdown/frontmatter. They
measure the available entrypoint text, not observed model tokens or latency.
Root instructions are always relevant within Scenery; the app skill is loaded
only when its workflow applies. Reference sections remain available on demand.

| Entrypoint | Before | After | Reduction |
|---|---:|---:|---:|
| `AGENTS.md` | 2,500 | 1,924 | 23.0% |
| `SKILL.md` | 3,870 | 829 | 78.6% |

All 16 skill links resolve, including their section anchors. New app/config and
Go-loader child instructions contain 136 and 113 words respectively; they are
loaded only for those subtrees. The app skill routes to existing guide,
cookbook, and runbook sections instead of copying each workflow.

## Live Validation Selection

The before binary was preserved with SHA-256
`cdafe749363e4c5b0a8ee07c388e6feecb163458f32ebb21ef5a56e61d818e13`.
The corrected binary used for this comparison has SHA-256
`93d565ae0f25c18f6bf8ad77477a7e3e5f7465efd1550f411acc80f6969f607a`.
Both were invoked with `inspect docs --for-path <path> -o json` against the
same repository. The shared classifier remains the only path-policy owner.

| Queried path | Before commands | After commands |
|---|---|---|
| `README.md` | full verifier; repository Go suite | quick verifier |
| `internal/postgresname/name.go` | full verifier; package and repository Go tests | package and repository Go tests |
| `runtime/server.go` | full verifier; package and repository Go tests | unchanged |

Here quick means `go run ./scripts/verify --quick --summary --write`; full
omits `--quick`. Package tests are `go test ./internal/postgresname` or
`go test ./runtime`; repository tests are `go test ./...`.
Path inspection is prospective: the complete diff and root matrix still choose
the actual verification mode and the selected run records evidence. Full's
successful Go-suite step satisfies that same required suite for unchanged inputs.

The corrected CLI also preserves child verification commands and both committed
compiler/generator fixture regenerations. In-process tests cover selection and
full replacing quick when a child contributes quick. Root `README.md` and
`ARCHITECTURE.md` now classify as documentation, without broadly exempting
Markdown embedded elsewhere in product or fixture trees.

## Executed Task Exercises

Two disposable copies of `testdata/apps/basic` received identical task requests.
They are labeled before/after for the instruction exercise; both deliberately
used the same immutable framework source digest
`be75768fb7f39b12632491adb2906337c4bb813a7b6b802460b6147d4ee35c04`.
This holds implementation source constant while checking that the shortened
workflow retains the necessary acceptance steps. It is not a blind or independent
model A/B experiment: the same agent knew both instruction versions.

| Task request | Before exercise | After exercise | Acceptance |
|---|---|---|---|
| Correct the README typo `echos` | passed | passed | exact intended sentence and `git diff --check` |
| Make Echo return a space after its prefix | passed | passed | operation test fails on `echo:probe`, then passes on `echo: probe` |
| Change a running endpoint prefix to `ready: ` | passed | passed | HTTP 200 with `ready: probe`, a new process and implementation, and current source/build identity |

Each copy ran the selected absolute Scenery executable returned by
`framework use --source <Scenery checkout> -o json`. Fixture commands were:

```sh
scenery generate --target contracts -o json
go test ./...
scenery check -o json
scenery generate --check -o json
scenery harness -o json --write
scenery up --detach --wait ready -o json
scenery build --development -o json
scenery down -o json
```

The first Go-test failure in each copy was intentional acceptance evidence;
after the requested source correction it passed. The check, generation check,
Go tests, and app harness passed again after the distinct runtime source edit.
No successful top-level command was repeated against an unchanged source stage.
The required core harness also runs its own check step in both variants; that
existing overlap was retained. Both app
harnesses reported success for their selected checks; live HTTP supplied the
additional runtime proof that the core harness alone does not establish.

For each final HTTP response, contract and implementation revisions matched
the freshly built runtime bundle; its build-input digest matched the response
header. The manifest's `service/api.go` entry matched the current file SHA-256.
The contract stayed the same across the implementation-only change.

| Exercise | Initial API PID | Changed API PID | Matched implementation revision |
|---|---:|---:|---|
| before | 65113 | 67862 | `sha256:26db56bb5cbdb91c3fff64cd4dcbdec4e2ecb0284559e1ec06bdd431dc1eb827` |
| after | 70128 | 70177 | `sha256:5b7aebf65d154c3c0eca7801508e2a6fa55a963981f90d783739ffc14db7803b` |

Both owned runtimes were stopped with their selected executable. Their two
supervisor PIDs and four API PIDs were absent in a subsequent process check;
retained-root inspection contained no active exercise sessions. No database or
retained-state cleanup was requested, and other application owners were untouched.

There were no clarification or approval requests for these exercises. One initial
startup invocation used `-o jsonl` from command help; detached startup rejected it
before starting a process, and the diagnostic-directed `-o json` invocation
succeeded. This is a command-help discrepancy, not evidence of an instruction
version causing extra questions. Its rejected output is retained with the run.

## Preserved Guidance and Boundaries

| Displaced or compacted guidance | Current owner |
|---|---|
| Native source, provider locks, route groups and generated contracts | [Native loop](agent-guide.md#native-change-loop), [generated artifacts](agent-guide.md#generated-and-cache-artifacts), [HTTP cutovers](agent-guide.md#directory-group-http-cutovers) |
| Assistant privacy and special Scenery acceptance script | [Assistant change loop](agent-guide.md#assistant-change-loop) |
| Typed streaming, app code tasks and public Go/auth recipes | [Cookbook](app-development-cookbook.md), [auth permissions](agent-guide.md#standard-auth-application-permissions) |
| Framework selection, current producer, lifecycle and deployment | [Runtime command choice](agent-guide.md#runtime-command-choice) |
| Storage, SQL, migration, snapshots and retained-state repair | [Storage/database workflow](agent-guide.md#storage-and-databases) and its runbooks |
| Generated TypeScript, UI slots and app browser acceptance | [Client integration](agent-guide.md#typescript-client-integration), [application acceptance](agent-guide.md#application-validation-and-completion) |
| PostgreSQL dependency and compiled-SQL ownership | [App instructions](../internal/app/AGENTS.md) and architecture |
| Go package-loader and model separation | [Parse instructions](../internal/parse/AGENTS.md) and architecture |

Root and app instructions retain explicit delegation authorization, protection
of unrelated work, verified runtime/data ownership, no implicit data deletion,
no shared CLI installation during validation, current-only protocols, managed
generated roots and trusted plan approvals. The root keeps the absolute 100ms
Go-root policy, full repository test requirement, named external probes and
explicit release/timing boundaries. Existing permission authorizes continued
work within its scope; it does not authorize new external actions.

## Repository Validation and Limits

Repository validation completed:

- `go test ./cmd/scenery ./internal/repoinfo`: passed; the latter has no test files.
- `go test ./scripts/verify`: passed after instruction-format corrections.
- `golangci-lint run ./...`: passed, zero issues.
- `go run ./scripts/verify --summary --write`: passed with warnings, including
  the complete repository Go suite, vet, schema, drift and documentation checks.
  It reported 41 documentation freshness warnings and 21 architecture warnings
  in existing large files; none is an error in the changed instruction/routing
  surfaces.
- `git diff --check`: passed.

The first full verifier caught instruction-format and living-plan requirements;
those were corrected before the passing run. Concurrent work committed part of
this task in `6028a01d` and subsequently replaced the shared latest report with
a quick run. Final evidence is therefore captured directly from
`go run ./scripts/verify -o json --write` into this task's `full-final.json`,
which preserves the run's own full structured response.

The skill validator required PyYAML, absent in both available Python environments.
PyYAML 6.0.3 was installed only in the ignored task evidence directory; the stock
`quick_validate.py` then passed using that module path. No project dependency or
global Python installation changed.

This evaluation demonstrates less entrypoint text, corrected command selection,
retained safeguards, and successful task outcomes. It does not establish a causal
change in model speed, token use, compaction frequency or clarification rate.
No release certification or all-root timing audit was run; no product external
boundary was changed by the documentation-routing fix.

## Reproduction Evidence

Machine-local evidence remains under
`.scenery/harness/instruction-refresh-0191/`: before instruction snapshots,
`baseline.json`, routing JSON, reference checks, context counts, fixture command
records, HTTP responses, runtime bundles and process-cleanup observations.
`prepare-exercises.py` and `runtime-exercise.py` record the exact fixture actions;
`exercise-roots.json` locates the stopped disposable copies. These are ignored
local artifacts, not committed generated state.
