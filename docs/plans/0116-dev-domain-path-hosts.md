# 0116 Dev Domain Hosts for Path-Mode Routing

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

Amended by docs/plans/0117-public-dev-domain-exposure.md (2026-07-15): the
host join changed from `<branch>.<domain>` to `<branch>-<domain>` for the
Cloudflare-fronted topology, and exposure/frontend-serve configuration was
added there. Host examples below reflect the original dot join.

## Purpose / Big Picture

Give path-mode local dev sessions a stable, branded browser origin derived from
the app's own domain and the current Git branch, instead of only
`http://localhost:<port>`. With `dev.routing.domain` set to `local.clean.tech`
in an app's `.scenery.json`:

- `scenery up` on branch `main` serves the app at `https://local.clean.tech/`.
- `scenery up` on branch `pricing` (typically in a Git worktree) serves
  `https://pricing.local.clean.tech/`.
- Under that origin the URL structure is exactly today's path mode: `/` is the
  Scenery route index, `/api/` proxies the app API, `/console/` serves the
  dashboard, `/<frontend>/` proxies each configured frontend, `/__runtime` is
  Scenery-owned.

DNS is user-owned: the app owner points a public wildcard (`local.clean.tech`
and `*.local.clean.tech`) at `127.0.0.1` (for Cloudflare this must be DNS-only,
not proxied). No managed dnsmasq is required. HTTPS termination reuses the
existing managed edge: the privileged loopback helper on `127.0.0.1:443`
forwards to user-owned Caddy, which proxies to the agent router with on-demand
TLS from the Scenery local CA.

Driving app: `github.com/pbrazdil/onlv` (`.scenery.json` name `clean-tech`,
frontends `pulse`, `blog`, `ui`, `next`, `deploy.domain` `local.clean.tech`).

## Progress

- [x] (2026-07-15) Design agreed with app owner: path-mode URL structure kept,
  branch-name labels, apex on `main`, public wildcard DNS to `127.0.0.1`.
- [x] (2026-07-15) Code walk: identified `publicRouteManifest` /
  `handlePublicPathRoute` (internal/agent/router.go) as the existing
  host→path-mode dispatch to mirror, and `rebuildRouteHostIndexLocked`
  (internal/agent/registry.go) as the host index to extend.
- [x] (2026-07-15) M1 config + host derivation + manifest field —
  `dev.routing.domain` in internal/app/root.go and the config schema,
  `DevDomainHost` in internal/agent/session.go, `domain_host`/`domain_url` on
  `RouteManifest` normalized for path mode only, host-mode + domain rejected
  in `devRoutingMode`.
- [x] (2026-07-15) M2 registry host index, single ownership, TLS allow —
  `claimDomainHostLocked` (conflict via `domain_host_conflict`, stale-owner
  reclaim, `--claim-aliases` force transfer), `RoutePathMode` sentinel in the
  host index, `/v1/tls/allow` covered by the same lookup.
- [x] (2026-07-15) M3 router dispatch — `devDomainRouteManifest` rebases the
  session manifest onto `https://<host>` and dispatches through
  `handlePathModeRoute`; full path-mode surface incl. `/runtime` and
  `/console` served on the domain origin.
- [x] (2026-07-15) M4 `scenery up` surfaces — `validateDevDomainURL` probe
  (component readiness without managed DNS + retrying HTTPS probe of
  `/runtime/health`), `App:`/`app_url` in run output and detach output,
  domain-first local path router redirect, conflict and not-ready warnings.
- [x] (2026-07-15) M5 docs and validation — local-contract, agent-guide,
  SKILL, README, cookbook recipe, AGENTS.md mental model, config schema;
  `go test ./...` green; new unit tests in
  internal/agent/dev_domain_routing_test.go and
  cmd/scenery/dev_domain_test.go. Self-harness run pending below.

## Surprises & Discoveries

- The dnsmasq/scoped-resolver path (`scenery system edge dns install
  --domain`) already supports custom domains, but it is unnecessary here
  because resolution comes from public DNS. Evidence: `edgeDNSConfigDomains`
  in cmd/scenery/edge.go and `dig +short foo.local.clean.tech` resolving via
  the public wildcard.
- With `deploy.domain` configured, the local path router already redirects
  non-local-only paths to `https://<deploy.domain>`
  (`localPathRouterRedirect`, cmd/scenery/local_path_router.go). For onlv this
  currently bounces local dev URLs to the deployed site; once the wildcard DNS
  points at `127.0.0.1` the same redirect lands on the local dev domain host.
- `local.clean.tech` and `*.local.clean.tech` currently resolve to Cloudflare
  (deployed onlv-209 app). The app owner explicitly chose to repoint the
  wildcard to `127.0.0.1`, accepting that the deployed site stops being
  reachable at that name.

## Decision Log

- 2026-07-15 (owner + agent): Keep path-mode URL structure under the domain
  host (`/api/`, `/next/`, ...) instead of host-per-route subdomains. Rationale:
  owner request ("it will work like it is now"), and a single-label wildcard
  (`*.local.clean.tech`) cannot resolve nested per-route hosts.
- 2026-07-15 (owner + agent): Worktree label is the sanitized Git branch name;
  branch `main` maps to the bare domain. Two live worktrees on the same branch
  conflict; the second keeps localhost-only URLs and reports the conflict.
- 2026-07-15 (owner + agent): DNS via public wildcard A records to `127.0.0.1`,
  user-owned. Scenery must not require managed dnsmasq for dev domain hosts.
- 2026-07-15 (agent): New config lives at `dev.routing.domain` and applies to
  path mode. It does not switch `dev.routing.mode`; host mode is unchanged.
  Rationale: repo rule against env knobs, config is app-scoped and syncs to
  every machine the app runs on.
- 2026-07-15 (agent): `scenery up` must not hard-fail when the edge stack is
  not ready. The same `.scenery.json` is synced to SSH deploy targets (onlv-209)
  where the local dev edge is absent; degrading to localhost URLs with an
  actionable warning keeps deploys working. This intentionally differs from
  host mode's fail-closed `requiresPortlessEdge` behavior because path mode
  always has a working localhost fallback.
- 2026-07-15 (agent): When a session has a dev domain host, the local path
  router redirect target becomes that session's own domain URL, taking
  precedence over the `deploy.domain` redirect. Rationale: redirecting a
  worktree's localhost URL to the apex would silently switch the browser to
  the main worktree's app.

## Outcomes & Retrospective

Completed 2026-07-15 in one pass. `dev.routing.domain` landed as specified:
path-mode sessions claim `https://<branch>.<domain>` (bare on `main`) as a
single-owner registered route host, the router rebases the path-mode manifest
onto that origin, `scenery up` advertises the URL only after an end-to-end
HTTPS probe and otherwise degrades to localhost with an actionable warning,
and conflicts surface as `domain_host_conflict`. Full `go test ./...` and
`scenery harness self --summary --write` green (the harness also flagged this
plan's initially missing living-document statement — fixed). Manual browser
acceptance on onlv still requires the operator steps: repoint the Cloudflare
wildcard to `127.0.0.1` (DNS-only) and run `scenery system edge install` +
`trust` once. Retrospective: the public deploy edge's
`handlePublicPathRoute`/`publicRouteManifest` precedent made the router work
small; the only genuinely new machinery was claim-time host ownership.

## Context and Orientation

Terms:

- Path mode: the default `dev.routing.mode`. Each `scenery up` session leases a
  localhost port (cmd/scenery/dev_ports.go) and runs a local path router
  (cmd/scenery/local_path_router.go) — a loopback HTTP listener that forwards
  every request to the agent router with `X-Scenery-Session`,
  `X-Scenery-Local-Route-Mode: path`, and the edge token. The agent router's
  `handlePathModeRoute` (internal/agent/router.go) dispatches by path using the
  session's `RouteManifest` (BaseURL `http://localhost:<port>`, route records
  for root/api/console/frontends).
- Agent router: HTTP server owned by the local agent. Host-based dispatch goes
  through `routeTargetForHost` → `Registry.RouteTargetForHost`, an exact-host
  index built by `rebuildRouteHostIndexLocked` (internal/agent/registry.go).
  Today that index only contains host-mode route records and alias leases;
  path-mode sessions are skipped.
- Managed edge: `scenery system edge install` runs a privileged helper on
  `127.0.0.1:443` forwarding TCP to user-owned Caddy, which proxies to the
  agent router over internal HTTP with `X-Scenery-Edge-Token`. Caddy issues
  on-demand TLS from the Scenery local CA; issuance is gated by the agent's
  `/v1/tls/allow`, which calls `tlsAllowedHost` — true for any registered
  route host whose session owner fingerprint verifies.
- Public deploy edge precedent: `handlePublicEdgeRoute` →
  `handlePublicPathRoute` + `publicRouteManifest` (internal/agent/router.go)
  already rebase a running path-mode session onto `https://<deploy.domain>`.
  The dev domain host feature mirrors this for local dev, keeping the full
  path-mode surface (route index, console, runtime) rather than the public
  edge's restricted one.

Key files:

- internal/app/root.go — `DevRoutingConfig` (add `Domain`).
- cmd/scenery/dev_session_controller.go — session preparation, port lease,
  registration, local path router start, redirect URL selection.
- cmd/scenery/watch.go — `routeNamespaceForConfig`, branch discovery helpers.
- internal/agent/session.go — `NewSession`, `normalizeRouteManifest`,
  `RouteManifest` normalization; internal/agent/types.go — manifest types.
- internal/agent/registry.go — `rebuildRouteHostIndexLocked`, alias lease
  ownership machinery to mirror for conflicts.
- internal/agent/router.go — `routerMux`, `handlePathModeRoute`,
  `publicRouteManifest` precedent.
- cmd/scenery/dev_edge_preflight.go — edge readiness checks and the HTTPS
  route probe (`defaultConfiguredEdgeRouteProbe`).
- docs/schemas/scenery.config.schema.json — `dev.routing` has
  `additionalProperties: false`, so the new field must be added there.
- docs/schemas/scenery.dev.detach.schema.json — detached result payload.

## Milestones

Each milestone keeps `go test ./...` green.

1. M1 Config and host derivation. `dev.routing.domain` parses from
   `.scenery.json`; `scenery up` computes the session's dev domain host
   (sanitized branch label + domain, apex on `main`, none when the branch is
   empty) and carries it on the path-mode `RouteManifest` as a new normalized
   field (working name `domain_host`).
2. M2 Registry ownership and TLS. `rebuildRouteHostIndexLocked` indexes
   path-mode sessions' `domain_host` with a path-mode route target; a second
   live verified owner of the same host keeps it and the newcomer records a
   conflict (stale owners are reclaimed after fingerprint verification, same
   rules as alias leases); `tlsAllowedHost` returns true for the host.
3. M3 Router dispatch. A trusted edge (or direct router) request whose Host is
   a registered dev domain host serves `handlePathModeRoute` with the
   session's manifest rebased to `https://<host>` (BaseURL and record URLs),
   preserving the full path-mode surface including `/console/` and
   `/__runtime`.
4. M4 `scenery up` surfaces. Console output and the detached
   `scenery.dev.detach` payload advertise the domain URL; when the edge stack
   is ready the domain URL is probed (reusing the retrying HTTPS probe) and
   printed as the primary base URL; when not ready, a one-line warning names
   `scenery system edge install` / `scenery system edge trust` and localhost
   stays primary. The local path router redirect targets the session's domain
   URL when one exists, else `deploy.domain` as today. Host conflicts print
   the owning app root, mirroring alias conflict reporting.
5. M5 Docs and validation. Update docs/local-contract.md, docs/agent-guide.md,
   SKILL.md, docs/app-development-cookbook.md (custom dev domain recipe with
   the DNS + edge setup steps), schemas, and this plan; run the validation
   matrix and `scenery harness self --summary --write`.

## Plan of Work

M1. Add `Domain string \`json:"domain"\`` to `DevRoutingConfig`
(internal/app/root.go) and to `dev.routing` in
docs/schemas/scenery.config.schema.json. In
cmd/scenery/dev_session_controller.go, after branch discovery, derive the dev
domain host: normalize the domain with the existing route-host normalizer,
sanitize the branch with the existing route-label sanitizer; label `main` (or
equal to the bare domain semantics) → host is the bare domain; empty label →
no host. Add `DomainHost` (and derived `DomainURL`, always `https://` + host)
to `RouteManifest` in internal/agent/types.go, normalized in
`normalizeRouteManifest` for path mode only (host-mode manifests must reject or
drop it). Unit tests: config decode, label/apex/empty-branch derivation, and
normalization (weird branch names, uppercase domains, schemes/ports pasted
into the config value).

M2. In internal/agent/registry.go, extend `rebuildRouteHostIndexLocked` to add
`hosts[domainHost] = routeTarget{SessionID, Route: <path sentinel>}` for
path-mode sessions. Define the sentinel in internal/agent (working name
`RoutePathMode = "__path"`); `normalizeAliasRoute` and route-name validation
must never accept it from external input. Ownership: at `Register` time, if
another session holds the same `DomainHost` and its owner fingerprint still
verifies, strip the host from the incoming session and record it in a new
`DomainHostConflict` field (session JSON), reusing `aliasLeaseOwnerStale`
verification semantics; otherwise the newcomer takes the host. Deterministic
index: when two persisted sessions still claim one host, prefer the verified
owner, then the most recently updated. Unit tests: index rebuild, conflict,
stale reclaim, `tlsAllowedHost`.

M3. In internal/agent/router.go `routerMux`, when host lookup returns the
path sentinel, load the session, require `RouteManifest.Mode == RouteModePath`
and `sessionOwnerVerifies`, rebase the manifest with a new
`devDomainRouteManifest(session, host)` — copy the session manifest, replace
BaseURL with `https://<host>`, rewrite each record URL onto that base (path
records keep Path/StripPrefix/Backend) — then call `handlePathModeRoute` with
`sessionWithRouteManifest`. `publicRouteManifest` stays untouched (public edge
requests are matched earlier in `routerMux` and remain restricted). Unit
tests: route index at `/`, `/api/` proxy, frontend proxy with SPA fallback,
`/console/`, `/__runtime/routes` reporting the domain base URL, 404 for
unregistered hosts, and owner-verification failure.

M4. In cmd/scenery/dev_session_controller.go: pass the manifest with
`DomainHost` at registration; select the local path router redirect target
(session domain URL first, else `https://<deploy.domain>`); after registration,
if the session has a domain host, check edge readiness with the existing
`edgeStatusForStateDomain`-based check but do NOT require managed DNS
readiness (public DNS is user-owned; treat the resolver functional check as
sufficient when it resolves to loopback) — when ready, probe
`https://<host>/__runtime/health` via `defaultConfiguredEdgeRouteProbe`; when
not ready, print the warning and continue. Update the dev console URL
rendering and the detached result writer (see
`TestWriteDetachedDevResultJSON` in cmd/scenery) plus
docs/schemas/scenery.dev.detach.schema.json with the domain URL and a
readiness flag. Surface conflicts in output the way alias conflicts are
surfaced today.

M5. Documentation: docs/local-contract.md (config grammar, URL shape,
ownership/conflict semantics, edge requirement and degraded behavior, DNS
ownership note including the Cloudflare DNS-only caveat), docs/agent-guide.md
(how agents read the domain URL from JSON output), SKILL.md (short note),
docs/app-development-cookbook.md (end-to-end recipe: config, Cloudflare
records, `scenery system edge install` + `trust`, worktree workflow),
README.md if it lists dev URLs. Update docs/plans/active.md (already done at
plan creation) and any schema-revision digests embedded in code (for example
`localRoutesResponse` in internal/agent/router.go if its payload gains the
domain base URL).

## Concrete Steps

Work from the repository root `/Users/petrbrazdil/Repos/scenery`.

1. `git checkout -b dev-domain-path-hosts` (or work on main per repo habit).
2. M1 edits, then `go test ./internal/app ./internal/agent ./cmd/scenery`.
3. M2 edits, then `go test ./internal/agent`.
4. M3 edits, then `go test ./internal/agent`.
5. M4 edits, then `go test ./cmd/scenery -run 'Dev|Detach|Edge|Route'` and
   `go test ./cmd/scenery`.
6. M5 docs + schemas, then the full validation set below.
7. Manual acceptance on onlv (owner machine): set `"routing": {"domain":
   "local.clean.tech"}` under `dev` in
   `/Users/petrbrazdil/Repos/onlv/.scenery.json`; ensure Cloudflare has
   DNS-only A records `local.clean.tech` → `127.0.0.1` and
   `*.local.clean.tech` → `127.0.0.1`; run `scenery system edge install` and
   `scenery system edge trust` once; then `scenery up` on main and in a
   worktree branch and load `https://local.clean.tech/` and
   `https://<branch>.local.clean.tech/` in a browser.

## Validation and Acceptance

Automated (must pass before completion):

    go test ./...
    go test ./cmd/scenery
    scenery harness self --summary --write

Do not run `go install ./cmd/scenery`; use the self-harness worktree-local
binary per root AGENTS.md.

Behavioral acceptance:

- A registered path-mode session with `dev.routing.domain` set answers, via a
  direct request to the agent router with `Host: <label>.<domain>` and the
  trusted edge token, with the same body as the localhost path router for
  `/`, `/api/...`, `/<frontend>/...`, and `/__runtime/routes`, and the
  `/__runtime/routes` payload reports `https://<label>.<domain>` as the base
  URL.
- On branch `main` the host is the bare domain.
- A second live session claiming the same host keeps localhost URLs and
  reports the conflict with the owning app root; after the first owner exits
  and its fingerprint no longer verifies, a re-registration takes the host.
- `curl -s "http://127.0.0.1:<router>/v1/tls/allow?domain=<host>"` returns
  allow for a registered host with a live verified owner.
- With no edge installed, `scenery up` still reaches ready and prints the
  warning naming `scenery system edge install`.

## Idempotence and Recovery

All milestones are additive and keep path mode's localhost behavior as the
fallback, so a partially landed feature degrades to today's behavior (no
domain host claimed, no output changes). Session registration is already
idempotent per app root; re-running `scenery up` re-derives the host from the
current branch. If a stale registry entry holds a host after a crash, the
existing owner-fingerprint staleness rules reclaim it on the next
registration. To abandon mid-way, revert the branch; no durable state format
changes are made (new fields are additive to session/manifest JSON, and
strict current decoding on upgraded binaries tolerates their absence).

## Artifacts and Notes

- Public DNS evidence (2026-07-15): `dig +short local.clean.tech A` →
  Cloudflare IPs (104.21.1.153, 172.67.129.115); wildcard resolves too. The
  owner will repoint these to `127.0.0.1` (DNS-only mode).
- onlv `.scenery.json` currently sets `deploy.domain: local.clean.tech`,
  `deploy.root: next`, frontends `pulse`, `blog`, `ui`, `next`.
- The local dev machine has no `~/.scenery/deploy` registry; `local.clean.tech`
  is not served locally today.

## Interfaces and Dependencies

- New config: `dev.routing.domain` (string, optional). Empty means no dev
  domain host; behavior is exactly today's.
- New session JSON fields (additive): `route_manifest.domain_host`,
  `route_manifest.domain_url`, `domain_host_conflict` (shape mirrors
  `alias_conflicts` entries). Update docs/schemas for the session/status and
  detach payloads that embed them.
- New internal route-target sentinel for path-mode host dispatch; never valid
  as a user-visible route name.
- Depends on existing managed edge (`scenery system edge install|trust`) for
  browser HTTPS; depends on user-owned public wildcard DNS. No new Go
  dependencies. No new environment variables.
