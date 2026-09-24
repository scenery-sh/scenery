# Recorded Process Ownership, Small SDK Closure And One Input Snapshot

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

`VNEXT.md` sets three direction points this plan implements in the current
code.

First, with many agents and worktrees on one machine, Scenery may stop only the
processes it recorded as its own when it created them, by exact identity (PID
plus the start time, executable and command fingerprint captured at creation),
never by a command-line pattern, a port number or a parent relationship.
Today several cleanup paths scan `ps`, match command lines or environment
variables, look up the owner of a listening port through `lsof`, or signal
whatever process group a recorded PID currently belongs to. After this plan
every signaling path acts only on a verified recorded owner; an unrecorded
process that merely resembles a Scenery process is left alone and the
operation reports a named failure instead of killing it.

Second, the application-facing Go packages `scenery.sh/db`, `scenery.sh/auth`
and `scenery.sh/durable` import the complete runtime implementation
(`scenery.sh/runtime`, 67 non-standard packages including assistant, MCP and
durable-store machinery). After this plan they depend only on the small
request-context layer (`internal/appsdk`, `runtime/shared` and small leaves),
and the runtime host registers its implementations into that layer, the same
way the root `scenery` package already works. Public import paths and
signatures stay unchanged; the observable proof is the `go list -deps`
closure of each package.

Third, one warm handler-body edit under `scenery up` rediscovers its inputs
several times: framework and workspace preparation, the Go input fingerprint,
per-process identities and a full snapshot rescan before activation each walk
or hash overlapping parts of the tree. After this plan a generation captures
one immutable input snapshot, and preparation, process identity and the
pre-activation verification consume it; the verification checks only that the
captured snapshot still describes the tree, without re-deriving what the
capture already proved.

## Progress

- [x] (2026-09-24) Milestone 1: recorded-identity cleanup. Session cleanup
  (`internal/agent/session_cleanup.go`) signals only the recorded supervisor
  owner and registered children that still verify; the `ps` command-line
  scan, the environment scan (`/proc/*/environ`, `ps eww`) and the
  "looks like a scenery process" fallbacks are deleted. `devprocess` tree
  signals reach a process group only when the PID leads it. The supervisor
  control listener records its `Owner` and no longer finds and kills the
  holder of its port through `lsof`. The agent records its lock holder in
  `run/agent-owner.json`; stale-agent stops use only that record, replacing
  the command-line scans for user and root agents and Caddy edges. Caddy
  `edge.Stop` verifies the recorded start time. `scenery system agent
  cleanup` reports legacy processes as `running_pids` and signals none.
  `go run ./scripts/verify --probe dev-cleanup --summary` passes: an
  unrecorded look-alike under the session state root survives, a record
  whose identity moved survives, recorded children and owner stop.
- [ ] Milestone 2: small SDK closure for `db`, `auth`, `durable`.
- [ ] Milestone 3: one input snapshot per generation.

## Surprises & Discoveries

- `devprocess.TerminateTreePID(pid)` signaled `-getpgid(pid)` for any PID. A
  recorded process that had not been started as its own group leader shared
  its parent's group, so a stale-session stop could reach the parent's whole
  group (for a process started from a shell, the shell's job).
- The production path of session cleanup already used only registered
  records; the command-line and environment scans ran only through
  `CleanupStaleSessionProcesses`/`StopDeletedSessionProcesses`, which only the
  release `dev-cleanup` probe called, and through the pattern fallbacks for
  records without an owner.

## Decision Log

- Decision: process-table reads stay allowed for observation (`scenery doctor`,
  deploy diagnostics, `scenery system agent cleanup` reporting); only the
  decision to signal must come from a recorded, verified owner. Rationale:
  VNEXT forbids ownership by resemblance, not diagnostics. Date: 2026-09-24.
  Author: Claude.
- Decision: a tree signal targets a process group only when the recorded PID
  leads that group (its process-group ID equals its PID, which is how
  `devprocess.ConfigureChild` starts every child); otherwise only the PID is
  signaled. Rationale: signaling `-getpgid(pid)` for a process that joined its
  parent's group signals the parent's whole group, which is ownership by
  parent relationship. Date: 2026-09-24. Author: Claude.
- Decision: `SessionOwnerProcessLive` keeps treating a live PID whose
  identity no longer verifies as live. It decides liveness, never a signal,
  and the recorded start time is formatted in local time, so a daylight-saving
  change makes every live owner fail verification; judging those dead would
  let a second supervisor claim a live root. Signals still require
  verification. Date: 2026-09-24. Author: Claude.
- Decision: the agent's owner record is a new small artifact
  (`scenery.agent.owner`, `run/agent-owner.json`) instead of a field in
  `agent.json`, because the agent state descriptor also identifies the health
  response and changing it would make every new CLI refuse the running
  machine agent until it restarts. Date: 2026-09-24. Author: Claude.
- Decision: `scenery system agent cleanup` keeps reporting legacy `~/.onlava`
  processes but never signals them, because they were not recorded by this
  Scenery; the operator stops them. Date: 2026-09-24. Author: Claude.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Process ownership records: `internal/agent/owner.go` captures an `Owner`
(PID, start time, executable, command-line hash) and `VerifyOwner` compares a
record with the live process. Sessions (`internal/agent/types.go` `Session`)
record the supervisor owner and each child in `Processes`. Cleanup lives in
`internal/agent/session_cleanup*.go`; tree signals in
`internal/devprocess/process_*.go`. Edge and agent lifecycle cleanup lives in
`cmd/scenery/edge.go`, `cmd/scenery/edge_process.go`,
`cmd/scenery/edge_helper.go`, `cmd/scenery/agent.go`,
`cmd/scenery/agent_cleanup.go` and `internal/edge/lifecycle.go`. The supervisor
control listener reclaims its port in `cmd/scenery/dashboard_state.go`.

SDK: the root package `scenery.go` delegates to `internal/appsdk`, which holds
registration hooks the runtime fills. `db/db.go`, `auth/*.go` and
`durable/runtime.go` still import `scenery.sh/runtime` directly.

Snapshot: `cmd/scenery/watch*.go` captures a `fileSnapshot`;
`cmd/scenery/dev_build_pipeline.go` converts it into `build.SourceSnapshot`
(`internal/build/build.go`) for contract compilation and preparation;
`internal/build/build_input.go` and `development_processes.go` compute the Go
input fingerprint and process identities; the supervisor rescans the tree
before activation (`cmd/scenery/dev_app_start.go`).

## Milestones

Milestone 1 removes every signaling path that selects processes by command
line, environment, listening port or parent group, and makes the remaining
paths verify a recorded owner. Acceptance: unit tests show an unrecorded
look-alike process is not selected, a recorded verified one is, and the
release `dev session process cleanup probe` proves the same with real
processes.

Milestone 2 moves the runtime functions used by `db`, `auth` and `durable`
behind `internal/appsdk` hooks. Acceptance: `go list -deps ./db ./auth
./durable` contains no `scenery.sh/runtime` and no `scenery.sh/internal/assistant*`
or `mcp*` package, and the runtime tests still pass.

Milestone 3 threads one captured snapshot through preparation, identity and
verification. Acceptance: `build.Step` telemetry of a warm handler-body edit on
`testdata/apps/multiservice` (process-model probe) shows no second full tree
walk, and edit-to-response decreases.

## Plan of Work

See Milestones; each milestone is committed separately with its tests and
contract-doc updates.

## Concrete Steps

Working directory: the repository root.

    go test ./internal/agent ./internal/devprocess ./internal/edge ./cmd/scenery
    go test ./db ./auth ./durable ./runtime
    go test ./internal/build ./cmd/scenery

## Validation and Acceptance

Changed-area classes: Go packages, CLI JSON contract (agent cleanup payload),
runtime. Run `go run ./scripts/verify --summary --write` after the fresh
worktree preflight and fulfill `changed_area.recommended_commands` from
`.scenery/harness/agent-context.json`. Named probes: `go run ./scripts/verify
--probe process-model --summary --write` for Milestone 3; the release-only dev
session cleanup probe is exercised through its unit-tested check function.
Run `golangci-lint run ./...`.

## Idempotence and Recovery

All changes are source edits; rerunning tests is safe. No durable state is
migrated except the agent state record gaining the agent owner, which the
agent rewrites at every start.

## Artifacts and Notes

None yet.

## Interfaces and Dependencies

No new dependencies.
