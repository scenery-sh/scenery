# 0114 Supervised Agent Lifecycle

This ExecPlan is a living document; update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

- Status: completed 2026-07-14 (implementation and live acceptance recorded below)
- Owner: scenery runtime / edge
- Created: 2026-07-14

## Purpose / Big Picture

On 2026-07-14, `https://micro.scenery.sh/platform/legacy/projects` returned
Cloudflare 502 while the public edge stack (root privileged helper + managed
Caddy) stayed alive: the Scenery agent that owns the local router at
`127.0.0.1:9440` had died and nothing restarted it. Caddy logged
`dial tcp 127.0.0.1:9440: connect: connection refused`.

`scenery deploy status` claimed the deploy-resume LaunchAgent was "installed"
because `~/Library/LaunchAgents/dev.scenery.deploy-resume.plist` existed, but
`launchctl print gui/501/dev.scenery.deploy-resume` showed the job was never
loaded: `installDeployResumeLaunchAgent` in `cmd/scenery/deploy.go` only wrote
the plist. Immediately after a manual `scenery system agent restart`, local
path routers kept proxying dashboard traffic to the dead previous dashboard
backend, because `startLocalPathRouter` in
`cmd/scenery/local_path_router.go` captured `health.DashboardBackend` once at
startup.

This plan makes agent availability a supervised contract instead of a
best-effort side effect of whichever CLI last started the agent.

## Progress

- [x] 2026-07-14 Root cause traced (unsupervised agent; plist-only "install";
      stale dashboard backend capture in local path routers).
- [x] 2026-07-14 `internal/agent/launchd.go`: supervised launchd job
      (`dev.scenery.agent`, KeepAlive) with bootstrap/bootout/status/kickstart
      and testable command hooks; `StartProcess` cooperates with it.
- [x] 2026-07-14 `cmd/scenery`: `scenery system agent restart` and
      `ensureEdgeAgent` restart through the supervisor; `scenery deploy
      setup/teardown/status` install, remove, and report supervision truth;
      setup skips the sudo helper reinstall when the installed helper already
      matches the current handoff contract.
- [x] 2026-07-14 Agent availability watchdog in the dev supervisor
      (`cmd/scenery/agent_watchdog.go`) after live testing proved launchd
      pends gui-domain KeepAlive respawns.
- [x] 2026-07-14 Local path router: per-request dashboard backend refresh,
      quiet rate-limited proxy errors, bounded dial retry; edge Caddyfile
      gains `lb_try_duration`/`lb_try_interval`.
- [x] 2026-07-14 Focused tests added; `go test ./...` green.
- [x] 2026-07-14 `scenery harness self --summary --write` green from the
      worktree-local harness binary.
- [x] 2026-07-14 Live acceptance on the maintainer machine (supervised
      restart + SIGKILL agent death, public probe + dashboard probe).

## Surprises & Discoveries

- The deploy-resume LaunchAgent had `RunAtLoad=true` but was never loaded on
  the incident machine (`launchctl print` reported "Could not find service"),
  so login recovery silently did not exist. Evidence: incident transcript,
  2026-07-14.
- `launchctl kickstart` without `-k` can race a KeepAlive respawn; the
  supervised start path treats a job that reports a running PID as success
  even when kickstart itself errors.
- launchd on this machine (Darwin 25.5) indefinitely defers KeepAlive,
  RunAtLoad, and StartInterval spawns of gui-domain LaunchAgents as
  "pended nondemand spawn = speculative/inefficient" — observed live: a
  SIGKILLed supervised agent was never respawned in 2.5+ minutes even with
  `ProcessType Interactive` and `ThrottleInterval 2`, while `launchctl
  kickstart` (a demand spawn) always started it instantly. Evidence:
  `launchctl print gui/501/dev.scenery.agent` during live acceptance on
  2026-07-14. Consequence: launchd KeepAlive alone is not sufficient
  supervision; installs/bootstraps must kickstart explicitly, and a watchdog
  in every live `scenery up` supervisor issues a demand start when the agent
  stays unreachable (`cmd/scenery/agent_watchdog.go`).
- The dominant agent killer was scenery itself: `edgeAgentCommandMatches`
  ignored its socket parameter, so `reapStaleAgentRouterOwner` — which runs
  on every agent start — SIGTERMed any same-user agent sharing the default
  router address. Every test, harness, or worktree agent start with a fresh
  temp home killed the machine's real supervised agent (launchd `runs`
  climbed 99→114 during one test-heavy evening, every exit code 0).
  `stopStaleUserSceneryAgents` now skips a live foreign agent — one that
  answers health on its own `--socket` — and lets the new agent take the
  router-port fallback instead. Evidence: launchd run counter stayed flat
  across a full `go test ./cmd/scenery` run after the fix, and climbed by
  several before it.
- `scenery harness self` invoked from an older installed binary validates
  schemas against that binary's payload shapes; use the worktree-local
  `.scenery/harness/bin/scenery` build.

## Decision Log

- 2026-07-14, agent session: supervision uses a launchd user agent installed
  by `scenery deploy setup` (public availability is the contract that
  requires it), not a new daemon or a scenery-owned babysitter process.
  launchd is the platform-native single-owner supervisor and restarts the
  agent at login via RunAtLoad and on exit via KeepAlive.
- 2026-07-14: `localagent.StartProcess` is the single cooperation point: when
  the supervised plist manages the requested socket, it bootstraps or
  kickstarts through launchd instead of spawning an unsupervised process, so
  every existing stop/start path (restart command, edge install, dashboard
  ensure, identity replacement in `Ensure`) cooperates without duplication.
- 2026-07-14: the plist pins the agent executable path recorded at install
  time; `scenery deploy setup` re-runs refresh it after upgrades, and setup
  no longer demands sudo when the installed privileged helper already matches
  the current handoff contract, so supervision repair is unattended-safe.
- 2026-07-14: deploy status gains `agent_supervisor`
  (installed/loaded/running/pid/label/path) and `launch_agent.loaded`; an
  unloaded supervisor or unloaded resume job forces `ready: false`.
- 2026-07-14: because launchd pends gui-domain nondemand spawns (see
  Surprises), recovery is layered: launchd KeepAlive when it works, an
  explicit kickstart after every bootstrap, and an agent availability
  watchdog inside each long-running `scenery up` supervisor (2s health
  pings; two consecutive failures trigger a demand start through
  `localagent.StartProcess`, which kickstarts the supervised job). A
  system-domain LaunchDaemon (`UserName`) would be immune to the pend but
  requires sudo for every lifecycle operation; the watchdog keeps repair
  sudo-free and lives exactly in the processes that need the agent.
- 2026-07-14: WebSockets established through the agent drop on any agent
  restart and must reconnect. The contract only guarantees that new requests
  do not see raw 502s during an ordinary supervised restart (Caddy
  `lb_try_duration 5s` plus local router dial retry); no zero-downtime claim
  is made for established connections.

## Outcomes & Retrospective

Implemented and live-verified on the maintainer machine on 2026-07-14:
supervised `scenery system agent restart` (kickstart, sub-second, PID change
proven by `scenery ps -o json`) and a SIGKILLed agent (recovered in ~4s by
the dev-supervisor watchdog issuing a launchd demand start) both completed
with zero non-200 responses on continuous probes of
`https://micro.scenery.sh/platform/legacy/projects` and the local path-mode
dashboard route; `scenery deploy status -o json` and `launchctl print
gui/501/dev.scenery.agent` agreed throughout, and both deploy target
sessions survived every restart.

## Context and Orientation

The "agent" is the long-lived `scenery system agent` process: it owns the
control socket `~/.scenery/run/agent.sock`, the local HTTP router at
`127.0.0.1:9440` (the upstream for the managed Caddy edge), and the dev
dashboard backend (a fresh loopback address chosen at each agent start).
Public deploy traffic flows: internet → root privileged helper
(`dev.scenery.edge-helper`, TCP proxy on 80/443) → managed Caddy →
`127.0.0.1:9440` (agent router) → app/session backends.

Key files:

- `internal/agent/launchd.go` — supervised launchd job lifecycle.
- `internal/agent/client.go` — `StartProcess` supervision cooperation.
- `cmd/scenery/agent.go` — `restartAgentViaSupervisor`, restart command.
- `cmd/scenery/deploy.go` — setup/teardown/status, resume LaunchAgent
  bootstrap, `agent_supervisor` reporting.
- `cmd/scenery/local_path_router.go` — dashboard backend refresh and quiet
  bounded-retry proxying.
- `cmd/scenery/edge.go` — Caddy edge config `lb_try_duration`.

## Milestones

1. Supervised launchd job in `internal/agent` with hooks and tests.
2. CLI cooperation (restart, edge install, deploy setup/teardown/status).
3. Local path router dashboard refresh and quiet retries; edge retry window.
4. Schemas, docs, validation, live acceptance.

All four landed in this change.

## Plan of Work

Implemented as described in the Decision Log. The supervised job
`dev.scenery.agent` is written to `~/Library/LaunchAgents/` and bootstrapped
into the `gui/<uid>` domain; teardown boots it out before removal. Status is
read through `launchctl print` and reported in `scenery deploy status -o
json` under `agent_supervisor`.

## Concrete Steps

- `scenery deploy setup` — installs and bootstraps `dev.scenery.agent` and
  `dev.scenery.deploy-resume`, restarts the edge; asks sudo only when the
  privileged helper needs reinstalling.
- `scenery system agent restart -o json` — kickstarts the supervised job
  (`supervised: true` in the payload) or falls back to the unsupervised
  stop/start path.
- `scenery deploy teardown` — boots out and removes both LaunchAgents.

## Validation and Acceptance

- `go test ./internal/agent ./internal/edge ./cmd/scenery`
- `go test ./...`
- `.scenery/harness/bin/scenery harness self --summary --write`
- Live: continuous 1s probes of the public deploy route and the local
  path-mode dashboard while running a supervised restart and a `kill -9` of
  the agent; both must recover without any 502 observed by the probes, and
  `scenery ps -o json` must show a PID change with intact sessions.

## Idempotence and Recovery

All launchd operations are idempotent: install boots out any previous job
before bootstrapping; remove tolerates a missing job or plist; bootstrap
retries transient post-bootout EIO failures for up to 10 seconds. If the
supervised agent crash-loops (for example a foreign process owns 9440),
launchd throttles respawns to ~10s intervals and `scenery doctor` /
`scenery deploy status` report the failure; the agent still fails closed on
its process lock.

## Artifacts and Notes

- `~/Library/LaunchAgents/dev.scenery.agent.plist` — supervised agent job.
- `~/Library/LaunchAgents/dev.scenery.deploy-resume.plist` — login resume.
- `~/.scenery/agent/agent.log` — agent stdout/stderr (also used by launchd).
- Incident timeline and live acceptance results: see PR description for this
  change.

## Interfaces and Dependencies

- `launchctl` (macOS launchd) — bootstrap/bootout/kickstart/print in the
  `gui/<uid>` domain; hooks in `internal/agent/launchd.go` keep tests free of
  real launchd.
- JSON payload changes: `scenery.deploy.status` (adds `agent_supervisor`,
  `launch_agent.loaded`), `scenery.deploy.setup` (adds `helper_reinstalled`,
  `agent_supervisor_installed`), `scenery.deploy.teardown` (adds
  `agent_supervisor_removed`), `scenery.agent.restart` (adds `supervised`).
  Schemas under `docs/schemas/` and `cmd/scenery/payload_identity.go` updated
  together.
