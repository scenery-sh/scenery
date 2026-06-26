# Victoria Shared Substrate Visibility

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Scenery should not add a new OS-level Victoria service just to avoid starting VictoriaMetrics, VictoriaLogs, and VictoriaTraces per dev session. With the local agent active, the current implementation already gives the important efficiency win: one shared Victoria substrate is started under the agent state root, registered in the agent substrate registry, verified for owner/reachability, and reused by later `scenery up` sessions.

This plan keeps that model and makes it harder to misunderstand. The desired end state is that developers and agents can tell, from docs and CLI output, that Victoria is shared per active agent state root rather than per session, while the no-agent fallback remains simple per-app-root sidecars. The plan intentionally avoids LaunchAgent/systemd/user-service infrastructure, new background service managers, or a separate `scenery system victoria` command unless later measurements show startup cost remains a real problem after agent-backed reuse is visible and reliable.

## Progress

- [x] 2026-06-26: Drafted this plan to settle the machine-service question and keep the next implementation slice small.
- [x] 2026-06-26: Added this plan to `docs/plans/active.md` and `docs/knowledge.json`.
- [x] 2026-06-26: Refreshed the Victoria/Grafana docs so the shared-agent behavior is explicit and review-due metadata is current.
- [x] 2026-06-26: Added the smallest useful runtime visibility improvements for Victoria substrate reuse.
- [x] 2026-06-26: Validated docs metadata, focused Go tests, full Go tests, and diff hygiene.

## Surprises & Discoveries

- 2026-06-26: The current agent-backed path already uses `managedSubstrateManager.Ensure(ctx, filepath.Join(paths.AgentDir, "victoria"), victoriaSubstrateAdapter)` and marks the resulting Victoria stack external after registry upsert, so session shutdown should not kill the shared stack.
- 2026-06-26: `scenery ps --json` already includes the agent `substrates` list. The missing piece is mainly human and documentation visibility, not a new persistence model.
- 2026-06-26: The no-agent fallback has a partial opportunistic reuse behavior because default Victoria ports are treated as external when already occupied, but that path lacks the agent registry, owner fingerprint verification, and degraded-state reporting. Do not promote that fallback into the primary sharing contract.
- 2026-06-26: `scenery ps` text only printed app roots before this slice, so the additive `Shared substrates` block is the smallest human-visible parity with the existing JSON payload.

## Decision Log

- Decision: Keep the current shared-agent substrate as the default answer for efficient Victoria reuse; do not add an OS-level machine daemon or service in this plan.
  Rationale: The active agent already gives one shared per-user/per-machine substrate without new platform-specific service management, multi-user trust boundaries, install/uninstall flows, or global version/port conflicts.
  Date/Author: 2026-06-26 / OpenAI assistant

- Decision: Preserve the no-agent path as per-app-root local sidecars with opportunistic external-port reuse.
  Rationale: The no-agent mode is valuable as a small fallback and test/debug path. Making it machine-global would recreate a weaker agent registry without ownership checks.
  Date/Author: 2026-06-26 / OpenAI assistant

- Decision: Improve docs and existing CLI visibility before inventing any new Victoria service command.
  Rationale: The reported confusion is about whether the efficient path exists. `scenery ps --json` and the substrate registry already expose the raw data; a small documentation/visibility slice is cheaper and safer than broad service-management infrastructure.
  Date/Author: 2026-06-26 / OpenAI assistant

## Outcomes & Retrospective

Completed on 2026-06-26. Contributors can answer the Victoria lifecycle question from the repo alone: with an active agent, Victoria is shared through the agent substrate registry; without the agent, sidecars remain app-root local; no OS machine service is introduced. `scenery up -v --json` reports `victoria.shared` with `mode` and `reused`, and human `scenery ps` now includes an additive `Shared substrates` section while preserving the existing JSON status payload.

## Context and Orientation

Start with these files:

- `cmd/scenery/dev_supervisor.go` for `devSupervisor.Start`, `startVictoriaStack`, and the agent-backed `Ensure` call.
- `cmd/scenery/victoria.go` for Victoria component defaults, environment export, registry serialization, external/reuse behavior, and substrate adapter methods.
- `cmd/scenery/dev_substrate_manager.go` for shared substrate locking, reusable-substrate checks, registry upsert, `MarkExternal`, and component-exit monitoring.
- `cmd/scenery/agent.go` for `scenery ps` and the existing `--json` status payload that includes `substrates`.
- `internal/agent/types.go` for `Substrate`, `SubstrateExit`, and substrate kind constants.
- `docs/local-contract.md` for the current local observability contract.
- `docs/grafana.md` for the adjacent shared-agent Grafana contract and review-due wording.
- `docs/plans/0003-victoria-observability-sidecars.md` for the completed historical sidecar plan.
- `docs/plans/active.md` and `docs/knowledge.json` for active plan indexing.

Current behavior to preserve:

- Without an agent, `scenery up` starts Victoria under `.scenery/victoria/` by default, unless `SCENERY_DEV_VICTORIA=0` disables it or explicit env vars redirect binaries/ports/state.
- With an agent, `scenery up` uses the agent directory as the Victoria root and registers the stack as substrate kind `victoria`.
- Reuse requires a ready substrate, a verified owner fingerprint, and reachable metrics/logs/traces endpoints.
- Component exits mark the substrate degraded and emit structured dev events with exit metadata.
- App processes receive `SCENERY_SESSION_ID`, `SCENERY_BASE_APP_ID`, `SCENERY_RUNTIME_APP_ID`, and Victoria OTLP endpoint env vars, so shared Victoria storage can still be queried with app/session scope.

## Milestones

1. Decision and docs alignment: this plan is active, the local contract states the shared-agent lifecycle in unambiguous terms, and the Grafana doc is brought back into review.
2. Visibility hardening: verbose `scenery up` and `scenery ps` make Victoria reuse/ownership visible through existing surfaces without adding a new service manager.
3. Validation and measurement hook: tests cover the changed output or event fields, and the plan records whether further startup-time measurement is needed before revisiting OS-level services.

## Plan of Work

Treat the active local agent as Scenery's machine-local control plane. Do not add LaunchAgent, systemd, Windows service, cron, login-item, or daemon-install code. The agent-backed Victoria substrate is already the efficient path; this work should make that obvious and robust.

First, update documentation and plan indexing. Then make one small runtime visibility change: surface whether a Victoria stack was newly prepared or reused when `managedSubstrateManager.Ensure` returns. Prefer adding fields to existing verbose/dev events over adding a new command. If maintainers want human `scenery ps` parity, add a compact shared-substrates section to text output while preserving the existing JSON payload shape.

Only consider a follow-up plan for OS-level bootstrap if measured startup cost remains high in no-agent workflows after this change, and if the measurement names a real user flow that cannot simply run `scenery system agent`.

## Concrete Steps

1. Save this file as `docs/plans/0079-victoria-shared-substrate-visibility.md`.
2. Add plan 0079 to `docs/plans/active.md` without renumbering existing IDs. Place it near the agent/runtime hardening plans because it affects shared runtime substrate behavior.
3. Add a `docs/knowledge.json` document entry for this plan with status `active`, owner `scenery runtime / agent DX`, quality `B`, freshness `current`, `last_reviewed` `2026-06-26`, `review_after` `2026-07-26`, and tags `plans`, `execplans`, `victoria`, `observability`, `agents`.
4. Update `docs/local-contract.md` local observability wording to say explicitly: active-agent Victoria is shared per agent state root, effectively per user/machine where that agent runs; it is not an OS-level service and is not started once for all users or all possible agent homes.
5. Refresh `docs/grafana.md` because it is review-due and should use parallel wording for shared agent substrate state versus no-agent local state.
6. In `cmd/scenery/dev_supervisor.go`, keep the existing `Ensure` call but stop discarding the `reused` boolean. Include `reused: true|false` and `mode: "shared-agent"` in the existing verbose `victoria.shared` console event or structured dev event fields. Do not change the public OTLP endpoint env names.
7. Inspect `cmd/scenery/agent.go`. Because `scenery ps --json` already includes `substrates`, leave that schema alone. If text output still gives no human clue that Victoria is shared, add a small `Shared substrates` section that prints kind, status, owner PID, component PIDs, and primary URLs for non-empty substrate lists.
8. Add or adjust focused tests in `cmd/scenery` for any changed event/text rendering. Prefer dependency-injected or synthetic substrate data; do not require real Victoria binaries in unit tests.
9. Update this plan's `Progress`, `Surprises & Discoveries`, and `Decision Log` as implementation details settle.

## Validation and Acceptance

Acceptance criteria:

- `docs/plans/active.md` links `0079-victoria-shared-substrate-visibility.md` without renumbering any historical plan IDs.
- `docs/knowledge.json` indexes plan 0079 as an active ExecPlan.
- `docs/local-contract.md` clearly states the lifecycle distinction: no-agent Victoria is app-root local; active-agent Victoria is shared per agent state root; no OS-level Victoria service is part of the contract.
- `docs/grafana.md` is refreshed so Grafana and Victoria shared-substrate wording does not drift.
- `scenery up -v` or the structured dev event stream distinguishes a newly prepared shared Victoria substrate from a reused one.
- Existing app/runtime env names for Victoria endpoints remain unchanged.
- `scenery ps --json` continues to include `substrates`; any text output addition is additive and human-only.
- No new service-management infrastructure, install/uninstall command, LaunchAgent, systemd unit, Windows service, or compatibility alias is introduced.

Validation commands from the repository root:

```sh
jq empty docs/knowledge.json
scenery inspect docs --json
go test ./cmd/scenery
go test ./internal/agent
git diff --check
```

Do not run `go install ./cmd/scenery` unless a human explicitly asks to update the shared installed binary.

## Idempotence and Recovery

These changes should be safe to resume. Documentation and JSON edits can be reapplied directly; rerun `jq empty docs/knowledge.json` after every JSON edit. Runtime visibility changes should be additive and should not change Victoria state paths, ports, endpoint environment variables, or substrate kind names.

If a half-finished code change makes `scenery up` fail before app startup, revert the event/text-output change first and keep the docs/plan decision. The current shared-agent substrate behavior is already implemented; the code work is visibility, not a prerequisite for correctness.

If active-plan indexing fails, do not renumber IDs. Fix the 0079 entries in `docs/plans/active.md` and `docs/knowledge.json`, then rerun docs inspection.

## Artifacts and Notes

Expected runtime artifacts are unchanged and must not be committed:

- no-agent Victoria state under `.scenery/victoria/`
- agent-backed Victoria state under the resolved agent directory, typically under an `agent/victoria` subtree
- managed substrate stdout/stderr logs under the substrate log paths recorded in the agent registry
- optional harness output under `.scenery/harness/` when validation is run with `--write`

Suggested `docs/plans/active.md` entry:

```md
- [0079 Victoria Shared Substrate Visibility](0079-victoria-shared-substrate-visibility.md)
  - Status: active
  - Owner: scenery runtime / agent DX
  - Created: 2026-06-26
  - Focus: keep Victoria efficient through the existing shared-agent substrate, make reuse/ownership visible, and avoid OS-level service infrastructure unless measurements later justify it.
```

Suggested `docs/knowledge.json` document entry:

```json
{
  "path": "docs/plans/0079-victoria-shared-substrate-visibility.md",
  "title": "Victoria Shared Substrate Visibility",
  "owner": "scenery runtime / agent DX",
  "status": "active",
  "quality": "B",
  "freshness": "current",
  "last_reviewed": "2026-06-26",
  "review_after": "2026-07-26",
  "summary": "Active ExecPlan for keeping Victoria efficient through the existing shared-agent substrate, clarifying lifecycle docs, surfacing reuse/ownership, and avoiding OS-level service management unless measured startup cost later justifies it.",
  "tags": [
    "plans",
    "execplans",
    "victoria",
    "observability",
    "agents"
  ]
}
```

## Interfaces and Dependencies

This plan depends on existing Scenery interfaces only:

- `localagent.SubstrateVictoria` and `scenery.dev.substrate.v1`
- `managedSubstrateManager.Ensure` and `Monitor`
- `victoriaSubstrateAdapter`
- existing Victoria endpoint environment variables
- existing `scenery ps --json` status shape

Do not add third-party dependencies. Use the Go standard library and existing Scenery patterns. Do not add a new stable service-management API in this plan.
