# scenery Agent MVP

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series describes moving local development from many public per-worktree ports to one machine-local agent that owns a control socket, a routed ingress, and session state. The immediate goal is a Scenery-native agent MVP that can run today without replacing every existing dev substrate.

After this work, `scenery dev` auto-starts or connects to a local `scenery agent`, registers the current worktree as a session, writes a session manifest under `.scenery/sessions/<session_id>/manifest.json`, and exposes routed session URLs through the agent. The existing app, dashboard, Temporal, Victoria, Grafana, and optional local proxy remain supervised by `scenery dev` for this milestone, but their public identity moves behind agent session records. `scenery status --json` and `scenery down` become agent-backed controls.

This plan deliberately does not move shared Postgres, Temporal, Victoria, or Grafana into daemon-owned substrates. Those are later agent-native local-dev phases. The MVP must keep existing `scenery dev --listen ...` and `--proxy` behavior working while adding the daemon path.

## Progress

* [x] 2026-05-26: Create this ExecPlan as `docs/plans/0037-scenery-agent-mvp.md` and link it from `docs/plans/active.md`.
* [x] 2026-05-26: Implement internal agent control socket, session registry, router, and manifest helpers.
* [x] 2026-05-26: Add `scenery agent`, `scenery status --json`, and `scenery down` commands.
* [x] 2026-05-26: Make `scenery dev` auto-start/register with the agent while preserving existing direct listen and proxy behavior.
* [x] 2026-05-26: Add runtime `SCENERY_LISTEN_NETWORK=unix` support.
* [x] 2026-05-26: Add unit/integration tests and run repository validation.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-26: macOS rejects long Unix socket paths with `bind: invalid argument`. The focused `go test ./internal/agent ./runtime ./cmd/scenery` run exposed this with a temp-directory socket path. `internal/agent.DefaultPaths` now falls back to a hashed socket path under `os.TempDir()` when the default path would exceed a conservative length.

## Decision Log

* Decision: Ship an agent MVP without moving Temporal, Victoria, Grafana, Postgres, or sync into daemon-owned shared substrates.
  Rationale: The agent-native local-dev plan explicitly phases those moves after the daemon/router/session model. Keeping current supervision intact makes the first change testable and preserves current dev workflows.
  Date/Author: 2026-05-26 / Codex

* Decision: Preserve explicit `scenery dev --listen` direct app binding during the MVP.
  Rationale: Existing tests and developer workflows rely on direct loopback addresses. The agent session URL becomes the advertised routed surface, while direct binding remains a compatibility detail until the hidden-port-only default can be safely rolled out.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Completed on 2026-05-26.

Shipped outcome:

* Added `internal/agent`, a standard-library local daemon package with Unix control socket, JSON session registry, host-based HTTP router, session manifest writing, and a Unix-socket aware reverse proxy.
* Added `scenery agent`, `scenery status --json`, and `scenery down` command surfaces.
* `scenery dev` now auto-starts/connects to the agent unless `SCENERY_AGENT_DISABLE=1`, registers the worktree session, writes `.scenery/sessions/<session_id>/manifest.json`, updates running/stopped/compile-error state, and advertises routed API/dashboard/removed agent transport URLs when no explicit local proxy is active.
* Existing direct `scenery dev --listen` and `--proxy` behavior remains intact. Existing integration helpers disable agent startup so they do not leak background daemon processes during tests.
* Runtime servers now support `SCENERY_LISTEN_NETWORK=unix` with TCP remaining the default.

Validation:

```sh
go test ./internal/agent ./runtime ./cmd/scenery
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

All validation commands passed. The self harness wrote `.scenery/harness/self-latest.json` and reported existing architecture warnings for large files, including `cmd/scenery/dev_supervisor.go`, but no errors.

## Context and Orientation

Relevant files:

```text
docs/plans/0037-scenery-agent-mvp.md
cmd/scenery/main.go
cmd/scenery/watch.go
cmd/scenery/dev_supervisor.go
cmd/scenery/dashboard.go
cmd/scenery/serve.go
cmd/scenery/console.go
runtime/app.go
runtime/server.go
internal/devdash/*
internal/localproxy/*
```

Existing `scenery dev` starts the app, dashboard, optional local proxy, Temporal dev server, Victoria stack, and Grafana from `cmd/scenery/dev_supervisor.go`. The dashboard currently binds `internal/devdash.ListenAddr()`, which defaults to `127.0.0.1:9401` but can be overridden with `SCENERY_DEV_DASHBOARD_ADDR`.

The agent MVP adds a new `internal/agent` package. It should use the Go standard library for the control plane and registry. The control API runs over a Unix socket at `~/.scenery/run/agent.sock` by default, with `SCENERY_AGENT_HOME` and explicit command flags available for tests and advanced local setups. The router listens on a loopback TCP address and proxies host-routed requests such as `api.<session>.scenery.localhost`.

## Milestones

Milestone 1 adds the internal agent package: paths, session IDs, registry persistence, manifest writing, Unix-socket HTTP control server, and HTTP router.

Milestone 2 exposes CLI commands. `scenery agent` runs the daemon in the foreground. `scenery status --json` reports sessions from the daemon. `scenery down` stops the current app-root session through the daemon.

Milestone 3 integrates `scenery dev`: ensure the agent is running, register/update the session, write the session manifest, and show agent-routed URLs when available.

Milestone 4 adds Unix listener support to generated app runtimes via `SCENERY_LISTEN_NETWORK=unix` while leaving TCP as the default.

Milestone 5 validates with focused tests, `go test ./...`, `go install ./cmd/scenery`, and `scenery harness self --json --write` if practical.

## Plan of Work

Implement the agent package first with no CLI dependency so it can be unit-tested directly. Then add thin command wrappers and dev-supervisor calls. Keep protocol structs explicit and versioned enough that a future daemon can evolve without scraping terminal output.

Use additive changes and avoid refactoring the dashboard internals in this milestone. The dashboard remains available on its existing hidden/direct address and the agent stores dashboard route metadata. A later phase can make the dashboard itself daemon-owned by moving the dashboard server behind a session-aware provider interface.

## Concrete Steps

1. Add `internal/agent` with:
   * default path resolution;
   * session ID generation from app root, branch, and hash;
   * JSON registry persistence;
   * session manifest writing;
   * Unix-socket HTTP control client/server;
   * host-based reverse proxy for registered backends.
2. Add `cmd/scenery/agent.go` and update `cmd/scenery/main.go` usage/dispatch for `agent`, `status`, and `down`.
3. Update `cmd/scenery/watch.go` and `cmd/scenery/dev_supervisor.go` so dev sessions are registered, updated after app start/reload, and unregistered on close.
4. Update `runtime/app.go` so `SCENERY_LISTEN_NETWORK=unix` creates and serves a Unix listener, removing stale socket files before binding.
5. Add tests for agent session ID/manifest/registry/router/client commands and Unix runtime listener behavior.
6. Run `gofmt`, `go test ./...`, `go install ./cmd/scenery`, `scenery harness self --json --write`, and `git diff --check`.

## Validation and Acceptance

Expected validation:

```sh
go test ./internal/agent ./runtime ./cmd/scenery
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Observable behavior:

* `scenery agent` creates a Unix control socket and serves a router.
* `scenery dev` registers a session with API/dashboard/removed agent transport route metadata and writes `.scenery/sessions/<session_id>/manifest.json`.
* `scenery status --json --app-root <path>` returns the registered session.
* `scenery down --app-root <path>` asks the daemon to stop that session.
* An app runtime can listen on a Unix socket when `SCENERY_LISTEN_NETWORK=unix`.

## Idempotence and Recovery

Agent startup should tolerate a stale socket by probing it first and removing it only when no server answers. Registry writes should be atomic. Session registration should be an upsert keyed by session ID so repeated `scenery dev` runs refresh the same worktree session instead of creating duplicates.

If agent auto-start fails, `scenery dev` should continue with existing direct behavior and warn rather than blocking development. If registry or manifest writes fail, surface a normal command error because those are part of the requested agent behavior.

## Artifacts and Notes

Expected new artifacts:

```text
internal/agent/*
cmd/scenery/agent.go
.scenery/sessions/<session_id>/manifest.json
```

No generated code or external service downloads should be required for the agent package itself.

## Interfaces and Dependencies

New CLI surface:

```sh
scenery agent [--socket <path>] [--router-listen <addr>] [--json]
scenery status --json [--app-root <path>] [--session <id>]
scenery down [--app-root <path>] [--session <id>]
```

New runtime environment:

```text
SCENERY_LISTEN_NETWORK=tcp|unix
SCENERY_LISTEN_ADDR=<host:port or socket path>
```

New agent environment:

```text
SCENERY_AGENT_HOME=<dir>
SCENERY_AGENT_SOCKET=<path>
SCENERY_AGENT_ROUTER_ADDR=<host:port>
SCENERY_AGENT_DISABLE=1
```

The implementation should stay within the Go standard library unless an existing repository dependency is already necessary at the call site.
