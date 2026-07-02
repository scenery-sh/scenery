# Tech Debt

This file tracks known project debt that should be visible to agents before they start large edits.

## Open

### Agent Thread Findings - 2026-07-02

Inspected 1 eligible Codex thread attached to `/Users/petrbrazdil/Repos/scenery` in the previous 24 hours. No eligible thread was missing a local `token_count` record; the active digest row is an edit-time snapshot. Only 4 real recurring automation/process issues were present.

| Thread | Input | Output | Reasoning | Cache Read | Total Tokens | Cost (USD) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Agent thread debt digest (July 2) | 183,740 | 4,074 | 1,722 | 135,424 | 323,238 | unavailable |
| **Totals** | **183,740** | **4,074** | **1,722** | **135,424** | **323,238** | unavailable |

1. Exact-window session filtering is still too easy to make platform-specific.
   - Area: automation / session filtering.
   - Symptom agents experienced: the first file-mtime filter failed on macOS because `find -newermt '2026-07-01T02:01:34Z'` could not parse the `Z` timestamp, so the agent had to switch to `session_meta.timestamp` filtering.
   - Evidence needed to avoid recreating the issue: thread `019f208f-0263-74b1-bdce-cb5b5c8bbe1d` (`Agent thread debt digest`, July 2); failed commands `find "$HOME/.codex/sessions" -type f -name '*.jsonl' -newermt '2026-07-01T02:01:34Z' ! -newermt '2026-07-02T02:01:34Z'`; working shortcut `jq 'select(.type=="session_meta")'` over `~/.codex/sessions/2026/07/{01,02}` and filter `payload.cwd == "/Users/petrbrazdil/Repos/scenery"`.
   - Likely fix owner or next concrete action: automation owner should keep a reusable exact-window `session_meta` filter snippet and avoid BSD/GNU `find` date parsing for eligibility.

2. `$CODEX_HOME` is still not guaranteed in the automation shell.
   - Area: automation environment / memory.
   - Symptom agents experienced: the required memory read at `$CODEX_HOME/automations/scenery-agent-thread-debt-digest/memory.md` initially returned `NO_MEMORY_FILE` because `$CODEX_HOME` was empty; the real memory existed under `${CODEX_HOME:-$HOME/.codex}`.
   - Evidence needed to avoid recreating the issue: thread `019f208f-0263-74b1-bdce-cb5b5c8bbe1d`; commands `if [ -f "$CODEX_HOME/automations/.../memory.md" ]` and fallback `if [ -f "${CODEX_HOME:-$HOME/.codex}/automations/.../memory.md" ]`; affected artifact `/Users/petrbrazdil/.codex/automations/scenery-agent-thread-debt-digest/memory.md`.
   - Likely fix owner or next concrete action: automation runner should export `CODEX_HOME`, or the digest prompt/snippets should consistently use `${CODEX_HOME:-$HOME/.codex}`.

3. Broad session text searches can explode output and drag in unrelated threads.
   - Area: automation / evidence gathering.
   - Symptom agents experienced: a broad `rg` over all session JSONL files produced a huge truncated output and surfaced unrelated tool metadata and non-scenery work, making the eligible set noisier than needed.
   - Evidence needed to avoid recreating the issue: thread `019f208f-0263-74b1-bdce-cb5b5c8bbe1d`; command `rg -n '"type":"session_meta"|/Users/petrbrazdil/Repos/scenery' "$HOME/.codex/sessions" -g '*.jsonl'`; output was truncated at 262,144 tokens and included unrelated sessions mentioning scenery paths from other repos; shortcut: first narrow by year/month/day directories and parse only `session_meta`, then inspect eligible rollout files.
   - Likely fix owner or next concrete action: automation owner should replace ad hoc full-session `rg` with a small jq-first eligibility command.

4. Dirty primary checkouts make doc-only automation harder to verify.
   - Area: repo hygiene / automation scope.
   - Symptom agents experienced: `git status --short` showed many pre-existing modified and untracked files, including `docs/tech-debt.md`, so the automation had to preserve unrelated work and rely on path-limited diffs.
   - Evidence needed to avoid recreating the issue: thread `019f208f-0263-74b1-bdce-cb5b5c8bbe1d`; command `git status --short`; affected paths already dirty included `AGENTS.md`, `README.md`, `apps/console/*`, `apps/consolenext/`, `cmd/scenery/*`, `db/*`, `internal/devdash/*`, and `docs/tech-debt.md`; verification shortcut: always run `git diff -- docs/tech-debt.md` after the patch and avoid broad cleanup.
   - Likely fix owner or next concrete action: agents should keep recurring doc automations path-limited in dirty primaries, or run them from a clean worktree when durable implementation work is ongoing.

### Agent Thread Findings - 2026-07-01

Inspected 1 eligible Codex thread attached to `/Users/petrbrazdil/Repos/scenery` in the previous 24 hours. No eligible thread was missing a local `token_count` record.

| Thread | Input | Output | Reasoning | Cache Read | Total Tokens | Cost (USD) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Build and migrate consolenext | 1,660,196 | 125,209 | 42,659 | 29,893,376 | 31,678,781 | unavailable |
| **Totals** | **1,660,196** | **125,209** | **42,659** | **29,893,376** | **31,678,781** | unavailable |

1. Astryx published packages disagreed with current docs/examples.
   - Area: `apps/consolenext` scaffold / frontend dependency integration.
   - Symptom agents experienced: the GitHub example, getting-started docs, and published `@astryxdesign/*@0.1.2` packages required repeated rebuilds before Vite + StyleX compiled.
   - Evidence needed to avoid recreating the issue: thread `019f17fb-5620-79e0-8218-24788711b1a8` (`Build and migrate consolenext`); commands `git clone --sparse https://github.com/facebook/astryx.git`, `curl https://astryx.atmeta.com/docs/getting-started`, `bunx astryx init`, `bun run build`; errors included StyleX `0.19.0` vs Astryx core `0.18.x` opaque type mismatch and `@astryxdesign/core/astryx.css` resolving to missing `src/astryx.css`; affected files `apps/consolenext/package.json`, `apps/consolenext/vite.config.ts`, and `apps/consolenext/src/*`; shortcut: pin StyleX to Astryx core's dependency version and verify package exports before trusting docs text.
   - Likely fix owner or next concrete action: consolenext owner should document the exact working Astryx/StyleX versions in `apps/consolenext/AGENTS.md` and revisit when Astryx publishes a compatible release.

2. Dashboard route ownership was correct while the embedded bundle stayed stale.
   - Area: dashboard build/embed/release contract.
   - Symptom agents experienced: `/consolenext/` was already the dashboard path, but `scenery up` still served the old embedded dashboard until build scripts, release checks, harness pointers, and docs were moved from `ui/` to `apps/consolenext/`.
   - Evidence needed to avoid recreating the issue: thread `019f17fb-5620-79e0-8218-24788711b1a8`; commands `rg -n "dashboard_static|build-dashboard|ui/dist|consolenext"`, `./scripts/build-dashboard-ui-embed.sh`, `.scenery/harness/bin/scenery build`, fixture `scenery up`, and Chrome proof; affected files `scripts/build-dashboard-ui-embed.sh`, `scripts/release-gate.sh`, `cmd/scenery/dashboard_ui_build.go`, `cmd/scenery/harness_self.go`, `docs/local-contract.md`, `docs/ui-agent-contract.md`; extra snag: Vite template still referenced missing `/favicon.svg`; shortcut: after any dashboard source move, verify HTML asset names and one JS/CSS asset through the advertised route, not just route registration.
   - Likely fix owner or next concrete action: dashboard/runtime owner should keep one source constant for dashboard source/dist paths and assert it in release gate plus self-harness.

3. Architecture/self-harness treated nested app dependency trees as source.
   - Area: harness hygiene / generated file scanning.
   - Symptom agents experienced: self-harness failed on ignored `apps/console/node_modules`, then on `apps/consolenext/node_modules`; deleting dependencies was a temporary workaround until the architecture walk ignored nested `node_modules`.
   - Evidence needed to avoid recreating the issue: thread `019f17fb-5620-79e0-8218-24788711b1a8`; commands `.scenery/harness/bin/scenery harness self --summary --write`, `go test ./cmd/scenery`, and architecture tests; output mentioned dashboard checks passing while architecture scanned app-local `node_modules`; affected files included `cmd/scenery/harness_self.go` and its tests; shortcut: reproduce with any app-local `node_modules` under `apps/*/node_modules`.
   - Likely fix owner or next concrete action: harness owner should keep generated-directory skips path-component based and include nested fixtures in architecture tests.

4. Live proof kept hitting stale isolated agent/runtime state.
   - Area: local runtime proof / embedded dashboard refresh.
   - Symptom agents experienced: rebuilt assets were present on disk, but the running isolated demo agent still served old embedded asset names until the app runtime, agent process, and Victoria helpers were stopped and restarted.
   - Evidence needed to avoid recreating the issue: thread `019f17fb-5620-79e0-8218-24788711b1a8`; commands used isolated `SCENERY_AGENT_HOME=/Users/petrbrazdil/Repos/scenery/.scenery/consolenext-demo/agent-home`, `.scenery/harness/bin/scenery down --app-root testdata/apps/basic`, process checks for pid `10490` and Victoria children, `curl http://localhost:4968/consolenext/`, and final asset checks for fresh `index-*.js`/`index-*.css`; shortcut: restart the dashboard-owning agent after rebuilding embedded assets, not only the app runtime.
   - Likely fix owner or next concrete action: agent/runtime owner should expose a cheap dashboard bundle hash or restart recommendation when embedded dashboard assets change under a live agent.

5. Mounted dashboard RPC needed path-aware WebSocket routing.
   - Area: consolenext live dashboard RPC / browser validation.
   - Symptom agents experienced: the migrated UI loaded under `/consolenext/` but stayed disconnected because the WebSocket resolver used `/__scenery` instead of `/consolenext/__scenery`; Chrome proof caught it after smaller DOM checks replaced an over-optimistic wait strategy.
   - Evidence needed to avoid recreating the issue: thread `019f17fb-5620-79e0-8218-24788711b1a8`; commands and checks `bun run lint`, `bun run typecheck`, `bun run build`, `./scripts/build-dashboard-ui-embed.sh`, Chrome proof at `http://localhost:4968/consolenext/`, tabs for Services/Logs/Traces/Databases, and `scenery check --app-root testdata/apps/basic --json`; affected files `apps/consolenext/src/App.tsx`, `apps/consolenext/src/dashboard-rpc.ts`, `apps/consolenext/src/dashboard-ui.tsx`, `apps/consolenext/src/dashboard-model.ts`; shortcut: browser-test embedded dashboards at their mounted path and assert the RPC URL follows the mount prefix.
   - Likely fix owner or next concrete action: consolenext owner should add a small route-mounted RPC smoke check to dashboard browser harness coverage.

### Agent Thread Findings - 2026-06-30

Inspected 2 eligible Codex threads attached to `/Users/petrbrazdil/Repos/scenery` in the previous 24 hours. No eligible thread was missing a local `token_count` record; the active digest row is an edit-time snapshot.

| Thread | Input | Output | Reasoning | Cache Read | Total Tokens | Cost (USD) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Consolegeist prototype and removal | 913,225 | 74,309 | 25,962 | 10,576,640 | 11,564,174 | unavailable |
| Agent thread debt digest (June 30) | 93,803 | 5,400 | 1,356 | 409,088 | 508,291 | unavailable |
| **Totals** | **1,007,028** | **79,709** | **27,318** | **10,985,728** | **12,072,465** | unavailable |

1. Geist component-system scope was confused with Geist font/tokens.
   - Area: frontend design-system integration.
   - Symptom agents experienced: the first implementation used Geist typography and tiny local primitives, then the user asked "are we using this system?" and linked `https://vercel.com/geist/button`.
   - Evidence needed to avoid recreating the issue: thread `019f1210-c44f-7980-8cd2-4affd9e1a070` (`Consolegeist prototype and removal`); commands `curl -L https://vercel.com/geist/button`, `npm view geist ...`, and `npm view @vercel/geistcn ...`; `@vercel/geistcn` returned npm `E404`; affected files were `apps/consolegeist/src/components/geist.tsx`, `apps/consolegeist/src/App.tsx`, and `apps/consolegeist/README.md`; shortcut: verify whether the requested design system is an installable component package before building local adapters.
   - Likely fix owner or next concrete action: scenery dashboard owner should keep design-system experiments explicit: font-only, local adapter, or real package, with the package check recorded before implementation.

2. Throwaway UI prototypes still create durable repo hooks.
   - Area: prototype cleanup / repo hygiene.
   - Symptom agents experienced: `consolegeist` was "just a test", but removal needed cleanup of `apps/consolegeist`, a root `AGENTS.md` child-index entry, route-manifest expectations, harness artifacts, and a live Vite listener.
   - Evidence needed to avoid recreating the issue: thread `019f1210-c44f-7980-8cd2-4affd9e1a070`; user request "remove consolegeist from the scenery, it was just a test"; commands `lsof -iTCP:5174 -sTCP:LISTEN -n -P`, `rm -rf apps/consolegeist .scenery/harness/consolegeist`, `rg -n "consolegeist" AGENTS.md internal apps docs cmd ui`, and `go test ./internal/agent -run TestPathRouteManifestForSession`; affected files included `AGENTS.md` and `internal/agent/path_routing_test.go`; shortcut: for temporary routes, track every durable hook at creation time and remove the same list on cleanup.
   - Likely fix owner or next concrete action: agent workflow should prefer isolated temporary app surfaces without child `AGENTS.md` or route-test edits unless the prototype is meant to survive review.

3. Console browser proof depended on fake backend plumbing and Vite mode details.
   - Area: browser verification / dashboard frontend.
   - Symptom agents experienced: dev-mode proof mounted the page but produced StrictMode WebSocket close noise, production proof needed `VITE_SCENERY_DASHBOARD_WS_URL`, detached dev startup exited without a useful log, and cleanup later printed repeated `ECONNREFUSED 127.0.0.1:9401` proxy errors.
   - Evidence needed to avoid recreating the issue: thread `019f1210-c44f-7980-8cd2-4affd9e1a070`; commands `VITE_SCENERY_DASHBOARD_WS_URL=ws://127.0.0.1:9401/__scenery bun run build`, inline Node/CDP fake-dashboard verifier, `bun run dev -- --host 127.0.0.1 --port 5174`, and `curl -I http://127.0.0.1:5174/consolegeist/`; affected files/routes were `apps/consolegeist/vite.config.ts`, `/consolegeist/`, and `/__scenery`; shortcut: browser-prove console flows against production preview with an explicit fake dashboard WebSocket URL, not an ad hoc dev server.
   - Likely fix owner or next concrete action: scenery dashboard should extract the fake dashboard WebSocket plus CDP journey into a small checked-in verifier or harness fixture.

4. Dirty primary checkout made scope proof harder.
   - Area: worktree hygiene / automation and implementation.
   - Symptom agents experienced: both the implementation and digest threads saw many pre-existing modified console/dashboard/runtime files, so the agent had to keep source-only sweeps and path-limited status checks separate from unrelated local work.
   - Evidence needed to avoid recreating the issue: threads `019f1210-c44f-7980-8cd2-4affd9e1a070` and `019f1642-63fb-7da0-a8bf-cf1d365e85d4`; commands `git status --short --branch`, `git status --short -- AGENTS.md internal/agent/path_routing_test.go apps/consolegeist`, and final `git diff -- docs/tech-debt.md`; affected files already dirty included `apps/console/*`, `cmd/scenery/*`, `db/*`, `internal/devdash/*`, and `docs/tech-debt.md`; shortcut: use path-limited status/diff before and after doc-only or prototype cleanup work.
   - Likely fix owner or next concrete action: agents should use fresh worktrees for durable implementation and keep automations path-limited when the primary checkout is dirty.

5. Exact-window digest mechanics are still easy to get wrong.
   - Area: automation / token accounting.
   - Symptom agents experienced: file mtimes included noisy candidates, `$CODEX_HOME` was empty in the shell, and the active digest thread's token totals changed while the table was being prepared.
   - Evidence needed to avoid recreating the issue: thread `019f1642-63fb-7da0-a8bf-cf1d365e85d4` (`Agent thread debt digest`, June 30); commands `date -u`, `session_meta.cwd == "/Users/petrbrazdil/Repos/scenery"` filtering, `jq 'select(.type=="event_msg" and .payload.type=="token_count") | .payload.info.total_token_usage' ... | tail -1`, and memory fallback `${CODEX_HOME:-$HOME/.codex}/automations/scenery-agent-thread-debt-digest/memory.md`; shortcut: filter by `session_meta.timestamp` and exact cwd, then refresh active-thread token totals immediately before patching.
   - Likely fix owner or next concrete action: automation owner should wrap the eligible-thread and token extraction in a small reusable shell snippet that handles empty `CODEX_HOME` and labels active rows as snapshots.

### Agent Thread Findings - 2026-06-29

Inspected 2 eligible Codex threads attached to `/Users/petrbrazdil/Repos/scenery` in the previous 24 hours. No eligible thread was missing a local `token_count` record. Only 4 real recurring process issues were present; no new implementation thread attached to the primary repo appeared in this window.

| Thread | Input | Output | Reasoning | Cache Read | Total Tokens | Cost (USD) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Agent thread debt digest (June 28) | 162,032 | 10,450 | 3,372 | 1,609,216 | 1,781,698 | unavailable |
| Agent thread debt digest (June 29) | 94,656 | 6,598 | 2,932 | 342,144 | 443,398 | unavailable |
| **Totals** | **256,688** | **17,048** | **6,304** | **1,951,360** | **2,225,096** | unavailable |

1. Active digest token totals drift while the table is being written.
   - Area: automation / token accounting.
   - Symptom agents experienced: the current automation thread's latest `token_count` changed during the run, and the June 28 digest itself logged that it had to refresh token totals because the active thread was still accumulating usage.
   - Evidence needed to avoid recreating the issue: threads `019f0bf6-3b07-7352-80d1-9bbc72213793` (`Agent thread debt digest`, June 28) and `019f111b-c312-7a63-9a8e-036c1162cf10` (`Agent thread debt digest`, June 29); commands `jq -c 'select(.type=="event_msg" and .payload.type=="token_count") | .payload.info.total_token_usage' ... | tail -1`; June 28 doc table recorded 818,240 tokens for its active digest while the final local record is 1,781,698; shortcut: refresh token totals immediately before the patch and treat the active thread row as an edit-time snapshot.
   - Likely fix owner or next concrete action: automation owner should either exclude the active digest thread from usage totals or explicitly label active-thread token rows as snapshots.

2. Exact repo attachment is easy to blur with scenery worktrees.
   - Area: session filtering / automation scope.
   - Symptom agents experienced: file mtime and text search surfaced a scenery-related worktree session in the 24-hour window, but its `session_meta.cwd` was `/Users/petrbrazdil/.codex/worktrees/fed2/scenery`, not `/Users/petrbrazdil/Repos/scenery`.
   - Evidence needed to avoid recreating the issue: current thread `019f111b-c312-7a63-9a8e-036c1162cf10`; excluded thread `019f0c63-32b7-7a51-a4eb-3c3659d8e4f5`; command filtering `session_meta.timestamp`, `session_meta.id`, and `session_meta.cwd`; affected doc `docs/tech-debt.md`; shortcut: build the eligible set from `session_meta.cwd == "/Users/petrbrazdil/Repos/scenery"` before reading content.
   - Likely fix owner or next concrete action: automation owner should keep an exact-cwd prefilter helper or command snippet in the automation memory.

3. Digest-only windows tempt stale issue recycling.
   - Area: agent workflow / debt hygiene.
   - Symptom agents experienced: the only eligible threads were yesterday's digest and today's running digest, while yesterday's five implementation findings came from `019f09cb-ae40-79c3-b838-5d1d746cb06c`, a thread outside the current 24-hour window.
   - Evidence needed to avoid recreating the issue: automation memory at `/Users/petrbrazdil/.codex/automations/scenery-agent-thread-debt-digest/memory.md`; existing `Agent Thread Findings - 2026-06-28` section in `docs/tech-debt.md`; current exact-window filter results showing only `019f0bf6-3b07-7352-80d1-9bbc72213793` and `019f111b-c312-7a63-9a8e-036c1162cf10`; shortcut: if all eligible threads are automation digests, write only real automation/process issues and state that no primary-repo implementation thread appeared.
   - Likely fix owner or next concrete action: scenery maintainers should keep the digest section honest when there is no fresh implementation evidence instead of carrying forward old top-five lists.

4. `$CODEX_HOME` is not guaranteed in the automation shell.
   - Area: automation environment.
   - Symptom agents experienced: reading `$CODEX_HOME/automations/scenery-agent-thread-debt-digest/memory.md` initially looked missing because `$CODEX_HOME` was empty, while `${CODEX_HOME:-$HOME/.codex}/automations/.../memory.md` existed.
   - Evidence needed to avoid recreating the issue: current thread `019f111b-c312-7a63-9a8e-036c1162cf10`; command output from `printf '%s\n' "$CODEX_HOME"` and the first memory read `__MISSING__`; fallback read of `/Users/petrbrazdil/.codex/automations/scenery-agent-thread-debt-digest/memory.md`; shortcut: always resolve automation memory with `${CODEX_HOME:-$HOME/.codex}` in shell commands.
   - Likely fix owner or next concrete action: automation runner should export `CODEX_HOME`, or digest instructions should use the fallback path in every shell snippet.

### Agent Thread Findings - 2026-06-28

- Area: agent workflow
- Severity: medium
- Owner: scenery maintainers
- Created: 2026-06-28
- Review after: 2026-07-05

Inspected 2 eligible Codex threads attached to `/Users/petrbrazdil/Repos/scenery` in the previous 24 hours. No eligible thread was missing a local `token_count` record.

| Thread | Input | Output | Reasoning | Cache Read | Total Tokens | Cost (USD) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Scenery ps, path routing, and console work | 4,613,417 | 284,731 | 82,250 | 105,908,352 | 110,806,500 | unavailable |
| Agent thread debt digest | 152,963 | 6,077 | 1,732 | 659,200 | 818,240 | unavailable |
| **Totals** | **4,766,380** | **290,808** | **83,982** | **106,567,552** | **111,624,740** | unavailable |

1. `scenery ps` registration was treated as runtime proof.
   - Area: routing / agent workflow.
   - Symptom agents experienced: a path-mode `scenery ps` table was presented as success even though `/`, `/console/`, `/pulse/`, and other URLs were not actually reachable or usable.
   - Evidence needed to avoid recreating the issue: thread `019f09cb-ae40-79c3-b838-5d1d746cb06c` (`Scenery ps, path routing, and console work`); user report "none of those urls work"; commands around `scenery ps`, `curl http://localhost:4747/...`, and browser checks; affected paths included `cmd/scenery/dev_frontends.go`, `cmd/scenery/local_path_router.go`, `internal/agent/router.go`, `internal/agent/session.go`, and `docs/local-contract.md`; shortcut: after any route table change, curl every advertised URL plus one asset URL per frontend before reporting success.
   - Likely fix owner or next concrete action: scenery runtime / agent DX should add a cheap route-manifest reachability check to `scenery harness ui` or a `scenery ps --verify-routes` style command.

2. Path-mode asset prefixing escaped the service paths.
   - Area: local path routing.
   - Symptom agents experienced: Vite, dashboard, Astro, and storage pages loaded shells or blanks because root-relative assets such as `/@vite/client`, `/assets/...`, and storage assets bypassed the service prefix.
   - Evidence needed to avoid recreating the issue: thread `019f09cb-ae40-79c3-b838-5d1d746cb06c`; status updates identified root-relative Vite/runtime assets, dashboard `/assets/...`, storage `/storage/assets/...`, and the dashboard route rename from `/console/` to `/consolenext/`; affected files included `cmd/scenery/dev_frontends.go`, `cmd/scenery/local_path_router.go`, `internal/agent/router.go`, `internal/agent/path_routing_test.go`, and ONLV `apps/blog/astro.config.mjs`; shortcut: compare HTML asset URLs against `route_manifest.routes` and request both prefixed and unprefixed asset URLs.
   - Likely fix owner or next concrete action: scenery runtime should keep prefix handling centralized and add fixture-backed browser coverage for one Vite frontend, one Astro frontend, dashboard assets, and storage assets.

3. Stale registry/session state kept looking like live infrastructure.
   - Area: agent registry / status display.
   - Symptom agents experienced: `scenery ps` showed removed Grafana/Temporal shared substrates and old domain service routes after the processes were gone, causing confusion about whether deprecated infrastructure still existed.
   - Evidence needed to avoid recreating the issue: thread `019f09cb-ae40-79c3-b838-5d1d746cb06c`; user report "I thought we got rid of temporal and grafana"; commands `scenery ps --json`, `pgrep -fl "grafana|temporal"`, port curls, and fake-dead registry row proof; fixed by PR #178 in `cmd/scenery/agent.go` and `cmd/scenery/agent_status_test.go`; remaining stale-service evidence lives in old session records.
   - Likely fix owner or next concrete action: scenery runtime should distinguish verified live routes from historical session records in `ps`, or expose an explicit stale-session prune/restart recommendation.

4. Cross-repo live proof depends on fragile local app state.
   - Area: validation / app integration.
   - Symptom agents experienced: ONLV path-mode proof in fresh worktrees hit `astro: command not found`, `missing required local env file`, `sqlite3.OperationalError: unable to open database file`, and even `zsh: command not found: curl` after a PATH-sensitive command.
   - Evidence needed to avoid recreating the issue: thread `019f09cb-ae40-79c3-b838-5d1d746cb06c`; commands around `scenery up --detach`, `tail .../path-mode-onlv-config...log`, `bun run typecheck`, and route curls; affected ONLV paths included `.scenery.json`, `apps/blog/astro.config.mjs`, `.env`, and generated session SQLite paths; shortcut: run `scenery doctor --json --app-root <app>` and verify app-owned env/toolchain prerequisites before claiming a cross-repo route fix works.
   - Likely fix owner or next concrete action: scenery agent DX should make missing app `.env`, missing frontend binaries, and managed SQLite path failures show as grouped readiness diagnostics before route verification starts.

5. Console UI changes were under-verified in the actual browser.
   - Area: dashboard console / browser harness.
   - Symptom agents experienced: the SQLite explorer first passed text checks but later produced a white page, selection reset after the 5-second refresh, awkward horizontal scrolling, and a red `VictoriaLogs is unavailable` error for optional telemetry.
   - Evidence needed to avoid recreating the issue: thread `019f09cb-ae40-79c3-b838-5d1d746cb06c`; user reports "when I select table, the page goes white", "allow me to scroll horizontally", and "VictoriaLogs is unavailable?"; browser checks used console logs, screenshots, DOM state, and scroll offsets; affected files included `apps/console/src/App.tsx`, `apps/console/vite.config.ts`, `apps/console/AGENTS.md`, `cmd/scenery/dashboard_rpc.go`, and `cmd/scenery/dashboard_ui_test.go`; shortcut: after console UI edits, click the target flow, wait past refresh, inspect browser console errors, and check scroll behavior when wide tables are involved.
   - Likely fix owner or next concrete action: scenery dashboard should add a fixture-backed console journey for database selection, table selection after refresh, wide-table scrolling, and telemetry-unavailable state.

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
