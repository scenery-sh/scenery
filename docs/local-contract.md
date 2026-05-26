# onlava Local Contract

This document freezes the local developer and agent-facing contract for onlava v0.

The goal is to make onlava deterministic and inspectable:
- app shape is explicit
- CLI grammar is explicit
- machine-readable JSON outputs have versioned schemas
- generated and cache artifact locations are named, even where some paths are still reserved for upcoming work

If implementation and this document disagree, treat that as a bug.

## Status

Implemented now. This list describes what the CLI can do today; it is not the
same as the stable v0 support surface.

- `.onlava.json`
- `onlava dev --json`
- `onlava run`
- `onlava worker`
- `onlava version --json`
- `onlava check --json`
- `onlava psql`
- `onlava harness --json`
- `onlava harness self --json`
- `onlava harness ui --json`
- `onlava admin traces clear --json`
- `onlava inspect app --json`
- `onlava inspect routes --json`
- `onlava inspect services --json`
- `onlava inspect endpoints --json`
- `onlava inspect wire --json`
- `onlava inspect build --json`
- `onlava inspect paths --json`
- `onlava inspect temporal --json`
- `onlava inspect traces --json`
- `onlava inspect metrics --json`
- `onlava inspect data --json`
- `onlava inspect docs --json`
- `onlava logs --jsonl`
- `.onlava/gen/app.json`
- `.onlava/gen/routes.json`
- `.onlava/gen/services.json`
- `.onlava/gen/endpoints.json`
- `.onlava/gen/wire/capabilities.json`
- `.onlava/gen/manifest.json`
- `.onlava/build/latest.json`
- `.onlava/harness/latest.json`
- `.onlava/harness/self-latest.json`
- `github.com/pbrazdil/onlava/data` beta dynamic data platform package

Reserved by contract, implementation pending:
- other `onlava admin ... --json` commands beyond `traces clear`
- repo-local runtime and state manifests beyond `.onlava/build/latest.json`, `.onlava/gen/*`, and `.onlava/harness/latest.json`

Stable v0 surface:
- `.onlava.json`
- `onlava run`
- `onlava build`
- `onlava version --json`
- `onlava check --json`
- `onlava inspect app|routes|services|endpoints|wire|build|paths|docs --json`
- `onlava logs --jsonl`
- `onlava test`
- `onlava gen client`
- typed/raw HTTP endpoints
- auth handler
- service struct initialization and shutdown
- private/internal calls
- secrets from process env and local `.env`
- basic runtime logs and trace emission

Dev-only or beta surface:
- `onlava dev`
- `onlava psql`
- `onlava inspect traces|metrics --json`
- `onlava inspect data --json`
- `onlava inspect temporal --json`
- `onlava worker`
- `onlava admin traces clear --json`
- `onlava harness ui --json`
- dashboard and API Explorer
- dashboard Data Explorer
- DB Studio
- MCP server
- local HTTPS/frontend proxy
- trust-store installation
- Victoria sidecars, Grafana, automatic observability binary downloads, and Victoria-backed local observability reads
- Temporal workflow/activity and cron runtime/admin affordances until their lifecycle, retry, scheduling, and clear/delete semantics are frozen
- cron UI
- `github.com/pbrazdil/onlava/data` dynamic data platform, including object/field metadata, record CRUD/query, indexes, saved views, relationships, beta search, and live updates
- `github.com/pbrazdil/onlava/temporal` workflow/activity declarations and worker registration
- migration compatibility for older app shapes

Compatibility posture:
- onlava-native syntax and imports are the stable API.
- Non-onlava directives/imports are not part of the v0 API.

## `.onlava.json`

Schema:
- [onlava.config.v1.schema.json](schemas/onlava.config.v1.schema.json)

Current shape:

```json
{
  "name": "myapp",
  "id": "myapp-dev",
  "proxy": {
    "workspace": "acme",
    "api_host": "api.acme.localhost",
    "console_host": "console.acme.localhost",
    "mcp_host": "mcp.acme.localhost",
    "frontends": {
      "app": {
        "host": "app.acme.localhost",
        "root": "apps/app"
      }
    }
  },
  "auth": {
    "enabled": true,
    "database_url_env": "DatabaseURL",
    "jwt_secret_env": "JWTSecret",
    "refresh_cookie_name": "onlv_refresh",
    "auto_bootstrap_database": true,
    "google_oauth": {
      "enabled": false,
      "client_id_env": "GoogleOAuthClientID",
      "client_secret_env": "GoogleOAuthClientSecret"
    },
    "dev_bootstrap": {
      "enabled": true,
      "default_user_id": "dev-mcp",
      "default_tenant_id": "00000000-0000-0000-0000-000000000001"
    }
  },
  "observability": {
    "logs": {
      "include_endpoints": [],
      "exclude_endpoints": []
    },
    "tracing": {
      "include_endpoints": [],
      "exclude_endpoints": []
    }
  },
  "temporal": {
    "enabled": false,
    "mode": "local",
    "namespace": "default",
    "address_env": "TEMPORAL_ADDRESS",
    "task_queue_prefix": "onlava.myapp",
    "payload_codec": "onlava-json-v1",
    "api_key_env": "TEMPORAL_API_KEY",
    "tls": {
      "enabled": false,
      "server_name_env": "TEMPORAL_TLS_SERVER_NAME",
      "ca_cert_file_env": "TEMPORAL_TLS_CA_CERT_FILE",
      "client_cert_file_env": "TEMPORAL_TLS_CERT_FILE",
      "client_key_file_env": "TEMPORAL_TLS_KEY_FILE"
    },
    "local": {
      "auto_start": false,
      "db_filename": ".onlava/temporal/dev.sqlite"
    }
  }
}
```

Rules:
- `name` or `id` must be non-empty.
- If `name` is empty, onlava falls back to `id`.
- App identity for runtime environment, dashboard routes, local logs, browser harness routes, and local observability is `id` when present, otherwise `name`. `name` remains the display name and source/build package identity.
- `proxy` is optional.
- `auth` is optional. When `auth.enabled` is true, onlava registers the built-in standard auth handler and auth endpoints.
- `observability` is optional.
- `temporal` is optional. When `temporal.enabled` is true, generated app binaries try to connect to Temporal during runtime startup.
- Unknown fields are rejected.
- `proxy.frontends` is a map keyed by frontend name. Each frontend requires `host`; `root` defaults to `apps/<name>`; `upstream` is optional and overrides Vite port discovery.
- Standard auth uses the `github.com/pbrazdil/onlava/auth` top surface and stores DB-backed auth state in PostgreSQL schema `onlava_auth`.
- Standard auth registers `/auth/signup/email`, `/auth/login/email`, `/auth/refresh`, `/auth/logout`, `/auth/me`, organization/invite/impersonation endpoints, Google OAuth raw endpoints, and local `/users/dev-bootstrap`.
- Standard auth endpoints appear in `onlava inspect routes|services|endpoints --json` and in generated TypeScript clients.
- `auth.auto_bootstrap_database` applies the first standard-auth schema bootstrap at runtime. It is useful for local fixtures; production deployments should manage schema changes deliberately.
- `temporal.address_env` defaults to `TEMPORAL_ADDRESS`; when that env var is unset, runtime defaults to `127.0.0.1:7233`.
- `temporal.namespace` defaults to `TEMPORAL_NAMESPACE` when that env var is set, otherwise `default`.
- `temporal.task_queue_prefix` defaults to `onlava.<app-name>` with unsafe task-queue characters normalized to dots.
- `temporal.payload_codec` defaults to `onlava-json-v1` and is validated at runtime. This is the only supported payload profile for onlava-managed Go and external workers in this milestone.
- `temporal.api_key_env` defaults to `TEMPORAL_API_KEY`. When set, the runtime uses Temporal API-key credentials.
- `temporal.tls.enabled` enables TLS without requiring an API key. `temporal.tls.server_name_env`, `ca_cert_file_env`, `client_cert_file_env`, and `client_key_file_env` default to `TEMPORAL_TLS_SERVER_NAME`, `TEMPORAL_TLS_CA_CERT_FILE`, `TEMPORAL_TLS_CERT_FILE`, and `TEMPORAL_TLS_KEY_FILE`. Client certificate and key env vars must be set as a pair for mTLS.
- Temporal worker deployment metadata is runtime-owned: `deployment_name` defaults to the task-queue prefix normalized for Temporal Worker Deployment naming and can be overridden with `ONLAVA_TEMPORAL_DEPLOYMENT_NAME`; `worker_build_id` defaults to `dev` and can be set with `ONLAVA_BUILD_ID`.
- Temporal workers opt into Worker Deployment Versioning. `ONLAVA_TEMPORAL_VERSIONING_BEHAVIOR` accepts `pinned` or `auto_upgrade` and defaults to `pinned`.
- Local onlava-managed worker processes set their `worker_build_id` as the current Temporal Worker Deployment version on startup so schedules and new workflow executions have a versioned routing target. Non-local workers do not self-promote; operators must promote deployment versions explicitly.
- `temporal.local.auto_start` and `temporal.local.db_filename` are local development settings for supervised Temporal dev server work.
- `ONLAVA_TEMPORAL_TASK_QUEUE` overrides the generated Temporal task queue for worker processes. `onlava worker --task-queue <name>` sets it.
- Generated binaries accept `ONLAVA_ROLE=all|api|worker`. `onlava dev` uses the default combined role. `onlava run` uses `api`. `onlava worker` uses `worker`.
- Packages that declare `github.com/pbrazdil/onlava/temporal` workflows or activities with `temporal.NewWorkflow` or `temporal.NewActivity` are imported into the generated main so their declarations register at startup.
- `temporal.ActivityConfig.MaxConcurrency` maps to the Temporal worker's per-task-queue maximum concurrent activity executions. Use a dedicated task queue when different activities need different limits.
- Cron jobs can set `cron.JobConfig.OverlapPolicy`, `CatchupWindow`, `PauseOnFailure`, `ActivityStartToClose`, and `ActivityRetryPolicy`. When Temporal is enabled these map to Temporal Schedule overlap/catchup/pause policy and to the generated cron activity options. Defaults are overlap `skip`, catchup window `1m`, pause-on-failure `false`, and activity start-to-close `1h`.
- Optional multi-language worker manifests live under `.onlava/workers/*.json` and use `onlava.worker.manifest.v1` or `onlava.worker.manifest.v2`. They require `build_id` and `payload_codec: "onlava-json-v1"`. v2 manifests use queue-level registrations with `registration_hash` values so `onlava inspect temporal --json` can reject incompatible workers sharing a Temporal task queue.

## CLI Grammar

Current implemented grammar:

```text
onlava dev [--port <n>] [--listen <addr>] [--app-root <path>] [-v|--verbose] [--json] [--proxy] [--trust]
onlava run [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
onlava temporal deployment set-current --build-id <id> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
onlava temporal deployment ramp --build-id <id> --percentage <0-100> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
onlava temporal deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
onlava version [--json]
onlava build [--app-root <path>] [-o <path>] [--db-studio]
onlava check [--app-root <path>] [--json]
onlava harness [--app-root <path>] [--json] [--write]
onlava harness self [--repo-root <path>] [--json] [--write]
onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
onlava inspect app|routes|services|endpoints|wire|build|paths|temporal|traces|metrics --json [--app-root <path>]
onlava inspect data --json --database-url <postgres-url> [--tenant <key>] [--object <name>]
onlava inspect docs --json [--repo-root <path>]
onlava inspect traces --json [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
onlava inspect metrics --json [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
onlava admin traces clear --json [--app-root <path>]
onlava logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [-f|--follow] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]
```

Implemented beta/dev helper grammar:

```text
onlava psql [--app-root <path>] [psql args...]
```

Inspect rules:
- `onlava inspect` requires a subject.
- `onlava inspect` currently requires `--json`.
- `--app-root` is optional. When omitted, onlava walks upward from the current working directory to find `.onlava.json`.
- Stable inspect subjects for v0 are `app`, `routes`, `services`, `endpoints`, `wire`, `build`, `paths`, and `docs`.
- `temporal`, `traces`, and `metrics` are beta diagnostic subjects. `temporal` reports effective Temporal config and, when enabled, a short connectivity check. `traces` and `metrics` prefer local VictoriaTraces reads when those sidecars are available, and fall back to the onlava dashboard SQLite store. If no local state exists, they return valid JSON with a warning and empty result sets.
- The `onlava.inspect.traces.v1` and `onlava.inspect.metrics.v1` schemas are useful for agents, but their source-selection, retention, rollup, percentile, and clear/delete semantics are not stable v0 API yet.
- `--since` accepts Go duration strings such as `15m`, `1h`, or `24h`.
- `--min-duration-ms` filters root traces by duration in milliseconds.
- `--status` accepts `ok` or `error`.
- `metrics` defaults to `--since 24h` and `--limit 10000` so agents get useful local summaries without scanning unbounded history.
- `docs` inspects the onlava repo knowledge base, not a target onlava app. It accepts `--repo-root` and otherwise walks upward to the `module github.com/pbrazdil/onlava` repo root.

Command split:

- `onlava dev` starts the local development platform: app process, dashboard, MCP endpoint, DB Studio when configured, file watching, and rebuild/restart supervision.
- `onlava dev` also starts local VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana by default when their binaries can be found or downloaded. SQLite dashboard storage remains active for parity and fallback. This is a dev-only beta implementation detail, not a stable production API.
- `onlava dev --proxy` enables the local HTTPS/frontend proxy.
- `onlava dev --proxy --trust` allows local trust-store installation. Without `--trust`, the proxy skips trust installation.
- `onlava run` builds once and starts the app runtime headlessly. It does not start the dashboard, MCP server, local proxy, DB Studio, frontend proxy, or file watcher.
- `onlava run` starts the generated binary with `ONLAVA_ROLE=api`, so it serves HTTP APIs without registering worker-only workflow or activity handlers.
- Cron declarations use Temporal Schedules when Temporal is enabled. `onlava run` reconciles schedules from the API role, while `onlava worker` runs the cron workflow/activity worker on `onlava.<app>.cron.go`. Temporal cron executions derive their onlava request start/idempotency metadata from the workflow scheduled start time.
- `onlava worker` builds once and starts the app runtime in worker-only mode with no public HTTP server. In this beta implementation it runs cron and native Temporal workers; generated binaries use `ONLAVA_ROLE=worker`.
- `onlava worker bindings` validates `.onlava/workers/*.json` manifests and writes language-specific activity starter files. Python manifests produce `onlava_worker.py`; TypeScript/JavaScript manifests produce `onlava_worker.ts`; unknown languages receive a normalized JSON binding file.
- `onlava temporal deployment set-current`, `ramp`, and `drain` are the explicit operator commands for Temporal Worker Deployment routing changes in non-local environments. They use the app's Temporal connection settings, including TLS/API-key env vars.
- `onlava build` produces the deployable binary and remains the preferred deployment artifact path.
- Generated app binaries are headless by default. `onlava build --db-studio` is an explicit opt-in for the DB Studio integration.
- `onlava harness ui --json` is an optional browser-backed dashboard check. It starts a temporary `onlava dev` process unless `--dashboard-url` points at an existing dashboard, visits core dashboard routes, checks stable `data-onlava-ui` markers, captures screenshots, and writes console/network artifacts under `.onlava/harness/ui/`.

Runtime safety:

- `onlava run` and generated binaries do not expose dev/admin endpoints by default.
- Dev/admin endpoints such as `/__onlava/config`, `/platform.Stats`, and `/debug/pprof/*` are enabled only for the development child process launched by `onlava dev` or when `ONLAVA_DEV_ENDPOINTS=1` is set explicitly.
- Runtime CORS reflection is enabled in dev endpoint mode. Outside dev mode, CORS origins must be explicitly allowlisted with `ONLAVA_CORS_ALLOW_ORIGINS`.
- Build workspaces skip local secret and machine artifacts such as `.env`, `.env.*`, `.git`, `.onlava`, `node_modules`, `.DS_Store`, `__MACOSX`, and `coverage`.

Local observability:

- onlava keeps SQLite observability writes active in `onlava dev`.
- When Victoria sidecars are available, onlava also exports OTLP protobuf to:
  - VictoriaMetrics: `/opentelemetry/v1/metrics`
  - VictoriaLogs: `/insert/opentelemetry/v1/logs`
  - VictoriaTraces: `/insert/opentelemetry/v1/traces`
- Dashboard trace reads and `onlava inspect traces|metrics --json` prefer Victoria data and fall back to SQLite data.
- Victoria sidecars are supervised by `onlava dev`, store data under `.onlava/victoria/` by default, and are stopped with the dev supervisor.
- `ONLAVA_DEV_VICTORIA=0` disables Victoria sidecars. `ONLAVA_DEV_VICTORIA_DOWNLOAD=0` disables automatic binary downloads. When enabled, missing Victoria binaries are downloaded into `.onlava/victoria/bin/`.
- Victoria binary names, versions, ports, storage layout, download behavior, and Victoria query semantics are beta. They are documented so local development is debuggable, but they are not part of the stable v0 runtime contract.
- Grafana is supervised by `onlava dev`, binds to loopback, stores generated config, provisioning, downloaded binaries, and plugin state under `.onlava/grafana/`, and is stopped with the dev supervisor when onlava started it.
- Grafana controls are `ONLAVA_DEV_GRAFANA=auto|1|0`, `ONLAVA_DEV_GRAFANA_DOWNLOAD=1|0`, `ONLAVA_GRAFANA_BIN`, `ONLAVA_GRAFANA_VERSION`, `ONLAVA_GRAFANA_PORT`, `ONLAVA_GRAFANA_DIR`, and `ONLAVA_GRAFANA_PLUGINS_PREINSTALL_SYNC`.
- Default Grafana, Grafana plugin, and Victoria sidecar versions are pinned in `internal/devtools/versions.json`; environment variables override those pins for local testing.
- Grafana provisioning uses datasource UIDs `onlava-victoriametrics`, `onlava-victorialogs`, and `onlava-victoriatraces-jaeger`, plus dashboard UIDs `onlava-dev-overview`, `onlava-dev-logs`, and `onlava-dev-endpoint`.
- Missing Grafana does not stop app startup in `auto` mode. `ONLAVA_DEV_GRAFANA=1` makes Grafana startup required.
- `onlava dev` writes local ignore markers under `.onlava/` and the Grafana/Victoria state roots so downloaded binaries, local databases, logs, generated build outputs, and other machine-local state are not accidentally committed by target apps.

Secrets and environment:

- Process environment always wins over values loaded from local files.
- The stable runtime path reads `.env` from the app root for local secret population when a value is not already present in the process environment.
- Local startup requires `.env` to exist in the app root. If `.env` is missing, `onlava dev`, local `onlava run`, and local `onlava worker` fail before serving with a clear error. `.env.local` is optional.
- `onlava dev` passes local file values into the child process before Go package initialization so package-level declarations can read them through `os.Getenv`.
- `onlava dev` loads `.env` first and `.env.local` second. `.env.local` overrides `.env` only for keys that are not already present in the parent process environment.
- Missing declared secrets warn in local development mode.
- `onlava run --env production` can use process environment without a `.env` file, and fails before serving if any declared secret is missing.
- `.env`, `.env.*`, and secret-bearing local files are not copied into build workspaces.

Beta dynamic data platform:

- Apps may import `github.com/pbrazdil/onlava/data` and open a store with a pgx-compatible pool.
- The data package exposes small query helpers such as `data.EQ`, `data.GTE`, `data.Contains`, `data.And`, `data.Or`, `data.Not`, `data.Asc`, and `data.Desc`.
- The first slice stores metadata and outbox rows in `onlava_data` and physical dynamic record tables in `onlava_data_records`.
- Objects and scalar/composite fields are metadata-defined and backed by real PostgreSQL tables and columns.
- User-managed select and multi-select fields use `text` and `text[]` plus metadata options, not PostgreSQL enum types.
- Apps may call `store.CreateIndex(ctx, actor, objectName, data.CreateIndexRequest{...})` and `store.ListIndexes(ctx, actor, objectName, data.ListIndexesRequest{...})` for metadata-backed PostgreSQL indexes. The first index surface supports btree scalar indexes, compound btree indexes, and explicit GIN indexes for multi-select and JSON fields.
- Relation fields can target another object through `data.RelationSettings`. `many_to_one` relations create a real UUID column plus PostgreSQL foreign key. `many_to_many` relations create a physical join table; record-level many-to-many mutation helpers are not stable yet.
- Apps may create, update, list, delete, and query saved views with `store.CreateView`, `store.UpdateView`, `store.ListViews`, `store.DeleteView`, and `store.QueryView`. The first view surface stores table-style columns, filter, sort, limit, visibility, owner ID, and layout metadata.
- Apps may use `data.StandardAuthPermissions` to scope data access to the active standard-auth `tenant_id`; the auth tenant ID maps directly to `TenantKey`.
- Apps may export and import portable tenant bundles with `store.ExportTenant` and `store.ImportTenant`. The bundle schema is `onlava.data.export.v1`; imports recreate metadata through existing mutation paths, create new record IDs, and return `record_id_map`.
- Public data methods wrap failures in `*data.Error` where possible. Use `data.CodeOf(err)` for coarse handling of `object_not_found`, `field_not_found`, `invalid_filter`, `permission_denied`, `migration_failed`, `schema_drift`, and `invalid_cursor`.
- Record queries are compiled from metadata to parameterized SQL; user input must not become SQL identifiers.
- Record queries can filter, sort, and select one-hop `many_to_one` relation paths such as `company.name`; deeper paths and many-to-many path queries remain future work.
- Record queries use keyset cursor pagination when `query.cursor` is set. `RecordPage.NextCursor` is a base64url-encoded opaque cursor tied to the object, schema version, and effective sort shape; callers must reuse the same sort shape when fetching the next page. Cursor pagination currently rejects nullable and relation sort fields because null-aware keyset semantics are not stable yet.
- Record mutations write outbox events in the same transaction.
- Live updates use SSE over ordinary raw onlava endpoints plus the PostgreSQL outbox sequence for reconnect/replay.
- Apps may call `store.EnableOutboxTriggers(ctx, actor, tenantKey, objectName)` to enable per-object trigger-backed outbox rows for direct SQL or DB Studio changes.
- Explicit onlava record mutations still write precise outbox events themselves; trigger-backed outbox skips those transactions to avoid duplicate events.
- Trigger-backed direct SQL events use logical field names in `before`, `after`, `diff`, and `changed_fields` where field metadata exists. Actor IDs come from transaction-local `onlava.actor_id` when set, otherwise they are empty.
- `onlava inspect data --json --database-url <postgres-url>` reports data tenants, objects, fields, relation metadata, indexes, saved views, migration state, and outbox state without dumping user records.
- `onlava inspect data --json --database-url <postgres-url> --tenant <tenant-key> --object <object-name>` filters the same infrastructure view to one data tenant/object.
- The dashboard Data Explorer is a dev-only view over the same data platform concepts. It can inspect data tenants/objects, query selected object records through the objectstore query path, and tail outbox events for debugging local apps.

Standard auth:

- Apps may enable the built-in standard auth module from `.onlava.json` instead of writing a `//onlava:authhandler`.
- Auth-protected app code can use `auth.UserID()`, `auth.Data()`, or `auth.CurrentAuthData()` from `github.com/pbrazdil/onlava/auth`.
- Access tokens are HMAC JWTs with required expiration and `tenant_id` claims.
- Refresh sessions are stored in PostgreSQL and rotate by hashing refresh tokens. The refresh cookie name defaults to `onlv_refresh` for ONLV compatibility and is configurable.
- Email delivery is a pluggable `auth.EmailSender`; the default sender is a no-op.
- `/users/dev-bootstrap` is local-only and can mint a development token without opening PostgreSQL.
- DB-backed auth endpoints require a database URL from `auth.database_url_env`, `DATABASE_URL`, or `ONLAVA_AUTH_DATABASE_URL`.

Implemented `dev --json` rules:

```text
onlava dev --json
```

- output is JSONL
- each line conforms to `onlava.run.event.v1`
- human-readable console output is suppressed in this mode
- child stdout/stderr are emitted as structured `process.output` events instead of raw terminal writes

Implemented `check --json` rules:

```text
onlava check --json
```

- output is a single JSON document
- output conforms to `onlava.check.result.v1`
- success returns `ok: true` and an empty `diagnostics` array
- failure returns `ok: false` and structured diagnostics
- diagnostics may include `stage`, `file`, `line`, `column`, `severity`, `message`, and `suggested_action`

Implemented `harness --json` rules:

```text
onlava harness --json
onlava harness --json --write
```

- output is a single JSON document
- output conforms to `onlava.harness.result.v1`
- it composes `onlava check --json` and the stable `onlava inspect ... --json` surfaces
- success returns `ok: true`
- failure returns `ok: false`, per-step errors, diagnostics, and `next_actions`
- `--write` persists the same result to `.onlava/harness/latest.json`

Implemented `harness self --json` rules:

```text
onlava harness self --json
onlava harness self --json --write
```

- output is a single JSON document
- output conforms to `onlava.harness.self.v1`
- it validates the onlava repo itself instead of a target app
- it runs docs knowledge validation, `onlava inspect docs --json`, architecture checks, UI static architecture checks, Go package tests for the CLI, dev dashboard store, and runtime, dashboard UI typecheck/build, DB Studio UI typecheck/build, UI freshness checks, `go install ./cmd/onlava`, and installed binary freshness checks
- architecture checks fail on unapproved direct dependencies, forbidden framework imports, CLI package boundary violations, missing generated/vendored ignore markers, and non-generated source files over 2500 lines
- architecture checks warn on non-generated source files over 1000 lines, cgo imports, `.DS_Store` artifacts, and compatibility imports outside known migration paths
- UI static architecture checks fail on raw shadcn install scripts, non-`@onlava` registries, unsafe registry item source/target declarations, legacy `components/ui` imports, direct vendor shadcn imports from screens, and direct Radix/styling utility imports outside onlava primitives/layouts/vendor
- UI static architecture checks scan multiline imports, re-exports, dynamic imports, and CommonJS requires for forbidden UI boundary bypasses
- UI static architecture checks warn on long or advanced `className` literals and common expression forms such as `cn(...)`, template literals, and conditional literals outside onlava primitives/layouts/vendor while the dashboard is migrated into the stricter slot-layout model
- `onlava harness ui --json` is not part of the default self-harness path. It needs a local Chrome/Chromium-compatible browser and is intended for explicit dashboard route validation.
- `--write` persists the same result to `.onlava/harness/self-latest.json`

Release gate:

```text
scripts/release-gate.sh
```

- this is the high-signal pre-release gate, not the normal inner-loop developer check
- it runs full Go tests, race tests, `golangci-lint`, dashboard UI and DB Studio typecheck/build, installed self-harness, clean source-copy install, fixture smoke, optional external app smoke, public-router safety checks, production secrets checks, and artifact hygiene checks
- `ONLAVA_RELEASE_GATE_EXTERNAL_APP_ROOT` may point at a read-only onlava app for the optional external app smoke
- `ONLAVA_RELEASE_GATE_LOG_DIR` may override the log directory; otherwise logs are written under `.onlava/release-gate/`
- artifact hygiene is intentionally strict and fails on local release artifacts such as `.DS_Store` and `__MACOSX`

Implemented `logs --jsonl` rules:

```text
onlava logs --jsonl
onlava logs --json
```

- `--json` is an alias for `--jsonl`
- output is JSONL
- each line conforms to `onlava.logs.event.v1`
- one JSON object is emitted per stored process-output chunk
- human-readable raw output remains the default when neither flag is used

Reserved grammar:

```text
onlava admin <subcommand> --json ...
```

Implemented `admin --json` rules:
- current supported command is `traces clear`
- output conforms to `onlava.admin.result.v1`
- admin commands are dev/admin beta for v0; their existence does not make cron, trace clearing, or queue deletion semantics stable

Any additional admin subcommands are reserved contract surfaces and should produce versioned JSON when implemented.

## Artifact Locations

### Current implemented locations

Use `onlava inspect paths --json` as the source of truth.

Today onlava uses:
- app config: `<app-root>/.onlava.json`
- cache root:
  - `$ONLAVA_DEV_CACHE_DIR`, if set
  - otherwise OS user cache + `/onlava`
- build workspace: `<cache-root>/build/<sanitized-app-name>-<hash>`
- built app binary: `<workspace>/onlava-app`
- build state: `<workspace>/.onlava-build-state.json`

### Stable repo-local locations

Implemented now:

```text
<app-root>/.onlava/
  gen/
    app.json
    routes.json
    services.json
    endpoints.json
    wire/
      capabilities.json
    manifest.json
  build/
    latest.json
  harness/
    latest.json
    self-latest.json
```

Reserved for upcoming work:

```text
<app-root>/.onlava/
  state/
  logs/
```

Rules:
- `app.json`, `routes.json`, `services.json`, and `endpoints.json` mirror the current `onlava inspect ... --json` outputs for those subjects
- `wire/capabilities.json` mirrors `onlava inspect wire --json` and the runtime `GET /_wire/capabilities` response
- `manifest.json` ties the generated inspect artifacts to schema versions, stable artifact paths, and deterministic content hashes
- `build/latest.json` is the stable repo-local pointer to the latest prepared or compiled build workspace
- `harness/latest.json` is the stable repo-local pointer to the latest agent validation run
- `harness/self-latest.json` is the stable repo-local pointer to the latest onlava repo validation run
- agents can use either `onlava inspect ... --json` or the corresponding `.onlava/gen/*.json` files
- future implementation should conform to these locations instead of inventing a different layout

## JSON Schemas

Implemented now:
- [onlava.inspect.app.v1.schema.json](schemas/onlava.inspect.app.v1.schema.json)
- [onlava.inspect.routes.v1.schema.json](schemas/onlava.inspect.routes.v1.schema.json)
- [onlava.inspect.services.v1.schema.json](schemas/onlava.inspect.services.v1.schema.json)
- [onlava.inspect.endpoints.v1.schema.json](schemas/onlava.inspect.endpoints.v1.schema.json)
- [onlava.inspect.traces.v1.schema.json](schemas/onlava.inspect.traces.v1.schema.json)
- [onlava.inspect.metrics.v1.schema.json](schemas/onlava.inspect.metrics.v1.schema.json)
- [onlava.data.export.v1.schema.json](schemas/onlava.data.export.v1.schema.json)
- [onlava.inspect.data.v1.schema.json](schemas/onlava.inspect.data.v1.schema.json)
- [onlava.inspect.docs.v1.schema.json](schemas/onlava.inspect.docs.v1.schema.json)
- [onlava.docs.index.v1.schema.json](schemas/onlava.docs.index.v1.schema.json)
- [onlava.wire.capabilities.v1.schema.json](schemas/onlava.wire.capabilities.v1.schema.json)
- [onlava.inspect.build.v1.schema.json](schemas/onlava.inspect.build.v1.schema.json)
- [onlava.inspect.paths.v1.schema.json](schemas/onlava.inspect.paths.v1.schema.json)
- [onlava.inspect.temporal.v1.schema.json](schemas/onlava.inspect.temporal.v1.schema.json)
- [onlava.worker.manifest.v1.schema.json](schemas/onlava.worker.manifest.v1.schema.json)
- [onlava.worker.manifest.v2.schema.json](schemas/onlava.worker.manifest.v2.schema.json)
- [onlava.gen.manifest.v1.schema.json](schemas/onlava.gen.manifest.v1.schema.json)
- [onlava.build.latest.v1.schema.json](schemas/onlava.build.latest.v1.schema.json)
- [onlava.run.event.v1.schema.json](schemas/onlava.run.event.v1.schema.json)
- [onlava.check.result.v1.schema.json](schemas/onlava.check.result.v1.schema.json)
- [onlava.harness.result.v1.schema.json](schemas/onlava.harness.result.v1.schema.json)
- [onlava.harness.self.v1.schema.json](schemas/onlava.harness.self.v1.schema.json)
- [onlava.logs.event.v1.schema.json](schemas/onlava.logs.event.v1.schema.json)
- [onlava.admin.result.v1.schema.json](schemas/onlava.admin.result.v1.schema.json)
- [onlava.version.v1.schema.json](schemas/onlava.version.v1.schema.json)

Reserved now:
- future command-specific admin schemas if `onlava.admin.result.v1` becomes too generic

Schema rules:
- top-level schema field is `schema_version`
- schema names are versioned strings like `onlava.inspect.app.v1`
- additive fields are allowed in future versions only by introducing a new schema version when needed
- consumers should match on `schema_version`, not on command name alone

## Examples

### `onlava inspect app --json`

```json
{
  "schema_version": "onlava.inspect.app.v1",
  "app": {
    "name": "billing",
    "id": "billing-dev",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.onlava.json",
    "module_path": "example.com/billing"
  },
  "config": {
    "name": "billing",
    "id": "billing-dev",
    "proxy": {
      "workspace": "billing",
      "api_host": "api.billing.localhost",
      "console_host": "console.billing.localhost",
      "mcp_host": "mcp.billing.localhost",
      "frontends": {
        "web": {
          "host": "web.billing.localhost",
          "root": "apps/web"
        }
      }
    },
    "observability": {
      "logs": {
        "include_endpoints": [],
        "exclude_endpoints": []
      },
      "tracing": {
        "include_endpoints": [],
        "exclude_endpoints": []
      }
    }
  },
  "counts": {
    "packages": 3,
    "services": 2,
    "endpoints": 7,
    "middleware": 1,
    "auth_handler": 1,
    "runtime_declarations": 3
  },
  "services": [
    "auth",
    "users"
  ],
  "auth_handler": {
    "service": "auth",
    "name": "AuthHandler"
  }
}
```

### `onlava inspect build --json`

```json
{
  "schema_version": "onlava.inspect.build.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.onlava.json"
  },
  "build": {
    "workspace_dir": "/cache/onlava/build/billing-abcdef0123456789",
    "binary_path": "/cache/onlava/build/billing-abcdef0123456789/onlava-app",
    "workspace_exists": true,
    "binary_exists": true,
    "build_state_path": "/cache/onlava/build/billing-abcdef0123456789/.onlava-build-state.json",
    "build_state_exists": true,
    "build_state_version": "2",
    "dependency_fingerprint": "abc123",
    "graph_fingerprint": "def456",
    "metadata_present": true,
    "api_encoding_present": true,
    "source_file_count": 24,
    "generated_file_count": 6
  }
}
```

### `onlava inspect endpoints --json`

```json
{
  "schema_version": "onlava.inspect.endpoints.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.onlava.json"
  },
  "endpoints": [
    {
      "id": "users.Get",
      "service": "users",
      "endpoint": "Get",
      "access": "public",
      "raw": false,
      "path": "/users/:id",
      "methods": ["GET"],
      "has_payload": true,
      "wire": {
        "available": true,
        "schema_hash": "abc123",
        "path": "/_wire/users.Get"
      }
    }
  ],
  "wire": {
    "wire_schema_hash": "def456",
    "available": 1,
    "unsupported": 0
  }
}
```

### `onlava inspect wire --json`

`onlava inspect wire --json` returns the same hidden generated-client capability document served at `GET /_wire/capabilities`. It is intended for generated clients and agents that need to know whether the JSON transport or binary transport will be used for each logical endpoint.

### `onlava inspect traces --json`

Beta diagnostic subject. Use this when an agent needs concrete local traces
without scraping the dashboard UI. The JSON shape is versioned, but retention,
backend preference, span reconstruction, and clear semantics may change before
this is promoted to stable v0.

Example:

```text
onlava inspect traces --json --endpoint SyncGet --min-duration-ms 2000 --since 1h --slowest
```

Example output:

```json
{
  "schema_version": "onlava.inspect.traces.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.onlava.json"
  },
  "query": {
    "app_id": "billing",
    "limit": 100,
    "since": "1h0m0s",
    "endpoint": "SyncGet",
    "min_duration_ms": 2000,
    "sort": "duration_desc",
    "available_filters": ["--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
  },
  "traces": [
    {
      "trace_id": "trace-1",
      "span_id": "span-1",
      "kind": "RPC",
      "status": "ok",
      "service": "sync",
      "endpoint": "SyncGet",
      "started_at": "2026-04-27T13:00:00Z",
      "duration_ms": 2310,
      "duration_nanos": 2310000000
    }
  ]
}
```

### `onlava inspect metrics --json`

Beta diagnostic subject. Use this when an agent needs a metrics-style rollup
over locally captured traces and logs. The JSON shape is versioned, but rollup
definitions, percentile calculations, default limits, and Victoria/SQLite source
selection may change before this is promoted to stable v0.

Example:

```text
onlava inspect metrics --json --service sync --since 15m
```

Example output:

```json
{
  "schema_version": "onlava.inspect.metrics.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.onlava.json"
  },
  "query": {
    "app_id": "billing",
    "limit": 10000,
    "since": "15m0s",
    "service": "sync",
    "sort": "started_at_desc",
    "available_filters": ["--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
  },
  "summary": {
    "trace_count": 12,
    "error_count": 1,
    "error_rate": 0.08333333333333333,
    "event_count": 34,
    "log_count": 9,
    "avg_duration_ms": 120.4,
    "min_duration_ms": 3.1,
    "max_duration_ms": 520.7,
    "p50_duration_ms": 88.2,
    "p95_duration_ms": 500.1
  },
  "services": [],
  "endpoints": [],
  "logs": [],
  "meta": {
    "trace_metric_limit": 10000
  }
}
```

### `onlava inspect docs --json`

Use this when an agent needs to understand the repo knowledge base before making changes.

Source files:

- [docs/index.md](index.md)
- [docs/knowledge.json](knowledge.json)
- [docs/plans/active.md](plans/active.md)
- [docs/plans/completed.md](plans/completed.md)
- [docs/tech-debt.md](tech-debt.md)

Example:

```text
onlava inspect docs --json
```

Example output:

```json
{
  "schema_version": "onlava.inspect.docs.v1",
  "repo": {
    "root": "/repo/onlava",
    "module_path": "github.com/pbrazdil/onlava",
    "go_mod_path": "/repo/onlava/go.mod"
  },
  "summary": {
    "document_count": 9,
    "missing_count": 0,
    "review_due_count": 0,
    "stale_count": 0,
    "quality": {
      "A": 4,
      "B": 5
    }
  },
  "documents": [
    {
      "path": "docs/local-contract.md",
      "title": "onlava Local Contract",
      "owner": "onlava runtime",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Frozen local developer and agent-facing contract.",
      "tags": ["contract", "cli", "agents", "schemas"],
      "exists": true,
      "review_due": false,
      "stale": false
    }
  ],
  "plans": {
    "active": {
      "path": "docs/plans/active.md",
      "exists": true
    },
    "completed": {
      "path": "docs/plans/completed.md",
      "exists": true
    }
  },
  "tech_debt": {
    "path": "docs/tech-debt.md",
    "exists": true
  }
}
```
