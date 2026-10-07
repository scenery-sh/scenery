# Telemetry-Driven Reliability

## Purpose / Big Picture

Finish the five improvements authorized on 2026-10-05 after reviewing this host's
CLI and supervisor telemetry: stop ONLV recorder authentication storms, expose
and contain blocked builds, make command evidence attributable and isolate test
traffic, define consistent latency measurements, and bound supervisor logs.
The companion ONLV work lives in its recorder and development-runtime frontend;
Scenery owns the CLI, event, runtime, storage and report contracts here.

## Progress

- [x] 2026-10-05 Read current ownership and validation rules; captured the
      baseline: 8,334 CLI records, 64 supervisor logs, 385 MiB of logs, 54,151
      unauthenticated recorder completions, and a live ONLV migration block.
- [x] Fix recorder authentication lifecycle and verify request bounds in ONLV.
- [x] Report live/historical build blocks and contain changed applied seeds.
- [x] Isolate telemetry by agent home and record invocation/producer/diagnostic
      identity, distinguishing expected supersession from failed builds.
- [x] Align percentiles and measure captured change to first served answer.
- [x] Rotate supervisor logs with bounded retention and preserve report coverage.
- [x] Complete focused tests, full repository verification, lint, named runtime
      probes, ONLV checks and browser acceptance; record exact evidence.

### Resume here

2026-10-05: completed locally. All five slices have implementation and acceptance
evidence below. No remaining implementation work; installation and publication
were not requested. Scenery started clean at 3749987e; ONLV started at 537ac155
with unrelated dirty work, which was preserved while another chat also worked
in that checkout. Temporary browser overrides and fixture runtimes are gone.

## Surprises & Discoveries

- The CLI writer ignores the injected agent home, so isolated validation calls
  leak into the personal telemetry file. 91 of 94 failed db migrate records name
  postgres-harness; historical totals are not a user-facing failure rate.
- Recorder transport caches credentials, catches generated-client failures as
  network retries, and does not stop on sign-out.
- The report ignores build.blocked events. ONLV has retained its last generation
  since 2026-10-02 with 12 rebuilds prevented by a pending migration.
- Query percentiles included failures while report percentiles excluded them.
- During this task another process restarted ONLV at 12:42 UTC; its current owner
  70742 and app process 71540 no longer report the historical migration block.
  UI acceptance used and removed a temporary browser-only block fixture.

## Decision Log

- 2026-10-05: Preserve source and data ownership. Use private fixture runtimes
  for service-boundary proof; do not reset existing ONLV data or install the CLI.
- 2026-10-05: Prefer native structured evidence over expanding transcript
  inference. Keep raw arguments, tokens and request bodies out of telemetry.
- 2026-10-05: Historical failure rates remain explicitly historical. New
  validation traffic belongs to its existing private agent home; do not infer
  that every custom home is a test environment.
- 2026-10-05: Latency must identify its boundaries and producer. Do not claim an
  optimization from measurements made with older app-pinned producers.

## Outcomes & Retrospective

Completed on 2026-10-05 with local source changes in Scenery and ONLV. Recorder
HTTP 401 storms stop after one credential refresh, deterministic changed-seed
failures block unrelated rebuilds, native command records identify invocation
and producer within the selected agent home, successful latency cohorts agree,
and detached supervisor history is bounded without losing rotation joins.

The first-response metric is now observed in a real process-model fixture:
five rebuilt-generation samples, p50 639 ms / p95 925 ms. These values prove
capture and attribution, not an ONLV speed improvement. The personal historical
report has no such samples until a producer containing this change owns the
runtime. It reconstructs two historical blocks and 28 prevented rebuilds; no
block was found among inspected current owners, with 27 unavailable owners
explicitly reported as unknown.

ONLV native Chrome acceptance observed exactly two rejected chunk attempts over
215,014 ms, then authenticated recovery to full recording. The Runtime panel
showed a temporary migration cause, 120 minutes and 12 prevented rebuilds;
Running was restored and the test tab closed. No account or database mutation
was used. ONLV's completed companion plan records details at
`docs/agent/exec-plans/completed/recorder-auth-and-build-blocks.md`.

All selected checks pass. Existing documentation freshness warnings, unrelated
ONLV lint warnings and its bundle-size advisory remain. No global CLI install,
commit, push, release certification or deployment was performed. VNEXT.md and
historical numbered plans were intentionally left unchanged.

## Plan of Work

1. In ONLV, make authentication failure stop capture and upload attempts until
   authentication is explicitly restored. Preserve bounded retry for transient
   failures and account isolation. Show build block cause, age and prevented
   count in the existing Runtime panel using its generated client.
2. In cmd/scenery, extend typed deterministic blocks to changed applied seeds;
   retain the last serving generation. Extend internal/telemetryreport to report
   prevented builds, block lifetimes and verified currently active blocks.
3. Route CLI telemetry through commandAgentPaths, record native invocation IDs,
   producer revision and sanitized diagnostic codes, and update the report's
   grouping and schemas. Superseded candidates are recorded separately.
4. Use successful samples consistently for percentiles, expose sample counts,
   and correlate a captured source change with activation and the first served
   response. Preserve explicit missing coverage rather than inventing timings.
5. Make each supervisor own a rotating log writer. Retain bounded segments and
   have historical reporting read them in order as one session, including
   operations spanning a rotation. No new dependencies or configuration knobs.

## Validation and Acceptance

Commands run from the Scenery root unless another directory is named. Run focused
`go test ./cmd/scenery ./internal/telemetryreport` and tests of any new helper
package after its implementation. Runtime and contract changes select full
`go run ./scripts/verify --summary --write`, followed by the cumulative commands
in that immutable archive's agent-context.json and `golangci-lint run ./...`.
Use `go run ./scripts/verify --probe dev-process --summary --write` for detached
writer lifecycle, `--probe process-model` for generation/request latency and
`--probe cli-process` for native isolated telemetry.
Each probe must stop its owned fixture and retain assertion/cleanup evidence.
If generator output changes, regenerate the native and house public_api fixture
clients with the commands in docs/harness-engineering.md and run their TS checks.

From ONLV run `just check-app nextnext`, focused recorder and devRuntime Bun tests,
`just repo-harness`, and the production frontend build if generated clients or
shared architecture change. Native Chrome at the Scenery origin must demonstrate
bounded authentication failure, recovery and visible blocked-runtime state.
Record exact commands and results here. Release certification and whole-repo
benchmark/timing audits are unselected; these fixes need behavior probes.

### Recorded acceptance

- Full `go run ./scripts/verify --summary --write`:
  `.scenery/harness/runs/20261005T131959.576894000Z`, stable inputs, all repository
  checks passed. Ten pre-existing freshness warnings and the cached-Go timing
  advisory remain. The final documentation closure repeats this same command.
- `golangci-lint run ./...`: zero issues.
- `go run ./scripts/verify --probe cli-process --summary --write`:
  `20261005T132124.998521000Z`, passed; real command records have native identity
  and diagnostics and do not leak from a private agent home to the personal home.
- `go run ./scripts/verify --probe process-model --summary --write`:
  `20261005T132937.198959000Z`, stable inputs and passed, including five attested
  first-response samples. The earlier run's functional probe passed but its
  archive was invalidated by a concurrent schema formatting edit; this frozen
  rerun replaces that incomplete evidence.
- `go run ./scripts/verify --probe dev-process --summary --write`:
  `20261005T132208.867493000Z`, stable inputs and passed. Real supervisor lifecycle
  passed; the production-size log proof retained four segments, 30,636,542 bytes
  and one report session after writing beyond the retention cap.
- Regenerated `typescript_client.public_api` for native, house and assistant
  fixtures with `.scenery/harness/bin/scenery generate`. Bun generated-client
  conformance/lifecycle tests: 55 passed, 287 assertions. Both generated-client
  and catalog TypeScript projects passed.
- ONLV: 30 focused recorder/runtime tests, `just check-app nextnext`,
  `bun run i18n:check`, production `bun run build`, `just repo-harness`, and
  native Chrome acceptance passed. Companion plan and BUGS.md are closed locally.
- Fresh `.scenery/harness/bin/scenery telemetry report --since 720h -o json`
  succeeded against retained local history with explicit source coverage.

Each archive's agent-context summary and full changed-area command union were
reviewed. The full Go run covers the package union; generation, lint and named
probes provide their separate same-source scope evidence. Release certification,
all-root timing audits and broad benchmarks are unselected for this change.

## Idempotence and Recovery

All edits are scoped and replayable. Test-owned homes, runtimes and databases are
cleaned up by their named probes. No live runtime or database is adopted or
reset. Log rotation must not lose the active writer or reinterpret two segments
as two sessions. Preserve unrelated dirty ONLV edits and generated ownership.
