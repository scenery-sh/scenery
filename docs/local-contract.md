# scenery Local Contract

This document freezes the local developer and agent-facing contract for scenery v0.

The goal is to make scenery deterministic and inspectable:
- app shape is explicit
- CLI grammar is explicit
- machine-readable JSON outputs have versioned schemas
- inspect commands are the API; generated files are cache
- app roots, dev runtimes, and capabilities are the user-facing model; substrate paths, ports, backing services, and internal session IDs are debug details

If implementation and this document disagree, treat that as a bug.

## Status

Implemented now. This list describes what the CLI can do today; it is not the
same as the stable v0 support surface.

- `.scenery.json`
- `scenery up --json`
- `scenery serve`
- `scenery worker`
- `scenery version --json`
- `scenery help --json`
- `scenery system toolchain list|sync|verify|path`
- `scenery doctor --json`
- `scenery check --json`
- `scenery generate`
- `scenery generate client`
- `scenery generate sqlc`
- `scenery db psql`
- `scenery db apply`
- `scenery db seed`
- `scenery db setup`
- `scenery db reset`
- `scenery db drop`
- `scenery db snapshot create|restore`
- `scenery db diff --generated`
- `scenery db branch status|list|checkout|reset|delete|restore|diff|expire|prune`
- `scenery db postgres install|start|status|logs|stop|restart|uninstall`
- `scenery worktree create|list|remove`
- `scenery task list|inspect|run|graph`
- `scenery task run <name>`
- `scenery task run <domain>:<name>`
- `scenery validate list|inspect|graph|changed`
- `scenery validate <profile> --json`
- `scenery harness --json`
- `scenery harness self --json`
- `scenery harness ui --json`
- `scenery traces clear --json`
- `scenery inspect app --json`
- `scenery inspect routes --json`
- `scenery inspect services --json`
- `scenery inspect endpoints --json`
- `scenery inspect models --json`
- `scenery inspect views --json`
- `scenery inspect wire --json`
- `scenery inspect build --json`
- `scenery inspect paths --json`
- `scenery inspect generators --json`
- `scenery inspect temporal --json`
- `scenery inspect validation --json`
- `scenery traces list --json`
- `scenery metrics list --json`
- `scenery inspect docs --json`
- `scenery logs --jsonl`

Reserved by contract, implementation pending:
- repo-local runtime and state manifests beyond the command JSON surfaces above

Stable v0 surface:
- `.scenery.json`
- `scenery serve`
- `scenery build`
- `scenery version --json`
- `scenery help --json`
- `scenery check --json`
- `scenery inspect app|routes|services|endpoints|wire|build|paths|docs --json`
- `scenery logs --jsonl`
- `scenery test`
- `scenery generate client`
- typed/raw HTTP endpoints
- auth handler
- service struct initialization and shutdown
- private/internal calls
- secrets from process env and local `.env`
- basic runtime logs and trace emission

Dev-only or beta surface:
- `scenery up`
- `scenery db psql`
- `scenery db apply`
- `scenery db seed`
- `scenery db setup`
- `scenery db reset`
- `scenery db drop`
- `scenery db snapshot create|restore`
- `scenery db branch status|list|checkout|reset|delete|restore|diff|expire|prune`
- `scenery db postgres install|start|status|logs|stop|restart|uninstall`
- `scenery worktree create|list|remove`
- `scenery generate`
- `scenery task list|inspect|run|graph`
- `scenery task run <name>`
- `scenery task run <domain>:<name>`
- `scenery validate`
- `scenery inspect validation --json`
- `scenery traces list|metrics --json`
- `scenery inspect generators --json`
- `scenery inspect temporal --json`
- `scenery system toolchain list|sync|verify|path`
- `scenery doctor --json`
- `scenery system edge install|trust|status|restart|uninstall|dns|privileged --json`
- `scenery worker`
- `scenery traces clear --json`
- `scenery harness ui --json`
- dashboard and API Explorer
- local HTTPS edge and frontend routing
- trust-store installation
- local observability and Grafana capabilities, backed today by Victoria/Grafana substrate and managed binary downloads
- Temporal workflow/activity and cron runtime/admin affordances until their lifecycle, retry, scheduling, and clear/delete semantics are frozen
- cron UI
- `scenery.sh/temporal` workflow/activity declarations and worker registration
- `scenery.sh/model` and `scenery.sh/page` static IR vocabulary, `//scenery:model`, `//scenery:page`, `model.Generate|Disable|Override` CRUD action policy, `scenery inspect models|views --json`, generated model endpoint markers, and beta generated data/web packages until the remaining production reference-app integration work is complete
- `scenery generate data --dry-run --json`, generated desired schema files under `.scenery/gen/db/<service>/schema.hcl`, generated seed files under `.scenery/gen/db/<service>/seed.sql`, generated frontend packages under `.scenery/gen/web/<frontend>/`, `scenery db diff --generated --json`, and `scenery check` model-schema drift diagnostics
- generated model CRUD endpoints/stores in the transient build workspace. These endpoints appear in `scenery inspect endpoints|routes --json` with `"generated": true`; generated CRUD access defaults to `auth` for every action, default generated CRUD route bases are service-scoped as `/<service>/<table>` and collision-checked against reserved route prefixes (`/__scenery`, `/api`, `/sync`) plus handwritten and generated app routes, while `model.Table(...)` remains the physical table name. Generated list endpoints accept `limit` and `offset` query parameters, default to `limit=100`, reject `limit < 1`, `limit > 500`, and negative offsets, and always emit bounded `LIMIT/OFFSET` SQL after tenant filtering. Generated create/patch payloads accept both generated response field names such as `CreatedAt` and DB-column JSON names such as `created_at`, so stored `time.Time` fields use Go's normal RFC3339 JSON parsing and reject malformed timestamps as invalid JSON. Generated stores use one shared package-level pgx pool for the app database selected by the configured app database URL env (`dev.services.postgres.database_url_env`, default `DatabaseURL`) or managed database env; when the resolved DSN changes, the shared pool is closed and reopened. Entities with a convention `TenantID`/`tenant_id` field additionally derive the active tenant from standard auth, scope list/get/update/delete SQL by `tenant_id`, and inject `tenant_id` on create so tenant IDs are not client-writable in generated create/patch payloads. Generated tenant fields support `string`, named string types, and `github.com/google/uuid.UUID`; unsupported tenant field types fail parse/check with an explicit diagnostic.
- migration compatibility for older app shapes

Compatibility posture:
- scenery-native syntax and imports are the stable API.
- Non-scenery directives/imports are not part of the v0 API.

## `.scenery.json`

Schema:
- [scenery.config.v1.schema.json](schemas/scenery.config.v1.schema.json)

Current shape:

```json
{
  "name": "myapp",
  "id": "myapp-dev",
  "proxy": {
    "workspace": "acme",
    "route_base_domain": "local.dev",
    "frontends": {
      "app": {
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
        "output": "apps/web/src/scenery-client.ts"
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
  "validation": {
    "default": "quick",
    "profiles": {
      "quick": {
        "description": "Fast agent handoff gate.",
        "cost": "low",
        "steps": ["harness:core", "task:harness"]
      },
      "frontend": {
        "description": "Frontend validation.",
        "cost": "medium",
        "paths": ["apps/web/**"],
        "steps": ["task:ui-harness"],
        "artifacts": ["test-results/ui-harness/diff-report.md"]
      },
      "full": {
        "description": "Full local quality gate.",
        "cost": "high",
        "steps": ["profile:quick", "profile:frontend"]
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
      "default_user_id": "dev-user",
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
    "task_queue_prefix": "scenery.myapp",
    "payload_codec": "scenery-json-v1",
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
      "db_filename": ".scenery/temporal/dev.db"
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
- If `name` is empty, scenery falls back to `id`.
- App identity for runtime environment, dashboard routes, local logs, browser harness routes, and local observability is `id` when present, otherwise `name`. `name` remains the display name and source/build package identity.
- `proxy` is optional.
- `build.go_flags` is an optional array of literal Go argv entries used for Scenery-owned app compilation. Values are not shell-split; write one argument per item, for example `["-tags=roofmapnet_native"]`. Scenery passes these flags to generated app `go build` invocations and generated-workspace `scenery test` `go test` invocations, while process `GOFLAGS` still applies for local one-off overrides. The normalized flag list participates in the build fingerprint/cache key.
- `auth` is optional. When `auth.enabled` is true, scenery registers the built-in standard auth handler and auth endpoints.
- `observability` is optional.
- `temporal` is optional and disabled by default. Scenery only starts or connects to Temporal when `temporal.enabled` is explicitly `true`; workflow/activity declarations, TypeScript worker settings, and local `auto_start` settings do not enable Temporal by themselves.
- Unknown fields are rejected. Runtime diagnostics include the config file path and JSON field path, for example `/repo/app/.scenery.json: unknown .scenery.json field "proxy.extra"`.
- The removed legacy proxy host key has no compatibility behavior. Remove it from app config; use dev runtime routes or `proxy.api_host`, `proxy.console_host`, and `proxy.frontends` for local routing.
- Agent dev-runtime manifests include `route_namespace`, the app-derived local browser namespace used by routed URLs. `route_namespace.workspace` comes from `proxy.workspace` when present and otherwise falls back to app identity only when no explicit route hosts are configured. `route_namespace.base_domain` defaults to `local.dev` and may be overridden with `proxy.route_base_domain`. `route_namespace.hosts` preserves explicit configured route hosts such as `api`, `console`, `temporal`, `grafana`, and configured frontend names; those hosts become route aliases for that route rather than changing the generated base for every route.
- `proxy.frontends` is a map keyed by frontend name. Each frontend requires `host`; `root` defaults to `apps/<name>`; `upstream` is optional but ignored by agent dev unless that frontend also sets `allow_shared_upstream: true`. With an active agent, `scenery up` prefers to start supported Vite/Astro frontends on hidden loopback ports, inject routed API/Electric URLs and the internal route host into their process environment, register those hidden ports as runtime backends, and expose `https://<frontend>.<route-id>.<route_namespace.base_domain>:<agent-router-port>/` by default. Managed Vite/Astro frontends receive the route host through Vite/Astro allowed-host controls so app configs do not need to hard-code route hosts. Managed frontend routes serve the frontend shell for HTML SPA deep links, while `/__scenery/*`, `/api/*`, `/sync/*`, and concrete asset paths are not history-fallback routes. `SCENERY_FRONTEND_<NAME>_ADDR` still overrides scenery-owned frontend startup for manual debugging.
- `dev.services` is a beta local-development config surface for scenery-owned substrates. Phase 5 accepts `postgres` and `electric` service declarations with `kind`, `mode`, `version`, `isolation`, `project`, `parent_branch`, `parent_database`, `branch_policy`, `branch_name_template`, `branch_strategy`, `ttl`, `database`, `role`, `database_url_env`, `image`, `route`, and string `env` values. The agent currently owns managed Postgres and Electric for this surface, while unsupported service kinds or isolation modes are rejected instead of silently falling back to target-app port orchestration.
- `scenery up` prepares declared local DB setup before the app process starts. When `.scenery.json` declares `database.apply` or service-local seed files are discovered, the supervisor runs the same split lifecycle as `scenery db setup`: apply first, then seed. It passes the same managed Postgres `DatabaseURL` env value that the app child receives, so setup targets the dev-runtime database. Successful setup is fingerprinted from `database.apply` config and seed file hashes; ordinary rebuilds skip setup until those inputs change.
- `dev.setup` is an optional beta list of shell commands that `scenery up` runs from the app root after managed dev services and the DB setup lifecycle are prepared, but before the app process starts. Setup commands receive the same managed Postgres `DatabaseURL` env value as the app child, so target apps can keep existing app-local setup during migration.
- `generators.clients` is a beta lifecycle config for generated TypeScript clients. `kind` defaults to `typescript-client`, `lang` defaults to TypeScript, and `output` is required. `scenery generate client` uses these entries when no explicit `--output` is passed.
- Generated TypeScript clients expose `WithMeta` methods that include response headers, status, `Response`, and parsed `txid` metadata from `X-Txid`/`X-TXID`. Electric-backed write flows should treat the API response and later Electric observation as separate phases: an HTTP success with `X-Txid` means the mutation committed, while `observeAPIResponseTxid(...)` reports later observer failures as `SyncObservationError` with `kind: "sync_observation_failure"`, `mutation_committed: true`, app/session/API/Electric context, txid, and observer error details.
- `generators.sqlc` is a beta lifecycle config for SQLC generation. `provider` may be empty or `sqlc`; `config` defaults to `sqlc.yaml`; `dev_url` defaults to `docker://postgres/18/dev`. When a SQLC schema path follows `<pkg>/db/gen/schema.sql` and `<pkg>/db/schema.hcl` exists, `scenery generate sqlc` refreshes the generated schema SQL with `atlas schema inspect` before running `sqlc generate`. SQLC generation is a generated-source lifecycle and must not apply database schema or seed data.
- Static model data generation is a beta read-only data lifecycle. `scenery generate data --dry-run --json` parses `//scenery:model` IR and writes desired Atlas HCL to disposable generated files at `.scenery/gen/db/<service>/schema.hcl`; generated model tables live in the app-owned schema derived from `<service>` rather than `public`, and generated Atlas resource labels are schema-qualified (`table "<service>" "<table>"`, `enum "<service>" "<enum>"`) to avoid cross-schema label collisions in apps with existing multi-schema HCL. Typed `model.Seed(...)` rows write deterministic idempotent upsert SQL to `.scenery/gen/db/<service>/seed.sql` using the same schema-qualified table, and generated CRUD stores use that table through one shared package-level pgx pool for the configured app database URL env or Scenery's managed database env. Generated CRUD endpoints default to `auth` for every action; generated list endpoints are capped to 100 rows by default and accept validated `limit`/`offset` query parameters up to a maximum limit of 500; generated create/patch payloads accept response field names and DB-column JSON names, so `time.Time` fields round-trip RFC3339 JSON timestamps or fail with a field-scoped JSON decode error; tenant-shaped entities add standard-auth tenant scoping on top of that access requirement and support `string`, named string, or `github.com/google/uuid.UUID` tenant fields. When a configured frontend and `//scenery:page` collection view are present, the same command also writes a beta hidden TypeScript package under `.scenery/gen/web/<frontend>/` with row/create/patch types, Electric shape definitions that expose `schema`, `table`, and `qualifiedTable`, TanStack DB collection descriptors/materializers, runtime adapter factories, route/default page factories, route registration helpers, and slot type assertions against the frontend's component files. Frontends consume the package through app-owned aliases such as `@scenery/generated` and provide the `@scenery/layout-kit` contract. `--dry-run` means no database mutation. `scenery db diff --generated --json` compares desired schemas with app-owned `SERVICE/db/schema.hcl` files and emits `scenery.db.generated_diff.v1`. `scenery check --json` reports `model-schema` diagnostics when the app-owned schema is missing or drifts from the generated desired schema. Apps without model directives have no generated model data work.
- `database.apply` is a beta DB lifecycle escape hatch. Phase 1 supports only `provider: "exec"` with an explicit shell `command`, optional `cwd`, and string `env` overlay. The accepted split lifecycle moves database mutation to `scenery db apply`; SQLC refresh stays under `scenery generate sqlc`.
- Service-local `SERVICE/db/seed.sql` and generated `.scenery/gen/db/<service>/seed.sql` files are initial data. They are not Atlas schema input or SQLC input. The accepted lifecycle applies seed data through `scenery db seed`; generated model seed files are materialized before seed discovery. The first implementation fails closed on changed previously-applied seed files and obviously destructive seed SQL rather than adding force or reseed escape hatches.
- `tasks` is a beta thin repo-task layer. Each configured task can define either `run` or `steps`, plus optional `cwd` and string `env`. `run` uses the platform shell from the app root or task cwd. `steps` currently accepts `task:<name>`, `task:<domain>:<name>`, `check`, `test`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`.
- Code tasks are beta app-local targets under `<domain>/tasks/`. Targets use `<domain>:<name>`, and both segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`. `scenery task list`, `scenery task inspect`, and `scenery task run <domain>:<name> [-- task args...]` discover and execute them without requiring the app model to parse cleanly.
- `validation` is a beta app-owned quality-gate layer. It has `default` and `profiles`; each profile can define `description`, `cost` (`low`, `medium`, or `high`), `paths`, `steps`, string `env`, and advisory `artifacts`. Profile names use the configured-task name rule and cannot contain `:`.
- Validation profile steps are not shell. They accept `profile:<name>`, `task:<name>`, `task:<domain>:<name>`, `harness:core`, `harness:ui`, `harness`, `check`, `test`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`. Shell commands must live behind configured `tasks.<name>.run`.
- `dev.services.postgres` currently defaults to version `18` and `isolation: "database"`. Other isolation modes are rejected until implemented. With an active agent-backed dev runtime, scenery creates or reuses a physical Postgres server substrate, verifies the recorded owner/reachability/version before reuse, and separately allocates either a deterministic per-runtime database or, when `branch_policy`, `branch_name_template`, `branch_strategy`, `parent_branch`, or `parent_database` is set, a deterministic branch database. The global Postgres substrate record contains physical-server metadata only: admin URL, version, isolation, data/socket directories, port, source, process owners, and exit metadata. It must not contain `session.<id>` database URLs or names. The runtime or branch database lease is exposed through app env as `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, and `SCENERY_MANAGED_DATABASE_NAME`, even when local env files already contain stale database URLs. Managed app, setup, DB setup, and worker environments do not receive Scenery-injected `DATABASE_URL`; `SCENERY_MANAGED_DATABASE_URL` remains available as tooling/debug metadata. The admin cluster comes from `SCENERY_DEV_POSTGRES_ADMIN_URL`, a reusable agent Postgres substrate, Docker when available for the requested version, or local `initdb`/`postgres` binaries under the agent state directory. Managed local Postgres starts with logical replication settings so `dev.services.electric` can attach. `SCENERY_DEV_POSTGRES_INITDB` and `SCENERY_DEV_POSTGRES_BIN` can point at explicit local binaries. Set `SCENERY_DEV_POSTGRES_EXTERNAL=1` to keep an explicit external `DatabaseURL` instead of using the managed runtime database; external mode requires `DatabaseURL` and ignores `DATABASE_URL` as an app database authority. Old substrate records with legacy `session.<id>` keys remain readable during adoption, but new writes omit those keys.
- `dev.services.postgres.kind: "postgres"` with explicit branch fields uses the managed Postgres branch provider. Phase one supports `mode: "local"`, `isolation: "database"`, and `branch_strategy: "template_database"`. `scenery db postgres install|start|status|logs|stop|restart|uninstall --json` manages or inspects the shared local Postgres dev cell. Branch pins live at `<app-root>/.scenery/worktree-db.json` with `schema_version: "scenery.db.branch.v1"` and provider `postgres`. The global branch registry lives at `~/.scenery/agent/postgres/branches.json` or `SCENERY_AGENT_HOME/agent/postgres/branches.json` with `schema_version: "scenery.db.branch.registry.v2"`, provider `postgres`, and endpoint metadata only. Raw connection URLs are not stored in the pin, registry, restore-point state, or branch status/list JSON.
- Only leases whose pin has `created_by: "scenery"` and `provider: "postgres"` are treated as Scenery-owned; foreign leases are hidden from branch list/status resolution, are not expired/pruned/deleted by Scenery cleanup, and block checkout when they match the requested project and branch. `scenery db branch status --json` reports `scenery.db.branch.status.v1` for the current app root and returns `status: "unpinned"` when the pin does not exist. It includes `connection` only for ready non-parent leases with endpoint metadata, and reports `backend_status: "protected"` for a ready parent-branch lease without exposing connection metadata. `scenery db branch list --json` reports Scenery-owned local lease pins and provider-normalized lease entries as `scenery.db.branch.list.v1`, including lease `status`, optional endpoint metadata, timestamps, and the registry path; protected parent leases report `status: "protected"` and suppress `endpoint` even if the registry lease is marked ready.
- `scenery db branch checkout <name> [--json]` writes or replaces the local pin using sanitized branch names and stable `br-local-*` IDs, keeps `.scenery/` ignored, ensures the parent template database exists, creates or reuses the branch database from that template, and records a ready endpoint without persisting raw connection URLs. During `scenery up`, an existing pin wins and is ensured through the same provider boundary; `branch_policy: "manual"` requires a prior checkout; `branch_policy: "worktree"` derives the branch from `branch_name_template` using `{app}`, `{git_branch}`, `{worktree}`, and `{session}`; `branch_policy: "session"` defaults to `{app}/{session}` when no template is set.
- `scenery db branch delete <name> [--force]` drops the matching Scenery-owned branch database, removes its lease, and removes the current worktree pin only when the deleted branch is current and `--force` is present. `scenery db branch reset --yes` drops and recreates the branch database from the parent template. `scenery db branch restore --at <timestamp-or-lsn> --yes` currently maps to template reset and emits `scenery.db.branch.restore.v1` with restore-point metadata. `scenery db branch diff <branch> [--json]` emits a deterministic schema-catalog diff when both branch databases exist and reports existence otherwise. `scenery db branch expire [<name>] --after <duration>` updates the local lease expiration, and `scenery db branch prune [--older-than <duration>]` removes expired non-current branch databases when the Postgres admin substrate is reachable.
- `scenery up`, `scenery db psql`, DB setup, and Electric rerun the configured branch-provider ensure boundary and wait briefly for a pending non-parent lease to become ready before synthesizing a process-local `DatabaseURL`. They still synthesize that URL only from a ready lease endpoint, and fail explicitly when no branch driver is configured, the pending lease does not become ready, or the lease is missing, expired, protected, or endpoint-less.
- `dev.services.electric` supports explicit upstream routing with `SCENERY_DEV_ELECTRIC_UPSTREAM`; when set, scenery registers the upstream as a hidden runtime backend and injects `ELECTRIC_URL`/`SCENERY_ELECTRIC_URL` using the agent route. Without an explicit upstream, scenery starts a hidden per-runtime Electric process from `SCENERY_DEV_ELECTRIC_BIN` or, when `dev.services.electric.image` is set and Docker is available, from that image. Electric uses the common managed process readiness and early-exit lifecycle, but remains runtime-scoped: it is registered as an agent backend/process, not as a global Electric substrate row. Electric receives the managed Postgres runtime database URL when `dev.services.postgres` is declared. When declared Postgres is in `SCENERY_DEV_POSTGRES_EXTERNAL=1` mode, Electric derives its private adapter URL from `DatabaseURL`; without declared Postgres it can still receive explicit `DatabaseURL`/`DATABASE_URL`. scenery also sets a deterministic runtime-scoped `ELECTRIC_REPLICATION_STREAM_ID` by default so multiple worktrees can share one Postgres cluster without colliding on Electric publication or replication-slot names. Configured `dev.services.electric.env` values stay on the Electric process/container and are not injected into the app process; an explicit `ELECTRIC_REPLICATION_STREAM_ID` there overrides the scenery default.
- Standard auth uses the `scenery.sh/auth` top surface and stores DB-backed auth state in PostgreSQL schema `scenery_auth`.
- Standard auth owns its framework tenant tables, including `scenery_auth.tenants`. Apps do not need an app-local `tenants` service, package, or table for standard auth; app-local tenant services are product-domain APIs and schema only.
- Standard auth registers `/auth/signup/email`, `/auth/login/email`, `/auth/refresh`, `/auth/logout`, `/auth/me`, organization/invite/impersonation endpoints, Google OAuth raw endpoints, and local `/users/dev-bootstrap`.
- Standard auth endpoints appear in `scenery inspect routes|services|endpoints --json` and in generated TypeScript clients.
- `auth.auto_bootstrap_database` applies the first standard-auth schema bootstrap at runtime. It is useful for local fixtures; production deployments should manage schema changes deliberately.
- `temporal.address_env` defaults to `TEMPORAL_ADDRESS`; when that env var is unset, runtime defaults to `127.0.0.1:7233`.
- `temporal.namespace` defaults to `TEMPORAL_NAMESPACE` when that env var is set, otherwise `default`.
- `temporal.task_queue_prefix` defaults to `scenery.<app-name>` with unsafe task-queue characters normalized to dots. `SCENERY_TEMPORAL_TASK_QUEUE_PREFIX` overrides the effective runtime prefix; `scenery up` sets it to a runtime-scoped value when the local agent is active. Test-marked runtimes append `SCENERY_TEMPORAL_TASK_QUEUE_TEST_SUFFIX` to the effective prefix so `scenery test` workers cannot poll or schedule onto a live dev session's Temporal queues for the same checkout.
- `temporal.payload_codec` defaults to `scenery-json-v1` and is validated at runtime. This is the only supported payload profile for scenery-managed Go and external workers in this milestone.
- `temporal.api_key_env` defaults to `TEMPORAL_API_KEY`. When set, the runtime uses Temporal API-key credentials.
- `temporal.tls.enabled` enables TLS without requiring an API key. `temporal.tls.server_name_env`, `ca_cert_file_env`, `client_cert_file_env`, and `client_key_file_env` default to `TEMPORAL_TLS_SERVER_NAME`, `TEMPORAL_TLS_CA_CERT_FILE`, `TEMPORAL_TLS_CERT_FILE`, and `TEMPORAL_TLS_KEY_FILE`. Client certificate and key env vars must be set as a pair for mTLS.
- Temporal worker deployment metadata is runtime-owned: `deployment_name` defaults to the task-queue prefix normalized for Temporal Worker Deployment naming and can be overridden with `SCENERY_TEMPORAL_DEPLOYMENT_NAME`; `worker_build_id` defaults to `dev` and can be set with `SCENERY_BUILD_ID`.
- Temporal workers opt into Worker Deployment Versioning. `SCENERY_TEMPORAL_VERSIONING_BEHAVIOR` accepts `pinned` or `auto_upgrade` and defaults to `pinned`.
- Temporal workers enable Go SDK host resource reporting by default using Temporal's `contrib/sysinfo` provider, so Worker heartbeats can include CPU and memory usage for Temporal Cloud worker health views. Set `SCENERY_TEMPORAL_HOST_RESOURCE_REPORTING=0` to disable this provider.
- Local scenery-managed worker processes set their `worker_build_id` as the current Temporal Worker Deployment version on startup so schedules and new workflow executions have a versioned routing target. Non-local workers do not self-promote; operators must promote deployment versions explicitly.
- `temporal.local.auto_start` and `temporal.local.db_filename` are local development settings for supervised Temporal dev server work and are only active when `temporal.enabled` is true. With an active agent, the Temporal dev server is registered as a shared agent substrate and its local database state is stored under the agent directory; each dev runtime also registers a `temporal` route for the shared UI, while app workers receive runtime-scoped task queue prefixes. Explicit workflow/activity task queues are prefixed in active dev runtimes too, so parallel worktrees do not poll or schedule onto each other's queues. Reuse of an agent-recorded Temporal substrate requires a verified owner fingerprint and a reachable Temporal listener before app workers start; stale ready records are discarded and replaced. Temporal stdout and stderr are always written to stable substrate log files and the agent registry records exit metadata when the managed process exits.
- `SCENERY_TEMPORAL_TASK_QUEUE` overrides the generated Temporal task queue for worker processes. `scenery worker --task-queue <name>` and `scenery worker typescript --task-queue <name>` set it.
- TypeScript Temporal activity support is activity-only. scenery discovers `*.worker.ts` files, plus ordinary `.ts` files with `//scenery:worker`, and generates `.scenery/generated/temporal/typescript/{scenery.ts,registry.ts,worker.ts,manifest.json,tsconfig.json,package.json}`. Source files import `activity` from `scenery/worker` or `@scenery/temporal`; the generated `tsconfig.json` maps both names to the generated local API. Before launching the generated worker, `scenery up` and `scenery worker typescript` install the app-root `package.json` dependencies and the generated worker package dependencies with `bun install`, falling back to `npm install` when Bun is unavailable.
- Go workflows declare TypeScript activities with `temporal.NewExternalActivity` using matching input/output type parameters and call them through `temporal.ExecuteActivity`. `scenery check --json` validates matching TypeScript activity names, task queues, and type names before build/runtime.
- `temporal.typescript.enabled`, `runtime`, and `auto_start` configure the TypeScript worker path. `scenery worker typescript` generates and runs the hidden worker directly. When `temporal.enabled`, `temporal.typescript.enabled`, and `auto_start` are all true, `scenery up` validates Go-to-TypeScript contracts, regenerates the hidden worker runtime, and supervises the TypeScript worker alongside the Go app. The worker receives the supervised Temporal address/namespace, runtime-scoped task queue prefix, deployment name, build ID, and internal agent identity environment. `runtime` accepts `bun` or `node`; when empty, scenery prefers `bun` and falls back to `node --import tsx`.
- Generated binaries accept `SCENERY_ROLE=all|api|worker`. `scenery up` uses the default combined role. `scenery serve` uses `api`. `scenery worker` uses `worker`.
- Packages that declare `scenery.sh/temporal` workflows or activities with `temporal.NewWorkflow`, `temporal.NewActivity`, or `temporal.NewExternalActivity` are imported into the generated main so their declarations register at startup.
- `temporal.ActivityConfig.MaxConcurrency` maps to the Temporal worker's per-task-queue maximum concurrent activity executions. Use a dedicated task queue when different activities need different limits. `temporal.WithHeartbeatTimeout(duration)` sets the workflow activity heartbeat timeout without changing the stable `ActivityConfig` struct shape.
- Cron jobs can set `cron.JobConfig.OverlapPolicy`, `CatchupWindow`, `PauseOnFailure`, `ActivityStartToClose`, and `ActivityRetryPolicy`. When Temporal is enabled these map to Temporal Schedule overlap/catchup/pause policy and to the generated cron activity options. Defaults are overlap `skip`, catchup window `1m`, pause-on-failure `false`, and activity start-to-close `1h`.
- Optional multi-language worker manifests live under `.scenery/workers/*.json` and use `scenery.worker.manifest.v1` or `scenery.worker.manifest.v2`. They require `build_id` and `payload_codec: "scenery-json-v1"`. v2 manifests use queue-level registrations with `registration_hash` values so `scenery inspect temporal --json` can reject incompatible workers sharing a Temporal task queue.

## CLI Grammar

Current implemented grammar:

```text
scenery up [--port <n>] [--listen <addr>] [--app-root <path>] [--claim-aliases] [-v|--verbose] [--json] [--detach]
scenery logs --follow [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [--jsonl|--json]
scenery logs query [--app-root <path>] --query <logsql> [--since <duration>] [--start <time>] [--end <time>] [--limit <n>] [--timeout <duration>] [--fields <csv>] [--json|--jsonl]
scenery logs tail [--app-root <path>] --query <logsql> [--since <duration>] [--timeout <duration>] [--fields <csv>] [--jsonl]
scenery console [--app-root <path>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]
scenery system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
scenery system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
scenery system edge install|trust|status|restart|uninstall|dns|privileged [--json]
scenery help <command>
scenery help all
scenery help --json
scenery ps [--json] [--app-root <path>] [--watch]
scenery down [--app-root <path>] [--db] [--state] [--all] [--json]
scenery prune --older-than <duration> [--app-root <path>] [--json]
scenery serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker bindings [--app-root <path>] [--out <dir>] [--json]
scenery worker typescript [--task-queue <name>[,<name>...]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
scenery worker deployment set-current --build-id <id> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
scenery worker deployment ramp --build-id <id> --percentage <0-100> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
scenery worker deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
scenery version [--json]
scenery system toolchain list [--json] [--include-source-locks] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
scenery system toolchain path [--json] --tool <name> [--platform <goos/goarch>]
scenery doctor [--app-root <path>] [--json]
scenery build [--app-root <path>] [-o <path>]
scenery check [--app-root <path>] [--json]
scenery db psql [--app-root <path>] [psql args...]
scenery db apply [--app-root <path>] [--json]
scenery db seed [--app-root <path>] [--dry-run] [--json]
scenery db setup [--app-root <path>] [--json]
scenery db reset [--app-root <path>]
scenery db drop [--app-root <path>]
scenery db snapshot create|restore <name> [--app-root <path>]
scenery db branch status|list [--app-root <path>] [--json]
scenery db branch checkout <name> [--app-root <path>] [--json]
scenery db branch reset [--app-root <path>] [--yes]
scenery db branch delete <name> [--app-root <path>] [--force]
scenery db branch restore --at <timestamp-or-lsn> [--app-root <path>] [--yes]
scenery db branch diff <branch> [--app-root <path>] [--json]
scenery db branch expire [<name>] --after <duration> [--app-root <path>] [--json]
scenery db branch prune [--older-than <duration>] [--app-root <path>] [--json]
scenery db postgres install|start|status|logs|stop|restart|uninstall [--json]
scenery generate [--app-root <path>] [--dry-run] [--json]
scenery generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
scenery generate sqlc [--app-root <path>] [--dry-run] [--json]
scenery task list [--app-root <path>] [--json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery task graph --json [--app-root <path>]
scenery validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery validate list [--app-root <path>] [--json]
scenery validate inspect <profile> [--app-root <path>] [--json]
scenery validate graph [<profile>] [--app-root <path>] --json
scenery validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery harness [--app-root <path>] [--json] [--write] [--with-validation[=<profile>]]
scenery harness self [--repo-root <path>] [--summary|--json|--json=summary|--json=full] [--write] [--quick|--race|--release] [--fresh-tests]
scenery harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
scenery inspect app|routes|services|endpoints|models|views|wire|build|paths|generators|temporal|observability|validation --json [--app-root <path>]
scenery inspect docs --json [--repo-root <path>]
scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]
scenery traces list --json [--app-root <path>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
scenery metrics list --json [--app-root <path>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
scenery metrics query --json [--app-root <path>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]
scenery metrics labels --json [--app-root <path>] [--match <selector>] [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
scenery metrics series --json [--app-root <path>] --match <selector> [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
scenery traces clear --json [--app-root <path>]
scenery logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [-f|--follow] [--jsonl|--json]
scenery test [--app-root <path>] [go test flags/packages...]
scenery generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
```

Implemented beta/dev helper grammar:

```text
scenery db psql [--app-root <path>] [psql args...]
scenery db branch status|list [--app-root <path>] [--json]
scenery db branch checkout <name> [--app-root <path>] [--json]
scenery db branch reset [--app-root <path>] [--yes]
scenery db branch delete <name> [--app-root <path>] [--force]
scenery db branch restore --at <timestamp-or-lsn> [--app-root <path>] [--yes]
scenery db branch diff <branch> [--app-root <path>] [--json]
scenery db branch expire [<name>] --after <duration> [--app-root <path>] [--json]
scenery db branch prune [--older-than <duration>] [--app-root <path>] [--json]
scenery db postgres install|start|status|logs|stop|restart|uninstall [--json]
scenery worktree create <name> [--from <branch>] [--app-root <path>] [--json]
scenery worktree list [--app-root <path>] [--json]
scenery worktree remove <name> [--app-root <path>] [--db] [--json]
```

`scenery db psql` connects to the managed dev-runtime database when `dev.services.postgres` is configured and an agent-backed runtime is active; otherwise it uses explicit local database configuration. With `dev.services.postgres.kind: "postgres"` plus explicit branch fields, `scenery db psql` reruns the configured branch-provider ensure boundary and waits briefly for a pending lease to become ready. Without an active runtime it reads `.scenery/worktree-db.json` directly and connects only when the Scenery-owned local lease is already ready and contains endpoint metadata. It fails explicitly when the lease stays pending, is missing, expired, protected, or endpoint-less. `scenery db snapshot create|restore` stores SQL files under the current runtime's internal `.scenery/sessions/<session>/db/snapshots/` directory and uses host `pg_dump`/`psql` against the managed runtime or branch database. For Postgres branches, restore resets the branch from the parent template before importing the SQL. `scenery db reset` and `scenery db drop` are only available for regular managed runtime databases. `scenery db apply` runs only an explicit `database.apply` provider and does not run seed files or SQLC generation.

`scenery db postgres install|start|status|logs|stop|restart|uninstall --json` manages the shared local Postgres dev cell. `scenery db branch status --json` and `scenery db branch list --json` inspect the current app root's `.scenery/worktree-db.json` pin and the global local `branches.json` lease registry, emitting `scenery.db.branch.status.v1` and `scenery.db.branch.list.v1`; `backend_status` distinguishes local pin state from backend branch state and can report `pending`, `missing`, `expired`, `protected`, or `ready`. Ready non-parent branch status includes redacted `connection` endpoint metadata and never a raw URL; protected parent branch status suppresses connection metadata. `scenery down --db` removes the app root's current local branch lease when runtime metadata is present, or the current non-parent local branch lease for legacy/worktree pins; `scenery down --state` removes the app root's local `.scenery/worktree-db.json` pin after runtime state cleanup and does not remove the shared Postgres dev-cell state.

`scenery worktree create <name> --json` runs `git worktree add -b <name>` next to the current app root and emits `scenery.worktree.create.v1`. When the target app declares Postgres branching, it also writes the target worktree's local `.scenery/worktree-db.json` branch pin, runs the branch-provider ensure boundary, and rolls back the Git worktree if pin creation or ensure fails. `scenery worktree list --json` emits `scenery.worktree.list.v1` from `git worktree list --porcelain`. `scenery worktree remove <name> --db --json` first resolves the target from `git worktree list --porcelain`, then removes local `.scenery` state before `git worktree remove`, and emits `scenery.worktree.remove.v1`; backend branch deletion is handled by `scenery db branch delete`.

DB lifecycle split:
- `scenery db apply` mutates schema or app-owned database setup only. It does not run seed files or SQLC generation.
- `scenery db seed` applies service-local initial data such as `SERVICE/db/seed.sql` and generated model seed data at `.scenery/gen/db/<service>/seed.sql`. It runs after schema exists and does not participate in Atlas or SQLC generation. It records successful runs in `scenery_internal.seed_runs` keyed by app ID and seed path. Unchanged seeds are skipped; changed previously-applied seeds fail closed with status `changed`. Seed validation also fails closed before opening the database when SQL contains destructive setup patterns such as `DROP`, `TRUNCATE`, `DELETE FROM ...` without `WHERE`, `WHERE true`, or `WHERE 1 = 1`; diagnostics include the seed path, line, message, and statement context.
- `scenery db setup` runs `db apply`, then `db seed`. It reports both phases in JSON mode and stops before seed if apply fails.
- `scenery generate sqlc` remains the SQLC generated-source command. It may refresh generated schema SQL from schema definitions and run `sqlc generate`; it must not mutate a database or consume seed files.
- `scenery up` runs the setup lifecycle before starting the app when DB setup inputs exist, and reruns it on rebuild only when the `database.apply` config or discovered seed file hashes change. Setup failures are reported through the existing compile/setup failure path and dev event stream, and the previous successful fingerprint is not advanced so the next rebuild can retry.

Doctor rules:
- `scenery doctor` is a fast, read-only local environment diagnostic. It does not install tools, download managed artifacts, start services, run builds, connect to databases, or mutate `.scenery/`.
- `scenery doctor --json` emits `scenery.doctor.result.v1` and exits non-zero only when required checks have status `error`.
- Check statuses are `ok`, `warn`, `error`, and `skipped`. Check severities are `required`, `optional`, and `informational`.
- Required failures currently cover baseline host readiness such as missing/old Go, very low memory, very low disk space, or an explicitly invalid `--app-root`.
- Doctor reports local state size through informational `storage.scenery_home` and `storage.postgres_database` checks. `storage.scenery_home` walks the resolved Scenery agent home (`~/.scenery` by default or `SCENERY_AGENT_HOME` when set). `storage.postgres_database` walks the managed Postgres state root at `<scenery-home>/agent/postgres`; if that path has not been installed yet, the check is `skipped` with the expected path.
- Optional missing tools such as `bun`, `atlas`, `sqlc`, and `git` warn by default. App configuration can make their messages more specific, but the initial doctor contract does not make optional tools fatal. Doctor reports Docker through `docker.context` and `docker.engine` checks instead of a generic host `tool.docker` line. `docker.context` reports the selected Docker context from `docker context show`. `docker.engine` warns when the Docker CLI is missing or the engine is unreachable, and when reachable it probes with `docker info --format '{{json .}}'` and reports engine details such as server version, OS/type, architecture, CPU/memory, root dir, storage driver, cgroup version, kernel version, and engine name when available.
- `--app-root` tunes app-sensitive diagnostics from `.scenery.json`. If omitted, doctor tries current-directory app discovery and silently continues with environment-only checks when no app is found.

Inspect rules:
- `scenery inspect` requires a subject.
- `scenery inspect` currently requires `--json`.
- `--app-root` is optional. When omitted, scenery walks upward from the current working directory to find `.scenery.json`.
- Stable inspect subjects for v0 are `app`, `routes`, `services`, `endpoints`, `wire`, `build`, `paths`, and `docs`.
- `generators`, `temporal`, `traces`, `metrics`, and `observability` are beta diagnostic subjects. `generators` reports configured generation graph inputs and outputs. `temporal` reports effective Temporal config and, when enabled, a short connectivity check. `traces`, `metrics`, and `observability` read scenery-managed local observability data. Victoria is the current backing substrate, not the integration API. If no local state exists, query/discovery commands return valid JSON with warnings and empty result sets where possible.
- `scenery inspect observability --json` emits `scenery.inspect.observability.v1` with backend readiness for logs, metrics, and traces; native dialect names; examples; and the exact enforced query scope for the selected app/session.
- The `scenery.inspect.traces.v1`, `scenery.inspect.metrics.v1`, `scenery.inspect.observability.v1`, `scenery.logs.query.v1`, `scenery.logs.tail.entry.v1`, `scenery.metrics.query.v1`, `scenery.metrics.labels.v1`, and `scenery.metrics.series.v1` schemas are useful for agents, but their source-selection, retention, rollup, percentile, and clear/delete semantics are not stable v0 API yet.
- `--since` accepts Go duration strings such as `15m`, `1h`, or `24h`.
- `--min-duration-ms` filters root traces by duration in milliseconds.
- `--status` accepts `ok` or `error`.
- `metrics` defaults to `--since 24h` and `--limit 10000` so agents get useful local summaries without scanning unbounded history.
- User-facing dev lifecycle and observability commands scope to the app root. Internal session IDs remain in JSON records, manifests, routes, and state paths for compatibility, but users should not select or create runtime sessions directly.
- `logs query` defaults to the app root's live runtime, `--since 15m`, `--limit 200`, `--timeout 3s`, and JSON envelope output. `--limit` is capped at 2000 and reports a JSON warning when clamped. It accepts native VictoriaLogs LogsQL through `--query`; `--logql` is rejected rather than silently treating Loki LogQL as LogsQL. Finite queries use an HTTP context deadline derived from `--timeout`.
- `logs tail` streams scoped `scenery.logs.tail.entry.v1` JSONL log entries from the VictoriaLogs live-tail endpoint, maps `--since` to VictoriaLogs `start_offset`, rejects `--start` and `--end`, and exits through normal context cancellation or interrupt handling.
- `metrics query` defaults to range mode for the app root's live runtime with `--since 15m`, `--step 5s`, `--timeout 3s`, `--limit 100`, and JSON output. `--limit` is capped at 10000 and reports a JSON warning when clamped. `--instant` switches to the instant Prometheus API endpoint. Finite queries use an HTTP context deadline derived from `--timeout`.
- `metrics labels` and `metrics series` default to the app root's live runtime with `--since 1h`, `--timeout 3s`, and `--limit 1000`; catalog limits are capped at 10000 and report a JSON warning when clamped. `metrics labels` accepts optional `--match`, and `metrics series` requires `--match`.
- Query commands are scoped by default. Scenery applies LogsQL scope through VictoriaLogs `extra_filters` and metrics scope through repeated VictoriaMetrics `extra_label` query parameters, and every JSON envelope echoes `scope.enforced=true`.
- `docs` inspects the scenery repo knowledge base, not a target scenery app. It accepts `--repo-root` and otherwise walks upward to the `module scenery.sh` repo root.

Toolchain rules:
- `scenery.toolchain.json` is the root checked-in manifest for Scenery-owned development executables, Docker images, plugins, and source lock references.
- The manifest uses `scenery.toolchain.v1`; `scenery system toolchain ... --json` emits `scenery.toolchain.status.v1`.
- Binary artifacts may use `platforms` for downloaded archives or `source_build: {kind: "go", package: "./cmd/..."}` for source-built Scenery binaries. Source-built artifacts are compiled with `go build` into the managed toolchain store and report `source: "source-build"` in toolchain status.
- `--tool <name>` selectors must match a manifest artifact exactly. Unknown selectors fail closed with `unknown toolchain artifact "<name>"` instead of returning an empty successful status.
- `scenery version --json` includes `toolchain_manifest.schema_version`, `sha256`, `artifact_count`, and `source_lock_count` for the bundled manifest.
- The default local store is `.scenery/toolchain/` under the app/repo root. Machine-level edge tools use `~/.scenery/toolchain/` under the local agent home. `SCENERY_TOOLCHAIN_DIR` overrides both store roots.
- `SCENERY_TOOLCHAIN_DOWNLOAD=0` disables automatic managed binary downloads. Per-tool download disable variables such as `SCENERY_DEV_GRAFANA_DOWNLOAD=0` and `SCENERY_DEV_VICTORIA_DOWNLOAD=0` still apply to their startup paths.
- Managed Caddy resolves from the managed store or manifest-driven download. Managed Grafana, Victoria, and Temporal CLI binaries resolve from explicit env overrides, the managed store, or manifest-driven download. They do not use implicit system `PATH` binaries.
- `scenery system toolchain verify --strict --images` fails for tag-only image refs. Tag-only image refs marked `stability: "unstable"` are accepted only outside strict verification during the migration to digest-pinned images.
- Go modules and UI package-manager files are source locks. Commands such as `go`, `bun`, `npm`, `node`, and `tsx` used to run source/package-manager workflows are not hidden Scenery-managed toolchain downloads.

Command split:

- `scenery up` starts the app root's one live dev runtime: app process, file watching, and rebuild/restart supervision. A second live code copy requires a separate Git worktree.
- `scenery up --detach` requires the local agent, starts the same dev supervisor in a background child process, waits for that child PID to register as the app root's runtime owner, prints a Docker-style app action summary, status/log/stop commands, and currently registered routes/aliases, then returns. Detached child stdout/stderr from the supervisor is written under the agent directory; app process output continues to flow through the scoped dashboard log store.
- `scenery logs --follow` follows the app root's live runtime logs by default with the same app-root, limit, stream, source, kind, level, grep, since, backend, and JSONL options, and it does not mutate runtime state.
- `scenery logs`, plain `scenery logs --follow`, and `scenery console` read structured dev events for the selected app root's live runtime. `--backend auto` and `--backend victoria` currently select the same Victoria-backed substrate path; use backend selection only when intentionally debugging that substrate. `SCENERY_LOGS_BACKEND` accepts the same values and applies to the console as well.
- If the backing dev-event substrate is unavailable, structured dev-event read commands fail loudly instead of falling back to the deprecated local process-output cache.
- `scenery console` opens the source-aware terminal console when stdin/stdout are real TTYs. In CI, dumb terminals, or redirected output it falls back to normal log following with the same backend option.
- Structured dev logs carry source identity. Current source ids include `api`, `worker:typescript`, `build`, `supervisor`, `temporal`, `electric`, `grafana`, `victoria`, and `frontend:<name>`.
- `scenery system agent restart` stops the currently reachable local agent process, starts a new background agent, waits until the control socket is reachable, and returns. The same `--socket`, `--router-listen`, `--router-tls`, `--trust`, and `--json` options apply to the restarted agent.
- `scenery system edge dns install` resolves the managed `dnsmasq` toolchain artifact, syncing/building it automatically unless managed downloads are disabled, starts user-owned dnsmasq for the configured wildcard dev domain plus other Scenery-managed resolver domains already present on the machine, and on macOS invokes a privileged helper only when `/etc/resolver/<domain>` is missing or mismatched. `scenery system edge privileged install` installs the macOS root-owned loopback helper that listens on `127.0.0.1:443` and `[::1]:443` and forwards raw TCP only to a validated user-owned Caddy target recorded under the agent run directory. Run it as the normal user; it invokes `sudo` only for the minimal helper install. `scenery system edge privileged uninstall` removes that helper. `scenery system edge install` and `scenery system edge restart` refuse root, start user-owned Caddy on an unprivileged high loopback port, ensure the local agent router is running as an unprivileged HTTP upstream on its internal loopback address, disable Caddy response buffering for streaming routes such as Electric SSE while preserving upstream cache headers, and write both edge state and helper target metadata under the agent run directory. If wildcard DNS or the privileged helper is missing or unhealthy, install prepares Caddy but fails with the actionable setup command because browser-ready default-port HTTPS requires both. They resolve Caddy from the managed `caddy` toolchain artifact, syncing it automatically unless managed downloads are disabled. `scenery system edge trust` resolves the same managed Caddy artifact, starts a temporary admin-only Caddy process with `local_certs`, runs Caddy's trust flow against that temporary admin endpoint, and does not require the port-443 edge to be running. `scenery system edge status --json` reports `scenery.edge.status.v1`. `scenery system edge uninstall` stops user-owned Caddy, leaves DNS and the privileged helper alone, and reports `scenery system edge privileged uninstall` as the helper removal command.
- `scenery down` stops and unregisters the selected app root's live dev runtime but is non-destructive by default. `--db` drops that runtime's managed Postgres database, `--state` removes that runtime's internal `.scenery/sessions/<id>` state root, and `--all` enables both. `--json` reports `scenery.down.v1` and still includes `session_id` for state compatibility.
- `scenery prune --older-than <duration>` prunes old agent sessions whose recorded owner is gone or mismatched and removes their `.scenery/sessions/<id>` state roots. It accepts Go durations such as `336h` plus day shorthand such as `14d`. It does not drop managed databases or delete VictoriaLogs storage; use `scenery down --db` or `scenery db drop` for destructive database cleanup.
- Starting `scenery up` for an app root requires exclusive ownership of that app root's live dev runtime. If another live owner already controls the same app root, startup fails with an "already running" error that points to `scenery down --app-root <path>` and Git worktrees. If the recorded owner is dead or its fingerprint no longer matches, the new owner may claim the runtime and clean recorded app, worker, Electric, and managed frontend child processes from the stale owner, plus Scenery-owned runtime processes whose injected app root/internal session environment matches. It must not clean other app roots, other worktrees, or unrelated user processes.
- Session owner checks treat `owner_pid` as the effective owner. `owner.pid` is the fingerprint for that same PID, not an independent owner field. If the stored owner fingerprint object points at a different stale PID, Scenery refreshes it on the next registration and must not delete or prune the session while the effective `owner_pid` is still live. Dev supervisors unregister sessions with an owner-conditional delete that includes the recorded owner fingerprint; if an older owner exits after ownership moved, or if the same PID now has a different recorded fingerprint, the delete is ignored and the newer session record remains registered.
- `scenery help --json` returns `scenery.help.v1`, a machine-readable command manifest for agents and contract checks. Human root help is intentionally orienting and does not contain the full command grammar; use `scenery help all` for the grouped command reference and `scenery help <command>` for exact flags and subcommands.
- `scenery ps` renders a headed app-root table by default. `scenery ps --json` treats a `starting` or `running` runtime with a missing or dead effective owner as `stale`, and a live but fingerprint-mismatched owner, dead app PID, or configured custom route base domain whose routes point at a non-default internal router port as `degraded`. Duplicate `scenery up` startup prevention uses the recorded runtime owner and owner fingerprint, not shell command text. Status JSON includes `status_reason` when scenery rewrites the runtime status. Status JSON also includes the agent substrate registry as `substrates`; failed shared substrates expose `status`, `last_exit`, and `component_exits` with component, PID, started/exited timestamps, exit code or signal, error text, and stdout/stderr log paths.
- When the local agent is active, the agent starts the visible dashboard backend and exposes the dashboard through the console route from `route_namespace`, for example `https://console.<route-id>.<route_namespace.base_domain>/`. The old path-shaped `console.../s/<session_id>` form is not the canonical dashboard URL. The Unix-socket control API remains protected by filesystem permissions.
- The agent router serves HTTPS by default when used directly, but the preferred default-port HTTPS path is `scenery system edge`: browser DNS for `local.dev` is provided by `scenery system edge dns install` through managed dnsmasq and a macOS scoped resolver, browser HTTPS reaches the privileged loopback helper on `127.0.0.1:443`, the helper forwards raw TCP to user-owned Caddy on an unprivileged loopback port, and Caddy proxies to the agent router on internal HTTP. API and console routes are generated from the app-derived `route_namespace`, and router requests resolve by exact registered route-host lookup instead of parsing a fixed localhost suffix. Entries in `routes` are canonical. If an app explicitly configures `proxy.route_base_domain`, `scenery up` requires the edge for browser-facing routes under that domain: it checks DNS readiness for the configured domain, the privileged listener, Caddy's current upstream, the live agent router, and a portless HTTPS dashboard probe before returning a runtime. When this preflight fails, startup refuses to publish internal `:9440` router URLs as normal routes and reports component-level DNS, privileged listener, Caddy, and router status with `scenery system edge restart`, `scenery system edge status`, `scenery system edge install`, and `scenery system edge trust` fix commands. Direct router URLs remain internal/diagnostic only in that mode. Friendly app-derived hosts are optional alias leases exposed in a separate `aliases` map only for the live app root that owns the free alias; a second worktree keeps its canonical routes, does not steal the alias, and reports held aliases in `alias_conflicts`. Same-app-root duplicate runtimes are rejected before alias ownership comes into play. Stale alias leases are reclaimed only after owner fingerprint verification proves the old owner is gone or mismatched. Live alias leases transfer only through `scenery up --claim-aliases`. Alias routing, router TLS host validation, and the Caddy on-demand TLS ask endpoint use the same exact registry lookup as canonical routes. The edge ask endpoint is `GET /v1/tls/allow?domain=<host>` and returns success only for a registered route or alias whose runtime owner fingerprint still verifies. Caddy forwards `X-Scenery-Edge-Token`; the agent trusts incoming forwarded proto/port headers only when that token matches and the request comes from loopback. Agent health and state distinguish the internal `router_addr`, browser-facing `public_router_addr`, public `router_scheme`, `edge`, and edge DNS state. `scenery system edge status --json` reports dnsmasq and resolver readiness; DNS is ready when the current managed dnsmasq state is running, or when an installed resolver functionally resolves the managed wildcard domain to the expected loopback address even though dnsmasq is owned by another agent home. `scenery system agent --router-http` or `SCENERY_AGENT_ROUTER_TLS=0` explicitly keeps the direct router on HTTP for local debugging. `scenery system agent --router-tls` and `SCENERY_AGENT_ROUTER_TLS=1` force direct HTTPS when an explicit setting is needed. `scenery system agent --trust` and `SCENERY_AGENT_TRUST=1` also enable direct router TLS and attempt to trust the existing scenery local CA. Trust installation failures are logged; the router still starts. Direct router TLS certificates are issued for `localhost` and registered route or alias hosts, not for arbitrary local names. Public HTTPS route URLs omit the port when the active public edge is on port `443`; non-default router ports stay explicit, and explicit occupied direct router addresses fail instead of silently falling back.
- Agent dev-runtime manifests always include a `dashboard` route for the global agent-owned dashboard. With the agent dashboard active, the manifest does not need a matching per-runtime `dashboard` backend; direct/per-runtime dashboard endpoints are kept for agent-disabled, unavailable-agent, or explicit local-proxy fallback paths.
- `scenery up` exposes local observability and Grafana capabilities for the dev runtime. The current substrate may start local VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana when their managed toolchain binaries are installed or can be downloaded. When the local agent is active, shared substrates are registered through one managed substrate lifecycle: owner fingerprint verification before reuse, service-specific reachability probing, stale-record deletion, ready/degraded/exited upserts, component exit monitoring, and structured dev events. Grafana is also registered as the runtime `grafana` backend, so manifests expose `https://grafana.<route-id>.<route_namespace.base_domain>:<agent-router-port>/` by default, or HTTP when the agent router is explicitly started with `--router-http` or `SCENERY_AGENT_ROUTER_TLS=0`. Dashboard runtime metadata is stored as compact, bounded JSON under the agent directory when the agent is active and `SCENERY_DEV_CACHE_DIR` is unset, so multiple worktrees for the same base app can appear in the global dashboard without report writes growing unbounded. These details are documented for intentional substrate debugging and are not the stable app-facing API.
- The local agent home defaults to `~/.scenery` unless `SCENERY_AGENT_HOME` is set. `SCENERY_DEV_CACHE_DIR` controls build and dashboard cache locations, not machine-wide agent identity.
- Managed frontend services start on runtime-private hidden loopback ports. A manual `SCENERY_FRONTEND_<NAME>_ADDR` override is accepted, but configured frontend upstreams are ignored unless that frontend sets `"allow_shared_upstream": true`.
- Dev app children are launched through an internal runtime executable path under `.scenery/sessions/<session_id>/run/app/` so stale same-runtime app processes can be identified without broad process-name matching.
- Managed Electric processes are runtime-owned children. They receive Scenery app-root, internal session, and runtime app identity in their environment and are recorded in the agent process map so a later owner can clean stale Electric processes for the same app-root/runtime without touching other worktrees. Before starting Electric, scenery checks live process command lines for the exact `ELECTRIC_REPLICATION_STREAM_ID=<runtime-stream-id>` stream. It terminates Scenery-owned same app-root/runtime Electric processes and fails fast with PID/state/stream/command diagnostics for any remaining process using that stream. Before starting Electric against managed Postgres, scenery tags Electric database connections with a deterministic Scenery `application_name`, checks advisory-lock or replication-slot backends for the exact `electric_slot_<runtime-stream-id>` slot, terminates only exact same-runtime Scenery-owned backends, and reports remaining contender PID/state/query/application/client/slot details.
- `scenery up --proxy`, `scenery up --trust`, and `SCENERY_LOCAL_PROXY=1 scenery up` are rejected from the normal dev path. Use default agent-routed app URLs, and run `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, and `scenery system edge trust` when trusted local HTTPS on the default port is needed. The legacy local proxy path remains blocked outside explicit legacy/debug code.
- `scenery up --port <n>` and `scenery up --listen <addr>` force a manual TCP app backend. The default agent path uses a runtime-private Unix socket and should be preferred for worktree-safe development.
- `scenery serve` builds once and starts the app runtime headlessly. It does not start the dashboard, local proxy, frontend proxy, or file watcher.
- `scenery serve` starts the generated binary with `SCENERY_ROLE=api`, so it serves HTTP APIs without registering worker-only workflow or activity handlers.
- `scenery task list|inspect|run|graph` is the canonical task surface. Plain targets resolve only to configured tasks from `.scenery.json`; `<domain>:<name>` targets resolve only to code tasks under `<app-root>/<domain>/tasks/...`. Configured task names containing `:` are rejected. Code task target segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`.
- Scenery task flags must appear before the target. Code task arguments must appear after `--`, for example `scenery task run --env production billing:reconcile -- --dry-run`. Configured tasks do not accept `--env`, `--lang`, or extra runtime arguments.
- Supported code task layouts are `<domain>/tasks/<name>.task.go`, `<domain>/tasks/<name>.task.ts`, `<domain>/tasks/<name>/main.go`, and `<domain>/tasks/<name>/index.ts`. Single-file Go tasks must start with `//go:build ignore` so normal app package loading cannot accidentally include them. If multiple candidates match a target, scenery fails unless `--lang go|typescript` selects a single language.
- Code tasks execute with cwd set to the app root. Go tasks use `go run`; TypeScript tasks prefer `bun` and fall back to `node --import tsx`. Task processes receive `SCENERY_APP_ID`, `SCENERY_APP_ROOT`, and `SCENERY_ENV`/`SCENERY_RUNTIME_ENV` when `--env` is set, with `.env` and `.env.local` loaded when present.
- `scenery inspect validation --json` is read-only and returns `scenery.inspect.validation.v1` with app metadata, default profile, profile records, advisory artifacts, and diagnostics.
- `scenery validate list|inspect|graph --json` returns `scenery.validation.list.v1`, `scenery.validation.inspect.v1`, and `scenery.validation.graph.v1`. `scenery validate <profile> --dry-run --json` returns `scenery.validation.plan.v1` and must not execute shell, task, code-task, harness, database, or generation steps.
- `scenery validate [<profile>] --json --write` runs the resolved profile sequentially, fails fast, keeps stdout as one JSON document, captures child output as bounded evidence tails and artifacts, returns `scenery.validation.result.v1`, and writes `.scenery/harness/validation/latest.json` plus `.scenery/harness/validation/<profile>-latest.json`.
- `scenery validate changed --base <ref>` computes `git diff --name-only <base>...HEAD`, includes the default profile, adds profiles whose `paths` globs match changed files, resolves nested `profile:` steps, deduplicates profiles, and reports selection reasoning in JSON.
- Cron declarations use Temporal Schedules when Temporal is enabled. `scenery serve` reconciles schedules from the API role, while `scenery worker` runs the cron workflow/activity worker on `scenery.<app>.cron.go`. Temporal cron executions derive their scenery request start/idempotency metadata from the workflow scheduled start time.
- `scenery worker` builds once and starts the app runtime in worker-only mode with no public HTTP server. In this beta implementation it runs cron and native Temporal workers; generated binaries use `SCENERY_ROLE=worker`.
- `scenery worker bindings` validates `.scenery/workers/*.json` manifests and writes language-specific activity starter files. Python manifests produce `scenery_worker.py`; TypeScript/JavaScript manifests produce `scenery_worker.ts`; unknown languages receive a normalized JSON binding file.
- `scenery worker deployment set-current`, `ramp`, and `drain` are the explicit operator commands for Temporal Worker Deployment routing changes in non-local environments. They use the app's Temporal connection settings, including TLS/API-key env vars.
- `scenery build` produces the deployable binary and remains the preferred deployment artifact path.
- `scenery harness ui --json` is an optional browser-backed dashboard check. It starts a temporary `scenery up` process unless `--dashboard-url` points at an existing dashboard, visits core dashboard routes, runs route-specific semantic journeys, checks stable `data-scenery-ui` markers, captures screenshots, writes compact DOM snapshots, and writes console/network artifacts under `.scenery/harness/ui/`.

Runtime safety:

- `scenery serve` and generated binaries do not expose dev/admin endpoints by default.
- Dev/admin endpoints such as `/__scenery/config`, `/platform.Stats`, and `/debug/pprof/*` are enabled only for the development child process launched by `scenery up` or when `SCENERY_DEV_ENDPOINTS=1` is set explicitly.
- Runtime CORS reflection is enabled in dev endpoint mode. Outside dev mode, CORS origins must be explicitly allowlisted with `SCENERY_CORS_ALLOW_ORIGINS`.
- Build workspaces skip local secret and machine artifacts such as `.env`, `.env.*`, `.git`, `.scenery`, `node_modules`, `.DS_Store`, `__MACOSX`, and `coverage`.

Local observability:

- The user-facing observability surface is `scenery logs`, `scenery logs query`, `scenery logs tail`, `scenery traces list --json`, `scenery metrics list --json`, `scenery metrics query`, `scenery metrics labels`, `scenery metrics series`, `scenery inspect observability --json`, the dashboard, and Grafana routes. The current backing substrate exports local observability to Victoria sidecars:
  - VictoriaMetrics: `/opentelemetry/v1/metrics`
  - VictoriaLogs: `/insert/opentelemetry/v1/logs`
  - VictoriaTraces: `/insert/opentelemetry/v1/traces`
- Dashboard trace reads and `scenery traces list|metrics --json` use scenery-managed observability data. Victoria is the current substrate when local sidecars are available.
- Victoria sidecars store data under `.scenery/victoria/` by default when running without the agent. With an active agent, shared Victoria state is stored under the agent directory and registered in the agent substrate registry; the dev supervisor reuses registered endpoints instead of owning per-worktree Victoria processes. Reuse requires verified owner fingerprints and reachable metrics/logs/traces listeners. Managed Victoria stdout and stderr are always written to stable substrate log files, and component exits update the substrate to `degraded` with `last_exit` and per-component exit metadata. Substrate exit events are exported to the structured dev log stream with component name, PID, exit code or signal, and log paths.
- `SCENERY_DEV_VICTORIA=0` disables Victoria sidecars. `SCENERY_DEV_VICTORIA_DOWNLOAD=0` disables automatic Victoria binary downloads. When enabled, missing Victoria binaries are downloaded into `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`.
- Victoria binary names, versions, ports, storage layout, download behavior, and Victoria query semantics are beta substrate details. They are documented so local development is debuggable, but they are hidden during ordinary app work and are not part of the stable v0 runtime contract.
- Grafana binds to loopback and stores generated config, provisioning, and plugin state under `.scenery/grafana/` when running without the agent; downloaded Grafana binaries live under `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`. With an active agent, shared Grafana state is stored under the agent directory and registered in the agent substrate registry; later dev runtimes reuse the verified shared Grafana and expose a runtime route such as `grafana.<route-id>.<route_namespace.base_domain>` that points at the shared upstream.
- Grafana controls are `SCENERY_DEV_GRAFANA=auto|1|0`, `SCENERY_DEV_GRAFANA_DOWNLOAD=1|0`, `SCENERY_GRAFANA_BIN`, `SCENERY_GRAFANA_VERSION`, `SCENERY_GRAFANA_PORT`, `SCENERY_GRAFANA_DIR`, `SCENERY_GRAFANA_PUBLIC_URL`, `SCENERY_GRAFANA_REUSE_EXTERNAL`, `SCENERY_GRAFANA_PRESERVE_GF_ENV`, `SCENERY_GRAFANA_DOWNLOAD_URL`, `SCENERY_GRAFANA_DOWNLOAD_SHA256`, and `SCENERY_GRAFANA_PLUGINS_PREINSTALL_SYNC`.
- Default Caddy, Grafana, Grafana plugin, Victoria sidecar, Temporal CLI, and managed image versions are pinned in `scenery.toolchain.json`; environment variables override explicit startup controls for local testing where documented. Caddy edge is managed-toolchain only.
- The managed Postgres image ref is declared in `scenery.toolchain.json` under the `postgres` image artifact. It is visible through `scenery system toolchain list|verify --images --json`; strict image verification uses digests only for refs that declare them.
- Grafana provisioning uses datasource UIDs `scenery-victoriametrics`, `scenery-victorialogs`, and `scenery-victoriatraces-jaeger`, plus dashboard UIDs `scenery-dev-overview`, `scenery-dev-logs`, and `scenery-dev-endpoint`.
- Missing Grafana does not stop app startup in `auto` mode. `SCENERY_DEV_GRAFANA=1` makes Grafana startup required. Grafana is marked usable only after the server, expected datasources, and expected dashboards are verified. External Grafana reuse requires `SCENERY_GRAFANA_REUSE_EXTERNAL=1`.
- Agent sessions inject `SCENERY_SESSION_ID`, `SCENERY_BASE_APP_ID`, `SCENERY_RUNTIME_APP_ID`, `SCENERY_APP_ROOT_HASH`, `SCENERY_BRANCH`, and `SCENERY_WORKTREE` into the app process. Local development reports carry that identity and the reporter PID into stored trace summaries/events and log events.
- Dev report endpoints reject missing-session, stale-session, and invalid-token reports before trace/store work. Rejections are recorded as structured warning log events with `kind=dev-report-rejected`, and app-side report clients back off after repeated deadline/unauthorized/stale-report failures so old processes cannot hot-loop the dashboard.
- The emitted VictoriaMetrics request duration contract is `scenery_request_duration_seconds` with labels `scenery_app`, `scenery_trace_type`, `scenery_is_root`, `scenery_is_error`, `scenery_service`, optional `scenery_session_id`, optional `scenery_app_root_hash`, optional `scenery_branch`, optional `scenery_worktree`, optional `scenery_endpoint`, and optional `scenery_message_id`.
- The emitted VictoriaTraces and VictoriaLogs attribute contract includes `scenery.application_id`, optional `scenery.session_id`, optional `scenery.app_root_hash`, optional `scenery.branch`, and optional `scenery.worktree`.
- `scenery up` writes local ignore markers under `.scenery/` and the Grafana/Victoria state roots so downloaded binaries, local databases, logs, generated build outputs, and other machine-local state are not accidentally committed by target apps.

Secrets and environment:

- The human env-var reference is [Environment Reference](environment.md). The machine-readable env contract is [environment.registry.json](environment.registry.json), and `scenery harness self` fails on unregistered production env usage.
- Do not add a new scenery-owned production env var as a convenience escape hatch. Prefer `.scenery.json`, explicit CLI flags, or checked-in manifests; if env is truly required, add a registry entry with rationale, docs, and tests in the same change.
- Process environment always wins over values loaded from local files.
- The stable runtime path reads `.env` from the app root for local secret population when a value is not already present in the process environment.
- Local startup requires `.env` to exist in the app root. If `.env` is missing, `scenery up`, local `scenery serve`, local `scenery task run`, and local `scenery worker` fail before serving or running with a clear error. `.env.local` is optional.
- `scenery up` passes local file values into the child process before Go package initialization so package-level declarations can read them through `os.Getenv`.
- `scenery up` loads `.env` first and `.env.local` second. `.env.local` overrides `.env` only for keys that are not already present in the parent process environment.
- Missing declared secrets warn in local development mode.
- `scenery serve --env production` can use process environment without a `.env` file, and fails before serving if any declared secret is missing.
- `.env`, `.env.*`, and secret-bearing local files are not copied into build workspaces.

Standard auth:

- Apps may enable the built-in standard auth module from `.scenery.json` instead of writing a `//scenery:authhandler`.
- Auth-protected app code can use `auth.UserID()`, `auth.Data()`, or `auth.CurrentAuthData()` from `scenery.sh/auth`.
- Access tokens are HMAC JWTs with required expiration and `tenant_id` claims.
- Standard auth tenant state is framework-owned and lives in `scenery_auth.tenants`; an app-local `tenants` service or table is only an app-domain concern.
- Refresh sessions are stored in PostgreSQL and rotate by hashing refresh tokens. The refresh cookie name defaults to `onlv_refresh` for ONLV compatibility and is configurable.
- Email delivery is a pluggable `auth.EmailSender`; the default sender is a no-op.
- `/users/dev-bootstrap` is local-only and can mint a development token without opening PostgreSQL.
- DB-backed auth endpoints require a database URL from `auth.database_url_env`, `DATABASE_URL`, or `SCENERY_AUTH_DATABASE_URL`.

Implemented `up --json` rules:

```text
scenery up --json
```

- output is JSONL
- each line conforms to `scenery.run.event.v1`
- human-readable console output is suppressed in this mode
- child stdout/stderr are emitted as structured `process.output` events instead of raw terminal writes

Implemented `check --json` rules:

```text
scenery check --json
```

- output is a single JSON document
- output conforms to `scenery.check.result.v1`
- success returns `ok: true` and an empty `diagnostics` array
- failure returns `ok: false` and structured diagnostics
- diagnostics may include `stage`, `file`, `line`, `column`, `severity`, `message`, and `suggested_action`

Implemented `harness --json` rules:

```text
scenery harness --json
scenery harness --json --write
```

- output is a single JSON document
- output conforms to `scenery.harness.result.v1`
- it composes `scenery check --json` and the stable `scenery inspect ... --json` surfaces
- success returns `ok: true`
- failure returns `ok: false`, per-step errors, diagnostics, and `next_actions`
- failed and expensive steps include `evidence` conforming to `scenery.harness.artifact.v1`
- `--write` persists the same result to `.scenery/harness/latest.json`
- `--write` persists large evidence payloads under `.scenery/harness/artifacts/<run-id>/`
- `--with-validation` and `--with-validation=<profile>` run app validation after the core harness and add a small `validation` pointer with `profile`, `ok`, and `result_path`; the validation result itself stays in `.scenery/harness/validation/latest.json`

Implemented `harness self` JSON rules:

```text
scenery harness self --summary
scenery harness self --json
scenery harness self --json=summary
scenery harness self --json=full
scenery harness self --summary --write
scenery harness self --json --write
```

- `--summary`, `--json`, and `--json=summary` output a single compact JSON document conforming to `scenery.harness.self.summary.v1`
- `--json=full` outputs the full archive JSON document conforming to `scenery.harness.self.v1`
- summary output is the agent-facing default and must reference artifacts instead of embedding full drift inventories, successful stdout/stderr tails, complete timing package lists, or full large-file lists
- green summary output should stay under 12 KB; failed summary output should stay under 32 KB while preserving the first actionable failure and artifact references
- it validates the scenery repo itself instead of a target app
- it runs docs knowledge validation, `scenery inspect docs --json`, architecture checks, UI static architecture checks, Go package tests, parallel dev-session safety, live Postgres branch lifecycle safety, dashboard UI typecheck/build, UI freshness checks, worktree-local `go build -o .scenery/harness/bin/scenery ./cmd/scenery`, and local binary freshness checks
- self-harness Go test steps use the Go test result cache by default. Pass `--fresh-tests` to add `-count=1` to the self-harness Go test commands when a fresh no-result-cache run is required.
- the default, race, and release self-harness modes start a managed Postgres dev cell under a temporary agent home, create two branch databases, check branch data isolation with `psql`, exercise reset, restore, diff, delete, and prune, and tear the temporary cell down. This proof requires Docker or local Postgres tooling plus `psql`. `--quick` intentionally skips it.
- agents must not run `go install ./cmd/scenery` unless a human explicitly requests updating the shared installed `scenery` binary; multiple worktrees may otherwise overwrite each other's CLI
- architecture checks fail on unapproved direct dependencies, forbidden framework imports, CLI package boundary violations, missing generated/vendored ignore markers, and non-generated source/code files over 2500 lines; Markdown docs are not subject to line-count size checks
- architecture checks warn on non-generated source/code files over 1000 lines, cgo imports, `.DS_Store` artifacts, and compatibility imports outside known migration paths; unchanged warnings outside the changed area are debt summary in compact output, not agent attention
- local harness/report artifacts matching `.scenery/**`, `coverage/**`, `test-results/**`, `*.harness*.json`, or `scenery-harness-self-*.json` are reported as ignored local artifacts and do not drive changed-area recommended commands
- UI static architecture checks fail on raw shadcn install scripts, non-`@scenery` registries, unsafe registry item source/target declarations, legacy `components/ui` imports, direct vendor shadcn imports from screens, and direct Radix/styling utility imports outside scenery primitives/layouts/vendor
- UI static architecture checks scan multiline imports, re-exports, dynamic imports, and CommonJS requires for forbidden UI boundary bypasses
- UI static architecture checks warn on long or advanced `className` literals and common expression forms such as `cn(...)`, template literals, and conditional literals outside scenery primitives/layouts/vendor while the dashboard is migrated into the stricter slot-layout model
- `scenery harness ui --json` is not part of the default self-harness path. It needs a local Chrome/Chromium-compatible browser and is intended for explicit dashboard route validation. The route journeys cover dashboard home app selector/status, API Explorer endpoint/form behavior, service catalog metadata, traces empty/table/detail behavior, DB list or unavailable states, cron status/empty states, and temporal/worker status cards.
- `--write` persists the full archive to `.scenery/harness/self-latest.json`, the compact summary to `.scenery/harness/self-summary-latest.json`, and topic artifacts such as `.scenery/harness/test-timing-latest.json`
- failed and expensive steps include `evidence` conforming to `scenery.harness.artifact.v1`; Go test JSONL evidence is written as `.scenery/harness/artifacts/<run-id>/go-test.jsonl` when `--write` is present
- `--write` refreshes `.scenery/harness/agent-context.json` as the one-file agent handoff. It includes current failing steps, first files to read, exact rerun commands, changed-area recommended commands, relevant active ExecPlans, recent failed harness artifacts, docs freshness, and risk classifications: `runtime`, `CLI contract`, `dashboard`, `schema`, `release`, and `onlv-impacting`.

Default agent loop:

```text
scenery doctor --json
scenery harness self --quick --summary --write
cat .scenery/harness/agent-context.json
# implement
scenery harness self --summary --write
```

Release-risk loop:

```text
scenery harness self --release --summary --write
scripts/release-gate.sh
```

Implemented `inspect harness` rules:

```text
scenery inspect harness --json
scenery inspect harness --json --app-root <path>
scenery inspect harness --json --repo-root <path>
scenery inspect harness artifact test-timing --json
scenery inspect harness diagnostics --severity warning --json
scenery inspect harness timing --top 10 --json
```

- manifest output conforms to `scenery.inspect.harness.v1`
- focused outputs use the same schema version and return bounded topic-specific JSON for artifacts, diagnostics, and timing
- from an app root, manifest output reports `.scenery/harness/latest.json`, `.scenery/harness/ui/latest.json`, and `.scenery/harness/artifacts/`
- from the scenery repo root, manifest output reports `.scenery/harness/self-latest.json`, `.scenery/harness/self-summary-latest.json`, `.scenery/harness/ui/latest.json`, and `.scenery/harness/artifacts/`
- focused artifact output reads known `.scenery/harness/*-latest.json` files by name (`self-harness`, `self-summary`, `toolchain`, `changed-area`, `drift`, `test-timing`, `fixture-matrix`, `schema-validation`, `agent-context`)
- diagnostics output caps returned diagnostics at 50 and supports `--severity error|warning`
- timing output reads `.scenery/harness/test-timing-latest.json`, sorts slow packages/tests by duration, and caps both lists with `--top`
- manifest output reads latest harness outputs when present and returns their normalized `artifacts` and `evidence` arrays
- evidence records use `scenery.harness.artifact.v1` and include `command`, `cwd`, `started_at`, `duration_ms`, `exit_code`, output tails, artifact references, and `repro_command`

Release gate:

```text
scripts/release-gate.sh
```

- this is the high-signal pre-release gate, not the normal inner-loop developer check
- it runs documentation/architecture checks, a parallel dev-session safety check, a real ONLV two-worktree smoke when an ONLV checkout is available, focused Go tests, dashboard UI typecheck/build, worktree-local binary freshness checks, and artifact hygiene checks
- release-gate logs and future ONLV gates should use the same `scenery.harness.artifact.v1` evidence shape for failed or expensive steps
- `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` may point at a read-only scenery app for the optional external app smoke
- `SCENERY_RELEASE_GATE_LOG_DIR` may override the log directory; otherwise logs are written under `.scenery/release-gate/`
- `SCENERY_ONLV_SMOKE_ROOT` may point at the ONLV checkout used by `scripts/onlv-two-worktree-smoke.sh`; otherwise the release gate uses `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` when set, then `/Users/petrbrazdil/Repos/onlv` when present. The smoke starts two temporary ONLV git worktrees under short `SCENERY_ONLV_SMOKE_TMPDIR` or `/tmp` paths with the current `SCENERY_BIN` and an isolated `SCENERY_AGENT_HOME` plus free loopback `SCENERY_AGENT_ROUTER_ADDR` and `TEMPORAL_ADDRESS`, refreshes Scenery-marked generated model schema files inside those disposable worktrees from the current generator output while refusing to overwrite handwritten schema files, expects edge DNS and the privileged edge helper to be installed, runs `scenery system edge install` for trusted HTTPS `127.0.0.1:443` routing, asserts session-scoped API, Pulse, Blog, Electric, Grafana, Temporal, and Console routes under the app's configured base domain without `.scenery.localhost`, `:9440`, or explicit HTTPS ports, checks managed database, Electric stream, Temporal queue, and alias exclusivity, then tears the app-root runtimes, edge, and worktrees down. The smoke uses managed dnsmasq and Caddy.
- artifact hygiene is intentionally strict and fails on local release artifacts such as `.DS_Store` and `__MACOSX`

Implemented `logs --jsonl` rules:

```text
scenery logs --jsonl
scenery logs --json
```

- `--json` is an alias for `--jsonl`
- output is JSONL
- each line conforms to `scenery.dev.event.v1`
- one JSON object is emitted per VictoriaLogs-backed structured dev event
- structured events include app id/root, session id, source id/kind/name/role/pid/stream/status, level, message, parsed fields, raw output, and parse metadata
- structured dev events are assigned a stable integer ID before export to VictoriaLogs
- human-readable raw output remains the default when neither flag is used

Implemented `traces clear --json` rules:
- output conforms to `scenery.traces.clear.v1`
- trace clearing is dev/admin beta for v0; its existence does not make cron, trace clearing, or queue deletion semantics stable

## Artifact Locations

### Current implemented locations

Use `scenery inspect paths --json` as the source of truth.

Today scenery uses:
- app config: `<app-root>/.scenery.json`
- cache root:
  - `$SCENERY_DEV_CACHE_DIR`, if set
  - otherwise OS user cache + `/scenery`
- build workspace: `<cache-root>/build/<sanitized-app-name>-<hash>`
- built app binary: `<workspace>/scenery-app`
- build state: `<workspace>/.scenery-build-state.json`

### Repo-Local Cache Locations

Implemented now:

```text
<app-root>/.scenery/
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
    validation/
      latest.json
      <profile>-latest.json
      artifacts/
        <run-id>/
    self-latest.json
```

Reserved for upcoming work:

```text
<app-root>/.scenery/
  state/
  logs/
```

Rules:
- Use `scenery inspect ... --json` for app, route, service, endpoint, wire, build, path, docs, generator, and Temporal metadata. Use `scenery traces list --json` and `scenery metrics list --json` for local observability metadata.
- Do not read `.scenery/gen/*` directly unless debugging scenery generation. These files are internal cache artifacts that may mirror inspect output today, but they are not the supported API.
- `wire/capabilities.json` is an internal cache for `scenery inspect wire --json` and the runtime `GET /_wire/capabilities` response.
- `models.json` and `views.json` are internal caches for `scenery inspect models --json` and `scenery inspect views --json`. Generated static-model schema, seed, and web package files under `.scenery/gen/db/` and `.scenery/gen/web/` are disposable generator outputs; integrate through `scenery generate data --dry-run --json`, `scenery inspect generators --json`, `scenery inspect models --json`, and `scenery inspect views --json` rather than reading cache files directly.
- `manifest.json` ties generated cache artifacts to schema versions, artifact paths, and deterministic content hashes for debugging generation.
- Use `scenery inspect build --json` for build metadata. `build/latest.json` is a local cache pointer to the latest prepared or compiled build workspace.
- Use `scenery harness --json` for framework app-model proof, `scenery validate <profile> --json` for app-owned quality gates, and `scenery harness self --summary` for scenery repo validation. `harness/latest.json`, `harness/validation/latest.json`, `harness/self-latest.json`, and `harness/self-summary-latest.json` are local snapshots written by `--write`; `--json=full` is the explicit full archive stdout mode.
- Future implementation should keep cache paths predictable for debugging, but external tools and agents should integrate through command JSON output.

## JSON Schemas

Implemented now:
- [scenery.inspect.app.v1.schema.json](schemas/scenery.inspect.app.v1.schema.json)
- [scenery.inspect.routes.v1.schema.json](schemas/scenery.inspect.routes.v1.schema.json)
- [scenery.inspect.services.v1.schema.json](schemas/scenery.inspect.services.v1.schema.json)
- [scenery.inspect.endpoints.v1.schema.json](schemas/scenery.inspect.endpoints.v1.schema.json)
- [scenery.inspect.models.v1.schema.json](schemas/scenery.inspect.models.v1.schema.json)
- [scenery.inspect.views.v1.schema.json](schemas/scenery.inspect.views.v1.schema.json)
- [scenery.inspect.traces.v1.schema.json](schemas/scenery.inspect.traces.v1.schema.json)
- [scenery.inspect.metrics.v1.schema.json](schemas/scenery.inspect.metrics.v1.schema.json)
- [scenery.inspect.observability.v1.schema.json](schemas/scenery.inspect.observability.v1.schema.json)
- [scenery.logs.query.v1.schema.json](schemas/scenery.logs.query.v1.schema.json)
- [scenery.logs.tail.entry.v1.schema.json](schemas/scenery.logs.tail.entry.v1.schema.json)
- [scenery.help.v1.schema.json](schemas/scenery.help.v1.schema.json)
- [scenery.down.v1.schema.json](schemas/scenery.down.v1.schema.json)
- [scenery.metrics.query.v1.schema.json](schemas/scenery.metrics.query.v1.schema.json)
- [scenery.metrics.labels.v1.schema.json](schemas/scenery.metrics.labels.v1.schema.json)
- [scenery.metrics.series.v1.schema.json](schemas/scenery.metrics.series.v1.schema.json)
- [scenery.inspect.docs.v1.schema.json](schemas/scenery.inspect.docs.v1.schema.json)
- [scenery.docs.index.v1.schema.json](schemas/scenery.docs.index.v1.schema.json)
- [scenery.wire.capabilities.v1.schema.json](schemas/scenery.wire.capabilities.v1.schema.json)
- [scenery.inspect.build.v1.schema.json](schemas/scenery.inspect.build.v1.schema.json)
- [scenery.inspect.paths.v1.schema.json](schemas/scenery.inspect.paths.v1.schema.json)
- [scenery.inspect.generators.v1.schema.json](schemas/scenery.inspect.generators.v1.schema.json)
- [scenery.inspect.temporal.v1.schema.json](schemas/scenery.inspect.temporal.v1.schema.json)
- [scenery.db.apply.result.v1.schema.json](schemas/scenery.db.apply.result.v1.schema.json)
- [scenery.db.seed.result.v1.schema.json](schemas/scenery.db.seed.result.v1.schema.json)
- [scenery.db.setup.result.v1.schema.json](schemas/scenery.db.setup.result.v1.schema.json)
- [scenery.task.list.v1.schema.json](schemas/scenery.task.list.v1.schema.json)
- [scenery.task.inspect.v1.schema.json](schemas/scenery.task.inspect.v1.schema.json)
- [scenery.task.graph.v1.schema.json](schemas/scenery.task.graph.v1.schema.json)
- [scenery.inspect.validation.v1.schema.json](schemas/scenery.inspect.validation.v1.schema.json)
- [scenery.validation.list.v1.schema.json](schemas/scenery.validation.list.v1.schema.json)
- [scenery.validation.inspect.v1.schema.json](schemas/scenery.validation.inspect.v1.schema.json)
- [scenery.validation.graph.v1.schema.json](schemas/scenery.validation.graph.v1.schema.json)
- [scenery.validation.plan.v1.schema.json](schemas/scenery.validation.plan.v1.schema.json)
- [scenery.validation.result.v1.schema.json](schemas/scenery.validation.result.v1.schema.json)
- [scenery.traces.clear.v1.schema.json](schemas/scenery.traces.clear.v1.schema.json)
- [scenery.worker.manifest.v1.schema.json](schemas/scenery.worker.manifest.v1.schema.json)
- [scenery.worker.manifest.v2.schema.json](schemas/scenery.worker.manifest.v2.schema.json)
- [scenery.gen.manifest.v1.schema.json](schemas/scenery.gen.manifest.v1.schema.json)
- [scenery.build.latest.v1.schema.json](schemas/scenery.build.latest.v1.schema.json)
- [scenery.run.event.v1.schema.json](schemas/scenery.run.event.v1.schema.json)
- [scenery.check.result.v1.schema.json](schemas/scenery.check.result.v1.schema.json)
- [scenery.harness.result.v1.schema.json](schemas/scenery.harness.result.v1.schema.json)
- [scenery.harness.self.v1.schema.json](schemas/scenery.harness.self.v1.schema.json)
- [scenery.harness.self.summary.v1.schema.json](schemas/scenery.harness.self.summary.v1.schema.json)
- [scenery.dev.event.v1.schema.json](schemas/scenery.dev.event.v1.schema.json)
- [scenery.logs.event.v1.schema.json](schemas/scenery.logs.event.v1.schema.json)
- [scenery.version.v1.schema.json](schemas/scenery.version.v1.schema.json)
- [scenery.doctor.result.v1.schema.json](schemas/scenery.doctor.result.v1.schema.json)
- [scenery.toolchain.v1.schema.json](schemas/scenery.toolchain.v1.schema.json)
- [scenery.toolchain.status.v1.schema.json](schemas/scenery.toolchain.status.v1.schema.json)

Schema rules:
- top-level schema field is `schema_version`
- schema names are versioned strings like `scenery.inspect.app.v1`
- additive fields are allowed in future versions only by introducing a new schema version when needed
- consumers should match on `schema_version`, not on command name alone

## Examples

### `scenery inspect app --json`

```json
{
  "schema_version": "scenery.inspect.app.v1",
  "app": {
    "name": "billing",
    "id": "billing-dev",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json",
    "module_path": "example.com/billing"
  },
  "config": {
    "name": "billing",
    "id": "billing-dev",
    "proxy": {
      "workspace": "billing",
      "api_host": "api.billing.localhost",
      "console_host": "console.billing.localhost",
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

### `scenery inspect build --json`

```json
{
  "schema_version": "scenery.inspect.build.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
  },
  "build": {
    "workspace_dir": "/cache/scenery/build/billing-abcdef0123456789",
    "binary_path": "/cache/scenery/build/billing-abcdef0123456789/scenery-app",
    "workspace_exists": true,
    "binary_exists": true,
    "build_state_path": "/cache/scenery/build/billing-abcdef0123456789/.scenery-build-state.json",
    "build_state_exists": true,
    "build_state_version": "3",
    "dependency_fingerprint": "abc123",
    "graph_fingerprint": "def456",
    "metadata_present": true,
    "api_encoding_present": true,
    "source_file_count": 24,
    "generated_file_count": 6
  }
}
```

### `scenery inspect endpoints --json`

```json
{
  "schema_version": "scenery.inspect.endpoints.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
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

Generated model CRUD endpoints include `"generated": true` and may have
`wire.available=false` until generated model wire contracts are implemented.
Handwritten endpoints omit the field.

### `scenery inspect wire --json`

`scenery inspect wire --json` returns the same hidden generated-client capability document served at `GET /_wire/capabilities`. It is intended for generated clients and agents that need to know whether the JSON transport or binary transport will be used for each logical endpoint.

### `scenery traces list --json`

Beta diagnostic subject. Use this when an agent needs concrete local traces
without scraping the dashboard UI. The JSON shape is versioned, but retention,
backend preference, span reconstruction, and clear semantics may change before
this is promoted to stable v0.

Example:

```text
scenery traces list --json --endpoint SyncGet --min-duration-ms 2000 --since 1h --slowest
```

Example output:

```json
{
  "schema_version": "scenery.inspect.traces.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
  },
  "query": {
    "app_id": "billing",
    "session_id": "feature-a-123abc",
    "limit": 100,
    "since": "1h0m0s",
    "endpoint": "SyncGet",
    "min_duration_ms": 2000,
    "sort": "duration_desc",
    "available_filters": ["--app-root", "--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
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

### `scenery metrics list --json`

Beta diagnostic subject. Use this when an agent needs a metrics-style rollup
over locally captured traces and logs. The JSON shape is versioned, but rollup
definitions, percentile calculations, default limits, and Victoria source
selection may change before this is promoted to stable v0.

Example:

```text
scenery metrics list --json --service sync --since 15m
```

Example output:

```json
{
  "schema_version": "scenery.inspect.metrics.v1",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
  },
  "query": {
    "app_id": "billing",
    "session_id": "feature-a-123abc",
    "limit": 10000,
    "since": "15m0s",
    "service": "sync",
    "sort": "started_at_desc",
    "available_filters": ["--app-root", "--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
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

### `scenery inspect observability --json`

Beta diagnostic subject. Use this before ad hoc observability queries when an
agent needs to know whether the local Victoria backends are reachable and which
scope will be enforced.

Example:

```text
scenery inspect observability --json
```

The response uses `scenery.inspect.observability.v1` and includes `scope`,
`backends.logs`, `backends.metrics`, `backends.traces`, examples, and optional
warnings. Raw backend URLs are exposed only under the optional `debug.base_urls`
object for intentional substrate debugging.

### `scenery logs query --json`

Beta query surface for scoped VictoriaLogs LogsQL. This is the preferred CLI
path for targeted log debugging when plain `scenery logs --jsonl` is too broad.

Example:

```text
scenery logs query --json --since 15m --limit 100 --query 'error OR panic'
```

The response uses `scenery.logs.query.v1`, echoes the selected scope and query
bounds, and returns normalized entries with `time`, `level`, `source`,
`message`, `fields`, `trace_id`, `span_id`, and `raw` where available. Passing
`--jsonl` writes only log entries as JSON Lines. `scenery logs tail --jsonl`
emits one `scenery.logs.tail.entry.v1` object per line and uses `--since` as the
VictoriaLogs live-tail `start_offset`.

### `scenery metrics query --json`

Beta query surface for scoped PromQL/MetricsQL. Range queries are the default;
`--instant` uses the instant query endpoint.

Example:

```text
scenery metrics query --json --since 15m --step 5s --promql 'max_over_time(scenery_request_duration_seconds[15m])'
```

The response uses `scenery.metrics.query.v1`, echoes scope and bounds, reports
the backend `result_type`, and returns normalized metric series and samples.
`scenery metrics labels --json --since 1h --match 'scenery_request_duration_seconds'` emits `scenery.metrics.labels.v1`.
`scenery metrics series --json --match 'scenery_request_duration_seconds'` emits
`scenery.metrics.series.v1`.

### `scenery inspect docs --json`

Use this when an agent needs to understand the repo knowledge base before making changes.

Source files:

- [docs/index.md](index.md)
- [docs/knowledge.json](knowledge.json)
- [docs/plans/active.md](plans/active.md)
- [docs/plans/completed.md](plans/completed.md)
- [docs/tech-debt.md](tech-debt.md)

Example:

```text
scenery inspect docs --json
```

Example output:

```json
{
  "schema_version": "scenery.inspect.docs.v1",
  "repo": {
    "root": "/repo/scenery",
    "module_path": "scenery.sh",
    "go_mod_path": "/repo/scenery/go.mod"
  },
  "summary": {
    "document_count": 9,
    "missing_count": 0,
    "review_due_count": 0,
    "stale_count": 0,
    "agent_scope_count": 1,
    "stale_child_index_entry_count": 0,
    "missing_child_index_entry_count": 0,
    "quality": {
      "A": 4,
      "B": 5
    }
  },
  "agents": {
    "scopes": [
      {
        "path": "AGENTS.md",
        "scope": "."
      }
    ],
    "child_index_path": "AGENTS.md#child-agent-index",
    "child_index_entries": [],
    "stale_child_index_entries": [],
    "missing_child_index_entries": []
  },
  "documents": [
    {
      "path": "docs/local-contract.md",
      "title": "scenery Local Contract",
      "owner": "scenery runtime",
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

The `agents` object reports every discovered `AGENTS.md` scope, compares child
scopes against the root `AGENTS.md` Child Agent Index, and reports stale index
entries plus discovered child scopes that are missing from the index.

### `scenery inspect harness --json`

Use this when an agent needs the latest harness evidence without parsing
terminal output.

Source files:

- `.scenery/harness/latest.json`
- `.scenery/harness/self-latest.json`
- `.scenery/harness/self-summary-latest.json`
- `.scenery/harness/ui/latest.json`
- `.scenery/harness/ui/screenshots/*.png`
- `.scenery/harness/ui/dom/*.json`
- `.scenery/harness/ui/console.jsonl`
- `.scenery/harness/ui/network.jsonl`
- `.scenery/harness/artifacts/`

Example:

```text
scenery inspect harness --json
scenery inspect harness artifact test-timing --json
scenery inspect harness diagnostics --severity warning --json
scenery inspect harness timing --top 10 --json
```

Example output:

```json
{
  "schema_version": "scenery.inspect.harness.v1",
  "scope": "repo",
  "root": "/repo/scenery",
  "latest": [
    {
      "name": "self-harness",
      "path": ".scenery/harness/self-latest.json",
      "schema_version": "scenery.harness.self.v1",
      "exists": true
    }
  ],
  "evidence": [
    {
      "schema_version": "scenery.harness.artifact.v1",
      "command": ["go", "test", "-json", "./..."],
      "cwd": "/repo/scenery",
      "started_at": "2026-06-07T20:45:00Z",
      "duration_ms": 1234,
      "exit_code": 1,
      "stdout_tail": "{\"Action\":\"fail\"}",
      "artifacts": [
        {
          "name": "go-tests-stdout",
          "path": ".scenery/harness/artifacts/20260607T204500Z/go-test.jsonl",
          "schema_version": "go.test.jsonl"
        }
      ],
      "repro_command": "cd /repo/scenery && go test -json ./..."
    }
  ]
}
```
