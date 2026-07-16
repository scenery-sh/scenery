# 0121 Named Environments: `envs` Config, Per-Env Domain/Serve/Deploy, Dotenv Layering

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

## Purpose / Big Picture

`.scenery.json` today encodes a hardcoded two-environment split without naming
it: `dev.*` is the implicit local environment and `deploy.*` is the implicit
production environment, with `frontends.<name>.serve` read by both. Diagnosing
the `github.com/pbrazdil/Micro` platform app on 2026-07-16 surfaced three
concrete defects of that shape:

1. **Localhost redirects to production.** The local path router's redirect
   target selection (`cmd/scenery/dev_session_controller.go:332`) falls back to
   `https://<deploy.domain>` whenever no validated `dev.routing.domain` URL is
   available — both when `dev.routing.domain` is absent and when it is
   configured but the edge probe fails (`cmd/scenery/dev_edge_preflight.go:97`
   returns an empty URL on host conflict, edge unreadiness, or probe failure).
   Measured: with only `deploy.domain: "platform.onegraph.dev"` configured,
   `http://localhost:4218/platform/` answered
   `307 Location: https://platform.onegraph.dev/platform/`. Local dev traffic
   must never be redirected to production.
2. **`serve: "production"` conflates two decisions.** One field selects both
   the local dev serving mode (`cmd/scenery/dev_frontends.go:204` — HMR dev
   server vs build-once static serving) and the deploy static-publication gate
   (`cmd/scenery/deploy_publish.go:197`). An app cannot have an HMR dev server
   locally while keeping edge static publication on deploy.
3. **No environment axis for values.** `--env <name>` already exists on
   `scenery worker`, `scenery task run`, and `scenery db seed`, and fixtures
   already select by environment name, but `.scenery.json` has no environment
   concept and dotenv loading knows only `.env` + `.env.local`. Running one app
   in more than one environment shape on the same machine is inexpressible.

This plan replaces the implicit split with **named environments**: an
`envs: {}` map in `.scenery.json` where each environment declares its browser
origin, per-frontend serve modes, routing details, and (for deployable
environments) its deploy targets; plus per-environment dotenv file layering
(`.env`, `.env.<env>`, `.env.local`, `.env.<env>.local`). Environment *names*
select configuration and files; *values* (secrets) never enter `.scenery.json`
and stay machine-owned.

Target config shape (driving app `Micro/platform`; also applies to
`github.com/pbrazdil/onlv`):

```json
{
  "name": "microgrid-platform",
  "frontends": {
    "platform": { "root": "apps/platform" }
  },
  "envs": {
    "local": {
      "default": true,
      "domain": "micro.scenery.sh",
      "frontends": { "platform": { "serve": "development" } }
    },
    "production": {
      "domain": "platform.onegraph.dev",
      "frontends": { "platform": { "serve": "production" } },
      "deploy": { "root": "platform", "ssh": ["onlv-209"] }
    }
  },
  "dev": { "services": { "postgres": {} } }
}
```

This is a hard cutover under the no-legacy rule: top-level `deploy.*`,
`dev.routing.*`, and the dual-purpose global reading of `frontends.<name>.serve`
are removed in the same change, with `scenery check` rejecting the old shape
loudly (unknown-field diagnostics already carry exact JSON field paths).

## Progress

- [x] (2026-07-16) Plan authored.
- [x] (2026-07-16) M1 config schema and environment resolution in `internal/app`.
- [x] (2026-07-16) M2 dev session and local routing consume the resolved environment.
- [x] (2026-07-16) M3 deploy consumes env-scoped targets; publication gates on the env.
- [x] (2026-07-16) M4 per-environment dotenv layering.
- [x] (2026-07-16) M5 environment policy: `SCENERY_ENV` injection, secret strictness.
- [x] (2026-07-16) M6 docs cutover across all layers plus fixture and client migration.
- [x] (2026-07-16) M7 full Go suite, self-harness, and Micro/platform live verification.

## Surprises & Discoveries

- (2026-07-16) The deploy-domain fallback fires even when `dev.routing.domain`
  IS configured but its edge probe fails — `validateDevDomainURL` returns
  `("", warning)` and the caller falls through to the `cfg.Deploy.Domain`
  branch. So a degraded local edge silently redirects developer traffic to
  production. Evidence: `cmd/scenery/dev_session_controller.go:332-337` with
  `cmd/scenery/dev_edge_preflight.go:97-115`.
- (2026-07-16) On the Micro/platform machine, `https://micro.scenery.sh/platform/`
  served 200 while the on-disk config had no `dev.routing.domain` — the agent
  router held a route registration from an earlier session. Routing config is
  read at session registration; config edits require a session restart to take
  effect. Worth keeping in mind when verifying M2.
- (2026-07-16) The dotenv loader already accepts a variadic file-name list
  (`envfile.MergeFiles(root, names...)` via `appEnvWithDotEnv`,
  `cmd/scenery/dev_supervisor.go:1083`), so per-env layering is mostly a
  call-site change, not new machinery.
- (2026-07-16) Live production verification found that later supervisor and
  frontend-restart registrations omitted the environment and therefore erased
  it after the initial registration. The registration updates now preserve the
  resolved environment, with a focused restart/status test.
- (2026-07-16) The production host correctly failed closed until its
  machine-owned `JWT_SECRET` and `AUTH_TOKEN_CIPHER_KEY` existed. It also
  exposed a release-boundary fact: Micro's checked-in `scenery.sh` module
  predates its generated CRUD-list adapter, so the acceptance run used the
  exact synced Scenery source as a remote module replacement. A published
  Scenery revision removes that acceptance-only replacement.

## Decision Log

All decisions 2026-07-16, owner (Petr Brazdil) with Claude, from the design
conversation that produced this plan.

- **Introduce `envs: {}`** rather than patching the two narrower defects
  individually (a redirect-fallback fix and a separate `publish` field). The
  env axis already half-exists (`--env` flag, fixture `environments`,
  the `production` magic string in `cmd/scenery/appenv.go:15`); config should
  name it once instead of accreting more implicit halves.
- **Constrained overlay, not free-form config-per-env.** Only a whitelisted,
  legitimately env-varying key set is allowed inside an env block: `default`,
  `domain`, `frontends.<name>.serve`, routing details (`expose`, port
  selection), and `deploy`. Everything else (`name`, `frontends.<name>.root`,
  services topology, storage) stays top-level and env-invariant so one app
  model holds across environments. Unknown keys inside env blocks are rejected
  like all unknown config fields.
- **`deploy` lives inside the env** (`envs.<name>.deploy.{root,ssh}`), not as a
  sibling section. An environment is "a complete place the app runs", so its
  targets belong to it. Invariants: an env is deployable iff it declares
  `deploy`; one SSH target may appear in at most one env (validated);
  `scenery deploy <ssh-target>` reverse-looks-up the unique owning env so the
  existing CLI grammar keeps working; `scenery deploy --env <name>` selects by
  env and is unambiguous when the env has one target. Host provisioning
  (`scenery deploy setup`, systemd/launchd units, edge lifecycle) stays
  target-scoped, not env-scoped.
- **No `publish` field for now.** Within a single deployable env,
  `serve: "production"` means both "serve `dist/` statically" and "publish
  statically to the edge on deploy" — the same intent. The original objection
  to `serve` was its cross-environment leak, which env scoping removes. A
  deployable env whose frontend sets `serve: "development"` is rejected by
  `scenery check` (nothing to publish; no dev server on a production host).
  A `publish` field can be reintroduced later without disturbing this shape if
  runtime-served production frontends ever become a real case.
- **The default env is named `local`** (renamed from `dev` during design:
  "local" names where it runs, matching the env-as-place model). Exactly one
  env must set `"default": true`; `scenery up` uses it. `local` is a reserved
  name with one special property: its per-env dotenv file IS `.env.local`, so
  its stack degenerates to today's exact `.env` → `.env.local` behavior and
  `.env.local.local` does not exist.
- **Dotenv layering by env name**: load order `.env` → `.env.<env>` →
  `.env.local` → `.env.<env>.local`, later files winning among themselves,
  process environment winning over all (unchanged precedence rule). The
  generic `.env.local` machine-override layer loads for *every* env run on the
  machine (Vite convention: overrides are about the machine, not the env).
  Arbitrary env names are supported (`.env.xyz` for an env named `xyz`).
- **The env name picks which files load; the machine picks which files
  exist.** `.env.production` is read both by prod-shaped local runs
  (`scenery task run --env production`) on the developer machine and by the
  deployed runtime on the server — but each machine reads its own file. Deploy
  sync continues to honor `.gitignore` and preserve remote dotenv state, so
  local dotenv files never ship to the server. Real production secrets exist
  only on the production host (its `.env`/`.env.production` or the systemd
  unit environment); a local `.env.production` holds prod-shaped *test* values
  (e.g. a separate Google OAuth test client), never real production secrets.
- **Values never enter `.scenery.json`.** No `envs.<name>.env: {KEY: value}`
  inline map: it invites committed secrets, duplicates the dotenv channel, and
  grows env-var surface against repo policy. Non-secret per-env app
  configuration should flow through typed `.scn` inputs; `SCENERY_ENV` remains
  a last resort for app-side branching. Canonical env-var names stay fixed
  (e.g. `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET`,
  `AUTH_TOKEN_CIPHER_KEY` per `docs/environment.md`); the removed `*_env`
  renaming selectors stay removed.
- **Dotenv strictness becomes a property of the env declaration**, replacing
  the `production` string match in `cmd/scenery/appenv.go:15`: default/local
  envs require `.env` for local runs and warn on missing declared secrets;
  deployable envs treat all dotenv files as optional (process environment
  sufficient) and fail closed before startup when a declared secret resolves
  to nothing from any source.
- **`SCENERY_ENV`/`SCENERY_RUNTIME_ENV` are always injected** with the
  resolved env name (today they are set only when `--env` is passed). Ordinary
  `scenery up` sessions run as the default env (`local`); app code never
  handles an empty env name.
- **The deploy-domain localhost redirect fallback is removed entirely.** The
  local path router redirects only to the session env's validated domain URL
  when the edge probe passes; otherwise it serves localhost with the existing
  warning. No cross-env redirect target exists by construction.
- **Deploy status and the publication registry record the env name.** History
  and per-frontend serving mode group by env; each published release records
  which env produced it, so a future staging env on the same host cannot
  collide in the `deploy-artifacts` store.
- **`serve` mode names stay `development`/`production`.** They are serving
  modes, not env names; renaming `production`-the-mode to `static` was
  considered and deferred as cosmetic and separable.
- **Hard cutover, one PR-sized campaign.** No dual-read of old and new config
  shapes; `dev.routing.*` and top-level `deploy.*` are removed and rejected as
  unknown fields with exact JSON paths. Client apps (Micro/platform, onlv)
  migrate their `.scenery.json` in lockstep.

## Outcomes & Retrospective

Implemented the hard cutover to one named-environment model. Config discovery
now requires `envs.local.default`, resolves per-environment routing, frontend
serve modes, deploy ownership, and dotenv files, and rejects the removed
top-level deploy/routing/serve shapes. Dev sessions, workers, tasks, detached
runtime restarts, remote deploy, publication registries, and status surfaces
carry the resolved environment. Deploy artifacts are separated by environment,
and deployable environments fail closed on missing declared auth secrets.

Validation on 2026-07-16 passed `go test ./...` and
`.scenery/harness/bin/scenery harness self --summary --write`; all self-harness
lanes, including fixture, schema, drift, vet, Postgres, UI, and generated-client
checks, were green. Micro/platform ran locally with `SCENERY_ENV=local`, a live
Vite process, `http://localhost:4219/platform/` returning 200 without a
redirect, and `https://micro.scenery.sh/platform/` returning 200. Suspending
the managed edge made the domain unavailable while the localhost route stayed
200 with no redirect; resuming the edge restored the domain to 200.

Production verification on `onlv-209` recorded environment `production`, a
live ready session, and release `20260716T182845.990198394Z` under
`deploy-artifacts/microgrid-platform/production/platform`. Both the public
Cloudflare route and direct-origin SNI probe returned 200; the built document
contained no Vite/react-refresh markers and its hashed JavaScript asset carried
`Cache-Control: public, max-age=31536000, immutable`. Micro/platform currently
declares no Scenery tasks, so the exact four-file task layering was proven by
the focused loader test while live app and frontend processes independently
showed both injected environment variables as `local`.

## Context and Orientation

Terms:

- **Environment (env)**: a named, complete place the app runs — its browser
  origin, frontend serving modes, routing exposure, deploy targets, dotenv
  files, fixture selection, and secret strictness. Declared under `envs` in
  `.scenery.json`.
- **Default env**: the single env with `"default": true`; used by `scenery up`
  and any command run without `--env`.
- **Deployable env**: an env declaring `deploy: {root, ssh}`; eligible for
  `scenery deploy`, with fail-closed secret policy.
- **Dotenv stack**: the ordered optional files `.env`, `.env.<env>`,
  `.env.local`, `.env.<env>.local` in the app root of whatever machine is
  running the env.

Where the current behavior lives:

- Config structs: `internal/app/root.go` — `DevConfig` (line ~203),
  `DevRoutingConfig` (~208), `DeployConfig` (~217), `FrontendConfig.Serve`
  (~200). The JSON schema for `.scenery.json` and its unknown-field rejection
  live alongside; runtime diagnostics print exact field paths
  (`docs/local-contract.md:299`).
- Localhost redirect selection: `cmd/scenery/dev_session_controller.go:319-369`
  (`redirectURL` chosen from validated dev-domain URL, else
  `cfg.Deploy.Domain`); dev-domain validation
  `cmd/scenery/dev_edge_preflight.go:97` (`validateDevDomainURL`).
- Dev domain / exposure / serve modes shipped by plans 0116/0117:
  `cmd/scenery/dev_routing.go`, `cmd/scenery/dev_frontends.go`,
  `cmd/scenery/dev_frontend_production.go`. This plan reshapes their *config
  surface* (`dev.routing.domain` → `envs.<name>.domain` etc.); their runtime
  mechanics (dash-host naming, single-owner host claims, expose filtering,
  static frontend server) are kept as-is.
- Deploy publication gate: `cmd/scenery/deploy_publish.go:197`
  (`frontend.Serve == "production"`); deploy flow `cmd/scenery/deploy.go`,
  remote publication and registry per plan 0119; status surfaces
  `scenery deploy status`.
- Env name plumbing: `cmd/scenery/appenv.go` (`appProcessEnv`,
  `SCENERY_ENV`/`SCENERY_RUNTIME_ENV` injection, the `production` dotenv
  special case); dotenv loading `cmd/scenery/dev_supervisor.go:1083`
  (`appEnvWithDotEnv`/`appEnvWithRequiredDotEnv` over
  `internal/envfile.MergeFiles`).
- Fixture env selection: deployment projection and `scenery db seed --env`
  already select fixtures by `environments` (`docs/local-contract.md:76`);
  env names now come from the `envs` map, and `scenery check` can validate
  fixture env references against declared envs.
- Docs contract: `docs/local-contract.md` (config §299-306, dotenv §596-600,
  deploy routing §541), `docs/environment.md` (dotenv rules line 7, canonical
  auth names 69-86), `docs/agent-guide.md`, `SKILL.md`, `README.md`,
  `docs/app-development-cookbook.md`.

Related plans: 0116 (dev domain hosts), 0117 (exposure + serve modes), 0119
(Caddy-first production frontends), 0101 (public deploy edge). This plan
changes where their configuration lives, not what they do. Annotate 0116/0117
with a "config surface moved by 0121" note when M6 lands.

## Milestones

**M1 — Config schema and resolution (`internal/app`).** Add `EnvsConfig`:
`Envs map[string]EnvConfig` with `Default bool`, `Domain string`,
`Frontends map[string]EnvFrontendConfig{Serve string}`, routing details
(exposure, port selection — absorbing `DevRoutingConfig` fields that remain
meaningful), and `Deploy *EnvDeployConfig{Root string, SSH []string}`. Remove
`DevRoutingConfig` from `DevConfig` and top-level `DeployConfig`. Validation:
exactly one default env; `local` reserved-name rules; whitelisted keys only;
one SSH target in at most one env; deployable env frontends must not use
`serve: "development"`; fixture env references must name declared envs (warn
or error — decide during M1). Provide one resolution helper: given an env
name (or empty → default), return the effective per-env view (domain, serve
modes, deploy block, dotenv file list, strictness policy). `scenery inspect
app -o json` exposes the resolved envs.

**M2 — Dev session and local routing.** `scenery up [--env <name>]` resolves
the env (default `local`-style default env), uses `envs.<name>.domain` where
`dev.routing.domain` was read, and passes only the validated env-domain URL as
the redirect target — the `cfg.Deploy.Domain` fallback branch is deleted.
Exposure and port config move to the env block. Session manifests carry the
env name.

**M3 — Deploy.** `scenery deploy <ssh-target>` reverse-looks-up the owning
env; `scenery deploy --env <name>` selects by env. Publication gates on the
deployable env's frontend `serve: "production"`. `scenery deploy status`
reports env name per target and per publication; the remote publication
registry records the producing env. The remote restart runs the app with the
deployed env name as `SCENERY_ENV` instead of the hardcoded literal.

**M4 — Dotenv layering.** Extend the app-process env construction to load the
four-file stack in order for the resolved env, `local` degenerating to
`.env` → `.env.local`. Applies uniformly to `scenery up`, `scenery task run`,
`scenery worker`, and the deployed runtime. Verify deploy sync excludes all
`.env*` files (gitignore-honoring rsync) and preserves remote dotenv state.

**M5 — Environment policy.** Always inject `SCENERY_ENV`/`SCENERY_RUNTIME_ENV`
with the resolved name. Replace the `production` string match with the
env-declaration-derived strictness: default/local envs require `.env` locally
and warn on missing declared secrets; deployable envs make dotenv optional and
fail closed on missing declared secrets.

**M6 — Docs and fixtures cutover.** Update `docs/local-contract.md`,
`docs/environment.md` (dotenv stack, gitignore convention: all `.env*`
gitignored, optional committed `.env.example` for names only),
`docs/agent-guide.md`, `SKILL.md`, `README.md`, cookbook, `.scenery.json`
schema, `docs/knowledge.json`; annotate plans 0116/0117; migrate every
`testdata/apps/*` fixture config and the console/app fixtures to the new
shape.

**M7 — Validation.** Full matrix below, plus live verification against
Micro/platform.

## Plan of Work

Work proceeds in milestone order; M1 must land first because every consumer
compiles against the new structs. M2 and M3 are independent after M1 and may
be developed in parallel worktrees, but land together with M6 in the cutover
since the old config fields disappear repo-wide at once. M4/M5 are small and
ride with M2. Keep the repo testable between milestones by migrating fixture
configs in the same commit as the struct change (the compiler rejects the old
shape immediately after M1 — there is no transition window, which is
intentional).

For M2, the redirect change is a deletion plus a test: remove the
`cfg.Deploy.Domain` branch in `cmd/scenery/dev_session_controller.go` and
assert that a session whose env-domain probe fails serves localhost without
any redirect. For M3, add the reverse-lookup (`envForSSHTarget`) with its
uniqueness validation in M1's resolution helper so deploy code stays thin.

## Concrete Steps

All commands run from the scenery repo root
(`/Users/petrbrazdil/Repos/scenery`) unless stated.

1. M1: edit `internal/app/root.go` (+ its schema/validation files and tests);
   run `go test ./internal/app/...`. Migrate `testdata/apps/*/.scenery.json`
   in the same commit; run `go test ./...` to find every consumer that still
   reads `cfg.Deploy` / `cfg.Dev.Routing` and update or stub them
   milestone-by-milestone.
2. M2: `cmd/scenery/dev_session_controller.go`, `dev_routing.go`,
   `dev_edge_preflight.go`, `dev_frontends.go`; tests in
   `cmd/scenery/dev_domain_test.go`, `dev_frontend_production_test.go`,
   `internal/agent/dev_domain_routing_test.go`. Run
   `go test ./cmd/scenery ./internal/agent`.
3. M3: `cmd/scenery/deploy.go`, `deploy_publish.go`, `deploy_systemd.go`,
   `internal/agent/deploy.go`; extend deploy tests. Run
   `go test ./cmd/scenery ./internal/agent ./internal/deployplan`.
4. M4/M5: `cmd/scenery/appenv.go`, `dev_supervisor.go`,
   `internal/envfile`; add table tests for the four-file precedence including
   the `local` degeneration and process-env priority.
5. M6: docs listed in Milestones; `docs/knowledge.json` entry updates;
   `docs/plans/active.md` stays linked.
6. M7: validation matrix below; then live check against
   `/Users/petrbrazdil/Repos/Micro/platform` after migrating its
   `.scenery.json` to the target shape shown in Purpose.

## Validation and Acceptance

Repo validation:

    go test ./...
    scenery harness self --summary --write

(Repo policy: do not run `go install ./cmd/scenery` during agent validation;
use the self-harness worktree-local build.)

Live acceptance against Micro/platform (app root
`/Users/petrbrazdil/Repos/Micro/platform`, branch `main`, using the
worktree-local `.scenery/harness/bin/scenery` build):

1. Migrate the app's `.scenery.json` to the target shape; `scenery check -o
   json` passes; the old shape fails with exact field paths.
2. `scenery down && scenery up --detach --wait ready`; `scenery inspect routes
   -o json` reports the env name and `domain_url: https://micro.scenery.sh`.
3. `curl -sI http://localhost:<port>/platform/` → redirect to
   `https://micro.scenery.sh/platform/`; with the edge deliberately stopped,
   the same request serves localhost content with **no** redirect (defect 1
   fixed by construction).
4. The `platform` frontend runs its HMR dev server locally
   (`serve: "development"` in the local env) — defect 2 fixed.
5. `scenery deploy onlv-209` resolves the `production` env, publishes the
   static frontend, and `scenery deploy status -o json` reports env
   `production` for the target and publication history.
6. `scenery task run --env production <domain>:<name>` on the dev machine
   loads `.env` → `.env.production` → `.env.local` → `.env.production.local`
   and receives `SCENERY_ENV=production`; a plain `scenery up` app process
   receives `SCENERY_ENV=local`.

Acceptance is all six live checks plus a green validation matrix.

## Idempotence and Recovery

Every milestone is ordinary Go + docs changes in one repo; recovery is git.
The fixture-config migration and struct change must be one commit so any
checkout compiles. The client-app migration (Micro/platform, onlv) is a
one-line-ish `.scenery.json` rewrite per app, safe to re-apply; old configs
fail `scenery check` with exact diagnostics rather than misbehaving. Remote
state on onlv-209 needs no migration: the preserved remote `.env` keeps
working, and `.env.production` on the host is optional. If M3 lands before a
remote host is redeployed, the previous publication remains served (registry
entries without an env name read as pre-0121; decide in M3 whether to backfill
or display "unknown env").

## Artifacts and Notes

- Design conversation: 2026-07-16, Micro/platform diagnosis → envs design.
  Measured defect evidence is recorded in Purpose and Surprises.
- Micro/platform interim workaround (pre-0121): `dev.routing.domain:
  "micro.scenery.sh"` under the current config shape stops the localhost →
  production redirect while the edge is healthy, at the cost of keeping the
  static-serve local frontend. Superseded by this plan.
- Gitignore convention to document in M6: all `.env*` files gitignored; an
  optional committed `.env.example` carries names only.
- Google auth example for docs (M6): canonical names
  `GOOGLE_OAUTH_CLIENT_ID`/`GOOGLE_OAUTH_CLIENT_SECRET`/`AUTH_TOKEN_CIPHER_KEY`;
  local client with redirect URI on the local env domain in `.env.local`;
  production client on the production host's dotenv/unit env; separate OAuth
  clients per env preferred.

## Interfaces and Dependencies

- `internal/app`: new `EnvsConfig`/`EnvConfig` structs, resolution helper,
  validation; removal of `DevRoutingConfig` and top-level `DeployConfig`.
- `cmd/scenery`: dev session controller, routing, frontends, deploy, appenv —
  consumers of the resolution helper; no direct env-map walking outside it.
- `internal/envfile`: unchanged mechanics; new call-site file lists.
- `internal/agent`: session manifests and deploy registry gain an env-name
  field (schema bump for the affected envelopes; update schemas under
  `docs/schemas/` and harness expectations together, per repo rules).
- Docs layers per M6. `docs/environment.registry.json` is unaffected (no new
  scenery-owned env vars; `SCENERY_ENV`/`SCENERY_RUNTIME_ENV` already exist).
- Client apps: Micro/platform and onlv `.scenery.json` migrations ride the
  release that ships this plan.
