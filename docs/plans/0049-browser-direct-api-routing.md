# Browser Direct API Routing

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The biggest risk is that "browser -> API" looks simpler than the current Vite proxy path, but it only works if auth, CORS, sync streams, and dev URL discovery are made explicit and tested end to end.

The current ONLV Pulse dev path is:

```text
browser -> agent router -> Pulse Vite dev server -> Vite proxy -> agent HTTPS API -> app API
```

That extra Vite proxy hop was useful before the scenery agent because the browser could call same-origin paths such as `/__scenery/api/...` while Vite forwarded them to the app API. With the agent, scenery already exposes both `pulse.<session>.scenery.localhost` and `api.<session>.scenery.localhost`, so Vite should not proxy application API traffic. The target path is:

```text
browser -> agent router -> app API
```

Pulse should learn the API base URL from scenery-injected frontend env or runtime config, then call `https://api.<session>.scenery.localhost:<port>` directly. Vite should serve only frontend assets, HMR, and local development files.

This plan deliberately does not preserve the old local `/__scenery/api` proxy path for agent dev. The repository rule in `AGENTS.md` says not to add legacy compatibility aliases or backwards-compatibility shims for renamed or removed scenery APIs.

## Progress

- [x] 2026-05-28: Created this ExecPlan from a live failure analysis of Pulse sync requests returning `502 Bad Gateway` through Vite.
- [ ] Phase 1: Make direct API URL discovery explicit for agent-managed frontends.
- [ ] Phase 2: Remove Pulse's Vite API proxy and make Pulse use the direct API base URL.
- [ ] Phase 3: Verify auth, refresh, CORS, and sync shape streams over cross-origin direct API calls.
- [ ] Phase 4: Add runtime and ONLV tests that fail if the Vite proxy or same-origin local fallback returns.
- [ ] Phase 5: Update local docs and developer commands.

## Surprises & Discoveries

- 2026-05-28: The session route was valid. `scenery status --json --app-root /Users/petrbrazdil/Repos/onlv` showed `pulse` routed to `https://pulse.main-dbe32e.scenery.localhost:9440/` and `api` routed to `https://api.main-dbe32e.scenery.localhost:9440/`.
- 2026-05-28: The Pulse root page loaded through the agent with HTTP 200, but `https://pulse.main-dbe32e.scenery.localhost:9440/__scenery/config` and `/__scenery/api/sync/...` returned HTTP 502.
- 2026-05-28: Direct API sync routing works structurally. `curl -k https://api.main-dbe32e.scenery.localhost:9440/sync/tasks.task_relations...` returned HTTP 401, proving the backend route exists and the failure is not an agent routing miss.
- 2026-05-28: `apps/pulse/.scenery` is not involved. The Pulse Vite log at `/Users/petrbrazdil/Repos/onlv/.scenery/sessions/main-dbe32e/logs/frontend-pulse.log` showed `Error: unable to verify the first certificate` for proxied `/sync/...` and `/__scenery/config` requests. Vite/Node was proxying to the agent HTTPS API URL and did not trust the local scenery certificate.
- 2026-05-28: `cmd/scenery/dev_frontends.go` already injects `API_BASE_URL`, `SCENERY_API_BASE_URL`, and `VITE_API_BASE_URL` with the session API route. Pulse's `resolvePulseBaseURL()` checks `VITE_SCENERY_BASE_URL` and runtime config, but it does not check `VITE_API_BASE_URL` before falling back to same-origin `/__scenery/api` for `.localhost` hosts.
- 2026-05-28: Runtime CORS in dev endpoint mode reflects the incoming origin and allows credentials. That may make direct browser-to-API work locally, but it needs explicit tests for `Origin: https://pulse.<session>.scenery.localhost:<port>`, `Authorization`, `Content-Type`, SSE/live shape requests, and auth refresh.

## Decision Log

- Decision: The agent-era development model should prefer browser-to-API calls over same-origin Vite API proxying.
  Rationale: The agent already owns session-scoped API and frontend hostnames. Keeping Vite as a second API proxy creates a hidden Node TLS trust dependency and splits routing ownership across scenery and app-specific frontend config.
  Date/Author: 2026-05-28 / Codex.
- Decision: Do not keep `/__scenery/api` as an agent-dev compatibility path for Pulse.
  Rationale: A compatibility path would hide stale client code and preserve the wrong ownership boundary. Old same-origin local proxy behavior should fail clearly once Pulse is migrated.
  Date/Author: 2026-05-28 / Codex.
- Decision: Do not disable TLS verification in Vite as the main fix.
  Rationale: `secure: false` would paper over the certificate symptom while leaving the bad request path in place. The correct fix is to remove the Vite API proxy from agent dev.
  Date/Author: 2026-05-28 / Codex.
- Decision: Treat auth/CORS as first-class acceptance criteria, not incidental browser behavior.
  Rationale: Cross-origin API calls are safe only if preflight, credentials, auth refresh cookies, Authorization headers, exposed mutation headers, and sync shape streaming all work deliberately.
  Date/Author: 2026-05-28 / Codex.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

The scenery repo is `/Users/petrbrazdil/Repos/scenery`. The target app is `/Users/petrbrazdil/Repos/onlv`. The affected frontend is `/Users/petrbrazdil/Repos/onlv/apps/pulse`.

Relevant scenery files:

- `cmd/scenery/dev_frontends.go` starts managed frontends and injects `API_BASE_URL`, `SCENERY_API_BASE_URL`, `VITE_API_BASE_URL`, `SYNC_URL`, `SCENERY_SYNC_URL`, and `VITE_SYNC_URL`.
- `cmd/scenery/dev_supervisor.go` sets auth-facing `API_BASE_URL`, `SCENERY_API_BASE_URL`, `PUBLIC_APP_URL`, and `SCENERY_PUBLIC_APP_URL` for the app process.
- `runtime/server.go` registers `/__scenery/config`, applies CORS, and handles global OPTIONS preflight.
- `runtime/server_test.go` covers `/__scenery/config` and basic CORS behavior.
- `internal/agent/router.go` routes `api.<session>.scenery.localhost` and `pulse.<session>.scenery.localhost` to their registered backends.

Relevant ONLV files:

- `apps/pulse/vite.config.ts` currently proxies `/__scenery/config` and `/__scenery/api` to `process.env.SCENERY_API_BASE_URL || "http://localhost:4000"`.
- `apps/pulse/src/auth/session.ts` defines `resolvePulseBaseURL()`, `initPulseRuntimeConfig()`, `apiFetch()`, auth header injection, and auth recovery.
- `apps/pulse/src/db/sync.ts` builds sync shape URLs from `resolvePulseBaseURL()`.
- `sync/sync.go` exposes raw public endpoints at `/sync/:table_name`.
- `docs/agent/SYNC.md` documents sync debugging and already says direct unauthenticated API sync requests should return 401.

Terms:

- Browser direct API means the frontend code calls `https://api.<session>.scenery.localhost:<port>` directly from the browser.
- Same-origin proxy means frontend code calls `/__scenery/api/...` on the frontend origin and expects Vite or another proxy to forward it.
- Agent session route means a hostname generated by the scenery agent, such as `https://pulse.main-dbe32e.scenery.localhost:9440/`.
- Shape stream means sync's browser-side shape sync calls to ONLV's `/sync/:table_name` proxy endpoints.

## Milestones

Milestone 1 makes the API route available as a stable browser-facing frontend value. The scenery side should continue injecting `VITE_API_BASE_URL`, and either Pulse must consume that existing variable or scenery must also inject a clearly named `VITE_SCENERY_API_BASE_URL`. Do not add a same-origin fallback for agent sessions.

Milestone 2 removes Pulse's Vite API proxy. `apps/pulse/vite.config.ts` should no longer proxy `/__scenery/api`, `/sync`, or API config in agent dev. If `/__scenery/config` remains for non-agent or desktop bootstrapping, it must not be required for agent-managed Pulse.

Milestone 3 proves auth and sync work cross-origin. Login, refresh, logout, ordinary generated client calls, raw sync GET, raw sync POST, live shape stream/SSE behavior, and mutation response headers must all work from `pulse.<session>` to `api.<session>`.

Milestone 4 adds tests that guard against regression. These should fail if Pulse falls back to `/__scenery/api` on `.scenery.localhost`, if Vite proxy config reappears for API paths, or if runtime CORS cannot satisfy direct API preflight from a session frontend origin.

Milestone 5 updates docs and operational checks so developers debug the direct API path, not the old Vite proxy path.

## Plan of Work

Start by making `resolvePulseBaseURL()` deterministic in agent-managed dev. It should prefer explicit build/dev env such as `VITE_API_BASE_URL` or `VITE_SCENERY_API_BASE_URL`, then runtime config when appropriate, then production environment selection. The `.localhost` branch that returns `${window.location.origin}/__scenery/api` is the weak point and should not apply to `.scenery.localhost` session hosts.

Next remove the Pulse Vite proxy for API traffic. This should delete the `/__scenery/api` proxy route from `apps/pulse/vite.config.ts`. The `/__scenery/config` proxy should also be removed for agent dev unless there is a concrete non-agent use that cannot be replaced. If a non-agent development mode still needs config discovery, implement it as an explicit local mode, not as the default path for agent sessions.

Then tighten browser auth behavior. Direct API calls already pass through `apiFetch()`, which sets `credentials: "include"` and `Authorization` when a token exists. Verify that refresh cookies set by the API host are stored and sent back to the API host during cross-origin fetches from Pulse. Check the `SameSite=Lax`, `Secure`, and cookie-domain behavior in local agent HTTPS and in non-agent local dev. Do not assume cookies work because generated client calls work with bearer tokens; refresh and OAuth callback flows need separate verification.

After that, test sync shape sync. `syncShapeOptions()` builds URLs from `resolvePulseBaseURL()`, so it should automatically move to direct API once the base URL is fixed. However, it currently can produce an empty `Authorization` header when no token exists. Decide during implementation whether that should be omitted instead of sent as an empty header. The direct API path must pass CORS preflight for `Authorization` and any sync headers used by live shape streams.

Finally add regression tests and docs. This is a routing model change, not a one-line frontend fix. The tests should cover static resolver behavior, Vite config absence of API proxy paths, runtime CORS preflight, and a live ONLV smoke through the agent.

## Concrete Steps

1. Scenery frontend env review:
   - Confirm `cmd/scenery/dev_frontends.go` injects the API route that browser code should use.
   - Add `VITE_SCENERY_API_BASE_URL` only if `VITE_API_BASE_URL` is too generic for scenery-owned semantics.
   - Update `cmd/scenery/dev_frontends_test.go` if new env is added.
2. Pulse base URL resolver:
   - Update `apps/pulse/src/auth/session.ts` so `resolvePulseBaseURL()` prefers `import.meta.env.VITE_API_BASE_URL` and/or `VITE_SCENERY_API_BASE_URL`.
   - Ensure `.scenery.localhost` does not fall back to same-origin `/__scenery/api`.
   - Make `initPulseRuntimeConfig()` skip same-origin config fetch when an explicit API base URL is present, or fetch config from the direct API origin if config is still needed.
3. Remove Vite API proxy:
   - Delete the `/__scenery/api` proxy block from `apps/pulse/vite.config.ts`.
   - Delete the `/__scenery/config` proxy block if direct env config covers agent dev.
   - Do not replace this with `secure: false`.
4. Auth and sync browser behavior:
   - Verify generated API calls use direct API URLs and include bearer tokens.
   - Verify auth refresh works with `credentials: "include"` across `pulse.<session>` to `api.<session>`.
   - Verify logout clears the API refresh cookie.
   - Verify Google OAuth callback still uses `APIBaseURL` for callback and `PublicAppURL` for frontend return.
   - Verify `/sync/:table_name` GET, POST, and live shape stream requests use direct API URLs and pass CORS.
   - Consider omitting the `Authorization` header in `apps/pulse/src/db/sync.ts` when no token exists, instead of sending an empty header.
5. Tests:
   - Add Pulse unit tests for `resolvePulseBaseURL()` covering `pulse.<session>.scenery.localhost`, loopback hosts, explicit env values, desktop mode, and production hostnames.
   - Add a static test or script that fails if `apps/pulse/vite.config.ts` contains an API proxy for `/__scenery/api`.
   - Add or update scenery runtime tests for CORS preflight from `https://pulse.main-dbe32e.scenery.localhost:9440` to API endpoints with `authorization` and `content-type` request headers.
   - Add ONLV integration or harness checks that open Pulse through the agent and assert network requests target `api.<session>.scenery.localhost`, not `pulse.<session>/__scenery/api`.
6. Docs:
   - Update `/Users/petrbrazdil/Repos/onlv/docs/agent/SYNC.md` to show direct API sync debugging.
   - Update scenery local docs only if any generic frontend env contract changes.

## Validation and Acceptance

Repo validation from `/Users/petrbrazdil/Repos/scenery`:

```text
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
```

ONLV validation from `/Users/petrbrazdil/Repos/onlv`:

```text
just gen-pulse-client
scenery check --json
scenery harness --json --write
cd apps/pulse && bun run typecheck && bun run lint
```

Live agent validation:

```text
scenery dev --app-root /Users/petrbrazdil/Repos/onlv
scenery status --json --app-root /Users/petrbrazdil/Repos/onlv
```

Expected live behavior:

- Loading `https://pulse.<session>.scenery.localhost:<port>/viewer` succeeds.
- Browser network requests for generated API calls use `https://api.<session>.scenery.localhost:<port>/...`.
- Browser network requests for shape sync use `https://api.<session>.scenery.localhost:<port>/sync/<table>`.
- No browser request uses `https://pulse.<session>.scenery.localhost:<port>/__scenery/api/...`.
- Pulse's frontend log does not contain Vite `http proxy error` lines for `/sync`, `/__scenery/api`, or `/__scenery/config`.
- A direct unauthenticated API sync request returns HTTP 401, not 502:

```text
curl -k -i 'https://api.<session>.scenery.localhost:<port>/sync/tasks.task_relations?offset=-1'
```

- CORS preflight from the Pulse origin succeeds:

```text
curl -k -i -X OPTIONS \
  -H 'Origin: https://pulse.<session>.scenery.localhost:<port>' \
  -H 'Access-Control-Request-Method: GET' \
  -H 'Access-Control-Request-Headers: authorization,content-type' \
  'https://api.<session>.scenery.localhost:<port>/sync/tasks.task_relations'
```

- Auth login, refresh, and logout succeed without same-origin `/__scenery/api`.
- Task collections load in Pulse without 502s and without empty/stuck shape streams.

## Idempotence and Recovery

The frontend change should be safe to retry. Removing Vite proxy blocks is deterministic. If Pulse still has an old dev server process running, restart `scenery dev` or down the current session and start a new one so the Vite process receives the updated environment.

If direct API calls fail with CORS errors, do not reintroduce the Vite proxy. Fix runtime CORS or the API origin configuration and add a regression test. If auth refresh fails, inspect API `Set-Cookie`, browser stored cookies for the API host, `credentials: "include"`, SameSite behavior, and the refresh endpoint response before changing routing.

If a developer has stale generated Pulse client code, rerun `just gen-pulse-client` from `/Users/petrbrazdil/Repos/onlv` before debugging runtime behavior.

If a live session gets into a mixed state, use:

```text
scenery down --all --app-root /Users/petrbrazdil/Repos/onlv
scenery dev --app-root /Users/petrbrazdil/Repos/onlv
```

## Artifacts and Notes

Expected artifacts:

```text
docs/plans/0049-browser-direct-api-routing.md
docs/plans/active.md
/Users/petrbrazdil/Repos/onlv/apps/pulse/src/auth/session.ts
/Users/petrbrazdil/Repos/onlv/apps/pulse/src/db/sync.ts
/Users/petrbrazdil/Repos/onlv/apps/pulse/vite.config.ts
/Users/petrbrazdil/Repos/onlv/docs/agent/SYNC.md
/Users/petrbrazdil/Repos/scenery/cmd/scenery/dev_frontends.go
/Users/petrbrazdil/Repos/scenery/runtime/server_test.go
```

The plan intentionally keeps `/__scenery/config` separate from `/__scenery/api`. Config discovery may still exist, but agent-managed browser API traffic should not depend on a same-origin frontend proxy. If config is retained, prefer direct API config fetch or build-time injected API base URL.

Hard-nosed review of the proposed direction:

- Weak point: assuming direct API is "just a URL change." It is not. It changes browser origin, preflight behavior, cookie storage, and error visibility.
- Weak point: keeping `/__scenery/api` around "just in case." That would let stale code pass tests and preserve the broken ownership boundary.
- Weak point: fixing this with Vite `secure: false`. That makes the current error disappear while keeping a second proxy layer that scenery does not own.
- Stronger direction: make the API origin explicit, make CORS/auth tested, remove the proxy, and make stale same-origin API calls fail.

## Interfaces and Dependencies

No new Go dependencies should be added. Avoid new frontend runtime dependencies unless a concrete browser compatibility issue requires one.

Changed or confirmed interfaces:

- `VITE_API_BASE_URL` or `VITE_SCENERY_API_BASE_URL`: browser-visible direct API base URL for agent-managed frontends.
- `API_BASE_URL` and `SCENERY_API_BASE_URL`: app and tooling API base URL env values remain available.
- `/__scenery/api`: removed from Pulse agent-dev routing; do not preserve as a compatibility path.
- `/__scenery/config`: may remain as runtime API config, but Pulse agent dev must not require same-origin Vite proxying to reach it.
- Runtime CORS must support session frontend origins in dev and explicit allowlists outside dev.
