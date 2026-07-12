# scenery Agent Guide

## Working in an edition-2027 mixed app

When `scenery.scn` exists, read it together with `scenery.migration.scn` and each installed package's `scenery.package.scn`. Use `scenery migrate status -o json` to see active frontend ownership and receipt-bound operational gates, then query canonical resources with `scenery list|get|explain ... -o json`. Treat `operational_ready=false` as a real missing drain, fence, cursor, consumer, alias, or other cutover proof; native source ownership alone does not make a migration ready. Do not infer ownership from Go directives or file order.

For native contract edits, run `scenery fmt`, `scenery check -o json`, `scenery compile --view expanded -o json`, and `scenery generate --check`. Edit `.scn` source rather than generated `scenerycontract`, `internal/scenerygen`, or TypeScript files. Keep each TypeScript `output_root` beneath a declared `workspace.managed_generated_roots` entry; the top-level generator commits every Go and TypeScript family as one transaction. Local module sources must stay inside the non-symlink workspace tree. Use `scenery diff --semantic` for compatibility review and `scenery graph` or `scenery agent serve` for dependency/context discovery; do not recover semantic facts through source-text search when the graph exposes them.

Branch on edition diagnostic codes, never message text. Inspect the complete catalog with `scenery schema scenery.diagnostics.2027.v1 -o json` or agent `schema.get`; request one `SCNxxxx` code for its stable identity, severity, fields, and documentation. Internal diagnostics expose only a sanitized message and opaque `report_token`. Source-map IDs are opaque and collision-resistant; resolve them through `source_map`, and interpret positions as zero-based Unicode-scalar columns plus UTF-8 byte offsets.

Compilation deliberately leaves `implementation_revision` null. Use `scenery build --target <name>` when exact implementation identity matters; the build hashes the declared target's complete Go input graph and records the resolved Go distribution/compiler and any host-CGO native tools in `.scenery/build/vnext/<target>.json`. Do not infer implementation identity from source globs or the ambient shell. A fixed non-host CGO target is unsupported until it can declare a content-addressed native toolchain.

No-input operations use exact `std.type.unit`, represented as `{}` / `scenery.Unit` / TypeScript `Unit`. Native CLI bindings run as their declared `scenery <command...>` path and derive help, completion, typed input, output, and exit status from the contract; use lower-kebab command/flag names and do not reuse built-in Scenery commands. Context-mapped fields are runtime-trusted and must never be accepted from caller flags or arguments. For local fixture data, select the same environment used by deployment with `scenery db seed --env <environment>`; Scenery projects only matching typed fixtures into deterministic PostgreSQL seed statements.

For an edition-2027 terminal path tail, require
`scenery.http-path-tail/v1` and `scenery.runtime-http-path-tail/v1`, author only
`/{name...}` as the final segment, and add one matching `path_tail "name"`
mapping to `string`, `relative_path`, or `optional(relative_path)`. It captures
zero or more complete segments without slash normalization: `/drive` is the
empty tail, while `/drive/` and empty/traversal/encoded-separator segments do
not match or fail as `transport.invalid_request`. Generated TypeScript clients
accept the semantic value and encode each segment independently. Migration
candidate generation can lower an eligible terminal legacy `/*path` route
without `SCN5401`; raw handlers and unsupported wildcard/body/response facets
remain independently diagnosed. `SCN5405` marks the required advisory
slash/selection/decoding comparison and does not authorize activation by
itself.

For generated Go constructor configuration, declare typed package inputs and reference them from lower-snake `service.config` fields. The config key is the generated field identity while the referenced input supplies type, phase, constraints, and sensitivity, so both `model_path = var.model_path` and the explicit alias `model_path = var.roof_model_path` are valid. Explain provenance for each generated config-schema field points to that exact package input and declaration/attribute range. Untyped values, invalid phases, and plaintext sensitive values fail compilation.

A service is the minimum bridge activation unit: all routes, lifecycle keys, durable/schedule identities, schema/event ownership, and generated-client projections transfer together. The bridge reads the exact normalized `legacy_config` while it exists, canonical `./...` package roots, declared namespaces and Go targets, and the explicit default legacy gateway. Omit `legacy_config` only after removing the shared config; remaining bounded legacy TypeScript clients are then generated from structured canonical resources in the compiled migration snapshot, so compile/generate/change/migration planning do not rediscover the removed file or infer auth from source symbol names. Each candidate is validated as the current active graph with only that service owner replaced, preserving cross-service dependencies and detecting global collisions. Shape discovery remains advisory until behavioral fixtures pass. `migrate compare` reports static contract equality separately from behavioral and operational completeness, and the service status aggregates the weakest construct evidence. Use it before activation; an advisory behavior match requires `risk_advisory_migration_evidence` approval, while non-stateless cutovers also require content-addressed `--evidence class=reference`. Cutover classes include resources present in either candidate, so a legacy stateful identity removed from native source still requires evidence. Retain the activation receipt for a rollback plan. Retirement closes the legacy candidate and rollback ownership; a committed `native_service` remains ready after a clean clone without machine-local activation receipts, but compilation rejects undeclared legacy models, pages, and references to the package-init builders `durable.NewTask` and `cron.NewJob` hidden in its Go packages. Non-registering durable/cron APIs remain allowed. The service implementation owns one native or bridge lifecycle while each operation independently keeps or removes `legacy_go_v0`; use status `lifecycle_adapter`, `remaining_operation_bridge_count`, and `adapter_retirement_ready` to distinguish those transitions. A native lifecycle with remaining legacy handlers is valid only when the constructor result pointer is statically assignable to every legacy endpoint receiver; `scenery check` rejects incompatible receivers before startup. Explicit `legacy_go_v0` handler adapters remain bridge-visible until retired. Declare durable work, schedules, and other runtime identities in `.scn` before retiring their legacy builders. Preserve an existing durable task namespace with `external_name`; when its persisted input ABI changes, increment `revision` and prove active jobs were drained or migrated. `migrate finish` is an app-wide transaction and also needs evidence for `v0_cli_consumers`, `legacy_generated_client_consumers` when applicable, and every stateful class reported by status. Existing legacy-only apps continue using the stable `--json` workflow.

Semantic changes and deployments use plan/apply rather than direct writes: create a plan against exact base revisions, optionally bind each request operation to `expected_kind` and `expected_schema_revision`, inspect the normalized output operations (which always include those resolved fields plus `view: "source"`), rename receipts, semantic/runtime consequences, and required approvals, then apply the exact issued plan with the expected revisions and same caller. Planning retains the canonical plan under `.scenery/plans/issued/`; apply checks an exact match before trusting expiry, approvals, operations, source edits, or provider actions, so recomputing a public content hash cannot alter an issued plan. Equivalent contextual/tagged scalars share one operation digest. Rename evidence is revision-bound and digest-checked, covers nested-module references inside exports, lists/objects, module inputs, and `scenery.migration.scn`; renaming a module instance derives evidence for every descendant address, and all receipts persist after apply. Later comparisons load matching app-local receipts or accept `scenery diff --semantic ... --rename-receipts <plan-or-receipt.json>` / agent `rename_receipts`. Rename a shared package declaration explicitly rather than targeting one of several module-instance addresses. A plan expires, is single-use, and is invalid after source, generated-artifact, provider, capability, operational-receipt, or approval binding changes.

Before constructing `resource.create`, read agent capabilities and require the target kind in `resource_create_kinds`, then fetch that kind with `schema.get`. Its recursive `fields` describe source attributes versus blocks, labels/cardinality/order, label policies, types and accepted references, expression/revision phases, defaults/constraints, sensitivity, and patchability. Supply labeled child blocks as objects containing `name`; wire labels such as `header "x-request-id"`, externally fixed query names, cookies, and multipart part names follow their advertised domain pattern rather than semantic lower-snake rules. Unadvertised kinds fail with `capability_unavailable`; do not flatten child blocks into object attributes or guess a destination file. Declarative `extension`/generic `resource` syntax is known but reported as `unsupported_profile` until `scenery.declarative-extensions/v1` exists.

When a plan reports `required_approvals`, obtain a detached token from the project's approval service and pass its file with repeatable `--approval-token`. For migration transitions, retain the planning result with `--out <plan>` and apply that exact file with `scenery migrate apply <plan>`; do not rerun the transition command because a fresh expiry creates a different plan ID. The token is bound to the exact plan ID, caller, sorted risk scopes, and expiry. Scenery verifies `ed25519:<key-id>:<base64>` against raw Ed25519 public keys in the uncommitted `.scenery/approval-trust.json`; see `docs/local-contract.md` and the `scenery.approval-token.v1` / `scenery.approval-trust.v1` schemas. Never ask an agent to invent a signature or place a private signing key in the app workspace.

This guide is for AI agents using scenery or changing scenery. It explains how to combine repo-local instructions, the installable skill, CLI JSON, scenery capabilities, and app-local instructions.

For exact command grammar and schemas, use `docs/local-contract.md`. For app recipes, use `docs/app-development-cookbook.md`. For scenery repo edits, `AGENTS.md` is the first file to read.

## Source Of Truth Order

Use this order when instructions overlap:

1. Current implementation and tests.
2. Machine-readable CLI output: `scenery inspect ... --json`, `scenery check --json`, `scenery logs --jsonl`, scoped observability query commands, and `scenery harness ... --json`.
3. JSON schemas in `docs/schemas/`.
4. `docs/local-contract.md`.
5. This guide.
6. `SKILL.md`.
7. README and completed ExecPlans.

If old prose disagrees with current JSON or tests, fix the drift. Do not add legacy aliases unless an active plan explicitly requires compatibility.

## Agent Fast Path

Inside a Scenery app:

```sh
scenery doctor --json
scenery check --json
scenery inspect app --json
scenery inspect routes --json
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery system toolchain verify --json
```

During local debugging:

```sh
scenery up
scenery inspect observability --json
scenery logs --jsonl --limit 200
scenery logs query --json --since 15m --query 'error OR panic'
scenery traces list --json --since 15m --slowest
scenery metrics list --json --since 1h
scenery metrics query --json --since 15m --step 5s --promql 'scenery_request_duration_seconds'
```

Before finishing app work:

```sh
scenery check --json
go test ./...
scenery harness --json --write
scenery validate quick --json --write
```

For an edition-2027 app, add:

```sh
scenery fmt --check -o json
scenery check -o json
scenery compile --view expanded -o json
scenery migrate status -o json   # mixed apps only
scenery generate --check -o json
```

Before finishing scenery repo work:

```sh
go test ./...
go test ./cmd/scenery
scenery harness self --summary --write
```

Self-harness always executes test bodies fresh with `-test.count=1` across the
complete `./...` graph. It reuses content-addressed linked test binaries, not
test results. `--fresh-tests` retains the explicit fresh timing-lane label;
cached and fresh lanes both have a five-second advisory budget and use the
locally measured package parallelism of three. Release retains its 30-second
enforced budget. Package/test warnings require isolated confirmation.

Do not run `go install ./cmd/scenery` during agent validation unless a human
explicitly asks. Multiple worktrees share the installed `scenery` binary; the
self-harness builds `.scenery/harness/bin/scenery` inside the current worktree for
freshness checks.

When a command cannot be run, report the exact command and environmental reason.

## Working In The scenery Repo

Read:

```text
AGENTS.md
docs/local-contract.md
docs/agent-guide.md
docs/plans/active.md
docs/tech-debt.md
```

Use `scenery inspect docs --json` when the binary is available. It reports indexed docs, missing docs, stale docs, plan paths, discovered `AGENTS.md` scopes, and Child Agent Index drift.

Keep these layers synchronized when behavior changes:

- CLI grammar and JSON contracts: `docs/local-contract.md`, schemas, tests.
- Agent workflows: `docs/agent-guide.md`, `AGENTS.md`, `SKILL.md`.
- Human overview: `README.md`.
- App recipes: `docs/app-development-cookbook.md`.
- Environment variables: `docs/environment.md` for humans and `docs/environment.registry.json` for the self-harness contract.
- Indexed docs metadata: `docs/knowledge.json`.

## Working In A Scenery App

Use app-local `AGENTS.md` first, then this skill/guide. In target apps, read the root `AGENTS.md` and every child `AGENTS.md` on the path to files you expect to touch before editing non-trivial changes.

A target app should not copy all scenery docs. It should define only app-specific rules:

````md
# <app> Agent Instructions

Use the scenery skill for shared scenery behavior.

## App Roots
- scenery app root: `<path>`
- scenery app config: `<path>/.scenery.json` (or `<path>/.config.json`)
- frontend root: `<path>`
- generated client: `<path>`

## Local Loop
```sh
scenery up --detach
scenery logs --follow
```

## Validation
```sh
scenery check --json
go test ./...
scenery harness --json --write
<frontend-test-command>
```

## App Invariants
- product/domain rule 1
- product/domain rule 2

## Environment
Required names only, no values:
- DatabaseURL
- <APP_ENV_NAME>
````

That small file gives agents what the reusable scenery skill cannot know: where the app lives, how frontends are wired, what generated files are committed, which validations matter, and which product invariants must be preserved.

## Is The Skill Enough For Client Apps?

No. The skill is necessary shared context, but it is intentionally generic.

Use three layers in client apps such as `github.com/pbrazdil/onlv`:

1. **Installable scenery skill** for the scenery app model, CLI, validation, and generated client workflow.
2. **App-local `AGENTS.md`** for app root, frontend roots, generated output paths, required environment names, app-required build flags, test commands, UI conventions, product invariants, and deployment assumptions.
3. **Machine-readable scenery commands** for the current app shape: `scenery inspect ... --json`.

This avoids duplicating stale runtime documentation into every client app while still giving agents the local context they need.

When an app needs Go build tags or other app-owned build-time flags, prefer app config `build.go_flags` such as `["-tags=roofmapnet_native"]` over asking every agent to export `GOFLAGS` before `scenery up`, `scenery check`, or `scenery test`.

For Google API integrations such as Gmail, use standard auth's Google connection endpoints and `auth.GoogleAccessToken` / `auth.GoogleAccessTokenForUser`; client apps should not store Google refresh tokens themselves.

## CLI Surfaces For Agents

Prefer JSON when output will feed another tool or decision.

| Intent | Command |
| --- | --- |
| Check local environment readiness | `scenery doctor --json` |
| Validate app model | `scenery check --json` |
| Inspect app/root/config | `scenery inspect app --json` |
| Inspect routes/endpoints/services | `scenery inspect routes --json`, `scenery inspect endpoints --json`, `scenery inspect services --json` |
| Inspect static model/view IR | `scenery inspect models --json`, `scenery inspect views --json` |
| Inspect build/cache paths | `scenery inspect build --json`, `scenery inspect paths --json` |
| Inspect generator graph | `scenery inspect generators --json` |
| Inspect storage capability | `scenery inspect storage --json` |
| Inspect docs knowledge base | `scenery inspect docs --json` |
| Inspect CLI command manifest | `scenery help --json` |
| Upgrade local Scenery install | `scenery upgrade --json` |
| Inspect public deploy readiness | `scenery deploy status --json` |
| Inspect managed local tools | `scenery system toolchain list --json`, `scenery system toolchain verify --json` |
| Install managed local tools | `scenery system toolchain sync --json` or `scenery system toolchain sync --tool <name> --json` |
| Inspect local HTTPS edge | `scenery system edge status --json` |
| Run app validation snapshot | `scenery harness --json --write` |
| Run app quality gate | `scenery validate quick --json --write`, `scenery validate changed --json --write`, or `scenery validate full --json --write` |
| Inspect app validation gates | `scenery inspect validation --json`, `scenery validate graph full --json` |
| Run repo validation snapshot | `scenery harness self --summary --write` |
| Follow logs | `scenery logs --jsonl --limit 200` |
| Query logs | `scenery logs query --json --query 'error OR panic'` |
| Inspect observability | `scenery inspect observability --json` |
| Inspect traces/metrics | `scenery traces list --json`, `scenery metrics list --json` |
| Query metrics | `scenery metrics query --json --promql 'scenery_request_duration_seconds'` |
| Generate TypeScript client | `scenery generate client --lang typescript --output <path>` |
| Run configured generation | `scenery generate --dry-run --json`, then `scenery generate` |
| Generate desired model schema | `scenery generate data --dry-run --json` |
| Check generated schema drift | `scenery db diff --generated --json` |
| Inspect storage status | `scenery storage status --json` |
| Inspect storage Web UI route | `scenery storage webui --json` |
| Manage configured storage objects | `scenery storage ls|stat|put|get|rm ... --json` |
| Dry-run storage cell cleanup | `scenery storage cleanup --json` |
| Apply configured DB lifecycle | `scenery db apply --json` |
| Apply service seed data | `scenery db seed --json` |
| Setup local DB lifecycle | `scenery db setup --json` |
| Inspect service databases | `scenery db list --json` |
| Open a service database shell | `scenery db shell` |
| Inspect managed Postgres server | `scenery db server status --json` |
| Human dev runtime status | `scenery ps` |
| Machine dev runtime status | `scenery ps --json` |
| Run repo-local task | `scenery task list`, `scenery task run <name>` |
| Run app-local code task | `scenery task list --json`, `scenery task run <domain>:<name> -- [args...]` |

Generated model CRUD endpoints are beta. They appear in `scenery inspect endpoints --json`
and `scenery inspect routes --json` with `"generated": true`; generated stores
use the app database selected by the configured app database URL env, defaulting
to `DatabaseURL`, or Scenery's managed database env.
Generated CRUD endpoints default to `auth` for every action; the beta DSL has no
implicit public read or public mutation surface.
Generated CRUD route bases are service-scoped as `/<service>/<table>` so `model.Table(...)`
remains a database-table decision rather than a public route shortcut, and generated
routes fail `scenery check` when they collide with reserved route prefixes
(`/runtime`, `/__scenery`, `/api`) or handwritten/generated routes.
Use `model.ExistingTable(schema, table)` when an entity reads an existing physical
table: inspect models exposes `source.kind`, `source.schema`, `source.table`,
and `source.qualified_table`; generated schema/seed output skips that entity;
and the first slice only allows generated list/get actions.
Typed `model.Seed(...)` rows generate `.scenery/gen/db/<service>/seed.sql` and
are consumed by `scenery db seed`. Configured frontends with static collection
pages receive beta generated packages under `.scenery/gen/web/<frontend>/` with
runtime adapter factories and route registration helpers for app-owned
data/TanStack/layout-kit wiring.
Tenant-shaped generated CRUD uses the convention `TenantID`/`tenant_id` field:
generated endpoints are auth-only, generated SQL is scoped to the active standard-auth
tenant, and create/patch payload types do not expose `tenant_id`. Tenant fields
may be `string`, a named string type, or `github.com/google/uuid.UUID`; other
tenant field types fail parse/check with an explicit diagnostic.

When local dev fails because the host may be missing Go, disk space, memory, Docker engine readiness, or optional tools, run `scenery doctor --json` first. Local storage needs no managed toolchain artifact, so there is no storage-specific doctor check; the standard disk/memory checks cover it. Stay on scenery command surfaces for ordinary app work. Use `scenery help --json` for machine-readable command discovery, `scenery help all` for the grouped human command reference, and `scenery ps --json` when dev runtime status will feed another tool. Use `scenery upgrade --json` to update a prebuilt local Scenery binary from a verified release archive; the upgraded binary then syncs managed toolchain entries already present locally, while `--toolchain all` pulls every frozen tool/image from the upgraded manifest. Public deploy is beta and operator-owned: app config claims one `deploy.domain`, `scenery deploy setup` configures public 80/443 with sudo, `scenery deploy enable` records the app root in the machine registry, and `scenery deploy status --json` is the readiness source for DNS, router, power, firewall, helper, and cert state; Cloudflare-proxied DNS is accepted when the origin record points at the reported public IP. Inspect managed dnsmasq, Caddy, Victoria, or Postgres details only when intentionally debugging the substrate. Shared substrate failures are visible in `scenery ps --json` under `substrates`, including exit metadata and stdout/stderr log paths; dead registered runtime children such as managed frontend processes appear as session `degraded` status with `status_reason`. App processes receive `DATABASE_URL`, per-service `<SERVICE>_DATABASE_URL` values, and `SCENERY_DATABASE_JSON` for the one app database and its service schemas. Managed Postgres worktree isolation comes from per-worktree database names on the shared server; use `scenery db server status --json` only when debugging that server. Do not install global binaries as a hidden fix; use `scenery system edge dns install` for wildcard local DNS, `scenery system edge install`, `scenery deploy setup`, `scenery system toolchain sync --json` for managed app-root tools, or document the configured external service.

Use non-JSON output only for human inspection.

## Runtime Command Choice

- Use `scenery up` to run the app root's one live dev runtime and expose capabilities for local development, debugging, agents, dashboard, logs, traces, metrics, managed dev services, and frontend routing. Default local dev routing is path mode: discover the runtime base URL from `scenery ps --json` or the session route manifest, then use `/api/`, `/consolenext/`, frontend paths, and `/runtime/` under that base URL. Paths ignored by `.gitignore` or app config `watch.ignore` are outside the watcher/rebuild surface; `watch.ignore` is Scenery-only and does not affect Git tracking. Use a Git worktree for another live code copy.
- During a watcher rebuild restart, the runtime drains in-flight streaming raw responses (SSE/long-poll) by canceling their request contexts so they end with a clean terminator, and the agent router answers requests for a restarting backend with `503` plus `Retry-After: 1` instead of `502`; clients should treat that as a brief retryable window.
- Managed Vite/Astro frontend dev servers are runtime children; if one exits unexpectedly, the dev supervisor restarts it on a new hidden loopback port and updates the agent route backend.
- Use `scenery up --detach` when the local agent should keep that dev runtime running in the background. By default it waits up to two minutes and returns only when the runtime is actually ready (session `running`, API backend accepting, frontends registered and accepting), so agents can call the API immediately after it returns; pass `--wait registered` for the 30-second fast registration path.
- Use `dev.routing.mode = "host"` plus `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, and `scenery system edge trust` when the browser needs trusted wildcard local HTTPS on `127.0.0.1:443`; dnsmasq owns wildcard local DNS, the privileged helper owns that port, forwards raw TCP to user-owned Caddy, and the edge syncs managed dnsmasq/Caddy as needed. Default path mode needs none of that.
- Use `scenery logs --follow` to follow the current app root's detached or agent-backed runtime.
- Use `scenery down` to stop the current app root's dev runtime; add `--db`, `--state`, or `--all` only when destructive cleanup is intended.
- Use `scenery worker` for worker-role execution of durable tasks and cron.
- For a standalone `scenery worker`, database services require an explicit app-level `DATABASE_URL`; the managed shared Postgres server is a `scenery up` dev substrate only.
- Use `scenery build` for a deployable binary artifact.
- Use `scenery generate` for configured file-producing generators. `scenery generate sqlc` is generated-source work only; it must not apply schema or seed data. `scenery generate data --dry-run --json` writes desired static-model Atlas HCL under `.scenery/gen/db/<service>/schema.hcl`, seed SQL under `.scenery/gen/db/<service>/seed.sql`, and beta generated frontend packages under `.scenery/gen/web/<frontend>/`, including page projection records and default page/route exports, without mutating databases. Generated model DB artifacts use the app-owned `<service>` schema and schema-qualified tables consistently across HCL, seed SQL, CRUD SQL, and entity source metadata; generated Atlas resource labels are also schema-qualified so app-owned schemas can coexist with handwritten multi-schema HCL. Entities declared with `model.ExistingTable(schema, table)` consume an app-owned existing schema/table and still generate read-only frontend and list/get code against the explicit qualified table, but they do not emit generated schema or seed artifacts. Generated list endpoints are bounded by default (`limit=100`, maximum `limit=500`, non-negative `offset`); generated create/patch payloads accept response field names such as `CreatedAt` as well as DB-column JSON names such as `created_at`; malformed `time.Time` timestamps fail JSON decoding. Use `scenery db diff --generated --json` to compare generated schema with app-owned `SERVICE/db/schema.hcl`.
- In handwritten services that use sqlc against the default app database, call `scenery.sh/db.Get(ctx)` during service initialization and pass the returned `*sql.DB` to the generated `Queries` constructor.
- Use `scenery inspect storage --json` and `scenery storage status|webui|ls|stat|put|get|rm|cleanup --json` for Scenery-owned storage capability work. Scenery-launched app processes, workers, setup commands, and app-local code tasks receive the storage capability env consumed by `scenery.sh/storage`, so app code should call `storage.Default(ctx)` or `storage.Named(ctx, name)`. In headless runtimes, that env must be an explicit operator-provided config with stores of `kind: "local"` (absolute `root`) or `kind: "proxy"` (`proxy_socket`); it fails closed when missing. In agent-backed dev sessions the app-facing proxy store serves the local backend from the shared storage-cell object directories. Browser code can use generated TypeScript `client.storage` helpers or the reserved `/__scenery/storage/<store>/...` routes for configured `auth` stores; `private` stores stay internal-only on the external HTTP surface and are available only to app/runtime helpers or Scenery's private route table. Tenant-scoped stores require standard-auth tenant context or explicit `storage.WithTenantID(ctx, tenantID)`. `PutOptions.ContentType` and `PutOptions.Metadata` are returned by `Head`, `Get`, and `List`, and browser/proxy routes carry metadata through `X-Scenery-Storage-Meta-*` headers. `inspect storage`/`storage status` report the storage-cell path and per-store object counts/bytes; `scenery storage cleanup --json` reports the cell and deletes it only with `--yes`. Treat proxy sockets and object roots as substrate details unless you are intentionally debugging the storage runtime. For offsite durability, replicate the storage-cell object directories (objects plus `__scenery/metadata/` sidecars) to S3 with `rclone`/`restic`.
- Use `scenery db apply` to mutate schema/app database setup only. Use `scenery db seed` to apply service-local initial data only; changed previously-applied seeds and destructive seed SQL fail closed with path/line diagnostics. Use `scenery db setup` for the one-command local setup path: apply then seed. `scenery up` runs that setup lifecycle before app startup when DB setup inputs exist, and skips it on ordinary rebuilds until `database.apply` config or seed file hashes change.
- Use `scenery db list --json` and `scenery db shell` for configured Postgres service schemas. Use `scenery db server status --json` only when debugging the shared managed Postgres server. Worktree isolation is automatic through per-worktree databases. `scenery db reset <service>` drops and recreates only that service schema; full-database save/restore uses `scenery db snapshot create|restore`. The default `scenery harness self --json --write` path includes a Docker-gated Postgres probe for one app database, service schemas, durable state, auth bootstrap, worktree isolation, and service-schema reset; use `--quick` for the smaller self-harness mode.
- Use `scenery task list`, `scenery task inspect <target>`, and `scenery task run <target>` for configured repo tasks and app-local code tasks. Configured tasks use plain names; code tasks use `<domain>:<name>`, and task arguments must appear after `--`.
- Use `scenery task run <name>` only for repo-local workflows that are not core scenery lifecycle commands.
- Use `scenery validate` for app-owned quality gates defined in app config. `scenery harness` remains the framework-owned app-model proof; `scenery validate quick|changed|full --json --write` runs app-specific tasks/profiles and writes `.scenery/harness/validation/latest.json`.

## Generated And Cache Artifacts

Agents should use command JSON as the integration surface:

```text
scenery inspect app --json
scenery inspect routes --json
scenery inspect services --json
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery inspect build --json
scenery inspect storage --json
scenery harness --json
scenery validate quick --json
scenery harness self --summary
```

Generated repo-local files may exist after inspect/build/harness commands produce them:

- `.scenery/gen/models.json` and `.scenery/gen/views.json` cache the static model/page IR consumed by `scenery inspect models|views --json`. View inspect records include each collection page's projection as IR: source row type, projection record type, projected fields, static column display hints, static filters, and static sorts. `.scenery/gen/db/<service>/schema.hcl` and `.scenery/gen/db/<service>/seed.sql` are disposable generated data artifacts. `.scenery/gen/web/<frontend>/` is the beta hidden generated package for static model/view frontends; app frontends should consume it through a local TypeScript alias such as `@scenery/generated` and provide `@scenery/layout-kit` plus declared slot components. The generated package exports typed storage rows, page projection records in `projections.ts`, entity source definitions, collection descriptors with static filter/sort/display metadata, runtime adapter factories, default page components, route factories, and `registerGeneratedRoutes` helpers through its barrel; app code still owns the production router, row data source, TanStack DB instance, and layout-kit implementation. Adoption is: declare the entity/page in Go, run `scenery generate data --dry-run --json`, configure the alias, import the generated page or route from the alias, mount it, then run the host typecheck/render or build command.

```text
<app-root>/.scenery/
  gen/
    app.json
    routes.json
    services.json
    endpoints.json
    manifest.json
  build/latest.json
  harness/latest.json
  harness/validation/latest.json
```

Treat these files as internal cache or local snapshot artifacts. Do not read `.scenery/gen/*` directly unless debugging scenery generation.

Do not edit generated artifacts by hand. Regenerate them with scenery commands.

## TypeScript Client Integration

Use generated TypeScript clients when frontends or client apps need a typed route-aware API surface.

Recommended workflow:

```sh
scenery inspect endpoints --json
scenery generate client --lang typescript --output <frontend-or-package-path>/scenery-client.ts
```

Client apps should commit generated clients only if that is their established workflow. If committed, app-local `AGENTS.md` must state the output path and require regeneration after endpoint changes.

Generated TypeScript `WithMeta` methods expose response headers, status, and the raw `Response` alongside decoded data.

Edition-2027 clients additionally reconstruct outcome values split across response bodies, headers, and cookies. Same-status outcomes are decoded against every distinct typed mapping and exactly one must match; compile-time disjointness uses observable media and structural wire shape, not nominal type or destination names. Canonical set ordering applies to JSON, query, form, and header encodings, and declared multipart parts enforce their names, kinds, accepted media, filename policy, multiplicity, and limits. Optional absent metadata remains absent. Fetch cannot preserve repeated request-header field lines, so TypeScript targets reject repeated list/set request headers; use comma encoding only for codecs that cannot contain unescaped commas. Repeated response headers require a Fetch runtime exposing `Headers.getAll(name)` and response cookies require `Headers.getSetCookie()`; otherwise the client reports `unsupported_runtime` instead of accepting a collapsed value. Declared transport/admission/dispatch failures are typed outcomes, while undeclared system failures throw. Generated clients do not retry implicitly.

Generated clients are the application-code integration surface. Agents should use CLI JSON and dashboard APIs for inspection and debugging.

## Environment

- List required environment names in docs; never include values.
- Do not add new scenery-owned production env vars unless the user explicitly asks for one or an active ExecPlan records the exception. Prefer app config, CLI flags, or checked-in manifests, and update `docs/environment.registry.json` when env is truly required.
- Process environment wins over local files.
- Local startup expects app-root `.env` for `scenery up`, local `scenery task run`, and local `scenery worker`.
- `.env.local` is optional and overrides `.env` only when the parent process did not already define a key.
- With `--env production`, `scenery worker` can use process environment without a `.env` file; operator-run generated binaries likewise use process environment directly.
- Secret-bearing files are not copied into build workspaces.

## Debugging Playbooks

Build or parse failure:

```sh
scenery check --json
scenery inspect app --json
scenery inspect paths --json
```

Route or endpoint mismatch:

```sh
scenery inspect routes --json
scenery inspect endpoints --json
```

Auth failure:

```sh
scenery inspect endpoints --json
scenery logs --jsonl --limit 200
```

Slow or failing request:

```sh
scenery inspect observability --json
scenery logs query --json --since 15m --query 'error OR panic'
scenery traces list --json --since 15m --slowest
scenery metrics list --json --since 1h
scenery metrics query --json --since 15m --step 5s --promql 'scenery_request_duration_seconds'
```

Dashboard `devdash.json` and its `app-model/.../sha256/*.json` sidecar blobs are
internal cache artifacts for app/session status, saved requests, onboarding, and
small process diagnostics. Trace summaries, trace events, and report log events
live in Victoria instead of `devdash.json`. Use CLI JSON or dashboard APIs
instead of reading those files directly. When the local agent is active, the
agent dashboard process owns writes to the global dashboard store; dev runtime
supervisors send authenticated control-plane mutations to that backend.
Symphony task-board data is stored separately in the managed `scenery_symphony` Postgres database
and should also be accessed through dashboard APIs rather than opened directly
outside substrate debugging. The dashboard WebSocket is same-origin, but it is not
the trust path for enabling auto mode: `symphony/workflow/update` cannot change a
non-auto workflow to `auto`; use `scenery symphony auto --on --app-root <path>`
locally, and `--off` to return to manual mode. If a Symphony workflow is set to
`auto`, the dashboard server requires saved workflow markdown or app-root
`WORKFLOW.md`, may prepare `Todo` tasks by creating or resetting isolated Git
worktrees, and runs one Codex app-server turn over stdio. `agent.max_attempts`
defaults to `3` and gates retries; `agent.max_turns` defaults to `20` but is
currently parsed for a future multi-turn loop rather than used as a retry count.
`agent.turn_timeout_ms` defaults to `3600000`; `agent.stall_timeout_ms` defaults to
`300000`. Active runs heartbeat leases; expired active leases become terminal
`stalled` runs and release tasks still in `Todo` or `In Progress` back to `Todo`.
Codex turn timeouts become terminal `timed_out` runs and route tasks to `Rework`;
Codex no-notification stalls become terminal `stalled` runs and route tasks to
`Rework`. Use the read-only `symphony/run/detail` RPC for workspace, changed-file,
diff, summary, and lifecycle evidence. Manual process-starting `symphony/run/*`
RPCs are not part of the public dashboard API.

Generated client mismatch:

```sh
scenery inspect endpoints --json
scenery generate client --lang typescript --output <expected-output>
```

Dashboard UI change:

```sh
cd apps/consolenext
bun run lint
bun run typecheck
bun run build
cd ../..
scenery harness ui --json --write
```

The `ui/` tree is the reusable `@scenery` component registry, not a runnable dashboard. Registry changes use `bun run typecheck` and `bun run test` from `ui/`.

## Keeping Agent Docs Fresh

When adding or changing an agent-facing behavior, include one of these updates in the same PR:

- Update `SKILL.md` when an app agent needs to know the behavior.
- Update `AGENTS.md` when a Scenery-repo agent needs to obey the behavior.
- Update this guide when the behavior affects client-app integration or cross-repo agent workflows.
- Update `docs/local-contract.md` when the behavior affects CLI grammar, JSON schemas, artifact paths, or stability status.
- Update `docs/knowledge.json` when adding or materially changing indexed docs.

Prefer deleting stale duplicated details over adding another copy. Link to the contract where exact grammar matters.
