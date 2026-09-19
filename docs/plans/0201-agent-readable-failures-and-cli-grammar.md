# Agent-Readable Failures And CLI Grammar

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

An agent drives Scenery through `scenery help -o json` and the `-o json`
envelopes. Two things made that path unreliable. A request written wrongly was
often reported as an internal failure (`SCN9000`) whose message is withheld, so
the agent could not tell what to correct. And a real internal failure carried a
`report_token` that resolved to nothing: the cause was neither printed nor
stored, so the agent had to repeat the command in human mode to learn it.

After this plan a wrongly written request is an invalid request that names the
mistake, every report token resolves locally to its cause with
`scenery inspect report <report-token> -o json`, and one probe holds the parser
to the grammar that `scenery help` advertises, in both directions.

## Progress

- [x] (2026-09-19) Usage errors are classified by type instead of by the prefix
  of their text (commit `8bce465c`): `usageErrorf` (exit 2, `SCN8001`),
  `preconditionErrorf` (exit 3), `unavailableErrorf` (exit 4). Of 182 malformed
  invocations across the CLI, 164 had answered `SCN9000`; none does now.
- [x] (2026-09-19) A report token resolves to its cause. Every site that mints a
  token (`internal/compiler`, `internal/storagefs`, `internal/contractagent`)
  hands the cause to `machine.ReportInternalFailure`; the CLI's sink writes
  `<agent home>/reports/<token>.json` (mode 0600, redacted arguments and cause,
  newest 200 retained) and `scenery inspect report <report-token> -o json`
  returns it as `scenery.failure.report`.
- [x] (2026-09-19) `go run ./scripts/verify --probe cli-grammar` derives its
  cases from `scenery help -o json`: 273 requests that must be refused as
  invalid and 42 advertised read-only requests that must not be refused as
  wrongly written, from 39 commands, in about 12 seconds.
- [x] (2026-09-19) A missing app root (`app.ErrRootNotFound`), a snapshot input
  that is no zip archive and a migration selection without a target are
  classified; the report store and the probe exposed them.

- [x] (2026-09-19) A token minted inside a detached `scenery up` resolves. The
  `process-model` probe first starts the session on an address no host can bind
  (`203.0.113.7:59999`); the answer is one `SCN9xxx` diagnostic whose message
  withholds the address, and `scenery inspect report <report-token> -o json`,
  run afterwards in the same agent home, returns command `up`, the arguments of
  the detached child and a cause that names the address. The real start that
  follows still succeeds. `scenery up --port` outside 0-65535, found the same
  way, is an invalid request now.

## Surprises & Discoveries

- Go's `errors.AsType` for an `ExitCode() int` method matches a wrapped
  `*exec.ExitError`, so `scenery worktree list` outside a Git repository exited
  128, git's code. A wrapping `codedCLIError` takes precedence, which is how the
  worktree commands now answer exit 3.
- The documented example `scenery logs query -o json` was refused, because the
  parser accepted only `jsonl` while JSON is the command's default output. The
  probe's accepted cases are exactly this class.
- `scenery help` spells a flag's alternatives in three ways (`--a|--b`,
  `[--a | --b]`, `-o json|-o jsonl`); the probe's usage parser reads all three
  and treats a line that forwards arguments (`...`) as unparseable.
- The first end-to-end use of the report store found two more misclassified
  requests at once (`check --app-root /nonexistent`, `snapshot verify` of a text
  file), which is the argument for keeping the cause readable.

- A running session mints no token for an ordinary build failure. Breaking the
  Go code or a `.scn` source of a session started on a disposable copy of
  `testdata/apps/multiservice` produced `build.error` events that carry the
  cause themselves (`SCN6202` with the Go compiler's text, `SCN3005`), and no
  report was written. Tokens come from the paths that render a sanitized
  diagnostic, which for `scenery up` is the startup answer of the detached
  session; that is the path the probe exercises.

## Decision Log

- Decision: classify usage errors with typed helpers instead of extending the
  text-prefix list in `cliExitCode`. Rationale: a prefix list misreads any
  runtime error that starts with the same word and silently misses every new
  message. Date: 2026-09-19. Author: Claude.
- Decision: mint sites report causes through a sink in `internal/machine`
  instead of the CLI recording the error it renders. Rationale: a diagnostic is
  minted in several packages and inside long-running processes; all three
  minting packages already import `internal/machine`, and a process without a
  sink (an application runtime) discards causes as before. Date: 2026-09-19.
  Author: Claude.
- Decision: reports live under the agent home, not the app root. Rationale: a
  failure can precede app discovery, and the agent home is already the private
  per-user state directory with an override for tests. Date: 2026-09-19.
  Author: Claude.
- Decision: the grammar probe does not execute `system` and `deploy`.
  Rationale: a parser bug there would act on the host, not on the disposable
  app; their usage errors are covered in process by
  `TestMalformedInvocationsAreInvalidRequestsThatNameTheMistake`. Date:
  2026-09-19. Author: Claude.

## Outcomes & Retrospective

Completed 2026-09-19. A wrongly written request is an invalid request that
names the mistake (164 of 182 malformed invocations had answered `SCN9000`);
every report token the CLI mints, including one minted inside a detached
`scenery up`, resolves locally to its cause; and `--probe cli-grammar` derives
273 refusals and 42 acceptances from the grammar `scenery help` advertises.
Keeping the cause readable paid for itself at once: its first uses found six
more requests that were reported as internal failures. Not in scope and
unchanged: an application runtime has no sink, so a token it mints in an HTTP
answer still resolves to nothing.

## Context and Orientation

`cmd/scenery/cli_flags.go` holds the typed error helpers and the shared flag
parser. `cmd/scenery/main.go` maps an error to its exit code (`cliExitCode`) and
`cmd/scenery/cli_diagnostic_error.go` to its diagnostic. A diagnostic of an
unclassified error is minted by `compiler.TransportDiagnostic`, which withholds
the message and attaches a `report_token`. `cmd/scenery/failure_report.go`
stores and reads reports; `internal/machine/internal_failure.go` is the sink.
`scripts/verify/harness_self_cli_grammar.go` is the probe.

## Milestones

1. Usage errors are invalid requests (done).
2. Report tokens resolve locally (done).
3. The advertised grammar is proved against the parser (done).
4. Prove that a token minted by a running development session resolves (done).

## Plan of Work

Milestone 4 added an assertion to the `process-model` probe: a detached start
that fails internally answers with a token, and `scenery inspect report`
resolves it in a later invocation.

## Concrete Steps

Run from the repository root:

    go test ./cmd/scenery ./internal/machine ./internal/agent
    go run ./scripts/verify --probe cli-grammar --summary
    go run ./scripts/verify --probe process-model --summary
    go run ./scripts/verify --summary --write

## Validation and Acceptance

Changed-area classes: `cli-json-contract`, `go-package`,
`release-sensitive-or-runtime`. Commands, all from the repository root:
`go test ./...`, `golangci-lint run ./...`,
`go run ./scripts/verify --summary --write`, and
`go run ./scripts/verify --probe cli-grammar --summary`. Acceptance: the probe
reports zero diagnostics; `scenery snapshot verify --input /etc/hosts -o json`
answers `SCN8001`; an unclassified failure's token answers
`scenery inspect report <report-token> -o json` with its cause.

## Idempotence and Recovery

Report files are written atomically by rename and pruned on write; deleting
`<agent home>/reports` loses only evidence. The probe works in a disposable
home and app copy under `/tmp` and removes both.

## Artifacts and Notes

`docs/schemas/scenery.failure.report.schema.json` is the report payload.

## Interfaces and Dependencies

`machine.SetInternalFailureSink`, `machine.ReportInternalFailure`,
`storagefs.InternalFailure(cause error)`, and the CLI subject
`scenery inspect report <report-token> -o json`.
