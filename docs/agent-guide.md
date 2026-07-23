# scenery Agent Guide

This guide is for agents using scenery applications or changing scenery itself. The installable `SKILL.md` is the portable quick reference; this document explains source-of-truth order, the current workflow, generated artifacts, and client-app integration.

## Source Of Truth Order

Use the narrowest current source that applies:

1. The target repository's root and child `AGENTS.md` files.
2. Current implementation, tests, and native `.scn` source.
3. Machine-readable CLI output and checked schemas.
4. `docs/local-contract.md`, this guide, and the app cookbook.
5. Historical ExecPlans only as history.

When prose and current JSON/tests disagree, fix the affected documentation in the same change or record the drift in an active ExecPlan. Do not add alternate declaration spellings to preserve old source.

## Current Application Model

Every supported app has:

- `.scenery.json` for independent runtime config;
- a required root `app.scn`;
- package-local `package.scn` files installed through root module blocks;
- an optional generated `app.lock.scn` dependency lock;
- Go implementations of generated native service contracts;
- generated outputs only beneath declared `workspace.managed_generated_roots`.

The `.scn` graph is the singular source of application semantics. Go comments and package initialization do not declare services, operations, routes, auth, middleware, data, durable work, schedules, events, pages, or renderers.

The role-named files are the only accepted spellings. `SCN1021` identifies a
retired pre-cutover filename and provides the exact rename; rename it rather
than adding an alias or fallback path.

Read root and package declarations before editing. Use graph commands instead of inferring ownership or references from text search:

```sh
scenery fmt --check -o json
scenery compile --view source -o json
scenery compile --view effective -o json
scenery compile --view expanded -o json
scenery list service -o json
scenery graph service.<name> -o json
scenery explain <address> <pointer> -o json
```

Source preserves authored expressions. Effective resolves inputs, defaults, and patches. Expanded adds generators. Provenance keys are RFC 6901 pointers into the selected resource spec. Source IDs are opaque; resolve them through `source_map`. Positions use zero-based Unicode-scalar columns and UTF-8 byte offsets.

## Native Change Loop

For ordinary app changes:

```sh
scenery doctor -o json
scenery fmt --check -o json
scenery check -o json
scenery generate --check -o json
go test ./...
scenery harness -o json --write
```

Edit authored `.scn` and Go implementation files. Go contracts, adapters, and composition are cache inputs: `check`, `test`, `build`, and `up` render them outside the checkout. A successful compile also refreshes a Scenery-owned, locally excluded root `go.work` so `gopls` and raw `go test ./...` resolve stable `scenerycontract` imports. Never hand-edit generated TypeScript or descriptors. Use `scenery generate --target contracts --materialize` only when exporting a published Go module.

Compilation intentionally leaves `implementation_revision` null. When exact implementation identity matters, use a declared build target:

```sh
scenery build --target development --output ./bin/app
```

The build revision comes from the complete content-addressed Go input manifest and resolved toolchain, not source globs or the ambient shell.

No-input operations use exact `std.type.unit`. CLI bindings own their help, completion, typed caller inputs, trusted runtime context, outcomes, and exit codes. Context-mapped fields must never become caller flags or arguments.

For terminal HTTP path tails, use only final `{name...}` syntax and add one matching `path_tail` mapping. Path tails are part of the current HTTP codec/runtime contract and require no extra source selector. Do not substitute router wildcards, pre-encoded fragments, or filesystem cleaning.

Generated Go constructor config fields reference typed package inputs. The input owns phase, type, constraints, and sensitivity; plaintext sensitive values fail compilation.

### Declared Go libraries

A package beneath `pkg/` may declare a `library` and library-owned operations
with direct record inputs and outcomes. Scenery generates
`scenerylib_<name>` beside the package in the external build/editor workspace.
App code imports that stable typed facade; never materialize, edit, or commit
the facade or its `export/` c-shared shim.

Select linkage per environment in `.scenery.json`:

```json
{
  "envs": {
    "local": {
      "default": true,
      "libraries": { "geometry": { "linkage": "source" } }
    },
    "production": {
      "libraries": {
        "geometry": {
          "linkage": "shared",
          "manifest": "dist/libraries/geometry/v1.2.3/geometry.scenery-library.json"
        }
      }
    }
  }
}
```

Build the portable fixed matrix with
`scenery build --lib geometry --version v1.2.3 -o json`. The default emits
darwin/arm64 and linux/amd64 artifacts plus a digest/ABI-bound manifest.
Shared startup fails closed if its artifact is missing, unsupported, stale,
tampered, or ABI-incompatible.

For a live load-alongside upgrade, call the generated facade's
`UseShared(newManifest)`; new calls use the new version atomically while active
old calls drain. `Versions()` exposes process-local state. Go c-shared runtimes
cannot be unloaded, so recycle long-running processes after unusually frequent
swaps. Validate both backends with a deterministic fixture on the same
architecture; cross-architecture floating-point bytes may differ.

## Diagnostics And Semantic Changes

Branch on stable diagnostic codes, never message text. Inspect the catalog with:

```sh
scenery schema SCN2101 -o json
```

Internal diagnostics publish a sanitized message and opaque report token.

For semantic creation, first read agent capabilities and verify the kind appears in `resource_create_kinds`, then fetch `schema.get`. Recursive schemas distinguish source attributes from child blocks, labels, cardinality, ordering, phases, defaults, constraints, sensitivity, and patchability. Unadvertised kinds are intentionally unavailable.

Semantic changes and deployments use revision-bound plan/apply. Planning retains the exact issued object beneath app-local trusted state. Apply rejects caller-recomputed plans before trusting expiry, approvals, operations, edits, or provider actions. Plans are single-use and invalid after bound state changes.

```sh
scenery changes plan ... -o json
scenery changes apply <plan> ... -o json
scenery deploy plan ... -o json
scenery deploy apply <plan> ... -o json
```

Use `scenery diff --semantic` for compatibility. Rename evidence is revision-bound and digest-checked; later diffs can load applied receipts or accept `--rename-receipts` explicitly.

## CLI Surfaces For Agents

Use `-o json` for compiler commands and command-specific current protocols. Never combine incompatible output modes.

| Intent | Command |
| --- | --- |
| Check host readiness | `scenery doctor -o json` |
| Validate native contract and generated outputs | `scenery check -o json` |
| Inspect canonical graph | `scenery compile --view expanded -o json` |
| Query resources and provenance | `scenery list|get|explain|graph ... -o json` |
| Inspect routed app views | `scenery inspect app|routes|services|endpoints -o json` |
| Rank React UI guardrail drift | `scenery inspect ui [--frontend <name>] -o human|json` |
| Inspect build and paths | `scenery inspect build -o json`, `scenery inspect paths -o json` |
| Inspect durable/storage capabilities | `scenery inspect durable -o json`, `scenery inspect storage -o json` |
| Generate/check Go artifacts | `scenery generate --target go [--check] -o json` |
| Generate/check a TypeScript target | `scenery generate --target typescript_client.<name> [--check] -o json` |
| Run app validation | `scenery harness -o json --write` |
| Follow logs | `scenery logs -o jsonl --limit 200` |
| Inspect traces and metrics | `scenery traces list -o json`, `scenery metrics list -o json` |
| Run code tasks | `scenery task list -o json`, `scenery task run <domain>:<name> -- [args...]` |
| Inspect databases | `scenery db list -o json`, `scenery db shell` |
| Apply initial DB state | `scenery db apply -o json`, `scenery db seed -o json`, `scenery db setup -o json` |
| Save, verify, or load app data | `scenery snapshot save|verify|load ... -o json` |
| Sync source to an allowed SSH target | `scenery deploy <ssh-target>` |

## Runtime Command Choice

- Use `scenery up` for the app root's one live development runtime and all safe local capabilities.
- Use `scenery up --detach` when the local agent should retain it; the default wait returns only after every advertised route and one declared frontend asset are reachable.
- `scenery up` reruns against an already-live app root are idempotent instead of failing: human foreground reruns report the existing runtime and attach to its logs (Ctrl+C detaches without stopping it), while `-o jsonl` and `--detach` reruns report and exit `0` (detached JSON sets `already_running: true`).
- Use `scenery ps -o json` to discover the current base URL, route manifest, child health, and substrate state.
- Use `scenery system agent restart` to restart only the control plane/router; registered Postgres and Victoria processes survive. On machines set up with `scenery deploy setup`, the agent is continuously owned by the `dev.scenery.agent` launchd LaunchAgent and restart cooperates with it (`supervised: true` in the JSON payload); `scenery deploy status -o json` reports supervision truth under `agent_supervisor`, reports the login-resume job's state and last exit code, and refuses `ready` when either supervisor is unloaded or the last resume completed unsuccessfully. Public resume is independent of optional `local.dev` wildcard DNS.
- Use `scenery system agent cleanup` to stop fingerprint-verified same-user processes tied to the pre-rebrand `~/.onlava` config or socket. It only reports old state by default; pass `--remove-state` to remove that directory explicitly.
- Use `scenery prune --older-than <duration>` for non-destructive stale record and substrate-lease cleanup. Add `--state`, `--db`, or `--all` only when the corresponding deletion is intended; database cleanup refuses external DSNs.
- Use `scenery doctor -o json` when startup reports an occupied Scenery port; it distinguishes duplicate Scenery owners from foreign listeners, and startup never falls back to an unadvertised router port.
- Use `scenery logs --follow` for the current runtime.
- Use `scenery down` to stop it; add destructive cleanup flags only intentionally.
- Use `scenery worker` for a worker-role runtime serving declared durable executions and schedules.
- Use `scenery build` for a deployable binary.
- Use `scenery deploy <ssh-target>` only for configured beta single-server source sync. The target must belong to exactly one `envs.<name>.deploy.ssh`; `scenery deploy --env <name>` is the equivalent shortcut when that env has one target. Scenery preserves remote `.env*`, `.scenery`, and Scenery-owned `go.work`, restarts with `--env <name>`, and publishes only that env's production frontends.
- Use `scenery generate` only for file generation. It must not apply database state.
- Use `scenery task` for app-local code tasks.
- Use Git worktrees for another live code copy.

Default local routing resolves the single default named env and gives one app root/worktree one localhost base URL. `scenery up --env <name>` selects another declared env. Its `domain`, `expose`, port fields, and frontend `serve` modes determine routing; session JSON includes `environment`. Domain-edge unreadiness degrades to localhost without a cross-env redirect.

Treat Caddy, dnsmasq, Victoria, proxy sockets, hidden ports, and local stores as substrate unless the task explicitly diagnoses them. Prefer scenery inspection and status commands over direct substrate access.

## Storage And Databases

Storage remains app config because it is a runtime capability, not an application declaration. App code uses `scenery.sh/storage`. Private stores stay internal; tenant-scoped calls require standard-auth context or `storage.WithTenantID`.

```sh
scenery inspect storage -o json
scenery storage status -o json
scenery storage ls <store> -o json
```

An explicit app-level `DATABASE_URL` is external. Otherwise `scenery up` manages one database per app root/worktree and service schemas. Use `db apply` for schema/app setup, `db seed` for initial data, and `db setup` for both. Changed applied seeds and destructive seed SQL fail closed.

For a portable point-in-time copy, explicitly select the data classes. Stop the runtime before load; overwrite is destructive and requires `--yes`.

```sh
scenery snapshot save --db --storage --output app.zip -o json
scenery snapshot verify --input app.zip -o json
scenery snapshot load --db --storage --input app.zip --mode overwrite --yes -o json
```

Verify validates every payload checksum without discovering or stopping a target app. Load repeats validation before changing data. Managed database overwrite and storage-store replacement are rerunnable after interruption; use `--dry-run` for target-specific preflight only. Use `scripts/snapshot-backup.sh` from the host scheduler for verified retention and optional rclone replication; Scenery does not install or own that schedule.

## Generated And Cache Artifacts

Generated/cache outputs include:

```text
<typescript-output-root>/
  client.ts
  runtime.ts
  types.ts
  metadata.ts
  index.ts
  scenery.typescript-client-generated.json
```

Go generation lives in Scenery's external build/editor caches and is never ordinary source. App-local `.scenery/` state is cache/evidence, not source; it may contain TypeScript cache materialization, editor ownership, build records, sessions, issued plans, logs, and harness outputs. Do not commit it. A migration may safely remove descriptor-authenticated legacy Go trees with `scenery generate --prune-materialized-go`.

## TypeScript Client Integration

Declare each target in root `app.scn`, select exact gateways, and choose `materialization = "source"` for a checked-in SDK or `materialization = "cache"` for `.scenery/gen/typescript/<name>`. Source output must remain beneath a managed root. Generated clients derive only from reachable canonical resources and exact binding codecs; they do not infer routes or auth from Go symbols.

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
```

Regenerate after changes to reachable types, bindings, codec mappings, authentication, authorization, or gateway behavior. When standard Google OAuth is enabled, Scenery also projects its framework-owned connection start/status/disconnect endpoints into inspection and the native client; use those generated methods instead of app-owned fetch wrappers. Keep app imports pointed at the declared output root.

For a declarative frontend, add `react { tsconfig = "..." }` to the TypeScript target and put URL search types plus optional navigation placement on each generated page declaration. Generation writes page adapters, `routes.generated.ts`, `app.generated.tsx`, and the binary-owned catalog in one managed transaction. Use `createSceneryApp`; pass its optional `client` when generated pages need the app's authenticated or customized `PublicApiClient`, register app-owned pages through its one descriptor-array extension, and fill the fixed auth/top-bar/content/link/icon slots. The generated layer owns the TanStack route tree, intent preloading, `Outlet`, active navigation, and `ClientAppShell`; do not keep a second route tree, navigation list, or hidden-page mount system. Keep one app-owned `QueryClientProvider`, supply the React/Astryx/StyleX/TanStack peers, and never edit generated output.

For a generated two-pane screen, declare a unit-input operation with HTTP and inherited internal bindings, app-owned `react_component` slots for `sidebar` and `detail`, and a generic `split_page`. Optional `sidebar_actions` and `detail_header` slots share the raw request state and URL-backed selection state. Scenery generates transport, request/selection state, and the reusable split layout only; each domain-specific slot owns its loading/error/ready rendering and should wrap those branches with `QueryState` from `@scenery/ui`.

For a generated one-column screen, use `content_page` with one app-owned `content` slot. Omit `source` for static content: `content` and optional header `actions` then receive no props. For loaded content, declare the unit-input HTTP plus inherited-internal operation pair and set `source`; both slots receive the shared typed request state and should adapt it to `QueryState` with `queryStateProps`. `max_width` bounds the centered content well.

For one routed record, use `detail_page` with a parameterized path, a call-delivery HTTP load binding, and declared field sections. The load operation must declare a business-error variant that this binding maps to HTTP 404; return that generated outcome when the entity is absent so the page renders typed error state instead of `system.internal`. Route parameters map to operation inputs by name or through `param` overrides. `presentation = "page" | "dialog" | "both"` selects the generated routed page, controlled dialog, or both without duplicating content. `hide_empty = true` omits an empty field while preserving zero and false values. Related `table_page` entries map a route parameter into one otherwise-unclaimed table input and render without nested page chrome. Simple declared actions open generated `form_dialog`s; use the typed app-owned `actions` slot for conditional choices, confirmations, or other domain workflows, and call its `onMutated` callback after success.

For a generated operations workbench, choose one explicit list contract. CRUD sources use fingerprint-bound cursor pagination. A call-delivery HTTP binding can either map numeric pagination through `pagination { page, page_size, total }`, or return one complete typed list with no pagination. Set `source` and the result-record `items` field in both binding forms. Use `metadata = ["summary", "types"]` on a binding table to project only those auxiliary operation-result fields into typed `context.metadata`; `items` and pagination `total` cannot be projected again. Map nonstandard operation input names with `query { search, sort, direction }` and `filter.input`; set `query.search_hidden = true` only when an app toolbar owns the visible search input, and drive it through `context.controls.setSearch`. Supply typed invisible fixed inputs with labeled `predicate` blocks. Only complete-list tables may declare `group "field"`, because a cursor or numeric page cannot provide honest section counts. Reuse `status_map` resources for badge/filter/group presentation; set `pinned = true` only on the zero-to-two generated selectors that deserve inline quick access. A filter with `hidden = true` stays typed and query-mapped but is omitted from built-in controls so a custom toolbar can own it. Every non-hidden filter remains in the Filters popover, active values remain visible as removable chips, and group/sort/direction stay separate from the filter count. Filters, toolbar, empty, and footer slots receive `TablePageResultContext` with current rows, optional total/truncation/projected metadata, filtered state, `isPlaceholderData`, `isRefreshing`, query, and query controls for setting/clearing one enum-filter value, setting search, or refreshing. A toolbar defaults to the Page header; use `placement = "content"` for a large workbench above the table. `row_detail` owns inline or panel presentation. Use mutually exclusive `row_action` for a selected-row workflow that receives `row` and `onClose`; it remains mounted outside request-state rendering. Bind `table_page.stats` to a unit-input metrics operation; declare loaded-result CSV `export`; use `hidden = true` for export-only columns and `export = false` for display-only columns. CSV is UTF-8, spreadsheet-formula hardened, and names `{date}` from the user's local calendar. `form_dialog` derives string/enum fields from a mutation input record, table actions open it, failures stay inline, and success invalidates list and stats query keys. `row_detail.dialog` is the inline-only edit path when every mutation input can be seeded from a matching row field. Regenerate instead of editing emitted files.

Tables default to `scroll = "table"`, which keeps page controls fixed and gives the grid the vertical scrollport. Use `scroll = "page"` when stats, controls, and rows should move together in the page scrollport.

Stats tiles may format primary and sub-line fields and declaratively set, toggle, or clear a typed filter or predicate through that same table request state. Date/datetime filters may add local-calendar `today`, `last_7_days`, and `month_to_date` presets without changing their paired wire inputs.

For UI cleanup triage, run `scenery inspect ui --frontend <name>` and start with
the highest-score file while reading both axes independently. Replace raw
layout and controls with the existing Astryx or `@scenery/ui` vocabulary, and
replace hardcoded design values with the app's imported StyleX theme tokens.
Re-run the report and confirm the relevant raw counts fall without treating the
score as a pass/fail threshold. Use `-o json` for automation and compare
`markup.ds_share` and `style.token_share` separately; a strong result on one
axis does not excuse drift on the other.

## Client-App Instructions

The installable skill is necessary but not sufficient for a client repository. Keep a small app-local `AGENTS.md` containing:

- app root and config path;
- module/frontend roots;
- generated client output paths;
- required environment names without values;
- standard validation commands;
- whether agents should start a detached runtime;
- product/domain invariants scenery cannot know.

Do not copy scenery's full skill or repository manual into every app.

## Working In The scenery Repository

Read root `AGENTS.md` plus every applicable child instruction file, then read the `docs/local-contract.md` and `docs/agent-guide.md` sections covering the surface you are changing. Check `docs/plans/active.md` when the area may have an active ExecPlan; run `scenery inspect docs -o json` when choosing doc-gardening work. Use an ExecPlan for complex features, migrations, or substantial refactors.

Validate ordinary changes with:

```sh
go test ./...
go vet ./...
```

For substantial changes, also run:

```sh
scenery harness self --summary --write
```

Ordinary, focused, and substantial final validation uses Go's test result
cache. Use `-count=1` or `scenery harness self --fresh-tests` only when
explicitly measuring fresh execution or investigating nondeterminism.

Do not install a shared CLI during agent validation. Self-harness builds a worktree-local binary.

### Repository Mental Model

scenery is a Go-native service runtime and local development platform. Think in app roots, app runtimes, and capability surfaces first; Victoria, agent routing, generated cache files, hidden ports, and local stores are substrate details unless the task is explicitly debugging that substrate.

- App roots are marked by `.scenery.json`.
- `.scn` source is the singular current app model. Root `app.scn` installs package-local `package.scn` modules and pairs with generated `app.lock.scn`; retired contract filenames fail with `SCN1021` instead of acting as aliases. Go source implements the generated native contracts but is not scanned for declarations.
- Generated Go contracts, adapters, composition, descriptors, and entrypoints live in external build/editor caches. Successful compilation maintains an ownership-verified, locally excluded root `go.work` for raw Go/editor resolution; source materialization is explicit export mode only.
- The compiler exposes source/effective/expanded graphs and separate workspace, contract, implementation, deployment, and artifact revisions. Source retains authored expressions, effective resolves inputs/defaults/patches, expanded adds generators, and every provenance key is an RFC 6901 pointer into that view's resource spec.
- `scenery task run <domain>:<name> -- [args...]` runs an app-local code task.
- `scenery worker` builds once and starts a worker-role runtime for declared durable executions and schedules.
- `scenery up` starts the app root's one live dev runtime: supervised app process, file watching, dashboard, API explorer, logs, traces, metrics, managed dev services, and optional frontend routing. Detached `--wait ready` returns only after every advertised route and one declared frontend asset are reachable. Re-running `scenery up` while a verified live owner already runs the same app root succeeds instead of failing: the human foreground form reports that runtime and attaches to its logs (Ctrl+C detaches without stopping it), `-o jsonl` reports and exits `0`, and detached reruns apply the requested wait readiness to the existing owner and set `already_running` in the JSON result. While that supervisor remains live, shared Victoria observability is probed and recovered as one managed stack; failed recovery is always surfaced as a degraded error rather than hidden behind verbose output.
- `scenery deploy <ssh-target>` is beta single-server source sync: the target belongs to exactly one `envs.<name>.deploy.ssh`; the remote restart and publication use that env name, rsync preserves remote `.env*` and `.scenery`, and status/registry records the environment. `scenery deploy --env <name>` selects the env directly when it has one target.
- Public deploy hosts have two service managers: launchd on macOS (privileged loopback helper) and systemd on Linux (`scenery deploy setup` as root installs `scenery-agent.service`, `scenery-edge.service` binding public 80/443 directly, and a boot-time deploy resume oneshot). While the edge unit exists, edge restart/reload paths converge through systemd. Public resume uses bounded reacquisition to retain a healthy fingerprinted Caddy/helper/agent chain and restarts it only when unavailable, independently of optional `local.dev` DNS; deploy status is degraded when the loaded one-shot resume job last completed with a nonzero exit.
- The local agent and managed edge are single-owner processes. Startup fails closed when a verified owner holds the runtime lock, reaps only same-user stale owners whose process fingerprints still match, and `scenery doctor` reports duplicate owners or foreign listeners on Scenery-owned ports. On machines configured with `scenery deploy setup`, the agent is continuously supervised by the `dev.scenery.agent` launchd LaunchAgent: LaunchAgent installs bootstrap the job (plist presence alone is not installation), teardown boots it out before removal, every agent start path cooperates with the supervisor instead of racing its KeepAlive respawn, and `scenery deploy status` reports supervision truth under `agent_supervisor` and is not `ready` without a loaded supervisor.
- `scenery prune --older-than <duration>` removes only eligible stale session records and their matching substrate leases by default; filesystem state and managed databases require explicit `--state`, `--db`, or `--all`. `scenery system agent cleanup` is the explicit pre-rebrand sweep and signals only same-user processes whose exact legacy managed path and live ownership fingerprint both verify; legacy state removal additionally requires `--remove-state`.
- Portable snapshots can be verified without a target app or stopped runtime. Scheduled retention, off-machine copy, and restore drills remain operator-owned through `scripts/snapshot-backup.sh` plus the host scheduler.
- `scenery system agent restart` restarts only the local control plane and router. Registered shared substrate processes survive; destructive shutdown stays with substrate-specific commands and verified lifecycle owners.
- Every CLI invocation best-effort appends one coarse, argument-free usage record to `~/.scenery/telemetry.jsonl`; telemetry write failures never affect the command result.
- `.scenery.json` declares named `envs`; exactly one reserved `local` env is default. The selected env owns domain/exposure/ports, frontend serve modes, deploy targets, dotenv layering, and secret strictness. `scenery up --env <name>` selects it, session manifests record it, and failed branded-domain validation stays on localhost without redirecting to another env.
- Public and auth endpoints are externally reachable. Private endpoints are internal-only and must be called through generated helpers.
- Typed endpoints decode path/query/header/cookie/body inputs into Go values and encode typed responses.
- CRUD resources can declare explicit search/filter/sort capabilities with fingerprint-bound cursor pagination. `table_page` can use that cursor-paginated CRUD list, a call-delivery HTTP binding with explicit numeric `pagination` mappings, or a binding whose named result field is one complete typed list. Binding input names are explicit through `query`, `filter.input`, and typed fixed `predicate` declarations. It can compose server metrics, declarative filters, reusable `status_map` badges, loaded-result CSV, visible/export-only columns, response-aware filter/toolbar/empty/footer slots, inline or resizable-panel row detail, an app-owned `row_action`, and generated mutation `form_dialog` actions. Only complete-list tables may group; cursor and numeric-page tables cannot. `content_page.source` is optional: sourced pages pass request state to their slots, while static slots receive no request-state props. `workspace_page` composes declared pages into one generated tabs/sidebar workspace. `detail_page` maps dynamic route parameters into one typed load operation, renders declared field sections and related tables, and shares one generated content component between routed-page and controlled-dialog presentations; simple mutations reuse `form_dialog`, while richer domain workflows stay in its typed app-owned actions slot. These page macros expand to ordinary page/renderer resources. React-enabled TypeScript clients materialize the binary-owned catalog, generated pages, typed route descriptors, TanStack route tree, navigation, and app shell only after staged verification by Scenery's exact managed native TypeScript checker. The catalog exposes composed components plus blessed Astryx primitives from `@scenery/ui` and semantic StyleX variables from the sole `@scenery/ui/tokens.stylex` subpath. Domain-specific UI remains in app-owned `react_component` slots or the generated app's fixed extension/visual slots, never in Scenery's catalog or compiler.
- Generated table metrics may format primary/sub values and declaratively set, toggle, or clear typed filters or predicates through the table's singular request state. Date/datetime filter presets are local-calendar client shortcuts over the existing paired typed inputs.
- `scenery inspect ui` gives declared React frontends a read-only, per-file markup/style adherence report and ranked cleanup queue; its score is triage guidance, not enforcement.
- Terminal HTTP path tails use `{name...}` plus one typed `path_tail` mapping under the HTTP codec/runtime contract. They capture zero or more complete segments with exact/literal/parameter/tail precedence, strict one-time segment decoding, ordinary typed Go inputs, and independently encoded TypeScript segments.
- Generated internal calls preserve route, private access, auth context, tracing, and error semantics.
- Constructors receive typed `scenery.sh/datasource` and `scenery.sh/object` capabilities; built-in CRUD, fixtures, views, pages, and renderers stay in the same generated application composition.
- Go packages beneath `pkg/` may declare a contract-bearing `library` whose generated typed facade selects source linkage or a verified hot-swappable c-shared artifact per environment. Shared linkage supports exactly darwin/arm64 and linux/amd64, loads through `scenery.sh/library`, and never unloads a Go runtime.
- Agent capabilities expose exact `resource_create_kinds`; `scenery schema` / `schema.get` provide the recursive authored shape, and semantic creation must reject unadvertised kinds instead of guessing blocks, labels, or source destinations.
- Mutation plans normalize typed values/references and resolved kind/schema identities before hashing. Planning retains the exact canonical plan under app-local trusted state, and apply rejects caller-recomputed plans before trusting expiry, approvals, operations, edits, or provider actions. Approval-bearing migration transitions use `--out <plan>` followed by `migrate apply <plan>` so the detached token binds the exact issued plan instead of a replanned expiry. Semantic renames emit revision-bound, digest-checked plan/apply receipts, including migration-manifest references and containing-module descendants; later diffs load matching app-local receipts or accept `--rename-receipts` explicitly.

### Fully Generated Client Rules

React-enabled client apps use the generated route tree, navigation, and shell, with app-owned code reduced to one route-descriptor extension array and fixed visual slots:

- Prefer generation-shaped contracts: a page's routing surface is data (path, search-parameter schema, component reference), not app logic. Search-parameter contracts belong in the page's `.scn` declaration, not in hand-written client router code.
- Route-descriptor and route-tree generation belongs in `internal/generate`, never in the `ui/` catalog; the catalog stays router-agnostic with router libraries as app-side peers.
- Overrides flow through declared slots and app-owned `react_component` resources only. Do not add override mechanisms that require apps to edit or fork materialized output.
- Hand-written pages register through `SceneryRouteDescriptor`; do not create a second TanStack route tree, navigation list, shell, or parallel page-selection system.
- A generated-page conversion does not cut over or delete its hand-written production route until a feature-by-feature inventory, focused tests that survive the deletion, and authenticated browser acceptance prove identical functionality. Keep the generated candidate on a separate non-navigation route until that gate passes.

### Public Surface Checklist

When editing source that changes the public app model, confirm the docs and tests cover:

- services, operations, executions, HTTP/internal/CLI bindings, authentication, authorization, and middleware resources
- CLI bindings, including generated help/completion, typed input, trusted context, delivery, outcomes, and exit codes
- `std.type.unit`, data sources, entities/views/CRUD/fixtures, pages/renderers, and typed constructor capability injection
- generated contract input/outcome types and explicit `.scn` HTTP request/response mappings
- public packages: `scenery`, `auth`, `errs`, `durable`, `db`, `datasource`, `object`, `storage`
- standard auth configuration and generated endpoints
- private/internal call behavior
- worker, durable, schedule, middleware, and generated TypeScript client behavior when touched

### Fresh Worktree Preflight

A fresh worktree fails UI and self-harness lanes for environment reasons, not code reasons, until:

1. Self-harness provisions `apps/console` dependencies itself: the `console dependencies` step runs `bun install --frozen-lockfile` before the tsc-dependent lanes and skips them with one actionable diagnostic when bun is missing or the install fails. No manual `bun install` is needed; only the `bun` binary itself must be installed.
2. `./scripts/build-dashboard-ui-embed.sh` runs once. Only `placeholder.txt` is tracked under `cmd/scenery/dashboard_static/dist`; the real embed bundle is built locally.
3. Self-harness runs through the worktree-local binary: `.scenery/harness/bin/scenery harness self --summary --write`. The `dashboard ui fresh` lane compares the invoking binary's own embedded dashboard bundle in-process, so the installed PATH `scenery` fails that lane in any worktree whose console build differs. The first installed-`scenery` run builds the worktree-local binary; rerun through it before trusting the `dashboard ui fresh` result.

### Self-Harness Timing

Self-harness timing keeps a five-second optimization target separate from its operational lanes: cached and fresh runs use five-second advisory budgets, while release mode enforces 30 seconds. Only explicit `--fresh-tests` runs use isolated timing confirmation. That fresh lane uses package parallelism three, selected from repeated measurements on the maintainer machine. The postgres service probe runs its smoke proof by default and its full DB proof (durable, auth, reset, snapshot) only in release mode; its step summary carries per-segment timings.

## Keeping Agent Docs Fresh

When behavior changes, update the current owning layers together: root/child `AGENTS.md`, `SKILL.md`, this guide, `docs/local-contract.md`, app cookbook, schemas, and `docs/knowledge.json` as applicable. Historical ExecPlans remain historical; add a short current-contract pointer rather than rewriting their original decisions.
