# Developer Test Loop Attribution

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

`docs/plans/0050-test-suite-speed-hardening.md` is complete and immutable. It
brought the warm suite down and built the timing report. This plan covers the
persistent regression that remained afterwards, and it exists because the
0050-era evidence could not attribute the cold-run penalty to anything.

## Purpose / Big Picture

Three costs were confounded in the reported numbers:

- Fresh suite 55.742s against a 5s target.
- Confirmation 99.506s, 64% of a 155.248s Go-test step.
- Warm suite 19.73s, roughly 4x target.

Confirmation cost is policy, not product speed: it re-ran every candidate on
every fresh run, including outliers that had been at the same level for weeks.
Cold-run cost was entirely unattributed — the run built 49 test binaries and
nothing recorded which links were expensive.

The goal of this plan is attribution first, then targeted reduction. Do not
optimize a number nothing can explain.

## Progress

- [x] 2026-07-28: Scoped confirmation to regressions. `budgets.confirmation_scope`
      is `regressions` on `--fresh-tests` and `all` on `--release --fresh-tests`.
      A candidate is re-confirmed only when the recorded baseline did not have it
      over budget, or when it is now at least 25% and 0.5s worse. Skips are
      recorded in `deferred_confirmations` with their baseline, never dropped
      silently.
- [x] 2026-07-28: Instrumented test-binary preparation. `internal/testsuite`
      reports `Result.Prepare` with total elapsed, package-list elapsed, and one
      `BinaryBuild{Package, BuildID, Elapsed}` per link; the harness surfaces it
      as `test_binaries` in `.scenery/harness/test-timing-latest.json`, and
      `scripts/testsuite -builds N` prints it to stderr.
- [x] 2026-07-28: Measured the cold penalty with the new instrumentation
      (numbers under Surprises & Discoveries).
- [x] 2026-07-28: Folded the second hand-rolled `native` fixture copy in
      `build_desktop_test.go` onto the shared `nativeFixtureRoot` helper.
- [x] 2026-07-28: Re-measured the fresh lane end to end.
      `.scenery/harness/bin/scenery harness self --fresh-tests --summary --write`
      run against the stale cached-lane baseline spent 39.303s confirming (16
      observed, 11 confirmed, 9 deferred); the immediately following run against
      the fresh baseline spent 13.861s (8 observed, 3 confirmed, 16 deferred).
      Against the reported 99.506s that is the steady-state cost of this change.
      The deferrals are correct: `scenery.sh/cmd/scenery` at 16.98s against a
      17.54s baseline is not a regression and was not re-run.
- [x] 2026-07-28: Cut the app runtime out of the compiler-side test closure.
      The contract value types moved from the root `scenery.sh` package to
      `internal/contract`, the authorization expression evaluator moved from
      `runtime/contract_policy_expression.go` to `internal/contractpolicy`, and
      root re-exports the contract surface as aliases so generated app code is
      unchanged. Ten packages — `scn`, `graph`, `workspacetx`, `librarybuild`,
      `compiler`, `deployplan`, `build`, `evolution`, `generate`,
      `contractagent` — no longer link `scenery.sh/runtime`, its HTTP stack, or
      the PostgreSQL driver (numbers under Surprises & Discoveries).
- [ ] Reduce the `cmd/scenery` serial critical path (see Decision Log).

## Surprises & Discoveries

- 2026-07-28: The cold penalty is linking, and package listing is nearly free.
  A cold run (`.scenery/harness/test-binaries` removed) spent 19.181s in
  prepare, of which package listing was 2.416s and 49 links accounted for
  129.776s of CPU compressed to roughly 16.8s of wall time. Total cold run was
  46.267s. The warm run of the same command spent 0.081s in prepare and 18.772s
  overall. So the cold/warm gap is prepare, and prepare is links.
- 2026-07-28: Link parallelism is already saturated. Raising `BuildParallelism`
  from 8 to 16 on a 24-logical-core host left prepare unchanged (19.181s versus
  19.300s) while total build CPU rose from 129.776s to 283.700s — the extra
  workers only contended with each other. Cold cost has to come out of the
  number and size of test binaries, not out of scheduling.
- 2026-07-28: The five slowest links are `internal/build` 6.777s,
  `internal/contractagent` 6.480s, `internal/compiler` 6.245s,
  `internal/deployplan` 5.658s, and `cmd/scenery` 4.842s. Link cost does not
  track test-execution cost: `internal/contractagent` and `internal/deployplan`
  are cheap to run and expensive to link.
- 2026-07-28: The warm suite is one package's serial critical path.
  `cmd/scenery` is 17.60s of an 18.77s wall; the next package,
  `internal/evolution`, is 5.87s and finishes hidden behind it. Nothing outside
  `cmd/scenery` can move the warm number.
- 2026-07-28: Inside `cmd/scenery` the problem is not duplicated compile work.
  181 top-level tests accumulate 21.90s against a 17.60s wall — an effective
  parallelism of 1.24x on a 24-core host with `-test.parallel=8`. The 36 tests
  at or above 0.2s hold 15.74s of that, and 27 of them (13.15s) do not call
  `t.Parallel`. Roughly half of those are blocked by `t.Setenv`; the rest bind
  fixed ports or swap `os.Stdout` through `captureStdout`. Only one real
  compile smoke remains in the package, so there is no duplicate-compile fat
  left to cut.

- 2026-07-28: The expensive links were paying for the PostgreSQL driver to parse
  a duration string. The root `scenery.sh` package held both the contract value
  types (`Duration`, `ParseDuration`, `UUID`, …) and `scenery.go`, whose two
  functions `Meta()` and `CurrentRequest()` import `scenery.sh/runtime` →
  `internal/durable/store` → `internal/postgresdb`, plus `net/http` and
  `crypto/tls`. Every package that wanted a scalar parser linked all of it. A
  probe importing only `scenery.sh` linked to 10.3MB; one importing only what
  the compiler actually needs (`runtimeapi` + `machine` + `spec`) linked to
  3.7MB. A second, independent edge did the same thing:
  `internal/compiler/security_validate.go` imported `scenery.sh/runtime` for
  `ValidateContractAuthorizationExpression`, a self-contained 551-line parser
  with only stdlib imports.
- 2026-07-28: Removing both edges cut test-binary size by 6.3%–41.2%:
  `workspacetx` -41.2%, `graph` -40.5%, `scn` -37.3%, `librarybuild` -25.9%,
  `deployplan` -19.1%, `compiler` -17.8%, and `build`/`contractagent`/
  `evolution`/`generate` -6.3% each (those four still carry the embedded UI
  catalog through `internal/generate`, which is now their dominant weight).
  Building those ten test binaries against an isolated cold `GOCACHE` with
  non-test dependencies pre-warmed went from 11.09s/8.40s to 9.27s/6.97s across
  two runs — 16.4% and 17.0% — with system time, the linker's dominant cost,
  down about 30% (73.97s → 55.10s, 73.83s → 50.38s). `cmd/scenery` is unchanged
  at 39.4MB, as expected: it owns the runtime and should link it.
- 2026-07-28: The architecture rule found a layering violation the move had
  hidden. `contract_path_tail_test.go` imported `scenery.sh/runtime` to test the
  runtime's HTTP input decoder against contract types, so it would have relinked
  the runtime into the new leaf's own test binary. It belongs next to
  `runtime/contract_path_tail.go` and now lives there.

## Decision Log

- Confirmation scope is derived from the existing mode flags rather than a new
  knob. `--release --fresh-tests` already means "periodic audit"; adding a
  separate flag would grow the CLI surface for a distinction the modes already
  encode.
  Date/Author: 2026-07-28 / Claude.
- Deferred candidates are recorded rather than dropped. A confirmation pass that
  silently skips known outliers reads as "nothing was over budget" on the next
  run, which is exactly the failure the timing report exists to prevent.
  Date/Author: 2026-07-28 / Claude.
- Build IDs are recorded alongside durations. A duration alone cannot tell a
  rebuild caused by a real input change from one caused by cache churn; the
  build ID makes a relink traceable to the identity that changed.
  Date/Author: 2026-07-28 / Claude.
- The contract surface keeps its app-facing spelling through aliases rather than
  moving generated code to a new import path. Generated app code says
  `scenery.Duration`; that is the current singular spelling and it does not
  change. `internal/contract` is the implementation, and root is a type-alias
  façade, so there is no second spelling and no compatibility shim — only one
  name reaches app authors. `contract_surface_test.go` pins type identity and
  method reachability so the façade cannot drift from the leaf.
  Date/Author: 2026-07-28 / Claude.
- The layering is enforced by a `packageLayerRule` in self-harness architecture
  checks, not by prose. The regression this prevents is invisible in tests and
  in review: one `scenery "scenery.sh"` import inside a compiler-side package
  silently relinks the runtime, HTTP stack, and PostgreSQL driver into that test
  binary, and nothing fails. Fixture apps under `testdata/` are exempt, since
  generated client code legitimately imports the façade.
  Date/Author: 2026-07-28 / Claude.
- The next reduction target is the `cmd/scenery` serial phase, not fixture
  reuse. The measurement above contradicts the fixture/duplicate-compile
  hypothesis for this package. The candidate work is removing `t.Setenv` from
  heavy tests by threading configuration through existing seams, and replacing
  fixed-port binding with port-zero listeners, so those tests can join the
  parallel phase. Mass-adding `t.Parallel` without those changes is unsafe, as
  0050 already recorded.
  Date/Author: 2026-07-28 / Claude.

## Outcomes & Retrospective

Open. The confirmation-scope change removes the largest reported cost
(99.506s of confirmation) from everyday fresh runs without losing regression
detection, and the prepare instrumentation answers the cold-run question. The
warm-suite target is unmet and now has a precise, measured owner.

## Context and Orientation

The timing report lives in `cmd/scenery/harness_oracle.go` (types, budgets,
parsing) and `cmd/scenery/harness_timing.go` (baseline selection, confirmation,
build-timing projection). The fresh execution lane lives in
`internal/testsuite`, with `prepare` and `buildMissingBinaries` in
`internal/testsuite/runner.go` owning package listing and linking.
`scripts/testsuite/main.go` is the manual adapter.

The persisted artifact is `.scenery/harness/test-timing-latest.json`, shaped by
`docs/schemas/scenery.harness.test_timing.schema.json` with its revision
mirrored in `cmd/scenery/payload_identity.go`. That artifact is also the
baseline the regression scope reads, so a run with `--write` both consumes the
previous baseline and records the next one.

Read `docs/local-contract.md` § harness self for the stable timing contract and
`internal/testsuite/AGENTS.md` for the runner's local rules.

## Milestones

1. Attribution (done): confirmation scope, prepare instrumentation, measured
   cold and warm baselines.
2. Cold reduction (partly done): fewer or cheaper links. The lever is the test
   dependency closure of the expensive-to-link packages, not scheduling. The
   app-runtime closure is out of the ten compiler-side packages. What remains is
   the embedded UI catalog reaching `build`, `contractagent`, `evolution`, and
   `generate` through `internal/generate`, and the 39.4MB `cmd/scenery` binary.
3. Warm reduction (open): shorten the `cmd/scenery` serial phase so the package
   stops being the whole wall clock.

## Plan of Work

Milestone 2 starts from the recorded `builds` array. For each of the five
slowest links, establish what pulls the closure wide, using the same
package-boundary technique 0050 used for `internal/app` and `internal/model`:
move pure helpers out of packages that drag heavy transitive dependencies into
a test binary. Accept a link cost only when the boundary is genuinely needed.

Milestone 3 works the 27 serial heavy tests in `cmd/scenery`. Two mechanical
blockers dominate. `t.Setenv` forbids `t.Parallel` outright, so those tests must
take their configuration through an existing seam instead of the process
environment. Fixed-port binding and `captureStdout`'s global `os.Stdout` swap
serialize the rest; port-zero listeners and a writer parameter remove both.
Neither change weakens an assertion boundary.

## Concrete Steps

```sh
# cold attribution: clear the cache, then read the ranked links
rm -rf .scenery/harness/test-binaries
go run ./scripts/testsuite -p 3 -run 'a^' -builds 20 -record-timings=false

# warm attribution: per-test elapsed for the critical-path package
go run ./scripts/testsuite -p 3 -run '.*' -record-timings=false > /tmp/warm.jsonl

# full fresh lane with regression-scoped confirmation
.scenery/harness/bin/scenery harness self --fresh-tests --summary --write

# periodic audit: confirm every candidate
.scenery/harness/bin/scenery harness self --release --fresh-tests --summary --write
```

## Validation and Acceptance

`go test ./cmd/scenery ./internal/testsuite`, then `go test ./...`. Schema and
CLI-contract changes additionally need
`.scenery/harness/bin/scenery harness self --quick --summary --write`, which
validates the committed `scenery.harness.test_timing` example against the
schema revision recorded in `cmd/scenery/payload_identity.go`.

Acceptance for milestone 1 is that a second `--fresh-tests` run against a
recorded baseline confirms only new or materially worsened candidates and lists
the rest under `deferred_confirmations`. Acceptance for milestones 2 and 3 is a
measured reduction in `test_binaries.build_seconds` and in the `cmd/scenery`
package wall respectively, each shown against the numbers in Surprises &
Discoveries on the same host.

## Idempotence and Recovery

Every step is repeatable. `.scenery/harness/test-binaries` is disposable cache;
removing it forces a cold run and nothing else. A missing, truncated, or
schema-stale `test-timing-latest.json` degrades to "no baseline", which confirms
every candidate — the conservative direction, and the same behavior as a first
run on a fresh worktree. No step mutates tracked source.

## Artifacts and Notes

- `.scenery/harness/test-timing-latest.json` — timing report and confirmation
  baseline.
- `.scenery/harness/test-binaries/` — linked binaries, manifest, and package
  timing estimates. Disposable.
- `.scenery/harness/artifacts/<run-id>/go-test.jsonl` — raw Go JSON events when
  the harness runs with `--write`.

Host for every number in this plan: 24 logical cores, 16 performance cores.
Timing conclusions do not transfer across machines; re-measure before comparing.

## Interfaces and Dependencies

- `scenery.harness.test_timing` gained `test_binaries`,
  `deferred_confirmations`, and `budgets.confirmation_scope`. The schema
  revision moved to
  `sha256:73231c3fb87a0790d678879fec827f095f584d8618755dbf10f4fcc4fddad3fa`.
- `testsuite.Result` gained `Prepare`; `testsuite.Run` is otherwise unchanged.
- `scripts/testsuite` gained `-builds N`, which writes to stderr only, leaving
  the stdout Go JSON event stream intact.
- No new environment variables, and no new CLI flags: confirmation scope is
  derived from the existing `--release` and `--fresh-tests` modes.
