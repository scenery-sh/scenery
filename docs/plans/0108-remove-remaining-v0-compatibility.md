# Remove Remaining v0 Compatibility

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current throughout the cleanup.

## Purpose / Big Picture

Remove the remaining unsupported v0-era compatibility surfaces after the Go
directive frontend was deleted. Scenery will expose one CLI machine protocol,
one app-config filename, one native runtime composition path, and one standard
auth callback and environment contract. Dead registries, aliases, generated
fixtures, and no-op persistence APIs disappear instead of remaining as shims.

## Progress

- [x] 2026-07-12 - Re-read repository instructions, current contracts, active plans, documentation status, and the whole-repository Ponytail audit findings.
- [x] 2026-07-12 - Removed the CLI v0 selector and every `--json` / `--api-version` compatibility branch in favor of `-o json|jsonl` and `scenery.cli.v1`.
- [x] 2026-07-12 - Removed orphan runtime registries, implicit Go-secret discovery, public middleware shims, and unchecked registration wrappers.
- [x] 2026-07-12 - Removed `.config.json`, no-op devdash persistence, standard-auth metadata and environment aliases, legacy dev-service fields, and the Google callback alias.
- [x] 2026-07-12 - Synchronized docs, schemas, generated artifacts, indexes, agent instructions, workflows, templates, and scripts.
- [x] 2026-07-12 - Passed focused tests, the uncached Go suite, Go vet, tracked-source contract searches, documentation inspection, schema validation, fixture/runtime probes, and self-harness.

## Surprises & Discoveries

- The previous directive-frontend removal intentionally retained CLI v0 and `.scenery.json` configuration because they were independent contracts; this cleanup explicitly supersedes the CLI-retention decision while preserving `.scenery.json` as the sole config filename.
- Current public docs still advertise `--json`, `--api-version`, `.config.json`, the Google connect callback alias, and removed public runtime packages, so implementation and documentation must move together.
- Reusing `-o` exposed a real collision in `scenery build`; the binary destination is now the explicit `--output` flag while `-o json` owns CLI serialization consistently.
- Live `scenery up` is a stream and therefore requires `-o jsonl`; detached `up` returns one `scenery.cli.v1` document with `-o json`, while its background child must independently run in JSONL mode.
- Self-harness found two internal detached-storage consumers that still assumed bare v0-era command JSON. Converting the child stream and unwrapping the v1 envelope made the write/restart/read proof pass.

## Decision Log

- Decision: Treat all eleven audited findings as one removal boundary, with no deprecation cycle or compatibility parser.
  Rationale: The user explicitly stated v0 is unsupported and requested every remaining audited compatibility surface be fixed.
  Date/Author: 2026-07-12 / user and Codex.
- Decision: Preserve command-specific data schemas inside the single `scenery.cli.v1` envelope where those schemas remain useful.
  Rationale: Removing selector forks does not require throwing away typed command results; it requires one stable machine-output transport.
  Date/Author: 2026-07-12 / Codex.
- Decision: Require `.scenery.json` and canonical configured standard-auth environment names without filename or environment fallback ladders.
  Rationale: Singular explicit configuration is easier to inspect, validate, and maintain than heuristic compatibility discovery.
  Date/Author: 2026-07-12 / Codex.

## Outcomes & Retrospective

Scenery now has one current machine protocol: non-streaming commands return
`scenery.cli.v1` with `-o json`, and streams return monotonic
`scenery.cli.event.v1` records with `-o jsonl`. The v0 selector, old command
schemas, app-config filename alias, runtime middleware and `et` packages,
reflection-based secret injection, auth fallback ladders, callback alias,
unchecked runtime registration shims, rejected dev-service fields, and no-op
devdash persistence are deleted.

The cleanup removed more than it added and introduced no replacement
compatibility layer. Current tracked-source searches find none of the removed
spellings outside historical plans. The uncached Go suite and Go vet pass;
documentation inspection reports no missing or stale documents; and full
self-harness passes all functional lanes, including schema validation,
generated TypeScript conformance, UI validation, parallel runtimes, Postgres,
and local-storage restart durability. Its remaining warnings are advisory
review-date, pre-existing file-size, and timing notices.

## Context and Orientation

CLI selector normalization and command dispatch live under `cmd/scenery`.
Edition-2027 generated composition and runtime registration live under
`internal/codegen`, `internal/vnext`, and `runtime`. App-root discovery lives
in `internal/app`. Standard auth lives under `auth`, with generated client and
inspection metadata under `internal/clientgen` and `cmd/scenery`. Dashboard
state is under `internal/devdash`; Victoria is already the supported trace and
log substrate.

The removal inventory is: `scenery.cli.v0`, `--api-version`, command-local
`--json`; `testdata/golden`, `scenery.sh/et`, runtime mocks; implicit `var
secrets` reflection; public middleware registration; `.config.json`; orphan
standard-auth metadata; auth environment fallbacks; rejected legacy dev-service
fields; `/auth/google/connect/callback`; no-op devdash observability storage;
and unchecked runtime registration wrappers.

## Milestones

1. Make `-o human|json|jsonl` the singular CLI output selector and remove all v0 envelope/argument code.
2. Delete unsupported runtime registration, middleware, mock, secret-reflection, and golden-generation surfaces.
3. Delete configuration, auth, dev-service, callback, and dashboard-persistence compatibility surfaces.
4. Synchronize every current contract and prove there are no surviving tracked references or regressions.

## Plan of Work

Apply the removals in independently testable code slices. Migrate current tests
to the singular supported API only when they still prove supported behavior;
delete tests whose sole purpose is preserving a removed shim. Do not introduce
replacement adapters. After code compiles, search all tracked sources for the
removed spellings and update current contracts, schemas, generated clients,
fixtures, and plan notes that incorrectly claim the aliases remain supported.

Historical ExecPlans retain their historical narrative, but any statement that
actively prescribes a removed command or callback must gain a concise current-
contract correction when leaving it untouched would mislead an executor.

## Concrete Steps

1. Collapse CLI output parsing and encoding to `scenery.cli.v1`, remove `--api-version` and `--json`, and migrate help/tests/harness subprocesses to `-o json` or `-o jsonl`.
2. Remove orphan golden files, `et`, runtime mock APIs, unchecked wrappers, implicit secret scanning/injection, and middleware registration/chain execution.
3. Require `.scenery.json`, remove JSON-shape sniffing, and let strict config decoding reject unknown dev-service fields directly.
4. Remove devdash trace/log persistence methods and tests now that reads and writes use Victoria.
5. Remove `internal/standardauthmeta`, collapse auth env resolution to configured canonical names, and remove the Google connect callback alias while retaining purpose dispatch on `/auth/google/callback`.
6. Update AGENTS, SKILL, README, cookbook, current contracts, schemas, environment registry, generated clients, knowledge index, and plan indexes.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/scenery`:

    gofmt -w <changed Go files>
    go test ./cmd/scenery ./runtime ./internal/app ./internal/devdash ./internal/codegen ./internal/vnext ./auth
    go test -count=1 ./...
    go vet ./...
    rg -n 'scenery\.cli\.v0|--api-version|--json|\.config\.json|/auth/google/connect/callback|scenery\.sh/(et|middleware)' --glob '!docs/plans/*.md' .
    go run golang.org/x/tools/cmd/deadcode@v0.46.0 -test ./...
    go run ./cmd/scenery inspect docs -o json
    go run ./cmd/scenery harness self --summary --write

Acceptance requires one CLI machine protocol, one app-config filename, no
removed public package or registry, no implicit secret reflection, no auth
fallback ladder or callback alias, no devdash observability persistence shim,
and no gated or weakened tests. Self-harness must report no blocking error.

## Idempotence and Recovery

All deletions are Git-tracked and recoverable from history. Work in compileable
slices and use focused tests after each slice. Do not install a shared Scenery
binary or commit `.scenery/` output. If a current native caller is discovered,
retain the minimum native primitive it needs without restoring the compatibility
name, heuristic, or selector.

## Artifacts and Notes

The durable evidence is the final tracked-source search, diff statistics,
focused and full test output, dead-code output, documentation inspection, and
self-harness summary.

## Interfaces and Dependencies

Reuse the edition-2027 compiler, generated native composition, Victoria-backed
observability, strict JSON decoding, and existing standard-auth configuration.
Add no dependency, environment variable, filename alias, parser shim, or
translation layer.
