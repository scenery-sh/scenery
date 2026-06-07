# Agent Detached Dev and Attach

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series makes `onlava dev` an agent client and defines an attached/detached lifecycle:

```text
onlava dev              # attached; Ctrl-C stops session
onlava dev --detach     # start session and return
onlava attach           # attach to logs
onlava down             # stop current session
```

The current agent-native local-dev implementation has agent sessions, routed URLs, `onlava status`, `onlava down`, and session-scoped logs, but `onlava dev` is still only an attached foreground process and there is no `onlava attach` command.

After this work, `onlava dev --detach` starts a normal dev supervisor as a background agent-owned session, returns after that session is registered, and prints enough information for tools and humans to inspect or attach. `onlava attach` follows the current session's logs by default and can target a specific session. `onlava down` remains the stopping surface by signalling the detached session owner PID recorded in the agent registry.

## Progress

* [x] 2026-05-27: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-27: Implement `onlava dev --detach` argument parsing, detached child spawning, startup polling, and output.
* [x] 2026-05-27: Make detached child processes survive the launcher returning without disabling normal parent-death cleanup for attached dev sessions.
* [x] 2026-05-27: Add `onlava attach` as the current-contract log attachment command.
* [x] 2026-05-27: Update command usage, local contract docs, README command list, and tests.
* [x] 2026-05-27: Run focused tests and full repository tests.
* [x] 2026-05-27: Run binary install, self harness, and diff checks.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-27: The detached child must not receive the original relative `--app-root` after the parent changes the child working directory. The launcher now rewrites child args to use the discovered absolute app root.

* 2026-05-27: The existing parent monitor is still correct for attached `onlava dev`, but detached children need a narrowly-scoped environment marker so they survive the launcher returning.

## Decision Log

* Decision: Make detached dev require the local agent.
  Rationale: The agent lifecycle is agent-owned. A detached process without an agent session has no stable route, owner PID record, or reliable `down`/`attach` target.
  Date/Author: 2026-05-27 / Codex

* Decision: Implement `onlava attach` on top of the existing session-scoped log store.
  Rationale: The log store already carries app id, session id, pid, stream, output, and JSONL formatting. Reusing it gives a stable machine-readable attachment surface without inventing terminal multiplexing in this milestone.
  Date/Author: 2026-05-27 / Codex

## Outcomes & Retrospective

Completed on 2026-05-27.

Shipped outcome:

* Added `onlava dev --detach`, which requires the local agent, starts a background dev child, redirects supervisor stdout/stderr to an agent log file, waits for that child PID to register as the app root's agent session owner, and returns human or JSON startup information.
* Detached children run the same `onlava dev` supervisor path but skip parent-process monitoring through a private environment marker, so the launcher can exit without stopping the session.
* Added `onlava attach`, which follows the current session's logs by default and supports the same app-root, session, limit, stream, and JSONL options as `onlava logs`.
* Updated CLI usage, README, local contract docs, and tests.

Validation:

```sh
go test ./cmd/onlava ./internal/agent ./internal/devdash
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

All validation commands passed. The self harness wrote `.onlava/harness/self-latest.json` and reported existing review-due and large-file warnings, but no errors.

## Context and Orientation

Relevant files:

```text
docs/plans/0037-onlava-agent-mvp.md
cmd/onlava/main.go
cmd/onlava/watch.go
cmd/onlava/logs.go
cmd/onlava/agent.go
cmd/onlava/process_*.go
internal/agent/*
internal/devdash/*
docs/local-contract.md
README.md
```

Current state:

* `onlava dev` starts an attached foreground watcher/supervisor.
* `runWithWatch` registers an agent session before building and records the `OwnerPID` used by `onlava down`.
* `startParentMonitor` cancels attached dev sessions when their invoking parent process exits.
* `onlava logs --session current --follow` can already follow the current session's process output.

## Milestones

Milestone 1 adds the CLI surface and detached process launcher while preserving the existing attached path.

Milestone 2 teaches the detached child path to skip parent-process monitoring while keeping attached cleanup unchanged.

Milestone 3 adds `onlava attach` as a focused alias over session log following and documents the lifecycle.

Milestone 4 validates process/session behavior with focused tests plus the normal repository gates.

## Plan of Work

Start with the smallest agent-native lifecycle that satisfies agent-native local-dev: detached mode spawns the same `onlava dev` implementation in a background child, with an environment marker that only affects parent monitoring. The parent waits for the child PID to appear as the owner of an agent session for the app root, then returns a concise human or JSON result. Keep logs in the existing dashboard store so `onlava attach` and `onlava logs` share output semantics.

## Concrete Steps

1. Extend `devOptions` and `parseDevArgs` with `--detach`.
2. Add a detached dev launcher that filters `--detach` out of the child args, sets a private child marker env var, redirects child stdio to an agent log file, starts the child with detached process attributes, and waits for an agent session owned by the child PID.
3. Make `runWithWatch` skip `startParentMonitor` only when the detached child marker is present.
4. Add `onlava attach` to the command switch and usage text, defaulting to `--session current --follow`.
5. Add unit tests for parsing, command dispatch, child arg filtering, parent monitor behavior, attach argument translation, and detached startup polling.
6. Update docs and mark the plan complete after validation.

## Validation and Acceptance

Expected validation:

```sh
go test ./cmd/onlava ./internal/agent ./internal/devdash
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

Observable behavior:

* `onlava dev --detach` starts a background session and returns after the agent registry has that child PID as owner.
* `onlava dev` without `--detach` remains attached and Ctrl-C still stops the session.
* A detached child survives the launcher returning.
* `onlava attach` follows the current session's logs by default.
* `onlava down` stops a detached session through the existing agent owner-PID signal path.

## Idempotence and Recovery

If startup polling times out or the child exits before session registration, the launcher should try to interrupt the child and return the detached child log path. A subsequent `onlava dev --detach` can retry normally. `onlava attach` should not mutate session state; it only reads from the log store.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/onlava/main.go
cmd/onlava/watch.go
cmd/onlava/logs.go
cmd/onlava/dev_detach.go
cmd/onlava/*test.go
docs/local-contract.md
README.md
docs/plans/0043-agent-detached-dev-and-attach.md
```

## Interfaces and Dependencies

No new external dependencies expected.

New or clarified CLI:

```text
onlava dev --detach [--app-root <path>] [--json] [other dev flags]
onlava attach [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--jsonl|--json]
```
