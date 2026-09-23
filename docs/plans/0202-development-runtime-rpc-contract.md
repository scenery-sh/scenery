# Development Runtime RPC Contract And Console Removal

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Scenery used to ship its own browser dashboard (`apps/console`, served under
`/console/` by every `scenery up`). The dashboard's data came from a private
WebSocket JSON-RPC surface on the worktree's dashboard backend. The ONLV team
wants the parts of that dashboard they use to live inside their own app shell
(the NextNext bottom panel) and wants Scenery to stop owning a browser UI.

After this plan:

- Scenery serves no dashboard UI. `/console/`, its embedded Vite bundle, the
  bundle-staleness headers, `scenery harness ui` and the dashboard build step
  are gone.
- The data surface that remains is an official, documented contract: the
  **development runtime RPC**, a JSON-RPC 2.0 protocol over one WebSocket at
  the app origin's `/runtime` path, plus streamed storage transfers at
  `/runtime/storage`. It exposes runtime status, PostgreSQL inspection and
  queries, and local object-storage inspection and maintenance.
- A `typescript_client` target can opt in with `dev_runtime = true`; generation
  then writes a typed `dev-runtime.ts` client for that contract next to the
  app's ordinary generated client. Other clients are unchanged.
- ONLV NextNext renders three development tabs in its bottom panel (runtime
  overview, database, storage) on top of that generated client. That work lives
  in the ONLV repository; this plan records the Scenery side and the
  cross-repository acceptance.

Observable result: in a running ONLV worktree, opening the NextNext bottom panel
shows live runtime status, lets the developer browse PostgreSQL tables and run
SQL, and browse, upload, download and delete local storage objects, while
`http://<origin>/console/` is no longer a Scenery route.

## Progress

- [x] (2026-09-23) Mapped the dashboard backend, RPC dispatch, routing of
  `/runtime`, `/console` and `/__storage`, the TypeScript client generator and
  every repository reference to `apps/console`.
- [x] (2026-09-23) RPC surface trimmed to the contract (`dashboard_rpc.go`),
  dedicated `scenery.dev-runtime.status` result and schema, unknown params
  rejected, concurrent calls per connection, notifications and their call
  sites removed, `/runtime/storage` routed in both routers.
- [x] (2026-09-23) `typescript_client.dev_runtime` and the generated
  `dev-runtime.ts` (`internal/generate/dev_runtime_client.ts`); the `house`
  fixture enables it and is typechecked.
- [x] (2026-09-23) Dashboard UI, embedding, bundle identity, `/console` and
  host-mode `console.*` routes, `console`/`dashboard` expose entries,
  `scenery harness ui`, `harness:ui` validation steps, the dashboard verifier
  lanes and CI steps removed; dependencies moved to `tools/typescript`.
- [x] (2026-09-23) Contracts, spec, instructions, knowledge index updated.
- [x] (2026-09-23) `db/query` answers `{columns, rows}` in select order (found
  during ONLV acceptance, see Surprises).
- [x] (2026-09-23) Validation: full verifier, lint, fixtures, typechecks (see
  Artifacts). Selected probes: see Artifacts.
- [x] (2026-09-23) ONLV worktree `onlv-dev-runtime-panel` (branch
  `dev-runtime-panel`): Runtime, Database and Storage bottom-panel tabs,
  browser acceptance at `http://localhost:4688`. Nothing is committed in
  either repository yet.
- [ ] Developer review; then commit Scenery, pin the new Scenery in ONLV and
  commit ONLV (the ONLV `go.mod` currently carries a local source replacement
  from `framework use --source` that must not be committed).

## Surprises & Discoveries

- In the normal agent-owned mode the worktree owner's dashboard backend never
  broadcasts supervisor notifications (`process/*`): the supervisor calls
  `notify` on its own, unstarted `dashboardServer`
  (`cmd/scenery/dev_supervisor.go`, `s.agent == nil` guard around
  `s.dashboard.Start`). Only `trace/new` from `/__scenery/report` reached
  browser clients. A request/response contract loses nothing; clients poll
  `status`.
- `traces/clear` is not UI-only: `scenery traces clear` sends it over the same
  WebSocket (`cmd/scenery/worktree_dashboard_client.go`).
- `apps/console` also hosted the TypeScript dependency tree used to typecheck
  the `ui/` catalog, generated clients and `examples/webhook-inbox`, the
  QueryTable profiler, the `dashboard-ui` toolchain source lock, and CI's
  frontend install.
- Framework preparation from source ran `scripts/build-dashboard-ui-embed.sh`
  inside the producer (`internal/build/framework_prepare.go`). A producer from
  before this plan therefore cannot prepare this source: `just framework-use
  --source` in ONLV failed with `build-dashboard-ui-embed.sh: No such file or
  directory`. One-time bootstrap: run `framework use --source` with
  `SCENERY_BIN` set to a CLI built from this checkout. Afterwards preparation
  needs only Go.
- `bun run profile:query-table` fails identically on a clean `main` checkout
  (`ENOENT while resolving package '@stylexjs/stylex' from ui/components/`);
  the move to `tools/typescript` neither caused nor fixed it.
- `db/query` returned row objects built from Go maps, so JSON ordered columns
  alphabetically and lost the select order (`select state, count(*) as places`
  rendered `places, state`). The contract now returns `{columns, rows}` with
  value arrays, like `postgres/rows`, and `array_mode` is gone.
- The spec revision changes with the new `dev_runtime` attribute, so every
  committed generated client carries a new descriptor revision. Besides the
  two matrix fixtures, `testdata/apps/worktree-postgres` and
  `testdata/assistant` had to be regenerated: the `worktree` probe failed with
  `SCN6204 generated TypeScript clients are stale` until they were.
  `examples/webhook-inbox` was already on an older revision before this plan
  and is left to its owner.
- The `worktree` probe's Victoria scenario used `GET /console/` as its serving
  sentinel; it now polls `/runtime/health` and calls the runtime RPC `status`
  after recovery.
- `service_processes` is empty for ONLV's development session even though the
  process model is on; the status projection is faithful to `devdash`'s
  record, so this is not a contract defect.

## Decision Log

- Decision: remove the RPC methods whose only consumer was the dashboard UI
  (`list-apps`, `logs/list`, `process/output/list`, `traces/list`, `api-call`,
  `stored-requests/*`) and all server notifications. Rationale: requested by the
  developer (2026-09-23); a small official surface. `traces/clear` stays as a
  CLI-internal method, not part of the generated client. Date: 2026-09-23.
  Author: Claude with the developer.
- Decision: the generated client is opt-in through a boolean
  `typescript_client.dev_runtime` attribute that writes `dev-runtime.ts`, which
  `index.ts` does not re-export. Rationale: developer choice; clients without
  the attribute keep byte-identical output. Date: 2026-09-23.
- Decision: normalize request fields to snake_case (`db/query` used
  `appId`/`arrayMode`/`dbId`), drop the ignored `database` selector from the
  PostgreSQL methods, and return a dedicated status object instead of the
  internal `devdash.AppStatus` (no `meta`, `apiEncoding`, `dashboardBundle`).
  Rationale: one exact, documented shape. Date: 2026-09-23.
- Decision: storage transfers move from the dashboard listener's `/__storage`
  (reachable only below `/console`) to `/runtime/storage` at the app origin; the
  dashboard listener serves them at `/__scenery/storage`. Date: 2026-09-23.
- Decision: the path-mode `dashboard` route record, the host-mode `console.*`
  route, and the `console`/`dashboard` `envs.<name>.expose` entries are removed;
  the dashboard backend stays as the runtime control listener (report intake,
  control plane, RPC). Date: 2026-09-23.
- Decision: the dependency-only package moves to `tools/typescript` (no app,
  no build), keeping the catalog/generated-client typechecks and the profiler.
  Date: 2026-09-23.
- Decision: no commits in either repository until the developer reviews; ONLV
  consumes this checkout through `just framework-use --source`. Date: 2026-09-23.
- Decision: `db/query` returns `{columns, rows}` and drops `array_mode`;
  `postgres/rows` never answers `null` rows. Rationale: select order is part of
  a SQL result. Date: 2026-09-23.
- Decision: `status` results carry payload identity and the generated client
  refuses a different `schema_revision`, so a client generated by another
  producer fails visibly (`protocol`) instead of misreading results.
  Date: 2026-09-23.

## Outcomes & Retrospective

Implemented and accepted locally on 2026-09-23; open only for developer review,
commits and the ONLV Scenery pin. Scenery serves no dashboard UI; the
development runtime RPC is documented, schema-identified and generated as an
opt-in client; ONLV's bottom panel renders runtime status, PostgreSQL browsing
and SQL, and storage browsing, upload, replacement, download and deletion on
top of it. Building and preparing the Scenery CLI no longer needs Bun.

## Context and Orientation

The worktree runtime owner (`cmd/scenery/worktree_runtime_owner.go`) starts a
"dashboard" HTTP listener (`cmd/scenery/dashboard.go`, controller in
`cmd/scenery/agent_dashboard.go`). It serves:

- `/__scenery` — WebSocket JSON-RPC (`cmd/scenery/dashboard_rpc.go`);
- `/__scenery/report` — trace/log intake from app processes;
- `/__scenery/control-plane` — supervisor writes into the compact store;
- `/__storage` — streamed storage transfers
  (`cmd/scenery/dashboard_storage_http.go`);
- `/` and `/assets/*` — the embedded dashboard UI
  (`cmd/scenery/dashboard_static`).

The browser reaches it through the app origin: `internal/agent/router.go`
(branded/agent routing) and `cmd/scenery/local_path_router.go` (local path
mode) proxy `/runtime` (exact) to `/__scenery` and `/console/*` to the UI.

PostgreSQL inspection lives in `cmd/scenery/dashboard_postgres.go`; storage RPC
in `cmd/scenery/dashboard_storage.go`. TypeScript clients are rendered by
`internal/generate/generate_typescript.go`; the target schema lives in
`internal/spec/schemas.go` and `internal/spec/source_schema_metadata.go`, and is
validated in `internal/compiler/typescript_validate.go`. The normative client
specification is `docs/spec/typescript-client.md`; the local contract is
`docs/local-contract.md`.

## Milestones

1. RPC contract on the server: trimmed dispatch, dedicated result types,
   `/runtime/storage`, no notifications. Proof: `go test ./cmd/scenery
   ./internal/agent`.
2. Generated client: `dev_runtime` attribute, `dev-runtime.ts`, fixture
   coverage and typecheck. Proof: `go test ./internal/spec ./internal/compiler
   ./internal/generate`, fixture regeneration, `tsc` over generated clients.
3. UI removal and tooling move. Proof: `go test ./...`, full verifier.
4. Documentation and instructions. Proof: quick verifier knowledge checks
   inside the full run.
5. ONLV adoption (other repository). Proof: NextNext lint/typecheck/unit tests
   and in-app browser acceptance at the worktree origin.

## Plan of Work

Milestone 1 rewrites `dashboardServer.dispatchRPC` around the contract methods
`status`, `postgres/tables`, `postgres/schema`, `postgres/rows`, `db/query`,
`storage/inspect`, `storage/list`, `storage/stat`, `storage/delete`,
`storage/delete-preview`, `storage/delete-selection`, plus the CLI-internal
`traces/clear`. It deletes `dashboard_traces.go`, `dashboard_stored_requests.go`,
stored-request persistence in `internal/devdash`, `apiCall`, the notification
broadcaster and its call sites, and `dashboardListApps`. Storage transfers are
registered at `/__scenery/storage` and routed from `/runtime/storage` in both
routers.

Milestone 2 adds `dev_runtime` (bool, default false) to the
`scenery.typescript-client` resource schema and source metadata, validates it,
and emits an embedded, fixed `dev-runtime.ts` when true. The descriptor covers
the file like every other generated file. The `house` fixture enables it so the
committed output is typechecked by
`internal/generate/testdata/tsconfig.generated-clients.json`.

Milestone 3 deletes `apps/console` source, `cmd/scenery/dashboard_static`,
`dashboard_ui_build.go`, `scripts/build-dashboard-ui-embed.sh`, bundle status,
`/console` routing, `scenery harness ui` and its CDP driver, the verifier's
dashboard steps, and CI's dashboard lanes. `tools/typescript` keeps the
dependency manifest/lockfile and the QueryTable profiler.

## Concrete Steps

Working directory: repository root of this worktree.

    go test ./cmd/scenery ./internal/agent ./internal/devdash
    go test ./internal/spec ./internal/compiler ./internal/generate
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
    tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write

## Validation and Acceptance

Changed-area classes: CLI JSON contract, compiler or generator, UI catalog
(tooling path only), release-sensitive runtime. The full verifier
`go run ./scripts/verify --summary --write` is required and refreshes
`.scenery/harness/agent-context.json`; its `changed_area.recommended_commands`
union is fulfilled in addition to the Concrete Steps above.

Acceptance in ONLV (worktree created for this task, runtime started with this
checkout's CLI): the bottom panel's Runtime tab shows session status, service
processes and observability signals and refreshes after an API rebuild; the
Database tab lists tables, pages rows and runs `select 1`; the Storage tab lists
an object, uploads a new object, downloads it and deletes it with the displayed
version. `curl -si <origin>/console/` returns the app's own response (NextNext
SPA), not a Scenery dashboard.

## Idempotence and Recovery

All steps are source edits and regenerations; rerunning generation rewrites the
same bytes. If `tools/typescript/node_modules` is missing, run
`bun install --frozen-lockfile` in `tools/typescript`. The ONLV worktree is
disposable; it is removed with `scenery worktree remove` only on explicit
request.

## Artifacts and Notes

Validation (repository root of this worktree, 2026-09-23):

- `go run ./scripts/verify --summary --write`: pass (warnings only:
  pre-existing review-due documents and file-length hotspots).
- `golangci-lint run ./...`: 0 issues.
- Both fixture regenerations: ok; `tools/typescript/node_modules/.bin/tsc -p
  internal/generate/testdata/tsconfig.generated-clients.json` and
  `...tsconfig.catalog.json`: pass.
- `scenery logs --limit 500 -o jsonl` against the ONLV fixture: 501 valid
  `scenery.cli.event` lines.
- `go run ./scripts/verify --probe ui --probe core-separation --probe edge
  --summary`: typescript dependencies, client conformance, client and catalog
  typechecks, core responsibility separation and edge process probes pass.
- `go run ./scripts/verify --probe worktree --summary`: worktree runtime and
  PostgreSQL acceptance pass (after the fixture regeneration and sentinel
  change above).
- Not selected: `storage` (its app storage routes and CLI are unchanged; the
  runtime transfer route is covered by the worktree probe and live acceptance)
  and release certification (`scripts/release-gate.sh`, explicit only).

Live contract probe (ONLV fixture, `ws://localhost:4688/runtime`): `status`
returned `scenery.dev-runtime.status` with the pinned revision,
`postgres/tables` 151 tables, `db/query` `select 1`, `storage/inspect` the
`app` store, and `list-apps` `method not found`. `/runtime/storage` without
`X-Scenery-Storage-Request` and with a foreign `Origin` answered 403; a
foreign-origin WebSocket upgrade answered 403; `/console/` answered NextNext's
SPA.

ONLV browser acceptance (in-app browser, 1440x900 and 1024x700): Runtime tab
showed session, routes and observability and moved Running → Rebuilding →
Running after a Go edit, Build failed with the compiler output for a syntax
error, and Running after the revert. Database tab listed tables by schema,
paged rows 1–100 → 101–200, showed columns, ran a parameterized query
(`["Texas"]`) and kept select order with NULL cells, and reported a
PostgreSQL error. Storage tab required a tenant for the tenant-scoped store,
listed six fixture objects, downloaded one through `/runtime/storage` (200),
uploaded `qa/note.txt`, refused a second create-only upload with
`SCN8003: storage object precondition failed`, replaced it with the displayed
version, deleted it after confirmation, and deleted a two-object prefix after
its preview. ONLV checks: `bun run lint`, `bun run typecheck`, `bun test
src/features/devRuntime` (8 pass), `bun run i18n:check`, `bun run build` (no
dev-runtime code in `dist/`), `just repo-harness`.

## Interfaces and Dependencies

Contract methods and shapes are specified in `docs/local-contract.md`
(Development runtime RPC). The generated client exports `DevRuntimeClient`,
`DevRuntimeError`, and the request/result types; storage transfer helpers are
`uploadStorageObject` and `downloadStorageObject` methods on the client.
