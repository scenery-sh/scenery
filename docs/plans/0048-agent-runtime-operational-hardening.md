# Agent Runtime Operational Hardening

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The current `main` branch is close to the intended PRD-5 local-runtime end state: `onlava dev` defaults to agent-routed sessions, owner metadata exists for sessions and substrates, frontend routes are session-scoped, ONLV has moved to agent-native defaults, and the router preserves public host/proto/port context.

The remaining work is operational hardening. The default path must stay agent-safe even when older environment variables are exported, cleanup must not leave managed databases behind, ordinary agent restarts must not interrupt live shared substrates, the legacy machine-global proxy must require an explicit compatibility opt-in, `dev.setup` needs a lifecycle policy, and the real two-worktree ONLV smoke must be an executable release gate.

This is not a redesign. The goal is to close the remaining correctness edges in priority order while preserving the current agent model.

## Progress

- [x] 2026-05-27: Created this follow-on ExecPlan from a source review of current `main` and the remaining PRD-5 operational risks.
- [ ] Phase 0: Record the current agent-safe default baseline with tests, install, harness, and live ONLV URL checks.
- [ ] Phase 1.1: Make dev dashboard/log storage agent-owned in agent mode even when `ONLAVA_DEV_CACHE_DIR` is exported.
- [ ] Phase 1.2: Add DB-aware GC and prune stale managed Postgres substrate session metadata.
- [ ] Phase 1.3: Make `onlava agent restart` preserve shared substrates by default.
- [ ] Phase 1.4: Hard-block legacy local proxy mode unless explicitly acknowledged.
- [ ] Phase 2: Add `dev.setup` run policy and update ONLV to use schema-change setup.
- [ ] Phase 3: Add and wire a real two-worktree ONLV parallel smoke gate.
- [ ] Phase 4: Consider optional `doctor dev`, browser-profile isolation, and later network sandbox hardening after the default path is stable.

## Surprises & Discoveries

- 2026-05-27: Agent home is decoupled from `ONLAVA_DEV_CACHE_DIR`, but `cmd/onlava/devdash_store.go` still checks `ONLAVA_DEV_CACHE_DIR` before the active agent. `cmd/onlava/watch.go` only forces the agent dashboard store when `ONLAVA_DEV_CACHE_DIR` is empty, so a globally exported old cache dir can still split logs/traces/dashboard state.
- 2026-05-27: `cmd/onlava/agent.go` implements `onlava gc --older-than` without `--db`, `--state`, or `--all`. The current command deletes stale sessions and state roots, but it does not drop managed per-session Postgres databases or prune `session.<id>` substrate metadata.
- 2026-05-27: `internal/agent/server.go` verifies substrate owners before signaling, but `Server.Close()` still walks registered substrates and interrupts verified component PIDs. An ordinary `onlava agent restart` can therefore disrupt live shared Postgres, Electric, Temporal, Victoria, or Grafana substrates used by running app sessions.
- 2026-05-27: `cmd/onlava/main.go` still lets `onlava dev --proxy` enable the legacy local proxy path after printing a warning. The underlying `internal/localproxy` defaults remain machine-global ports `80` and `443`, so warning-only behavior is still a footgun for parallel worktrees.
- 2026-05-27: `cmd/onlava/dev_supervisor.go` runs all `dev.setup` commands inside every `RebuildAndRestart` after compile and before app start. This is fine for fast idempotent scripts but will become expensive once setup includes migrations, seed data, imports, or codegen.
- 2026-05-27: `cmd/onlava/harness_parallel.go` contains a self-harness parallel session check, but this plan still requires a high-signal ONLV two-worktree smoke script that starts the real target app with managed Postgres, Electric, frontend, Temporal, logs, traces, and teardown as an executable release gate.

## Decision Log

- Decision: Keep this as a follow-on hardening plan instead of reopening the PRD-5 architecture.
  Rationale: The current implementation has reached the right shape. The remaining risks are operational edges around storage ownership, destructive cleanup, restart semantics, legacy escape hatches, setup frequency, and release gating.
  Date/Author: 2026-05-27 / Codex.
- Decision: Prioritize the `ONLAVA_DEV_CACHE_DIR` dashboard split first.
  Rationale: This can silently hide logs/traces/dashboard data for developers with legacy shell environments, while the fix is narrow and lowers confusion during all later validation.
  Date/Author: 2026-05-27 / Codex.
- Decision: Default GC remains non-destructive for databases.
  Rationale: Dropping databases must require an explicit `--db` or `--all` flag. Registry cleanup can be safe by default, but managed DB deletion is destructive.
  Date/Author: 2026-05-27 / Codex.
- Decision: Ordinary `agent restart` should restart only the router/control plane.
  Rationale: Shared substrates are session-continuity infrastructure. Restarting the control plane should not stop live app dependencies unless the caller opts into `--substrates` or uses `agent stop --all`.
  Date/Author: 2026-05-27 / Codex.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

The onlava repo is `/Users/petrbrazdil/Repos/onlava`. The primary real target app for validation is `/Users/petrbrazdil/Repos/onlv`.

Relevant implementation files:

- `cmd/onlava/watch.go` owns `onlava dev`, `prepareDevAgentSession`, session startup, and the current environment override behavior.
- `cmd/onlava/devdash_store.go` owns dashboard/log/trace store root selection through `openDevdashStore()` and `devdashCacheRoot()`.
- `cmd/onlava/agent.go` owns `onlava agent`, `onlava agent restart`, `onlava down`, and `onlava gc` argument parsing and command behavior.
- `internal/agent/server.go` owns the agent control/router server lifecycle and currently signals verified substrate component processes from `Server.Close()`.
- `cmd/onlava/dev_services.go` owns managed Postgres and Electric substrate setup and the per-session database metadata that must be pruned.
- `cmd/onlava/dev_supervisor.go` owns `RebuildAndRestart` and the current unconditional `dev.setup` execution.
- `internal/app/root.go` and `docs/schemas/onlava.config.v1.schema.json` define `.onlava.json` config shape, including the current `dev.setup` string list.
- `cmd/onlava/harness_parallel.go` contains the existing self-harness parallel dev-session check.
- `docs/local-contract.md` and `docs/environment.md` document local runtime behavior and environment variables.

Terms used in this plan:

- Agent mode means the default local-dev path where `onlava dev` ensures the local onlava agent, registers a session, and routes public URLs through the agent router.
- Dev dashboard store means the SQLite-backed local dashboard/log/trace store opened by `openDevdashStore()`.
- Substrate means an agent-managed shared dependency such as Postgres, Electric, Temporal, Victoria, or Grafana.
- Managed session database means the per-session Postgres database recorded in substrate metadata as `session.<id>`.
- Legacy local proxy means the older local HTTPS proxy enabled through `--proxy`, `--trust`, or `ONLAVA_LOCAL_PROXY`, with machine-global HTTP/HTTPS ports.

## Milestones

Milestone 0 locks the current improvement by recording the agent-safe baseline. Run the normal repo checks and a live ONLV smoke, then capture the observed routed URLs and lack of fixed-port requirements.

Milestone 1 fixes correctness edges in the runtime: dashboard store ownership, DB-aware GC, non-destructive agent restart, and a stronger opt-in for the legacy proxy.

Milestone 2 improves setup lifecycle so target apps can choose whether setup runs once per session, on schema changes, always, or manually.

Milestone 3 adds the real ONLV two-worktree release gate and wires it into a required validation path.

Milestone 4 contains optional hardening after stability: `onlava doctor dev --json`, browser-profile isolation, and later network sandboxing.

## Plan of Work

Start with storage ownership because split logs and traces make every later check harder to interpret. Add `ONLAVA_DEVDASH_CACHE_DIR` as the explicit dashboard/log store override, then make agent mode prefer `<agent-dir>/dashboard` before the old build cache env. Keep `ONLAVA_DEV_CACHE_DIR` for build/cache compatibility and legacy agent-disabled fallback.

Next make cleanup complete but explicit. `gc` should default to stale registry cleanup only. `--state` removes stale session state directories, `--db` drops managed stale session databases, and `--all` combines registry, state, and DB cleanup. The same metadata pruning should happen when a session is deleted through `down --all` or equivalent session cleanup.

Then split agent control-plane restart from substrate shutdown. Add command options so normal `agent restart` leaves substrates alone, `agent restart --substrates` restarts owned substrates too, and `agent stop --all` stops both agent and owned substrates. Keep owner verification before any signal.

After that, replace warning-only legacy proxy behavior with a hard block when the agent is available unless the user passes an explicit acknowledgement flag such as `--i-know-this-is-global`, or rename the surface to an explicit `--legacy-proxy`. Update docs and tests so the default recommendation is the agent router.

Finally add setup policies and the ONLV release gate. `dev.setup` entries should support object form with `run` and `when`; legacy string entries need a deliberate compatibility decision. ONLV should use `schema-change` for `./scripts/db-safe-apply.sh`. The two-worktree smoke should start real ONLV worktrees with the current onlava binary and fail on fixed global ports, shared DB names, shared task queues, mixed logs/traces, or teardown bleed.

## Concrete Steps

1. Phase 0 baseline:
   - Run from `/Users/petrbrazdil/Repos/onlava`: `go test ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`.
   - Run from `/Users/petrbrazdil/Repos/onlv`: `just dev`, `just urls`, and `just psql`.
   - Record in this plan whether the default ONLV URLs are agent-routed session URLs for API, `pulse`, `blog`, Electric, and console, and confirm the default path does not require fixed `4000`, `4321`, `5173`, `5433`, `3000`, `9401`, `8428`, `9428`, `10428`, or `10429`.
2. Dev dashboard store ownership:
   - Change `devdashCacheRoot()` semantics to use `ONLAVA_DEVDASH_CACHE_DIR` first, then `<agent-dir>/dashboard` when the agent is active, then `ONLAVA_DEV_CACHE_DIR`, then the existing legacy user-cache fallback.
   - Stop setting `ONLAVA_DEV_CACHE_DIR` to the dashboard path in `prepareDevAgentSession`; if an override is needed for child process store selection, use `ONLAVA_DEVDASH_CACHE_DIR`.
   - Update `docs/environment.md`, `docs/local-contract.md`, and tests in `cmd/onlava/watch_test.go`, `cmd/onlava/dashboard_state_test.go`, `cmd/onlava/logs_test.go`, and related observability tests.
3. DB-aware GC:
   - Extend `gcOptions` and `parseGCArgs` with `--db`, `--state`, and `--all`.
   - Keep default `gc --older-than` as stale registry cleanup only.
   - Add managed Postgres cleanup helpers that find stale `session.<id>` substrate metadata, drop the session database, and prune the metadata entry.
   - Reuse the same cleanup from `onlava down --db`, `onlava down --all`, and session deletion paths where practical.
   - Add unit tests for argument parsing, non-destructive defaults, metadata pruning, and database-drop command construction. Add integration coverage when Docker/Postgres is available.
4. Agent restart semantics:
   - Add flags for `agent restart --substrates` and `agent stop --all`, updating `parseAgentArgs` or introducing command-specific option parsing if clearer.
   - Change ordinary `agent restart` so it stops only the old agent/router/control plane. It must not call a shutdown path that signals substrates.
   - Keep owner verification for any explicit substrate stop/restart.
   - Add tests proving ordinary restart preserves registered substrate PIDs and explicit substrate restart signals only verified owners.
5. Legacy proxy hard block:
   - Add a required acknowledgement for `onlava dev --proxy` and `--trust` when the agent is available, or replace the public flag with `--legacy-proxy` while preserving a clear compatibility alias if needed.
   - Make `onlava dev --proxy` fail with a short actionable error in agent mode unless the acknowledgement is present.
   - Keep agent-disabled fallback usable for compatibility testing.
   - Update `cmd/onlava/run_json_test.go`, `docs/local-contract.md`, and `docs/environment.md`.
6. Setup lifecycle policy:
   - Change `internal/app.DevConfig.Setup` to support structured entries, for example `{ "run": "./scripts/db-safe-apply.sh", "when": "schema-change" }`.
   - Support `initial`, `schema-change`, `always`, and `manual`; expose manual setup through `onlava dev setup`.
   - Decide and document legacy string behavior. Prefer interpreting strings as `initial` if this is acceptable; otherwise keep strings as `always` for compatibility and state the migration path.
   - Detect schema changes using changed paths and configured/default migration patterns. Start with conservative file suffix/path matching such as `.sql`, migrations directories, Atlas files, and app-configured patterns if needed.
   - Update `docs/schemas/onlava.config.v1.schema.json`, `docs/local-contract.md`, and ONLV `.onlava.json`.
7. Two-worktree ONLV release gate:
   - Add `scripts/dev-parallel-smoke.sh`.
   - The script creates two temporary ONLV worktrees, starts both with `ONLAVA_BIN=<path-to-current-onlava>`, waits for sessions, asserts isolated routes/resources, downs both sessions with `onlava down --all`, and removes the worktrees.
   - Wire the script into `onlava harness self --json --write` or a top-level `scripts/release-gate.sh`.
   - Make the script fail if any fixed global port, shared managed database, shared Temporal task queue, shared frontend/Electric route, mixed logs/traces, or cross-session teardown leak appears.

## Validation and Acceptance

Default repo validation after each substantial slice:

```text
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```

Dev dashboard storage acceptance:

```text
ONLAVA_DEV_CACHE_DIR=/tmp/onlava-old-cache onlava dev --app-root .
onlava logs --session current --app-root .
onlava status --json --app-root .
```

All three commands must read the same agent-visible session data in agent mode.

DB cleanup acceptance:

```text
onlava dev --new-session
onlava down --all --session <id>
onlava status --json
```

The Postgres substrate metadata must no longer contain `session.<id>` URL/endpoint entries, and the managed database for that session must no longer exist. `onlava gc --older-than 14d --db` and `onlava gc --older-than 14d --all` must have equivalent DB cleanup behavior for eligible stale sessions.

Agent restart acceptance:

```text
onlava dev --app-root <onlv-a>
onlava agent restart
curl <api-session-url>
onlava db psql --app-root <onlv-a>
```

The API and database should continue to work or recover automatically. Shared substrates must not be signaled during ordinary restart.

Legacy proxy acceptance:

```text
onlava dev --proxy
```

When the agent is available, this fails with a short actionable error explaining the explicit legacy/global opt-in. The default recommendation remains the agent router.

Setup policy acceptance:

- Editing a Go file restarts the app without re-running ONLV DB setup when setup policy is `schema-change`.
- Editing migration/schema files runs setup before app restart.
- `onlava dev setup` runs `manual` setup entries on demand and reports failures clearly.

Two-worktree gate acceptance:

- The gate proves two ONLV worktrees have different `session_id`, different `runtime_app_id`, Unix-socket API backends, session-scoped frontend/Electric/Grafana/Temporal routes, different managed DB names, different Temporal task queues, session-scoped logs/traces, and no teardown bleed from session A to session B.
- The gate fails if any fixed global port or shared DB/task queue leaks back into the default path.

## Idempotence and Recovery

All commands must be safe to retry. If dashboard store selection changes while an old agent is running, the next agent-mode `onlava dev`, `onlava logs`, and `onlava status` should converge on the agent dashboard store without deleting the old cache directory.

DB-aware cleanup must handle partial failure. If database drop succeeds but metadata pruning fails, rerunning the same cleanup should prune metadata without treating the missing database as fatal. If metadata pruning succeeds but database drop fails, rerunning with `--db` or `--all` must retry the drop.

Agent restart must preserve sessions and shared substrate metadata by default. Explicit substrate shutdown should verify owners before signaling and should skip ambiguous or mismatched owners rather than risking wrong-process termination.

The two-worktree smoke script must clean up after success and failure. Use traps so `onlava down --all --app-root <worktree>` and `git worktree remove` run even when an assertion fails.

## Artifacts and Notes

Expected artifacts include:

```text
docs/plans/0048-agent-runtime-operational-hardening.md
docs/plans/active.md
docs/environment.md
docs/local-contract.md
docs/schemas/onlava.config.v1.schema.json
scripts/dev-parallel-smoke.sh
scripts/release-gate.sh
.onlava/harness/self-latest.json
```

The concrete ONLV config update belongs in `/Users/petrbrazdil/Repos/onlv/.onlava.json` once the setup policy schema exists. ONLV should set:

```json
{
  "dev": {
    "setup": [
      {
        "run": "./scripts/db-safe-apply.sh",
        "when": "schema-change"
      }
    ]
  }
}
```

## Interfaces and Dependencies

No new external Go dependencies should be added for these changes unless a concrete need appears. Prefer the standard library and existing internal packages.

New or changed public/local interfaces:

- `ONLAVA_DEVDASH_CACHE_DIR`: explicit override for dashboard/log/trace SQLite storage.
- `onlava gc --older-than <duration> --db`
- `onlava gc --older-than <duration> --state`
- `onlava gc --older-than <duration> --all`
- `onlava agent restart --substrates`
- `onlava agent stop --all`
- `onlava dev --proxy --i-know-this-is-global` or equivalent explicit legacy-proxy opt-in.
- `.onlava.json` `dev.setup` object entries with `run` and `when`.
- `onlava dev setup` for manual setup entries.

Keep existing defaults stable: normal `onlava dev` should use the agent router, normal `onlava agent restart` should not stop shared substrates, and normal `onlava gc --older-than` should not drop databases.
