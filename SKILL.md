---
name: scenery
description: Use when building, running, debugging, inspecting, validating, or generating clients for current scenery applications. Scenery is a Go-native runtime and CLI whose singular application model is declared in scenery.scn and package-local scenery.package.scn files.
---

# scenery

Scenery runs one supervised local application runtime and exposes safe capabilities for inspection and action. Applications declare a canonical current graph in `scenery.scn` plus package-local `scenery.package.scn` files. Go packages implement generated native contracts; comments and package initialization do not register application resources.

This skill is shared runtime knowledge, not a replacement for app-local instructions. Read the target repository's root `AGENTS.md` and every child `AGENTS.md` on the path to files you will touch. Client apps should record only their app root, frontend roots, generated output paths, required environment names, validation commands, and product invariants locally.

Read next when needed:

- `docs/agent-guide.md` for agent workflow and generated-artifact rules.
- `docs/local-contract.md` for exact CLI grammar, JSON schemas, and artifact paths.
- `docs/app-development-cookbook.md` for native app recipes.
- `docs/ui-agent-contract.md` before UI work.

## Agent Fast Path

```sh
scenery doctor -o json
scenery fmt --check -o json
scenery check -o json
scenery compile --view expanded -o json
scenery inspect app -o json
scenery inspect routes -o json
scenery inspect services -o json
scenery inspect endpoints -o json
scenery inspect durable -o json
scenery inspect storage -o json
scenery logs -o jsonl --limit 200
scenery harness -o json --write
```

Prefer machine-readable output for decisions. `-o json` selects the singular `scenery.cli` envelope; command-specific results live under `data`. `-o jsonl` emits `scenery.cli.event` envelopes for streaming commands. Verify exact schema/spec revisions and producer identity, and branch on stable `SCNxxxx` diagnostics rather than message text. Resolve opaque source IDs through the returned source map.

Run `scenery doctor -o json` before deep troubleshooting when host readiness is uncertain. For scenery repository changes, use `scenery inspect docs -o json` before editing current contracts and `scenery harness self --summary --write` after substantial work.

## Mental Model

- `.scenery.json` marks the app root.
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

- root workspace/application/toolchain/target/gateway/module declarations;
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

For semantic creation, read agent `resource_create_kinds` and `schema.get` first. Unadvertised kinds are intentionally unavailable. For terminal HTTP path tails, use final `{name...}` syntax and declare one matching typed `path_tail` mapping; path tails are part of the current HTTP contract and require no extra source selector. Do not substitute router globs or pre-encoded fragments.

## Public Go Capabilities

- `scenery.sh` for runtime metadata and contract wire helpers.
- `scenery.sh/auth` for request auth and standard-auth/Google connection helpers.
- `scenery.sh/errs` for coded errors.
- `scenery.sh/durable` for non-registering durable steps and signals; ownership is declared in `.scn`.
- `scenery.sh/db` for service-scoped Postgres pools.
- `scenery.sh/datasource` and `scenery.sh/object` for typed constructor capabilities.
- `scenery.sh/storage` for app storage.

Standard auth can be enabled through app config. Its tenant tables are framework-owned under the app database's `scenery` schema. When Google OAuth is enabled, app code can use `auth.GoogleAccessToken` or `auth.GoogleAccessTokenForUser`; clients should treat `google_reauth_required` as a reconnect prompt.

## Local Development and Debugging

```sh
scenery up
scenery up --detach
scenery ps -o json
scenery logs --follow
scenery console
scenery traces list -o json --since 15m --slowest
scenery metrics list -o json --since 1h
scenery down
```

Default local routing gives each live app root one localhost base URL. Discover it and every routed capability through `scenery ps -o json`; do not guess hidden ports or substrate paths. Treat Caddy, dnsmasq, and Victoria as substrate unless the task explicitly diagnoses them. Use managed toolchain commands instead of relying on ambient `PATH` binaries.

```sh
scenery system toolchain verify -o json
scenery system edge status -o json
scenery deploy status -o json
```

## Storage and Databases

App config declares local storage cells and stores. App code uses `scenery.sh/storage`; it should not inspect proxy sockets or object directories. Tenant-scoped private calls require standard-auth context or `storage.WithTenantID`. Inspect and operate through:

```sh
scenery inspect storage -o json
scenery storage status -o json
scenery storage ls <store> -o json
```

An explicit app `DATABASE_URL` is external. Otherwise `scenery up` manages one Postgres database per app root/worktree and service-scoped schemas. Use `scenery db apply` for schema mutation, `scenery db seed` for initial data, and `scenery db setup` for both. Do not make file generation apply database state.

```sh
scenery db list -o json
scenery db seed --env development --dry-run -o json
scenery db setup -o json
```

## Generated TypeScript Clients

Declare each `typescript_client` target in `scenery.scn`, including its gateway set and managed `output_root`:

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
```

Generated clients implement the exact declared HTTP mappings and typed outcomes. They do not infer routes or authentication from Go names. Regenerate after any reachable binding, type, codec, or auth contract changes.

## Tasks and Workers

Use `scenery task` for app-local code tasks. Targets use `<domain>:<name>` and may run even when the application graph is temporarily invalid.

```sh
scenery task list -o json
scenery task inspect <target> -o json
scenery task run <domain>:<name> -- [args...]
scenery worker --app-root <path> --env <name>
```

Single-file Go code tasks live under a domain `tasks` directory and use `//go:build ignore`; that build constraint is not an application declaration.

## UI Work

Follow `apps/console/AGENTS.md` for dashboard work. For reusable components, follow `docs/ui-agent-contract.md`, use the `@scenery registry`, and install through commands such as `bun run shadcn:add @scenery/button`.

```sh
cd apps/console
bun run lint
bun run typecheck
bun run build
cd ../..
scenery harness ui -o json --write
```

## Command Reference

Use `docs/local-contract.md` for full grammar. Common agent commands:

```text
scenery up [--app-root <path>] [-o jsonl] [--detach]
scenery ps [--app-root <path>] [-o json]
scenery down [--app-root <path>] [-o json]
scenery build [--app-root <path>] [--target <go-target>] [--output <path>] [-o human|json]
scenery fmt --check [--app-root <path>] -o json
scenery check [--app-root <path>] -o json
scenery compile [--app-root <path>] [--view source|effective|expanded] -o json
scenery list|get|explain|graph ... [--app-root <path>] -o json
scenery diff --semantic BASE TARGET [--rename-receipts <path>] -o json
scenery generate [--app-root <path>] [--target go|contracts|typescript_client.<name>] [--check] -o json
scenery changes plan|apply ... -o json
scenery deploy plan|apply ... -o json
scenery inspect app|routes|services|endpoints|build|paths|durable|storage|observability -o json
scenery logs [--app-root <path>] [-o jsonl] [--limit <n>]
scenery traces list -o json [--app-root <path>]
scenery metrics list -o json [--app-root <path>]
scenery task list|inspect|run ...
scenery db list|path|shell|apply|seed|setup|reset|drop ...
scenery test [--app-root <path>] [go test flags/packages...]
scenery harness [--app-root <path>] -o json --write
```

## Validation Before Finishing

For app changes:

```sh
scenery check -o json
scenery generate --check -o json
go test ./...
scenery harness -o json --write
```

For scenery repository changes:

```sh
go test ./...
go vet ./...
scenery harness self --summary --write
```

Keep Go's test result cache enabled for ordinary, focused, and substantial
final validation. Use `-count=1` or `--fresh-tests` only for explicit fresh
measurement or nondeterminism investigation.

Do not run `go install ./cmd/scenery` unless the human explicitly asks. Multiple worktrees share the installed binary; self-harness builds a worktree-local binary.
