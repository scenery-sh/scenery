---
name: scenery
description: Use when building, running, debugging, inspecting, validating, or generating clients for current scenery applications. Scenery is a Go-native runtime and CLI whose singular application model is declared in app.scn and package-local package.scn files.
---

# scenery

Scenery runs one supervised local runtime from the canonical graph in `app.scn` and package-local `package.scn` files. Go packages implement generated contracts; comments and package initialization register nothing.

This skill complements app-local instructions. Read the root `AGENTS.md` and every child scope on the path to files you will touch. Keep app-specific roots, outputs, environment names, validation, and product invariants in the client repository.

The paths below are relative to the Scenery source checkout, not the target
app. Use the corresponding documentation from the installed skill when
bundled; otherwise use a Scenery checkout matching the installed binary.
For command syntax without a checkout, start with `scenery help <command> -o json`.

Read next when needed:

- `docs/agent-guide.md` for agent workflow and generated-artifact rules.
- `docs/local-contract.md` for exact CLI grammar, JSON schemas, and artifact paths.
- `docs/app-development-cookbook.md` for native app recipes.
- `docs/ui-agent-contract.md` before changing Scenery's generated UI catalog.

## Route by Task

| Intent | Start with |
|---|---|
| Understand the application | `scenery inspect app -o json` |
| Investigate routing | `scenery inspect routes -o json` |
| Declare or debug an assistant | Read the assistant sections of `docs/local-contract.md` and `docs/spec/SPEC.md`; use `scenery inspect assistants -o json` |
| Investigate an operation | `scenery inspect endpoints -o json` |
| Investigate a runtime failure | `scenery doctor -o json`, then bounded `scenery logs -o jsonl --limit 200` |
| Change the source contract | `scenery fmt --check -o json`, `scenery check -o json`, then the applicable `scenery compile --view source\|effective\|expanded -o json` |
| Validate a completed application change | Focused tests, `scenery generate --check -o json`, then the applicable `scenery harness -o json --write` |
| Validate a Scenery repository change | Refresh `.scenery/harness/agent-context.json`, then run its exact `changed_area.recommended_commands` union |

Run only the route relevant to the task; expand when its evidence points elsewhere. Prefer `-o json` and `-o jsonl`, verify schema/spec revisions and producer identity, branch on stable `SCNxxxx` diagnostics, and resolve opaque source IDs through the returned source map.

## Mental Model

- `.scenery.json` marks the app root.
- `app.scn` installs package-local modules and pairs with generated `app.lock.scn`; `SCN1021` requires an exact filename migration, not an alias.
- Choose graph views intentionally: source preserves authored expressions, effective resolves inputs/defaults/patches, and expanded adds generators. Provenance paths are RFC 6901 pointers into the selected resource spec.
- Workspace, contract, implementation, deployment, and artifact revisions are separate. `scenery compile` does not invent an implementation revision; build supplies an exact target input manifest.
- Declare services, operations, bindings, auth, middleware, durable work, schedules, events, data, and UI in `.scn`.
- Declare MCP bindings, `mcp_server`/`mcp_connection` resources, and `assistant`
  surfaces in `.scn`. Scenery exposes a provider-neutral conversation API and
  keeps the selected implementation adapter in a supervised child behind
  private control and loopback MCP protocols.
- Generated Go contract and application-composition files are outputs, never source of truth.
- Declared `pkg/` libraries expose generated `scenerylib_<name>` facades; environments choose source or verified shared linkage without changing imports.
- `scenery up` starts the app process, rebuild loop, dashboard, API explorer, logs, traces, metrics, managed dev services, and configured frontends for one app root. `scenery up --desktop` additionally opens every frontend declaring `tauri` through the app-local Tauri 2 CLI; closing that window leaves the runtime running.
- Top-level `.scenery.json` `root` names the frontend served only at `/` across local, branded-domain, agent-proxied, and published-edge surfaces. A single frontend is the default root; other frontends remain at `/<name>/`.
- Public and auth HTTP bindings are externally reachable. Internal bindings are called through generated clients so auth, visibility, tracing, delivery, and error semantics remain intact.
- Use Git worktrees for multiple live code copies.

App-required build flags belong in `build.go_flags` in app config. Non-runtime tracked trees that should not trigger rebuilds belong in `watch.ignore`. Do not add ambient environment controls when checked-in config or an explicit flag is sufficient.

## Native Source and Generated Artifacts

Start from `testdata/apps/basic` or the minimal `README.md` example. Declare the workspace, app, toolchain, target, gateway, module, package import path, service constructor, typed records, operation, execution, and bindings; implement the constructor and methods with generated `scenerycontract` types.

Use this loop:

```sh
scenery fmt --check -o json
scenery compile --view expanded -o json
scenery generate --target contracts -o json
scenery generate --check -o json
scenery check -o json
go test ./...
```

Never commit or hand-edit cached `scenerycontract` or `internal/scenerygen` output. Use contract materialization only to publish a module. TypeScript targets use source materialization beneath a declared managed root or cache materialization beneath `.scenery/gen/typescript/`.

For a large direct HTTP download, declare `delivery = "stream"`, map every
result body from a required `bytes` value, and return the typed outcome plus
`scenery.NewByteStream(body, exactSize)`. Leave the mapped byte field empty and
do not close the body after a successful return; the generated adapter and
runtime own it. This is a typed response-only path, not a raw HTTP handler.

Import a declared library through its generated facade. Shared linkage requires an app-root-relative artifact manifest; build the fixed darwin/arm64 and linux/amd64 matrix with `scenery build --lib <name> --version <vN.N.N> -o json`. Swap verified versions alongside each other; never unload a Go c-shared runtime.

Use `scenery list|get|explain|graph ... -o json` for graph facts and `scenery diff --semantic` for compatibility. Semantic changes and deployments use immutable revision-bound plan/apply with one durable commit and authenticated receipt replay. The model-facing flow is `changes.plan` (compact summary), optional trusted `plans.get` (full review artifact), `changes.apply({plan_id})`, and `changes.receipt.get` for recovery. Apply loads the exact app-local issued plan, binds the server-owned caller/capabilities context, and rejects caller-recomputed approvals, operations, edits, or provider actions. A retry of an already committed plan returns its validated receipt without repeating side effects; corrupt or mismatched receipts fail closed.

Before semantic creation, read `resource_create_kinds` and `schema.get`; unadvertised kinds are unavailable. A terminal HTTP path tail uses final `{name...}` syntax plus one typed `path_tail` mapping, never a router glob or pre-encoded fragment.

Mutation-capable Eve adapters derive the principal from `ctx.session.auth` and
bind identity, app root, granted capabilities, and available session metadata
in server-owned execution context. `caller`, claimed capabilities, and approval
tokens are not model-visible tool inputs. The approval handler obtains a
plan-bound token only after user approval. Eve MCP connection definitions do
not currently expose per-action `callId` or configurable `toModelOutput`; use
Scenery's compact plan response and gateway-generated request ID. A separately
authored Eve tool may project richer review data with `toModelOutput`. Eve's
ordinary MCP tool approval is not an evolution approval token; approval-bearing
contract-agent apply still needs a trusted adapter/operator context until a
dedicated plan-bound broker exists.

## Assistant Surfaces

An assistant is a graph resource that binds one `mcp_server` to an authored
implementation and a Scenery-owned public conversation surface. Expose each
local operation through an ordinary `protocol = "mcp"`, `delivery = "call"`
binding. Use `mcp_connection` for remote Streamable HTTP tools; Scenery owns
credential termination, filtering, readiness, and authorization.

Use the provider-neutral lifecycle commands:

```sh
scenery assistant init <name> --mcp-server <name> --client <name> -o json
scenery assistant sync <name> -o json
scenery assistant status <name> -o json
scenery inspect assistants -o json
scenery inspect assistants --implementation -o json
```

Public conversation routes use opaque Scenery handles and normalized NDJSON
events with exclusive-cursor reconnects. Approval and cancellation are typed
public operations. The implementation adapter (currently `eve`) is private
developer/operator data: it may appear in authored source and explicit
implementation inspection, but not in public routes, generated clients,
OpenAPI/schemas, cookies, events, errors, or default status. `sync` reuses the
exact package/lock bytes in Scenery's content-addressed Node/npm cache and
never rewrites authored package files.

## Public Go Capabilities

- `scenery.sh` for runtime metadata and contract wire helpers.
- `scenery.sh/auth` for request auth, live standard-auth profiles, application
  permission checks, user lifecycle controls, and Google connection helpers. Configure one read-only
  `auth.PermissionChecker` during startup; permission names and storage remain
  application-owned, and `auth.HasPermissions` requires every supplied name.
  Persist `auth.CurrentAuditIdentity(ctx)` for audited work so impersonation
  retains separate effective and actor users.
- `scenery.sh/errs` for coded errors.
- `scenery.sh/library` for generated facade loading and load-alongside swaps;
  app code normally uses its typed facade instead of this package directly.
- `scenery.sh/durable` for non-registering durable steps and signals; ownership is declared in `.scn`.
- `scenery.sh/db` for service-scoped Postgres pools.
- `scenery.sh/datasource` and `scenery.sh/object` for typed constructor capabilities.
- `scenery.sh/storage` for app storage.

Standard-auth tenant tables are framework-owned under the app database's `scenery` schema. Use `auth.CurrentUser(ctx)` instead of querying the framework-owned user table. An app-owned, authorized lifecycle command may call `auth.DisableUser`, `auth.EnableUser`, or `auth.RevokeUserSessions`; disabling and session revocation are atomic, and enabling requires a fresh sign-in. Google-enabled apps use `auth.GoogleAccessToken` or `auth.GoogleAccessTokenForUser`; clients treat `google_reauth_required` as a reconnect prompt.

## Local Development and Debugging

Use `scenery up` for the live loop, `--detach` for a background runtime, and `--desktop` for configured Tauri shells. The default wait proves advertised routes and one frontend asset; use `--wait registered` only when readiness is intentionally deferred.

`scenery up` is idempotent per app root. Foreground reruns attach to its logs, Ctrl+C detaches without stopping it, and detached reruns report `already_running: true`. Use a worktree for a second live code copy.

The selected environment owns domains, exposure, ports, frontend serving, and deployment. Discover URLs with `scenery ps -o json`; never guess hidden ports or substrate paths. Diagnose with bounded logs, traces, and metrics before widening the search.

Deploy through a configured environment or its singular SSH target. SSH uses passwordless OpenSSH and rsync, preserves remote `.env*` and `.scenery`, waits for readiness, and provides no backend rollback. Verify with `scenery deploy status -o json`.

## Storage and Databases

Declare storage cells and stores in app config. App code uses `scenery.sh/storage`, never proxy sockets or object directories. Tenant-scoped private calls require auth context or `storage.WithTenantID`. Inspect with `scenery inspect storage -o json`; operate through `scenery storage status|ls|stat|put|get|rm`.

An explicit app `DATABASE_URL` is external. Otherwise `scenery up` manages one Postgres database per app root/worktree and service-scoped schemas. Use `scenery db apply` for schema mutation, `scenery db seed` for initial data and declared `database.seed.commands`, and `scenery db setup` for both. SQL seeds are immutable; file-backed commands rerun only when their explicit workspace input hash changes and must be atomic or idempotent. Do not make file generation apply database state.

Snapshots include only selected data. Verify checks every payload without stopping a target app. Stop the app before loading; use `--dry-run` first and `--mode overwrite --yes` only for exact replacement. Interrupted overwrite loads are safe to rerun.

## Generated TypeScript Clients

Declare each `typescript_client` target in `app.scn`, including gateways, materialization, and a managed output root for source mode:

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
```

Generated clients implement declared HTTP mappings and outcomes; they never infer routes or auth from Go names. Regenerate after reachable binding, type, codec, or auth changes. Every app HTTP response includes `X-Trace-Id`; browser observers should read that header.

For expensive application phases, use stable source-defined names with
`scenery.StartSpan`, pass its returned context into nested work, and call
`span.End(err)`. Never encode request values, user data, coordinates, IDs, or
other high-cardinality content in span names.

When an assistant is reachable from a target, the generated client also exposes
provider-neutral `client.assistants.<name>.createConversation`, `sendTurn`,
`streamEvents`, `resolveApproval`, and `cancelRun` methods. Its event/error
unions and NDJSON cursor handling are generated from the assistant public
schemas; no provider adapter import, URL, token, or private control field is
emitted.

For React, declare a page macro and any typed search/navigation metadata, then set the target's React tsconfig. Scenery owns generated adapters, routes, app shell, catalog, and staged typecheck. Use `createSceneryApp`, one authored route descriptor array, and the fixed slots; do not rebuild route selection, navigation, or the shell. Vite apps alias `@scenery/ui` and its token subpath to the materialized catalog and provide its peer dependencies.

Generated page macros and workspace tabs may carry opaque `application_key`
and `access_key` values. Supply one synchronous `resolveAccess` callback after
loading app-owned entitlements; Scenery uses it for navigation, direct-route
invocation, and tabs, but never as backend authorization. Reuse the returned
`routes` catalog and `matchSceneryRoute` instead of URL-prefix maps.

Choose the page macro by shape, inspect it with `scenery schema <kind> -o json`, and read the full contract only for that macro:

- `split_page` — two panes with app-owned sidebar/detail request-state slots; Scenery owns selection and layout.
- `content_page` — one required content slot; omit the source for static content.
- `detail_page` — one routed record whose declared business error maps to HTTP 404; use simple form dialogs or a typed app-owned action slot and refresh after mutation.
- `table_page` — a cursor-paginated, numeric-pagination, or complete-list workbench with typed filters/actions; only complete lists may group.

Keep generated page, route, dialog, and query wiring intact rather than rebuilding it in app code.

## Tasks and Workers

Use `scenery task list|inspect|run` for app-local `<domain>:<name>` code tasks; they may run while the graph is temporarily invalid. Use `scenery worker --app-root <path> --env <name>` for the worker role.

Single-file Go code tasks live under a domain `tasks` directory and use `//go:build ignore`; that build constraint is not an application declaration.

## UI Work

In a Scenery checkout, follow `apps/console/AGENTS.md` for dashboard work and
`ui/AGENTS.md` for catalog work. In a target app, generated table pages use
Scenery's binary-owned catalog; mount `generatedPages` and customize declared
slots or CSS tokens instead of editing materialized catalog files.

Before rewriting an app frontend, run `scenery inspect ui --frontend <name> -o human`. Move the top offender onto Astryx/`@scenery/ui` and StyleX tokens, then rerun; the score is triage guidance, not enforcement.

Run the target app's own frontend validation and browser acceptance. The
`scenery harness ui` command verifies Scenery's dashboard, not arbitrary
application pages.

## Command Reference

Use `scenery help <command> -o json` for one scoped machine-readable command
descriptor; omit `-o json` for human help. The full grammar lives in
`docs/local-contract.md`. Choose one command or graph view at a time; pipes
in syntax descriptions denote alternatives, not shell pipelines.

Scenery repository validation uses
`.scenery/harness/bin/scenery harness self --summary --write` and the root
validation matrix. `scenery harness ui -o json` is the separate dashboard
browser harness.

## Validation Before Finishing

For app changes:

```sh
scenery check -o json
scenery generate --check -o json
go test ./...
scenery harness -o json --write
```

When an assistant surface is changed, also run `scenery inspect assistants
-o json`, inspect the explicit `--implementation` view when debugging the
helper, and regenerate the declared TypeScript client. Check that provider
identity and private signatures stay out of public artifacts. When changing
Scenery itself, also run its `./scripts/test-assistant-public-surface.sh`.

For Scenery repository changes, follow the root `AGENTS.md`; changed paths and contract surfaces calculate the validation classes and exact command union. Keep Go's test cache enabled. Use `-count=1` or `--fresh-tests` only for explicit measurement or nondeterminism investigation.

Do not run `go install ./cmd/scenery` unless the human explicitly asks. Multiple worktrees share the installed binary; self-harness builds a worktree-local binary.

CLI installation and updates are source-only. Select a checkout revision and
build its dashboard before an explicitly requested install. Use separate
absolute binary paths for parallel versions. Compatible agent contracts are
shared without automatic replacement; incompatible health schema/spec fails
closed. Use the matching binary or a private agent home and distinct router
address. Changing agent home does not isolate machine-global DNS/edge listeners.
Installing a CLI does not migrate application dependencies or durable data.
