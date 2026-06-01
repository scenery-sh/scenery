# onlava Script Runner

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

onlava needs a small, boring local script runner for domain-owned operational scripts such as data backfills, reconciliations, one-off inspections, and fixture loaders. The runner should let contributors invoke a script by app-local namespace:

```sh
onlava run billing:reconcile --dry-run --limit 100
```

The important constraint is isolation from normal app parsing and builds. Go scripts placed under an app root can otherwise be discovered by `packages.Load("./...")`, and multiple `package main` files in one directory can break type checking before onlava ever decides to ignore them. This feature must therefore define script conventions that are safe before package loading happens.

The final behavior should support simple single-file scripts and robust directory-per-script layouts for both Go and TypeScript, without coupling script discovery to onlava service discovery.

## Progress

- [x] 2026-06-01: Created this ExecPlan and linked it from `docs/plans/active.md`.
- [x] 2026-06-01: Implemented filesystem-first script discovery under app roots.
- [x] 2026-06-01: Added `onlava run list`, `onlava run inspect`, and `onlava run <domain>:<script>`.
- [x] 2026-06-01: Moved headless API execution to `onlava serve`, leaving `onlava run` dedicated to scripts.
- [x] 2026-06-01: Implemented safe Go and TypeScript execution conventions.
- [x] 2026-06-01: Added tests, docs, usage text, fixture coverage, and validation.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

- 2026-06-01: The design must not rely on filtering `.script.go` files after `packages.Load("./...")`; by then Go package loading may already have failed on duplicate `main` declarations or other script-only code.
- 2026-06-01: `0057-typed-lifecycle-graph-phase1.md` already exists in the working tree, so this plan uses the next historical ID, `0058`.
- 2026-06-01: Adding `.onlava.json` to `testdata/apps/script-fixture` made the self-harness fixture matrix treat it as a normal app. The fixture now includes a minimal public API package so script-runner coverage and normal app fixture checks both pass.
- 2026-06-01: `onlava harness self --json --write` now passes the script fixture matrix, but the overall harness remains red on the existing full-suite timing budget: this run reported 10.700s against the 7.000s target and points to `docs/plans/0050-test-suite-speed-hardening.md`.

## Decision Log

- Decision: Keep all local operational script commands under `onlava run`.
  Rationale: `onlava serve` now owns headless API execution, so `run` can be the ergonomic operational-script verb and discovery namespace without keeping a second `script` command.
  Date/Author: 2026-06-01 / Codex

- Decision: Discover scripts from the filesystem, not from `parse.App` or service discovery.
  Rationale: Scripts are local domain tooling and should remain usable when the app model has compile or type errors. This also keeps the namespace path-based rather than service-name-based.
  Date/Author: 2026-06-01 / Codex

- Decision: Require single-file Go scripts to use `//go:build ignore`.
  Rationale: Build constraints keep single-file scripts out of ordinary package loading while still allowing explicit file-path execution with `go run`.
  Date/Author: 2026-06-01 / Codex

- Decision: Treat ambiguous script matches as errors unless the user provides `--lang`.
  Rationale: Scripts often mutate data. Hidden language precedence between Go and TypeScript would be risky.
  Date/Author: 2026-06-01 / Codex

## Outcomes & Retrospective

Completed on 2026-06-01.

Implemented a narrow script runner in `cmd/onlava` with filesystem-first discovery, strict `<domain>:<script>` target parsing, ambiguity errors, Go and TypeScript layout support, and process execution from the app root. The command surface is `onlava run list|inspect` plus `onlava run <domain>:<script>` for executing a script target.

Single-file Go scripts must start with `//go:build ignore`; directory Go scripts use `go run ./<domain>/scripts/<script>`. TypeScript scripts prefer Bun and fall back to Node with `--import tsx`. Script processes receive app-root/app-id metadata and optional runtime env variables.

Validation completed:

- `go test ./cmd/onlava` passed.
- `go test ./...` passed.
- `git diff --check` passed.
- `go install ./cmd/onlava` passed.
- Focused fixture scenarios passed:
  - `onlava run list --app-root testdata/apps/script-fixture --json`
  - `onlava run inspect billing:reconcile --app-root testdata/apps/script-fixture --json`
  - `onlava run --app-root testdata/apps/script-fixture billing:reconcile --dry-run`
- `onlava harness self --json --write` wrote `.onlava/harness/self-latest.json`; all feature-relevant checks and the fixture matrix passed, while the overall harness remained red on the pre-existing full-suite timing budget.

## Context and Orientation

Relevant existing code:

```text
cmd/onlava/main.go
cmd/onlava/serve.go
cmd/onlava/worker.go
internal/app/root.go
internal/parse/parser.go
```

Current shape:

- `onlava serve` is the headless app runner; `onlava run` executes operational scripts.
- `internal/app/root.go` discovers the app root by walking to `.onlava.json`.
- The parser/build path uses Go package loading for app analysis.
- TypeScript worker code already has runtime selection conventions: prefer Bun, otherwise use Node with `tsx` where applicable.

The new script feature should not need the full parsed app model. It should start from:

```go
resolveAppRoot
app.DiscoverRoot
filesystem scan under app root
```

Script namespace:

```text
<domain>:<script>
```

maps to:

```text
<app-root>/<domain>/scripts/<script>...
```

The domain is a top-level path segment, not an onlava service name.

## Milestones

Milestone 1: Script resolver and model.

Implement a filesystem-first resolver that can list and resolve script targets without loading the app model. It should understand these candidates in order:

```text
<domain>/scripts/<name>.script.go
<domain>/scripts/<name>.script.ts
<domain>/scripts/<name>/main.go
<domain>/scripts/<name>/index.ts
```

Acceptance:

- `billing:reconcile` resolves under `billing/scripts`.
- Missing scripts produce clear errors with searched paths.
- Multiple language/layout matches fail with an ambiguity error unless `--lang go|typescript` is provided.
- Invalid target syntax fails before filesystem work.

Milestone 2: CLI surface.

Add canonical commands:

```sh
onlava run list [--app-root <path>] [--json]
onlava run inspect <domain>:<script> [--app-root <path>] [--lang go|typescript] [--json]
```

Add sugar:

```sh
onlava run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<script> [script args...]
```

Acceptance:

- `onlava run billing:reconcile --dry-run` executes the `billing:reconcile` script.
- Script flags must appear before the target. Arguments after `<domain>:<script>` are passed verbatim to the script.
- `--` remains accepted but is not required.

Milestone 3: Go execution.

Support single-file and directory-per-script Go layouts.

Single-file:

```text
billing/scripts/reconcile.script.go
```

must start with:

```go
//go:build ignore
```

Directory:

```text
billing/scripts/reconcile/main.go
billing/scripts/reconcile/helpers.go
```

Acceptance:

- Single-file Go scripts run by explicit file path from the app root.
- Directory Go scripts run with `go run ./billing/scripts/reconcile`.
- Single-file scripts missing `//go:build ignore` fail with an actionable error.
- Script execution uses `cmd.Dir = app root`.

Milestone 4: TypeScript execution.

Support:

```text
billing/scripts/reconcile.script.ts
billing/scripts/reconcile/index.ts
```

Acceptance:

- TypeScript scripts run from the app root.
- Runtime selection follows onlava's TypeScript worker convention: prefer Bun, otherwise use Node with `tsx`.
- Script args pass through unchanged.
- Missing TypeScript runtime produces an actionable error.

Milestone 5: Docs and validation.

Update CLI usage, local contract, and cookbook docs with supported layouts and argument parsing rules.

Acceptance:

- `docs/local-contract.md` documents the script command grammar.
- `docs/app-development-cookbook.md` shows single-file Go, multi-file Go, single-file TypeScript, and multi-file TypeScript examples.
- Tests cover resolver behavior, ambiguity, arg splitting, Go build-tag validation, and TypeScript runtime selection.

## Plan of Work

Start with a small internal script model in `cmd/onlava` or an internal package if reuse becomes useful. Keep the first implementation local to the CLI unless another package needs the resolver.

Implement target parsing and filesystem discovery before adding execution. Once the resolver is covered by tests, add `onlava run list`, `onlava run inspect`, and `onlava run <domain>:<script>` execution.

Keep the runner intentionally narrow. Do not add scheduling, remote execution, dashboard UI, database helpers, or script metadata files in this pass.

## Concrete Steps

1. Add target parsing for `<domain>:<script>` with strict validation.
2. Add filesystem resolver for the four supported layouts.
3. Add ambiguity detection and `--lang go|typescript`.
4. Add `onlava run list` and `onlava run inspect`.
5. Add Go execution with build-tag validation for `*.script.go`.
6. Add TypeScript execution with Bun/Node runtime selection.
7. Add top-level `onlava run <domain>:<script>` and argument-splitting tests.
8. Update usage text and docs.
9. Run validation and update this ExecPlan.

## Validation and Acceptance

Required validation:

```sh
go test ./cmd/onlava
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

Focused scenario validation:

```sh
onlava run list --app-root testdata/apps/script-fixture --json
onlava run inspect billing:reconcile --app-root testdata/apps/script-fixture --json
onlava run --app-root testdata/apps/script-fixture billing:reconcile --dry-run
```

Acceptance criteria:

- Script discovery works even when normal app package loading would fail.
- Single-file Go scripts cannot accidentally join normal app packages.
- Script args after the target are passed through exactly.
- Ambiguous scripts fail loudly with both candidate paths listed.
- The feature uses no new dependencies unless TypeScript runtime probing already requires an existing helper.

## Idempotence and Recovery

- Script discovery is read-only.
- `list` and `inspect` are read-only.
- `run` only executes user-authored script code and should not mutate onlava state by itself.
- If a script process exits non-zero, onlava returns that failure without cleaning or modifying source files.
- Re-running a script command should not create persistent onlava artifacts except normal build/cache artifacts created by Go, Bun, Node, or existing onlava helpers.

## Artifacts and Notes

Expected changed artifacts:

```text
docs/plans/0058-onlava-script-runner.md
docs/plans/active.md
cmd/onlava/main.go
cmd/onlava/serve.go
cmd/onlava/script*.go
cmd/onlava/script*_test.go
docs/local-contract.md
docs/app-development-cookbook.md
testdata/apps/script-fixture/...
```

Supported layouts:

```text
Single-file Go:
  billing/scripts/reconcile.script.go
  must start with //go:build ignore

Single-file TypeScript:
  billing/scripts/reconcile.script.ts

Multi-file Go:
  billing/scripts/reconcile/main.go

Multi-file TypeScript:
  billing/scripts/reconcile/index.ts
```

Explicitly out of scope:

- Cron or scheduled script execution.
- Remote/cloud script execution.
- Dashboard script UI.
- Script metadata manifests.
- Coupling script names to onlava service discovery.

## Interfaces and Dependencies

Public CLI:

```sh
onlava run list [--app-root <path>] [--json]
onlava run inspect <domain>:<script> [--app-root <path>] [--lang go|typescript] [--json]
onlava run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<script> [script args...]
```

Internal interfaces can evolve, but the resolver should expose enough information for `list`, `inspect`, and `run` without invoking the app parser:

```go
type ScriptTarget struct {
    Domain string
    Name   string
}

type ScriptCandidate struct {
    Target ScriptTarget
    Lang   string
    Layout string
    Path   string
}
```

Dependencies:

- Prefer the Go standard library for discovery, validation, and process execution.
- Reuse existing onlava helpers for app-root discovery and TypeScript runtime selection where practical.
- Do not add new third-party dependencies for script parsing or execution.
