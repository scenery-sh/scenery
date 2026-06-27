# Tech Debt

This file tracks known project debt that should be visible to agents before they start large edits.

## Open

### Agent Thread Findings - 2026-06-27

- Area: agent workflow
- Severity: low
- Owner: scenery maintainers
- Created: 2026-06-27
- Review after: 2026-07-04

Inspected 2 eligible Codex threads attached to `/Users/petrbrazdil/Repos/scenery` in the previous 24 hours. No eligible thread was missing a local `token_count` record. Only one thread contained implementation work, so these are repeated friction points inside that thread rather than cross-thread trends.

| Thread | Input | Output | Reasoning | Cache Read | Total Tokens | Cost (USD) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Analyze scenery threads daily | 293,826 | 14,334 | 4,126 | 2,494,080 | 2,802,240 | unavailable |
| Implement ExecPlan 0088 | 917,824 | 109,361 | 13,377 | 38,332,928 | 39,360,113 | unavailable |
| **Totals** | **1,211,650** | **123,695** | **17,503** | **40,827,008** | **42,162,353** | unavailable |

1. Repo-local subagent ban conflicts with generic task prompts.
   - Symptom: the user allowed 0-5 subagents for ExecPlan 0088, but the agent had to override that because root `AGENTS.md` forbids subagents in this repo.
   - Evidence needed: thread `019f086a-4db6-7731-945e-ea43ce0224c0` (`Implement ExecPlan 0088`), first agent status: "I’ll use 0 subagents here because the repo’s own `AGENTS.md` forbids them".
   - Next action: keep this rule visible in `AGENTS.md`; agents should cite it immediately when prompts mention subagents.

2. SQLite migration touches more active surfaces than the plan headline suggests.
   - Symptom: the agent repeatedly discovered stale database-driver paths in auth, DB CLI, dev runtime env injection, branch lifecycle, self-harness, drift harness, and tests.
   - Evidence needed: thread `019f086a-4db6-7731-945e-ea43ce0224c0`; affected paths included `auth/*`, `auth/db/*`, `cmd/scenery/db_*`, `cmd/scenery/dev_*`, `cmd/scenery/harness_*`, `db/db.go`, `internal/app/root.go`, `internal/sqlitedb/*`, and `sqlc.yaml`.
   - Next action: before more database migration work, run the migration plan's active-code inventory and triage active-code hits before docs-only hits.

3. Auth sqlc conversion to SQLite is the main schema/type trap.
   - Symptom: blind conversion failed on old schema details; generated code then needed named SQLite args, a tiny UUID scanner/value type, and time/null-time fixes.
   - Evidence needed: thread `019f086a-4db6-7731-945e-ea43ce0224c0`; statuses mention Atlas/COMMENT/namespace details, `auth/db/queries.sql`, `auth/db/gen/schema.sql`, `auth/db/gen/types.go`, `sqlc.yaml`, and generated param/name fixes in standard auth files.
   - Next action: use `sqlc generate` as the source of truth and fix SQL/schema inputs, not generated Go output.

4. Mechanical database-provider renames are risky in branch lifecycle code.
   - Symptom: broad rename created bad identifiers and stale helper names, then compile exposed old branch helper calls in agent cleanup, harness, and tests.
   - Evidence needed: thread `019f086a-4db6-7731-945e-ea43ce0224c0`; statuses mention "mechanical rename was a little too broad", "bad spaces", malformed signatures, and old helper names in `cmd/scenery/db_branch_*`, `cmd/scenery/agent.go`, and `cmd/scenery/harness_*`.
   - Next action: prefer targeted compile-guided edits around provider boundaries, then run `go test ./cmd/scenery` before widening.

### Full Dashboard Parity

- Area: dashboard
- Severity: medium
- Owner: scenery dashboard
- Created: 2026-04-27
- Review after: 2026-05-27

The editable dashboard source exists, but parity should continue to be verified visually for complex pages such as traces, API Explorer, Cron, and DB Explorer.

### Browser Harness Fixture-Backed Mutation Depth

- Area: harness
- Severity: medium
- Owner: scenery runtime
- Created: 2026-06-07
- Review after: 2026-07-07

The browser UI harness now captures route-specific semantic journeys, screenshots, console events, network requests, and DOM snapshots for the core dashboard routes. Remaining debt is deeper fixture-backed mutation coverage for flows such as actually sending API Explorer requests, running DB queries against managed fixtures, clearing traces, and validating docs/help routes when those pages exist.

### Deeper Architecture Checks

- Area: harness
- Severity: low
- Owner: scenery runtime
- Created: 2026-04-27
- Review after: 2026-05-27

The self harness now enforces the first architecture checks: dependency allowlist, forbidden imports, CLI package boundaries, generated-file hygiene, and file-size thresholds. Future work can add deeper package dependency direction rules once the repo structure stabilizes.

### Long Build Tests

- Area: tests
- Severity: low
- Owner: scenery runtime
- Created: 2026-04-27
- Review after: 2026-05-27

Some full `go test ./...` runs still spend most time in build/package tests. Keep these real tests, but continue optimizing the build path rather than gating them away.
