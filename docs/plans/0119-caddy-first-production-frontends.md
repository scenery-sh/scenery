# Caddy-First Production Frontend Deployments

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
the `Decision Log`, and `Outcomes & Retrospective` current while implementing
it.

- Status: completed (2026-07-15)
- Owner: scenery runtime / edge / deploy
- Created: 2026-07-15

## Purpose / Big Picture

Make a public Scenery deployment serve production frontend files from Caddy
instead of sending every browser request through the Scenery agent and a Vite
development server. Caddy should do the work it is designed for: terminate TLS,
compress responses, serve static files, support range requests and validators,
and route only dynamic traffic to Scenery. The agent remains the policy and
routing owner for `/api/*`, non-static frontends, and other dynamic app
backends.

The user-visible result is that `scenery deploy <ssh-target>` can deploy an app
whose `frontends.<name>.serve` is `"production"` to a Linux host, publish the
built frontend safely, and expose it through Scenery's managed Caddy edge. The
HTML must not contain the Vite development client or React Refresh, and requests
for its JavaScript, CSS, images, fonts, and models must not traverse the agent
router.

This plan fixes Scenery's public deployment topology. It does not make Scenery
responsible for application bundle splitting or for avoiding application-level
eager data and model loads. A large asset can still be slow over the network;
the important contract here is that Scenery serves the production artifact
efficiently and does not amplify one page load into hundreds of proxied
development-module requests.

## Progress

- [x] 2026-07-15 14:54Z Inspected plans 0101, 0115, and 0117 and the current
      Caddy, public-router, app-config, and SSH-deploy boundaries.
- [x] 2026-07-15 14:54Z Recorded the live `platform.onegraph.dev` failure shape
      and separated Scenery transport work from application bundle work.
- [x] 2026-07-15 Implemented `internal/edge` publisher: immutable releases
      under `<agent home>/agent/deploy-artifacts/<app>/<frontend>/<release>`,
      atomic `current` symlink switch, symlink/special-file rejection,
      staging cleanup, retention of 3 releases, and `RollbackCurrentRelease`.
- [x] 2026-07-15 Extended `DeployTarget` with additive `frontends` records and
      the Caddy renderer with blocked paths, `/api/*` agent proxy, per-frontend
      `handle_path` static routes, root-frontend `/` serving, SPA fallback,
      immutable `/assets/*` caching, and Linux direct `:443/:80` binding.
      Verified with golden tests plus a live managed-Caddy integration test.
- [x] 2026-07-15 Added Linux/systemd lifecycle: `scenery-agent.service`,
      `scenery-edge.service`, `scenery-deploy-resume.service`; `deploy
      setup/status/teardown` dispatch per service manager; systemd-aware edge
      restart/reload and Caddy `validate` before every reload.
- [x] 2026-07-15 Added `scenery deploy publish` (remote build → publish →
      registry upsert → validate/reload → local-SNI probe → rollback on
      failure) and appended it to `scenery deploy <ssh-target>` when the app
      has production frontends and a deploy domain.
- [x] 2026-07-15 Updated `docs/local-contract.md`, `docs/agent-guide.md`,
      `SKILL.md`, `README.md`, the cookbook, root and child `AGENTS.md`, CLI
      help, deploy schemas (new `scenery.deploy.publish`, extended
      setup/teardown/status/registry), and `docs/knowledge.json`.
- [x] 2026-07-15 Passed focused/race tests, `go test ./...`, live managed-Caddy
      integration, real Linux acceptance on the authorized host, and the
      live-browser waterfall (see Outcomes).

## Surprises & Discoveries

- The live deployment already used Caddy, but only as a TLS reverse proxy:
  `Cloudflare -> Caddy -> Scenery agent router -> Vite`. Caddy was not serving
  application files. `internal/edge/caddyconfig.go` confirms that every public
  site currently has one unconditional `reverse_proxy` to the agent upstream.
- A cold `platform.onegraph.dev` browser load took 11.391 seconds to
  `DOMContentLoaded`, 12.097 seconds to `load`, and 14.761 seconds to its final
  resource. It fetched 249 resources, including 241 scripts and 42.53 MB. The
  HTML loaded `@vite/client` and React Refresh, proving that the public origin
  exposed a Vite development runtime.
- The same deployment served a 31.27 MB `scene.opt.glb`. That payload and the
  app's eager route/component imports are application concerns; direct Caddy
  serving removes proxy and dev-module overhead but cannot make bytes disappear.
- Direct Vite responses were about 1 ms, while the direct Scenery agent route
  was roughly 350-410 ms before concurrency and reached about 2.336 seconds p95
  during the module waterfall. The edge and Cloudflare TLS were fast. The delay
  was therefore behind Caddy, not in DNS, Cloudflare, ACME, or TLS setup.
- Plan 0117 already defines `frontends.<name>.serve: "production"` for local and
  development-domain use. It deliberately uses a Scenery-internal static server.
  Public deployment can reuse the same intent without changing that local
  behavior.
- Plan 0115 deliberately syncs source and excludes ignored build output. A
  public static artifact must therefore be built on the remote host and copied
  into Scenery-owned state; adding `dist/` to rsync would violate the existing
  source-sync contract and is unreliable because it is normally ignored.
- `scenery deploy setup` and teardown currently reject non-macOS hosts. The live
  Linux deployment required a hand-written systemd Caddy service, which is a
  Scenery capability gap rather than an app responsibility.
- First Linux setup exposed two real bugs: the `http://` redirect site missed
  the direct-mode `bind 0.0.0.0`, leaving port 80 loopback-only so ACME HTTP-01
  could not complete; and installing `scenery-agent.service` while an
  unsupervised agent held `agent.lock` crash-looped the unit. The fix mirrors
  launchd: the setup handover stops the unsupervised agent before the
  Restart=always unit starts.
- `deploydiag.DefaultLANIP` was macOS-only (`ipconfig getifaddr`); Linux now
  discovers the first non-loopback IPv4 interface address.
- macOS unix admin-socket paths exceed the ~104-byte limit under `t.TempDir()`,
  so the live-Caddy integration test allocates a short `os.MkdirTemp` socket
  directory.
- The production Platform bundle initially mounted nothing: the published
  Astryx design-system packages were compiled with the development JSX
  transform, and production React exports `jsxDEV = undefined` from
  `react/jsx-dev-runtime`. This is an application dependency bug, exactly the
  class this plan scoped out of Scenery; the Micro repo now aliases
  `react/jsx-dev-runtime` to a production-runtime shim for `vite build`
  (app-side fix; Astryx should republish with the production transform).

## Decision Log

- 2026-07-15, Petr and agent: use Caddy directly wherever a frontend has a
  production build. Caddy owns TLS and static delivery; Scenery owns publication,
  configuration, dynamic routing, status, and diagnostics. This removes a hot
  proxy hop without introducing an app-owned web-server configuration.
- 2026-07-15, agent: reuse `frontends.<name>.serve: "production"`; do not add a
  second deploy-only static flag. The existing value already states the app
  author's intent and keeps the configuration surface small.
- 2026-07-15, agent: preserve plan 0117's Scenery-internal static server for
  `scenery up` and development-domain routing. Caddy-first serving applies only
  to enabled public deploy domains, so local HMR and production-mode testing
  remain coherent.
- 2026-07-15, agent: publish remote builds into Scenery-owned immutable
  directories and atomically switch a `current` pointer only after validation.
  Caddy must never observe a partially copied bundle, and a failed build must
  leave the prior public frontend usable.
- 2026-07-15, agent: extend the existing deploy registry and managed Caddy
  renderer rather than adding a second router or app-specific Caddyfile. One
  Scenery edge remains authoritative for all enabled domains.
- 2026-07-15, agent: support the first Linux edge as a single-user systemd host,
  including root SSH hosts such as the motivating deployment. Do not design a
  multi-tenant hosting platform, container scheduler, or general release
  manager in this plan.
- 2026-07-15, agent: keep API and dynamic requests behind the trusted-edge token
  and public-route checks in the agent. Static routing must not expose
  `/runtime`, `/dashboard`, `/__scenery`, dotfiles, source maps unless explicitly
  present in the production artifact, or any path outside the published root.

## Outcomes & Retrospective

Completed 2026-07-15 with live acceptance on the authorized Linux host.

- Implementation: `internal/edge/publish.go` (atomic artifact publisher with
  retention and rollback), extended `internal/edge/caddyconfig.go` (static
  routes, blocked paths, `/api/*` proxy, Linux direct binding),
  `internal/edge/systemd.go` and `internal/agent/systemd.go` (Linux service
  manager), `cmd/scenery/deploy_systemd.go` and `deploy_publish.go` (Linux
  setup/teardown and the publish command), SSH-deploy publish step, typed
  status `frontends` fields, and Caddy `validate` before every reload.
- Live evidence (platform.onegraph.dev on the acceptance host): on-host direct
  origin entry-document TTFB 23-25 ms across three uncached requests (budget:
  under 100 ms). Public HTML has no `@vite/client` or React Refresh. Hashed
  `/assets/*` respond with `cache-control: public, max-age=31536000, immutable`
  plus ETags; `HEAD`, 206 byte ranges on the 31 MB model, missing concrete
  assets 404, `/runtime` 404, `/api` 200 through the agent. Cold browser
  waterfall: DOMContentLoaded 1.2-1.9 s, 3-8 requests, versus the recorded
  baseline of 11.391 s and 249 requests. `scenery deploy status -o json`
  reports `ready: true`, `service_manager: "systemd"`, and
  `frontends[].mode: "caddy_static"`. Re-running the deploy was idempotent and
  published a second retained release.
- Remaining load time is the app's 31 MB photogrammetry model and production
  bundle — recorded as an application follow-up, not a transport concern.
- Follow-ups: Astryx packages should be republished with the production JSX
  transform so the Micro shim can be removed; macOS keeps the existing helper
  topology and could adopt direct binding later if ever needed.

## Context and Orientation

Scenery has three related but distinct paths:

1. `scenery up` starts an app runtime. In development frontend mode it starts the
   package dev server. In production frontend mode, added by plan 0117, it runs
   the package build and starts a small Scenery-owned static server on loopback.
2. `scenery deploy enable` records a public domain, app root, and root service in
   `<agent home>/deploy.json`. `internal/edge/caddyconfig.go` turns those targets
   into Caddy sites. Today every public site reverse-proxies to the agent router.
3. `scenery deploy <ssh-target>`, added by plan 0115, validates locally, stops
   the remote app, rsyncs non-ignored source to
   `$HOME/.scenery/apps/<app-id>`, and starts remote `scenery up`.

The relevant code is:

- `internal/app/root.go`: `FrontendConfig`, `DeployConfig`, and validation of
  `deploy.domain`, `deploy.root`, and `deploy.ssh`.
- `cmd/scenery/dev_frontend_production.go`: current build and local static-server
  behavior for a production-mode frontend.
- `cmd/scenery/deploy_ssh.go`: the ordered remote source-sync deployment steps.
- `cmd/scenery/deploy.go`: deploy registration, setup, resume, teardown, status,
  and readiness diagnostics.
- `internal/agent/deploy.go` and `internal/agent/paths.go`: deploy-registry and
  agent-home state contracts.
- `internal/edge/caddyconfig.go`: Caddyfile model and renderer for local and
  public sites.
- `internal/edge/lifecycle.go`: managed Caddy process lifecycle.
- `internal/agent/router.go`: trusted public-edge request dispatch and public
  containment checks.
- `docs/schemas/scenery.config.schema.json`: application configuration schema.

After this plan, a production public frontend follows this path:

```text
browser -> Cloudflare or direct origin -> Caddy
    /api/* and dynamic routes          -> Scenery agent -> app backend
    /<production-frontend>/*           -> published files on disk
    / when deploy.root is that frontend -> published files on disk
```

A development-mode frontend continues through the agent to its runtime backend.
Caddy is therefore preferred where feasible, not forced where HMR or a dynamic
frontend server is the requested behavior.

The published artifact belongs under the agent home, outside the synchronized
app source. Use a path derived from a validated app ID and frontend name, for
example `<agent home>/deploy-artifacts/<app-id>/<frontend>/<release>/`, plus an
atomically replaced `current` symlink. The final names may follow existing path
conventions discovered during implementation, but must remain machine-owned,
inspectable, and impossible to escape through config input.

The deploy registry needs enough durable data for Caddy rendering without
starting the app or guessing its build layout. Extend each enabled target with
normalized published-frontend records containing the frontend route name, the
validated current directory, and whether it owns `/`. Treat the new fields as
an additive artifact revision and keep old registry files readable; an old
target with no publication metadata retains the current agent-proxy behavior.

## Milestones

### Milestone 1: Production artifact publication

Extract the smallest reusable build-output discovery from
`cmd/scenery/dev_frontend_production.go`. After the existing production build
succeeds, identify the built directory using the current frontend contract,
validate that it contains an entry document, and copy it into a new immutable
Scenery-owned release directory. Reject symlinks or paths that escape the build
root. Preserve normal file modes; do not preserve ownership, sockets, devices,
or unrelated source files.

Validate the staged artifact before switching `current`. At minimum require a
regular `index.html`, ensure every staged path remains below the release root,
and make the release readable by the unprivileged Caddy process. Replace the
pointer atomically on the same filesystem. Keep the previous release until the
new Caddy configuration validates and reloads, then prune older releases with a
small fixed retention count. This is artifact safety, not a general rollback
system.

Add focused tests for successful publication, nested assets, a missing entry
document, symlink/path escape, interrupted copy, atomic switch, repeat
publication, and cleanup. Keep filesystem mechanics in a small internal package
owned by deploy or edge rather than adding them to the CLI package.

### Milestone 2: Caddy static and dynamic route composition

Extend `internal/edge.PublicDomainSite` so a site can describe zero or more
published production frontends. Render Caddy handlers in this order:

1. Reject Scenery-owned public-blocked paths and unsafe dotfile traversal.
2. Reverse-proxy `/api/*` and explicitly dynamic routes to the agent with the
   existing trusted public-edge headers, restart retry window, and streaming
   behavior.
3. Serve each non-root production frontend under `/<name>/*`, stripping only
   that route prefix.
4. If `deploy.root` names a published production frontend, serve it at `/`.
   Otherwise use the existing agent proxy as the catch-all.

For static handlers, accept only `GET` and `HEAD`, serve concrete files before
using `index.html` as an SPA fallback, and never apply SPA fallback to `/api/*`,
blocked paths, or a request that looks like a missing concrete asset. Enable
Caddy's gzip and zstd encoders where content negotiation permits. Preserve
Caddy file-server validators and byte-range behavior. Give content-hashed assets
an immutable cache policy; keep `index.html` and stable filenames revalidatable
so a deployment does not strand clients on an obsolete entry document.

Add golden Caddyfile tests plus an integration test using the managed Caddy
binary. Prove that a static document, nested SPA route, hashed asset, `HEAD`,
range request, missing asset, API request, and blocked Scenery path each reach
the intended handler. Validate the generated Caddyfile before reloading it.

### Milestone 3: Linux public-edge lifecycle

Add Linux support to `scenery deploy setup`, `status`, `resume`, and `teardown`
using systemd. Keep the first contract intentionally narrow: one Linux user owns
the Scenery agent home and managed Caddy state on a single host; the setup
invocation must have the privileges needed to install or update the system unit
and bind ports 80 and 443. Report an actionable error when systemd or those
privileges are unavailable.

Use Scenery's managed Caddy binary and generated Caddyfile. Do not require a
separately installed distro Caddy, generate an app-local unit, or leave a second
manually managed Caddy process competing for the ports. The unit should restart
on failure, start after the network is available, write logs to journald, and
run with the least privilege compatible with the selected bind strategy. Keep
TLS and artifact state under the Scenery owner and make permissions explicit.

Model platform-specific service management behind a small internal interface so
macOS launchd behavior remains unchanged. Unit rendering and state inspection
must be testable without invoking live systemd. Status must identify the service
manager, loaded/active state, Caddy config validation, public listeners, and
whether configured static roots resolve to complete published artifacts.

### Milestone 4: SSH deploy integration and recovery

Extend `scenery deploy <ssh-target>` after remote source sync so the remote host
builds and publishes every production-mode frontend, enables or refreshes the
app's configured deploy target, validates the candidate Caddyfile, reloads the
edge, and only then reports success. Keep one-time privileged edge setup
explicit: if the remote edge has not been configured, stop with the exact remote
`scenery deploy setup` command rather than silently attempting privilege
escalation.

Order the operation to preserve the last known-good frontend:

1. Validate the local app and SSH target.
2. Preflight the remote Scenery, rsync, build-tool, edge, and disk requirements.
3. Stop and synchronize source using plan 0115's existing behavior.
4. Start the remote runtime and wait for dynamic readiness.
5. Stage and validate production frontend artifacts.
6. Write candidate deploy metadata and validate the generated Caddyfile.
7. Atomically switch artifacts, persist the target, and reload Caddy.
8. Probe the direct-origin HTTPS document and API route before success.

If steps 5-7 fail, restore the previous pointer and Caddy configuration and keep
the previous static frontend public. Report separately whether the newly synced
dynamic runtime is ready; do not hide a partial outcome behind a generic SSH
exit error. Re-running the command with unchanged source must be safe.

Expose publication truth in `scenery deploy status -o json`: app ID, frontend,
route, serving mode (`caddy_static` or `agent_proxy`), current artifact identity,
entry-document presence, Caddy config validity, and direct-origin probe result.
Keep field names typed and documented so agents do not need to infer topology
from process lists or Caddyfile text.

### Milestone 5: Contracts, docs, and end-to-end acceptance

Update the root `AGENTS.md`, `SKILL.md`, `README.md`, `docs/local-contract.md`,
`docs/agent-guide.md`, `docs/app-development-cookbook.md`, CLI help, app config
schema, deploy status examples, and `docs/knowledge.json`. State plainly that
`serve: "production"` has two compatible implementations: a Scenery static
server for local runtime verification and direct managed-Caddy serving for an
enabled public deployment.

Use a small fixture app to prove both static and dynamic paths. Then deploy the
real Platform app to the Linux acceptance host and capture browser and HTTP
evidence against `platform.onegraph.dev` and direct-origin SNI. Do not place
host credentials, Cloudflare tokens, raw environment files, or database URLs in
the repository or plan artifacts.

## Plan of Work

Begin with contract tests for the deploy artifact and Caddy route model. The
tests should describe the desired boundary before CLI orchestration changes:
old registry targets remain proxy-only, production targets gain validated static
routes, and API/public-containment behavior is unchanged.

Implement publication as a small internal package with explicit inputs: source
build directory, destination deploy root, app ID, frontend name, and artifact
identity. Return a typed published record. Keep process execution and user
output in `cmd/scenery`; keep copy, validation, switching, and cleanup logic out
of the command package.

Teach the deploy registry to store those records and
`internal/edge/caddyconfig.go` to consume them. First make renderer tests pass,
then run Caddy's own `validate` command and integration requests. Do not bypass
the registry by reading app configuration from Caddy rendering; the registry is
the machine-level declaration of what is currently public.

Add the Linux service-manager implementation after the Caddy contract works in
an ordinary process. Reuse `internal/edge/lifecycle.go` where possible and keep
OS-specific unit installation at the boundary. Ensure the test suite cannot
write `/etc`, call real systemctl, or bind public ports.

Finally wire the ordered remote steps into `cmd/scenery/deploy_ssh.go`, enrich
status and diagnostics, update documentation, and perform the live acceptance.
Do not combine this with application code splitting, Cloudflare configuration,
containers, zero-downtime backend rollout, or a generalized release store.

## Concrete Steps

Run all commands from the Scenery repository root unless stated otherwise.

1. Establish the current baseline and preserve it in `Surprises & Discoveries`:

       git status --short --branch
       go test ./internal/edge ./internal/agent ./cmd/scenery
       scenery inspect docs -o json

2. Add failing focused tests for publication, registry compatibility, Caddy
   route generation, Linux unit rendering, status, and SSH command ordering.
   Implement in small milestones, running:

       go test ./internal/edge ./internal/agent ./cmd/scenery
       go test -race ./internal/edge ./internal/agent

3. Validate real generated Caddy configuration using the managed binary. Obtain
   its path through Scenery's toolchain APIs rather than `PATH`, then run the
   equivalent of:

       caddy validate --config <temporary-caddyfile> --adapter caddyfile

   Use `/tmp` for the temporary fixture and config.

4. Exercise Linux setup first against a disposable systemd-capable VM, then the
   authorized acceptance host. Record `systemctl status`, public listener state,
   `scenery deploy status -o json`, and direct-origin probes without committing
   secrets or IP-specific service files.

5. Run full repository validation:

       go test ./...
       go install ./cmd/scenery
       scenery harness self -o json --write
       git diff --check

6. From the Platform app root, run the real source-sync deploy and browser proof:

       scenery check
       scenery deploy <configured-ssh-alias>
       scenery deploy status -o json

   Use the configured alias, never embed credentials in the plan. Probe both the
   Cloudflare-facing URL and direct-origin SNI, then capture a cold browser
   waterfall for comparison with the baseline.

## Validation and Acceptance

The plan is complete only when all of the following are observable:

- Existing app configs and deploy registries still work. A target without
  published metadata retains the current agent-proxy behavior.
- Local `scenery up` behavior from plan 0117 is unchanged in both development
  and production frontend modes.
- On Linux, Scenery installs, validates, starts, inspects, resumes, and removes
  its managed public Caddy service without an app-owned Caddyfile or unit.
- `scenery deploy <ssh-target>` builds on the remote host, publishes atomically,
  reloads only a valid Caddyfile, probes the result, and is idempotent.
- Public production HTML contains neither `@vite/client` nor React Refresh.
- A production frontend document, JS/CSS asset, image/model range request, and
  SPA navigation are answered by Caddy without a corresponding request in the
  Scenery agent router. `/api/*` does reach the agent and preserves streaming,
  forwarded host/proto, and trusted-edge containment.
- `/runtime`, `/dashboard`, `/__scenery`, traversal attempts, and unknown public
  hosts remain unavailable. Missing concrete assets return 404 rather than the
  SPA document.
- Hashed assets have immutable caching; `index.html` revalidates; ETag, `HEAD`,
  compression, and byte ranges behave correctly.
- A failed build, invalid artifact, invalid Caddyfile, or failed reload leaves
  the previous static frontend reachable. Status reports the failed candidate
  and the still-active artifact distinctly.
- Against the direct origin, three uncached requests for the production entry
  document have median time-to-first-byte below 100 ms on the acceptance host.
  This isolates Scenery/Caddy from Cloudflare and client-network variance.
- The Platform cold-browser waterfall no longer contains hundreds of Vite
  source modules and shows a material improvement from the recorded 11.391
  second `DOMContentLoaded` baseline. If total load remains dominated by the
  31.27 MB model or the app's production bundle, record that separately as an
  application follow-up; it is not grounds for reintroducing the agent proxy.
- Focused tests, race tests, `go test ./...`, install, self-harness, docs
  inspection, and `git diff --check` pass from a clean Scenery worktree.

## Idempotence and Recovery

Publication writes a new immutable release and switches one pointer; repeating
it cannot corrupt an existing release. A staging directory is never referenced
by Caddy and can be deleted after a crash. Cleanup must refuse paths outside the
validated deploy-artifact root.

Caddy configuration is generated to a candidate file, validated by Caddy, and
only then installed/reloaded. Preserve the prior config until the reload and
direct-origin probe succeed. If a reload fails after an artifact switch, restore
both the previous pointer and config and report the rollback result.

Linux service setup is convergent: re-running it updates only a changed managed
binary, unit, or config, runs `daemon-reload` when required, and leaves a healthy
service running. Teardown removes only Scenery-owned unit and edge files; it does
not delete published app artifacts or ACME state unless the documented command
explicitly requests that cleanup.

SSH deployment keeps plan 0115's source exclusions and remote secrets. A retry
after network loss re-validates current remote state, rebuilds if necessary,
and either completes the candidate or leaves the previous publication active.
Never delete the previous release before the new public probe passes.

## Artifacts and Notes

Keep concise, redacted evidence here while implementing:

- Before/after Caddyfile excerpts showing handler order.
- Focused test and Caddy-validation output.
- Redacted `scenery deploy status -o json` for the Linux fixture and live app.
- Direct-origin timing samples and route-specific headers.
- Browser waterfall totals: document timing, request count, script count, bytes,
  largest resources, and presence/absence of Vite development markers.
- Failure-injection evidence for build, publication, validation, and reload.

Do not commit generated frontend releases, ACME state, systemd units containing
machine paths, browser profiles, `.env` files, or raw host credentials.

## Interfaces and Dependencies

Prefer existing dependencies and the standard library. Caddy remains Scenery's
managed edge dependency; do not add nginx, a Node static server, a deployment
provider SDK, or a second proxy.

The implementation should leave these explicit interfaces:

- A small internal publisher accepts validated app/frontend identities and a
  build-output directory, then returns a typed immutable publication record.
- `internal/agent.DeployTarget` stores optional published frontend records in an
  additive, backward-readable deploy-registry contract.
- `internal/edge.PublicDomainSite` describes static frontend routes plus the
  existing dynamic upstream and renders one deterministic Caddy site.
- The edge lifecycle selects launchd on macOS and systemd on Linux behind a
  narrow service-manager boundary.
- `scenery deploy status -o json` exposes serving mode and artifact/config
  health as stable typed fields.
- `scenery deploy <ssh-target>` remains the only ordinary remote deployment
  command. One-time privileged Linux setup is explicit and separate.

If implementation discovers that Caddy cannot safely serve a particular
frontend artifact, retain the agent-proxy fallback for that frontend, report the
reason in status, and add the evidence and decision here before expanding the
configuration surface.
