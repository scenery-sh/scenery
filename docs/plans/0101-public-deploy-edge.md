# 0101 Public Deploy Edge: Internet-Facing 80/443 Routing to Scenery Apps

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Today a Scenery machine can already serve local HTTPS on `127.0.0.1:443` through a small privileged helper, but nothing Scenery-managed can accept traffic from the internet. This plan gives one Mac the ability to serve real public domains: a single Scenery-managed edge binds `0.0.0.0:80` and `0.0.0.0:443`, terminates TLS with real ACME (Let's Encrypt) certificates, and routes each incoming request by `Host` header to the Scenery app whose config claims that domain. One edge serves many Scenery apps.

The user-visible contract after this plan:

```sh
# once per machine (asks for sudo password)
scenery deploy setup

# in an app root whose .scenery.json has {"deploy": {"domain": "onlv.dev"}}
scenery deploy enable
scenery up --detach

# from anywhere on the internet, assuming DNS + router forwarding point at this Mac
curl https://onlv.dev/api/health
```

Public traffic reaches the live dev session (`scenery up`) of the enabled app root. The routing shape on a public domain is the production path-route shape sketched in plan 0090: `/` maps to a configured root service (for example the `ui` frontend), `/api/` maps to the API backend, other frontends get their path prefixes. Scenery-owned dev surfaces (console, dashboard, `/runtime/*`) are never reachable on a public domain.

The whole stack must come back after a reboot without manual work: the privileged listener returns at boot (it is a root LaunchDaemon with `RunAtLoad` + `KeepAlive`, which already survives reboots today), and a user LaunchAgent resumes Caddy, the Scenery agent, and enabled detached app runtimes at login.

macOS is the supported platform for this plan. Linux code paths may compile but are explicitly untested and must say so in errors/docs.

## Progress

* [x] 2026-07-07: Explored existing edge substrate (`cmd/scenery/edge.go`, `internal/agent/*`), confirmed privileged helper + Caddy + host routing already exist and are the right base.
* [x] 2026-07-07: User decisions captured: full stack at login, live dev session as public target, one domain per app, CLI surface under `scenery deploy`.
* [x] 2026-07-07: Created this ExecPlan and registered it in `docs/plans/active.md` and `docs/knowledge.json`.
* [ ] Milestone 1: `deploy` app config surface (`deploy.domain`, `deploy.root`) + schema + `scenery check` diagnostics.
* [ ] Milestone 2: machine deploy registry + `scenery deploy enable|disable|status` (works before machine setup; status reports missing pieces).
* [ ] Milestone 3: privileged helper v2 — public `0.0.0.0` binding, port 80 forwarding, versioned install metadata, `scenery deploy setup` sudo flow.
* [ ] Milestone 4: Caddy edge config v2 — public ACME site blocks alongside local internal certs, pinned cert storage, graceful reload on enable/disable.
* [ ] Milestone 5: agent public host routing with path dispatch and strict containment of Scenery-owned surfaces.
* [ ] Milestone 6: login resume LaunchAgent + `scenery deploy resume`.
* [ ] Milestone 7: reachability/DNS/power/firewall diagnostics in `scenery deploy status` and `scenery doctor`.
* [ ] Milestone 8: upgrade story — helper version drift detection, `scenery upgrade` notice.
* [ ] Milestone 9: docs, schemas, knowledge index, self-harness.
* [ ] Milestone 10: real-world acceptance on this Mac with a real domain (Let's Encrypt staging first).

## Surprises & Discoveries

Record unexpected findings here during implementation.

Initial known facts from source review (2026-07-07):

* A privileged edge already exists. `cmd/scenery/edge.go` installs a root LaunchDaemon `dev.scenery.edge-helper` (`/Library/LaunchDaemons/dev.scenery.edge-helper.plist`) whose binary is a copy of the scenery executable at `/usr/local/libexec/scenery-edge-helper`. `edgePrivilegedHelperRun` listens on `127.0.0.1:443` and `[::1]:443` and blind-forwards TCP. Per connection it re-validates a target metadata file (`validateEdgeTarget`): owner UID, loopback-only target, port in 19000–19999, live PID with matching start time, and a Caddy-looking command line. Root never parses HTTP or TLS. This plan extends that pattern instead of inventing a new daemon.
* Caddy is already the managed edge. `caddyEdgeConfig` renders a Caddyfile with `local_certs`, `on_demand_tls` gated by an `ask` endpoint (`/v1/tls/allow` on the agent router), `strict_sni_host on`, and a reverse proxy to the agent router that injects `X-Scenery-Edge-Token`. Caddy is resolved from the managed toolchain store, never from `PATH`.
* The agent router already dispatches by host. `internal/agent/registry.go` `RouteTargetForHost` maps normalized hosts to `(session, route kind)`; `internal/agent/router.go` handles console/frontend/backend kinds. Public domains can become a new route kind on this same lookup.
* Plan 0090 made localhost path routing the default and sketched the production shape this plan reuses: a domain whose `/` maps to a configured app service with `/api/` and per-frontend path prefixes, explicitly excluding Scenery debug surfaces from public exposure.
* Field learnings from a previous port-binding exercise on this machine (recorded in the task that motivated this plan): the service must bind `0.0.0.0`, not `127.0.0.1`, or router-forwarded traffic to the LAN IP fails; a real `/Library/LaunchDaemons` plist is the only reliable persistence mechanism (nohup/screen/`launchctl submit`/AppleScript were flaky); root cannot read scripts under `~/Documents` due to TCC privacy, so root-executed files must live in root-readable paths (our `/usr/local/libexec` copy already satisfies this); Chrome can show `ERR_BLOCKED_BY_CLIENT` on plain-HTTP localhost while curl works — trust curl and server logs during validation.

## Decision Log

* Decision: Extend the existing edge substrate (privileged helper + Caddy + agent host routing). Do not add a second daemon or a parallel proxy.
  * Rationale: `0.0.0.0:443` subsumes today's `127.0.0.1:443`; two daemons would fight over the port. The helper's dumb-TCP-forwarder-with-per-connection-revalidation design is exactly the right privilege boundary for a root process, and Caddy already does TLS + reverse proxy with a managed binary. One edge, many apps.
  * Date: 2026-07-07. Author: initial ExecPlan (Claude), confirmed by repo exploration.

* Decision: Yes, Caddy terminates public TLS. Public domains get explicit ACME site blocks in the generated Caddyfile; local `*.local.dev` keeps internal on-demand certs; both live in the one Caddy process.
  * Rationale: Caddy is already integrated, managed, and doing on-demand internal TLS. For public domains, explicit site blocks (regenerated from the deploy registry, reloaded via the admin socket) beat on-demand ACME: the domain set is small and known (one per app), issuance is deterministic, and it avoids Let's Encrypt rate-limit exposure from `ask`-gated lazy issuance.
  * Date: 2026-07-07. Author: initial ExecPlan.

* Decision: The privileged helper stays a dumb TCP forwarder. Public mode adds listeners on `0.0.0.0:80`, `0.0.0.0:443`, `[::]:80`, `[::]:443` and forwards 443 → Caddy HTTPS target (today `127.0.0.1:19443`) and 80 → a new Caddy HTTP target (`127.0.0.1:19080`). Per-connection target revalidation is retained; when Caddy is down the helper drops connections (fail-closed).
  * Rationale: Root code must stay minimal and auditable. All routing, TLS, and policy live unprivileged. Port 80 must be forwarded for ACME HTTP-01 challenges and HTTP→HTTPS redirects.
  * Date: 2026-07-07. Author: initial ExecPlan.

* Decision: Caddy's global `http_port` is set to `19080` and `https_port` to `19443` when public mode is enabled, so Caddy's ACME HTTP-01 solver and redirects work behind the forwarder while public ports remain 80/443. Public HTTP sites are explicit `http://<domain>` blocks that `redir https://{host}{uri} 308`.
  * Rationale: Caddy must answer ACME challenges on the socket that public port 80 reaches. Explicit redirect blocks keep the existing global `auto_https disable_redirects` behavior for local routes unchanged. Milestone 10 must verify against Let's Encrypt staging that the challenge handler wins over the redirect block (Caddy injects the ACME challenge middleware ahead of site routing; if that assumption fails, switch public issuance to TLS-ALPN-01 only and record it here).
  * Date: 2026-07-07. Author: initial ExecPlan.

* Decision: CLI surface is a new top-level `scenery deploy` noun: `setup`, `status`, `enable`, `disable`, `resume`, `teardown` (all with `--json`). Edge internals stay under `scenery system edge`; privileged sub-steps reuse the `privileged-helper` command family.
  * Rationale: User decision (2026-07-07). Public exposure is a user-facing capability, not substrate; `deploy` names the intent.
  * Date: 2026-07-07. Author: user + initial ExecPlan.

* Decision: App config is one domain per app: `deploy.domain` (string), plus optional `deploy.root` naming the service that owns `/` on the public domain.
  * Rationale: User chose the smallest surface ("one domain per app only"). The field lives under `deploy` (not `public`) to match the chosen CLI noun and the `deploy.routing` production sketch in plan 0090; aliases/subdomains and multi-domain lists are explicitly deferred.
  * Date: 2026-07-07. Author: user + initial ExecPlan.

* Decision: Public traffic targets the live dev session of the enabled app root. There is no separate serve runtime in this plan.
  * Rationale: User decision. Matches "route to that app instance". Exactly one app root per domain is enabled at a time (`scenery deploy enable` claims the domain in the machine deploy registry), which resolves the many-worktrees question.
  * Date: 2026-07-07. Author: user + initial ExecPlan.

* Decision: Boot story is "ports at boot, full stack at login". The root LaunchDaemon keeps 80/443 bound from boot; a user LaunchAgent (`~/Library/LaunchAgents/dev.scenery.deploy-resume.plist`, `RunAtLoad`) runs `scenery deploy resume`, which restarts the Caddy edge, the Scenery agent, and `scenery up --detach` for every enabled app root. Between boot and login, connections are accepted and dropped (fail-closed).
  * Rationale: User decision. Fully headless boot fights macOS TCC and the user-session design of the agent; ports-only leaves apps down indefinitely. Login resume is the pragmatic middle.
  * Date: 2026-07-07. Author: user + initial ExecPlan.

* Decision: Upgrade story: the helper contract (TCP forward + target metadata schema) is deliberately tiny so scenery upgrades almost never require touching root state. The install stamps the scenery version into the plist arguments (`--helper-version`); `scenery deploy status` and `scenery doctor` compare it against the running binary and against the target metadata schema version, and instruct `scenery deploy setup` (sudo) only when the helper actually needs replacing. `scenery upgrade` prints the same notice post-upgrade when drift is detected. ACME certificates/account live in a pinned Caddy storage dir under the agent home (`<agent home>/edge/caddy-data`) so scenery/Caddy upgrades never re-issue certs.
  * Rationale: The question "does upgrading scenery affect the privileged part?" needs a designed answer: normally no (helper logic is version-stable), and when yes, it is detected and requires one explicit sudo re-setup. Cert storage stability protects Let's Encrypt rate limits.
  * Date: 2026-07-07. Author: initial ExecPlan.

* Decision: Public requests are containment-checked in the agent. Caddy tags public-edge requests (`X-Scenery-Public-Edge: 1` plus the existing edge token); the agent routes them only to app surfaces (root service, `/api/`, frontends, sync). Console/dashboard route kinds, `/runtime/*`, and path-mode session headers are rejected on public hosts. Unknown public domains 404; enabled domains without a live session get a minimal 503 page.
  * Rationale: Exposing a dev runtime to the internet is the feature, but Scenery-owned control surfaces must never be part of it. `scenery deploy enable` is the explicit consent step.
  * Date: 2026-07-07. Author: initial ExecPlan.

* Decision: No new environment variables. All knobs are CLI flags, app config (`deploy.*`), or machine deploy registry state.
  * Rationale: Repo rule.
  * Date: 2026-07-07. Author: initial ExecPlan.

* Decision: Diagnostics may make outbound network calls (public IP discovery, DNS lookup of the domain) only inside explicit `scenery deploy status` / `scenery doctor` invocations, never ambiently. Router NAT forwarding and DNS A records are the operator's job; Scenery detects and explains, it does not configure routers or DNS. Power management (`pmset` sleep settings) and the macOS application firewall are diagnosed, not mutated.
  * Rationale: Small blast radius; the machine's network and power posture belong to the human.
  * Date: 2026-07-07. Author: initial ExecPlan.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Definitions used throughout:

* Agent: the per-user background process (`scenery system agent`) that owns the session registry and the HTTP router all edges forward to. Its files live under the agent home (`localagent.DefaultPaths()`; `paths.Home`, `paths.RunDir`, `paths.EdgeDir`).
* Edge: the unprivileged Caddy process managed by `scenery system edge install`, config at `paths.EdgeConfigPath` (`<agent home>/edge/Caddyfile`), state at `paths.EdgeStatePath`, listening on a high port (default `127.0.0.1:19443`).
* Privileged helper: the root LaunchDaemon `dev.scenery.edge-helper` that forwards privileged ports to the edge. Constants at the top of `cmd/scenery/edge.go` (`edgeHelperLabel`, `edgeHelperBinaryPath`, `edgeHelperPlistPath`).
* Target metadata: `paths.EdgeTargetPath` JSON (`localagent.EdgeTargetState`) the helper re-reads and re-validates on every connection (`validateEdgeTarget` in `cmd/scenery/edge.go`).
* Session: a live dev runtime registered with the agent (`internal/agent/types.go` `Session`, with `RouteManifest` from plan 0090).
* Deploy registry (new): machine-level JSON at `<agent home>/deploy.json` mapping public domains to app roots, written by `scenery deploy enable|disable`.

Key files:

* `cmd/scenery/edge.go` — all edge/DNS/privileged-helper commands, Caddyfile generation (`caddyEdgeConfig`), helper run loop (`edgePrivilegedHelperRun`), plist generation (`edgeHelperPlist`), status (`privilegedListenerStatus`).
* `cmd/scenery/edge_test.go`, `cmd/scenery/dev_edge_preflight.go` — existing edge tests and readiness checks.
* `internal/agent/paths.go` — agent home layout; `internal/agent/edge.go` — edge state types and schema versions.
* `internal/agent/registry.go` (`RouteTargetForHost`, `routeHosts`), `internal/agent/router.go` (host dispatch, `handleTLSAllow`, path-mode dispatch from plan 0090), `internal/agent/session.go` (route manifests).
* `internal/app/root.go` — app config `Config` struct; this plan adds a `Deploy DeployConfig` section.
* `docs/schemas/scenery.config.v1.schema.json` — app config schema.
* `cmd/scenery/upgrade.go` — self-upgrade; touch point for the helper-drift notice.
* `docs/plans/0090-local-path-routing.md` — the path-routing model and the production manifest shape this plan reuses.

Traffic path after this plan:

```text
internet → router NAT → Mac 0.0.0.0:80/443 (root helper, dumb TCP forward)
        → 127.0.0.1:19080/19443 (Caddy: ACME TLS for public domains, redirect on 80)
        → agent router (host = public domain → enabled app root's live session)
        → path dispatch: / → deploy.root service, /api/ → API backend, /<frontend>/ → frontend
```

## Milestones

### Milestone 1: `deploy` app config surface

Add to `internal/app/root.go`:

```go
type DeployConfig struct {
    Domain string `json:"domain,omitempty"`
    Root   string `json:"root,omitempty"`
}
```

wired as `Deploy DeployConfig \`json:"deploy"\`` on `Config` (and in `MarshalJSON`, omitted when zero). Validation in the config loader and `scenery check`:

* `deploy.domain` must be a valid lowercase FQDN (reuse `normalizeRouteNamespaceHost` semantics), must not end in `.local.dev` or the configured local route base domain, must not be `localhost` or an IP.
* `deploy.root`, when set, must name the API backend (`api`) or a configured frontend; when unset and the app has exactly one frontend, that frontend is the implied root; when unset with zero or multiple frontends, `/` on the public domain serves a minimal 404/landing (not an error — `scenery check` emits an info diagnostic suggesting `deploy.root`).
* `deploy.root` may not name reserved segments (`console`, `dashboard`, `runtime`, `sync`, `__scenery`).

Update `docs/schemas/scenery.config.v1.schema.json`. Acceptance: `go test ./internal/app ./cmd/scenery`; a fixture config with `deploy.domain` passes `scenery check --json`; invalid domains produce pointed diagnostics.

### Milestone 2: machine deploy registry + `scenery deploy enable|disable|status`

New file `cmd/scenery/deploy.go` (plus `deploy_test.go`), command registered in `main.go`/`help.go`:

```text
scenery deploy setup    [--acme-email <email>] [--acme-ca production|staging] [--json]
scenery deploy status   [--json]
scenery deploy enable   [--app-root <path>] [--json]
scenery deploy disable  [--app-root <path>] [--json]
scenery deploy resume   [--json]
scenery deploy teardown [--json]
```

Registry file `<agent home>/deploy.json`:

```json
{
  "schema_version": "scenery.deploy.registry.v1",
  "acme_email": "p.brazdil@gmail.com",
  "acme_ca": "production",
  "targets": [
    {
      "domain": "onlv.dev",
      "app_root": "/Users/petrbrazdil/Repos/onlv",
      "root_service": "ui",
      "enabled": true,
      "created_at": "2026-07-07T00:00:00Z",
      "updated_at": "2026-07-07T00:00:00Z"
    }
  ]
}
```

`enable` resolves the app root, loads its config, requires `deploy.domain`, rejects a domain already claimed by a different app root (the error names the other root and says to `scenery deploy disable` there first), upserts the target, then (when machine setup is present) regenerates the Caddy config and gracefully reloads (Milestone 4) — enable/disable of one app must not drop connections to other apps. `disable` flips `enabled: false` and reloads. Both work before `scenery deploy setup` has ever run; they just record intent and `status` explains what is missing.

`status --json` (schema `scenery.deploy.status.v1`) reports: helper install/health incl. public binding and version, Caddy edge state, agent state, LaunchAgent presence, ACME CA/email, and per-target: domain, app root, enabled, live session (yes/no + session ID), cert presence (from Caddy storage), and the diagnostics from Milestone 7.

Acceptance: unit tests for registry CRUD, domain-conflict rejection, and status JSON shape with a fake registry; `go test ./cmd/scenery`.

### Milestone 3: privileged helper v2 (public binding + port 80 + versioned install)

Extend the helper without breaking the loopback-only install:

* `edgeHelperOptions` gains `Public bool`, `HelperVersion string`. `parseEdgeHelperArgs` accepts `--public`, `--helper-version`.
* `edgePrivilegedHelperRun`: when `--public`, listen set becomes `0.0.0.0:443`, `[::]:443`, `0.0.0.0:80`, `[::]:80`; otherwise today's loopback pair. Port-443 conns forward to the validated HTTPS target as today; port-80 conns forward to a new HTTP target.
* `localagent.EdgeTargetState` gains `HTTPTargetAddr string \`json:"http_target_addr,omitempty"\`` (additive; keep `EdgeTargetSchemaVersion` unless validation semantics change). `validateEdgeTarget` validates the HTTP target with the same rules (loopback, 19000–19999, owned live Caddy PID). No HTTP target in the file → port-80 conns are dropped (fail-closed, covers old edges).
* `scenery deploy setup` (non-root) shells to `sudo <exe> system edge privileged-helper install --public --helper-version <version> ...` exactly like `edgePrivilegedInstall` does today (binary copy to `/usr/local/libexec`, plist write, `launchctl bootstrap` + `kickstart`). It then runs the unprivileged parts: Milestone 4 edge restart, LaunchAgent install (Milestone 6), registry init. Setup preflight: if something that is not the Scenery helper already listens on 80 or 443, fail with the listener's process info and do not touch it.
* `privilegedListenerStatus` reports the listen set and helper version parsed from the plist.
* `scenery deploy teardown` reinstalls the helper in loopback-only mode (public exposure off, local HTTPS keeps working), removes the LaunchAgent, keeps the registry and certs.

Testing without root: run-loop and validation logic take listen specs as parameters; tests use high ports and a fake target. Plist/arg round-trip tests extend the existing `parseEdgeHelperPlistOptions` tests. Acceptance: `go test ./cmd/scenery`; manual sudo verification deferred to Milestone 10.

### Milestone 4: Caddy edge config v2 (public ACME sites)

Extend `caddyEdgeConfigOptions` with `PublicDomains []publicDomainSite` (domain, redirect target), `ACMEEmail`, `ACMECA`, `StorageDir`, `HTTPListenPort`. Generated Caddyfile shape when public targets exist:

```caddyfile
{
    default_bind 127.0.0.1
    auto_https disable_redirects
    local_certs
    on_demand_tls { ask <agent>/v1/tls/allow }
    admin unix//<socket>
    storage file_system <agent home>/edge/caddy-data
    email <acme email>
    http_port 19080
    https_port 19443
    servers { strict_sni_host on }
}

https://:19443 {
    # existing local on-demand internal-cert site, unchanged
    tls internal { on_demand }
    reverse_proxy <agent router> { ... X-Scenery-Edge-Token ... }
}

onlv.dev:19443 {
    tls { issuer acme }   # staging CA directory when acme_ca=staging
    reverse_proxy <agent router> {
        flush_interval -1
        header_up Host {host}
        header_up X-Forwarded-Proto https
        header_up X-Forwarded-Port 443
        header_up X-Scenery-Edge-Token <token>
        header_up X-Scenery-Public-Edge 1
    }
}

http://onlv.dev:19080 {
    redir https://{host}{uri} 308
}
```

Notes: `default_bind 127.0.0.1` keeps Caddy itself loopback-only (public reach is only via the helper). Public sites must bind the same 19443 listener as the local site (Caddy SNI-routes within one listener); verify `local_certs` global does not override per-site ACME issuers — if it does, restructure so the internal issuer is per-site on the local block instead of global, and record it in Surprises.

The one generator serves both `scenery system edge install` (no public targets → today's output modulo pinned storage) and deploy flows. Enable/disable regenerates and reloads via the admin socket (`caddy reload` semantics against `unix//<socket>`), falling back to full restart if the admin call fails. Cert presence for `status` is read from the storage dir.

Acceptance: golden-ish unit tests for generation with 0, 1, and 2 public domains and staging CA; reload-path test with a fake admin socket; `go test ./cmd/scenery`.

### Milestone 5: agent public host routing + containment

* New route kind `RoutePublic` (`internal/agent/types.go`). The agent loads the deploy registry (`<agent home>/deploy.json`) at start and re-reads it on registry change signal (a control-socket RPC `deploy/reload` invoked by `scenery deploy enable|disable`; simplest correct alternative: mtime check per lookup miss — decide during implementation and record).
* `RouteTargetForHost` fallback: exact-match an enabled deploy domain → find the newest live session whose `AppRoot` matches the target's app root → route kind `RoutePublic`. Enabled domain with no live session → serve minimal 503 "app is not running" (no Scenery branding leakage beyond a plain page). Unknown host → 404 as today.
* `RoutePublic` dispatch reuses plan 0090's path-mode machinery against the session's route manifest, but with a public manifest derived per request: `/` → `root_service` (from registry/config resolution), `/api/` → API backend, `/<frontend>/` → frontends, `/sync/` if present. Explicitly rejected on public hosts: console/dashboard kinds, `/runtime/*`, `/__scenery/*`, and any `X-Scenery-Session` path-mode header (public requests must never select arbitrary sessions).
* Requests only qualify as public when they carry the valid `X-Scenery-Edge-Token` and `X-Scenery-Public-Edge: 1` (set by Caddy, Milestone 4); a matching Host without the token is not routed publicly.
* WebSocket upgrades already pass through the TCP forwarder and Caddy `reverse_proxy` with `flush_interval -1`; router proxying must not buffer them differently for `RoutePublic`.

Security-focused router tests: public host cannot reach `/runtime/health`; public host cannot reach console; spoofed `X-Scenery-Session` on a public host is ignored; unknown domain 404; enabled-but-down 503; `/api/*` strips prefix identically to local path mode. Acceptance: `go test ./internal/agent`.

### Milestone 6: login resume

`scenery deploy setup` installs `~/Library/LaunchAgents/dev.scenery.deploy-resume.plist` (`RunAtLoad` true, `KeepAlive` false) running `<installed scenery path> deploy resume`. The path is `os.Executable()` at setup time; never a worktree-local harness build (guard: refuse to install a LaunchAgent pointing into `.scenery/harness`).

`scenery deploy resume` is idempotent: ensure agent running → ensure edge Caddy running with current config → for each enabled target whose app root exists, `scenery up --detach --app-root <root>` unless a live session already exists → print/JSON summary. Failures on one target do not stop the others. Logs to `<agent home>/deploy-resume.log`.

Acceptance: unit tests for plist generation and resume planning (which targets need action) with fakes; manual reboot test in Milestone 10.

### Milestone 7: diagnostics

In `scenery deploy status` (and summarized in `scenery doctor --json` as a new `deploy` section):

* Listener truth: ports 80/443 bound on wildcard (via `netstat -anv -p tcp` parse or Go probe), helper launchd state (`launchctl print system/dev.scenery.edge-helper`), helper version vs current binary.
* LAN IP (`ipconfig getifaddr en0 || en1`) and loopback/LAN self-probes of `http://<lan-ip>/` and `https://<lan-ip>/` (TLS verification off for the probe; we only test reachability).
* Public IP discovery + per-domain DNS A/AAAA lookup; mismatch between the domain's A record and the discovered public IP is reported with the exact fix ("point onlv.dev A record at 217.x.x.x" or "configure router to forward 80/443 to <lan-ip>"). CGNAT hint when public probe fails but LAN probe succeeds.
* Power posture: parse `pmset -g` sleep values; warn that sleep takes the site down, suggest `sudo pmset -c sleep 0` without running it.
* Firewall: `socketfilterfw --getglobalstate`; if enabled, note that `/usr/local/libexec/scenery-edge-helper` may need an allow entry.
* Cert status per domain from Caddy storage (exists, NotAfter).

All external calls happen only inside these explicit commands. Acceptance: unit tests with faked probe functions (follow the existing `edge*Func` seam pattern in `cmd/scenery/edge.go`).

### Milestone 8: upgrade story

* `scenery deploy status`/`doctor`: helper `--helper-version` (from plist) vs `version.Version` of the running binary → "helper is vX, current is vY" info; escalate to actionable warning only when the running binary's expected target-metadata schema differs from what the installed helper validates (tracked by a constant bumped on breaking helper changes).
* `scenery upgrade`: after a successful upgrade, if a deploy helper is installed and drift is actionable, print "run `scenery deploy setup` to update the privileged listener (asks for sudo)". Never auto-sudo.
* Document the invariant: the helper contract is the plist argument list + `EdgeTargetState` schema; changes to either require bumping the helper-contract constant and a re-setup.

Acceptance: unit test for the drift computation; upgrade-notice test alongside existing `upgrade_test.go` patterns.

### Milestone 9: docs and harness

Update in one change: `docs/local-contract.md` (new `scenery deploy` grammar, `scenery.deploy.registry.v1` / `scenery.deploy.status.v1` schemas, artifact paths, stability: beta), `docs/agent-guide.md` (how agents check/enable public exposure), `SKILL.md` (short: deploy exists, needs sudo setup, domain in config), `README.md` (human walkthrough), `docs/app-development-cookbook.md` (recipe: expose an app on your own domain, incl. router/DNS steps and the curl verification ladder from Surprises), `docs/schemas/scenery.config.v1.schema.json`, new schemas under `docs/schemas/`, `docs/knowledge.json`. No `docs/environment.md` changes (no env vars). Run `scenery harness self --summary --write`.

### Milestone 10: real-world acceptance (this Mac, real domain)

Operator + agent session on the target machine:

1. `scenery deploy setup --acme-ca staging --acme-email p.brazdil@gmail.com` (sudo password once). Verify `launchctl print system/dev.scenery.edge-helper`, `netstat -anv -p tcp | grep -E '\.(80|443) '` shows `*.80` / `*.443`.
2. Router: forward public 80/443 → this Mac's LAN IP; DNS A record for the test domain → public IP. `scenery deploy status` must go green on reachability + DNS.
3. In the app root: `scenery deploy enable`, `scenery up --detach`. Verify staging cert issuance in Caddy log, then the curl ladder: `curl http://localhost/` (redirects), `curl -k https://localhost/` (SNI mismatch → local site), `curl https://<domain>/api/...` from LAN and from a phone off-WiFi.
4. Flip to production CA (`scenery deploy setup --acme-ca production`), re-verify.
5. Containment spot-checks: `curl https://<domain>/runtime/health` → 404/403; console host unreachable publicly.
6. Reboot the Mac. Before login: connection to port 443 accepted-then-dropped or refused (record which). After login: site serves with no manual command. `scenery deploy status --json` green.
7. `scenery down` → domain serves 503 page; `scenery up --detach` → serves again. `scenery deploy teardown` → public unreachable, local `https://*.local.dev` still works.

## Plan of Work

Phase A (read-only): re-run source inventory on the implementation branch (`rg` for `edgeHelper`, `caddyEdgeConfig`, `RouteTargetForHost`, `RouteManifest`, `DeployConfig` collisions), read `docs/plans/0090-local-path-routing.md` outcomes for the current path-dispatch entry points, and check active plans 0048/0049/0079 for conflicts. Do not edit before this.

Phase B (models + pure functions first): Milestones 1–2 config/registry types, validation, and Caddyfile/plist generators with unit tests. Everything testable without root, network, or Caddy.

Phase C (runtime wiring): Milestones 3–5 — helper run loop, agent routing, edge reload. Keep each landable: helper v2 with `--public` unused by default; agent `RoutePublic` behind absence of registry file.

Phase D (lifecycle + operator UX): Milestones 6–8 — resume, diagnostics, upgrade notices.

Phase E (docs + harness + real world): Milestones 9–10.

Implementation model note: bulk mechanical stages (schema plumbing, table-driven tests) suit codex (gpt-5.5) via the codex-implementation skill with worktree isolation; routing/security dispatch and the Caddyfile generator deserve fable/opus-level review before merge.

## Concrete Steps

1. Branch from `main`. Re-verify plan number 0101 is still free; if taken, take the next free number and update `active.md`/`knowledge.json` references.
2. Milestone 1: edit `internal/app/root.go` (add `DeployConfig`, wire into `Config`, `MarshalJSON`, validation), extend `docs/schemas/scenery.config.v1.schema.json`, add `scenery check` diagnostics near the existing proxy/frontend checks, tests in `internal/app` + a `testdata` fixture app with `deploy.domain`.
3. Milestone 2: new `cmd/scenery/deploy.go` + `deploy_test.go`; registry load/save with the same atomic-write style as `writeEdgeDNSState`; register the command in `main.go` and `help.go`.
4. Milestone 3: edit `cmd/scenery/edge.go` (`edgeHelperOptions`, `parseEdgeHelperArgs`, `edgePrivilegedHelperRun`, `edgeHelperPlist`, `privilegedListenerStatus`) and `internal/agent/edge.go` (`EdgeTargetState.HTTPTargetAddr`); keep loopback default; `deploy setup` orchestration in `deploy.go`.
5. Milestone 4: extend `caddyEdgeConfigOptions`/`caddyEdgeConfig`; pin `storage file_system`; add admin-socket reload helper; wire `edgeRestart` to include public targets from the registry so `scenery system edge install` and `scenery deploy` stay consistent.
6. Milestone 5: `internal/agent/types.go` (`RoutePublic`), `registry.go` (deploy-domain fallback + reload RPC), `router.go` (public dispatch + containment), tests in `internal/agent`.
7. Milestones 6–8 per their sections; diagnostics use injectable func vars like the existing `edge*Func` seams.
8. Milestone 9 docs sweep; `scenery harness self --summary --write`.
9. Milestone 10 on the real machine; append results (incl. Let's Encrypt staging output and the reboot observation) to Surprises & Discoveries and close out Outcomes & Retrospective.

## Validation and Acceptance

Default validation for every landed step (repo root):

```sh
go test ./...
go test ./cmd/scenery
go test ./internal/agent ./internal/app
```

For substantial steps: `scenery harness self --summary --write` (use the worktree-local `.scenery/harness/bin/scenery`; do not `go install` during agent validation). JSON sanity: `jq empty docs/knowledge.json docs/schemas/scenery.config.v1.schema.json`.

Feature acceptance is Milestone 10's script. The plan is done when: a real domain serves an app over public HTTPS with a Let's Encrypt production cert; two apps with two domains serve simultaneously through the one edge; reboot + login restores service untouched; containment checks pass; `scenery deploy status --json` reflects all of it truthfully; and docs/schema layers are updated together.

## Idempotence and Recovery

* `scenery deploy setup` is rerunnable: it re-copies the helper binary, rewrites the plist, `bootout` + `bootstrap` + `kickstart` (the existing install already tolerates re-runs), reinstalls the LaunchAgent, and regenerates/reloads Caddy. Re-running after a failed sudo leaves the previous install intact.
* `enable`/`disable` are upserts on the registry followed by config regen + graceful reload; a crash between registry write and reload is healed by the next `resume`/`status --fix`-style regen (resume always regenerates from the registry).
* If Caddy fails to reload a new config, keep the old config file on disk until the reload succeeds (write to `Caddyfile.next`, reload from it, then rename) so a bad domain cannot brick local HTTPS; on failure surface Caddy's error verbatim.
* Helper down or Caddy down → fail-closed (dropped connections), never a root-served response. `resume` and `status` both detect and repair/instruct.
* ACME failures (DNS not propagated, port not reachable) must not loop hot: Caddy's own backoff applies; `status` shows the cert error from the Caddy log. Use `--acme-ca staging` for all experiments to protect production rate limits.
* `teardown` never deletes certs or the registry, so re-`setup` restores service without re-issuance.
* Recovery from a wedged launchd state: `sudo launchctl bootout system/dev.scenery.edge-helper` then `scenery deploy setup` — document in the cookbook.

## Artifacts and Notes

New/changed committed artifacts:

```text
docs/plans/0101-public-deploy-edge.md          (this file)
docs/plans/active.md                            (link)
docs/knowledge.json                             (index entry)
cmd/scenery/deploy.go, deploy_test.go
cmd/scenery/edge.go, edge_test.go               (helper v2, Caddyfile v2)
internal/agent/edge.go                          (EdgeTargetState.HTTPTargetAddr)
internal/agent/types.go, registry.go, router.go (RoutePublic)
internal/app/root.go + tests                    (DeployConfig)
docs/schemas/scenery.config.v1.schema.json
docs/schemas/scenery.deploy.registry.v1.schema.json
docs/schemas/scenery.deploy.status.v1.schema.json
docs/local-contract.md, docs/agent-guide.md, SKILL.md, README.md,
docs/app-development-cookbook.md
testdata fixture app with deploy.domain
```

Machine-local artifacts (never committed):

```text
/Library/LaunchDaemons/dev.scenery.edge-helper.plist   (rewritten with --public)
/usr/local/libexec/scenery-edge-helper                 (root copy of scenery)
~/Library/LaunchAgents/dev.scenery.deploy-resume.plist
<agent home>/deploy.json
<agent home>/edge/Caddyfile
<agent home>/edge/caddy-data/                          (ACME account + certs)
<agent home>/deploy-resume.log
```

Out of scope for this plan (record here so nobody scope-creeps silently): dynamic DNS updating, router/UPnP configuration, Linux support (code may compile; docs must say untested), multiple domains or wildcard subdomains per app, a production serve runtime, exposing the Scenery console publicly, and any authentication gateway in front of the app (apps use their own auth).

## Interfaces and Dependencies

CLI (new, all beta): `scenery deploy setup|status|enable|disable|resume|teardown [--json]`; `setup` flags `--acme-email`, `--acme-ca production|staging`. Extended (internal): `scenery system edge privileged-helper install|run` gains `--public`, `--helper-version`, and HTTP-target awareness.

JSON contracts (new): `scenery.deploy.registry.v1`, `scenery.deploy.status.v1`; additive field `http_target_addr` on the edge target state; app config gains `deploy.domain` / `deploy.root`.

HTTP: public domains serve `/` (root service), `/api/*`, `/<frontend>/*`, `/sync/*`; explicitly not `/runtime/*`, console, dashboard. Headers to backends match local path mode plus `X-Forwarded-Proto: https`, `X-Forwarded-Port: 443`. Trusted edge-internal headers: existing `X-Scenery-Edge-Token` plus new `X-Scenery-Public-Edge`.

Dependencies: no new Go modules. Caddy (existing managed toolchain artifact) gains ACME duty; dnsmasq is untouched by this plan. launchd (`launchctl`), `ipconfig`, `netstat`, `pmset`, `socketfilterfw` are consulted on macOS only.

Interaction with active plans: builds directly on 0090 (path dispatch, production manifest shape); respects 0048 (non-destructive restarts — graceful Caddy reloads); 0079 (Victoria substrate visibility — deploy status must attribute the shared edge clearly); does not conflict with 0100/0098/0099. If 0090's deferred "production route manifest" work starts later, it should consume `DeployConfig` rather than reintroducing `deploy.routing`.
