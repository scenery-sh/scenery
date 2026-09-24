# Telemetry Containment And Trustworthy Evidence

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

An external review of Scenery at `24f62879` (2026-09-24) found that the
telemetry and reliability work of the previous days (start failure
containment for the local agent, build blocks, `scenery telemetry report`, the
bounded development runtime RPC) was right in direction but incomplete in
three ways that make protections silently absent or evidence misleading:

1. Start failure containment of the local agent did not run on upgraded
   installations and had bypasses. A launchd plist written before the agent
   gained `--supervised` kept running the agent without containment after
   `scenery system agent restart`, because restart re-registered the existing
   plist unchanged; this host's `~/Library/LaunchAgents/dev.scenery.agent.plist`
   was such a plist. `startAgentServer` in `cmd/scenery/agent.go` ended the
   start incident before `Server.Run` in `internal/agent/server.go` published
   the agent state, so a failing state write escaped containment. The retry
   count lived only in the persisted incident, so an unwritable incident
   reset it to one on every restart.
2. `scenery telemetry report` (`internal/telemetryreport`) attributed shell
   outcomes and durations to Scenery commands that did not produce them
   (`false && scenery check`, `scenery check && false`, pipes), counted quoted
   examples and here-document bodies as invocations, charged a build error
   that named an unknown operation to another build, silently discarded a
   transcript that failed part way, retained every parsed tool call before
   aggregating, and kept a timestamp per failure for burst detection.
3. Report intake of the runtime control backend (`handleReport` in
   `cmd/scenery/dashboard.go`) decoded bodies of any size and started one
   goroutine per exported report without a concurrency bound.

After this plan: restarting a supervised agent brings its job up to the
current invocation; a supervised agent never exits for a failed start and
bounds its retries in-process whether or not it can write the incident; the
report separates attempts, shell outcomes and Scenery's own outcomes, states
how completely it read its sources, and streams transcripts; intake refuses
bodies over 1 MiB and exports through a bounded queue whose drops and failures
the dev-runtime `status` reports.

Out of scope, recorded for follow-up: ONLV adoption (framework pin, generated
client with `build_block` and export counts, blocked-state display, wrapper
preflight consolidation) and a native CLI event contract (invocation
correlation, agent-home isolation of `telemetry.jsonl`, rotation, producer
identity, foreground build events). The review recommends investing in native
capture instead of extending historical inference further.

## Progress

- [x] 2026-09-24 Verified every review finding against the source at `24f62879`.
- [x] 2026-09-24 Start containment: start phase includes state publication;
      in-process retries with an in-memory count (`StartContainment`);
      diagnostic incident written without fsync.
- [x] 2026-09-24 Supervisor jobs: `ReconcileAgentLaunchd` before restart,
      `ReconcileAgentSystemd` before `systemctl restart`, restart JSON
      `supervisor_updated`, doctor check `runtime.agent_supervisor`.
- [x] 2026-09-24 Report: shell scan (`internal/telemetryreport/shell.go`),
      attributable outcomes, unknown outcomes, Codex structured results,
      unmatched build errors, source coverage, streaming per-file tallies,
      line reader that skips oversized lines, per-hour burst stats,
      one sort per timing.
- [x] 2026-09-24 Intake: 1 MiB report limit with 413, bounded export queue
      with batching, `observability.export` in `status`, regenerated fixture
      client.
- [x] 2026-09-24 Contract docs and schemas updated.
- [x] 2026-09-24 Validation: `go run ./scripts/verify --summary --write`
      passed with warnings (38 pre-existing knowledge review-due entries; the
      pre-existing size of `cmd/scenery/edge.go`, where one line changed; the
      uncached full Go suite over its advisory cached budget);
      `golangci-lint run ./...` reported 0 issues; both fixture regenerations
      are stable; the new and changed test roots stay below 60 ms in repeated
      isolated runs.
- [ ] Commit and review; ONLV adoption and native capture follow separately.

## Surprises & Discoveries

- This host's installed `dev.scenery.agent.plist` lacks `--supervised`
  (`grep -o "<string>[^<]*</string>" ~/Library/LaunchAgents/dev.scenery.agent.plist`),
  so the review's rollout defect applies here, not only in theory.
- Codex transcripts record no usable command duration. In exec scripts,
  `wall_time_seconds` of an `exec_command` result times the read of that
  output chunk (for example `0.000004291` for a `git diff`); the
  `Wall time:` header of an `exec_command` function result also reads
  `0.0000 seconds` for a `scenery check` that exited 1. The previous report
  used these as command durations.
- Codex has three result shapes: `exec_command` function results with a
  `Process exited with code N` header, `shell_command` results with
  `Exit code: N`, and exec scripts printing JSON result objects. The previous
  regular expression matched only the third, and only with
  `wall_time_seconds` immediately before `exit_code`.
- On this host's last 30 days, 1,064 of 1,155 Claude Code shell commands that
  ran Scenery piped its output (`… | tail`), so their exit status was the last
  command's; only 2 were simple commands. The previous report counted piped
  Scenery failures as successes.
- A 20 MiB report posted to intake still receives a clean 413 over real HTTP
  (probed with a throwaway test), so the runtime reporter, which disables
  itself on connection resets, keeps reporting.

## Decision Log

- 2026-09-24, Claude: a supervised agent retries in-process instead of exiting
  after a delay. Containment then depends on no persisted state; the incident
  is diagnostic only and is written atomically without fsync.
- 2026-09-24, Claude: restart rewrites the supervisor job from the current
  template, keeping the executable, socket, router and log (or home) settings
  it parses; a job with arguments Scenery does not render is never rewritten
  and a launchd restart then fails with `failed_precondition` before stopping
  anything. On systemd the rewrite happens where the unit is restarted
  (`startSystemdSupervisedAgent`) and a failure is logged, because refusing
  there would leave an unsupervised spawn racing `Restart=always`.
- 2026-09-24, Claude: attribution is a whitelist, not a shell interpreter. A
  shell command is attributable only when it is one simple command that runs
  Scenery directly; everything else is an attempt with a shell outcome.
  `cd dir && scenery …` is deliberately not attributable.
- 2026-09-24, Claude: Codex runs contribute outcomes only; durations come only
  from Claude Code's tool use and result timestamps.
- 2026-09-24, Claude: export batching concatenates encoded OTLP export
  requests, which protobuf decodes as one request with every resource.
- 2026-09-24, Claude: export counts live in the dev-runtime `status`
  (`observability.export`), which changes its schema revision; ONLV
  regenerates its client in its adoption step.

## Outcomes & Retrospective

Implementation and repository validation are complete; the plan stays active
until the change is merged. The live launchd upgrade on this host was not
exercised: it restarts the installed agent and needs an explicit request.
Remaining follow-ups are outside this plan: ONLV adoption and a native CLI
event contract (see Purpose / Big Picture).

## Context and Orientation

- Local agent start: `cmd/scenery/agent.go` (`agentCommand`,
  `startAgentServer`, `runSupervisedAgent`, `restartAgentViaSupervisor`),
  `internal/agent/start_incident.go` (`StartContainment`),
  `internal/agent/server.go` (`PublishState`), `internal/agent/launchd.go`
  and `internal/agent/systemd.go` (job rendering and reconciliation),
  `internal/agent/supervisor_job.go` (`InstalledSupervisorJob`),
  `cmd/scenery/doctor.go` (`runtime.agent_start`, `runtime.agent_supervisor`).
- Report: `internal/telemetryreport/{agents,shell,lines,builds,cli,findings,report}.go`,
  rendered by `cmd/scenery/telemetry_report.go`; schema
  `docs/schemas/scenery.telemetry.report.schema.json`.
- Intake and export: `cmd/scenery/dashboard.go` (`handleReport`),
  `cmd/scenery/telemetry_export.go`, `cmd/scenery/dashboard_rpc.go`
  (`status`); schema `docs/schemas/scenery.dev-runtime.status.schema.json`;
  client template `internal/generate/dev_runtime_client.ts`.

## Milestones

1. Start containment and supervisor job upgrades.
2. Report attribution, coverage and streaming.
3. Bounded intake and export with visible counts.
4. Validation and outcome recording.

## Plan of Work

Each milestone keeps the repository testable: containment and job
reconciliation first, with focused tests in `internal/agent` and
`cmd/scenery`; the report rewrite with fixture tests in
`internal/telemetryreport`; the intake and exporter with handler and
exporter tests in `cmd/scenery`; then schema revisions, the fixture client
regeneration and the contract text.

## Concrete Steps

From the worktree root:

    go test ./internal/agent ./internal/telemetryreport ./internal/machine ./internal/generate ./cmd/scenery
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write

## Validation and Acceptance

Changed-area classes: Go packages, CLI JSON contract (`scenery.agent.restart`,
`scenery.telemetry.report`, `scenery.dev-runtime.status`), generator template
(`internal/generate/dev_runtime_client.ts`), runtime. The union requires, from
the worktree root after the [Fresh Worktree Preflight](../agent-guide.md#fresh-worktree-preflight):
`go test ./...`, both fixture regenerations above, `golangci-lint run ./...`
and the full verifier `go run ./scripts/verify --summary --write`, then the
commands the refreshed `.scenery/harness/agent-context.json` recommends.

Acceptance, each shown by a named test:

- An old plist without `--supervised` is rewritten with it, keeping its
  executable, socket, router and log, and is left alone the second time
  (`TestReconcileAgentLaunchdUpgradesAnOldJob`); a foreign plist is never
  rewritten (`TestReconcileAgentLaunchdLeavesAForeignJobUntouched`); restart
  reconciles before re-registering and refuses before stopping anything when
  it cannot (`TestRestartAgentViaSupervisorUpdatesTheJobBeforeRegisteringIt`);
  the systemd unit is upgraded and systemd reloaded
  (`TestReconcileAgentSystemdUpgradesAnOldUnit`); doctor names a job without
  containment (`TestDoctorAgentSupervisorCheckNamesMissingContainment`).
- A late state-write failure leaves the incident and releases the attempt's
  lock, socket and listeners (`TestAgentStartPublishesStateBeforeEndingTheIncident`);
  unwritable incident storage still blocks at the eighth attempt with the
  documented delays (`TestSupervisedAgentStartStaysBoundedWhenTheIncidentCannotBeRecorded`,
  `TestStartContainmentBlocksWhenTheIncidentCannotBePersisted`).
- Short-circuits, pipes, quoted examples, comments and here-documents are
  classified correctly (`TestSceneryAttemptsFindCommandsAtCommandPosition`);
  reordered Codex fields and missing exit codes are read by name
  (`TestCodexScriptRunsReadResultsByFieldName`); an unknown operation ID is
  unmatched (`TestBuildErrorNamingAnUnknownOperationIsUnmatched`); a partial
  transcript keeps its evidence and is counted
  (`TestPartialTranscriptKeepsItsEvidence`); oversized lines are skipped
  (`TestReadLinesSkipsOversizedLinesAndContinues`).
- Oversized reports get 413 and are counted in `status`
  (`TestReportIntakeRefusesAnOversizedReport`); a stalled backend holds at most
  the workers and drops beyond the queue
  (`TestTelemetryExporterStaysBoundedBehindASlowBackend`); batches send one
  request per signal (`TestTelemetryExportBatchesReportsPerSignal`).

Not covered by these commands: a live launchd upgrade on this host (it would
restart the installed agent, which needs an explicit request) and the
release-mode local-agent restart probe.

## Idempotence and Recovery

All steps are re-runnable. Reconciliation rewrites a job only when it differs
from the template and never rewrites one it cannot parse. The report reads
files only. Fixture regeneration is deterministic.

## Artifacts and Notes

Real-data check on this host (read-only): `scenery telemetry report
--agent-transcripts --since 720h -o json` built with this change read 284
transcripts completely in 8.6 s with 128 MB maximum resident memory and
reported 7 invalid and 63 oversized records, 41 results without a call and 11
calls without a result.

At the developer's request the host's telemetry was reset after validation on
2026-09-24 at 16:53 UTC: `~/.scenery/telemetry.jsonl` and 374 closed
supervisor logs were moved, and the one live supervisor log copied and then
truncated (its writer appends), into
`~/.scenery/telemetry-archive/20260924T165321Z/`, preserving their paths
relative to `~/.scenery`. Reports on this host now start from that moment;
Victoria observability data, internal failure reports and agent transcripts
were not touched.

## Interfaces and Dependencies

No new dependencies. Changed public surfaces: `scenery.agent.restart` gains
`supervisor_updated`; `scenery.telemetry.report` gains source coverage,
`unmatched_errors`, `scenery_outcome_unknown`, `scenery_attributable` and
`scenery_attributable_failed`; `scenery.dev-runtime.status` gains
`observability.export`; `scenery doctor` gains `runtime.agent_supervisor`.
