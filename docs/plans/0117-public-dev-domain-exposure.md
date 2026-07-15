# 0117 Public Dev Domain: Dash Hosts, Exposure Config, Frontend Serve Modes

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

## Purpose / Big Picture

Plan 0116 gave path-mode dev sessions a branded origin served by the loopback
edge with local-CA TLS. The app owner's real topology is different: Cloudflare
proxies the domain to a static home IP, the router port-forwards 80/443 to the
dev Mac, and Cloudflare terminates the browser-facing TLS. That changes three
things, all owner-decided on 2026-07-15:

1. Host shape becomes first-level: `https://<branch>-local.clean.tech`
   (`https://local.clean.tech` on `main`), because Cloudflare Universal SSL
   covers only `*.clean.tech` — second-level wildcards like
   `*.local.clean.tech` need paid ACM. The dot-join from 0116 is replaced, not
   kept as an alternative (one current behavior).
2. The domain origin is internet-reachable, so what it serves must be
   config-chosen, not hardcoded: `dev.routing.expose` lists the route names
   the domain serves (`api`, `console`, `runtime`, frontend names, `root`).
   Absent means everything — the owner explicitly wants no default blocking.
   `localhost:<port>` always keeps the full surface.
3. Frontends gain a serve mode: `frontends.<name>.serve` is `development`
   (today's dev server with HMR, default) or `production` (scenery runs the
   package's `build` script and serves the build output directory from an
   internal static file server on a hidden loopback port).

Driving app: `github.com/pbrazdil/onlv` (`local.clean.tech`, frontends
`pulse`, `blog`, `ui`, `next`).

## Progress

- [x] (2026-07-15) M1 dash host join — `DevDomainHost` joins `<label>-<domain>`,
  0116 tests/docs/schema updated, 0116 plan annotated.
- [x] (2026-07-15) M2 exposure — `dev.routing.expose` validated in
  `devExposeRouteNames` (cmd/scenery/dev_routing.go), carried as
  `route_manifest.public_routes`, filtered in the router's `RoutePathMode`
  branch with `/runtime*`+`/__scenery*` gating; localhost unfiltered. Tests in
  internal/agent/dev_domain_routing_test.go and cmd/scenery/dev_domain_test.go.
- [x] (2026-07-15) M3 production serve — `frontends.<name>.serve`,
  `managedFrontendBuildCommand`, in-process `staticFrontendServer`
  (cmd/scenery/dev_frontend_production.go) with SPA fallback and 503-until-built,
  supervisor `RebuildProductionFrontends`, watcher extension via
  `productionFrontendWatch` + `splitProductionFrontendPaths` (dist/ excluded).
  Tests in cmd/scenery/dev_frontend_production_test.go.
- [x] (2026-07-15) M4 docs — local-contract expose/serve semantics, cookbook
  recipe rewritten for loopback and Cloudflare-fronted topologies, README,
  agent-guide, SKILL, AGENTS.md, config schema.
- [x] (2026-07-15) M5 validation — full `go test ./...` and
  `scenery harness self --summary --write` green (see Outcomes).

## Surprises & Discoveries

- (2026-07-15) Traffic through the port-forwarded helper is indistinguishable
  from local edge traffic at the router, so exposure filtering keys on "request
  arrived for the dev domain host", not on a public-edge header.
- (2026-07-15) `isWatchedFile` (cmd/scenery/watch.go) had never included the
  `.scn` extension — verified live on the `testdata/apps/basic` fixture: Go
  edits triggered `rebuild.detected`, interleaved `scenery.scn` and
  `scenery.package.scn` edits did not. Fixed in the follow-up task: every
  `.scn` file except the compiler-owned `scenery.lock.scn` (mirroring
  `scn.SourceFiles` discovery) is watched, re-verified live (one rebuild per
  `.scn` edit, successful reloads, no loops), with
  `TestIsWatchedFileIncludesScenerySources`.
- (2026-07-15) DNS has no `*-local.<domain>` pattern syntax — a wildcard is
  only the whole leftmost label. The owner chose a proxied `*.clean.tech`
  catch-all A record to the static IP; explicit records keep winning over the
  wildcard.

## Decision Log

- 2026-07-15 (owner): Public exposure via static IP + port forward to the Mac,
  Cloudflare proxied in the middle. Cloudflare terminates client TLS, so
  managed dnsmasq and local-CA browser trust drop out of the picture; the
  origin leg uses Caddy's internal on-demand cert (Cloudflare "Full", not
  strict).
- 2026-07-15 (owner): Host naming `<worktree>-local.clean.tech` to stay within
  Universal SSL's first-level wildcard.
- 2026-07-15 (owner): Nothing is blocked by default on the domain origin;
  `dev.routing.expose` is the opt-in narrowing. Console and runtime are
  servable publicly when listed (or when the list is absent) — the owner
  accepts this and can front the hostnames with Cloudflare Access later.
- 2026-07-15 (owner): Per-frontend `serve` mode with `production` meaning a
  real build served statically, `development` meaning today's HMR dev server.
- 2026-07-15 (agent): Production frontends build at `scenery up` startup and
  rebuild when the existing file watcher reports changes under that frontend
  root; no extra config knobs (no custom output dir, no build command
  override) until a real app needs them.
- 2026-07-15 (agent): Rebuilds write the build output in place instead of the
  originally planned temp-dir-and-swap: vite/astro own their output directory
  layout, and a brief mid-write window is acceptable for a dev runtime. The
  first build gates serving behind a 503, and a failed rebuild keeps the
  previous bundle.
- 2026-07-15 (agent): The first production build runs asynchronously behind
  the same readiness channel as dev-server startup, so `--wait ready` includes
  it and foreground startup is not serialized behind frontend builds.

## Outcomes & Retrospective

Completed 2026-07-15, same day as 0116. Dash hosts, `dev.routing.expose`, and
`frontends.<name>.serve: production` landed with full `go test ./...` and
`scenery harness self --summary --write` green. Owner-side acceptance still
pending real Cloudflare records (proxied `*.clean.tech` catch-all to the
static IP, SSL mode Full), router 80/443 port-forward to the Mac, and
`scenery deploy setup`. Retrospective: the exposure filter landed almost
entirely at the existing `RoutePathMode` dispatch seam; the genuinely new
machinery was the in-process static frontend server and the watcher's
production-frontend partition. The `.scn` watch-set gap discovered on the way
is tracked separately.

## Context and Orientation

Read docs/plans/0116-dev-domain-path-hosts.md first; this plan amends it.

- Host derivation: `DevDomainHost` in internal/agent/session.go currently
  joins with `.`; M1 switches to `-`.
- Exposure: route manifests already travel from `scenery up` to the agent
  (internal/agent/types.go `RouteManifest`); `public_routes` rides there the
  same way `domain_host` does. The dispatch point is the `RoutePathMode`
  branch in `routerMux` (internal/agent/router.go), which rebases the
  manifest via `devDomainRouteManifest`; filtering removes non-exposed route
  records (and refuses `/runtime`+`/console` special paths unless exposed)
  before `handlePathModeRoute` runs.
- Frontends: cmd/scenery/dev_frontends.go starts dev servers
  (`managedFrontendCommand` detects vite/astro and package manager, hidden
  loopback port, allowed-host and base-path injection). Production mode adds:
  run the package `build` script (same package-manager detection, with the
  vite `--base` equivalent) into the package's default output directory
  (`dist/`), then serve it with a scenery-internal static HTTP server
  (loopback port, SPA fallback to `index.html` for HTML navigation requests,
  no fallback for `/runtime`, `/__scenery`, concrete asset paths) registered
  as the frontend backend. The file watcher (cmd/scenery/watch.go
  `scanWatchedFiles`) already snapshots the app root; changes under a
  production frontend root trigger that frontend's rebuild instead of a Go
  rebuild.
- Ingress: `scenery deploy setup` already installs the public helper
  (0.0.0.0:80/443 → Caddy). Dev-domain hosts are served by the existing
  on-demand internal-TLS site gated by `/v1/tls/allow` (registered hosts
  pass), which Cloudflare "Full" accepts at the origin. DNS: proxied records
  for `local.clean.tech` plus either per-branch records or a `*.clean.tech`
  wildcard pointing at the static IP.

## Milestones

1. M1: `DevDomainHost` dash join; update 0116 tests, config schema
   description, local-contract/README/cookbook/AGENTS wording.
2. M2: `dev.routing.expose` parsing + validation (unknown names rejected at
   `scenery up`), `RouteManifest.PublicRoutes`, dispatch filter in the
   `RoutePathMode` branch, tests for allowed/blocked routes incl. runtime and
   console special paths and the route index only listing exposed routes.
3. M3: `frontends.<name>.serve` parsing + validation, production build
   invocation, static server backend with SPA fallback and base path,
   watcher-triggered rebuild, tests (config validation, static server
   behavior, build command derivation).
4. M4: cookbook recipe rewrite for the Cloudflare topology (DNS records,
   Cloudflare SSL mode Full, `scenery deploy setup`, router port-forward),
   local-contract exposure semantics.
5. M5: `go test ./...`, `go test ./cmd/scenery`, `scenery harness self
   --summary --write`.

## Plan of Work

M1 is a two-line behavior change in `DevDomainHost` plus test/doc updates.

M2 adds `Expose []string` to `DevRoutingConfig`, normalized route names
carried as `RouteManifest.PublicRoutes` (path mode only). Validation at
session preparation: every entry must be `root`, `api`, `console`, `runtime`,
or a configured frontend name; duplicates collapse; unknown names fail
`scenery up` with the offending entry. The router filter builds the rebased
manifest, then when `PublicRoutes` is non-empty removes unlisted route
records, 404s `/runtime*` unless `runtime` is listed, and maps `console` to
the dashboard record the way `normalizeAliasRoute` does.

M3 adds `Serve string` to `FrontendConfig` (`""`/`development`/`production`).
For production frontends, session preparation runs the build (package manager
`run build`, vite base arg when applicable), fails `scenery up` with the build
output on error, starts `net/http` static servers on hidden loopback ports
serving the output dir, and registers them as ordinary frontend backends —
route manifests, allowed hosts, and domain dispatch see no difference. The
supervisor's rebuild path gets a hook: changed paths entirely under production
frontend roots trigger that frontend's rebuild (atomic swap of the served dir:
build into a temp dir, then swap the server's root) instead of the Go
rebuild.

M4 rewrites the cookbook "Branded Dev Domain Per Worktree" recipe for this
topology and updates docs/local-contract.md for expose + serve semantics.

## Concrete Steps

Work from /Users/petrbrazdil/Repos/scenery on `main`.

1. M1: edit `DevDomainHost`, its tests, schema description, docs wording.
2. M2: internal/app config, cmd/scenery session prep validation,
   internal/agent manifest field + router filter, tests in
   internal/agent/dev_domain_routing_test.go and cmd/scenery/dev_domain_test.go.
3. M3: internal/app `FrontendConfig.Serve`, cmd/scenery/dev_frontends.go
   production build + static server, watch.go rebuild hook, tests.
4. M4: docs.
5. M5: validation matrix.

## Validation and Acceptance

    go test ./...
    go test ./cmd/scenery
    scenery harness self --summary --write

Acceptance (owner machine, after DNS + `scenery deploy setup` + router
forward):

- `scenery up` on onlv `main` serves `https://local.clean.tech/` through
  Cloudflare from any device; a worktree on branch `pricing` serves
  `https://pricing-local.clean.tech/`.
- With `"expose": ["api", "next"]`, the domain serves `/api/` and `/next/`
  and 404s `/console/`, `/runtime`, `/pulse/`; `localhost:<port>` still
  serves everything.
- With `"serve": "production"` on `blog`, `scenery up` builds it once,
  `/blog/` serves the built assets with no Vite dev client, and editing a
  blog source file rebuilds and swaps the output without restarting the app.

## Idempotence and Recovery

All changes are additive config + behavior swaps guarded by config values;
absent config reproduces 0116 behavior except the dash join, which is a pure
naming change. Failed production builds fail `scenery up` with the build log
(recovery: fix the frontend, rerun). Static servers are session-scoped
processes-in-process (goroutines), cleaned up with the session.

## Artifacts and Notes

- Cloudflare Universal SSL covers apex + one label; that constraint drove the
  dash naming.
- Origin TLS uses Caddy's internal on-demand issuance already gated by
  `/v1/tls/allow`; Cloudflare must be set to SSL mode "Full" (not "Full
  (strict)") for those hostnames.

## Interfaces and Dependencies

- Config: `dev.routing.expose` (optional []string), `frontends.<name>.serve`
  (optional, `development`|`production`). Schema updated in
  docs/schemas/scenery.config.schema.json.
- Manifest: `route_manifest.public_routes` (optional []string, path mode).
- No new Go dependencies, no env vars. Depends on `scenery deploy setup`'s
  public helper for ingress and operator-owned Cloudflare DNS/SSL settings.
