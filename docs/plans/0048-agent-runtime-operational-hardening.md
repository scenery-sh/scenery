# Agent Runtime Operational Hardening

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The current `main` branch is close to the intended agent-native local-dev local-runtime end state: `scenery dev` defaults to an agent-routed app-root runtime, owner metadata exists for internal runtime records and substrates, frontend routes are runtime-scoped, ONLV has moved to agent-native defaults, and the router preserves public host/proto/port context.

The remaining work is operational hardening. Cleanup must not leave managed databases or stale substrate leases behind, ordinary agent restarts must not interrupt live shared substrates, the legacy machine-global proxy must stay removed from the normal `scenery up` surface, and pre-rebrand processes/state need an explicit safe cleanup path. Scenery release validation must stay inside the Scenery repo and must not create client-application worktrees.

Release guard policy: keep the guard strict, but stop letting nondeterministic external substrate readiness masquerade as core release safety. Strictness belongs on Scenery-owned invariants, contracts, schemas, fixtures, and release artifacts; external app or host substrate readiness belongs in explicit evidence and diagnostics unless the release is intentionally validating that substrate boundary.

This is not a redesign. The goal is to close the remaining correctness edges in priority order while preserving the current agent model.

This file is the active ExecPlan for the 2026-05-28 source-review findings about what is still missing. Do not create a parallel plan for the same gaps; update this file as the work proceeds.

## Progress

- [x] 2026-05-27: Created this follow-on ExecPlan from a source review of current `main` and the remaining agent-native local-dev operational risks.
- [x] 2026-05-28: Revalidated the missing-work list against source and refreshed this plan as the active source-review ExecPlan. The cleanup command is now `scenery prune`, and the obsolete spelling has no compatibility alias.
- [x] 2026-06-11: Updated the active runtime contract for one live Scenery dev runtime per app root. Git worktrees are the supported way to run multiple live code copies; internal session IDs remain only for routing, state, and observability compatibility.
- [x] 2026-07-22 Phase 0: Cached `go test ./...`, the worktree-local self-harness
  second pass, explicit CLI installation/version proof, and live routed checks
  completed. `https://local.clean.tech/next/` and
  `https://micro.scenery.sh/platform/` returned 200 while doctor reported no
  duplicate owners or required host errors.
- [x] 2026-07-22 Phase 1.1 disposition: Retained the current documented explicit `SCENERY_DEV_CACHE_DIR` cache/store override. The earlier requirement to ignore it and force agent-owned storage is superseded.
- [x] 2026-07-22 Phase 1.2: Made prune non-destructive by default, added explicit `--state`, `--db`, and `--all` scopes, reused managed-database refusal of external DSNs, and made session deletion atomically remove matching shared-substrate leases.
- [x] Phase 1.3: Make `scenery agent restart` preserve shared substrates by default.
- [x] Phase 1.4: Remove or hard-block the legacy local proxy from the normal `scenery dev` surface with no backwards-compatibility alias.
- [x] 2026-07-07: Separate source grep and focused tests covered old CLI/env proxy paths: `scenery up --proxy` and `scenery up --trust` are unknown flags, and `SCENERY_LOCAL_PROXY` is absent from production `cmd/scenery` and `internal` source. App-config `proxy` removal did not close this phase by itself.
- [x] 2026-07-22 Phase 2 disposition: `dev.setup` has been removed from the current product contract. No lifecycle policy, object form, manual subcommand, or compatibility path is reintroduced.
- [x] 2026-06-25: Removed the ONLV client-app worktree smoke from Scenery release validation. `scripts/release-gate.sh` no longer creates ONLV worktrees, and the old smoke script was deleted.
- [x] 2026-06-25: Added structured `scenery.dev.failure.v1` evidence artifacts for required managed ZeroFS preflight, toolchain/start, and bounded readiness failures, including phase, session, and substrate context.
- [x] 2026-07-22 Phase 3: Existing Scenery-owned parallel runtime fixture/self-harness proof covers the retained parallel-runtime contract; client-app worktrees remain outside release validation.
- [x] 2026-07-22 Phase 4 disposition: Optional `doctor dev`, browser-profile isolation, and network sandboxing are deferred outside this plan.
- [x] 2026-07-14 Phase 5.1: Added lifetime single-instance locks for the local agent and Unix Caddy edge, fail-closed router binding, serialized edge operations, and fingerprint-verified same-user stale-owner reaping for current and pre-rebrand Caddy configuration paths.
- [x] 2026-07-22 Phase 5.2: Added `scenery system agent cleanup [--remove-state]`, which stops only fingerprint-verified same-user processes tied to exact pre-rebrand managed paths, reports retained `~/.onlava` state by default, and removes it only on the explicit flag.
- [x] 2026-07-14 Phase 5.3: `scenery doctor` now reports duplicate local-agent or managed-Caddy owners and foreign TCP/UDP listeners on Scenery-owned ports.
- [x] 2026-07-22: Cached focused tests passed for prune flags/defaults, external-DSN refusal, atomic substrate-lease deletion, pre-rebrand path matching/state opt-in, and checked CLI payload schema revisions; `go test ./internal/agent` passed. A broad cached `go test ./cmd/scenery` was attempted but an unrelated concurrently edited compiler fixture test (`TestContractCheckJSONReportsValidNativeImplementation`) failed with `contract compilation failed`. Phase 0 still owns a clean broad run, self-harness, and live routed-runtime evidence.
- [x] 2026-06-12: Hardened shared substrate and Postgres branch locks with bounded nonblocking acquisition, named wait diagnostics, real Windows file locking, short `branches.lock` registry sections, and a separate parent-database operation lock for branch DDL.
- [x] 2026-07-13: Made running `scenery up` supervisors self-heal the shared Victoria stack after a component or agent-driven shutdown, using owner verification, the existing substrate locks/registry, and bounded retry backoff.
- [x] 2026-07-13: Made each failed Victoria recovery attempt visible without Victoria through a red foreground warning, detached JSONL event, dashboard notification, and best-effort degraded registry state.
- [x] 2026-07-13: Made generic agent close/restart control-plane-only by removing registered-substrate signaling and the obsolete restart wait. A process-backed regression test proves registered Postgres and Victoria PIDs plus an app route survive registry reload under a replacement agent.

## Surprises & Discoveries

- 2026-07-22: Current Postgres substrate metadata is lease-based (`substrate.leases[session_id]`), not the older `session.<id>` endpoint shape described by the original plan. Removing the matching lease inside the registry's owner-checked session deletion makes cleanup atomic and preserves the shared substrate plus unrelated leases.
- 2026-07-22: `SCENERY_DEV_CACHE_DIR` remains a documented, intentional override for local build/dashboard cache selection. Treating its presence as stale shell contamination would break explicit test and operator isolation, so the earlier agent-owned-regardless requirement is superseded.

- 2026-07-12: Current contract note: `dev.setup` was removed rather than extended with lifecycle policy. Database initialization uses `database.apply`, seeds, and `scenery db setup`; app-local operational tasks are declared in `.scn`.
- 2026-07-13: Victoria's exit monitor recorded `degraded` state but never returned control to the shared ensure path, so a healthy app supervisor could outlive all three observability processes indefinitely. The recovery path must serialize exit writes with replacement registration so late exits from the old generation cannot overwrite the new stack.

- 2026-05-27: Agent home is decoupled from `SCENERY_DEV_CACHE_DIR`, but `cmd/scenery/devdash_store.go` still checks `SCENERY_DEV_CACHE_DIR` before the active agent. `cmd/scenery/watch.go` only forces the agent dashboard store when `SCENERY_DEV_CACHE_DIR` is empty, so a globally exported old cache dir can still split logs/traces/dashboard state. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-27: `cmd/scenery/agent.go` implements `scenery prune --older-than` without `--db`, `--state`, or `--all`. The current command deletes stale runtime records and state roots, but it does not drop managed runtime Postgres databases or prune `session.<id>` substrate metadata. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-28: The stale-session cleanup command is now `scenery prune` with no compatibility alias. The obsolete spelling is intentionally removed.
- 2026-05-27: `internal/agent/server.go` verifies substrate owners before signaling, but `Server.Close()` still walks registered substrates and interrupts verified component PIDs. An ordinary `scenery agent restart` can therefore disrupt live shared Postgres, legacy async runtime, Victoria, or Grafana substrates used by running app runtimes. Source review on 2026-05-28 confirmed this is still open.
- 2026-05-27: `cmd/scenery/main.go` still lets `scenery dev --proxy` enable the legacy local proxy path after printing a warning. The underlying `internal/localproxy` defaults remain machine-global ports `80` and `443`, so warning-only behavior is still a footgun for parallel worktrees. Source review on 2026-05-28 confirmed this is still open.
- 2026-07-07: The current local runtime command is `scenery up`. Source grep found no `SCENERY_LOCAL_PROXY` in production `cmd/scenery` or `internal` source, and focused tests now guard that `--proxy` and `--trust` stay rejected by the dev parser. `--trust` still exists only on `scenery system agent`, where it controls direct router TLS trust instead of the removed legacy dev proxy.
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
- Decision: Supersede the earlier dashboard-split requirement and retain the current documented `SCENERY_DEV_CACHE_DIR` override.
  Rationale: The variable is an intentional explicit cache/store override used for isolation. Current behavior is singular and documented; silently ignoring it in agent mode would make the override misleading.
  Date/Author: 2026-07-22 / Petr + Codex.
- Decision: Default prune remains non-destructive for databases.
  Rationale: Dropping databases must require an explicit `--db` or `--all` flag. Registry cleanup can be safe by default, but managed DB deletion is destructive.
  Date/Author: 2026-05-27 / Codex.
- Decision: Default prune is also non-destructive for session state; registry records and matching substrate leases are the default cleanup scope.
  Rationale: `--state`, `--db`, and `--all` make every filesystem or database deletion explicit, while atomic lease removal prevents stale shared-substrate metadata without stopping the substrate.
  Date/Author: 2026-07-22 / Petr + Codex.
- Decision: Remove Phase 2 rather than reintroducing `dev.setup` or a compatibility spelling.
  Rationale: The current database lifecycle is expressed through `database.apply`, seeds, `scenery db setup`, and declared tasks. A removed config contract should not return as a parallel setup system.
  Date/Author: 2026-07-22 / Petr + Codex.
- Decision: Defer Phase 4 optional doctor/browser/network sandbox ideas outside this plan.
  Rationale: They are optional follow-on hardening, not retained acceptance gaps for the current runtime contract.
  Date/Author: 2026-07-22 / Petr + Codex.
- Decision: The stale-session cleanup command is `scenery prune`, and no legacy alias is kept.
  Rationale: `prune` is the clearer user-facing operation. Carrying deprecated command aliases conflicts with the project rule to keep one current public surface.
  Date/Author: 2026-05-28 / Codex.
- Decision: Do not add backwards-compatibility shims for removed or renamed local runtime APIs.
  Rationale: The repository now has an explicit no-legacy/no-backcompat rule in `AGENTS.md`. For this plan, that means old command spellings and legacy proxy flags should fail clearly or be removed rather than kept as deprecated aliases.
  Date/Author: 2026-05-28 / Codex.
- Decision: Ordinary `agent restart` should restart only the router/control plane.
  Rationale: Shared substrates are session-continuity infrastructure. Restarting the control plane must not stop live app dependencies; destructive shutdown stays with existing substrate-specific commands and verified lifecycle owners instead of a generic agent-wide flag.
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
- Decision: Recover Victoria through its existing shared ensure path and replace all three components as one stack.
  Rationale: The existing ownership fingerprints, substrate locks, and singular registry key already provide the safety and deduplication boundary; a generic substrate recovery framework or consumer retry would add surface without fixing lifecycle ownership.
  Date/Author: 2026-07-13 / Codex.
- Decision: Report Victoria recovery failures through terminal, detached-supervisor, and dashboard paths that do not depend on Victoria.
  Rationale: An observability outage cannot safely use the unavailable observability backend as its only alert path; the existing process-output channel provides immediate visibility while registry degradation preserves machine-readable state when the agent is reachable.
  Date/Author: 2026-07-13 / Codex.

## Outcomes & Retrospective

The retained implementation gaps are closed: prune has explicit destructive
scopes with a non-destructive default, session deletion atomically removes
matching substrate leases, and pre-rebrand process/state cleanup is explicit
and fingerprint-verified. The old dashboard override requirement and removed
`dev.setup` proposal were retired instead of adding parallel contracts;
optional Phase 4 hardening was deferred outside this plan. Cached full tests,
the worktree-local self-harness, installed CLI identity, and simultaneous live
ONLV/Micro routes complete the baseline evidence.

## Context and Orientation

The scenery repo is `/Users/petrbrazdil/Repos/scenery`. The primary real target app for validation is `/Users/petrbrazdil/Repos/onlv`.

Relevant implementation files:

- `cmd/scenery/watch.go` owns `scenery up`, session startup, and the current environment override behavior.
- `cmd/scenery/devdash_store.go` owns dashboard/log/trace store root selection through `openDevdashStore()` and `devdashCacheRoot()`.
- `cmd/scenery/agent.go` owns `scenery system agent`, `scenery down`, and `scenery prune` argument parsing and command behavior; `cmd/scenery/agent_cleanup.go` owns the explicit pre-rebrand sweep.
- `internal/agent/server.go` owns the agent control/router server lifecycle and preserves registered substrate processes on close.
- `cmd/scenery/dev_services_postgres.go` and `cmd/scenery/db_cli.go` own managed Postgres resolution and safe drop behavior.
- `internal/agent/registry.go` owns atomic session and matching substrate-lease deletion.
- `cmd/scenery/harness_parallel.go` contains the existing self-harness parallel worktree runtime check.
- `docs/local-contract.md` and `docs/environment.md` document local runtime behavior and environment variables.

Terms used in this plan:

- Agent mode means the default local-dev path where `scenery up` ensures the local scenery agent, registers an app-root runtime, and routes public URLs through the agent router.
- Dev dashboard store means the SQLite-backed local dashboard/log/trace store opened by `openDevdashStore()`.
- Substrate means an agent-managed shared dependency such as Postgres, legacy async runtime, Victoria, or Grafana.
- Managed runtime database means the deterministic per-app-root/worktree Postgres database resolved by Scenery; the shared Postgres substrate separately records runtime leases keyed by session ID.
- Legacy local proxy means the older local HTTPS proxy enabled through `--proxy`, `--trust`, or `SCENERY_LOCAL_PROXY`, with machine-global HTTP/HTTPS ports.

## Milestones

Milestone 0 locks the current improvement by recording the agent-safe baseline. Run the normal repo checks and a live ONLV smoke, then capture the observed routed URLs and lack of fixed-port requirements.

Milestone 1 fixes correctness edges in the runtime: dashboard store ownership, DB-aware prune, non-destructive agent restart, and removal or hard-blocking of the legacy proxy surface.

Milestone 2 is removed: `dev.setup` is no longer part of the current product contract.

Milestone 3 keeps parallel runtime validation in Scenery-owned fixtures and self-harness checks. Client-app worktree smokes are explicitly outside the Scenery release gate.

Milestone 4 is deferred outside this plan: optional `scenery doctor dev --json`, browser-profile isolation, and later network sandboxing are not acceptance requirements here.

## Plan of Work

The original storage-ownership change is superseded. Retain the current documented explicit `SCENERY_DEV_CACHE_DIR` override; do not add a second dashboard-cache environment variable.

Next make cleanup complete but explicit. `prune` should default to stale registry cleanup only. `--state` removes stale session state directories, `--db` drops managed stale session databases, and `--all` combines registry, state, and DB cleanup. The same metadata pruning should happen when a session is deleted through `down --all` or equivalent session cleanup.

Agent control-plane restart remains separate from substrate shutdown. Normal restart leaves substrates alone; destructive operations stay with substrate-specific commands and verified lifecycle owners.

After that, replace warning-only legacy proxy behavior with removal or a hard block. Do not keep `--proxy`, `--trust`, or `SCENERY_LOCAL_PROXY` as deprecated compatibility paths for normal `scenery dev`. If the machine-global proxy is still needed for tests, move it behind an internal test helper or a separately designed command rather than carrying old flags.

Do not reintroduce setup policies: `dev.setup` was removed. Keep the already-implemented parallel runtime proof in Scenery-owned fixtures and self-harness checks; Scenery release validation must not create ONLV worktrees.

## Concrete Steps

1. Phase 0 baseline:
   - Run from `/Users/petrbrazdil/Repos/scenery`: `go test ./...` and `scenery harness self --json --write`. Do not run `go install ./cmd/scenery` during agent validation unless a human explicitly asks.
   - Run from `/Users/petrbrazdil/Repos/onlv`: `just dev`, `just urls`, and `just psql`.
   - Record in this plan whether the default ONLV URLs are agent-routed runtime URLs for API, `pulse`, `blog`, and console, and confirm the default path does not require fixed `4000`, `4321`, `5173`, `5433`, `3000`, `9401`, `8428`, `9428`, `10428`, or `10429`.
2. Dev dashboard store ownership (superseded):
   - Retain the current documented `SCENERY_DEV_CACHE_DIR` override and do not add `SCENERY_DEVDASH_CACHE_DIR`.
3. DB-aware prune:
   - Extend `pruneOptions` and `parsePruneArgs` with `--db`, `--state`, and `--all`.
   - Keep default `prune --older-than` as stale registry cleanup only.
   - Resolve eligible managed databases through the existing database API, and remove matching lease metadata atomically with the session record.
   - Reuse the same cleanup from `scenery down --db`, `scenery down --all`, and session deletion paths where practical.
   - Add unit tests for argument parsing, non-destructive defaults, metadata pruning, and database-drop command construction. Add integration coverage when Docker/Postgres is available.
4. Agent restart semantics:
   - Keep generic agent close/restart limited to the old agent/router/control plane; it must never signal registered substrate processes.
   - Keep destructive shutdown in existing substrate-specific commands and verified lifecycle recovery paths.
   - Test that registered Postgres and Victoria PIDs and an app route survive agent shutdown, registry reload, and replacement startup.
5. Legacy proxy removal or hard block:
   - Remove `--proxy`, `--trust`, and `SCENERY_LOCAL_PROXY` from the normal `scenery dev` path, or make them fail with a short actionable error that points to the agent router.
   - Do not add acknowledgement aliases, compatibility aliases, or deprecated spellings.
   - If a machine-global proxy remains necessary for tests, keep it out of the public `scenery dev` CLI and document the internal test-only entrypoint.
   - Update `cmd/scenery/run_json_test.go`, `docs/local-contract.md`, and `docs/environment.md`.
6. Setup lifecycle policy (removed):
   - Do not reintroduce `dev.setup`, structured setup entries, compatibility parsing, or `scenery dev setup`.
7. Parallel runtime validation:
   - Keep this coverage inside `scenery harness self --json --write` and Scenery-owned fixture apps.
   - Do not create or mutate ONLV worktrees from the Scenery repo.
   - Make Scenery-owned parallel checks fail if fixed global ports, shared managed databases, shared legacy async runtime task queues, mixed logs/traces, or cross-session teardown leaks appear.

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

Setup policy acceptance is removed because `dev.setup` is no longer a current contract.

Parallel runtime acceptance:

- Scenery-owned parallel validation proves separate internal `session_id` values, separate `runtime_app_id` values, isolated API backends, runtime-scoped routes, different managed DB names, different legacy async runtime task queues, runtime-scoped logs/traces, and no teardown bleed.
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
docs/local-contract.md
docs/agent-guide.md
README.md
docs/schemas/scenery.prune.schema.json
docs/schemas/scenery.agent.cleanup.schema.json
```

## Interfaces and Dependencies

No new external Go dependencies should be added for these changes unless a concrete need appears. Prefer the standard library and existing internal packages.

New or changed public/local interfaces:

- `scenery prune --older-than <duration> --db`
- `scenery prune --older-than <duration> --state`
- `scenery prune --older-than <duration> --all`
- `scenery system agent cleanup [--remove-state]`
- Removal or hard-blocking of `scenery dev --proxy`, `scenery dev --trust`, and `SCENERY_LOCAL_PROXY` from normal `scenery dev`.

Keep existing defaults stable: normal `scenery up` uses the agent router, normal `scenery system agent restart` does not stop shared substrates, and normal `scenery prune --older-than` deletes neither databases nor session state.
