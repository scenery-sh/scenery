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
- a required root `scenery.scn`;
- package-local `scenery.package.scn` files installed through root module blocks;
- Go implementations of generated native service contracts;
- generated outputs only beneath declared `workspace.managed_generated_roots`.

The `.scn` graph is the singular source of application semantics. Go comments and package initialization do not declare services, operations, routes, auth, middleware, data, durable work, schedules, events, pages, or renderers.

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

## Runtime Command Choice

- Use `scenery up` for the app root's one live development runtime and all safe local capabilities.
- Use `scenery up --detach` when the local agent should retain it; the default wait returns only after every advertised route and one declared frontend asset are reachable.
- `scenery up` reruns against an already-live app root are idempotent instead of failing: human foreground reruns report the existing runtime and attach to its logs (Ctrl+C detaches without stopping it), while `-o jsonl` and `--detach` reruns report and exit `0` (detached JSON sets `already_running: true`).
- Use `scenery ps -o json` to discover the current base URL, route manifest, child health, and substrate state.
- Use `scenery system agent restart` to restart only the control plane/router; registered Postgres and Victoria processes survive.
- Use `scenery doctor -o json` when startup reports an occupied Scenery port; it distinguishes duplicate Scenery owners from foreign listeners, and startup never falls back to an unadvertised router port.
- Use `scenery logs --follow` for the current runtime.
- Use `scenery down` to stop it; add destructive cleanup flags only intentionally.
- Use `scenery worker` for a worker-role runtime serving declared durable executions and schedules.
- Use `scenery build` for a deployable binary.
- Use `scenery generate` only for file generation. It must not apply database state.
- Use `scenery task` for app-local code tasks.
- Use Git worktrees for another live code copy.

Default local routing gives one app root/worktree one localhost base URL with API, dashboard, runtime, and frontend paths. Do not guess hidden ports. Host-mode wildcard HTTPS is opt-in through managed edge/DNS commands.

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

Declare each target in root `scenery.scn`, select exact gateways, and choose `materialization = "source"` for a checked-in SDK or `materialization = "cache"` for `.scenery/gen/typescript/<name>`. Source output must remain beneath a managed root. Generated clients derive only from reachable canonical resources and exact binding codecs; they do not infer routes or auth from Go symbols.

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --target typescript_client.public_api --check -o json
```

Regenerate after changes to reachable types, bindings, codec mappings, authentication, authorization, or gateway behavior. Keep app imports pointed at the declared output root.

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

Read root `AGENTS.md` plus every applicable child instruction file. Before non-trivial changes, run `scenery inspect docs -o json` and read current contracts and active plans. Use an ExecPlan for complex features, migrations, or substantial refactors.

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

## Keeping Agent Docs Fresh

When behavior changes, update the current owning layers together: root/child `AGENTS.md`, `SKILL.md`, this guide, `docs/local-contract.md`, app cookbook, schemas, and `docs/knowledge.json` as applicable. Historical ExecPlans remain historical; add a short current-contract pointer rather than rewriting their original decisions.
