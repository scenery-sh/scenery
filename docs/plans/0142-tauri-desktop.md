# Tauri 2.0 Desktop Shells for Configured Frontends

This ExecPlan is a living document and must be updated as implementation,
discoveries, decisions, and validation progress.

## Purpose / Big Picture

Target apps want to ship a desktop application built with Tauri 2.0 whose webview
loads one of the app's configured web frontends. Scenery already owns the local
frontend loop: `scenery up` starts each configured frontend's dev server (Vite or
Astro) on a hidden loopback port, injects `VITE_SCENERY_*` and API-base
environment, and routes local URLs through the agent. Tauri fits on top of that
loop: `tauri dev` accepts a `--config` JSON overlay, so Scenery can point
`build.devUrl` at the managed frontend dev server and blank `beforeDevCommand`
(Scenery already runs the dev server), and `tauri build` can be pointed at a
Scenery-built production bundle through `build.frontendDist`.

After this plan:

- `.scenery.json` frontends may declare a `tauri` block selecting them as the web
  surface of a Tauri 2 desktop shell.
- `scenery up --desktop` runs the ordinary dev session plus a live Tauri window
  with HMR against the managed frontend dev server.
- `scenery build --desktop [--env <name>] [-o json]` builds the frontend
  production bundle and runs `tauri build`, reporting bundle artifacts in a new
  `scenery.build.desktop` JSON envelope.

Decisions fixed with the human before this plan: per-frontend `tauri` config
block (not a top-level `desktop` section), `scenery up --desktop` (not a new
top-level command), dev loop plus production bundling in one plan, and app-local
`node_modules/.bin/tauri` as the only supported CLI (no cargo or global
fallback).

## Progress

- [x] (2026-07-23) Explored frontend management (`cmd/scenery/dev_frontends.go`,
  `dev_frontend_production.go`, `dev_session_controller.go`), config decoding
  (`internal/app/root.go`), and CLI dispatch; confirmed design with the human.
- [x] (2026-07-23) ExecPlan written and linked from `docs/plans/active.md` and
  `docs/knowledge.json`.
- [x] (2026-07-23) Milestone 1: config surface done and green
  (`go test ./internal/app`). `internal/app/root.go` has
  `FrontendConfig.Tauri *FrontendTauriConfig`, `FrontendTauriConfig{Root}`,
  `validateFrontends()` wired into `Config.Validate()`, and a `ResolveEnv`
  pointer clone. `internal/app/root_test.go` has
  `TestDiscoverRootAcceptsFrontendTauriConfig` and
  `TestDiscoverRootRejectsInvalidFrontendTauriConfig`. The
  `docs/schemas/scenery.config.schema.json` update is deferred to Milestone 4.
- [x] (2026-07-23) Milestone 2: `scenery up --desktop` dev loop with managed
  desktop process, agent-registry visibility/removal, clean shutdown, and
  no-restart-on-window-close tests.
- [x] (2026-07-23) Milestone 3: `scenery build --desktop` frontend build,
  Tauri bundling, artifact discovery, `scenery.build.desktop` schema, and
  executable CLI-stub tests.
- [x] (2026-07-23) Milestone 4: `docs/local-contract.md`, config/build JSON
  schemas, `docs/agent-guide.md`, `SKILL.md`, and `ARCHITECTURE.md` updated.
- [x] (2026-07-23) Validation complete: focused desktop/config/schema tests,
  `go test ./...`, and current-source
  `.scenery/harness/bin/scenery harness self --summary --write` all green.
  Self-harness passed all 22 lanes. No real Tauri project exists under the
  available `/Users/petrbrazdil/Repos` checkouts, so real-window/toolchain
  acceptance was not runnable; the executable-stub tests cover the full CLI
  build envelope and managed dev lifecycle.

## Surprises & Discoveries

- 2026-07-23 (exploration, recorded for the implementing agent — these are the
  exact dev-loop hook points so Milestone 2 needs no re-exploration):
  - `scenery up` flows `upCommand` → `parseDevArgs` (`cmd/scenery/main.go`) →
    `runWithWatchFunc(listen, verbose, json, appRoot, env)` →
    `runWithWatch` (`cmd/scenery/watch.go`). Adding `--desktop` means threading
    a new parameter (or passing `devOptions`) through that signature; the
    `runWithWatchFunc` stub in `cmd/scenery/main_test.go` (~line 419) must be
    updated to match.
  - `runWithWatch` sets `cfg.Frontends = resolvedEnv.Frontends`
    (`cmd/scenery/watch.go:177`), so per-env-resolved `FrontendConfig` values —
    including the cloned `Tauri` pointer — are what the supervisor and any
    desktop launcher see on `cfg`.
  - Frontend processes start in `prepareDevAgentSessionDetailed`
    (`cmd/scenery/dev_session_controller.go`, "Starting frontend dev servers"
    phase) and land on `prepared.FrontendProcesses` /
    `prepared.FrontendReady`. `runWithWatch` then calls
    `supervisor.adoptManagedFrontends(...)` and
    `supervisor.addStartupReady(prepared.FrontendReady)`
    (`cmd/scenery/watch.go:255-260`). The natural desktop launch point is a
    goroutine that waits on a tee of `FrontendReady` (see `joinStartupReady`
    in `cmd/scenery/dev_supervisor.go`) and then starts the tauri processes
    with the already-known per-frontend loopback `Addr`s from
    `prepared.FrontendProcesses` (or the override/upstream backend recorded in
    the session backends map for frontends without a local process).
  - `devSupervisor` already tracks `frontends map[string]*managedFrontendProcess`
    and stops them in `Close()` (`cmd/scenery/dev_supervisor.go:238-242`);
    desktop processes can reuse `managedFrontendProcess`-style tracking or a
    parallel small struct, but must be stopped in `Close()` the same way.
  - `devManagedProcess` (`cmd/scenery/dev_process_runner.go`) has no restart
    supervision, which matches the decision that a closed window stays closed.
  - Session `Processes` are registered at `client.Register` time
    (`frontendSessionProcesses`); Register is an upsert, so the desktop PID can
    be added with a follow-up `Register` carrying the same session identity.
- 2026-07-23 (implementation): Agent `Register` treats an empty `Processes`
  map as "preserve existing." Removing the final `desktop-<name>` process
  therefore requires a zero-PID tombstone map; `NewSession` then discards the
  tombstone and stores no processes. The exit test exposed this when the
  desktop PID remained registered after a clean stub exit.
- 2026-07-23 (human review): Keeping every Tauri-specific operation in
  `cmd/scenery` made the CLI package own more than orchestration. Project
  resolution, exact overlays/commands, command execution, and bundle discovery
  moved to `internal/desktop`; only dev-supervisor and agent-session wiring
  remain in `cmd/scenery`.
- 2026-07-23 (validation): The first current-source self-harness run exposed
  that this plan lacked the required living-document statement. After adding
  it, the full harness passed all 22 lanes. A repository-wide
  `rg --files /Users/petrbrazdil/Repos -g 'tauri.conf.json'
  -g '!**/node_modules/**'` returned no real Tauri project for the optional
  manual acceptance.

## Decision Log

- 2026-07-23 (human + agent): Tauri declaration lives per frontend at
  `frontends.<name>.tauri`; the config expresses "this frontend is the web
  surface of a desktop shell". Rationale: keeps frontend identity singular and
  avoids a second top-level section referencing frontends by name.
- 2026-07-23 (human + agent): Dev entrypoint is `scenery up --desktop`. One
  process owns the whole session; the Tauri window is another managed dev
  process that dies with the session. No new top-level command.
- 2026-07-23 (human + agent): Scope includes production bundling
  (`scenery build --desktop`) in this plan, not a follow-up.
- 2026-07-23 (human + agent): Only the app-local `node_modules/.bin/tauri`
  binary is supported, resolved like the existing vite/astro local-bin lookup
  (`managedFrontendLocalBin`). Missing binary is a clear diagnostic naming the
  expected path and `@tauri-apps/cli`; no cargo/global fallback magic.
- 2026-07-23 (agent): The desktop dev process is not restart-supervised.
  Closing the window is a normal user action; the session logs the exit and
  keeps running. `devManagedProcess` has no restart layer today, so this is the
  default behavior, kept deliberately.
- 2026-07-23 (agent): `tauri dev` receives
  `{"build":{"devUrl":"http://<addr>","beforeDevCommand":""}}` via `--config`
  inline JSON (a Tauri 2 CLI capability); `tauri build` receives
  `{"build":{"frontendDist":"<abs dist>","beforeBuildCommand":""}}`. Scenery
  owns the frontend build/serve; the app's `tauri.conf.json` stays the source of
  truth for everything else (identifier, windows, bundling targets).
- 2026-07-23 (human + agent): `internal/desktop` is the durable Tauri
  integration boundary. `cmd/scenery` parses flags, builds frontends, manages
  session child processes, and renders output; it does not own Tauri project
  resolution or command semantics. Rationale: keep CLI orchestration auditable
  without turning it into the implementation layer.

## Outcomes & Retrospective

Completed on 2026-07-23.

Configured frontends can now opt into one Tauri 2 shell with
`frontends.<name>.tauri`. `scenery up --desktop` waits for the existing managed
frontend, launches the app-local Tauri CLI against its hidden backend, registers
the desktop PID in the existing agent session, captures its logs, and stops it
with the session. A user-closing window removes the registered PID without
restarting the desktop or affecting the app/frontend.

`scenery build --desktop --env <name> -o json` builds each enabled frontend,
injects the selected environment and domain API base, runs `tauri build` with
the absolute `frontendDist`, discovers installer bundles, and returns the
checked `scenery.build.desktop` payload. Tauri-specific resolution, command
construction/execution, and artifact discovery live in `internal/desktop`;
`cmd/scenery` remains lifecycle and output orchestration.

Focused tests use executable local CLI stubs to prove cwd, argv, overlays,
environment, registry state, clean exit, no restart, artifact discovery, and
full command JSON schema validity. `go test ./...` and the current-source
self-harness passed. Real window/HMR and native installer proof remains an
operator acceptance in an actual client repository because none of the
available checkouts contains a Tauri project.

## Context and Orientation

Terms:

- App root: directory containing `.scenery.json` (decoded by
  `internal/app/root.go` into `app.Config`).
- Configured frontend: an entry in `.scenery.json` `frontends.<name>` with an
  app-root-relative `root`. During `scenery up`, `cmd/scenery/dev_frontends.go`
  starts one dev server per frontend (`serve: "development"`, the default) or an
  in-process static server over a production build (`serve: "production"`,
  built by `cmd/scenery/dev_frontend_production.go`). Both expose a loopback
  `Addr` used as the session backend.
- Managed dev process: child process started through `startDevManagedProcess`
  (`cmd/scenery/dev_process_runner.go`), with log capture into session logs and
  Victoria, no restart supervision.
- Tauri 2 project: a directory containing `src-tauri/tauri.conf.json` plus Rust
  sources; the `tauri` CLI (`@tauri-apps/cli`) runs `dev` and `build` from that
  directory. `tauri dev` loads `build.devUrl` in the app webview; `tauri build`
  bundles `build.frontendDist` into installers under
  `src-tauri/target/release/bundle/`.

Key existing code to reuse:

- `managedFrontendLocalBin(root, name)` — walks up `node_modules/.bin` for a
  local binary (`cmd/scenery/dev_frontends.go`).
- `frontendDevEnv(...)` — the env injected into frontend dev servers, including
  API base URLs (`cmd/scenery/dev_frontends.go`).
- `managedFrontendBuildCommand(root, basePath)` — production build argv
  (`cmd/scenery/dev_frontends.go`); the static server resolves the built output
  directory (`cmd/scenery/dev_frontend_production.go`).
- `beginManagedFrontendBackendsForSession` and the `FrontendReady` channel in
  `cmd/scenery/dev_session_controller.go` — the hook point after which frontend
  loopback addresses are live.
- `parseDevArgs` in `cmd/scenery/main.go` — `scenery up` flag surface.
- `buildCommand` in `cmd/scenery/build.go` — `scenery build` flag surface.

## Milestones

1. Config surface. `frontends.<name>.tauri { root }` decodes, validates, and
   round-trips; repo tests prove acceptance and rejection paths. Repo stays
   green with no CLI behavior change.
2. Dev loop. `scenery up --desktop` launches one Tauri window per tauri-enabled
   frontend after frontend readiness, with logs, `scenery ps` visibility, and
   clean shutdown. Errors (no tauri frontends, missing CLI, missing
   `tauri.conf.json`) are actionable.
3. Production bundling. `scenery build --desktop` builds the frontend bundle
   and runs `tauri build`; `-o json` emits `scenery.build.desktop` with bundle
   artifact paths; schema checked in.
4. Docs and contract. `docs/local-contract.md` (App Config + CLI Grammar +
   JSON Schemas), `docs/schemas/scenery.config.schema.json`, new
   `docs/schemas/scenery.build.desktop.schema.json`, `docs/agent-guide.md`,
   and `SKILL.md` updated in the same change.

## Plan of Work

Milestone 1 — config (`internal/app/root.go`, `internal/app/root_test.go`):

Add `Tauri *FrontendTauriConfig` to `FrontendConfig` with
`FrontendTauriConfig{ Root string }`. `Root` is app-root-relative, must stay
beneath the app root (same `filepath.IsAbs` + `filepath.Clean` + `..` prefix
rule as `envs.*.libraries.*.manifest`), and empty means the frontend root. Add a
`validateFrontends()` step to `Config.Validate()`. Clone the pointer in
`ResolveEnv` so resolved envs do not alias config. The reflective unknown-field
walker covers nested `tauri` fields automatically; add tests for an accepted
block, a defaulted root, `unknown .scenery.json field "frontends.app.tauri.extra"`,
and an absolute or escaping root.

Milestone 2 — dev loop (`cmd/scenery/main.go`, new `cmd/scenery/dev_desktop.go`,
hookup in `cmd/scenery/dev_session_controller.go` / `dev_supervisor.go`):

`parseDevArgs` gains `--desktop` (bool on `devOptions`). Fail fast when set and
no configured frontend declares `tauri`. New `dev_desktop.go` resolves, for each
tauri-enabled frontend: the tauri root (app-root-relative, default frontend
root), preflight `src-tauri/tauri.conf.json` existence, the app-local `tauri`
binary via `managedFrontendLocalBin`, and the dev URL — the started frontend
process/static-server `Addr`, or the override/upstream backend address when the
frontend resolved to an external backend. Launch after the frontend-ready phase
resolves, as a managed process: `tauri dev --config <inline JSON>` with
`Kind: "desktop"`, `Role: "desktop-shell"`, cwd the tauri root, env
`frontendDevEnv(...)`, log file `logs/desktop-<name>.log`, and Victoria capture
with source ID `desktop:<name>`. Re-`Register` the session with
`desktop-<name>` in `Processes` (Register is an upsert) so `scenery ps` and
stale-process cleanup see the PID. Stop on session shutdown via the same
restorer pattern as frontends; a self-exit (window closed) logs and does not
stop the session. `--detach` composes without special-casing.

Milestone 3 — bundling (`cmd/scenery/build.go`, new
`cmd/scenery/build_desktop.go`, `docs/schemas/scenery.build.desktop.schema.json`):

`scenery build --desktop [--env <name>] [--app-root <path>] [-o json]`;
`--desktop` conflicts with `--target`/`--lib`. Per tauri-enabled frontend: build
the frontend production bundle with `managedFrontendBuildCommand(root, "")`
(root base `/` — the bundle is served from `frontendDist`, not a routed path),
resolve the dist directory the way the production static server does, then run
app-local `tauri build --config <inline JSON>` in the tauri root. Env is the
base env plus the `--env`-resolved dotenv files; when the selected env declares
a `domain`, inject `SCENERY_API_BASE_URL` and `VITE_API_BASE_URL` as
`https://<domain>` so the bundled frontend targets that API. JSON output lists
per-frontend bundle artifacts discovered under
`<tauri root>/src-tauri/target/release/bundle/`; register the schema wherever
the schema catalog test enumerates `docs/schemas/`.

Milestone 4 — docs: update every affected layer in the same change, per the
root documentation update rules.

## Concrete Steps

Work from the repository root `/Users/petrbrazdil/Repos/scenery`.

1. Done (2026-07-23). `internal/app/root.go` (types, validation, `ResolveEnv`
   clone) and `internal/app/root_test.go` are in the working tree and green:

       go test ./internal/app

2. Add the `--desktop` flag in `cmd/scenery/main.go`; create
   `cmd/scenery/dev_desktop.go` and `cmd/scenery/dev_desktop_test.go`; hook the
   launch after the frontend-ready phase in
   `cmd/scenery/dev_session_controller.go` / `dev_supervisor.go`. Tests stub the
   `tauri` binary with an executable script fixture that records argv/cwd/env.
   Run:

       go test ./cmd/scenery -run 'Desktop'

3. Create `cmd/scenery/build_desktop.go`; wire `--desktop` into
   `cmd/scenery/build.go`; add `docs/schemas/scenery.build.desktop.schema.json`
   and its catalog/test registration; add build tests with the stub binary and a
   golden JSON envelope. Run:

       go test ./cmd/scenery -run 'Desktop|Build'

4. Update `docs/local-contract.md` (App Config rules, CLI Grammar for
   `up --desktop` and `build --desktop`, JSON Schemas list),
   `docs/schemas/scenery.config.schema.json`, `docs/agent-guide.md` (desktop
   workflow note), `SKILL.md` (target-app agent note), and this plan's
   `Progress`.

5. Full validation (see below), then update `Outcomes & Retrospective`, move the
   plan reference to `docs/plans/completed.md`, and refresh `docs/knowledge.json`.

## Validation and Acceptance

- `go test ./...` — repo green including new config, dev, and build tests.
- `.scenery/harness/bin/scenery harness self --summary --write` from the
  worktree (do not `go install ./cmd/scenery`; worktrees share the installed
  path).
- Acceptance (automated): with a fixture app whose frontend declares `tauri`
  and a stub `node_modules/.bin/tauri`, the dev path invokes
  `tauri dev --config {"build":{"devUrl":"http://<addr>","beforeDevCommand":""}}`
  from the tauri root, and the build path invokes
  `tauri build --config {"build":{"frontendDist":"<abs dist>","beforeBuildCommand":""}}`
  after producing the frontend bundle; `scenery build --desktop -o json`
  validates against `scenery.build.desktop.schema.json`.
- Acceptance (manual, outside CI): in a client app with a real Tauri 2 project,
  `scenery up --desktop` opens the window with HMR against the managed dev
  server; closing the window leaves the session running;
  `scenery build --desktop -o json` reports installer bundle paths.
- If a command cannot run in the current environment (for example no real Tauri
  toolchain), say exactly which command was skipped and why.

## Idempotence and Recovery

All steps are ordinary source edits plus cached Go tests; re-running any step is
safe. The dev loop change is additive behind `--desktop`, and the build change is
additive behind `--desktop`, so partial implementation leaves existing commands
untouched. If a session is interrupted mid-implementation, `git status` plus
this plan's `Progress` checkboxes recover the position. Stub-binary test
fixtures live under the test's `t.TempDir()`; nothing persists outside the
repo except ordinary `.scenery/harness` snapshots.

## Artifacts and Notes

- Plan file: `docs/plans/0142-tauri-desktop.md` (this file), linked from
  `docs/plans/active.md` and indexed in `docs/knowledge.json`.
- New schema: `docs/schemas/scenery.build.desktop.schema.json`.
- New sources: `internal/desktop/desktop.go` (+ test) owns Tauri project and
  command behavior; `cmd/scenery/dev_desktop.go` (+ test) owns supervisor
  integration; `cmd/scenery/build_desktop.go` (+ test) owns build orchestration
  and machine output.
- Tauri 2 CLI facts relied on: `tauri dev`/`tauri build` accept repeated
  `--config <path or inline JSON>` merged over `src-tauri/tauri.conf.json`;
  dev webview loads `build.devUrl`; bundles land under
  `src-tauri/target/release/bundle/<format>/`.

## Interfaces and Dependencies

- `.scenery.json` gains `frontends.<name>.tauri { root }` — a public app-config
  contract change; schema, docs, and unknown-field tests move together.
- CLI grammar gains `scenery up --desktop` and
  `scenery build --desktop [--env <name>] [-o json]` — documented in
  `docs/local-contract.md` § CLI Grammar.
- New machine contract: `scenery.build.desktop` envelope data schema.
- No new Go module dependencies. The `tauri` executable is an app-owned
  dependency resolved from the app's `node_modules/.bin`; Scenery never vendors
  or installs it.
- No changes to `internal/localproxy`, the compiler, the graph, or generated
  clients; the feature is config + CLI orchestration only.
