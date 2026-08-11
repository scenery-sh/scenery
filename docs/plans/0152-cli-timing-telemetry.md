# CLI Timing Telemetry

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery already best-effort appends coarse command timings to
`~/.scenery/telemetry.jsonl`, but the data has no supported read surface and no
application identity. This plan adds `scenery telemetry` so developers and
agents can inspect recent command timings and grouped summaries without reading
private implementation files. New records gain configured app ID/name
attribution, allowing exact app filtering without persisting filesystem paths.

The intended command is:

    scenery telemetry [--app <id-or-name>]... [--command <command>]... [--since <duration>] [--limit <n>] [-o human|json]

It reads the existing global owner-only telemetry store, streams it with bounded
record retention, reports overall/per-app/per-command timing summaries, and
keeps historical app-less records visible as unattributed data.

## Progress

- [x] (2026-08-11) Traced the existing best-effort recorder, CLI help/schema
  identity conventions, app discovery, and validation requirements.
- [x] (2026-08-11) Registered ExecPlan 0152 in the active plan and knowledge
  indexes.
- [x] (2026-08-11) Added privacy-preserving app attribution to new timing records.
- [x] (2026-08-11) Implemented bounded telemetry querying, app/command/time filters, human
  rendering, and current JSON output.
- [x] (2026-08-11) Added focused recorder/query/filter/schema/help tests.
- [x] (2026-08-11) Updated current CLI and agent-facing documentation.
- [x] (2026-08-11) Ran the changed-area validation union and recorded ONLV runtime proof.

## Surprises & Discoveries

- Observation: the telemetry store is global at `~/.scenery/telemetry.jsonl`,
  not app-local, even though app build/session evidence commonly lives under an
  app root's `.scenery/` directory.
  Evidence: `cmd/scenery/telemetry.go` and `docs/local-contract.md`.

- Observation: existing records intentionally exclude flags, arguments, paths,
  and identifiers beyond coarse command/mode/version timing.
  Evidence: `cliTelemetryRecord` and `telemetryCommand`.

- Observation: the first full self-harness run found the initial ExecPlan
  missing required headings and observed one unrelated concurrent evolution
  test failure after a standalone full suite had passed. Adding the required
  sections fixed knowledge validation; the focused cached evolution test and
  the next complete self-harness both passed without a code change.
  Evidence: `.scenery/harness/self-latest.json` and
  `TestConcurrentIssuedChangePlanApplyReturnsOneReplay`.

## Decision Log

- Decision: Keep one global telemetry store and add optional `{id,name}` app
  attribution rather than splitting or copying records into each app root.
  Rationale: one store already exists, global commands have no app, and a
  singular stream supports cross-app comparison without proliferating state.
  Date/Author: 2026-08-11 / Codex.

- Decision: Never persist app roots or raw CLI arguments; resolve only the
  configured stable app ID and display name after command timing has stopped.
  Rationale: app filtering needs identity, not machine-specific paths, and
  attribution overhead must not inflate the measured command duration.
  Date/Author: 2026-08-11 / Codex.

- Decision: Retain at most the requested recent records while streaming the
  complete file for aggregate counters.
  Rationale: telemetry is append-only and may grow indefinitely; the read
  command must have bounded record memory while still producing useful totals.
  Date/Author: 2026-08-11 / Codex.

## Context and Orientation

`cmd/scenery/main.go` measures every CLI invocation and calls the recorder in
`cmd/scenery/telemetry.go`. The recorder appends compact JSON lines beneath the
user home. `internal/app` owns `.scenery.json` discovery and exposes configured
`Config.AppID()`/`Config.Name`; the telemetry path consumes only those values.

The new read command lives in `cmd/scenery/telemetry_command.go`. Like other
machine-readable commands, its data is wrapped in the singular `scenery.cli`
envelope and carries an exact command-payload identity from
`cmd/scenery/payload_identity.go` backed by a checked schema under
`docs/schemas/`.

## Milestones

1. Attribute new records to configured applications without storing paths or
   affecting command success/timing.
2. Add a bounded query and aggregation command with exact app/command/time
   filters and human/JSON rendering.
3. Publish help, schema, documentation, knowledge, and harness coverage.
4. Validate focused behavior, the full repository, a real app invocation, and
   the complete self-harness before closing the plan.

## Plan of Work

Extend the existing record shape with one optional app object. Discover that
identity after the measured command duration and ignore every discovery error.
Stream the global JSONL store, skip invalid lines with a visible warning,
calculate counters incrementally, and retain a ring of only the requested most
recent matching records. Render the same response as a typed JSON payload or a
compact table. Register the command in dispatch and help, check its complete
schema revision, and update current contract documents.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./cmd/scenery -run 'Test(Telemetry|RecordCLITelemetry|LoadCLITelemetry|CLIPayloadSchemaRevisionsMatchCheckedSchemas)'
    go test ./cmd/scenery
    go test ./...
    go build -o .scenery/harness/bin/scenery ./cmd/scenery
    .scenery/harness/bin/scenery harness self --summary --write

For runtime proof, build a temporary current binary, run one command from a
real app root with an isolated `HOME`, and query the resulting app ID through
`scenery telemetry --app <id> -o json`.

## Validation and Acceptance

Acceptance requires all of the following:

- new app-scoped records contain exact configured ID/name and no path;
- app filters match either ID or name, multiple filters are ORed, and combined
  app/command filters are ANDed;
- historical app-less records remain queryable without an app filter;
- the record result is bounded to 1-10,000 entries while aggregates cover all
  matches;
- malformed records are skipped visibly without blocking valid data;
- human and current `scenery.cli` JSON output both work;
- focused tests, `go test ./cmd/scenery`, `go test ./...`, schema validation,
  knowledge validation, and the full self-harness pass.

## Idempotence and Recovery

The query is read-only and safe to repeat. Recording remains best effort and
append-only. An interrupted write can leave at most one invalid final line;
querying skips it and reports the invalid count. Existing records require no
migration because app identity is optional on disk. Deleting the owner-only
telemetry file resets local history without affecting Scenery operation.

## Artifacts and Notes

The durable contract artifacts are:

- `docs/schemas/scenery.telemetry.schema.json`;
- the `scenery.telemetry` payload revision in
  `cmd/scenery/payload_identity.go`;
- real-run evidence in `/tmp/0152-telemetry.json` and
  `/tmp/0152-version.txt`;
- final self-harness evidence under `.scenery/harness/`.

## Interfaces and Dependencies

No new dependency or environment variable is introduced. The implementation
uses the Go standard library, existing `internal/app` discovery, existing CLI
flag/envelope helpers, and the existing schema/harness machinery. The public
interface is the command grammar and `scenery.telemetry` JSON payload; the raw
JSONL file remains machine-local implementation state.

## Outcomes & Retrospective

Completed on 2026-08-11 against `main` commit
`a22b868f9d67c6d931aa5657b5189ef8fc02e178`. Every new CLI record now includes
configured app ID/name when discovery succeeds, without persisting paths or
charging discovery to the measured command duration. `scenery telemetry`
streams the global owner-only store, supports repeatable exact app and command
filters plus time bounds, keeps at most 1-10,000 recent records, and reports
overall/per-app/per-command timing summaries in human or current JSON output.

Focused telemetry/schema tests, `go test ./cmd/scenery`, `go test ./...`, the
quick self-harness, and the complete 22-step self-harness passed with the cached
test policy. A temporary current binary run from the ONLV root recorded app
`clean-tech`; querying `--app clean-tech -o json` returned exactly that record
under kind `scenery.telemetry`. Evidence is in `/tmp/0152-telemetry.json` and
`.scenery/harness/self-latest.json`.
