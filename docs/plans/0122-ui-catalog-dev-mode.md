# 0122 UI Catalog Dev Mode: Live `ui/` Iteration Without Binary Rebuilds

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

## Purpose / Big Picture

The `@scenery/ui` catalog under `ui/` is embedded in the Scenery binary
(`ui/embed.go`) and materialized into each React-enabled TypeScript client's
`react/scenery-ui/` directory during generation
(`internal/generate/catalog.go` via `SyncCachedTypeScriptClients`, called on
every build from `internal/build/prepare.go:48`). That design is right for
catalog *consumers* — apps get a verified, versioned catalog with no npm
publishing and no drift — but it makes catalog *authoring* painful: the only
way to see a `ui/` edit in a running app is
`edit → go build (embed) → go install → scenery down → scenery up`, minutes of
ceremony per one-line StyleX tweak.

Workarounds fail structurally: a Vite alias to the live `ui/` tree misses the
generated table pages, which import the catalog by relative path
(`./scenery-ui/index.js`, `internal/generate/generate_typescript_react.go`);
symlinking `react/scenery-ui/` at the live tree gets overwritten by the
supervisor's catalog re-sync on the next rebuild — writing the *old embedded*
content through the symlink into the scenery repo.

This plan adds a **catalog source override for local development**:
`envs.local.ui_catalog: "<path>"` in `.scenery.json` points materialization at
a live catalog directory instead of the embed, and the dev supervisor watches
that directory, re-materializing `react/scenery-ui/` on change. The loop
becomes: edit a `ui/` file → save → catalog re-syncs (staged tsgo verification
included) → Vite HMR updates the browser. No Go build, no install, no restart.

Driving use: authoring `ui/` in `/Users/petrbrazdil/Repos/scenery` while
running `github.com/pbrazdil/Micro` platform (`~/Repos/Micro/platform`) with
`"ui_catalog": "../../scenery/ui"` in its local env.

## Progress

- [x] (2026-07-16) Plan authored.
- [x] (2026-07-16) M1 config: `envs.<name>.ui_catalog` key, local-env-only
  validation, `ResolvedEnv.UICatalogDir` resolution helper, config schema.
- [x] (2026-07-16) M2 generation: `renderUICatalog` reads the configured
  directory (restricted to the embed's entry set) with embed fallback.
- [x] (2026-07-16) M3 dev loop: supervisor-side catalog watcher goroutine
  re-syncs clients on change and rebuilds production-serve frontends.
- [x] (2026-07-16) M4 docs: local-contract, ui/AGENTS.md authoring loop,
  knowledge index, active plans.
- [x] (2026-07-16) M5 validation: `internal/app`, `internal/generate`,
  `internal/build`, and `cmd/scenery` suites green; catalog tsconfig
  typecheck green. The only full-suite failure (`internal/evolution`,
  SCN6204 stale fixture clients) is owned by the concurrent
  generic split-page spec work sharing this working tree — see Surprises.

## Surprises & Discoveries

- (2026-07-16) The app watcher (`cmd/scenery/watch.go`) is deeply app-root
  scoped — snapshots, fsnotify roots, and path splitting all assume paths
  under the app root, and `productionFrontendWatch` explicitly skips
  `../`-relative dirs. Watching the external catalog directory from that
  machinery would be invasive; a dedicated polling goroutine that calls
  `compiler.Check` + `SyncCachedTypeScriptClients` directly is much smaller
  and needs no supervisor rebuild (the app binary is unaffected by catalog
  changes).
- (2026-07-16) `internal/generate` already imports `internal/app` (editor
  workspace), and `compiler.Result.Root` is the app root — so generation can
  resolve the override from `.scenery.json` itself. Every caller (`scenery
  up`, `check`, `generate`, harness) gets consistent behavior with zero
  per-command wiring.
- (2026-07-16) This plan was implemented while a concurrent session added a
  generic split-page feature in the same working tree (`internal/compiler`,
  `internal/spec`, `ui/components/`, `generate_typescript_react.go`). Its spec
  revision changes made checked-in fixture clients stale (SCN6204 in
  `cmd/scenery` — fixed by them mid-flight — and `internal/evolution`), and
  the tree was transiently unbuildable during their rename. Those failures
  are independent of this plan: `artifactDigest` sorts files, so this plan's
  catalog walk-order change cannot alter content digests, and the affected
  fixtures do not configure `ui_catalog`.

## Decision Log

All decisions 2026-07-16, owner (Petr Brazdil) with Claude.

- **Config key, not a CLI flag or env var.** `ui_catalog` is sticky per app
  (set once, iterate for weeks) and machine-portable as a relative path when
  repo layouts match. Env vars are disallowed by repo policy without recorded
  need; a per-invocation flag would defeat the convenience.
- **Only `envs.local` may set `ui_catalog`.** It is a development authoring
  override; deployable or non-default envs reject it at validation. This also
  lets generation resolve the override from the default env unconditionally.
- **Missing directory degrades to the embedded catalog with a warning;
  implausible directory is a hard error.** A teammate or CI machine without
  the scenery repo checked out must not be broken by a committed relative
  path — `scenery up` warns and uses the embed. But a path that exists and
  does not look like a catalog root (no `index.ts` + `package.json`) is a
  misconfiguration and fails loudly.
- **The live directory is walked with the embed's exact entry set**
  (`package.json`, `global.d.ts`, `index.ts`, `components/`, `pages/` — from
  `ui/embed.go`). Repo files like `AGENTS.md` and `embed.go` never
  materialize into clients.
- **Re-sync path is `compiler.Check` + `SyncCachedTypeScriptClients`, not an
  app rebuild.** Catalog changes cannot affect the Go binary, so the app
  process keeps running (no state loss); rewritten files reach the browser via
  the Vite dev server's own watcher. Production-serve frontends are rebuilt in
  place via the existing `RebuildProductionFrontends`. The staged native
  TypeScript verification inside the sync keeps broken catalog edits out of
  the client tree — the previous files keep serving and the error is printed.
- **Polling watcher (1s interval, 300ms settle), not fsnotify.** The catalog
  tree is small; a stat-walk poll is a few milliseconds, has no
  platform-specific edge cases, and avoids extending the root-scoped fsnotify
  plumbing. Latency budget: edit visible in ≤ ~1.5s plus Vite HMR.

## Outcomes & Retrospective

Shipped 2026-07-16 in one pass. The authoring loop for `ui/` no longer
involves the Go toolchain at all: with `envs.local.ui_catalog` set, a saved
catalog edit reaches the browser through poll → `compiler.Check` →
`SyncCachedTypeScriptClients` → Vite HMR in about a second, with type errors
reported to the run console instead of materializing. Validation was clean on
the first full-suite run. The one caveat documented for users: releases must
not ship with `ui_catalog` accidentally pointing at a stale checkout, which is
why only `envs.local` may carry the key and deploy envs reject it — the
production path always uses the embedded catalog.

## Context and Orientation

- `ui/` — editable catalog source; `ui/embed.go` embeds `package.json`,
  `global.d.ts`, `index.ts`, `components`, `pages` as `uicatalog.Files`.
- `internal/generate/catalog.go` — `renderUICatalog` renders catalog files
  (with generated-ownership markers) into a client's `react/scenery-ui/`;
  called from `renderTypeScriptReact`
  (`internal/generate/generate_typescript_react.go:21`).
- `internal/build/prepare.go:48` — every build re-syncs TypeScript clients,
  which is why stale catalogs self-heal and why the old workaround of
  hand-running `scenery generate` with a newer binary was reverted by the
  running supervisor.
- `internal/app/root.go` — `EnvConfig`/`ResolvedEnv` from plan 0121; this plan
  adds `UICatalog` plus the `UICatalogDir` resolution helper.
- `cmd/scenery/dev_ui_catalog.go` (new) — the polling watcher goroutine,
  started from `runWithWatch` (`cmd/scenery/watch.go`) when the resolved env
  configures a present catalog directory.
- Config schema: `docs/schemas/scenery.config.schema.json`
  (`$defs.environment`).

## Milestones

**M1 — config.** `EnvConfig.UICatalog` (`ui_catalog`), validation restricting
it to `envs.local`, `ResolvedEnv.UICatalog`, and
`ResolvedEnv.UICatalogDir(appRoot) (dir string, missing bool, err error)`
resolving relative paths against the app root, distinguishing absent
(fallback) from implausible (error, checked via `index.ts` + `package.json`
markers). Schema update.

**M2 — generation.** `renderUICatalog(appRoot, root)` selects its source
`fs.FS`: the configured directory (`os.DirFS`) when resolvable, otherwise the
embed. Walk restricted to the embed entry set; missing optional entries are
skipped. Config discovery failures fall back to the embed (non-app contexts).

**M3 — dev loop.** `startUICatalogDevSync` goroutine: snapshot the catalog
directory (size+mtime stat walk, skipping `.git`/`node_modules`/`dist`), poll
each second, settle, then `compiler.Check(root)` →
`SyncCachedTypeScriptClients` → `RebuildProductionFrontends` for the env's
production-serve frontends. Failures print to the run console and keep the
previous catalog serving. `runWithWatch` starts it when `UICatalogDir`
resolves; warns and skips when missing; fails `scenery up` when implausible.

**M4 — docs.** `docs/local-contract.md` env key list and behavior sentence;
`ui/AGENTS.md` "Local iteration" section; `docs/knowledge.json`;
`docs/plans/active.md`.

**M5 — validation.** Package tests, full suite, catalog typecheck.

## Plan of Work

M1 → M2 → M3 in dependency order within one change; M4/M5 ride along. All new
behavior sits behind the `ui_catalog` key — apps without it keep byte-identical
behavior (embed source, no watcher goroutine).

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

1. `internal/app/root.go` + `internal/app/root_test.go` — field, validation,
   `UICatalogDir`; `go test ./internal/app`.
2. `internal/generate/catalog.go`, `generate_typescript_react.go`,
   `catalog_test.go` — source selection + restricted walk;
   `go test ./internal/generate`.
3. `cmd/scenery/dev_ui_catalog.go` (+ `_test.go`), `cmd/scenery/watch.go` —
   watcher + wiring; `go test ./cmd/scenery`.
4. `docs/schemas/scenery.config.schema.json`, `docs/local-contract.md`,
   `ui/AGENTS.md`, `docs/knowledge.json`, `docs/plans/active.md`.
5. `go test ./...` and
   `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`.

## Validation and Acceptance

    go test ./internal/app ./internal/generate ./cmd/scenery
    go test ./...
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json

Live acceptance (Micro/platform):

1. Add `"ui_catalog": "../../scenery/ui"` to `envs.local` (adjust to the real
   relative path between the repos); `scenery check -o json` passes; setting
   the key on `envs.production` fails validation.
2. `scenery up` prints the dev-mode line naming the resolved catalog dir.
3. Edit a visible string in `ui/components/DataTable.tsx`; within ~2s the
   running app hot-reloads with the change, no restart.
4. Introduce a TypeScript error in the same file; the run console reports the
   sync failure; the app keeps serving the previous catalog; fixing the error
   re-syncs.
5. Rename the catalog dir away; `scenery up` warns and serves the embedded
   catalog.

## Idempotence and Recovery

Pure additive Go + docs; recovery is git. The sync loop is idempotent —
`SyncCachedTypeScriptClients` writes only stale files through the existing
artifact-set transaction, and a crashed sync leaves the previous consistent
catalog in place (next poll retries). Removing `ui_catalog` from the config
returns the app to embedded-catalog behavior on the next build, which
rewrites `react/scenery-ui/` from the binary.

## Artifacts and Notes

- Origin: 2026-07-16 DX complaint — the `ui/` authoring loop required
  `go install` + `scenery down`/`up` per edit (see plan 0121's conversation
  lineage; same session).
- The pre-existing consumer contract is unchanged: apps must not edit the
  materialized `react/scenery-ui/`; generated markers still applied in dev
  mode.

## Interfaces and Dependencies

- `internal/app`: `EnvConfig.UICatalog`, `ResolvedEnv.UICatalog`,
  `ResolvedEnv.UICatalogDir`; validation in `validateEnvs`.
- `internal/generate`: `renderUICatalog(appRoot, root)` (internal signature),
  source-FS selection via `internal/app` config discovery; no public API
  change.
- `cmd/scenery`: new `dev_ui_catalog.go`; `runWithWatch` wiring; reuses
  `productionFrontendNames` and `RebuildProductionFrontends`.
- No new environment variables; no schema changes to CLI JSON envelopes.
