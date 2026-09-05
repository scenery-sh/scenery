# Go Lint Cleanup

This ExecPlan is a living document. Keep its progress, decisions, discoveries,
and outcomes current as work proceeds, following `PLANS.md`.

## Purpose / Big Picture

Make `golangci-lint run ./...` pass with the installed Go-1.27-compatible
golangci-lint 2.13.2, without disabling checks, excluding files, or hiding an
existing-issue baseline. Preserve the already dirty TypeScript and edge work.

## Progress

- [x] (2026-09-05) Recorded the unrestricted baseline: 695 findings (485
  errcheck, 13 ineffassign, 108 staticcheck, 89 unused).
- [x] (2026-09-05) Remove unused private code and simplify equivalent expressions.
- [x] (2026-09-05) Fix lost write errors and migrate deprecated HTTP proxy/cancellation APIs.
- [x] (2026-09-05) Review cleanup and output error handling; unrestricted lint has zero findings.
- [x] (2026-09-05) Regenerate all three fixtures unchanged; package, full, race,
  fresh-runner, TypeScript, and quick-harness checks pass.
- [x] (2026-09-05) Repair PostgreSQL fixture source on explicit follow-up;
  the complete release self-harness now passes.
- [x] (2026-09-05) Add the approved nine correctness linters to an explicit
  fourteen-linter policy; fix wrapped-error handling, host/port construction,
  and SQL result handling. A focused in-process driver test proves schedule
  iteration errors propagate without starting jobs.
- [x] (2026-09-05) Stage all authored sources for publication; clean-checkout
  installation now passes.
- [x] (2026-09-05) Repair headless fixture startup and adjacent shell gate
  issues; the complete gate passes, including clean install, HTTP smoke,
  router safety, and artifact hygiene. Optional external-app checks are
  explicitly skipped because no external app was selected.

## Surprises & Discoveries

The default report capped repeated diagnostics at 156. Removing reporting caps
revealed 695 findings. Three ineffectual assignments discard actual write,
permission, or sync errors through a shadowed `err` in CLI request creation,
runtime bundle publication, and evolution transaction locking.

## Decision Log

- Keep checks enabled and fix all findings, including those hidden by default
  reporting caps. Date/Author: 2026-09-05 / Codex.
- Preserve primary operation errors; read-handle closure, rollback after a
  completed transaction, and disposable-file cleanup are explicitly best effort.
  Test resource closure should report unexpected errors where meaningful.
  Date/Author: 2026-09-05 / Codex.
- Replace obsolete transport APIs with context cancellation and proxy Rewrite;
  preserve URL/Host behavior and test forwarding-header handling.
  Date/Author: 2026-09-05 / Codex.

## Outcomes & Retrospective

All 695 lint findings are fixed without check exclusions or suppressions.
Three shadowed errors now propagate from request creation, bundle publication,
and transaction locking. Proxy Rewrite preserves routing while rebuilding
forwarding headers; focused tests cover trusted versus untrusted edge input.
The test-binary cache fingerprints the selected Go executable rather than the
runner's deprecated baked-in GOROOT. Public machine schemas are unchanged;
the forwarding trust policy is recorded in `docs/local-contract.md`.

Implementation and repository release acceptance are complete. The repaired
shell gate builds the explicit development fixture target, runs the resulting
binary, propagates build failures, installs only into disposable GOBIN paths,
and stops children before deleting their files. Two untracked Finder metadata
files were removed with explicit user approval. No external application was
selected for the optional external-app lane.

## Context and Orientation

The baseline is `.scenery/harness/lint-all-baseline.json`. Most findings concern
deferred cleanup, CLI presentation output, and private helpers superseded by
the current runtime or release harness. Behavioral changes belong to their
existing owners: `cmd/scenery`, `internal/build`, `internal/evolution`,
`internal/agent`, and `internal/mcpfederation`.

## Milestones

1. Reduce mechanically equivalent and dead-code findings.
2. Fix meaningful IO and transport behavior with focused tests.
3. Prove the unrestricted lint report is empty and validate the changed areas.

## Plan of Work

Use analyzer locations and Go syntax boundaries for mechanical edits. Review
remaining calls individually. Do not add blanket suppression comments or
change linter configuration without explicit approval. Keep application and
machine JSON shapes stable.

On 2026-09-05 the user approved the recommended lint policy: preserve the five
standard linters and add asasalint, bidichk, durationcheck, errorlint,
gocheckcompilerdirectives, nilnesserr, nosprintfhostport, rowserrcheck, and
sqlclosecheck. Configure errorlint comparisons/assertions, not mandatory error
wrapping. Keep reporting uncapped. Preserve three intentional exact-identity
test assertions (token failures, unchanged unrelated auth errors, and extracted
diagnostic causes) with narrow documented errorlint suppressions.

## Concrete Steps

Run from the repository root:

```sh
golangci-lint run --max-issues-per-linter=0 --max-same-issues=0 ./...
go test ./...
go test -race ./...
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go test ./internal/compiler ./internal/parse
go run ./scripts/testsuite -run 'a^' -record-timings=false
go run ./scripts/testsuite
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery harness self --quick --summary --write
.scenery/harness/bin/scenery harness self --release --summary --write
scripts/release-gate.sh
git diff --check
```

Run affected package tests before the full suite and the refreshed
`changed_area.recommended_commands` union. Expected classes include Go package,
compiler/generator, CLI, and release-sensitive/runtime. Release mode supersedes
the default full harness. Isolate release-gate `GOBIN` in a temporary directory
so its install steps cannot replace the user's shared Scenery CLI.

## Validation and Acceptance

Both ordinary and unrestricted lint commands must report zero issues. Preserve
existing routing, cancellation, and artifact-integrity tests; add focused
in-process proof for behavior changes. Every exact Go test root must remain
below the repeated isolated 100ms p95 budget. If the release harness encounters
the known PostgreSQL fixture issue, record it explicitly rather than treating
the release as green or changing unrelated database behavior.

## Idempotence and Recovery

Keep all edits unstaged; preserve pre-existing dirty files. Regeneration is
atomic and repeatable. Temporary analyzer reports and rewrite helpers belong
under ignored `.scenery/harness`. Never reset the checkout or shared tools.

## Artifacts and Notes

Retain task-specific lint and test reports under `.scenery/harness/lint-all-*`.

The approved policy follow-up is validated with `golangci-lint config verify`,
`golangci-lint run ./...` (zero issues), all affected package tests, the full Go
suite, and the complete release self-harness including its full race suite.
Reports are `.scenery/harness/lint-policy-*.log`. The new schedule-row test
passes 20 exact-root repetitions below 100ms. No production dependencies,
environment knobs, or public schemas changed. The three test-only suppressions
preserve exact error identity rather than relaxing those assertions to permit
wrappers. Staging the complete work resolved the clean-checkout install
limitation. The publication rerun passed full Go, race, lint, UI build,
dashboard embedding, isolated installation, self-harness, and clean-checkout
installation. Its fixture smoke then failed with `unknown command "serve"`;
external-app smoke, router safety, and artifact hygiene were not reached.
Evidence: `.scenery/harness/commit-push-gate.log` and
`.scenery/release-gate/20260905T184325Z/fixture-smoke-app.log`.

Final follow-up proof: `bash -n scripts/release-gate.sh`, standalone fixture
and router probes, standalone artifact hygiene, the complete release
self-harness (including race), and `scripts/release-gate.sh` all pass.
The final gate includes `go test ./...`, `go test -race ./...`,
`golangci-lint run ./...`, UI typecheck/build, dashboard embedding, disposable
and clean-checkout installs, self-harness, HTTP smoke, router safety, and
artifact hygiene. Its optional external-app check is skipped without a supplied
root. Reports: `.scenery/harness/serve-repair-release.log`,
`.scenery/harness/serve-repair-gate-final.log`, and
`.scenery/release-gate/20260905T190245Z/`.

Validation results on native macOS (2026-09-05):

- Unrestricted lint: zero findings (`lint-all-final.log`). The ordinary lint
  command also passed in the release gate.
- Affected package tests, `go test ./...`, and `go test -race ./...`: pass.
- Native, house, and assistant regeneration: pass, no output changes.
- Bun conformance: 26 pass; generated-client and catalog typechecks: pass.
- Compiler/parse tests: pass. The documented CLI `-run TestGenerate` filter
  matches no current tests; the complete CLI package tests pass.
- Both manual testsuite commands: pass (59 linked test packages); the fresh
  execution log contains 1782 passing top-level roots. Concurrent-run timings
  are not isolated performance proof.
- Each of the three new test roots was run separately with `go test -json
  -count=20 -run '^<exact-name>$'`: all pass, p95 below the Go event timer's
  10ms reporting resolution, comfortably below 100ms.
- Quick self-harness: pass, including all affected package tests and schemas.
- Linux amd64 cross-build (`GOOS=linux GOARCH=amd64 go build ./...`): pass;
  Linux runtime behavior is unmeasured.
- Release gate: full Go, race, lint, UI build, embedded dashboard, isolated
  install, and self-harness pass. Clean-checkout install fails because its
  tracked-file copy omits pre-existing untracked
  `internal/generate/generate_typescript_outcomes.go`. Later fixture/external
  smoke, router-safety, and artifact-hygiene steps are not reached. No files
  were staged and the shared installed Scenery binary was not replaced.
- Release self-harness initially found a second old-capitalization assertion in
  its edge startup probe. That assertion was corrected and the lane rerun;
  edge process probes passed, leaving PostgreSQL as the only failing step
  (`lint-all-release-final.log`). The explicit follow-up repaired that fixture
  with minimal application source; the complete release self-harness now
  passes (`postgres-fixture-release.log`), including the full PostgreSQL probe
  and race suite. `go test ./cmd/scenery`, `go test ./...`, and ordinary lint
  also pass. The new fixture-discovery test passes 20 repetitions with p95
  20ms. The repeated isolated-install release gate still stops at its
  tracked-file-only clean checkout; later checks remain unrun.
- `scenery logs --limit 500 -o jsonl` is not applicable: no target-app dev
  runtime was started for this repository lint cleanup.

## Interfaces and Dependencies

No new production dependency, configuration knob, or public API is required.
Use standard-library context cancellation, ReverseProxy.Rewrite, and existing
error propagation conventions.
