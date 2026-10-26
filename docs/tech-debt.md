# Tech Debt

This file tracks known project debt that should be visible to agents before they start large edits. Skim this list, then read only the entries touching your surface.

Current repository verification is `go run ./scripts/verify` (plan 0168).
Older dated evidence below retains the command and source paths that existed
when recorded; those historical `harness self` invocations are not live commands.

- [Resolved](#resolved)
- [Open](#open)
  - [Deeper Architecture Checks](#deeper-architecture-checks)
  - [Long Build Tests](#long-build-tests)

## Resolved

- 2026-10-21: Published Dotfiles — `copyPublishTree` in `internal/edge/publish.go`
  now excludes dotfiles and dot-directories except non-dotfile entries beneath a
  top-level `.well-known` directory, and `staticFrontendBody` in
  `internal/edge/caddyconfig.go` blocks `/.*` and `*/.*` outside `/.well-known/*`
  with HTTP 404 while preventing missing `/.well-known/*` paths from falling
  back to `/index.html`. Covered by in-process publication/Caddyfile tests in
  `internal/edge/publish_test.go` and `internal/edge/caddystatic_test.go` plus
  live managed-Caddy HTTP proof in `scripts/verify/harness_self_edge_static.go`.

- 2026-09-23: Scenery dashboard debt (Full Dashboard Parity, Browser Harness
  Fixture-Backed Mutation Depth, and the dashboard/console findings of the
  2026-06-27 through 2026-07-03 agent threads) — obsolete: ExecPlan 0202
  removed `apps/console`, the embedded dashboard, and `scenery harness ui`. The
  remaining data surface is the documented development runtime RPC with a
  generated `dev-runtime.ts` client; apps own their developer UI.
- 2026-09-16: Deprecated single application development model — removed with
  its selector, candidate restart path, retained compiler,
  `internal/nativebuilddriver` and the native build benchmarks. The
  `dev-process` and `native-contract` probes now run through the process model,
  whose host attests each public answer with the build of its generation, so
  runtime-bundle and `build --development --verify-generation` checks still
  bind served answers. Production builds keep one executable (Plan 0200).

- 2026-09-05: PostgreSQL release fixture source — temporary worktrees now
  include a minimal `app.scn`. An in-process test covers configured-app and
  seed discovery; the full release self-harness passes, including PostgreSQL
  schema reset, snapshot round-trip, and disposable-container cleanup.

- 2026-09-05: Edge static integration test placement — moved the real Caddy
  HTTP journey from `internal/edge/caddystatic_test.go` to the release edge
  probe in `cmd/scenery/harness_self_edge_static.go`. Production rendering and
  publication feed 16 HTTP checks plus raw traversal proof; ordinary renderer,
  publisher, and injected-runner tests remain. Native release edge proof,
  `go test ./...`, and the release full race suite passed. The initially
  unrelated PostgreSQL fixture failure was subsequently resolved above.

- 2026-07-06: 2026-07-03 finding 1 (dashboard embed drift) — the dashboard now exposes the embedded bundle hash via the `version` RPC, response headers, and HTML meta tags, warns when the running binary's bundle differs from `apps/console/dist`, and the self-harness `dashboard ui fresh` step uses the same hash comparison. See docs/local-contract.md.
- 2026-07-14: 2026-06-28 finding 1 (`scenery ps` treated as runtime proof) — default `scenery up --detach --wait ready` now requests every advertised route and one script or stylesheet asset from each frontend before returning; `scenery ps` remains an inspection surface rather than a probe.
- 2026-07-06: 2026-07-03 finding 3 (Postgres review) — all four code findings (duplicate_database race, mixed-app SQLite branch rejection, reset/drop resolving all Postgres services, swallowed trailing `--yes`) were verified already fixed on main by commit f07065c2; the docs/knowledge.json and 0093 plan-text drift had already been corrected (0093 is completed and indexed as such). The self-harness postgres probe now provisions a disposable managed container when missing (cleaning up what it created) instead of hard-failing.
- 2026-09-02: Public router ownership verification — public requests now read an immutable in-memory route snapshot. Candidate public-route sessions retain the full PID-reuse-resistant fingerprint check during restoration/registration, deploy/session changes republish the snapshot, and a bounded owner monitor invalidates exited owners; request handling no longer reads `deploy.json` or inspects a process. Enabled-but-down apps and backend failures remain fail-closed with `503`.

## Open

### Deeper Architecture Checks

- Area: harness
- Severity: low
- Owner: scenery runtime
- Created: 2026-04-27
- Review after: 2026-11-20 (last reviewed 2026-10-21)

The self-harness enforces dependency allowlists, forbidden imports, package
direction rules, generated-file hygiene, and file-size thresholds. New checks
should target a demonstrated gap in an established boundary; inspect
`scripts/verify/harness_arch*.go` before proposing an already enforced rule.

### Long Build Tests

- Area: tests
- Severity: low
- Owner: scenery runtime
- Created: 2026-04-27
- Review after: 2026-11-20 (last reviewed 2026-10-21)

Track remaining link, preparation, and test-body cost in active
[0145](plans/0145-test-loop-attribution.md). Preserve ordinary in-process
coverage and move real toolchain/process proof to explicit release probes,
following the root 100ms p95 policy. Historical full-suite durations above
are incident evidence, not a current measurement or permission to keep slow
external-boundary tests in the ordinary lane.
