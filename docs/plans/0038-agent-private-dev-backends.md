# Agent Private Dev Backends

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series says worktrees should not bind stable host ports. The shipped 0037 agent MVP created the daemon, control socket, router, session registry, and routed session URLs, but `scenery dev` still starts the app on `127.0.0.1:4000` by default and enables the older local HTTPS proxy unless disabled by environment.

After this work, the default `scenery dev` path starts the app on a session-private backend, preferably a Unix domain socket under `.scenery/sessions/<session_id>/run/api.sock`, registers that backend with the agent, and prints the agent route as the public API URL. Explicit `--listen`, `--port`, `--proxy`, and `--trust` flags remain available for compatibility and debugging, but they are no longer the default public surface.

This plan intentionally does not move Grafana, Victoria, legacy async runtime, Postgres, or frontend process ownership. Those are covered by later agent-native local-dev plans.

## Progress

* [x] 2026-05-26: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-26: Default `scenery dev` to an agent-routed private Unix app backend when the agent is available.
* [x] 2026-05-26: Keep explicit `--listen` and `--port` as TCP compatibility/debug paths.
* [x] 2026-05-26: Make the old local HTTPS proxy opt-in through `--proxy`, `--trust`, or an explicit `SCENERY_LOCAL_PROXY=1` environment override instead of defaulting on.
* [x] 2026-05-26: Update focused tests for routed Unix backend registration, TCP fallback behavior, and proxy flags.
* [x] 2026-05-26: Run full repository validation and install the updated `scenery` binary.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-26: The existing local HTTPS proxy only supports TCP upstreams. For compatibility, `--proxy`, `--trust`, or `SCENERY_LOCAL_PROXY=1` now make the app backend a daemon-registered hidden loopback TCP address instead of a Unix socket.

## Decision Log

* Decision: Preserve explicit TCP flags while changing the no-flag default to agent/private.
  Rationale: The agent-native local-dev plan wants humans to stop seeing per-worktree API ports, but keeping explicit TCP flags gives developers an escape hatch and keeps existing CI helpers simple.
  Date/Author: 2026-05-26 / Codex

* Decision: Make the existing local HTTPS proxy opt-in during this phase.
  Rationale: The proxy binds machine-global ports and conflicts with the daemon/router ownership model. Keeping it available behind `--proxy`/`--trust` avoids mixing the old and new public surfaces by default.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Completed on 2026-05-26.

Shipped outcome:

* `scenery dev` with no explicit listen flags registers a session-private Unix API backend at `.scenery/sessions/<session_id>/run/api.sock` when the agent is available.
* Explicit `--listen` and `--port` keep using TCP and register TCP API backends.
* `--proxy`, `--trust`, or `SCENERY_LOCAL_PROXY=1` keep the legacy local HTTPS proxy available and force a hidden loopback TCP app backend because the proxy only supports TCP upstreams.
* The app child receives `SCENERY_LISTEN_NETWORK` and `SCENERY_LISTEN_ADDR`, and the dev supervisor can wait for either TCP or Unix listeners.

Validation:

```sh
go test ./cmd/scenery -run 'Test(DevCommand|ParseDevArgs|PrepareDevAgentSession|Backend|WaitForAppStartup|ScanWatched|ChangedPaths|SnapshotsEqual|ShouldIgnoreWatchPath)'
go test ./cmd/scenery ./internal/agent ./runtime
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

All validation commands passed. The self harness wrote `.scenery/harness/self-latest.json` and reported only existing large-file architecture warnings.

## Context and Orientation

Relevant files:

```text
docs/plans/0037-scenery-agent-mvp.md
cmd/scenery/main.go
cmd/scenery/watch.go
cmd/scenery/dev_supervisor.go
cmd/scenery/console.go
internal/agent/*
runtime/app.go
internal/localproxy/*
```

The 0037 MVP already added `SCENERY_LISTEN_NETWORK=unix` support in `runtime/app.go` and Unix backend routing in `internal/agent/router.go`. This plan wires those primitives into the normal dev supervisor path.

## Milestones

Milestone 1 updates CLI option handling so the dev path can distinguish explicit TCP requests from the no-flag default.

Milestone 2 computes the session backend path after app-root discovery and agent registration, then starts the app with `SCENERY_LISTEN_NETWORK=unix`.

Milestone 3 updates session manifests and console URLs so routed agent URLs are the default visible API/dashboard/removed agent transport surfaces.

Milestone 4 updates tests and validation.

## Plan of Work

Use additive changes to the supervisor so the old TCP path remains obvious and testable. Avoid moving dashboard internals in this plan; the dashboard still runs as a hidden per-session backend while the agent provides the visible route.

## Concrete Steps

1. Add a dev listen request type that records the requested network/address and whether the user explicitly chose TCP.
2. Change `prepareDevAgentSession` so the no-flag default registers a Unix API backend under the session state root.
3. Start the child process with both `SCENERY_LISTEN_NETWORK` and `SCENERY_LISTEN_ADDR`.
4. Teach startup checks to probe Unix sockets as well as TCP addresses.
5. Make `configureDevProcessEnv` leave `SCENERY_LOCAL_PROXY` unset unless `--proxy` or `--trust` is provided.
6. Update tests for CLI flags, agent session registration, and startup probing.
7. Run `gofmt`, focused Go tests, `go test ./cmd/scenery ./internal/agent ./runtime`, `go install ./cmd/scenery`, and broader validation when practical.

## Validation and Acceptance

Expected validation:

```sh
go test ./cmd/scenery ./internal/agent ./runtime
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Observable behavior:

* `scenery dev` with no listen flags registers an API backend with `"network":"unix"` in `.scenery/sessions/<session_id>/manifest.json`.
* The app child receives `SCENERY_LISTEN_NETWORK=unix` and a session-local socket path.
* The printed API URL uses the agent route, not `127.0.0.1:4000`, when the agent is available.
* `scenery dev --port <n>` and `scenery dev --listen <addr>` still use TCP and register a TCP backend.
* The local HTTPS proxy only starts when explicitly requested or enabled by environment.

## Idempotence and Recovery

The runtime already removes stale Unix socket files before binding. Session registration remains an upsert keyed by app root and branch, so repeated dev starts refresh the same session manifest.

If the agent cannot start, `scenery dev` should fall back to the existing TCP behavior and warn rather than blocking local development.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/scenery/main.go
cmd/scenery/watch.go
cmd/scenery/dev_supervisor.go
cmd/scenery/*_test.go
docs/plans/0038-agent-private-dev-backends.md
```

## Interfaces and Dependencies

No new external dependencies.

Expected effective default:

```text
SCENERY_LISTEN_NETWORK=unix
SCENERY_LISTEN_ADDR=<app-root>/.scenery/sessions/<session_id>/run/api.sock
```

Explicit compatibility flags continue to produce TCP backends.
