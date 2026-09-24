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
- [x] (2026-09-24) Milestone 2: small SDK closure. `internal/appsdk/host.go`
  defines one `Host` interface the runtime registers from its package
  initialization (`runtime/sdk_host.go`); `auth` (current authentication,
  `WithContext`, standard-auth handler and endpoint registration, JSON contract
  codecs), `durable` (`Signal`, `Step`) and `db` (`.env` loading, now in
  `appsdk.LoadDotEnv`) no longer import `scenery.sh/runtime`. `DurableRun`
  moved to `runtime/shared` and `runtime.DurableRun` is an alias, so existing
  callers compile unchanged. `go list -deps` non-standard closures: `db`
  68 -> 41, `auth` 73 -> 53, `durable` 68 -> 7 (all packages 289/296/289 ->
  247/265/203); none contains `scenery.sh/runtime`, assistant, MCP or durable
  store packages. A new architecture rule forbids `scenery.sh/runtime` in
  non-test files of the SDK packages. `go test ./...` passed and
  `go run ./scripts/verify --probe capability-authority --summary` passed
  (standard auth dev bootstrap and `/auth/me` through the host).
- [x] (2026-09-24) Milestone 3 baseline. A scaled copy of
  `testdata/apps/multiservice` (41 services, 2,446 Go files, 4,000 web files;
  generator and scripts kept outside the repository) under `scenery up -o
  jsonl`, ten warm handler-body edits polled every 10 ms: median edit to new
  response 1,212 ms. Median step timeline of a build request: 200 ms before
  `go build` (compile-start events 18, `framework.verify` 18,
  `workspace.cache` 87 of which a second framework walk 15, a workspace walk
  for the dependency fingerprint 20 and the build fingerprint 11,
  `workspace.verify before_compile` 25-30, `go.input_fingerprint` 27-33),
  `go build` 500, then `workspace.verify after_compile` 30 and `process.retain`
  25 in sequence before the 330 ms preflight. The fresh
  `supervisor.snapshot_verify` rescan (67 ms) already ran beside the preflight.
- [x] (2026-09-24) Milestone 3 changes, each proven equivalent by a test:
  the workspace's framework fingerprint is the source digest
  `framework.verify` just checked (the persisted metadata fingerprint cache is
  deleted); the dependency fingerprint uses the workspace membership the
  preparation just enforced instead of walking it; a cached refresh keeps the
  workspace lock into the process build, which then skips `before_compile`;
  `after_compile` verification and build recording moved into the build join
  so retain and preflight start first; a retained content digest costs one
  metadata read (the second `lstat` on a hit is gone, discovery observes each
  consumed file once and each package directory once, the framework walk's
  metadata is reused, and a preparation reads each workspace file's metadata
  once); generated artifact stamps use retained digests instead of reading and
  hashing every generated file; the retained digest bound is 65,536.
  Result on the same scaled app: median edit to response 1,078 ms (-134 ms,
  -11 %), 117 ms before `go build` (was 200), `go.input_fingerprint` 12 ms
  (was 29), projections 1.3 ms (was 10.5). `go run ./scripts/verify --probe
  process-model` and `--probe dev-process` pass.

## Surprises & Discoveries

- The estimate that one snapshot would save about 0.8 s on ONLV was wrong for
  the current code. It summed the 0200 ONLV costs of preparation (376 ms),
  input fingerprint and identity (210 ms) and the snapshot rescan (200 ms), but
  the rescan already runs beside the candidate preflight and several walks have
  been memoized since. On the scaled fixture the whole input rediscovery on the
  critical path was about 150 ms; the remaining critical path is `go build`
  (about 500 ms) and the first execution of the new executable (preflight,
  about 330 ms). Evidence: the median step timelines in Progress.
- The retained content digest cache held 16,384 entries first-in-first-out. A
  warm ONLV build consults about ten thousand workspace Go files, their authored
  copies, generated artifacts, the framework and module dependencies, which can
  exceed it; then every entry is evicted before its next use and every build
  rereads everything. The bound is now 65,536.
- Replacing the per-file existence `stat` of materialization with directory
  listings did not change `workspace.materialize` (26-27 ms); its cost is the
  membership walk that removes unexpected files, which stays as the
  reconciliation against changes made outside Scenery. The change was reverted.

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
- Decision: standard auth keeps its public `auth.RegisterStandard` entry and
  registers through `appsdk.Host` instead of moving to a new public package,
  because generated application mains call it and internal packages cannot be
  imported from application modules. Without a linked runtime it fails with
  `appsdk.ErrNoHost`; `durable.Step` runs its function directly, as it already
  did outside a durable task. Date: 2026-09-24. Author: Claude.
- Decision: the process build consumes the lock its cached refresh kept
  instead of recording a lock epoch. An epoch bumped at every lock acquisition
  would let the build skip verification when no other acquisition happened,
  but an older Scenery binary on the same app root locks without bumping it,
  so the skip would silently adopt its files. A held lock admits no one.
  Date: 2026-09-24. Author: Claude.
- Decision: the membership walk that removes unexpected workspace files and the
  fresh `supervisor.snapshot_verify` rescan stay. They are the reconciliation
  against changes outside Scenery that the watcher cannot prove absent.
  Date: 2026-09-24. Author: Claude.
- Decision: `scenery system agent cleanup` keeps reporting legacy `~/.onlava`
  processes but never signals them, because they were not recorded by this
  Scenery; the operator stops them. Date: 2026-09-24. Author: Claude.

## Outcomes & Retrospective

Completed 2026-09-24. Every process signal now comes from a recorded, verified
owner: no cleanup path selects a process by command line, environment,
listening port or parent group, and the `dev-cleanup` probe proves that an
unrecorded look-alike survives while recorded owners and children stop. The
application SDK packages `db`, `auth` and `durable` no longer link the runtime
implementation (non-standard closures 68/73/68 -> 41/53/7), guarded by an
architecture rule. A warm edit captures its inputs once and the preparation,
identity and verification steps consume what that capture and the held
workspace lock already established: on a 41-service scaled fixture the median
edit to response fell from 1,212 to 1,078 ms and the work before `go build`
from 200 to 117 ms. The expected 0.8 s saving did not exist in the current code
(see Surprises); the latency target is bounded by `go build` and the first
execution of the new service executable, which this plan did not change.

Validation: `go test ./...`, `golangci-lint run ./...` (0 issues),
`go run ./scripts/verify --summary --write` (pass with advisory warnings),
probes `dev-cleanup`, `capability-authority`, `process-model` and
`dev-process` passed; each new test root stays below 100 ms in ten isolated
runs.

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
