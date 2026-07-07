# 0090 Local Path Routing and Per-Runtime Dev Ports

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Scenery local development currently optimizes for domain-style URLs, backed by Caddy plus local DNS plumbing. This gives excellent human semantics such as `api.<worktree>.local.dev`, but the DNS layer is a recurring operational failure because it needs privileged setup, resolver files, and dnsmasq health. The goal of this plan is to make the default local dev path work without sudo, without dnsmasq, and without users or agents needing to reason about N different backend ports.

The new default local contract is:

```text
one Scenery dev runtime / app root / Git worktree = one local base URL
```

Example:

```text
main worktree         http://localhost:4001/
feature worktree      http://localhost:4002/
another app           http://localhost:4003/
```

Every app service is then discoverable by stable path prefix under that runtime base URL:

```text
http://localhost:4001/          Scenery runtime console / service launcher
http://localhost:4001/api/      app API
http://localhost:4001/ui/       frontend named ui
http://localhost:4001/blog/     frontend named blog
http://localhost:4001/pulse/    frontend named pulse
http://localhost:4001/sync/     sync/entity stream route when present
http://localhost:4001/runtime/ Scenery-owned internal runtime surface
```

This changes the user and agent question from:

```text
Which port is API in this worktree?
```

to:

```text
What is this runtime's base URL?
```

Once the base URL is known, the API URL is always:

```text
<base-url>/api/
```

This matches Scenery's declared mental model: app roots, dev runtimes, and capabilities are user-facing, while substrate paths, internal backing service ports, and internal session IDs are debug details.  It also matches the repo-local operating model that `scenery up` owns one live dev runtime for an app root and that separate live code copies should be represented as Git worktrees.

Caddy remains part of the system. The change is that Caddy's default local-dev responsibility becomes "listen on the assigned unprivileged localhost port and forward to the Scenery agent/router," instead of "be part of a privileged local DNS + HTTPS edge requirement." DNS/domain routing remains available as an optional compatibility/nice-URL mode.

The production deployment story should share the same route manifest. In dev, root `/` defaults to a Scenery console/discovery page. In production, root `/` defaults to a configured app service such as `ui`, while `/api/`, `/blog/`, `/pulse/`, and other route prefixes continue to follow the same path-routing contract.

## Progress

* [x] 2026-06-27: Created this ExecPlan under `docs/plans/0090-local-path-routing.md`.
* [x] 2026-06-27: Linked this ExecPlan from `docs/plans/active.md`.
* [x] 2026-06-27: Confirmed no existing `0090-*` plan exists on the implementation branch.
* [x] 2026-06-27: Ran `scenery inspect docs --json`, read the root instruction chain, and confirmed no child `AGENTS.md` files under `docs`.
* [x] 2026-06-27: Added `route_manifest` and path/host route-mode model to agent sessions.
* [x] 2026-06-27: Added deterministic localhost port lease manager under the agent run directory.
* [x] 2026-06-27: Added per-runtime localhost listener artifacts, local path proxy, and Caddyfile generation under the session state root.
* [x] 2026-06-27: Added path dispatch to the Scenery agent router, including trusted local route headers, prefix stripping, SPA fallback, and `/runtime/health|routes|config`.
* [x] 2026-06-27: Renamed the path-mode public Scenery-owned control prefix from `/__scenery/` to `/runtime/`, while keeping internal dashboard websocket and storage data-plane paths on their existing private contracts.
* [x] 2026-06-27: Added root runtime route index for path-mode sessions.
* [x] 2026-06-27: Updated `scenery ps` human output to show app, worktree, base URL, services, and update age; JSON now includes the richer route manifest through session records.
* [x] 2026-06-27: Added dev env injection for route mode, base URL, API URL/path, and frontend public URL/path.
* [x] 2026-06-27: Kept host/domain routing available through explicit `dev.routing.mode = "host"`.
* [x] 2026-06-27: Added focused unit tests for path route manifests, router dispatch, port leases, and updated runtime/status expectations.
* [x] 2026-06-27: Updated config schema, local contract, agent guide, skill, README, cookbook, and environment registry.
* [x] 2026-06-27: Validated with `go test ./...`, `go test ./cmd/scenery`, `go run ./cmd/scenery inspect docs --json`, `jq empty docs/environment.registry.json docs/knowledge.json docs/schemas/scenery.config.v1.schema.json`, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write`. Self-harness passed with warnings for pre-existing large-file and slow-test budget findings.

## Surprises & Discoveries

Record unexpected findings here during implementation.

2026-06-27: `--port` already means "force the app backend listen port" for `scenery up`, so this implementation keeps that behavior instead of silently repurposing it as the browser-facing route port. The route lease is configured through `dev.routing.port` and `dev.routing.port_start` / `dev.routing.port_end`.

2026-06-27: The default path-mode listener can be implemented as a small local reverse proxy that forwards trusted route-mode headers to the agent router while still writing the per-session Caddyfile artifact. This keeps tests and ordinary local dev independent from the managed Caddy binary while preserving the Caddy-compatible listener contract.

Initial known facts from source review:

The current agent router is host-based. It extracts the request host, resolves a session and route kind, and then proxies the request to a backend.

The current proxy implementation already supports `stripPrefix`, which is exactly the primitive needed for path-prefix routing.

The current session model already stores `BaseAppID`, `RuntimeAppID`, `RouteNamespace`, `AppRoot`, `StateRoot`, `Routes`, and `Backends`, so the plan should extend this model rather than invent a separate runtime registry.

The current session ID generation already includes a sanitized branch/worktree-ish label plus a short hash of the app root. That makes it a good basis for distinguishing two worktrees even when branch names collide.

The current `scenery ps` table exposes app root, status, console URL, and update age. It should be changed to expose app name, worktree label, base URL, and service paths.

The current Caddy edge config already acts as a managed reverse proxy to the agent upstream and sets forwarding headers plus `X-Scenery-Edge-Token`. The new mode should reuse that Caddy management machinery but generate localhost-port listeners instead of requiring wildcard DNS.

The current DNS path installs/runs dnsmasq and resolver state, and that is the part this plan should make optional for default local development.

## Decision Log

* Decision: The default local dev route mode will be path routing under `http://localhost:<allocated-port>/`.

  * Rationale: It removes sudo/dnsmasq as a default dependency and gives humans/agents a stable invariant: discover one base URL, then append known route paths.
  * Date: 2026-06-27
  * Author: initial ExecPlan

* Decision: The allocated port belongs to the Scenery dev runtime session/app root/worktree, not to each individual service.

  * Rationale: The problem being solved is cognitive overhead from many service ports. Giving every service a path under one runtime URL removes the "which port is API?" question.
  * Date: 2026-06-27
  * Author: initial ExecPlan

* Decision: Do not include the worktree label in the path by default.

  * Rationale: `http://localhost:4001/api/` and `http://localhost:4002/api/` are easier to reason about than `http://localhost:4001/<worktree>/api/`; the port and discovery page identify the worktree.
  * Date: 2026-06-27
  * Author: initial ExecPlan

* Decision: Keep Caddy as the managed listener/proxy layer.

  * Rationale: Caddy is already integrated and working well. The failure mode is the privileged DNS side, not Caddy itself.
  * Date: 2026-06-27
  * Author: initial ExecPlan

* Decision: Keep host/domain routing as an optional compatibility mode.

  * Rationale: Existing workflows and production-like testing may still want domain-style routes. This plan changes the default, not necessarily the whole capability surface.
  * Date: 2026-06-27
  * Author: initial ExecPlan

* Decision: Keep `scenery up --port` as the hidden app-backend debugging escape hatch and use `dev.routing.port` for the browser-facing path-mode route lease.

  * Rationale: Existing CLI behavior and tests already rely on `--port` for the backend listener. Reusing it for the public route port would make app startup less predictable and break a debugging workflow.
  * Date: 2026-06-27
  * Author: Codex

* Decision: Start path mode through a Scenery-owned localhost reverse proxy and write the equivalent per-session Caddyfile under `local-caddy/`.

  * Rationale: The routing contract depends on a localhost listener that injects trusted headers before forwarding to the agent router. The equivalent Caddyfile documents the intended managed Caddy shape, while the built-in listener keeps default local dev from depending on Caddy availability.
  * Date: 2026-06-27
  * Author: Codex

* Decision: Production deployment should use the same route manifest shape, but allow `/` to map to an app service instead of the Scenery console.

  * Rationale: The same "base URL + path route" model should work locally and in production, with only the base URL and root route differing by environment.
  * Date: 2026-06-27
  * Author: initial ExecPlan

## Outcomes & Retrospective

Path-mode local dev is now the default for agent-backed `scenery up`. Sessions carry a route manifest with `mode`, `base_url`, route records, and a per-runtime port lease; compatibility `routes` are still populated from that manifest.

dnsmasq and wildcard HTTPS are no longer required for default local development. Host/domain routing remains available through `dev.routing.mode = "host"` and keeps the existing edge readiness checks.

`scenery ps` now exposes app, worktree, status, base URL, services, and update age in human output, while JSON sessions include `route_manifest` for agents.

The production route-manifest work remains deferred. ONLV-specific end-to-end proof was not run in this worktree; the Scenery self-harness and fixture matrix passed, and path routing is covered by focused agent/router/session tests plus the parallel runtime isolation harness.

## Context and Orientation

The current local routing stack has these relevant pieces:

`cmd/scenery/edge.go` owns the system edge commands. It currently has defaults for `127.0.0.1:443`, an internal target `127.0.0.1:19443`, DNS defaults, Caddy startup, dnsmasq config, edge state, and privileged listener management. The edge command includes `dns`, `dns-helper`, `privileged`, and Caddy install/restart/status/uninstall flows.

`internal/agent/server.go` owns the agent server, router listener, registry, and dashboard backend wiring. It opens the registry with the public router address and route scheme, then serves control and router muxes.

`internal/agent/router.go` currently implements host-based dispatch. It resolves the request host into a session and route kind. For dashboard routes it calls `handleConsole`; for frontend routes it calls `handleFrontendRoute`; otherwise it proxies to the backend.

`internal/agent/router.go` also already contains `proxyBackendOptions{stripPrefix string, spaFallback bool}` and applies the prefix stripping inside the proxy director. That should be reused for path mode.

`internal/agent/session.go` builds route URLs from session ID, router address, router scheme, backends, and route namespace. Today this is host-oriented: `api.<session>.<domain>`, `console.<session>.<domain>`, and one host per frontend/service route.

`internal/agent/registry.go` indexes routes by host. Path mode needs either a separate lookup path keyed by base URL/session listener or a way for the per-runtime Caddy listener to identify the target session before the request reaches the agent router.

`cmd/scenery/agent_status_table.go` writes the `scenery ps` human table. This should change after the route manifest and path URL contract are introduced.

`docs/local-contract.md` defines Scenery's local developer contract and currently documents edge/DNS/HTTPS as dev-only or beta surfaces. This file must be updated when the default dev route contract changes.

`docs/agent-guide.md` tells agents to use `scenery ps` / `scenery ps --json` and related JSON surfaces during local debugging. This must be updated so agents discover `base_url` and service paths rather than domain-style routes.

ONLV currently configures route-base-domain and per-frontend hosts such as `pulse.onlv.dev`, `blog.onlv.dev`, and `ui.onlv.dev`. It also defines auth URL env names such as `PublicAppURL`, `APIBaseURL`, and `AuthCookieDomain`. Path mode must support this app without requiring these domain hosts in default local development.

## Milestones

### Milestone 1: Define the route-mode model and JSON contract

Introduce an explicit local route mode:

```text
host
path
```

The default for new local development should be `path`.

The route mode needs to be inspectable in `scenery ps --json`, app/session manifests, and possibly `scenery inspect routes --json`.

The path-mode contract should expose at least:

```json
{
  "mode": "path",
  "base_url": "http://localhost:4001",
  "root_route": "scenery-console",
  "routes": {
    "root": {
      "url": "http://localhost:4001/",
      "path": "/",
      "kind": "scenery-console"
    },
    "api": {
      "url": "http://localhost:4001/api/",
      "path": "/api/",
      "backend": "api",
      "strip_prefix": "/api"
    },
    "ui": {
      "url": "http://localhost:4001/ui/",
      "path": "/ui/",
      "backend": "ui",
      "strip_prefix": "/ui"
    }
  }
}
```

Keep existing `session.Routes map[string]string` for compatibility if practical, but add a richer structure for route metadata. String-only route maps cannot reliably express base URL, path prefix, strip prefix, route mode, root route, or production root mapping.

Candidate new types in `internal/agent/types.go`:

```go
type RouteMode string

const (
    RouteModeHost RouteMode = "host"
    RouteModePath RouteMode = "path"
)

type RouteManifest struct {
    Mode      RouteMode              `json:"mode"`
    BaseURL   string                 `json:"base_url"`
    Root      string                 `json:"root"`
    Routes    map[string]RouteRecord `json:"routes"`
    PortLease *PortLease             `json:"port_lease,omitempty"`
}

type RouteRecord struct {
    Name        string `json:"name"`
    Kind        string `json:"kind"`
    URL         string `json:"url"`
    Path        string `json:"path,omitempty"`
    StripPrefix string `json:"strip_prefix,omitempty"`
    Backend     string `json:"backend,omitempty"`
}
```

Extend `localagent.Session`:

```go
type Session struct {
    ...
    RouteManifest RouteManifest `json:"route_manifest,omitempty"`
}
```

Keep:

```go
Routes map[string]string `json:"routes"`
```

during transition.

Acceptance for this milestone:

```text
go test ./internal/agent
go test ./cmd/scenery
```

Focused tests should prove that route manifests render both host mode and path mode for the same set of backends.

### Milestone 2: Add deterministic localhost port lease management

Create a small port lease manager, probably in a new package/file such as:

```text
internal/agent/ports.go
internal/agent/ports_test.go
```

or, if it is more CLI/runtime-owned:

```text
cmd/scenery/dev_ports.go
cmd/scenery/dev_ports_test.go
```

The lease manager owns the mapping:

```text
app root + session ID -> localhost port
```

Default range:

```text
4001-4999
```

Configuration:

```json
{
  "dev": {
    "routing": {
      "mode": "path",
      "port_range": {
        "start": 4001,
        "end": 4999
      }
    }
  }
}
```

Do not add environment-variable knobs by default. Repo rules say new env vars should be avoided unless flags/config are insufficient.

Port allocation algorithm:

1. Resolve absolute app root.
2. Derive session ID with existing `SessionID(appRoot, branch)` logic.
3. Hash the clean app root into the configured range.
4. If the preferred port is free or owned by this live session, use it.
5. If taken, scan forward through the range.
6. Before claiming a port, check:

   * no live Caddy/listener process owns it for another session,
   * no unrelated process is listening,
   * any old Scenery lease owner is stale before reclaim.
7. Persist the lease.
8. Reuse the same lease for the same app root/session on restart if possible.
9. Release on `scenery down` where safe.
10. Prune stale leases as part of existing prune/down flows.

Lease file candidate:

```text
<scenery home>/run/dev-ports.json
```

or under the existing agent/run paths, depending on `localagent.Paths`.

Lease shape:

```go
type PortLease struct {
    SchemaVersion string    `json:"schema_version"`
    AppRoot       string    `json:"app_root"`
    SessionID     string    `json:"session_id"`
    BaseAppID     string    `json:"base_app_id"`
    Branch        string    `json:"branch,omitempty"`
    WorktreeLabel string    `json:"worktree_label"`
    Port          int       `json:"port"`
    URL           string    `json:"url"`
    OwnerPID      int       `json:"owner_pid,omitempty"`
    Owner         Owner     `json:"owner"`
    CreatedAt     time.Time `json:"created_at"`
    UpdatedAt     time.Time `json:"updated_at"`
}
```

Lease registry shape:

```go
type PortLeaseFile struct {
    SchemaVersion string      `json:"schema_version"`
    Leases        []PortLease `json:"leases"`
}
```

A stale lease is reclaimable only if:

* the owner PID is missing or dead, or
* the owner fingerprint fails and no process is listening on the leased port, or
* the listener is verified to be an old Scenery Caddy process for that stale owner.

Never kill unrelated processes. If the preferred port is occupied by an unrelated process, choose the next port and record a warning in JSON/status.

Acceptance for this milestone:

* Unit tests for deterministic preferred port.
* Unit tests for scan-forward collision handling.
* Unit tests for stale Scenery lease reclaim.
* Unit tests proving unrelated occupied ports are skipped, not killed.
* Unit tests proving two app roots with same branch get different stable session/worktree labels because app-root hash is included.

### Milestone 3: Generate and manage Caddy localhost listeners per runtime

Add a Caddy local path edge mode. This should not replace the existing `system edge` DNS/HTTPS flow immediately. It should be a new managed local-dev listener mode.

Candidate files:

```text
cmd/scenery/local_caddy.go
cmd/scenery/local_caddy_test.go
```

or extend existing `cmd/scenery/edge.go` carefully if that does not make the file too large.

The Caddy config for one runtime should conceptually be:

```caddyfile
{
    admin unix//<admin-socket>
    auto_https off
}

http://127.0.0.1:4001, http://localhost:4001 {
    reverse_proxy <agent-router-addr> {
        flush_interval -1
        header_up Host {host}
        header_up X-Forwarded-Proto http
        header_up X-Forwarded-Port 4001
        header_up X-Scenery-Local-Route-Mode path
        header_up X-Scenery-Session <session-id>
        header_up X-Scenery-Base-URL http://localhost:4001
        header_up X-Scenery-Edge-Token <token-if-reusing-edge-trust>
    }
}
```

The key routing decision: the agent must know which session owns a request to `localhost:4001`. Host alone is insufficient because every runtime uses `localhost`. Therefore either:

1. Caddy injects `X-Scenery-Session: <session-id>`, and the agent trusts this header only from loopback plus a shared token; or
2. each runtime gets its own direct Go listener, and Caddy is not in the local path; or
3. the agent itself owns every per-runtime listener instead of Caddy.

Chosen approach for this plan: use Caddy with a trusted Scenery header/token, because the user explicitly wants to keep Caddy for this logic and current Caddy edge already injects `X-Scenery-Edge-Token` for trusted forwarding.

Security rule:

* Do not trust `X-Scenery-Session` from arbitrary clients.
* Trust it only when:

  * remote address is loopback, and
  * `X-Scenery-Edge-Token` matches the Scenery-managed local token, and
  * the target session owner fingerprint is still valid.

Add a helper similar in spirit to existing `trustedEdgeRequest(req)`:

```go
func (s *Server) trustedLocalPathRequest(req *http.Request) bool
```

This avoids allowing a random browser/client to spoof another session merely by setting headers.

Caddy process/state model:

Each live session can either get:

* one Caddy process per runtime port, simpler to isolate and clean up; or
* one Caddy process with many site blocks, more efficient but more complex to update atomically.

Chosen implementation path:

First version: one Caddy process per runtime session.

Rationale:

* Easy cleanup on `scenery down`.
* Easy stale owner verification.
* Easy to reason about with worktrees.
* Keeps failure localized: one bad config or occupied port does not take down all local runtimes.

Possible future optimization:

* Consolidate into one managed Caddy process with multiple site blocks once the contract is stable.

State file candidate:

```text
<session-state-root>/local-caddy.json
<session-state-root>/local-caddy/Caddyfile
<session-state-root>/local-caddy/caddy.log
<session-state-root>/local-caddy/admin.sock
```

State shape:

```go
type LocalCaddyState struct {
    SchemaVersion string    `json:"schema_version"`
    Status        string    `json:"status"`
    PID           int       `json:"pid,omitempty"`
    SessionID     string    `json:"session_id"`
    AppRoot       string    `json:"app_root"`
    Listen        string    `json:"listen"`
    BaseURL       string    `json:"base_url"`
    AgentRouter   string    `json:"agent_router"`
    ConfigPath    string    `json:"config_path"`
    AdminSocket   string    `json:"admin_socket"`
    LogPath       string    `json:"log_path"`
    Owner         Owner     `json:"owner"`
    UpdatedAt     time.Time `json:"updated_at"`
}
```

Startup algorithm inside `scenery up`:

1. Ensure Scenery agent is running.
2. Compute or load session ID.
3. Allocate port lease.
4. Generate Caddy config for the runtime port.
5. Start Caddy.
6. Health-check `http://localhost:<port>/runtime/health` or equivalent.
7. Register/update session with route manifest and base URL.
8. Emit JSON and human output using the base URL.

Acceptance for this milestone:

* Unit test for Caddyfile generation.
* Unit test proving generated config includes `X-Scenery-Session`, base URL, forwarded proto/port, and token.
* Integration-ish test with fake Caddy binary if existing tests use fake managed artifacts.
* Test stale Caddy state cleanup without touching unrelated process.
* Test collision: occupied port results in next lease.

### Milestone 4: Add path-mode dispatch to the agent router

Modify:

```text
internal/agent/router.go
internal/agent/router_test.go
```

Current behavior:

```text
Host -> registry.RouteTargetForHost(host) -> session + route kind
```

New behavior:

```text
trusted local path request with X-Scenery-Session -> session by ID -> route by URL path prefix
else host routing as today
```

Pseudo-code:

```go
func (s *Server) routerMux() http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
        if cleanRequestPath(req.URL.Path) == "/v1/tls/allow" {
            s.handleTLSAllow(w, req)
            return
        }

        if s.trustedLocalPathRequest(req) {
            sessionID := strings.TrimSpace(req.Header.Get("X-Scenery-Session"))
            if session, ok := s.registry.GetSession(sessionID); ok {
                if s.handlePathModeRoute(w, req, session) {
                    return
                }
            }
            http.NotFound(w, req)
            return
        }

        // existing host mode
        host := requestHost(req)
        session, kind, ok := s.routeTargetForHost(host)
        ...
    })
}
```

Add:

```go
func (s *Server) handlePathModeRoute(w http.ResponseWriter, req *http.Request, session Session) bool
```

Dispatch order matters:

1. Normalize path.
2. Handle exact internal Scenery paths:

   * `/runtime/config`
   * `/runtime/health`
   * future `/runtime/routes`
3. Handle `/api` and `/api/*`.
4. Handle `/sync` and `/sync/*` if a sync backend exists.
5. Handle frontend routes by longest prefix:

   * `/pulse`
   * `/pulse/*`
   * `/blog`
   * `/blog/*`
   * `/ui`
   * `/ui/*`
6. Handle `/console` and `/console/*`.
7. Handle `/`:

   * dev mode: Scenery runtime console / launcher
   * production mode later: configured root route
8. Return 404 with a helpful plain text or minimal HTML page listing available routes.

Prefix rules:

* Accept both `/api` and `/api/`.
* Canonical browser redirects:

  * `/api` can either proxy as `/` or redirect to `/api/`; for APIs, avoid surprising redirects for non-GET methods.
  * `/ui` should redirect to `/ui/` for browser frontends so relative assets behave.
* Strip prefix before proxying:

  * external `/api/v1/todos` -> backend `/v1/todos`
  * external `/ui/assets/app.js` -> frontend backend `/assets/app.js`
* Set headers:

  * `X-Forwarded-Prefix: /api`
  * `X-Scenery-Route-Prefix: /api`
  * `X-Scenery-Base-URL: http://localhost:4001`
  * `X-Scenery-Public-URL: http://localhost:4001/api/`

Important frontend behavior:

The existing frontend handler blocks reserved paths such as `/runtime`, `/__scenery`, `/api`, and `/sync` from being handled by a frontend. Keep that invariant for path mode.

SPA fallback:

Existing frontend proxy logic supports SPA fallback for HTML requests. Path mode must preserve it after prefix stripping. A request to:

```text
/ui/settings
```

should proxy to frontend backend as:

```text
/settings
```

and if that 404s with `Accept: text/html`, SPA fallback should request backend `/`.

Route conflict rules:

* `api`, `console`, `dashboard`, `runtime`, `sync`, and `__scenery` are reserved top-level path segments.
* Frontend or dev service route names must sanitize to safe path labels.
* If two backends want the same route path, `scenery check` must fail before runtime.
* If a route name contains unsafe characters, use existing `sanitizeLabel` behavior and expose the sanitized route in inspect output.
* If a configured frontend name sanitizes to empty, fail check.

Acceptance for this milestone:

* Router unit tests for:

  * `/` discovery page.
  * `/api/foo` strips `/api`.
  * `/api` behavior for GET and POST.
  * `/ui/settings` strips `/ui` and SPA fallback still works.
  * `/runtime/config` works from frontend path mode.
  * reserved paths cannot be captured by frontend routes.
  * unknown prefix returns 404 with route list.
  * spoofed `X-Scenery-Session` without token is rejected.
  * host routing still works.

### Milestone 5: Root runtime console / service launcher

Add a simple root page for path mode. This does not need to be the full dashboard UI. It must be durable, plain, fast, and useful to humans and agents.

Candidate implementation:

```text
internal/agent/router_console.go
internal/agent/router_console_test.go
```

or extend existing dashboard/devdash code if appropriate.

`GET /` in dev path mode should return HTML:

```html
<!doctype html>
<title>Scenery: onlv checkout-flow-91bc0a</title>
<h1>onlv</h1>
<p>Worktree: checkout-flow-91bc0a</p>
<p>App root: /Users/pbrazdil/dev/onlv-checkout-flow</p>

<h2>Services</h2>
<ul>
  <li><a href="/api/">API</a></li>
  <li><a href="/ui/">ui</a></li>
  <li><a href="/blog/">blog</a></li>
  <li><a href="/pulse/">pulse</a></li>
  <li><a href="/sync/">sync</a></li>
  <li><a href="/console/">Scenery console</a></li>
</ul>
```

Also add JSON discovery:

```text
GET /runtime/routes
```

returns:

```json
{
  "schema_version": "scenery.local.routes.v1",
  "app": "onlv",
  "worktree": "checkout-flow-91bc0a",
  "session_id": "checkout-flow-91bc0a",
  "base_url": "http://localhost:4001",
  "routes": {
    "api": {
      "url": "http://localhost:4001/api/",
      "path": "/api/",
      "kind": "api"
    },
    "ui": {
      "url": "http://localhost:4001/ui/",
      "path": "/ui/",
      "kind": "frontend"
    }
  }
}
```

Add a lightweight health endpoint:

```text
GET /runtime/health
```

returns:

```json
{
  "status": "ok",
  "session_id": "...",
  "base_url": "http://localhost:4001"
}
```

Why this matters:

* Humans opening the root know where to go.
* Agents can discover routes without scraping terminal output.
* Caddy startup can health-check the runtime base URL.
* `scenery ps --json` and `GET /runtime/routes` become mutually reinforcing.

Acceptance for this milestone:

* HTML page test includes app name, worktree label, app root, and route links.
* JSON route endpoint has stable schema/version.
* Health endpoint works through Caddy local path mode.
* Root page does not require frontend build assets.

### Milestone 6: Update `scenery ps` and `scenery ps --json`

Modify:

```text
cmd/scenery/agent_status_table.go
cmd/scenery/agent.go
cmd/scenery/agent_test.go
```

Current table:

```text
APP ROOT    STATUS    CONSOLE    UPDATED
```

New preferred human table:

```text
APP   WORKTREE              STATUS    URL                    SERVICES          UPDATED
onlv  main-a83f12           running   http://localhost:4001  api,ui,blog,pulse now
onlv  checkout-flow-91bc0a  running   http://localhost:4002  api,ui,blog,pulse now
```

Rules:

* `APP` = base app ID or config name.
* `WORKTREE` = stable user-facing worktree label derived from branch/basename/session ID.
* `URL` = path-mode base URL, or host-mode console URL if running in host mode.
* `SERVICES` = sorted route keys excluding internal-only entries unless `--verbose` exists later.
* Keep `APP ROOT` out of the default table to reduce width; include it in JSON.
* If route mode is host, display existing console URL and route mode if needed.

Update JSON shape:

```json
{
  "schema_version": "scenery.agent.status.v2",
  "agent": { "...": "..." },
  "sessions": [
    {
      "schema_version": "scenery.dev.session.v1",
      "session_id": "checkout-flow-91bc0a",
      "base_app_id": "onlv",
      "app_root": "/Users/pbrazdil/dev/onlv-checkout-flow",
      "worktree": "checkout-flow-91bc0a",
      "status": "running",
      "route_manifest": {
        "mode": "path",
        "base_url": "http://localhost:4002",
        "routes": {
          "root": { "url": "http://localhost:4002/" },
          "api": { "url": "http://localhost:4002/api/" },
          "ui": { "url": "http://localhost:4002/ui/" }
        }
      }
    }
  ]
}
```

Compatibility:

* Either bump the status schema to `scenery.agent.status.v2`, or keep v1 and add fields additively.
* Prefer additive if downstream tools are known to tolerate additional fields.
* If changing semantics substantially, bump and update docs/schemas/tests.

Acceptance for this milestone:

* Existing `scenery ps` tests updated.
* New table test covers path mode.
* JSON test covers base URL and route manifest.
* Host-mode test still passes.

### Milestone 7: Path-mode dev env injection

Path mode only works well if app backends and frontends know their public URLs.

Modify the dev runtime environment assembly code that launches:

* API process
* frontend dev servers
* managed dev services
* generated client/config endpoints

Likely target areas to inspect during implementation:

```text
cmd/scenery/dev_*.go
cmd/scenery/dev_frontends*.go
cmd/scenery/watch*.go
internal/devdash/*
```

Desired env values for local path mode:

```text
SCENERY_ROUTE_MODE=path
SCENERY_BASE_URL=http://localhost:4001
SCENERY_API_URL=http://localhost:4001/api/
SCENERY_API_BASE_PATH=/api/
SCENERY_PUBLIC_APP_URL=http://localhost:4001/
SCENERY_FRONTEND_BASE_PATH=/ui/       # per frontend process
SCENERY_FRONTEND_PUBLIC_URL=http://localhost:4001/ui/
```

But do not add public env knobs as the main external contract if config fields already exist. The repo rule is to avoid new environment-variable knobs by default. Existing app-config env names can be populated where the app declares them.

For ONLV specifically, populate configured names:

```text
PublicAppURL=http://localhost:4001/
APIBaseURL=http://localhost:4001/api/
AuthCookieDomain=
```

Do not set `Domain=localhost` cookies. In path-mode local dev, host-only cookies are usually the right behavior.

Frontend concerns:

Many dev servers assume they are mounted at `/`. Path mode requires one of:

1. The frontend dev server supports a base path and Scenery injects it.
2. The Scenery frontend runtime wrapper rewrites base paths for generated Scenery frontend packages.
3. Scenery proxies with prefix stripping and relies on relative assets.

Avoid HTML rewriting as a default strategy. It is fragile.

For each frontend backend, Scenery should set:

```text
BASE_PATH=/ui/
PUBLIC_URL=http://localhost:4001/ui/
```

using app-specific env names if configured, plus Scenery-owned names for generated packages.

Acceptance for this milestone:

* API sees correct external base URL.
* Generated runtime config reports path-mode API URL.
* ONLV frontend can call same-origin `/api/` or configured absolute API URL.
* Auth login/callback URL resolves under `/api/`.
* Cookies are host-only in local path mode unless app config explicitly opts out.
* No new unregistered env vars are added without docs/environment registry updates.

### Milestone 8: Config and schema support

Update app config schema and docs.

Candidate config:

```json
{
  "dev": {
    "routing": {
      "mode": "path",
      "port_range": {
        "start": 4001,
        "end": 4999
      },
      "root": "scenery-console"
    }
  },
  "deploy": {
    "routing": {
      "mode": "path",
      "domain": "app.example.com",
      "root": "ui",
      "routes": {
        "api": "/api/",
        "ui": "/",
        "blog": "/blog/",
        "pulse": "/pulse/"
      }
    }
  }
}
```

Current contract note:

App configs no longer carry proxy hosts. Frontend roots live under top-level `frontends`; local URLs come from the dev runtime route manifest.

Defaulting:

* If `dev.routing.mode` is absent, use `path`.
* Default local dev stays path mode.
* `dev.routing.mode = "host"` uses the default `local.dev` edge path.

CLI overrides:

```text
scenery up --routing path
scenery up --routing host
scenery up --port 4001
scenery up --port-range 4001-4999
```

Be conservative with CLI surface. Add only what implementation actually supports and document it.

Schema updates:

```text
docs/schemas/scenery.config.v1.schema.json
```

Potential additional schemas:

```text
docs/schemas/scenery.agent.status.v2.schema.json
docs/schemas/scenery.local.routes.v1.schema.json
```

Docs updates:

```text
docs/local-contract.md
docs/agent-guide.md
SKILL.md
README.md
docs/app-development-cookbook.md
docs/environment.md
docs/environment.registry.json
docs/knowledge.json
```

The repo explicitly requires updating all affected docs and JSON contracts when behavior changes.

Acceptance for this milestone:

* Config schema accepts path routing config.
* Existing ONLV config remains valid.
* `scenery check --json` reports route conflicts.
* Docs describe default local path routing.
* Docs describe optional host/domain routing and dnsmasq as no longer required for default local dev.

### Milestone 9: `scenery up`, `scenery down`, restart, and prune lifecycle

Integrate path routing with the dev runtime lifecycle.

`scenery up` should:

1. Discover app root/config.
2. Compute session ID/worktree label.
3. Start or connect to agent.
4. Allocate port.
5. Start per-runtime Caddy listener.
6. Start app process and frontends.
7. Register session with `RouteManifest`.
8. Print base URL and service links.
9. If `--json`, emit machine-readable route manifest.

Example human output:

```text
scenery up running onlv checkout-flow-91bc0a
URL: http://localhost:4001

Services:
  console  http://localhost:4001/
  api      http://localhost:4001/api/
  ui       http://localhost:4001/ui/
  blog     http://localhost:4001/blog/
  pulse    http://localhost:4001/pulse/
```

Example JSON:

```json
{
  "schema_version": "scenery.up.v2",
  "app": "onlv",
  "worktree": "checkout-flow-91bc0a",
  "session_id": "checkout-flow-91bc0a",
  "base_url": "http://localhost:4001",
  "routes": {
    "api": "http://localhost:4001/api/",
    "ui": "http://localhost:4001/ui/"
  }
}
```

`scenery down` should:

1. Stop app/frontend processes.
2. Stop per-runtime Caddy listener if owned by this session.
3. Release or mark port lease stale/available.
4. Preserve unrelated Caddy/listener processes.
5. Continue existing DB/storage cleanup semantics.

Existing down flow already resolves a session by app root, stops session-owned processes, deletes a session record, and optionally cleans DB/state. Integrate local Caddy and port lease cleanup there.

`scenery prune` should:

* Remove stale sessions.
* Remove stale port leases.
* Remove stale local Caddy state under removed session state roots.
* Never delete active leases.

Acceptance:

* `scenery up` twice in same app root reuses the same base URL if possible.
* Two worktrees get two different ports.
* `scenery down` frees only the correct runtime.
* `scenery prune --older-than ...` removes stale path-mode runtime state.
* Detached mode works.

### Milestone 10: Host/domain compatibility and optional nice URLs

Do not remove host/domain routing in this plan.

Keep existing behavior available through:

```text
dev.routing.mode = "host"
```

or:

```text
scenery up --routing host
```

The old edge DNS commands can remain:

```text
scenery system edge dns install
scenery system edge dns status
scenery system edge dns uninstall
```

But default local dev should not require them.

Update `scenery system edge status` messaging:

* If path mode is default and working, DNS missing should not make the whole local dev story look broken.
* DNS status should be scoped to host/domain mode.
* Error text should say DNS is required only for host/domain local routes, not for default localhost path routes.

Optional future nicety:

Support `*.localhost` aliases if cross-platform tests prove it. This is not part of v1 acceptance. Do not make it the default contract.

Acceptance:

* Host-mode routing tests still pass.
* DNS install/status tests still pass.
* `scenery up` path mode succeeds without dnsmasq.
* `scenery system edge status` no longer implies dnsmasq is required for default local dev.

### Milestone 11: Production route manifest skeleton

This milestone may be implemented partially in this ExecPlan and completed in a later deployment ExecPlan if production deployment is not yet ready.

Add an internal route manifest shape that can serve both dev and production.

Dev example:

```json
{
  "environment": "dev",
  "base_url": "http://localhost:4001",
  "root": "scenery-console",
  "routes": {
    "api": "/api/",
    "ui": "/ui/",
    "blog": "/blog/"
  }
}
```

Production example:

```json
{
  "environment": "production",
  "base_url": "https://app.example.com",
  "root": "ui",
  "routes": {
    "ui": "/",
    "api": "/api/",
    "blog": "/blog/",
    "pulse": "/pulse/"
  }
}
```

Production Caddy generation should eventually emit:

```caddyfile
app.example.com {
    handle_path /api/* {
        reverse_proxy <api-upstream>
    }

    handle_path /blog/* {
        reverse_proxy <blog-upstream>
    }

    handle_path /pulse/* {
        reverse_proxy <pulse-upstream>
    }

    handle {
        reverse_proxy <ui-upstream>
    }
}
```

Important production differences:

* Root `/` maps to configured app service, not Scenery console.
* TLS/domain is real operator-managed or Caddy-managed production TLS.
* Ports/domains are configurable.
* Local Scenery debug endpoints such as `/runtime` must not be public unless explicitly enabled and secured.
* Auth cookie behavior differs from local dev and must respect production domain settings.

Acceptance for this milestone if included:

* Route manifest model supports dev and prod root policies.
* Docs clearly mark production deployment support as planned/partial unless fully implemented.
* No insecure `/runtime` production exposure is introduced.

## Plan of Work

### Phase A: Preparation and source inventory

1. Read root instructions:

   * `AGENTS.md`
   * `PLANS.md`
   * `docs/local-contract.md`
   * `docs/agent-guide.md`
   * `docs/plans/active.md`
   * `docs/tech-debt.md`
2. Run:

   ```sh
   scenery inspect docs --json
   ```

   If the command cannot run, record the reason in this plan.
3. Search for child `AGENTS.md` files in directories to touch:

   ```sh
   find cmd internal docs testdata -name AGENTS.md -print
   ```
4. Inventory relevant code:

   ```sh
   rg "RouteNamespace|RouteManifest|Routes|Backends|RouteDashboard|RouteAPI" internal cmd
   rg "writeStatusTable|scenery ps|statusCommand" cmd
   rg "caddyEdgeConfig|dnsmasq|edge dns|system edge" cmd
   rg "frontends|APIBaseURL|PublicAppURL|AuthCookieDomain" cmd internal docs
   ```

Do not edit until the exact source layout is confirmed.

### Phase B: Data-model and tests first

1. Add route-mode and route-manifest types.
2. Add port lease types and pure functions.
3. Write unit tests for:

   * route path generation,
   * route prefix normalization,
   * route conflict detection,
   * port allocation,
   * stale lease behavior.
4. Make tests fail for the intended reasons before wiring runtime code.

### Phase C: Router implementation

1. Add trusted local path request detection.
2. Add path dispatch.
3. Add prefix stripping.
4. Add discovery/health endpoints.
5. Preserve host routing tests.

### Phase D: Caddy local listener

1. Add Caddy config generator.
2. Add process startup/stop state.
3. Integrate with `scenery up`.
4. Integrate cleanup with `scenery down`.
5. Add fake-process or fake-caddy tests.

### Phase E: CLI output and docs

1. Update `scenery ps`.
2. Update `scenery ps --json`.
3. Update `scenery up --json` if applicable.
4. Update docs/schemas/harness expectations.
5. Add cookbook examples.

### Phase F: ONLV validation

Use ONLV as the real integration target because it has multiple frontends and auth URL env names.

Expected local route shape:

```text
http://localhost:4001/
http://localhost:4001/api/
http://localhost:4001/ui/
http://localhost:4001/blog/
http://localhost:4001/pulse/
```

ONLV config currently has three frontend names: `pulse`, `blog`, and `ui`; those are a good path-mode test fixture.

## Concrete Steps

### Step 1: Create the ExecPlan file and register it

Create:

```text
docs/plans/0090-local-path-routing.md
```

Add a link in `docs/plans/active.md`:

```md
- [0090 Local Path Routing and Per-Runtime Dev Ports](0090-local-path-routing.md)
  - Status: active
  - Owner: scenery runtime / agent DX
  - Created: 2026-06-27
  - Focus: make localhost path routing with one automatic port per dev runtime the default local routing mode, keep Caddy, and make dnsmasq/domain routing optional.
```

Update `docs/knowledge.json` if active plans are indexed there.

### Step 2: Add route-mode types

Edit:

```text
internal/agent/types.go
```

Add:

```go
type RouteMode string

const (
    RouteModeHost RouteMode = "host"
    RouteModePath RouteMode = "path"
)

type RouteManifest struct {
    SchemaVersion string                 `json:"schema_version"`
    Mode          RouteMode              `json:"mode"`
    BaseURL       string                 `json:"base_url"`
    Root          string                 `json:"root"`
    Worktree      string                 `json:"worktree,omitempty"`
    Routes        map[string]RouteRecord `json:"routes"`
    PortLease     *PortLease             `json:"port_lease,omitempty"`
}

type RouteRecord struct {
    Name        string `json:"name"`
    Kind        string `json:"kind"`
    URL         string `json:"url"`
    Path        string `json:"path,omitempty"`
    StripPrefix string `json:"strip_prefix,omitempty"`
    Backend     string `json:"backend,omitempty"`
}
```

Extend `Session`:

```go
RouteManifest RouteManifest `json:"route_manifest,omitempty"`
```

Add tests in:

```text
internal/agent/session_test.go
```

or new:

```text
internal/agent/routes_test.go
```

### Step 3: Add path route generation

Edit:

```text
internal/agent/session.go
```

Add:

```go
func pathRouteManifestForSession(sessionID, baseURL string, backends map[string]Backend, namespace RouteNamespace) RouteManifest
```

Route generation rules:

* Always include `root`.
* Include `api` only if API backend exists.
* Include `console` or `dashboard`.
* Include one route per frontend backend.
* Sort route keys only for deterministic output in tests; maps remain maps in JSON.
* Normalize path prefixes to leading and trailing slash for URLs:

  * path field may be `/api/`
  * strip prefix may be `/api`

Keep existing `routesForSession` for host mode.

Potential transitional helper:

```go
func routesForPathManifest(manifest RouteManifest) map[string]string
```

so legacy `session.Routes` still gets string URLs.

### Step 4: Add port lease manager

Create:

```text
internal/agent/ports.go
internal/agent/ports_test.go
```

or a runtime package if better after source inventory.

Functions:

```go
func PreferredPort(appRoot string, start, end int) (int, error)
func AllocatePortLease(path string, req PortLeaseRequest) (PortLease, error)
func LoadPortLeases(path string) (PortLeaseFile, error)
func SavePortLeases(path string, file PortLeaseFile) error
func PruneStalePortLeases(path string) error
```

Use standard library only.

Important tests:

```go
func TestPreferredPortStableForAppRoot(t *testing.T)
func TestPreferredPortDifferentForDifferentWorktrees(t *testing.T)
func TestAllocatePortLeaseReusesExistingLiveLease(t *testing.T)
func TestAllocatePortLeaseSkipsOccupiedUnownedPort(t *testing.T)
func TestAllocatePortLeaseReclaimsStaleOwnedLease(t *testing.T)
func TestAllocatePortLeaseFailsWhenRangeExhausted(t *testing.T)
```

### Step 5: Add Caddy local path config generation

Create or extend:

```text
cmd/scenery/local_caddy.go
cmd/scenery/local_caddy_test.go
```

Types:

```go
type localCaddyOptions struct {
    ListenAddr  string
    PublicHost  string
    PublicPort  int
    BaseURL     string
    Upstream    string
    AdminSocket string
    SessionID   string
    Token       string
}
```

Function:

```go
func caddyLocalPathConfig(opts localCaddyOptions) string
```

Test exact important fragments, not necessarily full Caddyfile string:

* contains `http://127.0.0.1:4001`
* contains `http://localhost:4001`
* reverse-proxies to agent upstream
* sets `X-Scenery-Session`
* sets `X-Scenery-Base-URL`
* sets `X-Forwarded-Port`
* sets token header
* disables automatic HTTPS for localhost HTTP mode

### Step 6: Start/stop local Caddy with session ownership

Add functions:

```go
func startLocalPathCaddy(...)
func stopLocalPathCaddy(...)
func loadLocalCaddyState(...)
func writeLocalCaddyState(...)
```

Use existing process owner/fingerprint helpers rather than raw PID-only checks.

State under session root:

```text
.scenery/sessions/<session-id>/local-caddy/
```

or corresponding generated state root from `localagent.StateRoot(appRoot, sessionID)`.

Do not place generated state into tracked files.

### Step 7: Wire `scenery up`

Find the supervisor startup flow. During source inventory, locate where `RegisterRequest` is built and sent to the agent.

Change registration to include:

* path-mode route manifest,
* base URL,
* worktree label,
* backends as before.

If current `RegisterRequest` does not carry route manifest, extend it:

```go
type RegisterRequest struct {
    ...
    RouteManifest RouteManifest `json:"route_manifest,omitempty"`
}
```

Update `NewSession`:

* If request has path-mode route manifest, use it.
* Else preserve existing host-mode route generation.

Be careful with existing host-mode route namespace reuse. Current `NewSession` preserves an existing route namespace when the request omits it.  Preserve that behavior for host mode.

### Step 8: Path router dispatch

Edit:

```text
internal/agent/router.go
```

Add:

```go
func (s *Server) handlePathModeRoute(w http.ResponseWriter, req *http.Request, session Session)
func routeForPath(manifest RouteManifest, requestPath string) (RouteRecord, bool)
func matchRoutePrefix(record RouteRecord, requestPath string) bool
```

Route matching:

* Exact `/` -> root.
* Longest prefix wins.
* Prefix `/api/` matches `/api` and `/api/...`.
* Prefix `/ui/` matches `/ui` and `/ui/...`.
* Internal `/runtime/*` handled before frontend fallback.
* Unknown path returns 404.

Proxying:

```go
s.proxyBackendWithOptions(w, req, backend, proxyBackendOptions{
    stripPrefix: strings.TrimSuffix(record.Path, "/"),
    spaFallback: isFrontendSessionBackend(record.Backend) && shouldUseSPAFallback(req),
})
```

But do not accidentally strip `/` for root route.

### Step 9: Add discovery endpoints

In path mode, before proxy dispatch:

```text
GET /runtime/health
GET /runtime/routes
GET /runtime/config
```

Existing `/__scenery/config` for frontend currently proxies to API backend in frontend routes. Decide whether path-mode `/runtime/config` should:

* proxy to API backend exactly as today, or
* be served by the agent with route manifest.

Initial recommendation:

* Keep compatibility behavior if generated frontend code expects the API response.
* Add `/runtime/routes` as the agent-owned route manifest endpoint.

### Step 10: Update `scenery ps`

Edit:

```text
cmd/scenery/agent_status_table.go
```

Add helper functions:

```go
func statusSessionApp(session localagent.Session) string
func statusSessionWorktree(session localagent.Session) string
func statusSessionBaseURL(session localagent.Session) string
func statusSessionServices(session localagent.Session) string
```

If `session.RouteManifest.Mode == "path"`:

* base URL = `session.RouteManifest.BaseURL`
* services = keys from manifest routes excluding `root` and maybe internal entries

If host mode:

* base URL = dashboard/console route as before
* services = route keys from `session.Routes`

Update tests.

### Step 11: Update config parsing and schema

Find config types and schema definitions. Add:

```go
type DevRoutingConfig struct {
    Mode      string          `json:"mode,omitempty"`
    PortRange PortRangeConfig `json:"port_range,omitempty"`
    Root      string          `json:"root,omitempty"`
}
```

Likely under app config package.

Validation:

* mode must be empty, `path`, or `host`
* port range start/end valid
* port range does not include privileged ports by default
* root route valid
* frontend route names cannot collide with reserved routes

Update:

```text
docs/schemas/scenery.config.v1.schema.json
```

### Step 12: Add route conflict checks

In `scenery check`, add diagnostics for:

* frontend named `api`
* frontend named `sync`
* frontend named `console`
* frontend named `dashboard`
* frontend named `runtime` or `__scenery`
* two names that sanitize to same route
* configured route path missing leading slash
* production root route missing backend

Diagnostic examples:

```text
dev.routing route conflict: frontend "api" collides with reserved path /api/
dev.routing route conflict: frontends "my-ui" and "my_ui" both sanitize to "my-ui"
```

### Step 13: Update docs

Update:

```text
docs/local-contract.md
```

Add local dev route contract:

```text
Default local dev routing is path mode.
A live dev runtime has one base URL.
Services are exposed under stable path prefixes.
dnsmasq/domain routing is optional.
```

Update:

```text
docs/agent-guide.md
```

Agent fast path:

```sh
scenery up --detach
scenery ps --json
```

Tell agents:

* read `route_manifest.base_url`
* use `route_manifest.routes.api.url` for API
* do not infer API port from process logs
* root URL is a service launcher

Update:

```text
SKILL.md
README.md
docs/app-development-cookbook.md
docs/environment.md
docs/environment.registry.json
docs/knowledge.json
```

Only update env docs if new Scenery-owned env names are added.

### Step 14: Add fixture coverage

Add or update a test fixture app with:

* API backend
* at least two frontends
* one reserved route conflict negative case
* auth URL config if feasible

Potential fixture names:

```text
testdata/apps/path-routing
testdata/apps/path-routing-conflict
```

Tests should validate:

```sh
go test ./cmd/scenery -run PathRouting
go test ./internal/agent -run PathRouting
```

If the fixture can be run by harness:

```sh
go run ./cmd/scenery check --app-root testdata/apps/path-routing --json
```

### Step 15: ONLV manual validation recipe

From an ONLV worktree:

```sh
scenery check --json
scenery up --detach --json
scenery ps --json
```

Expected:

* `route_manifest.mode == "path"`
* base URL is `http://localhost:<port>`
* API route is `<base>/api/`
* `ui`, `blog`, and `pulse` routes exist
* no dnsmasq install required

Manual smoke checks:

```sh
curl -fsS http://localhost:<port>/runtime/health
curl -fsS http://localhost:<port>/runtime/routes
curl -I http://localhost:<port>/api/
curl -I http://localhost:<port>/ui/
curl -I http://localhost:<port>/blog/
curl -I http://localhost:<port>/pulse/
```

Browser smoke:

* Open root base URL.
* Click API/config route if exposed.
* Open UI frontend.
* Trigger auth login or dev bootstrap flow.
* Verify frontend can call API without CORS issues.
* Verify cookies are host-only or otherwise correct for localhost.

## Validation and Acceptance

### Required static/unit validation

Run from repository root:

```sh
go test ./internal/agent
go test ./cmd/scenery
go test ./...
```

For substantial changes, repo instructions also expect:

```sh
scenery harness self --summary --write
```

The root instructions explicitly list `go test ./...`, `go test ./cmd/scenery`, and self-harness for substantial scenery repo changes.

If dashboard UI code changes, also run:

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
```

The repo has specific dashboard validation commands for UI changes.

### Required CLI behavior

Path mode must work without DNS setup:

```sh
scenery system edge dns status
scenery up --detach --json
scenery ps --json
```

Even if DNS status is missing/stopped, `scenery up` path mode should succeed.

`scenery ps` human output should show:

```text
APP   WORKTREE   STATUS   URL   SERVICES   UPDATED
```

`scenery ps --json` should expose:

```text
sessions[].route_manifest.mode
sessions[].route_manifest.base_url
sessions[].route_manifest.routes.api.url
```

### Required runtime behavior

With one app root:

```text
http://localhost:4001/
http://localhost:4001/api/
http://localhost:4001/<frontend>/
```

With two worktrees:

```text
worktree A -> http://localhost:4001/api/
worktree B -> http://localhost:4002/api/
```

Both can run simultaneously.

The root page must show:

* app name,
* worktree label,
* app root,
* route links.

The JSON discovery endpoint must show equivalent machine-readable data.

### Required safety behavior

* A client cannot spoof `X-Scenery-Session` without the Scenery token/trusted local Caddy path.
* An occupied unrelated port is skipped, not killed.
* A stale Scenery-owned Caddy listener can be cleaned only after owner verification fails.
* `scenery down` stops only the current session's Caddy/listener.
* Host/domain mode remains available.

### Required ONLV acceptance

With ONLV config, path mode must expose at least:

```text
/api/
/ui/
/blog/
/pulse/
```

The existing ONLV auth URL env names must receive path-mode-compatible values if Scenery currently injects those env names. ONLV declares `PublicAppURL`, `APIBaseURL`, and `AuthCookieDomain`, so local path mode must not leave those stale as domain URLs when default routing is path mode.

### Required docs acceptance

Update every affected contract layer:

* `docs/local-contract.md`
* `docs/agent-guide.md`
* `SKILL.md`
* `README.md`
* `docs/app-development-cookbook.md`
* schemas under `docs/schemas/`
* `docs/knowledge.json`
* `docs/plans/active.md`

If an expected docs command cannot run, record why.

## Idempotence and Recovery

Port leases must be safe to retry.

If `scenery up` fails after allocating a port but before starting Caddy:

* keep the lease only if it has a live verified owner,
* otherwise mark it stale/reclaimable,
* next `scenery up` should retry cleanly.

If Caddy starts but session registration fails:

* stop the just-started Caddy process,
* remove or stale-mark the port lease,
* return an actionable error.

If session registration succeeds but app process startup fails:

* preserve enough route/runtime state for logs/debug,
* status should show degraded,
* `scenery down` should still clean Caddy and lease.

If `scenery down` fails halfway:

* it should be safe to rerun.
* It must not fail just because Caddy is already gone.
* It must not fail just because the lease file is missing.
* It must not remove another active worktree's lease.

If a user manually kills Caddy:

* `scenery ps` should mark the runtime degraded or stale.
* `scenery up` in that app root should restart Caddy and reuse the same port if available.

If a user manually occupies the old port with another process:

* next `scenery up` should allocate a new port and report the reason.
* old stale lease should not cause an infinite failure loop.

If dnsmasq is missing or broken:

* path mode must continue working.
* `scenery system edge dns status` may show missing/stopped, but default local dev should not fail because of that.

Rollback strategy:

* Set `dev.routing.mode = "host"` or run `scenery up --routing host` to use existing host/domain behavior.
* Keep all old host-route functions and tests until path mode has been stable for at least one release cycle.
* Do not delete `system edge dns` commands in this plan.

## Artifacts and Notes

Expected new or changed artifacts:

```text
docs/plans/0090-local-path-routing.md
docs/plans/active.md
docs/local-contract.md
docs/agent-guide.md
SKILL.md
README.md
docs/app-development-cookbook.md
docs/schemas/scenery.config.v1.schema.json
docs/knowledge.json
internal/agent/types.go
internal/agent/session.go
internal/agent/router.go
internal/agent/ports.go
internal/agent/*_test.go
cmd/scenery/local_caddy.go
cmd/scenery/local_caddy_test.go
cmd/scenery/agent_status_table.go
cmd/scenery/agent.go
cmd/scenery/*dev* files as discovered
testdata/apps/path-routing/*
testdata/apps/path-routing-conflict/*
```

Expected runtime state artifacts, not committed:

```text
<scenery-home>/run/dev-ports.json
<session-state-root>/local-caddy/Caddyfile
<session-state-root>/local-caddy/caddy.log
<session-state-root>/local-caddy/admin.sock
<session-state-root>/local-caddy.json
```

Terminology:

* Runtime base URL: the one public local URL assigned to a live dev runtime, for example `http://localhost:4001`.
* Path mode: local routing mode where services are exposed below the runtime base URL by path prefix.
* Host mode: existing routing mode where services are exposed by hostname/domain.
* Worktree label: stable human label for a live app root, derived from branch/worktree name plus app-root hash/session ID.
* Route manifest: machine-readable map from service names to public URLs, paths, prefixes, and backend names.
* Port lease: persisted ownership record for a localhost port assigned to one dev runtime.

## Interfaces and Dependencies

### CLI interfaces

Potential changed/new CLI flags:

```text
scenery up --routing path
scenery up --routing host
scenery up --port 4001
scenery up --port-range 4001-4999
```

Do not add these flags until implementation actually supports them. If default path mode is sufficient, the first PR can omit manual override flags and rely on config.

Existing CLI surfaces affected:

```text
scenery up [--json]
scenery up --detach [--json]
scenery ps
scenery ps --json
scenery down [--json]
scenery prune
scenery system edge status
scenery system edge dns status
scenery check --json
scenery inspect routes --json
```

### JSON interfaces

Potential new schemas:

```text
scenery.local.routes.v1
scenery.dev.port_lease.v1
scenery.agent.status.v2
```

Potential additive session fields:

```json
{
  "worktree": "checkout-flow-91bc0a",
  "route_manifest": {
    "schema_version": "scenery.route_manifest.v1",
    "mode": "path",
    "base_url": "http://localhost:4001",
    "root": "scenery-console",
    "routes": {}
  }
}
```

### Config interfaces

Potential config addition:

```json
{
  "dev": {
    "routing": {
      "mode": "path",
      "port_range": {
        "start": 4001,
        "end": 4999
      },
      "root": "scenery-console"
    }
  }
}
```

Potential production addition:

```json
{
  "deploy": {
    "routing": {
      "mode": "path",
      "domain": "app.example.com",
      "root": "ui",
      "routes": {
        "ui": "/",
        "api": "/api/"
      }
    }
  }
}
```

### HTTP interfaces

Local path mode:

```text
GET  /
GET  /runtime/health
GET  /runtime/routes
ANY  /api/*
ANY  /sync/*
ANY  /console/*
ANY  /<frontend>/*
```

Forwarded headers:

```text
X-Forwarded-Host
X-Forwarded-Proto
X-Forwarded-Port
X-Forwarded-Prefix
X-Scenery-Route-Prefix
X-Scenery-Base-URL
X-Scenery-Public-URL
```

Trusted internal headers from Caddy to agent:

```text
X-Scenery-Session
X-Scenery-Local-Route-Mode
X-Scenery-Edge-Token
```

Security requirement: the agent must not trust these internal headers unless the request is loopback and token-authenticated.

### Dependencies

Use existing dependencies and standard library. Do not add new dependencies unless there is a clear reason recorded in the Decision Log.

Caddy remains managed by Scenery's existing toolchain resolution path. Existing code resolves a managed Caddy binary rather than using arbitrary `PATH` binaries, so local path Caddy should follow the same policy.

dnsmasq becomes optional for default local dev. Existing DNS commands remain available for host/domain mode.

### Interaction with active plans

This plan may intersect with:

* Agent runtime operational hardening.
* Agent-first development control plane.
* Browser direct API routing.
* Runtime contracts.
* Toolchain manifest / managed tool store.

Before implementation, review those active plans for conflicting decisions. The active plan index shows several runtime/agent DX plans are currently open.

```
