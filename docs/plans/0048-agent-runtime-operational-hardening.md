# Agent Runtime Operational Hardening

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The current `main` branch is close to the intended agent-native local-dev local-runtime end state: `scenery dev` defaults to an agent-routed app-root runtime, owner metadata exists for internal runtime records and substrates, frontend routes are runtime-scoped, ONLV has moved to agent-native defaults, and the router preserves public host/proto/port context.

The remaining work is operational hardening. The default path must stay agent-safe even when older environment variables are exported, cleanup must not leave managed databases behind, ordinary agent restarts must not interrupt live shared substrates, the legacy machine-global proxy must be removed from the normal `scenery dev` surface, and `dev.setup` needs a lifecycle policy. Scenery release validation must stay inside the Scenery repo and must not create client-application worktrees.

Release guard policy: keep the guard strict, but stop letting nondeterministic external substrate readiness masquerade as core release safety. Strictness belongs on Scenery-owned invariants, contracts, schemas, fixtures, and release artifacts; external app or host substrate readiness belongs in explicit evidence and diagnostics unless the release is intentionally validating that substrate boundary.

This is not a redesign. The goal is to close the remaining correctness edges in priority order while preserving the current agent model.

This file is the active ExecPlan for the 2026-05-28 source-review findings about what is still missing. Do not create a parallel plan for the same gaps; update this file as the work proceeds.

## Progress

- [x] 2026-05-27: Created this follow-on ExecPlan from a source review of current `main` and the remaining agent-native local-dev operational risks.
- [x] 2026-05-28: Revalidated the missing-work list against source and refreshed this plan as the active source-review ExecPlan. The cleanup command is now `scenery prune`, and the obsolete spelling has no compatibility alias.
- [x] 2026-06-11: Updated the active runtime contract for one live Scenery dev runtime per app root. Git worktrees are the supported way to run multiple live code copies; internal session IDs remain only for routing, state, and observability compatibility.
- [ ] Phase 0: Record the current agent-safe default baseline with tests, install, harness, and live ONLV URL checks.
- [ ] Phase 1.1: Make dev dashboard/log storage agent-owned in agent mode even when `SCENERY_DEV_CACHE_DIR` is exported.
- [ ] Phase 1.2: Add DB-aware prune and prune stale managed Postgres substrate session metadata.
- [ ] Phase 1.3: Make `scenery agent restart` preserve shared substrates by default.
- [ ] Phase 1.4: Remove or hard-block the legacy local proxy from the normal `scenery dev` surface with no backwards-compatibility alias.
- [ ] Phase 2: Add `dev.setup` run policy and update ONLV to use schema-change setup.
- [x] 2026-06-25: Removed the ONLV client-app worktree smoke from Scenery release validation. `scripts/release-gate.sh` no longer creates ONLV worktrees, and the old smoke script was deleted.
- [x] 2026-06-25: Added structured `scenery.dev.failure.v1` evidence artifacts for required managed ZeroFS preflight, toolchain/start, and bounded readiness failures, including phase, session, and substrate context.
- [ ] Phase 3: Keep parallel runtime safety covered by Scenery-owned fixtures and self-harness checks, not client-app worktrees.
- [ ] Phase 4: Consider optional `doctor dev`, browser-profile isolation, and later network sandbox hardening after the default path is stable.
- [ ] Phase 5.1: Add single-instance locks for the edge Caddy and `scenery system agent`, and reap stale binders on owned ports (TCP and UDP 19443, router port) at startup.
- [ ] Phase 5.2: Add a rebrand-migration sweep that detects and stops pre-rebrand `~/.onlava` processes and offers `~/.onlava` state cleanup.
- [ ] Phase 5.3: Teach `scenery doctor` to flag duplicate listeners on scenery-owned ports and orphaned `scenery system agent` processes.
- [x] 2026-06-12: Hardened shared substrate and Postgres branch locks with bounded nonblocking acquisition, named wait diagnostics, real Windows file locking, short `branches.lock` registry sections, and a separate parent-database operation lock for branch DDL.

## Surprises & Discoveries

- 2026-05-27: Agent home is decoupled from `SCENERY_DEV_CACHE_DIR`, but `cmd/scenery/devdash_store.go` still checks `SCENERY_DEV_CACHE_DIR` before the active agent. `cmd/scenery/watch.go` only forces the agent dashboard store when `SCENERY_DEV_CACHE_DIR` is empty, so a globally exported old cache dir can still split logs/traces/dashboard state. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-27: `cmd/scenery/agent.go` implements `scenery prune --older-than` without `--db`, `--state`, or `--all`. The current command deletes stale runtime records and state roots, but it does not drop managed runtime Postgres databases or prune `session.<id>` substrate metadata. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-28: The stale-session cleanup command is now `scenery prune` with no compatibility alias. The obsolete spelling is intentionally removed.
- 2026-05-27: `internal/agent/server.go` verifies substrate owners before signaling, but `Server.Close()` still walks registered substrates and interrupts verified component PIDs. An ordinary `scenery agent restart` can therefore disrupt live shared Postgres, Electric, Temporal, Victoria, or Grafana substrates used by running app runtimes. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-27: `cmd/scenery/main.go` still lets `scenery dev --proxy` enable the legacy local proxy path after printing a warning. The underlying `internal/localproxy` defaults remain machine-global ports `80` and `443`, so warning-only behavior is still a footgun for parallel worktrees. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-27: `cmd/scenery/dev_supervisor.go` runs all `dev.setup` commands inside every `RebuildAndRestart` after compile and before app start. This is fine for fast idempotent scripts but will become expensive once setup includes migrations, seed data, imports, or codegen. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-27: `cmd/scenery/harness_parallel.go` contains a self-harness parallel session check. Earlier versions of this plan proposed a high-signal ONLV client-app smoke, but Scenery release validation should not create or mutate ONLV worktrees; app-specific validation belongs in the client app.
- 2026-06-11: Explicit session-selection flags conflict with the current product rule. Parallel live development should be expressed as multiple Git worktrees, not multiple user-named runtimes from one app directory.
- 2026-06-12: A pre-rebrand `~/.onlava` edge Caddy (started 2026-06-08) ran for four days racing the current `~/.scenery` edge on TCP and UDP 127.0.0.1:19443 via SO_REUSEPORT, and three orphaned `scenery system agent` processes pointed `--router-listen` at an already-owned port. Nothing in `scenery doctor` or `scenery ps` surfaced either condition; both were found via `lsof -nP -iTCP:19443 -sTCP:LISTEN` and `lsof -nP -iUDP:19443` while debugging an unrelated SSE incident. Duplicate UDP binders are a live HTTP/3 hazard because QUIC flows hash across both processes.
- 2026-06-12: Narrowing the Postgres branch registry lock exposed the real contended resource: branch database DDL races on the parent template database can produce `pq: tuple concurrently updated (XX000)`. The registry lock should stay metadata-only; branch create/reset/drop now serialize with a parent-database operation lock.
- 2026-06-12: A 30-second file-lock timeout was too short for legitimate cold shared substrate startup under parallel runtime validation. Grafana cold startup can keep another session waiting long enough to trip a 30-second timeout even though the system is healthy, so lock waits now warn early and periodically while allowing a two-minute bounded wait.
- 2026-06-25: ZeroFS preflight failures happen before a real dev-session state root exists, so their structured evidence belongs under the app root's `.scenery/evidence/` directory with `session.status: "not_created"`. Readiness failures happen after process startup and can write under the active session state root's `artifacts/` directory.
- 2026-06-25: Strict release gates are most useful when they separate core Scenery release safety from host or client-app substrate variance. Flaky external readiness should produce structured evidence with phase/session/substrate context, not a disguised verdict that the Scenery release itself is unsafe.

## Decision Log

- Decision: Keep this as a follow-on hardening plan instead of reopening the agent-native local-dev architecture.
- Decision: One app root can have at most one live Scenery dev runtime. Internal `session_id` values remain compatibility labels for routes, state directories, logs, traces, metrics, manifests, and cleanup, but they are no longer a user-managed workflow.
  Rationale: The current implementation has reached the right shape. The remaining risks are operational edges around storage ownership, destructive cleanup, restart semantics, legacy escape hatches, setup frequency, and release gating.
  Date/Author: 2026-05-27 / Codex.
- Decision: Prioritize the `SCENERY_DEV_CACHE_DIR` dashboard split first.
  Rationale: This can silently hide logs/traces/dashboard data for developers with legacy shell environments, while the fix is narrow and lowers confusion during all later validation.
  Date/Author: 2026-05-27 / Codex.
- Decision: Default prune remains non-destructive for databases.
  Rationale: Dropping databases must require an explicit `--db` or `--all` flag. Registry cleanup can be safe by default, but managed DB deletion is destructive.
  Date/Author: 2026-05-27 / Codex.
- Decision: The stale-session cleanup command is `scenery prune`, and no legacy alias is kept.
  Rationale: `prune` is the clearer user-facing operation. Carrying deprecated command aliases conflicts with the project rule to keep one current public surface.
  Date/Author: 2026-05-28 / Codex.
- Decision: Do not add backwards-compatibility shims for removed or renamed local runtime APIs.
  Rationale: The repository now has an explicit no-legacy/no-backcompat rule in `AGENTS.md`. For this plan, that means old command spellings and legacy proxy flags should fail clearly or be removed rather than kept as deprecated aliases.
  Date/Author: 2026-05-28 / Codex.
- Decision: Ordinary `agent restart` should restart only the router/control plane.
  Rationale: Shared substrates are session-continuity infrastructure. Restarting the control plane should not stop live app dependencies unless the caller opts into `--substrates` or uses `agent stop --all`.
  Date/Author: 2026-05-27 / Codex.
- Decision: Keep `branches.lock` scoped to short branch registry reads and writes, and use a separate parent-database operation lock for managed Postgres branch DDL.
  Rationale: Holding the registry lock through substrate startup or database cloning hides useful diagnostics and blocks unrelated metadata readers. The parent template database is the actual shared Postgres resource that needs serialization during `CREATE DATABASE ... WITH TEMPLATE`, reset, and drop.
  Date/Author: 2026-06-12 / Codex.
- Decision: Use bounded nonblocking lock acquisition with early and periodic diagnostics instead of silent blocking flock waits.
  Rationale: Release-gate and multi-worktree runs need a clear named lock path when contention is real, while legitimate cold substrate startup needs more than the original 30-second wait budget.
  Date/Author: 2026-06-12 / Codex.
- Decision: Do not run ONLV client-app worktree smoke from Scenery release validation.
  Rationale: Scenery's release gate must validate Scenery-owned behavior. Creating two ONLV worktrees from inside the Scenery repo couples a core release guard to mutable client-app state and local substrate readiness.
  Date/Author: 2026-06-25 / Codex.
- Decision: Keep the release guard strict while classifying nondeterministic external substrate readiness as diagnostic evidence, not core release safety.
  Rationale: A strict guard should block regressions in Scenery-owned contracts, schemas, release artifacts, fixture runtimes, routing isolation, and managed-substrate semantics. External host/client readiness can still be checked when explicitly requested, but it must report which phase/session/substrate failed and should not masquerade as a failure of the core Scenery release surface.
  Date/Author: 2026-06-25 / Codex.
- Decision: Managed dev substrate failures should write structured failure evidence instead of relying only on terminal text.
  Rationale: The failure artifact answers which phase failed, whether a session existed, and which substrate component, process, socket, log, and config were involved. This is especially important for required ZeroFS preflight and bounded readiness failures that are intermittent under release validation.
  Date/Author: 2026-06-25 / Codex.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

The scenery repo is `/Users/petrbrazdil/Repos/scenery`. The primary real target app for validation is `/Users/petrbrazdil/Repos/onlv`.

Relevant implementation files:

- `cmd/scenery/watch.go` owns `scenery dev`, `prepareDevAgentSession`, session startup, and the current environment override behavior.
- `cmd/scenery/devdash_store.go` owns dashboard/log/trace store root selection through `openDevdashStore()` and `devdashCacheRoot()`.
- `cmd/scenery/agent.go` owns `scenery agent`, `scenery agent restart`, `scenery down`, and `scenery prune` argument parsing and command behavior.
- `internal/agent/server.go` owns the agent control/router server lifecycle and currently signals verified substrate component processes from `Server.Close()`.
- `cmd/scenery/dev_services.go` owns managed Postgres and Electric substrate setup and the runtime database metadata that must be pruned.
- `cmd/scenery/dev_supervisor.go` owns `RebuildAndRestart` and the current unconditional `dev.setup` execution.
- `internal/app/root.go` and `docs/schemas/scenery.config.v1.schema.json` define `.scenery.json` config shape, including the current `dev.setup` string list.
- `cmd/scenery/harness_parallel.go` contains the existing self-harness parallel worktree runtime check.
- `docs/local-contract.md` and `docs/environment.md` document local runtime behavior and environment variables.

Terms used in this plan:

- Agent mode means the default local-dev path where `scenery dev` ensures the local scenery agent, registers an app-root runtime, and routes public URLs through the agent router.
- Dev dashboard store means the SQLite-backed local dashboard/log/trace store opened by `openDevdashStore()`.
- Substrate means an agent-managed shared dependency such as Postgres, Electric, Temporal, Victoria, or Grafana.
- Managed runtime database means the per-runtime Postgres database recorded in substrate metadata as `session.<id>`.
- Legacy local proxy means the older local HTTPS proxy enabled through `--proxy`, `--trust`, or `SCENERY_LOCAL_PROXY`, with machine-global HTTP/HTTPS ports.

## Milestones

Milestone 0 locks the current improvement by recording the agent-safe baseline. Run the normal repo checks and a live ONLV smoke, then capture the observed routed URLs and lack of fixed-port requirements.

Milestone 1 fixes correctness edges in the runtime: dashboard store ownership, DB-aware prune, non-destructive agent restart, and removal or hard-blocking of the legacy proxy surface.

Milestone 2 improves setup lifecycle so target apps can choose whether setup runs once per session, on schema changes, always, or manually.

Milestone 3 keeps parallel runtime validation in Scenery-owned fixtures and self-harness checks. Client-app worktree smokes are explicitly outside the Scenery release gate.

Milestone 4 contains optional hardening after stability: `scenery doctor dev --json`, browser-profile isolation, and later network sandboxing.

## Plan of Work

Start with storage ownership because split logs and traces make every later check harder to interpret. Add `SCENERY_DEVDASH_CACHE_DIR` as the explicit dashboard/log store override, then make agent mode prefer `<agent-dir>/dashboard` before the old build cache env. Keep `SCENERY_DEV_CACHE_DIR` for build/cache compatibility and legacy agent-disabled fallback.

Next make cleanup complete but explicit. `prune` should default to stale registry cleanup only. `--state` removes stale session state directories, `--db` drops managed stale session databases, and `--all` combines registry, state, and DB cleanup. The same metadata pruning should happen when a session is deleted through `down --all` or equivalent session cleanup.

Then split agent control-plane restart from substrate shutdown. Add command options so normal `agent restart` leaves substrates alone, `agent restart --substrates` restarts owned substrates too, and `agent stop --all` stops both agent and owned substrates. Keep owner verification before any signal.

After that, replace warning-only legacy proxy behavior with removal or a hard block. Do not keep `--proxy`, `--trust`, or `SCENERY_LOCAL_PROXY` as deprecated compatibility paths for normal `scenery dev`. If the machine-global proxy is still needed for tests, move it behind an internal test helper or a separately designed command rather than carrying old flags.

Finally add setup policies and keep parallel runtime proof in the Scenery repo. `dev.setup` entries should support object form with `run` and `when`; legacy string entries need a deliberate compatibility decision. ONLV should use `schema-change` for `./scripts/db-safe-apply.sh`, but Scenery release validation must not create ONLV worktrees.

## Concrete Steps

1. Phase 0 baseline:
   - Run from `/Users/petrbrazdil/Repos/scenery`: `go test ./...` and `scenery harness self --json --write`. Do not run `go install ./cmd/scenery` during agent validation unless a human explicitly asks.
   - Run from `/Users/petrbrazdil/Repos/onlv`: `just dev`, `just urls`, and `just psql`.
   - Record in this plan whether the default ONLV URLs are agent-routed runtime URLs for API, `pulse`, `blog`, Electric, and console, and confirm the default path does not require fixed `4000`, `4321`, `5173`, `5433`, `3000`, `9401`, `8428`, `9428`, `10428`, or `10429`.
2. Dev dashboard store ownership:
   - Change `devdashCacheRoot()` semantics to use `SCENERY_DEVDASH_CACHE_DIR` first, then `<agent-dir>/dashboard` when the agent is active, then `SCENERY_DEV_CACHE_DIR`, then the existing legacy user-cache fallback.
   - Stop setting `SCENERY_DEV_CACHE_DIR` to the dashboard path in `prepareDevAgentSession`; if an override is needed for child process store selection, use `SCENERY_DEVDASH_CACHE_DIR`.
   - Update `docs/environment.md`, `docs/local-contract.md`, and tests in `cmd/scenery/watch_test.go`, `cmd/scenery/dashboard_state_test.go`, `cmd/scenery/logs_test.go`, and related observability tests.
3. DB-aware prune:
   - Extend `pruneOptions` and `parsePruneArgs` with `--db`, `--state`, and `--all`.
   - Keep default `prune --older-than` as stale registry cleanup only.
   - Add managed Postgres cleanup helpers that find stale `session.<id>` substrate metadata, drop the session database, and prune the metadata entry.
   - Reuse the same cleanup from `scenery down --db`, `scenery down --all`, and session deletion paths where practical.
   - Add unit tests for argument parsing, non-destructive defaults, metadata pruning, and database-drop command construction. Add integration coverage when Docker/Postgres is available.
4. Agent restart semantics:
   - Add flags for `agent restart --substrates` and `agent stop --all`, updating `parseAgentArgs` or introducing command-specific option parsing if clearer.
   - Change ordinary `agent restart` so it stops only the old agent/router/control plane. It must not call a shutdown path that signals substrates.
   - Keep owner verification for any explicit substrate stop/restart.
   - Add tests proving ordinary restart preserves registered substrate PIDs and explicit substrate restart signals only verified owners.
5. Legacy proxy removal or hard block:
   - Remove `--proxy`, `--trust`, and `SCENERY_LOCAL_PROXY` from the normal `scenery dev` path, or make them fail with a short actionable error that points to the agent router.
   - Do not add acknowledgement aliases, compatibility aliases, or deprecated spellings.
   - If a machine-global proxy remains necessary for tests, keep it out of the public `scenery dev` CLI and document the internal test-only entrypoint.
   - Update `cmd/scenery/run_json_test.go`, `docs/local-contract.md`, and `docs/environment.md`.
6. Setup lifecycle policy:
   - Change `internal/app.DevConfig.Setup` to support structured entries, for example `{ "run": "./scripts/db-safe-apply.sh", "when": "schema-change" }`.
   - Support `initial`, `schema-change`, `always`, and `manual`; expose manual setup through `scenery dev setup`.
   - Decide and document legacy string behavior. Prefer interpreting strings as `initial` if this is acceptable; otherwise keep strings as `always` for compatibility and state the migration path.
   - Detect schema changes using changed paths and configured/default migration patterns. Start with conservative file suffix/path matching such as `.sql`, migrations directories, Atlas files, and app-configured patterns if needed.
   - Update `docs/schemas/scenery.config.v1.schema.json`, `docs/local-contract.md`, and ONLV `.scenery.json`.
7. Parallel runtime validation:
   - Keep this coverage inside `scenery harness self --json --write` and Scenery-owned fixture apps.
   - Do not create or mutate ONLV worktrees from the Scenery repo.
   - Make Scenery-owned parallel checks fail if fixed global ports, shared managed databases, shared Temporal task queues, mixed logs/traces, or cross-session teardown leaks appear.

## Validation and Acceptance

Default repo validation after each substantial slice:

```text
go test ./...
scenery harness self --json --write
```

Dev dashboard storage acceptance:

```text
SCENERY_DEV_CACHE_DIR=/tmp/scenery-old-cache scenery dev --app-root .
scenery logs --app-root .
scenery ps --json --app-root .
```

All three commands must read the same agent-visible runtime data in agent mode.

DB cleanup acceptance:

```text
scenery dev --app-root .
scenery down --all --app-root .
scenery ps --json
```

The Postgres substrate metadata must no longer contain `session.<id>` URL/endpoint entries, and the managed database for that runtime must no longer exist. `scenery prune --older-than 14d --db` and `scenery prune --older-than 14d --all` must have equivalent DB cleanup behavior for eligible stale runtime records.

Agent restart acceptance:

```text
scenery dev --app-root <onlv-a>
scenery agent restart
curl <api-session-url>
scenery db psql --app-root <onlv-a>
```

The API and database should continue to work or recover automatically. Shared substrates must not be signaled during ordinary restart.

Legacy proxy acceptance:

```text
scenery dev --proxy
```

This fails with a short actionable error. The default recommendation remains the agent router, and there is no deprecated alias or backwards-compatible fallback for the old machine-global proxy path.

Setup policy acceptance:

- Editing a Go file restarts the app without re-running ONLV DB setup when setup policy is `schema-change`.
- Editing migration/schema files runs setup before app restart.
- `scenery dev setup` runs `manual` setup entries on demand and reports failures clearly.

Parallel runtime acceptance:

- Scenery-owned parallel validation proves separate internal `session_id` values, separate `runtime_app_id` values, isolated API backends, runtime-scoped routes, different managed DB names, different Temporal task queues, runtime-scoped logs/traces, and no teardown bleed.
- The validation fails if any fixed global port or shared DB/task queue leaks back into the default path.
- Release-gate failures must distinguish Scenery-owned invariant regressions from external substrate readiness. When the failed condition is host/client substrate readiness, the failure output must point at structured evidence instead of relying on terminal scrollback or implying that the release artifact is intrinsically unsafe.

## Idempotence and Recovery

All commands must be safe to retry. If dashboard store selection changes while an old agent is running, the next agent-mode `scenery dev`, `scenery logs`, and `scenery status` should converge on the agent dashboard store without deleting the old cache directory.

DB-aware cleanup must handle partial failure. If database drop succeeds but metadata pruning fails, rerunning the same cleanup should prune metadata without treating the missing database as fatal. If metadata pruning succeeds but database drop fails, rerunning with `--db` or `--all` must retry the drop.

Agent restart must preserve sessions and shared substrate metadata by default. Explicit substrate shutdown should verify owners before signaling and should skip ambiguous or mismatched owners rather than risking wrong-process termination.

Any Scenery-owned parallel smoke must clean up after success and failure. Use traps so `scenery down --all --app-root <fixture-root>` and temporary directory cleanup run even when an assertion fails.

## Artifacts and Notes

Expected artifacts include:

```text
docs/plans/0048-agent-runtime-operational-hardening.md
docs/plans/active.md
docs/environment.md
docs/local-contract.md
docs/schemas/scenery.config.v1.schema.json
scripts/release-gate.sh
.scenery/harness/self-latest.json
```

The concrete ONLV config update belongs in `/Users/petrbrazdil/Repos/onlv/.scenery.json` once the setup policy schema exists. ONLV should set:

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

- `SCENERY_DEVDASH_CACHE_DIR`: explicit override for dashboard/log/trace SQLite storage.
- `scenery prune --older-than <duration> --db`
- `scenery prune --older-than <duration> --state`
- `scenery prune --older-than <duration> --all`
- `scenery agent restart --substrates`
- `scenery agent stop --all`
- Removal or hard-blocking of `scenery dev --proxy`, `scenery dev --trust`, and `SCENERY_LOCAL_PROXY` from normal `scenery dev`.
- `.scenery.json` `dev.setup` object entries with `run` and `when`.
- `scenery dev setup` for manual setup entries.

Keep existing defaults stable: normal `scenery dev` should use the agent router, normal `scenery agent restart` should not stop shared substrates, and normal `scenery prune --older-than` should not drop databases.
