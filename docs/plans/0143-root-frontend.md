# 0143 Root Frontend: Serve One Declared Frontend At `/` On Every Surface

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as implementation proceeds.

## Purpose / Big Picture

An app root can declare one of its configured frontends as the app's **root
frontend**. That frontend is then served at `/` — with clean root-relative
URLs — on every surface that serves the app:

- the local path-mode base URL (`http://localhost:<port>/` from `scenery up`),
- dev domains bound to a path-mode session,
- public deploy domains proxied by the agent (`scenery deploy` targets whose
  frontends run in `serve: "development"` mode),
- the managed public edge serving published production frontends from disk.

Today no such concept exists for the live runtime. Every frontend is mounted
at `/<name>/`, the frontend dev server is started with that base path, and `/`
either renders the built-in "Scenery: services" index page (path mode) or
bounces visitors to `/<name>/`. Concretely, opening `https://local.clean.tech/`
(a deploy target for the onlv app, root service `next`) returns
`302 Location: /next/` — the redirect comes from the **Vite dev server itself**,
because Scenery starts it with `--base /next/` and Vite redirects requests
outside its base. The user-visible URL is permanently uglified with `/next/`.

After this plan, the declared root frontend is served **only** at `/` (its
`/<name>/` mount ceases to exist), other frontends keep `/<name>/`, and apps
without a declared root keep today's behavior exactly.

## Progress

- [x] Milestone 1 — Config surface: top-level `root`, `deploy.root` removed (2026-07-23)
- [x] Milestone 2 — Agent routing: root frontend owns `/` and unmatched paths (2026-07-23)
- [x] Milestone 3 — Dev serve: root frontend runs at base `/` (2026-07-23)
- [x] Milestone 4 — Publish + managed edge: base-`/` build, root-only mount (2026-07-23)
- [x] Milestone 5 — Docs, validation sweep, client migration notes (2026-07-23)

2026-07-23: Plan drafted from a live diagnosis of `local.clean.tech` (see
Surprises & Discoveries). No implementation yet.

2026-07-23: Implemented milestones 1–4 plus the living-doc and ONLV config
migration portions of milestone 5. Focused validation is running before the
runtime acceptance and completed-plan move.

2026-07-23: Completed the focused and repository validation suites, the full
self-harness, the ONLV consumer harness, and matched-binary runtime acceptance.
Moved the plan to the completed index.

## Surprises & Discoveries

- 2026-07-23 — The `/` → `/next/` `302` on `local.clean.tech` is not emitted by
  any Scenery code path. Evidence: replaying the edge request directly against
  the agent router (`curl -H "Host: local.clean.tech" -H "X-Scenery-Edge-Token:
  ..." -H "X-Scenery-Public-Edge: 1" http://127.0.0.1:9440/`) returns
  `302 Location: /next/` with `Vary: Origin`, while every redirect in
  `internal/agent/router.go` is a `301` for missing-trailing-slash only. The
  `302` is proxied from the frontend backend: `scenery up` starts Vite with
  `--base /next/` (`managedFrontendBasePath`, `cmd/scenery/dev_frontends.go`),
  and Vite answers requests outside its base with a `302` to the base.
- 2026-07-23 — `deploy publish` builds even the root-service frontend with base
  `/<name>` (`runDeployPublishBuild(frontendRoot, "/"+name, ...)` in
  `cmd/scenery/deploy_publish.go`), while registering its route as `/` and
  rendering the Caddy site with `OwnsRoot`. The published SPA therefore loads
  at `/` but self-navigates back into `/<name>/...` URLs.
- 2026-07-23 — In path mode, `routeForPath` (`internal/agent/router.go`)
  matches the `root` record only for the exact path `/`. On a public deploy
  domain whose frontends are agent-proxied (development serve), SPA deep links
  like `/projects/42` match no record and 404.
- 2026-07-23 — The localhost listener has a small in-process router in front
  of the agent. It already delegates unmatched paths to the agent, so the new
  root catch-all required no second routing implementation there; explicit
  non-root frontend and dashboard fast paths remain unchanged.
- 2026-07-23 — `SCENERY_PUBLIC_APP_URL` previously selected the first named
  frontend and skipped `root`. Once the root frontend loses its named route,
  that would advertise a sibling frontend. It now selects the frontend-kind
  root record first.
- 2026-07-23 — The parallel-worktree self-harness fixture had one frontend and
  therefore became a root-frontend fixture under the existing single-frontend
  default. Its assertion still looked for the retired `web` named route. The
  harness now asserts two distinct session-scoped `root` URLs, the `web`
  backend identity, and absence of duplicate named mounts.

## Decision Log

- 2026-07-23 (petr + agent) — Root declaration is a new **top-level**
  `.scenery.json` field `"root": "<frontend-name>"`, applying to every env and
  surface. `envs.<env>.deploy.root` is removed, not aliased (no-legacy policy:
  one spelling). `deploy setup`/`deploy publish` read the top-level field.
- 2026-07-23 (petr + agent) — The root frontend is served **only at `/`**. Its
  `/<name>/` mount, the Caddy `redir /<name> ...` + `handle_path /<name>/*`
  blocks, and the `/<name>/` route record all disappear for that frontend.
  Other frontends keep `/<name>/` mounts.
- 2026-07-23 (agent, revisit during implementation) — Keep the existing
  single-frontend default from `deployRootService`: an app with exactly one
  configured frontend treats it as the root frontend without an explicit
  `"root"` entry. This now also applies to local dev (the base URL serves the
  app instead of the services index). Rationale: "open the IP with port and no
  path → the root app is served" is the requested behavior and a single
  frontend is unambiguous. If this surprises existing multi-surface apps in
  practice, record it here and require the explicit field instead.
- 2026-07-23 (agent) — Apps with no root frontend (zero or several frontends,
  no `"root"`) keep the current services index at `/` in path mode. The index
  is a debugging surface, not a product surface; it stays for the no-root case
  only.

## Outcomes & Retrospective

Completed 2026-07-23.

Scenery now has one current root-frontend contract. Top-level
`.scenery.json` `root` names the frontend that owns `/` across local path-mode
development, domain routing, agent-proxied deploy targets, publish builds, and
managed static edge output. An unambiguous single frontend remains the
default. The root frontend is not duplicated at `/<name>/`; sibling
frontends retain their named mounts. API, runtime, dashboard, and sibling
frontend routes outrank the root SPA catch-all.

ONLV migrated from `envs.production.deploy.root` to top-level
`"root": "next"`. A matched 0143 binary and isolated agent proved the live
route manifest contains `root -> next` with no `next` named route. On the
running app, `/`, `/projects`, `/pulse/`, `/blog/`, `/ui/`, `/console/`, and
`/runtime/health` returned the expected responses without restarting the app
between route checks; `/api/` remained owned by the API surface rather than
falling through to the SPA. Replaying public-edge headers directly against
that matched agent proved `/` and `/projects` serve the root frontend while
`/pulse/` remains a sibling frontend and protected API/runtime paths do not
fall through.

Validation completed:

- `go test ./internal/app ./internal/agent ./internal/edge ./cmd/scenery`
- `go test ./...`
- `.scenery/harness/bin/scenery harness self --summary --write`
- ONLV `scenery check -o json`, `go test ./...`, and
  `scenery harness -o json --write`

No generated TypeScript runtime change was needed. The deploy registry shape
remains unchanged; only app config grammar and routing behavior changed.

## Context and Orientation

Definitions used throughout:

- **App root**: a directory containing `.scenery.json`. The onlv example
  (`/Users/petrbrazdil/Repos/onlv/.scenery.json`) declares four frontends:
  `pulse`, `blog`, `ui`, `next`, and a production env with
  `domain: local.clean.tech`, `deploy: { root: "next", ssh: [...] }`.
- **Frontend**: a named entry under top-level `frontends` in `.scenery.json`
  (`internal/app/root.go`, `FrontendConfig`). In dev serve mode Scenery spawns
  its dev server (Vite) on a loopback port; in production serve mode it serves
  the built `dist/` directory.
- **Path mode**: the default local routing mode. One base URL per session
  (e.g. `http://localhost:4488`); routes are path prefixes. Route records live
  in the session `RouteManifest` (`internal/agent/types.go`), completed by
  `completePathRouteRecords` (`internal/agent/session.go`): `root` (currently
  always kind `scenery-console`), `api`, `dashboard`, and one `/<name>/`
  record per frontend backend.
- **Agent router**: `internal/agent/router.go`. `handlePathModeRoute`
  dispatches by `routeForPath`; the `root` record goes to
  `handlePathModeRoot`, which renders the plain-HTML services index.
  `handlePublicEdgeRoute` → `handlePublicPathRoute` serves public deploy
  domains: `publicRouteManifest` binds `root` to the deploy target's
  `root_service` backend when set.
- **Deploy target**: an entry in the agent's deploy registry
  (`~/.scenery/agent/deploy.json`, `internal/agent/deploy.go`) created by
  `scenery deploy setup`, carrying `domain`, `app_root`, `root_service`, and —
  after `deploy publish` — published `frontends` records with a `root` flag.
- **Managed edge**: the Caddy process rendered by
  `internal/edge/caddyconfig.go`. Public domain sites serve published
  production frontends statically (`StaticFrontendRoute`, `OwnsRoot` serves a
  frontend additionally at `/`); everything else proxies to the agent router.
- **Frontend dev server base**: `managedFrontendBasePath` +
  `managedFrontendViteBaseArg` (`cmd/scenery/dev_frontends.go`) pass
  `--base /<name>/` to Vite and export
  `SCENERY_FRONTEND_BASE_PATH`/`VITE_SCENERY_FRONTEND_BASE_PATH` to the
  process env, which generated TypeScript clients consume.

Current request flow for `https://local.clean.tech/` (the motivating bug):
Cloudflare → the Mac's managed edge (`~/.scenery/agent/edge/Caddyfile` site
`local.clean.tech:19443`) → agent router with `X-Scenery-Public-Edge: 1` →
`handlePublicPathRoute` with `root_service: next` → proxied to the `next`
Vite dev server (base `/next/`) → Vite responds `302 /next/`.

## Milestones

Each milestone leaves `go test ./...` green and is independently commitable.

1. **Config surface.** `.scenery.json` gains top-level `"root"`;
   `envs.<env>.deploy.root` is removed. `Config.RootFrontend()` resolves the
   explicit field or the single-frontend default. Validation: `root` must name
   a configured frontend. Observable: `scenery check -o json` rejects a bad
   name with a clear diagnostic; a config still using `deploy.root` fails with
   the strict-decode unknown-field error naming
   `envs.<env>.deploy.root` (verify the message is actionable; if the generic
   unknown-field text does not mention the replacement, add an explicit
   rejection that does).
2. **Agent routing.** A `root` route record may carry a frontend backend. In
   `routeForPath`, a root record with a frontend backend is the
   lowest-precedence catch-all (all paths not claimed by `/runtime`,
   `/dashboard`, `/api/`, or another frontend's prefix), so SPA deep links
   resolve; without a frontend backend it keeps exact-`/` matching and the
   services index. `handlePathModeRoute` proxies a frontend-backed root with
   SPA fallback and protected-path filtering (same treatment as
   `isFrontendSessionBackend` routes); `handlePathModeRoot` renders the index
   only for console-kind roots. `publicRouteManifest` gets the same catch-all
   semantics, fixing public deep-link 404s. `completePathRouteRecords` must
   not emit a `/<name>/` record for the root frontend.
3. **Dev serve.** Session route records built by `scenery up` bind the root
   frontend to the `root` record (path `/`), skip its `/<name>/` mount, and
   `managedFrontendBasePath` returns `/` for it (Vite gets no `--base`;
   `SCENERY_FRONTEND_BASE_PATH=/`). Observable: with onlv running,
   `curl -s -D- http://localhost:<port>/` returns the app's HTML `200` (no
   redirect, no services index), `curl http://localhost:<port>/pulse/` still
   serves pulse, and a deep link like `/projects` returns the SPA shell.
4. **Publish + managed edge.** `deploy publish` builds the root frontend with
   base `/` (`runDeployPublishBuild(frontendRoot, "/", ...)` when it is the
   root frontend); `writePublicDomainSite` emits only the catch-all `handle`
   for the `OwnsRoot` frontend (no `redir /<name>` / `handle_path /<name>/*`
   for it). Observable: caddyconfig golden tests; on a published domain `/`
   serves `index.html` whose asset URLs are root-relative (`/assets/...`).
5. **Docs + sweep.** Contract and guide docs updated (see Interfaces and
   Dependencies), validation matrix commands run, client-repo migration note
   recorded.

## Plan of Work

Milestone 1 — `internal/app`:

- Add `Root string \`json:"root"\`` to `Config` (and to the `configJSON`
  mirror in `MarshalJSON`). Delete `Root` from `EnvDeployConfig`. Add
  validation: non-empty `root` must match a key of `frontends`; error text
  `root must name a configured frontend (one of: ...)`.
- Add `Config.RootFrontend() string`: explicit `Root` if set, else the single
  configured frontend's name if `len(Frontends) == 1`, else `""`.
- Rewrite `deployRootService` (`cmd/scenery/deploy.go`) to
  `cfg.RootFrontend()`; update `deploy_publish.go`, `harness_schema.go`
  examples, and every test fixture using `deploy: {"root": ...}`.

Milestone 2 — `internal/agent`:

- `RouteRecord` needs no new fields: a frontend-backed root is
  `records["root"] = {Name: "root", Kind: "frontend", Path: "/", Backend: <name>}`.
  Teach `backendForRouteName`/`normalizeRouteRecords` not to erase the root
  record's backend (today `backendForRouteName("root")` forces `""`; keep
  a record-provided backend, only default to `""`).
- `routeForPath`: when the root record's kind is `frontend`, treat it as a
  match for any path (score below every prefixed record); keep exact-`/`
  matching otherwise.
- `handlePathModeRoute`: route a frontend-kind root through the existing
  frontend branch (redirect guard is a no-op for path `/`, protected-path
  filter, SPA fallback proxy). `handlePathModeRoot` remains for console-kind
  roots only.
- `completePathRouteRecords`: skip emitting `/<name>/` for a backend already
  bound as the root record's backend; still default `records["root"]` to the
  console index when absent.
- `publicRouteManifest`: same skip; root record kind is already
  `publicRouteKind`. Verify `filterExposedRouteRecords` + `expose` validation
  (`devExposeRouteNames`, `cmd/scenery/dev_routing.go`) behave: exposing
  `root` exposes the root frontend.

Milestone 3 — `cmd/scenery`:

- Wherever the session's initial route records are built for `scenery up`
  (dev session controller / supervisor writing `RouteManifest.Routes`), bind
  `cfg.RootFrontend()` to the root record and drop its named mount.
- `managedFrontendBasePath`: return `/` when the frontend is the app's root
  frontend (thread the app config or the resolved root name into the call
  sites; the session route record for the frontend no longer exists, so the
  current `routeBasePath` lookup will fall through — make the decision
  explicit rather than accidental).
- Confirm `frontendDevEnv` exports `SCENERY_FRONTEND_BASE_PATH=/` and the
  generated TypeScript runtime treats `/` correctly (router basename, asset
  URL joining). If `internal/generate` output changes, regenerate both
  committed fixture clients (commands in AGENTS.md) in the same change.
- Production serve mode (`startProductionFrontendServer`,
  `cmd/scenery/dev_frontend_production.go`): the root frontend's static
  server must serve a base-`/` build; verify no `/<name>/` assumptions.

Milestone 4 — `internal/edge` + `cmd/scenery/deploy_publish.go`:

- `runDeployPublishBuild` base argument: `/` for the root frontend.
- `writePublicDomainSite`: for the `OwnsRoot` route, emit only the final
  catch-all `handle`; do not emit its `redir` + `handle_path` mounts. Update
  golden/behavior tests in `internal/edge`.
- Deploy registry schema (`scenery.deploy.registry`) is shape-stable
  (`root_service`, `frontends[].root` already exist); no schema change
  expected — assert with the quick self-harness.

Milestone 5 — docs and sweep: see Interfaces and Dependencies; run the full
Validation and Acceptance list; write the client migration note.

## Concrete Steps

Work in a worktree; use the worktree-local harness binary.

    cd /Users/petrbrazdil/Repos/scenery
    git worktree add ../scenery-0143 -b 0143-root-frontend
    cd ../scenery-0143
    # fresh worktree preflight: docs/agent-guide.md § Fresh Worktree Preflight

Per milestone:

    go test ./internal/app          # M1
    go test ./internal/agent        # M2
    go test ./cmd/scenery           # M1–M3
    go test ./internal/edge         # M4
    go test ./...                   # before each commit

Fixture regeneration (only if `internal/generate` output changes):

    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json

Real-process verification against onlv (after M3), driven from the onlv
worktree with this branch's binary:

    go build -o /tmp/scenery-0143 ./cmd/scenery
    cd /Users/petrbrazdil/Repos/onlv   # after applying the config migration below
    /tmp/scenery-0143 up --detach --wait ready
    curl -s -D- -o /dev/null http://localhost:<port>/            # expect 200 app HTML, no Location header
    curl -s -D- -o /dev/null http://localhost:<port>/pulse/      # expect 200 pulse
    curl -s -D- -o /dev/null http://localhost:<port>/projects    # expect 200 SPA shell (deep link)
    curl -sk -D- -o /dev/null --resolve local.clean.tech:443:127.0.0.1 https://local.clean.tech/   # expect 200, no 302

onlv config migration (client repo, coordinated change):

    # .scenery.json: add top-level  "root": "next"
    # .scenery.json: delete  envs.production.deploy.root  (keep "ssh")

## Validation and Acceptance

Validation matrix rows that apply: One Go package (several), CLI JSON
contract, Compiler or generator (only if generated TS runtime changes),
Release-sensitive or runtime.

- `go test ./internal/app ./internal/agent ./internal/edge ./cmd/scenery`,
  then `go test ./...`.
- `.scenery/harness/bin/scenery harness self --summary --write` (full: this
  touches runtime routing; its real-process steps are required proof).
- Acceptance (behavioral):
  1. onlv with `"root": "next"`: base URL `/` serves the next app `200`
     directly; no `302`, no `/next/` in the address bar after navigation.
  2. `/pulse/`, `/blog/`, `/ui/` unchanged; `/api/`, `/runtime`, `/dashboard/`
     unchanged and take precedence over root catch-all.
  3. `https://local.clean.tech/` serves the app at `/` (dev-serve deploy
     target path), and deep links no longer 404.
  4. `scenery deploy publish` output: root frontend built with base `/`;
     rendered Caddyfile has no `/next` mounts; published site serves `/`.
  5. An app with no root frontend and ≥2 frontends: `/` still renders the
     services index (regression guard).
  6. `scenery check -o json` on a config with `root: "nope"` fails with the
     named diagnostic; a config with `envs.*.deploy.root` fails strict decode.

## Idempotence and Recovery

All steps are ordinary source edits plus cached tests; re-running any command
is safe. `scenery up --detach` sessions are stopped with `scenery down` (or
kill the supervisor) before retrying. The onlv config migration is a two-line
edit, revertible with git. If a milestone lands partially, the strict config
decode (M1) is the only step that breaks existing app roots still using
`deploy.root`; land M1 and the onlv config edit in the same working session.
Deploy registry entries on other machines (`root_service`) remain valid; they
are re-derived on the next `deploy setup`/`publish`.

## Artifacts and Notes

Diagnosis evidence (2026-07-23):

    $ curl -skI https://local.clean.tech/
    HTTP/2 302
    location: /next/
    via: 1.1 Caddy

    $ curl -s -D- -H "Host: local.clean.tech" \
        -H "X-Scenery-Edge-Token: <token>" -H "X-Scenery-Public-Edge: 1" \
        http://127.0.0.1:9440/
    HTTP/1.1 302 Found
    Location: /next/
    Vary: Origin          # ← Vite dev server (base /next/), not Scenery code

Mac deploy registry (`~/.scenery/agent/deploy.json`) target:
`domain local.clean.tech`, `app_root /Users/petrbrazdil/Repos/onlv`,
`root_service next`, enabled — created by `scenery deploy setup` on 2026-07-07.

## Interfaces and Dependencies

Changed public contracts (update in the same change as code):

- `.scenery.json` grammar: new top-level `root`; `envs.<env>.deploy.root`
  removed → `docs/local-contract.md` (config section) and any
  `docs/schemas/*` file that embeds the app-config shape; routing semantics
  (root frontend owns `/` and unmatched paths) → `docs/local-contract.md`
  routing section.
- Repo mental model line "React-enabled clients ... domain-specific UI stays
  in app-owned slots" is unaffected, but `docs/agent-guide.md` § Working In
  The scenery Repository and the runtime/deploy lifecycle prose must describe
  the root frontend; root `AGENTS.md` Mental Model bullet mentioning typed
  endpoints/page macros does not change.
- `SKILL.md`: agents working inside target apps must know that the root
  frontend serves at `/` and that `/<name>/` does not exist for it.
- `README.md` / `docs/app-development-cookbook.md`: config examples showing
  `deploy.root` move to top-level `root`.
- `docs/plans/active.md` + `docs/knowledge.json`: this plan (done at draft
  time).
- Client repos (onlv `AGENTS.md`): record the app-specific fact that `next`
  is the root frontend and the generated client base path is `/`.

Dependencies: none beyond the existing toolchain. Vite's base handling is the
only third-party behavior relied on (`--base` omitted ⇒ `/`), already the
default. The stylex base shim in onlv's `apps/next/vite.config.ts` becomes
inert (its base-rewrite branch only activates for non-`/` bases) and can be
removed by the client later; not part of this plan.
