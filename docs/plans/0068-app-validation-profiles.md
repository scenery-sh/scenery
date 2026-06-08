# App Validation Profiles

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Onlava has two related but different validation needs. `onlava harness` proves
that the framework-owned app model and local introspection surfaces are healthy:
check, inspect, routes, services, wire, build paths, traces, metrics, and stable
evidence artifacts. App repositories also need their own quality gates: repo
harnesses, frontend type checks, linters, UI harnesses, backend tests, and
domain-specific smoke tests.

This plan adds validation profiles as a first-class app lifecycle layer:

```sh
onlava validate quick --json --write
onlava validate full --json --write
onlava validate changed --base origin/main --json --write
onlava inspect validation --json
```

The key contract is that `onlava harness` remains framework-owned, deterministic
app-model proof, while `onlava validate <profile>` is an app-owned quality gate
defined in `.onlava.json`. Validation profiles are powered by the existing
configured task primitive and share harness-style evidence/artifact machinery,
but they do not change core harness semantics by default.

The observable end state is that an agent can inspect available validation
profiles, dry-run a resolved validation graph, execute a named profile, and read
a stable JSON result from `.onlava/harness/validation/latest.json` without
scraping terminal output or reverse-engineering repo-local `just` recipes.

## Progress

- [x] 2026-06-08: Created ExecPlan `0068-app-validation-profiles.md` from the requested validation profiles brief.
- [x] 2026-06-08: Added `validation` config structs and schema support for `.onlava.json`.
- [x] 2026-06-08: Added read-only profile inspection, list, graph, and dry-run CLI surfaces.
- [x] 2026-06-08: Added sequential profile execution with harness-style evidence and JSON artifacts.
- [x] 2026-06-08: Added changed-file profile selection with selection reasoning.
- [x] 2026-06-08: Added optional harness bridge and updated docs, schemas, tests, and agent instructions.
- [x] 2026-06-08: Validated with `go test ./cmd/onlava`, `go test ./...`, JSON schema parsing, docs inspection, and source-driven CLI smoke tests.

## Surprises & Discoveries

- 2026-06-08: `.onlava/harness/bin/onlava` was not present in this worktree, so the initial docs inspection used the installed `onlava inspect docs --json` command instead. That command reported one review-due document, `docs/ui-agent-contract.md`, which is unrelated UI documentation and should remain visible without being folded into this CLI/config plan.
- 2026-06-08: `internal/app/root.go` rejects unknown `.onlava.json` fields through both reflection-based field checks and `json.Decoder.DisallowUnknownFields`. Adding `validation` must update Go structs and `docs/schemas/onlava.config.v1.schema.json` in the same implementation change.
- 2026-06-08: `cmd/onlava/task.go` already contains most of the lifecycle vocabulary needed by validation profiles: configured tasks, code-backed tasks, `task:<name>`, `check`, `test`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`.
- 2026-06-08: `cmd/onlava/harness.go` already owns the core harness result shape and `.onlava/harness/latest.json`; validation should reuse its evidence/artifact ideas without inlining large validation results into core harness output.
- 2026-06-08: Focused package tests passed early, but `go test ./...` caught that a helper used by validation only existed in test files. The fix was to add a validation-local production helper instead of reusing test utilities.
- 2026-06-08: Code-backed tasks and DB lifecycle commands had stdout paths that bypassed normal writers. Validation now captures code-task output through script options and redirects hardcoded DB command stdout/stderr during JSON-mode validation steps.
- 2026-06-08: Parent profile env overlays need to flow through nested `profile:` steps. Resolved validation steps now carry the inherited env map so composite gates behave predictably.

## Decision Log

- 2026-06-08: Use the top-level config key `validation` instead of `harness.profiles` or `harness.commands`. Rationale: app-owned quality gates should not be confused with the framework-owned harness contract.
- 2026-06-08: Add a top-level `onlava validate` command and `onlava inspect validation --json`. Rationale: validation is a lifecycle action like `task`, `check`, and `harness`, while inspect remains the read-only machine-readable discovery surface.
- 2026-06-08: Keep profile `steps` as strings in phase 1. Rationale: the existing task step grammar is compact and enough for the first surface; object-shaped steps with timeouts, retries, or optional behavior can come later when needed.
- 2026-06-08: Require shell commands to live behind configured tasks. Rationale: profile steps should remain readable and safe; `tasks.<name>.run` is already the explicit shell escape hatch.
- 2026-06-08: Store validation results under `.onlava/harness/validation/`. Rationale: validation should share harness-style evidence and artifact conventions while remaining a distinct result contract.
- 2026-06-08: Defer changed-file profile selection and harness bridging until after profile inspection and execution are stable. Rationale: changed selection and `harness --with-validation` are valuable, but the core contract is profiles, graphs, execution, and evidence.

## Outcomes & Retrospective

Completed on 2026-06-08.

Shipped:

- `.onlava.json` `validation.default` and `validation.profiles` config, including profile metadata, costs, path globs, env overlays, steps, and advisory artifacts.
- `onlava inspect validation --json`, `onlava validate list|inspect|graph`, `onlava validate <profile> --dry-run --json`, `onlava validate <profile> --json --write`, and `onlava validate changed --base <ref>`.
- Sequential fail-fast validation execution with profile nesting, configured tasks, code-backed tasks, built-ins, output capture, evidence tails, repro commands, and `.onlava/harness/validation/` result artifacts.
- Optional `onlava harness --with-validation[=<profile>]` bridge that keeps core harness output compact by linking to validation results instead of inlining them.
- JSON schemas, CLI usage, local contract docs, agent guide updates, skill updates, app cookbook recipe, README command list, self-harness schema inventory, and focused tests.

Validation:

- `go test ./cmd/onlava` passed.
- `go test ./...` passed.
- `python3 -m json.tool docs/knowledge.json docs/schemas/*.json` passed.
- `onlava inspect docs --json` passed with 42 documents, 0 missing, 1 review-due, and 0 stale.
- Source-driven smoke tests with `go run ./cmd/onlava` passed for `inspect validation`, `validate --dry-run`, `validate --json --write`, and `harness --with-validation`.

## Context and Orientation

Start with these files and surfaces:

- `internal/app/root.go` defines `.onlava.json` structs, root discovery, and unknown-field rejection.
- `docs/schemas/onlava.config.v1.schema.json` is the machine-readable config schema and must accept the same `validation` shape as `internal/app/root.go`.
- `cmd/onlava/main.go` dispatches top-level commands and owns usage text.
- `cmd/onlava/task.go` owns configured tasks, code-backed task targets, task graphs, built-in lifecycle steps, and task execution.
- `cmd/onlava/harness.go`, `cmd/onlava/harness_artifacts.go`, and `cmd/onlava/inspect_harness.go` define current harness result/evidence/artifact conventions.
- `cmd/onlava/inspect.go` routes `onlava inspect ... --json` subjects.
- `docs/schemas/onlava.task.list.v1.schema.json`, `docs/schemas/onlava.task.inspect.v1.schema.json`, and `docs/schemas/onlava.task.graph.v1.schema.json` are useful references for list/inspect/graph response style.
- `docs/local-contract.md` is the CLI grammar, JSON schema, artifact path, and stability contract.
- `docs/agent-guide.md`, `SKILL.md`, and `docs/app-development-cookbook.md` are the agent and app-facing workflow layers that should point agents toward `onlava validate` when it ships.
- `docs/knowledge.json` indexes active ExecPlans until deterministic plan indexing exists.

The new `.onlava.json` shape should be:

```json
{
  "validation": {
    "default": "quick",
    "profiles": {
      "quick": {
        "description": "Fast agent handoff gate.",
        "cost": "low",
        "steps": ["harness:core", "task:repo-harness"]
      },
      "backend": {
        "description": "Backend correctness gate.",
        "cost": "medium",
        "paths": ["**/*.go", "go.mod", "go.sum", "**/*.sql", "**/*.hcl"],
        "steps": ["profile:quick", "test:go"]
      },
      "full": {
        "description": "Full local quality gate.",
        "cost": "high",
        "steps": ["profile:backend"]
      }
    }
  }
}
```

Recommended Go structs:

```go
type ValidationConfig struct {
    Default  string                             `json:"default"`
    Profiles map[string]ValidationProfileConfig `json:"profiles"`
}

type ValidationProfileConfig struct {
    Description string            `json:"description"`
    Cost        string            `json:"cost"`
    Paths       []string          `json:"paths"`
    Steps       []string          `json:"steps"`
    Env         map[string]string `json:"env"`
    Artifacts   []string          `json:"artifacts"`
}
```

Profile names follow the configured-task name rule:
`[A-Za-z0-9_][A-Za-z0-9_-]*`. They cannot contain `:`.

## Milestones

1. Read-Only Profile Model: add config structs, schema support, validation
   diagnostics, `onlava inspect validation --json`, `onlava validate list
   --json`, `onlava validate inspect <profile> --json`, `onlava validate graph
   <profile> --json`, and `onlava validate <profile> --dry-run --json`.
2. Executor and Evidence: add `onlava validate [profile] [--json] [--write]`
   with sequential fail-fast execution, nested profiles, task/builtin steps,
   stable JSON output, and `.onlava/harness/validation/` artifacts.
3. Changed Profiles: add `onlava validate changed --base <ref>` with
   deterministic path matching, default-profile inclusion, nested-profile
   dedupe, and selection reasoning in JSON.
4. Harness Bridge: add optional `onlava harness --with-validation` and
   `onlava harness --with-validation=<profile>` only after validation is
   stable, with a pointer to the validation result instead of an inlined result.
5. Adoption and Docs: update contracts, schemas, self-harness checks, agent
   workflows, and app cookbook examples so agents can choose quick, changed, or
   full validation gates without app-specific prose.

## Plan of Work

First add the config model and diagnostics without executing commands. The
implementation should parse `validation.default` and `validation.profiles`,
reject invalid profile names, reject invalid costs, report empty profile steps,
detect unknown referenced profiles and configured tasks, and detect profile
cycles before any run. This is the low-risk phase because it proves the config
surface and read-only JSON contracts before adding process execution.

Next add the CLI surfaces. `onlava inspect validation --json` should be
read-only and return the profiles, default, step summaries, artifacts, and
diagnostics. `onlava validate list|inspect|graph` should mirror the existing
task command style where practical. `onlava validate <profile> --dry-run
--json` should return the exact execution plan without running shell commands,
configured tasks, or built-ins.

Then add execution by extracting a reusable lifecycle step runner from the
current task implementation instead of duplicating `runTaskStep`. Validation
steps must support:

```text
profile:<name>
task:<name>
task:<domain>:<name>
harness:core
harness:ui
check
test
test:go
generate
generate:client
generate:sqlc
db:apply
db:seed
db:setup
```

`harness` may remain an alias for `harness:core`, but `harness:core` should be
documented as canonical. Step stdout and stderr should not be mixed into stdout
when `--json` is set; JSON mode should capture tails and artifact paths.

After execution is stable, implement changed-file selection. This should use
`git diff --name-only <base>...HEAD`, slash-normalize paths, support simple
glob matching with `*`, `**`, `?`, include the default profile, include matching
profiles by `paths`, resolve nested profiles, dedupe by profile name, and return
the selection reasoning in the validation JSON result.

Finally add the optional harness bridge. `onlava harness --with-validation`
should run the core harness first, run the requested validation profile, and add
a small pointer object to the harness result:

```json
{
  "validation": {
    "profile": "full",
    "ok": false,
    "result_path": ".onlava/harness/validation/latest.json"
  }
}
```

Do not make validation part of default harness behavior. Core harness should
stay fast, deterministic, and framework-owned.

## Concrete Steps

1. Add ``Validation ValidationConfig `json:"validation"` `` to
   `internal/app.Config`, plus `ValidationConfig` and
   `ValidationProfileConfig` structs.
2. Update `docs/schemas/onlava.config.v1.schema.json` with a top-level
   `validation` property and a `$defs.validationProfile` definition. Reject
   unknown validation fields with `additionalProperties: false`.
3. Add validation profile helpers, either in a new `cmd/onlava/validate.go` or a
   small internal package if the logic becomes shared by `inspect` and
   execution. Keep CLI parsing in `cmd/onlava`.
4. Implement profile diagnostics for invalid names, invalid cost values,
   missing default profile, empty steps, unsupported steps, unknown referenced
   profiles, unknown referenced configured tasks, invalid code task targets,
   invalid globs, and profile cycles.
5. Add `onlava inspect validation --json [--app-root <path>]` in
   `cmd/onlava/inspect.go`.
6. Add top-level `validate` dispatch and usage text in `cmd/onlava/main.go`.
7. Implement `onlava validate list --json [--app-root <path>]`.
8. Implement `onlava validate inspect <profile> --json [--app-root <path>]`.
9. Implement `onlava validate graph <profile> --json [--app-root <path>]`.
   Graph output should include profile nodes, task nodes, builtin nodes, and
   edges, with deterministic ordering.
10. Implement `onlava validate <profile> --dry-run --json [--app-root <path>]`
    and default-profile resolution for `onlava validate --dry-run --json`.
11. Refactor task step execution so validation can call configured tasks,
    code-backed tasks, and built-ins without copying the current `runTaskStep`
    switch wholesale.
12. Implement `onlava validate [profile] [--json] [--write] [--app-root
    <path>]` with sequential, fail-fast execution.
13. Add profile env overlays. Profile-level env overlays app dotenv/process env
    for descendant steps; task-level env overlays profile env for configured
    task shell commands.
14. Define result schemas:
    - `onlava.inspect.validation.v1`
    - `onlava.validation.list.v1`
    - `onlava.validation.inspect.v1`
    - `onlava.validation.graph.v1`
    - `onlava.validation.plan.v1`
    - `onlava.validation.result.v1`
15. Add JSON schema files under `docs/schemas/` for the new response contracts.
16. Write validation results to:
    - `.onlava/harness/validation/latest.json`
    - `.onlava/harness/validation/<profile>-latest.json`
    - `.onlava/harness/validation/artifacts/<run-id>/`
17. Implement human text output for `onlava validate <profile>` that shows each
    step, status, duration, failing repro command, and relevant artifact path.
18. Implement `onlava validate changed --base <ref> [--json] [--write]
    [--dry-run] [--app-root <path>]` after named-profile execution is stable.
19. Add optional `onlava harness --with-validation[=<profile>]` and update
    `onlava.inspect.harness.v1` only after validation result writing is stable.
20. Update `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`,
    `README.md`, and `docs/app-development-cookbook.md` with the new command
    grammar, JSON contracts, artifact paths, and recommended agent workflows.
21. Update `docs/knowledge.json` for new schemas, changed docs, and this active
    ExecPlan entry.

## Validation and Acceptance

Acceptance criteria:

- `.onlava.json` accepts a valid `validation` block and still rejects unknown
  validation fields with field-path diagnostics.
- `onlava inspect validation --json` returns
  `onlava.inspect.validation.v1`, including app metadata, default profile,
  profile records, artifacts, and diagnostics.
- `onlava validate list --json` returns all profiles with description, cost,
  paths, step count, and default status.
- `onlava validate inspect full --json` returns the resolved profile definition,
  nested `profile:` references, referenced task records, expected artifacts,
  diagnostics, and config source path.
- `onlava validate graph full --json` returns deterministic profile, task, and
  builtin nodes and edges, and cycles fail before any command runs.
- `onlava validate full --dry-run --json` returns the execution plan and does
  not execute shell, tasks, built-ins, code-backed tasks, or harness commands.
- `onlava validate full --json --write` emits parseable JSON on stdout, captures
  child output into evidence/artifacts or bounded tails, writes stable latest
  files, and exits non-zero through a silent CLI error when the result is not OK.
- Explicit profile execution does not use `paths`; `paths` only affects
  `onlava validate changed`.
- `onlava validate changed --base origin/main --json --write` includes the
  default profile, selects matching profiles by changed paths, dedupes nested
  profiles, and reports matched files and patterns.
- `onlava harness --json --write` remains unchanged unless
  `--with-validation` is explicitly set.
- Documentation, schemas, tests, and harness output agree on the command grammar
  and JSON shapes.

Validation commands for implementation work:

```sh
go test ./...
go test ./cmd/onlava
onlava inspect docs --json
onlava harness self --summary --write
```

For config/schema work, also run:

```sh
python3 -m json.tool docs/schemas/onlava.config.v1.schema.json >/dev/null
python3 -m json.tool docs/knowledge.json >/dev/null
```

For runtime behavior, use a temporary fixture app with configured tasks:

```sh
onlava validate list --json --app-root <fixture-app>
onlava validate inspect full --json --app-root <fixture-app>
onlava validate graph full --json --app-root <fixture-app>
onlava validate full --dry-run --json --app-root <fixture-app>
onlava validate full --json --write --app-root <fixture-app>
```

## Idempotence and Recovery

The read-only inspection and dry-run commands must be safe to rerun at any time.
They should not create files, start services, mutate databases, or execute
shell commands.

Execution with `--write` may overwrite stable latest files under
`.onlava/harness/validation/`, which is expected. Each run should also place
run-specific artifacts under a unique run ID so a failed or interrupted run can
be inspected after the fact. Rerunning the same profile should be the recovery
path for ordinary failures.

If a configured task fails halfway, Onlava should report the failed step,
reproducible command, working directory, exit code, output tail, and artifact
paths. Recovery is to fix the task or app state and rerun the same
`onlava validate ...` command. Database-mutating steps such as `db:apply`,
`db:seed`, and `db:setup` keep their existing idempotence and failure semantics;
validation must not hide or retry those mutations.

Cycle detection, unknown references, invalid globs, invalid names, unsupported
steps, and missing default profiles should fail before execution starts. That
makes retries safe after editing `.onlava.json`.

## Artifacts and Notes

Proposed result shape:

```json
{
  "schema_version": "onlava.validation.result.v1",
  "ok": false,
  "generated_at": "2026-06-08T12:00:00Z",
  "app": {
    "name": "demo",
    "id": "demo-dev",
    "root": "/repo/demo",
    "config_path": "/repo/demo/.onlava.json"
  },
  "profile": "full",
  "selection": {
    "mode": "explicit",
    "requested": ["full"],
    "resolved_profiles": ["full", "backend", "quick"]
  },
  "steps": [
    {
      "id": "full/backend/quick/harness:core",
      "name": "harness:core",
      "kind": "builtin",
      "ok": true,
      "duration_ms": 1200,
      "evidence": {
        "command": ["onlava", "harness", "--app-root", "/repo/demo", "--json"],
        "cwd": "/repo/demo",
        "exit_code": 0,
        "repro_command": "onlava harness --app-root /repo/demo --json"
      }
    }
  ],
  "artifacts": [
    {
      "path": ".onlava/harness/validation/artifacts/<run-id>/repo-harness.stdout.txt",
      "kind": "stdout"
    }
  ],
  "next_actions": [
    "Fix task repo-harness, then rerun: onlava validate full --json --write"
  ],
  "wrote": ".onlava/harness/validation/latest.json"
}
```

Proposed inspect shape:

```json
{
  "schema_version": "onlava.inspect.validation.v1",
  "app": {},
  "default": "quick",
  "profiles": [
    {
      "name": "quick",
      "description": "Fast agent handoff gate.",
      "cost": "low",
      "steps": ["harness:core", "task:repo-harness"],
      "paths": [],
      "artifacts": []
    }
  ],
  "diagnostics": []
}
```

Human output should stay compact and copy-pasteable:

```text
profile full
  ok  harness:core                 1.2s
  ok  task:repo-harness            0.4s
  ok  test:go                      8.8s
  fail task:pulse-ui-harness       5.3s

failed: task:pulse-ui-harness
repro: onlava task run pulse-ui-harness --app-root /repo/demo
artifact: test-results/ui-harness/diff-report.md
```

Features intentionally deferred from the first implementation:

- parallel steps
- object-shaped per-step config
- per-step timeouts
- retries
- `allow_failure`
- required artifacts
- remote execution or cache-aware execution
- automatic dependency installation
- CI matrix generation

## Interfaces and Dependencies

New CLI grammar:

```text
onlava validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]
onlava validate list [--app-root <path>] [--json]
onlava validate inspect <profile> [--app-root <path>] [--json]
onlava validate graph [<profile>] [--app-root <path>] --json
onlava validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]
onlava inspect validation --json [--app-root <path>]
```

Optional later bridge:

```text
onlava harness --with-validation
onlava harness --with-validation=<profile>
```

Supported profile steps:

```text
profile:<name>
task:<name>
task:<domain>:<name>
harness:core
harness:ui
harness
check
test
test:go
generate
generate:client
generate:sqlc
db:apply
db:seed
db:setup
```

New or changed docs and schemas:

- `docs/schemas/onlava.config.v1.schema.json`
- `docs/schemas/onlava.inspect.validation.v1.schema.json`
- `docs/schemas/onlava.validation.list.v1.schema.json`
- `docs/schemas/onlava.validation.inspect.v1.schema.json`
- `docs/schemas/onlava.validation.graph.v1.schema.json`
- `docs/schemas/onlava.validation.plan.v1.schema.json`
- `docs/schemas/onlava.validation.result.v1.schema.json`
- `docs/local-contract.md`
- `docs/agent-guide.md`
- `SKILL.md`
- `README.md`
- `docs/app-development-cookbook.md`
- `docs/knowledge.json`

The first implementation should avoid adding third-party dependencies. Use the
Go standard library where possible, including `filepath`/`path` helpers for
path normalization and a small in-repo glob matcher if the standard library
matching semantics are not enough for `**`.
