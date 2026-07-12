---
name: scenery
description: Use when building, running, debugging, inspecting, validating, or generating clients for edition-2027 scenery applications. Scenery is a Go-native runtime and CLI whose singular application model is declared in scenery.scn and package-local scenery.package.scn files.
---

# scenery

Scenery runs one supervised local application runtime and exposes safe capabilities for inspection and action. Applications declare a canonical edition-2027 graph in `scenery.scn` plus package-local `scenery.package.scn` files. Go packages implement generated native contracts; comments and package initialization do not register application resources.

This skill is shared runtime knowledge, not a replacement for app-local instructions. Read the target repository's root `AGENTS.md` and every child `AGENTS.md` on the path to files you will touch. Client apps should record only their app root, frontend roots, generated output paths, required environment names, validation commands, and product invariants locally.

Read next when needed:

- `docs/agent-guide.md` for agent workflow and generated-artifact rules.
- `docs/local-contract.md` for exact CLI grammar, JSON schemas, and artifact paths.
- `docs/app-development-cookbook.md` for native app recipes.
- `docs/ui-agent-contract.md` before UI work.

## Agent Fast Path

```sh
scenery doctor --json
scenery fmt --check -o json
scenery check -o json
scenery compile --view expanded -o json
scenery inspect app --json
scenery inspect routes --json
scenery inspect services --json
scenery inspect endpoints --json
scenery inspect durable --json
scenery inspect storage --json
scenery logs --jsonl --limit 200
scenery harness --json --write
```

Prefer machine-readable output for decisions. `-o json` selects `scenery.cli.v1` for edition commands; `--json` is an independently versioned command-specific protocol and must not be combined with `-o`. Branch on stable `SCNxxxx` diagnostics rather than message text. Resolve opaque source IDs through the returned source map.

Run `scenery doctor --json` before deep troubleshooting when host readiness is uncertain. For scenery repository changes, use `scenery inspect docs --json` before editing current contracts and `scenery harness self --summary --write` after substantial work.

## Mental Model

- `.scenery.json` marks the app root; `.config.json` is accepted only when `.scenery.json` is absent.
- `scenery.scn` is required and installs package-local `scenery.package.scn` modules.
- Source, effective, and expanded graph views are distinct. Effective resolves inputs, defaults, and patches; expanded adds generators. Provenance paths are RFC 6901 pointers into the selected resource spec.
- Workspace, contract, implementation, deployment, and artifact revisions are separate. `scenery compile` does not invent an implementation revision; build supplies an exact target input manifest.
- Services, operations, executions, HTTP/internal/CLI bindings, authentication, authorization, middleware, durable work, schedules, events, data, and UI resources are `.scn` declarations.
- Generated Go contract and application-composition files are outputs, never source of truth.
- `scenery up` starts the app process, rebuild loop, dashboard, API explorer, logs, traces, metrics, managed dev services, and configured frontends for one app root.
- Public and auth HTTP bindings are externally reachable. Internal bindings are called through generated clients so auth, visibility, tracing, delivery, and error semantics remain intact.
- Use Git worktrees for multiple live code copies.

App-required build flags belong in `build.go_flags` in app config. Non-runtime tracked trees that should not trigger rebuilds belong in `watch.ignore`. Do not add ambient environment controls when checked-in config or an explicit flag is sufficient.

## Native Source and Generated Artifacts

Start from the checked-in `testdata/apps/basic` fixture or the minimal example in `README.md`. A native Go service has:

- root language/workspace/application/toolchain/target/gateway/module declarations;
- package metadata with a `go_contract.import_path`;
- a service constructor declaration;
- typed records, an operation, an execution, and one or more bindings;
- a Go constructor and methods using generated `scenerycontract` input/outcome types.

Use this loop:

```sh
scenery fmt --check -o json
scenery compile --view expanded -o json
scenery generate --target go -o json
scenery generate --check -o json
scenery check -o json
go test ./...
```

Keep every generated output beneath `workspace.managed_generated_roots`. Top-level generation plans all selected Go and TypeScript families before writing any of them. Never hand-edit `scenerycontract`, `internal/scenerygen`, generated descriptors, or generated TypeScript.

Use `scenery list|get|explain|graph ... -o json` for graph facts and `scenery diff --semantic` for compatibility. Semantic changes and deployments use immutable revision-bound plan/apply. Apply accepts only the exact app-local issued plan and rejects caller-recomputed approvals, operations, edits, or provider actions.

For semantic creation, read agent `resource_create_kinds` and `schema.get` first. Unadvertised kinds are intentionally unavailable. For terminal HTTP path tails, require both path-tail profiles, use final `{name...}` syntax, and declare one matching typed `path_tail` mapping; do not substitute router globs or pre-encoded fragments.

## Public Go Capabilities

- `scenery.sh` for runtime metadata and contract wire helpers.
- `scenery.sh/auth` for request auth and standard-auth/Google connection helpers.
- `scenery.sh/errs` for coded errors.
- `scenery.sh/middleware` for middleware runtime types.
- `scenery.sh/durable` for non-registering durable steps and signals; ownership is declared in `.scn`.
- `scenery.sh/db` for service-scoped Postgres pools.
- `scenery.sh/datasource` and `scenery.sh/object` for typed constructor capabilities.
- `scenery.sh/storage` for app storage.
- `scenery.sh/et` for tests.

Standard auth can be enabled through app config. Its tenant tables are framework-owned under the app database's `scenery` schema. When Google OAuth is enabled, app code can use `auth.GoogleAccessToken` or `auth.GoogleAccessTokenForUser`; clients should treat `google_reauth_required` as a reconnect prompt.

## Local Development and Debugging

```sh
scenery up
scenery up --detach
scenery ps --json
scenery logs --follow
scenery console
scenery traces list --json --since 15m --slowest
scenery metrics list --json --since 1h
scenery down
```

Default local routing gives each live app root one localhost base URL. Discover it and every routed capability through `scenery ps --json`; do not guess hidden ports or substrate paths. Treat Caddy, dnsmasq, and Victoria as substrate unless the task explicitly diagnoses them. Use managed toolchain commands instead of relying on ambient `PATH` binaries.

```sh
scenery system toolchain verify --json
scenery system edge status --json
scenery deploy status --json
```

## Storage and Databases

App config declares local storage cells and stores. App code uses `scenery.sh/storage`; it should not inspect proxy sockets or object directories. Tenant-scoped private calls require standard-auth context or `storage.WithTenantID`. Inspect and operate through:

```sh
scenery inspect storage --json
scenery storage status --json
scenery storage ls <store> --json
```

An explicit app `DATABASE_URL` is external. Otherwise `scenery up` manages one Postgres database per app root/worktree and service-scoped schemas. Use `scenery db apply` for schema mutation, `scenery db seed` for initial data, and `scenery db setup` for both. Do not make file generation apply database state.

```sh
scenery db list --json
scenery db seed --env development --dry-run --json
scenery db setup --json
```

## Generated TypeScript Clients

Declare each `typescript_client` target in `scenery.scn`, including its gateway set and managed `output_root`:

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
bun test internal/vnext/testdata/typescript_client_conformance.test.ts
```

Generated clients implement the exact declared HTTP mappings and typed outcomes. They do not infer routes or authentication from Go names. Regenerate after any reachable binding, type, codec, or auth contract changes.

## Tasks and Workers

Use `scenery task` for configured repo tasks and app-local code tasks. Code tasks use `<domain>:<name>` and may run even when the application graph is temporarily invalid.

```sh
scenery task list --json
scenery task inspect <target> --json
scenery task run <domain>:<name> -- [args...]
scenery worker --app-root <path> --env <name>
```

Single-file Go code tasks live under a domain `tasks` directory and use `//go:build ignore`; that build constraint is not an application declaration.

## UI Work

Follow `apps/consolenext/AGENTS.md` for dashboard work. For reusable components, follow `docs/ui-agent-contract.md`, use the `@scenery registry`, and install through commands such as `bun run shadcn:add @scenery/button`.

```sh
cd apps/consolenext
bun run lint
bun run typecheck
bun run build
cd ../..
scenery harness ui --json --write
```

## Command Reference

Use `docs/local-contract.md` for full grammar. Common agent commands:

```text
scenery up [--app-root <path>] [--json] [--detach]
scenery ps [--app-root <path>] [--json]
scenery down [--app-root <path>] [--json]
scenery build [--app-root <path>] [--target <go-target>] [-o <path>]
scenery fmt --check [--app-root <path>] -o json
scenery check [--app-root <path>] -o json
scenery compile [--app-root <path>] [--view source|effective|expanded] -o json
scenery list|get|explain|graph ... [--app-root <path>] -o json
scenery diff --semantic BASE TARGET [--rename-receipts <path>] -o json
scenery generate [--app-root <path>] [--target go|contracts|typescript_client.<name>] [--check] -o json
scenery changes plan|apply ... -o json
scenery deploy plan|apply ... -o json
scenery inspect app|routes|services|endpoints|build|paths|durable|storage|observability --json
scenery logs [--app-root <path>] [--jsonl] [--limit <n>]
scenery traces list --json [--app-root <path>]
scenery metrics list --json [--app-root <path>]
scenery task list|inspect|run ...
scenery db list|path|shell|apply|seed|setup|reset|drop ...
scenery test [--app-root <path>] [go test flags/packages...]
scenery harness [--app-root <path>] --json --write
```

## Validation Before Finishing

For app changes:

```sh
scenery check -o json
scenery generate --check -o json
go test ./...
scenery harness --json --write
```

For scenery repository changes:

```sh
go test -count=1 ./...
go vet ./...
scenery harness self --summary --write
```

Do not run `go install ./cmd/scenery` unless the human explicitly asks. Multiple worktrees share the installed binary; self-harness builds a worktree-local binary.
