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
- `onlava serve`
- `onlava worker`
- `onlava version --json`
- `onlava check --json`
- `onlava generate`
- `onlava generate client`
- `onlava generate sqlc`
- `onlava db psql`
- `onlava db sync`
- `onlava db reset`
- `onlava db drop`
- `onlava db snapshot create|restore`
- `onlava task list|run|graph`
- `onlava run list|inspect`
- `onlava run <domain>:<script>`
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
- `onlava inspect generators --json`
- `onlava inspect temporal --json`
- `onlava inspect traces --json`
- `onlava inspect metrics --json`
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

Reserved by contract, implementation pending:
- other `onlava admin ... --json` commands beyond `traces clear`
- repo-local runtime and state manifests beyond `.onlava/build/latest.json`, `.onlava/gen/*`, and `.onlava/harness/latest.json`

Stable v0 surface:
- `.onlava.json`
- `onlava serve`
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
- `onlava db psql`
- `onlava db sync`
- `onlava db reset`
- `onlava db drop`
- `onlava db snapshot create|restore`
- `onlava generate`
- `onlava task list|run|graph`
- `onlava run list|inspect`
- `onlava run <domain>:<script>`
- `onlava psql`
- `onlava inspect traces|metrics --json`
- `onlava inspect generators --json`
- `onlava inspect temporal --json`
- `onlava worker`
- `onlava admin traces clear --json`
- `onlava harness ui --json`
- dashboard and API Explorer
- MCP server
- local HTTPS/frontend proxy
- trust-store installation
- Victoria sidecars, Grafana, automatic observability binary downloads, and Victoria-backed local observability reads
- Temporal workflow/activity and cron runtime/admin affordances until their lifecycle, retry, scheduling, and clear/delete semantics are frozen
- cron UI
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
  "generators": {
    "clients": [
      {
        "id": "web",
        "kind": "typescript-client",
        "target": "myapp-dev",
        "output": "apps/web/src/onlava-client.ts"
      }
    ],
    "sqlc": {
      "provider": "sqlc",
      "config": "sqlc.yaml",
      "dev_url": "docker://postgres/18/dev",
      "schemas": [
        {
          "sqlc_schema": "auth/db/gen/schema.sql",
          "atlas_source": "auth/db/schema.hcl"
        }
      ]
    }
  },
  "database": {
    "apply": {
      "provider": "exec",
      "command": "./scripts/db-safe-apply.sh"
    }
  },
  "tasks": {
    "harness": {
      "steps": ["check", "test:go"]
    },
    "ui-harness": {
      "cwd": "apps/web",
      "run": "bun run ui-harness"
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
    },
    "typescript": {
      "enabled": false,
      "runtime": "bun",
      "auto_start": false
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
- `proxy.frontends` is a map keyed by frontend name. Each frontend requires `host`; `root` defaults to `apps/<name>`; `upstream` is optional but ignored by agent dev unless that frontend also sets `allow_shared_upstream: true`. With an active agent, `onlava dev` prefers to start supported Vite/Astro frontends on hidden loopback ports, inject routed API/Electric URLs into their process environment, register those hidden ports as session backends, and expose `https://<frontend>.<session>.onlava.localhost:<agent-router-port>/` by default. `ONLAVA_FRONTEND_<NAME>_ADDR` still overrides onlava-owned frontend startup for manual debugging.
- `dev.services` is a beta local-development config surface for onlava-owned substrates. Phase 5 accepts `postgres` and `electric` service declarations with `kind`, `version`, `isolation`, `image`, `database`, `route`, and string `env` values. The agent currently owns managed Postgres and Electric for this surface, while unsupported service kinds or isolation modes are rejected instead of silently falling back to target-app port orchestration.
- `dev.setup` is an optional beta list of shell commands that `onlava dev` runs from the app root after managed dev services are prepared and before the app process starts. Setup commands receive the same managed Postgres `DatabaseURL`/`DATABASE_URL` env values as the app child, so target apps can apply local schema to the per-session database.
- `generators.clients` is a beta lifecycle config for generated TypeScript clients. `kind` defaults to `typescript-client`, `lang` defaults to TypeScript, and `output` is required. `onlava generate client` uses these entries when no explicit `--output` is passed.
- `generators.sqlc` is a beta lifecycle config for SQLC generation. `provider` may be empty or `sqlc`; `config` defaults to `sqlc.yaml`; `dev_url` defaults to `docker://postgres/18/dev`. When a SQLC schema path follows `<pkg>/db/gen/schema.sql` and `<pkg>/db/schema.hcl` exists, `onlava generate sqlc` refreshes the generated schema SQL with `atlas schema inspect` before running `sqlc generate`.
- `database.apply` is a beta DB lifecycle escape hatch. Phase 1 supports only `provider: "exec"` with an explicit shell `command`, optional `cwd`, and string `env` overlay. `onlava db sync` runs this provider and then refreshes configured SQLC artifacts. It does not infer or apply migrations by convention.
- `tasks` is a beta thin repo-task layer. Each task can define either `run` or `steps`, plus optional `cwd` and string `env`. `run` uses the platform shell from the app root or task cwd. `steps` currently accepts `task:<name>`, `check`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, and `db:sync`.
- Operational scripts are beta app-local script targets under `<domain>/scripts/`. Targets use `<domain>:<script>`, and both segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`. `onlava run list`, `onlava run inspect`, and `onlava run <domain>:<script> [script args...]` discover and execute them without requiring the app model to parse cleanly.
- `dev.services.postgres` currently defaults to version `18` and `isolation: "database"`. Other isolation modes are rejected until implemented. With an active agent session, onlava creates or reuses a deterministic per-session database, registers Postgres substrate metadata, and injects session-scoped `DatabaseURL`/`DATABASE_URL` even when local env files already contain those keys. The admin cluster comes from `ONLAVA_DEV_POSTGRES_ADMIN_URL`, a reusable agent Postgres substrate, Docker when available for the requested version, or local `initdb`/`postgres` binaries under the agent state directory. Managed local Postgres starts with logical replication settings so `dev.services.electric` can attach. `ONLAVA_DEV_POSTGRES_INITDB` and `ONLAVA_DEV_POSTGRES_BIN` can point at explicit local binaries. Set `ONLAVA_DEV_POSTGRES_EXTERNAL=1` to keep an explicit external `DatabaseURL`/`DATABASE_URL` instead of using the managed session database. Once registered, later sessions and `onlava db ...` commands can reuse the agent-recorded Postgres substrate URL.
- `dev.services.electric` supports explicit upstream routing with `ONLAVA_DEV_ELECTRIC_UPSTREAM`; when set, onlava registers the upstream as a hidden session backend and injects `ELECTRIC_URL`/`ONLAVA_ELECTRIC_URL` using the agent route. Without an explicit upstream, onlava starts a hidden per-session Electric process from `ONLAVA_DEV_ELECTRIC_BIN` or, when `dev.services.electric.image` is set and Docker is available, from that image. Electric receives the managed Postgres session database URL when `dev.services.postgres` is declared, unless `ONLAVA_DEV_POSTGRES_EXTERNAL=1` is set; otherwise it receives explicit `DATABASE_URL`/`DatabaseURL`. onlava also sets a deterministic session-scoped `ELECTRIC_REPLICATION_STREAM_ID` by default so multiple sessions can share one Postgres cluster without colliding on Electric publication or replication-slot names. Configured `dev.services.electric.env` values stay on the Electric process/container and are not injected into the app process; an explicit `ELECTRIC_REPLICATION_STREAM_ID` there overrides the onlava default.
- Standard auth uses the `github.com/pbrazdil/onlava/auth` top surface and stores DB-backed auth state in PostgreSQL schema `onlava_auth`.
- Standard auth registers `/auth/signup/email`, `/auth/login/email`, `/auth/refresh`, `/auth/logout`, `/auth/me`, organization/invite/impersonation endpoints, Google OAuth raw endpoints, and local `/users/dev-bootstrap`.
- Standard auth endpoints appear in `onlava inspect routes|services|endpoints --json` and in generated TypeScript clients.
- `auth.auto_bootstrap_database` applies the first standard-auth schema bootstrap at runtime. It is useful for local fixtures; production deployments should manage schema changes deliberately.
- `temporal.address_env` defaults to `TEMPORAL_ADDRESS`; when that env var is unset, runtime defaults to `127.0.0.1:7233`.
- `temporal.namespace` defaults to `TEMPORAL_NAMESPACE` when that env var is set, otherwise `default`.
- `temporal.task_queue_prefix` defaults to `onlava.<app-name>` with unsafe task-queue characters normalized to dots. `ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX` overrides the effective runtime prefix; `onlava dev` sets it to a session-scoped value when an agent session is active.
- `temporal.payload_codec` defaults to `onlava-json-v1` and is validated at runtime. This is the only supported payload profile for onlava-managed Go and external workers in this milestone.
- `temporal.api_key_env` defaults to `TEMPORAL_API_KEY`. When set, the runtime uses Temporal API-key credentials.
- `temporal.tls.enabled` enables TLS without requiring an API key. `temporal.tls.server_name_env`, `ca_cert_file_env`, `client_cert_file_env`, and `client_key_file_env` default to `TEMPORAL_TLS_SERVER_NAME`, `TEMPORAL_TLS_CA_CERT_FILE`, `TEMPORAL_TLS_CERT_FILE`, and `TEMPORAL_TLS_KEY_FILE`. Client certificate and key env vars must be set as a pair for mTLS.
- Temporal worker deployment metadata is runtime-owned: `deployment_name` defaults to the task-queue prefix normalized for Temporal Worker Deployment naming and can be overridden with `ONLAVA_TEMPORAL_DEPLOYMENT_NAME`; `worker_build_id` defaults to `dev` and can be set with `ONLAVA_BUILD_ID`.
- Temporal workers opt into Worker Deployment Versioning. `ONLAVA_TEMPORAL_VERSIONING_BEHAVIOR` accepts `pinned` or `auto_upgrade` and defaults to `pinned`.
- Temporal workers enable Go SDK host resource reporting by default using Temporal's `contrib/sysinfo` provider, so Worker heartbeats can include CPU and memory usage for Temporal Cloud worker health views. Set `ONLAVA_TEMPORAL_HOST_RESOURCE_REPORTING=0` to disable this provider.
- Local onlava-managed worker processes set their `worker_build_id` as the current Temporal Worker Deployment version on startup so schedules and new workflow executions have a versioned routing target. Non-local workers do not self-promote; operators must promote deployment versions explicitly.
- `temporal.local.auto_start` and `temporal.local.db_filename` are local development settings for supervised Temporal dev server work. With an active agent, the Temporal dev server is registered as a shared agent substrate and its local database state is stored under the agent directory; each dev session also registers a `temporal` route for the shared UI, while app workers receive session-scoped task queue prefixes. Explicit workflow/activity task queues are prefixed in active dev sessions too, so parallel worktrees do not poll or schedule onto each other's queues.
- `ONLAVA_TEMPORAL_TASK_QUEUE` overrides the generated Temporal task queue for worker processes. `onlava worker --task-queue <name>` and `onlava worker typescript --task-queue <name>` set it.
- TypeScript Temporal activity support is activity-only. onlava discovers `*.worker.ts` files, plus ordinary `.ts` files with `//onlava:worker`, and generates `.onlava/generated/temporal/typescript/{onlava.ts,registry.ts,worker.ts,manifest.json,tsconfig.json,package.json}`. Source files import `activity` from `onlava/worker` or `@onlava/temporal`; the generated `tsconfig.json` maps both names to the generated local API. Before launching the generated worker, `onlava dev` and `onlava worker typescript` install the app-root `package.json` dependencies and the generated worker package dependencies with `bun install`, falling back to `npm install` when Bun is unavailable.
- Go workflows declare TypeScript activities with `temporal.NewExternalActivity` using matching input/output type parameters and call them through `temporal.ExecuteActivity`. `onlava check --json` validates matching TypeScript activity names, task queues, and type names before build/runtime.
- `temporal.typescript.enabled`, `runtime`, and `auto_start` configure the TypeScript worker path. `onlava worker typescript` generates and runs the hidden worker directly. When `temporal.typescript.enabled` and `auto_start` are both true, `onlava dev` validates Go-to-TypeScript contracts, regenerates the hidden worker runtime, and supervises the TypeScript worker alongside the Go app. The worker receives the supervised Temporal address/namespace, session-scoped task queue prefix, deployment name, build ID, and agent session identity environment. `runtime` accepts `bun` or `node`; when empty, onlava prefers `bun` and falls back to `node --import tsx`.
- Generated binaries accept `ONLAVA_ROLE=all|api|worker`. `onlava dev` uses the default combined role. `onlava serve` uses `api`. `onlava worker` uses `worker`.
- Packages that declare `github.com/pbrazdil/onlava/temporal` workflows or activities with `temporal.NewWorkflow`, `temporal.NewActivity`, or `temporal.NewExternalActivity` are imported into the generated main so their declarations register at startup.
- `temporal.ActivityConfig.MaxConcurrency` maps to the Temporal worker's per-task-queue maximum concurrent activity executions. Use a dedicated task queue when different activities need different limits.
- Cron jobs can set `cron.JobConfig.OverlapPolicy`, `CatchupWindow`, `PauseOnFailure`, `ActivityStartToClose`, and `ActivityRetryPolicy`. When Temporal is enabled these map to Temporal Schedule overlap/catchup/pause policy and to the generated cron activity options. Defaults are overlap `skip`, catchup window `1m`, pause-on-failure `false`, and activity start-to-close `1h`.
- Optional multi-language worker manifests live under `.onlava/workers/*.json` and use `onlava.worker.manifest.v1` or `onlava.worker.manifest.v2`. They require `build_id` and `payload_codec: "onlava-json-v1"`. v2 manifests use queue-level registrations with `registration_hash` values so `onlava inspect temporal --json` can reject incompatible workers sharing a Temporal task queue.

## CLI Grammar

Current implemented grammar:

```text
onlava dev [--port <n>] [--listen <addr>] [--app-root <path>] [--session <id>|--new-session] [-v|--verbose] [--json] [--proxy] [--trust] [--detach]
onlava attach [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria|sqlite] [--jsonl|--json] [--tui]
onlava console [--app-root <path>] [--session current|<id>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria|sqlite]
onlava agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava status --json [--app-root <path>] [--session <id>] [--watch]
onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all]
onlava prune --older-than <duration> [--app-root <path>] [--json]
onlava serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
onlava worker typescript [--task-queue <name>[,<name>...]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
onlava temporal deployment set-current --build-id <id> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
onlava temporal deployment ramp --build-id <id> --percentage <0-100> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
onlava temporal deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
onlava version [--json]
onlava build [--app-root <path>] [-o <path>]
onlava check [--app-root <path>] [--json]
onlava db psql [--app-root <path>] [psql args...]
onlava db sync [--app-root <path>]
onlava db reset [--app-root <path>]
onlava db drop [--app-root <path>]
onlava db snapshot create|restore <name> [--app-root <path>]
onlava generate [--app-root <path>] [--dry-run] [--json]
onlava generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
onlava generate sqlc [--app-root <path>] [--dry-run] [--json]
onlava task list [--app-root <path>] [--json]
onlava task run <name> [--app-root <path>]
onlava task graph --json [--app-root <path>]
onlava run list [--app-root <path>] [--json]
onlava run inspect <domain>:<script> [--app-root <path>] [--lang go|typescript] [--json]
onlava run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<script> [script args...]
onlava harness [--app-root <path>] [--json] [--write]
onlava harness self [--repo-root <path>] [--json] [--write]
onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|traces|metrics --json [--app-root <path>]
onlava inspect docs --json [--repo-root <path>]
onlava inspect traces --json [--session current|<id>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
onlava inspect metrics --json [--session current|<id>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
onlava admin traces clear --json [--app-root <path>]
onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria|sqlite] [-f|--follow] [--jsonl|--json]
onlava logs compare [--app-root <path>] [--session current|<id>] [--backend-a sqlite|victoria] [--backend-b sqlite|victoria] [--limit <n>] [--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]
```

Implemented beta/dev helper grammar:

```text
onlava psql [--app-root <path>] [psql args...]
```

`onlava db psql` is the PRD-facing spelling. When `dev.services.postgres` is configured and an agent session is active, it connects to the managed session database; otherwise it falls back to the older beta `onlava psql` behavior. `onlava db reset`, `onlava db drop`, and `onlava db snapshot create|restore` are only available for managed session databases. `onlava db sync` is beta and runs only an explicit `database.apply` provider before dependent SQLC generation.

Inspect rules:
- `onlava inspect` requires a subject.
- `onlava inspect` currently requires `--json`.
- `--app-root` is optional. When omitted, onlava walks upward from the current working directory to find `.onlava.json`.
- Stable inspect subjects for v0 are `app`, `routes`, `services`, `endpoints`, `wire`, `build`, `paths`, and `docs`.
- `generators`, `temporal`, `traces`, and `metrics` are beta diagnostic subjects. `generators` reports configured generation graph inputs and outputs. `temporal` reports effective Temporal config and, when enabled, a short connectivity check. `traces` and `metrics` prefer local VictoriaTraces reads when those sidecars are available, and fall back to the onlava dashboard SQLite store. If no local state exists, they return valid JSON with a warning and empty result sets.
- The `onlava.inspect.traces.v1` and `onlava.inspect.metrics.v1` schemas are useful for agents, but their source-selection, retention, rollup, percentile, and clear/delete semantics are not stable v0 API yet.
- `--since` accepts Go duration strings such as `15m`, `1h`, or `24h`.
- `--min-duration-ms` filters root traces by duration in milliseconds.
- `--status` accepts `ok` or `error`.
- `metrics` defaults to `--since 24h` and `--limit 10000` so agents get useful local summaries without scanning unbounded history.
- `docs` inspects the onlava repo knowledge base, not a target onlava app. It accepts `--repo-root` and otherwise walks upward to the `module github.com/pbrazdil/onlava` repo root.

Command split:

- `onlava dev` starts the local development platform: app process, file watching, and rebuild/restart supervision.
- `onlava dev --session <id>` registers the dev process under an explicit session ID. `onlava dev --new-session` creates a fresh session ID for this run, even when the app root and branch already have a deterministic default session. These flags are mutually exclusive.
- `onlava dev --detach` requires the local agent, starts the same dev supervisor in a background child process, waits for that child PID to register as the app root's agent session owner, prints the session URLs plus attach/stop commands, and returns. Detached child stdout/stderr from the supervisor is written under the agent directory; app process output continues to flow through the session-scoped dashboard log store.
- `onlava attach` follows the current agent session's logs by default. It is equivalent to `onlava logs --session current --follow` with the same app-root, limit, stream, source, kind, level, grep, since, backend, and JSONL options, and it does not mutate session state.
- `onlava logs`, plain `onlava attach`, `onlava attach --tui`, and `onlava console` use the same dev-event backend selector. `--backend victoria` and `--backend sqlite` force either side while the migration is being verified; `ONLAVA_LOGS_BACKEND` accepts the same values and applies to the TUI as well.
- `--backend auto` prefers the shared agent VictoriaLogs dev-event stream when the agent has registered one. Non-following plain logs still fall back to SQLite or legacy process output when Victoria returns no rows for an existing local session. Fresh `--follow --backend auto` keeps following the selected backend even when the initial result set is empty, so new Victoria events are not missed.
- `onlava logs compare` reads both selected backends and emits either a short human summary or machine-readable JSON mismatch diagnostics for event counts, IDs, source data, levels, messages, raw output, parsed fields, parse metadata, and timestamps.
- `onlava attach --tui` and `onlava console` open the source-aware terminal console when stdin/stdout are real TTYs. In CI, dumb terminals, or redirected output they fall back to normal log following with the same backend option.
- Structured dev logs carry source identity. Current source ids include `api`, `worker:typescript`, `build`, `supervisor`, `temporal`, `electric`, `grafana`, `victoria`, and `frontend:<name>`.
- `onlava agent restart` stops the currently reachable local agent process, starts a new background agent, waits until the control socket is reachable, and returns. The same `--socket`, `--router-listen`, `--router-tls`, `--trust`, and `--json` options apply to the restarted agent.
- `onlava down` stops and unregisters the selected session but is non-destructive by default. `--db` drops that session's managed Postgres database, `--state` removes that session's `.onlava/sessions/<id>` state root, and `--all` enables both.
- `onlava prune --older-than <duration>` prunes old agent sessions whose recorded owner is gone or mismatched, removes their `.onlava/sessions/<id>` state roots, and deletes matching SQLite `dev_events`/`dev_sources` compatibility-cache rows. It accepts Go durations such as `336h` plus day shorthand such as `14d`. It does not drop managed databases or delete VictoriaLogs storage; use `onlava down --db` or `onlava db drop` for destructive database cleanup.
- When the local agent is active, the agent starts the visible dashboard backend and routes `console.onlava.localhost/s/<session_id>` to it. The Unix-socket control API remains protected by filesystem permissions.
- The agent router serves HTTPS by default, and newly registered sessions receive `https://...onlava.localhost` routes. `onlava agent --router-http` or `ONLAVA_AGENT_ROUTER_TLS=0` explicitly keeps the router on HTTP for local debugging. `onlava agent --router-tls` and `ONLAVA_AGENT_ROUTER_TLS=1` force HTTPS when an explicit setting is needed. `onlava agent --trust` and `ONLAVA_AGENT_TRUST=1` also enable router TLS and attempt to trust the existing onlava local CA. Trust installation failures are logged; the router still starts.
- Agent session manifests always include `dashboard` and `mcp` routes for the global agent-owned dashboard. With the agent dashboard active, the manifest does not need matching per-session `dashboard` or `mcp` backends; direct/per-session dashboard endpoints are kept for agent-disabled, unavailable-agent, or explicit local-proxy fallback paths.
- `onlava dev` also starts local VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana by default when their binaries can be found or downloaded. When the local agent is active, Victoria and Grafana are registered as shared agent substrates and later dev sessions reuse their endpoints. Grafana is also registered as the session `grafana` backend, so manifests expose `https://grafana.<session_id>.onlava.localhost:<agent-router-port>/` by default, or HTTP when the agent router is explicitly started with `--router-http` or `ONLAVA_AGENT_ROUTER_TLS=0`. SQLite dashboard storage is stored under the agent directory when the agent is active and `ONLAVA_DEV_CACHE_DIR` is unset; the store keeps session-addressable app records so multiple worktrees for the same base app can appear in the global dashboard. Agent-disabled fallback keeps the previous user-cache behavior. This is a dev-only beta implementation detail, not a stable production API.
- The local agent home defaults to `~/.onlava` unless `ONLAVA_AGENT_HOME` is set. `ONLAVA_DEV_CACHE_DIR` controls build and dashboard cache locations, not machine-wide agent identity.
- Managed frontend services start on session-private hidden loopback ports. A manual `ONLAVA_FRONTEND_<NAME>_ADDR` override is accepted, but configured frontend upstreams are ignored unless that frontend sets `"allow_shared_upstream": true`.
- `onlava dev --proxy` enables the legacy local HTTPS/frontend proxy. This is a manual debugging escape hatch that binds machine-global proxy ports and is not the recommended path for parallel worktrees.
- `onlava dev --proxy --trust` allows local trust-store installation. Without `--trust`, the proxy skips trust installation.
- `onlava dev --port <n>` and `onlava dev --listen <addr>` force a manual TCP app backend. The default agent path uses a session-private Unix socket and should be preferred for worktree-safe development.
- `onlava serve` builds once and starts the app runtime headlessly. It does not start the dashboard, MCP server, local proxy, frontend proxy, or file watcher.
- `onlava serve` starts the generated binary with `ONLAVA_ROLE=api`, so it serves HTTP APIs without registering worker-only workflow or activity handlers.
- `onlava run list|inspect` and `onlava run <domain>:<script> [script args...]` are the canonical local operational script surface. Script discovery is filesystem-first and does not parse or type-check the onlava app model. Targets use `<domain>:<script>` and map to `<app-root>/<domain>/scripts/<script>...`; the domain is a top-level path segment, not an onlava service name. Both target segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`.
- Onlava script flags must appear before the target, such as `onlava run --env production billing:reconcile --dry-run`. Arguments after the target belong to the script; `--` is accepted but not required. `onlava run` no longer starts the API server; use `onlava serve` for headless API execution.
- Supported script layouts are `<domain>/scripts/<name>.script.go`, `<domain>/scripts/<name>.script.ts`, `<domain>/scripts/<name>/main.go`, and `<domain>/scripts/<name>/index.ts`. Single-file Go scripts must start with `//go:build ignore` so normal app package loading cannot accidentally include them. If multiple candidates match a target, onlava fails unless `--lang go|typescript` selects a single language.
- Scripts execute with cwd set to the app root. Go scripts use `go run`; TypeScript scripts prefer `bun` and fall back to `node --import tsx`. Script processes receive `ONLAVA_APP_ID`, `ONLAVA_APP_ROOT`, and `ONLAVA_ENV`/`ONLAVA_RUNTIME_ENV` when `--env` is set, with `.env` and `.env.local` loaded when present.
- Cron declarations use Temporal Schedules when Temporal is enabled. `onlava serve` reconciles schedules from the API role, while `onlava worker` runs the cron workflow/activity worker on `onlava.<app>.cron.go`. Temporal cron executions derive their onlava request start/idempotency metadata from the workflow scheduled start time.
- `onlava worker` builds once and starts the app runtime in worker-only mode with no public HTTP server. In this beta implementation it runs cron and native Temporal workers; generated binaries use `ONLAVA_ROLE=worker`.
- `onlava worker bindings` validates `.onlava/workers/*.json` manifests and writes language-specific activity starter files. Python manifests produce `onlava_worker.py`; TypeScript/JavaScript manifests produce `onlava_worker.ts`; unknown languages receive a normalized JSON binding file.
- `onlava temporal deployment set-current`, `ramp`, and `drain` are the explicit operator commands for Temporal Worker Deployment routing changes in non-local environments. They use the app's Temporal connection settings, including TLS/API-key env vars.
- `onlava build` produces the deployable binary and remains the preferred deployment artifact path.
- `onlava harness ui --json` is an optional browser-backed dashboard check. It starts a temporary `onlava dev` process unless `--dashboard-url` points at an existing dashboard, visits core dashboard routes, checks stable `data-onlava-ui` markers, captures screenshots, and writes console/network artifacts under `.onlava/harness/ui/`.

Runtime safety:

- `onlava serve` and generated binaries do not expose dev/admin endpoints by default.
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
- Victoria sidecars store data under `.onlava/victoria/` by default when running without the agent. With an active agent, shared Victoria state is stored under the agent directory and registered in the agent substrate registry; the dev supervisor reuses registered endpoints instead of owning per-worktree Victoria processes.
- `ONLAVA_DEV_VICTORIA=0` disables Victoria sidecars. `ONLAVA_DEV_VICTORIA_DOWNLOAD=0` disables automatic binary downloads. When enabled, missing Victoria binaries are downloaded into `.onlava/victoria/bin/`.
- Victoria binary names, versions, ports, storage layout, download behavior, and Victoria query semantics are beta. They are documented so local development is debuggable, but they are not part of the stable v0 runtime contract.
- Grafana binds to loopback and stores generated config, provisioning, downloaded binaries, and plugin state under `.onlava/grafana/` when running without the agent. With an active agent, shared Grafana state is stored under the agent directory and registered in the agent substrate registry; later dev sessions reuse the verified shared Grafana and expose a per-session `grafana.<session>.onlava.localhost` route that points at the shared upstream.
- Grafana controls are `ONLAVA_DEV_GRAFANA=auto|1|0`, `ONLAVA_DEV_GRAFANA_DOWNLOAD=1|0`, `ONLAVA_GRAFANA_BIN`, `ONLAVA_GRAFANA_VERSION`, `ONLAVA_GRAFANA_PORT`, `ONLAVA_GRAFANA_DIR`, `ONLAVA_GRAFANA_PUBLIC_URL`, `ONLAVA_GRAFANA_REUSE_EXTERNAL`, `ONLAVA_GRAFANA_PRESERVE_GF_ENV`, `ONLAVA_GRAFANA_DOWNLOAD_URL`, `ONLAVA_GRAFANA_DOWNLOAD_SHA256`, and `ONLAVA_GRAFANA_PLUGINS_PREINSTALL_SYNC`.
- Default Grafana, Grafana plugin, and Victoria sidecar versions are pinned in `internal/devtools/versions.json`; environment variables override those pins for local testing.
- Grafana provisioning uses datasource UIDs `onlava-victoriametrics`, `onlava-victorialogs`, and `onlava-victoriatraces-jaeger`, plus dashboard UIDs `onlava-dev-overview`, `onlava-dev-logs`, and `onlava-dev-endpoint`.
- Missing Grafana does not stop app startup in `auto` mode. `ONLAVA_DEV_GRAFANA=1` makes Grafana startup required. Grafana is marked usable only after the server, expected datasources, and expected dashboards are verified. External Grafana reuse requires `ONLAVA_GRAFANA_REUSE_EXTERNAL=1`.
- Agent sessions inject `ONLAVA_SESSION_ID`, `ONLAVA_BASE_APP_ID`, `ONLAVA_RUNTIME_APP_ID`, `ONLAVA_APP_ROOT_HASH`, `ONLAVA_BRANCH`, and `ONLAVA_WORKTREE` into the app process. Local development reports carry that identity into stored trace summaries/events and log events.
- The emitted VictoriaMetrics request duration contract is `onlava_request_duration_seconds` with labels `onlava_app`, `onlava_trace_type`, `onlava_is_root`, `onlava_is_error`, `onlava_service`, optional `onlava_session_id`, optional `onlava_app_root_hash`, optional `onlava_branch`, optional `onlava_worktree`, optional `onlava_endpoint`, and optional `onlava_message_id`.
- The emitted VictoriaTraces and VictoriaLogs attribute contract includes `onlava.application_id`, optional `onlava.session_id`, optional `onlava.app_root_hash`, optional `onlava.branch`, and optional `onlava.worktree`.
- `onlava dev` writes local ignore markers under `.onlava/` and the Grafana/Victoria state roots so downloaded binaries, local databases, logs, generated build outputs, and other machine-local state are not accidentally committed by target apps.

Secrets and environment:

- The human env-var reference is [Environment Reference](environment.md). Add new onlava-owned env vars there when adding or changing runtime/dev behavior.
- Process environment always wins over values loaded from local files.
- The stable runtime path reads `.env` from the app root for local secret population when a value is not already present in the process environment.
- Local startup requires `.env` to exist in the app root. If `.env` is missing, `onlava dev`, local `onlava serve`, local `onlava run`, and local `onlava worker` fail before serving or running with a clear error. `.env.local` is optional.
- `onlava dev` passes local file values into the child process before Go package initialization so package-level declarations can read them through `os.Getenv`.
- `onlava dev` loads `.env` first and `.env.local` second. `.env.local` overrides `.env` only for keys that are not already present in the parent process environment.
- Missing declared secrets warn in local development mode.
- `onlava serve --env production` can use process environment without a `.env` file, and fails before serving if any declared secret is missing.
- `.env`, `.env.*`, and secret-bearing local files are not copied into build workspaces.

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
- it runs docs knowledge validation, `onlava inspect docs --json`, architecture checks, UI static architecture checks, Go package tests for the CLI, dev dashboard store, and runtime, dashboard UI typecheck/build, UI freshness checks, `go install ./cmd/onlava`, and installed binary freshness checks
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
- it runs documentation/architecture checks, a parallel dev-session safety check, focused Go tests, dashboard UI typecheck/build, installed-binary freshness checks, and artifact hygiene checks
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
- each line conforms to `onlava.dev.event.v1`
- one JSON object is emitted per stored structured dev event or legacy process-output chunk
- structured events include app id/root, session id, source id/kind/name/role/pid/stream/status, level, message, parsed fields, raw output, and parse metadata
- structured dev events are assigned a stable integer ID before storage, then dual-written to SQLite and VictoriaLogs during the migration; the JSONL output shape is the same for both `--backend sqlite` and `--backend victoria`
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
- [onlava.inspect.docs.v1.schema.json](schemas/onlava.inspect.docs.v1.schema.json)
- [onlava.docs.index.v1.schema.json](schemas/onlava.docs.index.v1.schema.json)
- [onlava.wire.capabilities.v1.schema.json](schemas/onlava.wire.capabilities.v1.schema.json)
- [onlava.inspect.build.v1.schema.json](schemas/onlava.inspect.build.v1.schema.json)
- [onlava.inspect.paths.v1.schema.json](schemas/onlava.inspect.paths.v1.schema.json)
- [onlava.inspect.generators.v1.schema.json](schemas/onlava.inspect.generators.v1.schema.json)
- [onlava.inspect.temporal.v1.schema.json](schemas/onlava.inspect.temporal.v1.schema.json)
- [onlava.task.graph.v1.schema.json](schemas/onlava.task.graph.v1.schema.json)
- [onlava.worker.manifest.v1.schema.json](schemas/onlava.worker.manifest.v1.schema.json)
- [onlava.worker.manifest.v2.schema.json](schemas/onlava.worker.manifest.v2.schema.json)
- [onlava.gen.manifest.v1.schema.json](schemas/onlava.gen.manifest.v1.schema.json)
- [onlava.build.latest.v1.schema.json](schemas/onlava.build.latest.v1.schema.json)
- [onlava.run.event.v1.schema.json](schemas/onlava.run.event.v1.schema.json)
- [onlava.check.result.v1.schema.json](schemas/onlava.check.result.v1.schema.json)
- [onlava.harness.result.v1.schema.json](schemas/onlava.harness.result.v1.schema.json)
- [onlava.harness.self.v1.schema.json](schemas/onlava.harness.self.v1.schema.json)
- [onlava.dev.event.v1.schema.json](schemas/onlava.dev.event.v1.schema.json)
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
onlava inspect traces --json --session current --endpoint SyncGet --min-duration-ms 2000 --since 1h --slowest
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
    "session_id": "feature-a-123abc",
    "limit": 100,
    "since": "1h0m0s",
    "endpoint": "SyncGet",
    "min_duration_ms": 2000,
    "sort": "duration_desc",
    "available_filters": ["--session current|<id>", "--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
  },
  "traces": [
    {
      "trace_id": "trace-1",
      "span_id": "span-1",
      "session_id": "feature-a-123abc",
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
onlava inspect metrics --json --session current --service sync --since 15m
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
    "session_id": "feature-a-123abc",
    "limit": 10000,
    "since": "15m0s",
    "service": "sync",
    "sort": "started_at_desc",
    "available_filters": ["--session current|<id>", "--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
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
