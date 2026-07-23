---
name: scenery
description: Use when building, running, debugging, inspecting, validating, or generating clients for current scenery applications. Scenery is a Go-native runtime and CLI whose singular application model is declared in app.scn and package-local package.scn files.
---

# scenery

Scenery runs one supervised local runtime from the canonical graph in `app.scn` and package-local `package.scn` files. Go packages implement generated contracts; comments and package initialization register nothing.

This skill complements app-local instructions. Read the root `AGENTS.md` and every child scope on the path to files you will touch. Keep app-specific roots, outputs, environment names, validation, and product invariants in the client repository.

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
| Investigate an operation | `scenery inspect endpoints -o json` |
| Investigate a runtime failure | `scenery doctor -o json`, then bounded `scenery logs -o jsonl --limit 200` |
| Change the source contract | `scenery fmt --check -o json`, `scenery check -o json`, then the applicable `scenery compile --view source\|effective\|expanded -o json` |
| Validate a completed application change | Focused tests, `scenery generate --check -o json`, then the applicable `scenery harness -o json --write` |
| Validate a substantial Scenery change | `go test ./...`, then `.scenery/harness/bin/scenery harness self --summary --write` |

Run only the route relevant to the task; expand when its evidence points elsewhere. Prefer `-o json` and `-o jsonl`, verify schema/spec revisions and producer identity, branch on stable `SCNxxxx` diagnostics, and resolve opaque source IDs through the returned source map.

## Mental Model

- `.scenery.json` marks the app root.
- `app.scn` installs package-local modules and pairs with generated `app.lock.scn`; `SCN1021` requires an exact filename migration, not an alias.
- Choose graph views intentionally: source preserves authored expressions, effective resolves inputs/defaults/patches, and expanded adds generators. Provenance paths are RFC 6901 pointers into the selected resource spec.
- Workspace, contract, implementation, deployment, and artifact revisions are separate. `scenery compile` does not invent an implementation revision; build supplies an exact target input manifest.
- Declare services, operations, bindings, auth, middleware, durable work, schedules, events, data, and UI in `.scn`.
- Generated Go contract and application-composition files are outputs, never source of truth.
- Declared `pkg/` libraries expose generated `scenerylib_<name>` facades; environments choose source or verified shared linkage without changing imports.
- `scenery up` starts the app process, rebuild loop, dashboard, API explorer, logs, traces, metrics, managed dev services, and configured frontends for one app root. `scenery up --desktop` additionally opens every frontend declaring `tauri` through the app-local Tauri 2 CLI; closing that window leaves the runtime running.
- Public and auth HTTP bindings are externally reachable. Internal bindings are called through generated clients so auth, visibility, tracing, delivery, and error semantics remain intact.
- Use Git worktrees for multiple live code copies.

App-required build flags belong in `build.go_flags` in app config. Non-runtime tracked trees that should not trigger rebuilds belong in `watch.ignore`. Do not add ambient environment controls when checked-in config or an explicit flag is sufficient.

## Native Source and Generated Artifacts

Start from `testdata/apps/basic` or the minimal `README.md` example. Declare the workspace, app, toolchain, target, gateway, module, package import path, service constructor, typed records, operation, execution, and bindings; implement the constructor and methods with generated `scenerycontract` types.

Use this loop:

```sh
scenery fmt --check -o json
scenery compile --view expanded -o json
scenery generate --target go -o json
scenery generate --check -o json
scenery check -o json
go test ./...
```

Never commit or hand-edit cached `scenerycontract` or `internal/scenerygen` output. Use contract materialization only to publish a module. TypeScript targets use source materialization beneath a declared managed root or cache materialization beneath `.scenery/gen/typescript/`.

Import a declared library through its generated facade. Shared linkage requires an app-root-relative artifact manifest; build the fixed darwin/arm64 and linux/amd64 matrix with `scenery build --lib <name> --version <vN.N.N> -o json`. Swap verified versions alongside each other; never unload a Go c-shared runtime.

Use `scenery list|get|explain|graph ... -o json` for graph facts and `scenery diff --semantic` for compatibility. Semantic changes and deployments use immutable revision-bound plan/apply. Apply accepts only the exact app-local issued plan and rejects caller-recomputed approvals, operations, edits, or provider actions.

Before semantic creation, read `resource_create_kinds` and `schema.get`; unadvertised kinds are unavailable. A terminal HTTP path tail uses final `{name...}` syntax plus one typed `path_tail` mapping, never a router glob or pre-encoded fragment.

## Public Go Capabilities

- `scenery.sh` for runtime metadata and contract wire helpers.
- `scenery.sh/auth` for request auth and standard-auth/Google connection helpers.
- `scenery.sh/errs` for coded errors.
- `scenery.sh/library` for generated facade loading and load-alongside swaps;
  app code normally uses its typed facade instead of this package directly.
- `scenery.sh/durable` for non-registering durable steps and signals; ownership is declared in `.scn`.
- `scenery.sh/db` for service-scoped Postgres pools.
- `scenery.sh/datasource` and `scenery.sh/object` for typed constructor capabilities.
- `scenery.sh/storage` for app storage.

Standard-auth tenant tables are framework-owned under the app database's `scenery` schema. Google-enabled apps use `auth.GoogleAccessToken` or `auth.GoogleAccessTokenForUser`; clients treat `google_reauth_required` as a reconnect prompt.

## Local Development and Debugging

Use `scenery up` for the live loop, `--detach` for a background runtime, and `--desktop` for configured Tauri shells. The default wait proves advertised routes and one frontend asset; use `--wait registered` only when readiness is intentionally deferred.

`scenery up` is idempotent per app root. Foreground reruns attach to its logs, Ctrl+C detaches without stopping it, and detached reruns report `already_running: true`. Use a worktree for a second live code copy.

The selected environment owns domains, exposure, ports, frontend serving, and deployment. Discover URLs with `scenery ps -o json`; never guess hidden ports or substrate paths. Diagnose with bounded logs, traces, and metrics before widening the search.

Deploy through a configured environment or its singular SSH target. SSH uses passwordless OpenSSH and rsync, preserves remote `.env*` and `.scenery`, waits for readiness, and provides no backend rollback. Verify with `scenery deploy status -o json`.

## Storage and Databases

Declare storage cells and stores in app config. App code uses `scenery.sh/storage`, never proxy sockets or object directories. Tenant-scoped private calls require auth context or `storage.WithTenantID`. Inspect with `scenery inspect storage -o json`; operate through `scenery storage status|ls|stat|put|get|rm`.

An explicit app `DATABASE_URL` is external. Otherwise `scenery up` manages one Postgres database per app root/worktree and service-scoped schemas. Use `scenery db apply` for schema mutation, `scenery db seed` for initial data, and `scenery db setup` for both. Do not make file generation apply database state.

Snapshots include only selected data. Verify checks every payload without stopping a target app. Stop the app before loading; use `--dry-run` first and `--mode overwrite --yes` only for exact replacement. Interrupted overwrite loads are safe to rerun.

## Generated TypeScript Clients

Declare each `typescript_client` target in `app.scn`, including gateways, materialization, and a managed output root for source mode:

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
```

Generated clients implement declared HTTP mappings and outcomes; they never infer routes or auth from Go names. Regenerate after reachable binding, type, codec, or auth changes.

For React, declare a page macro and any typed search/navigation metadata, then set the target's React tsconfig. Scenery owns generated adapters, routes, app shell, catalog, and staged typecheck. Use `createSceneryApp`, one authored route descriptor array, and the fixed slots; do not rebuild route selection, navigation, or the shell. Vite apps alias `@scenery/ui` and its token subpath to the materialized catalog and provide its peer dependencies.

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

Follow `apps/console/AGENTS.md` for dashboard work. Generated table pages use Scenery's binary-owned catalog; mount `generatedPages` and customize declared slots or CSS tokens instead of editing materialized catalog files.

Before rewriting an app frontend, run `scenery inspect ui --frontend <name> -o human`. Move the top offender onto Astryx/`@scenery/ui` and StyleX tokens, then rerun; the score is triage guidance, not enforcement.

```sh
cd apps/console
bun run lint
bun run typecheck
bun run build
cd ../..
scenery harness ui -o json --write
```

## Command Reference

Use `scenery help <command> -o json` for one scoped machine-readable command descriptor, omit `-o json` for human help, and use `docs/local-contract.md` for the full grammar:

```text
scenery doctor -o json
scenery fmt --check -o json
scenery check -o json
scenery compile --view source|effective|expanded -o json
scenery inspect app|routes|services|endpoints|durable|storage|ui -o json
scenery logs [-o jsonl] [--limit <n>] [--follow]
scenery up [--env <name>] [--app-root <path>] [--desktop] [-o jsonl] [--detach]
scenery ps [--app-root <path>] [-o json]
scenery down [--app-root <path>] [-o json]
scenery build [--app-root <path>] [--target <go-target>] [--output <path>] [-o human|json]
scenery build --desktop [--env <name>] [--app-root <path>] [-o human|json]
scenery list|get|explain|graph ... [--app-root <path>] -o json
scenery diff --semantic BASE TARGET [--rename-receipts <path>] -o json
scenery generate [--app-root <path>] [--target contracts|typescript_client.<name>] [--materialize] [--prune-materialized-go] [--merge-editor-workspace] [--check] -o json
scenery changes plan|apply ... -o json
scenery deploy plan|apply ... -o json
scenery task list|inspect|run ...
scenery db list|path|shell|apply|seed|setup|reset|drop ...
scenery snapshot save|verify|load ...
scenery test [--app-root <path>] [go test flags/packages...]
scenery harness -o json --write
scenery harness ui -o json --write
.scenery/harness/bin/scenery harness self --summary --write
```

## Validation Before Finishing

For app changes:

```sh
scenery check -o json
scenery generate --check -o json
go test ./...
scenery harness -o json --write
```

For Scenery repository changes, follow the root `AGENTS.md`; substantial changes use the worktree-local self-harness command above. Keep Go's test cache enabled. Use `-count=1` or `--fresh-tests` only for explicit measurement or nondeterminism investigation.

Do not run `go install ./cmd/scenery` unless the human explicitly asks. Multiple worktrees share the installed binary; self-harness builds a worktree-local binary.
