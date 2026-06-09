# onlava Local Contract

This document freezes the local developer and agent-facing contract for onlava v0.

The goal is to make onlava deterministic and inspectable:
- app shape is explicit
- CLI grammar is explicit
- machine-readable JSON outputs have versioned schemas
- inspect commands are the API; generated files are cache
- app sessions and capabilities are the user-facing model; substrate paths, ports, and backing services are debug details

If implementation and this document disagree, treat that as a bug.

## Status

Implemented now. This list describes what the CLI can do today; it is not the
same as the stable v0 support surface.

- `.onlava.json`
- `onlava up --json`
- `onlava serve`
- `onlava worker`
- `onlava version --json`
- `onlava help --json`
- `onlava system toolchain list|sync|verify|path`
- `onlava doctor --json`
- `onlava check --json`
- `onlava generate`
- `onlava generate client`
- `onlava generate sqlc`
- `onlava db psql`
- `onlava db apply`
- `onlava db seed`
- `onlava db setup`
- `onlava db reset`
- `onlava db drop`
- `onlava db snapshot create|restore`
- `onlava db branch status|list|checkout|reset|delete|restore|diff|expire|prune`
- `onlava db neon install|start|status|logs|stop|restart|uninstall`
- `onlava worktree create|list|remove`
- `onlava task list|inspect|run|graph`
- `onlava task run <name>`
- `onlava task run <domain>:<name>`
- `onlava validate list|inspect|graph|changed`
- `onlava validate <profile> --json`
- `onlava harness --json`
- `onlava harness self --json`
- `onlava harness ui --json`
- `onlava traces clear --json`
- `onlava inspect app --json`
- `onlava inspect routes --json`
- `onlava inspect services --json`
- `onlava inspect endpoints --json`
- `onlava inspect wire --json`
- `onlava inspect build --json`
- `onlava inspect paths --json`
- `onlava inspect generators --json`
- `onlava inspect temporal --json`
- `onlava inspect validation --json`
- `onlava traces list --json`
- `onlava metrics list --json`
- `onlava inspect docs --json`
- `onlava logs --jsonl`

Reserved by contract, implementation pending:
- repo-local runtime and state manifests beyond the command JSON surfaces above

Stable v0 surface:
- `.onlava.json`
- `onlava serve`
- `onlava build`
- `onlava version --json`
- `onlava help --json`
- `onlava check --json`
- `onlava inspect app|routes|services|endpoints|wire|build|paths|docs --json`
- `onlava logs --jsonl`
- `onlava test`
- `onlava generate client`
- typed/raw HTTP endpoints
- auth handler
- service struct initialization and shutdown
- private/internal calls
- secrets from process env and local `.env`
- basic runtime logs and trace emission

Dev-only or beta surface:
- `onlava up`
- `onlava db psql`
- `onlava db apply`
- `onlava db seed`
- `onlava db setup`
- `onlava db reset`
- `onlava db drop`
- `onlava db snapshot create|restore`
- `onlava db branch status|list|checkout|reset|delete|restore|diff|expire|prune`
- `onlava db neon install|start|status|logs|stop|restart|uninstall`
- `onlava worktree create|list|remove`
- `onlava generate`
- `onlava task list|inspect|run|graph`
- `onlava task run <name>`
- `onlava task run <domain>:<name>`
- `onlava validate`
- `onlava inspect validation --json`
- `onlava traces list|metrics --json`
- `onlava inspect generators --json`
- `onlava inspect temporal --json`
- `onlava system toolchain list|sync|verify|path`
- `onlava doctor --json`
- `onlava system edge install|trust|status|restart|uninstall|dns|privileged --json`
- `onlava worker`
- `onlava traces clear --json`
- `onlava harness ui --json`
- dashboard and API Explorer
- local HTTPS edge and frontend routing
- trust-store installation
- local observability and Grafana capabilities, backed today by Victoria/Grafana substrate and managed binary downloads
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
      "db_filename": ".onlava/temporal/dev.db"
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
- `temporal` is optional and disabled by default. Onlava only starts or connects to Temporal when `temporal.enabled` is explicitly `true`; workflow/activity declarations, TypeScript worker settings, and local `auto_start` settings do not enable Temporal by themselves.
- Unknown fields are rejected. Runtime diagnostics include the config file path and JSON field path, for example `/repo/app/.onlava.json: unknown .onlava.json field "proxy.extra"`.
- The removed legacy proxy host key has no compatibility behavior. Remove it from app config; use dev session routes or `proxy.api_host`, `proxy.console_host`, and `proxy.frontends` for local routing.
- Agent session manifests include `route_namespace`, the app-derived local browser namespace used by routed session URLs. `route_namespace.workspace` comes from `proxy.workspace` when present and otherwise falls back to app identity only when no explicit route hosts are configured. `route_namespace.base_domain` defaults to `local.dev` and may be overridden with `proxy.route_base_domain`. `route_namespace.hosts` preserves explicit configured route hosts such as `api`, `console`, `temporal`, `grafana`, and configured frontend names; those hosts become session-scoped route aliases for that route rather than changing the generated base for every route.
- `proxy.frontends` is a map keyed by frontend name. Each frontend requires `host`; `root` defaults to `apps/<name>`; `upstream` is optional but ignored by agent dev unless that frontend also sets `allow_shared_upstream: true`. With an active agent, `onlava up` prefers to start supported Vite/Astro frontends on hidden loopback ports, inject routed API/Electric URLs and the session route host into their process environment, register those hidden ports as session backends, and expose `https://<frontend>.<session>.<route_namespace.base_domain>:<agent-router-port>/` by default. Managed Vite/Astro frontends receive the route host through Vite/Astro allowed-host controls so app configs do not need to hard-code session hosts. Managed frontend session routes serve the frontend shell for HTML SPA deep links, while `/__onlava/*`, `/api/*`, `/sync/*`, and concrete asset paths are not history-fallback routes. `ONLAVA_FRONTEND_<NAME>_ADDR` still overrides onlava-owned frontend startup for manual debugging.
- `dev.services` is a beta local-development config surface for onlava-owned substrates. Phase 5 accepts `postgres`, `neon`, and `electric` service declarations with `kind`, `mode`, `version`, `isolation`, `project`, `parent_branch`, `branch_policy`, `branch_name_template`, `ttl`, `database`, `role`, `database_url_env`, `image`, `route`, and string `env` values. The agent currently owns managed Postgres and Electric for this surface, while unsupported service kinds or isolation modes are rejected instead of silently falling back to target-app port orchestration.
- `onlava up` prepares declared local DB setup before the app process starts. When `.onlava.json` declares `database.apply` or service-local seed files are discovered, the supervisor runs the same split lifecycle as `onlava db setup`: apply first, then seed. It passes the same managed Postgres `DatabaseURL` env value that the app child receives, so setup targets the session database. Successful setup is fingerprinted from `database.apply` config and seed file hashes; ordinary rebuilds skip setup until those inputs change.
- `dev.setup` is an optional beta list of shell commands that `onlava up` runs from the app root after managed dev services and the DB setup lifecycle are prepared, but before the app process starts. Setup commands receive the same managed Postgres `DatabaseURL` env value as the app child, so target apps can keep existing app-local setup during migration.
- `generators.clients` is a beta lifecycle config for generated TypeScript clients. `kind` defaults to `typescript-client`, `lang` defaults to TypeScript, and `output` is required. `onlava generate client` uses these entries when no explicit `--output` is passed.
- Generated TypeScript clients expose `WithMeta` methods that include response headers, status, `Response`, and parsed `txid` metadata from `X-Txid`/`X-TXID`. Electric-backed write flows should treat the API response and later Electric observation as separate phases: an HTTP success with `X-Txid` means the mutation committed, while `observeAPIResponseTxid(...)` reports later observer failures as `SyncObservationError` with `kind: "sync_observation_failure"`, `mutation_committed: true`, app/session/API/Electric context, txid, and observer error details.
- `generators.sqlc` is a beta lifecycle config for SQLC generation. `provider` may be empty or `sqlc`; `config` defaults to `sqlc.yaml`; `dev_url` defaults to `docker://postgres/18/dev`. When a SQLC schema path follows `<pkg>/db/gen/schema.sql` and `<pkg>/db/schema.hcl` exists, `onlava generate sqlc` refreshes the generated schema SQL with `atlas schema inspect` before running `sqlc generate`. SQLC generation is a generated-source lifecycle and must not apply database schema or seed data.
- `database.apply` is a beta DB lifecycle escape hatch. Phase 1 supports only `provider: "exec"` with an explicit shell `command`, optional `cwd`, and string `env` overlay. The accepted split lifecycle moves database mutation to `onlava db apply`; SQLC refresh stays under `onlava generate sqlc`.
- Service-local `SERVICE/db/seed.sql` is initial data. It is not Atlas schema input, not SQLC input, and not a generated-source input. The accepted lifecycle applies seed data through `onlava db seed`; the first implementation fails closed on changed previously-applied seed files and obviously destructive seed SQL rather than adding force or reseed escape hatches.
- `tasks` is a beta thin repo-task layer. Each configured task can define either `run` or `steps`, plus optional `cwd` and string `env`. `run` uses the platform shell from the app root or task cwd. `steps` currently accepts `task:<name>`, `task:<domain>:<name>`, `check`, `test`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`.
- Code tasks are beta app-local targets under `<domain>/tasks/`. Targets use `<domain>:<name>`, and both segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`. `onlava task list`, `onlava task inspect`, and `onlava task run <domain>:<name> [-- task args...]` discover and execute them without requiring the app model to parse cleanly.
- `validation` is a beta app-owned quality-gate layer. It has `default` and `profiles`; each profile can define `description`, `cost` (`low`, `medium`, or `high`), `paths`, `steps`, string `env`, and advisory `artifacts`. Profile names use the configured-task name rule and cannot contain `:`.
- Validation profile steps are not shell. They accept `profile:<name>`, `task:<name>`, `task:<domain>:<name>`, `harness:core`, `harness:ui`, `harness`, `check`, `test`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`. Shell commands must live behind configured `tasks.<name>.run`.
- `dev.services.postgres` currently defaults to version `18` and `isolation: "database"`. Other isolation modes are rejected until implemented. With an active agent session, onlava creates or reuses a physical Postgres server substrate, verifies the recorded owner/reachability/version before reuse, and separately allocates a deterministic per-session database. The global Postgres substrate record contains physical-server metadata only: admin URL, version, isolation, data/socket directories, port, source, process owners, and exit metadata. It must not contain `session.<id>` database URLs or names. The session database lease is exposed through session/app env as `DatabaseURL`, `ONLAVA_MANAGED_DATABASE_URL`, and `ONLAVA_MANAGED_DATABASE_NAME`, even when local env files already contain stale database URLs. Managed app, setup, DB setup, and worker environments do not receive Onlava-injected `DATABASE_URL`; `ONLAVA_MANAGED_DATABASE_URL` remains available as tooling/debug metadata. The admin cluster comes from `ONLAVA_DEV_POSTGRES_ADMIN_URL`, a reusable agent Postgres substrate, Docker when available for the requested version, or local `initdb`/`postgres` binaries under the agent state directory. Managed local Postgres starts with logical replication settings so `dev.services.electric` can attach. `ONLAVA_DEV_POSTGRES_INITDB` and `ONLAVA_DEV_POSTGRES_BIN` can point at explicit local binaries. Set `ONLAVA_DEV_POSTGRES_EXTERNAL=1` to keep an explicit external `DatabaseURL` instead of using the managed session database; external mode requires `DatabaseURL` and ignores `DATABASE_URL` as an app database authority. Old substrate records with legacy `session.<id>` keys remain readable during adoption, but new writes omit those keys.
- `dev.services.postgres.kind: "neon"` is the first contract slice of the branch-isolated Neon provider. It accepts `mode: "self-hosted"`, `isolation: "branch"`, `project`, `parent_branch`, `branch_policy: "manual"|"worktree"|"session"`, `branch_name_template`, `ttl`, `database`, `role`, and `database_url_env`. Unsupported Neon modes or isolation values fail closed. Current commands can install generated local Neon dev-cell state files, inspect that state, inspect a worktree branch pin, resolve/write the local branch pin during `onlava up`, and consume a non-parent ready local lease endpoint. The provider name for the actual self-hosted Neon dev cell is `neon-selfhost`. `ONLAVA_DEV_NEON_SELFHOST_DRIVER` selects the actual self-hosted Neon branch driver executable and remains the highest-priority override. When that env var is unset, Onlava can resolve the managed `neon-selfhost-driver` path recorded in `cell.json`, then the installed toolchain artifact, before falling back to `ONLAVA_DEV_LOCAL_POSTGRES_BRANCH_DRIVER`. `ONLAVA_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` is a development fallback hook for local Postgres-shaped branch endpoints, not the Neon dev-cell backend. Until the provider records a non-parent ready lease with endpoint metadata, app-session startup with `kind: "neon"` fails explicitly after branch-pin resolution instead of silently falling back to database-isolated Postgres.
- Neon dev-cell state is machine-local and lives under `~/.onlava/agent/substrates/neon/` by default, or under `ONLAVA_AGENT_HOME/agent/substrates/neon/` when the agent home is overridden. `onlava db neon install --json` creates generated `cell.json`, `compose.generated.yml`, `pageserver_config/pageserver.toml`, `pageserver_config/identity.toml`, `compute_templates/`, `backend.json`, and branch-registry files, attempts to install the source-built `neon-selfhost-driver` toolchain artifact under the agent-home toolchain store, records the driver status in `cell.json.driver`, and reports `onlava.db.neon.status.v1`; it does not claim a ready database until runtime health checks prove one. `onlava db neon start --json` starts the generated Docker Compose project with `docker compose -f <compose.generated.yml> -p onlava-neon up -d`, then reports probed status. `onlava db neon stop --json` stops that generated project without removing local substrate files or branch leases. `onlava db neon status --json` reports provider, mode, status, generated file presence, managed driver status, backend branch/compute counts from `backend.json`, Docker availability, optional unstable image presence, labeled component/container status, reserved loopback debug ports, listener checks for running components, component log paths, and the required follow-up action without per-session connection URLs. The generated Compose project is the storage cell only: MinIO, bucket init, storage broker, pageserver, and three safekeepers. It does not include a static app compute container; branch compute endpoints are driver-owned and default to stable loopback ports allocated from `55440` upward. The initial reserved storage-cell ports are `55430` MinIO API, `55431` MinIO console, `55432` storage broker, `55434` pageserver HTTP, and `55435`-`55437` safekeeper HTTP listeners; presence in JSON or Compose does not mean a listener is running. A running component with an unhealthy Docker health state or closed reserved listener is reported as degraded, while a successful bucket-init container is reported as `completed`. `onlava db neon restart --json` restarts existing Onlava-owned Neon containers when Docker can see them, but it does not create missing containers. `onlava db neon uninstall --destroy-data` removes this local substrate state after tearing down Compose containers and any remaining Onlava-labeled Neon compute containers.
- The worktree-local Neon branch pin path is `<app-root>/.onlava/worktree-db.json` with `schema_version: "onlava.db.branch.v1"`. It records provider, project, parent branch, branch name/id, database, role, optional session/worktree identity, owner, and TTL; it must not contain database connection URLs. The global local lease registry lives at `~/.onlava/agent/substrates/neon/branches.json` or `ONLAVA_AGENT_HOME/agent/substrates/neon/branches.json` with `schema_version: "onlava.db.branch.registry.v1"`. A lease may include endpoint metadata (`host`, `port`, `database`, `role`, optional `sslmode`, optional `source`) only when its status is `ready`; raw connection URLs are not stored in the pin, registry, restore-point state, or branch status/list JSON.
- Neon restore-point metadata lives at `~/.onlava/agent/substrates/neon/restore-points.json` or `ONLAVA_AGENT_HOME/agent/substrates/neon/restore-points.json` with `schema_version: "onlava.db.neon.restore_points.v1"`. The file records branch id/name/project, database name, source, timestamp ref, and optional restored-from ref, not raw connection URLs.
- Only leases whose pin has `created_by: "onlava"` and `provider: "neon-selfhost"` are treated as Onlava-owned; foreign leases are hidden from branch list/status resolution, are not expired/pruned/deleted by Onlava cleanup, and block checkout when they match the requested project and branch. `onlava db branch status --json` reports `onlava.db.branch.status.v1` for the current app root and returns `status: "unpinned"` when the pin does not exist. It includes `connection` only for ready non-parent leases with endpoint metadata, and reports `backend_status: "protected"` for a ready parent-branch lease without exposing connection metadata. `onlava db branch list --json` reports Onlava-owned local lease pins and provider-normalized lease entries as `onlava.db.branch.list.v1`, including lease `status`, optional endpoint metadata, timestamps, and the registry path; protected parent leases report `status: "protected"` and suppress `endpoint` even if the registry lease is marked ready.
- `onlava db branch checkout <name> [--json]` writes or replaces the local pin using sanitized branch names and stable `br-local-*` IDs, keeps `.onlava/` ignored, and runs the branch-provider ensure boundary. Driver resolution order is explicit `ONLAVA_DEV_NEON_SELFHOST_DRIVER`, `cell.json.driver.path`, installed `neon-selfhost-driver` toolchain artifact, then `ONLAVA_DEV_LOCAL_POSTGRES_BRANCH_DRIVER`. Both self-host and fallback drivers receive `<driver> ensure --project <project> --parent-branch <parent> --branch <branch> --branch-id <branch_id> --database <database> --role <role> [--ttl <duration>] --json`; stdout must be JSON with `status` (`ready`, `pending`, `missing`, or `expired`), optional `message`, and for `ready` status an `endpoint` object with `host`, `port`, optional `database`, optional `role`, optional `sslmode`, and optional `source`. The current source-built `neon-selfhost-driver` command records backend branch metadata in `backend.json`, bootstraps pageserver tenant/timeline metadata when the generated storage cell is reachable, starts or reuses a branch compute container on the persisted loopback port when Docker and compute templates are available, returns public `pending` while the endpoint is not SQL-ready, and returns `ready` with redacted endpoint metadata only after `psql` verifies the Postgres endpoint and creates the requested database when it is missing. If no driver is configured, the provider only renews the local lease registry and reports pending/missing state from the generated dev cell. During `onlava up`, an existing pin wins and is ensured through the same provider boundary; `branch_policy: "manual"` requires a prior checkout; `branch_policy: "worktree"` derives the branch from `branch_name_template` using `{app}`, `{git_branch}`, `{worktree}`, and `{session}`; `branch_policy: "session"` defaults to `{app}/{session}` when no template is set.
- `onlava db branch delete <name> [--force]` removes matching pending/missing/expired Onlava-owned local leases after parent/current branch guards and removes the current worktree pin only when the deleted branch is current and `--force` is present; ready branch deletion requires the configured `neon-selfhost` or local-postgres-branch driver and invokes `<driver> delete ... --json` with the same branch arguments. The source-built `neon-selfhost-driver` delete path removes driver-owned backend metadata and removes the known compute container when Docker can do so safely. `onlava db branch reset --yes` and `onlava db branch restore --at <timestamp-or-lsn> --yes` validate required arguments, destructive intent, and parent/current branch guards, then delegate to `<driver> reset ... --json` or `<driver> restore ... --at <timestamp-or-lsn> --json` when a driver is configured; successful driver reset/restore records restore-point metadata and `restore --json` emits `onlava.db.branch.restore.v1`. The current source-built driver reset/restore paths preserve the public branch ID and persisted port, remove the old compute endpoint, create a replacement pageserver timeline from the parent branch or requested restore LSN/timestamp, and recreate branch compute on the persisted loopback port when Docker and compute templates are available. `onlava db branch diff <branch> [--json]` delegates to `<driver> diff ... --target <branch> --json` when configured and emits `onlava.db.branch.diff.v1`; without a driver reset/restore/diff still return explicit backend placeholders. `onlava db branch expire [<name>] --after <duration>` updates the local lease expiration, and `onlava db branch prune [--older-than <duration>]` removes expired non-current local leases for the current Neon project from the registry; neither command mutates backend Neon branches without a configured branch driver.
- `onlava up`, `onlava db psql`, DB setup, and Electric synthesize a process-local `DatabaseURL` only from a non-parent ready lease endpoint and fail explicitly for pending, missing, expired, protected, or endpoint-less leases.
- `dev.services.electric` supports explicit upstream routing with `ONLAVA_DEV_ELECTRIC_UPSTREAM`; when set, onlava registers the upstream as a hidden session backend and injects `ELECTRIC_URL`/`ONLAVA_ELECTRIC_URL` using the agent route. Without an explicit upstream, onlava starts a hidden per-session Electric process from `ONLAVA_DEV_ELECTRIC_BIN` or, when `dev.services.electric.image` is set and Docker is available, from that image. Electric uses the common managed process readiness and early-exit lifecycle, but remains session-scoped: it is registered as an agent session backend/process, not as a global Electric substrate row. Electric receives the managed Postgres session database URL when `dev.services.postgres` is declared. When declared Postgres is in `ONLAVA_DEV_POSTGRES_EXTERNAL=1` mode, Electric derives its private adapter URL from `DatabaseURL`; without declared Postgres it can still receive explicit `DatabaseURL`/`DATABASE_URL`. onlava also sets a deterministic session-scoped `ELECTRIC_REPLICATION_STREAM_ID` by default so multiple sessions can share one Postgres cluster without colliding on Electric publication or replication-slot names. Configured `dev.services.electric.env` values stay on the Electric process/container and are not injected into the app process; an explicit `ELECTRIC_REPLICATION_STREAM_ID` there overrides the onlava default.
- Standard auth uses the `github.com/pbrazdil/onlava/auth` top surface and stores DB-backed auth state in PostgreSQL schema `onlava_auth`.
- Standard auth owns its framework tenant tables, including `onlava_auth.tenants`. Apps do not need an app-local `tenants` service, package, or table for standard auth; app-local tenant services are product-domain APIs and schema only.
- Standard auth registers `/auth/signup/email`, `/auth/login/email`, `/auth/refresh`, `/auth/logout`, `/auth/me`, organization/invite/impersonation endpoints, Google OAuth raw endpoints, and local `/users/dev-bootstrap`.
- Standard auth endpoints appear in `onlava inspect routes|services|endpoints --json` and in generated TypeScript clients.
- `auth.auto_bootstrap_database` applies the first standard-auth schema bootstrap at runtime. It is useful for local fixtures; production deployments should manage schema changes deliberately.
- `temporal.address_env` defaults to `TEMPORAL_ADDRESS`; when that env var is unset, runtime defaults to `127.0.0.1:7233`.
- `temporal.namespace` defaults to `TEMPORAL_NAMESPACE` when that env var is set, otherwise `default`.
- `temporal.task_queue_prefix` defaults to `onlava.<app-name>` with unsafe task-queue characters normalized to dots. `ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX` overrides the effective runtime prefix; `onlava up` sets it to a session-scoped value when an agent session is active.
- `temporal.payload_codec` defaults to `onlava-json-v1` and is validated at runtime. This is the only supported payload profile for onlava-managed Go and external workers in this milestone.
- `temporal.api_key_env` defaults to `TEMPORAL_API_KEY`. When set, the runtime uses Temporal API-key credentials.
- `temporal.tls.enabled` enables TLS without requiring an API key. `temporal.tls.server_name_env`, `ca_cert_file_env`, `client_cert_file_env`, and `client_key_file_env` default to `TEMPORAL_TLS_SERVER_NAME`, `TEMPORAL_TLS_CA_CERT_FILE`, `TEMPORAL_TLS_CERT_FILE`, and `TEMPORAL_TLS_KEY_FILE`. Client certificate and key env vars must be set as a pair for mTLS.
- Temporal worker deployment metadata is runtime-owned: `deployment_name` defaults to the task-queue prefix normalized for Temporal Worker Deployment naming and can be overridden with `ONLAVA_TEMPORAL_DEPLOYMENT_NAME`; `worker_build_id` defaults to `dev` and can be set with `ONLAVA_BUILD_ID`.
- Temporal workers opt into Worker Deployment Versioning. `ONLAVA_TEMPORAL_VERSIONING_BEHAVIOR` accepts `pinned` or `auto_upgrade` and defaults to `pinned`.
- Temporal workers enable Go SDK host resource reporting by default using Temporal's `contrib/sysinfo` provider, so Worker heartbeats can include CPU and memory usage for Temporal Cloud worker health views. Set `ONLAVA_TEMPORAL_HOST_RESOURCE_REPORTING=0` to disable this provider.
- Local onlava-managed worker processes set their `worker_build_id` as the current Temporal Worker Deployment version on startup so schedules and new workflow executions have a versioned routing target. Non-local workers do not self-promote; operators must promote deployment versions explicitly.
- `temporal.local.auto_start` and `temporal.local.db_filename` are local development settings for supervised Temporal dev server work and are only active when `temporal.enabled` is true. With an active agent, the Temporal dev server is registered as a shared agent substrate and its local database state is stored under the agent directory; each dev session also registers a `temporal` route for the shared UI, while app workers receive session-scoped task queue prefixes. Explicit workflow/activity task queues are prefixed in active dev sessions too, so parallel worktrees do not poll or schedule onto each other's queues. Reuse of an agent-recorded Temporal substrate requires a verified owner fingerprint and a reachable Temporal listener before app workers start; stale ready records are discarded and replaced. Temporal stdout and stderr are always written to stable substrate log files and the agent registry records exit metadata when the managed process exits.
- `ONLAVA_TEMPORAL_TASK_QUEUE` overrides the generated Temporal task queue for worker processes. `onlava worker --task-queue <name>` and `onlava worker typescript --task-queue <name>` set it.
- TypeScript Temporal activity support is activity-only. onlava discovers `*.worker.ts` files, plus ordinary `.ts` files with `//onlava:worker`, and generates `.onlava/generated/temporal/typescript/{onlava.ts,registry.ts,worker.ts,manifest.json,tsconfig.json,package.json}`. Source files import `activity` from `onlava/worker` or `@onlava/temporal`; the generated `tsconfig.json` maps both names to the generated local API. Before launching the generated worker, `onlava up` and `onlava worker typescript` install the app-root `package.json` dependencies and the generated worker package dependencies with `bun install`, falling back to `npm install` when Bun is unavailable.
- Go workflows declare TypeScript activities with `temporal.NewExternalActivity` using matching input/output type parameters and call them through `temporal.ExecuteActivity`. `onlava check --json` validates matching TypeScript activity names, task queues, and type names before build/runtime.
- `temporal.typescript.enabled`, `runtime`, and `auto_start` configure the TypeScript worker path. `onlava worker typescript` generates and runs the hidden worker directly. When `temporal.enabled`, `temporal.typescript.enabled`, and `auto_start` are all true, `onlava up` validates Go-to-TypeScript contracts, regenerates the hidden worker runtime, and supervises the TypeScript worker alongside the Go app. The worker receives the supervised Temporal address/namespace, session-scoped task queue prefix, deployment name, build ID, and agent session identity environment. `runtime` accepts `bun` or `node`; when empty, onlava prefers `bun` and falls back to `node --import tsx`.
- Generated binaries accept `ONLAVA_ROLE=all|api|worker`. `onlava up` uses the default combined role. `onlava serve` uses `api`. `onlava worker` uses `worker`.
- Packages that declare `github.com/pbrazdil/onlava/temporal` workflows or activities with `temporal.NewWorkflow`, `temporal.NewActivity`, or `temporal.NewExternalActivity` are imported into the generated main so their declarations register at startup.
- `temporal.ActivityConfig.MaxConcurrency` maps to the Temporal worker's per-task-queue maximum concurrent activity executions. Use a dedicated task queue when different activities need different limits. `temporal.WithHeartbeatTimeout(duration)` sets the workflow activity heartbeat timeout without changing the stable `ActivityConfig` struct shape.
- Cron jobs can set `cron.JobConfig.OverlapPolicy`, `CatchupWindow`, `PauseOnFailure`, `ActivityStartToClose`, and `ActivityRetryPolicy`. When Temporal is enabled these map to Temporal Schedule overlap/catchup/pause policy and to the generated cron activity options. Defaults are overlap `skip`, catchup window `1m`, pause-on-failure `false`, and activity start-to-close `1h`.
- Optional multi-language worker manifests live under `.onlava/workers/*.json` and use `onlava.worker.manifest.v1` or `onlava.worker.manifest.v2`. They require `build_id` and `payload_codec: "onlava-json-v1"`. v2 manifests use queue-level registrations with `registration_hash` values so `onlava inspect temporal --json` can reject incompatible workers sharing a Temporal task queue.

## CLI Grammar

Current implemented grammar:

```text
onlava up [--port <n>] [--listen <addr>] [--app-root <path>] [--session <id>|--new-session] [--claim-aliases] [-v|--verbose] [--json] [--detach]
onlava logs --follow [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [--jsonl|--json]
onlava logs query [--app-root <path>] [--session current|<id>] --query <logsql> [--since <duration>] [--start <time>] [--end <time>] [--limit <n>] [--timeout <duration>] [--fields <csv>] [--json|--jsonl]
onlava logs tail [--app-root <path>] [--session current|<id>] --query <logsql> [--since <duration>] [--timeout <duration>] [--fields <csv>] [--jsonl]
onlava console [--app-root <path>] [--session current|<id>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]
onlava system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
onlava system edge install|trust|status|restart|uninstall|dns|privileged [--json]
onlava help <command>
onlava help all
onlava help --json
onlava ps [--json] [--app-root <path>] [--session <id>] [--watch]
onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all]
onlava prune --older-than <duration> [--app-root <path>] [--json]
onlava serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker bindings [--app-root <path>] [--out <dir>] [--json]
onlava worker typescript [--task-queue <name>[,<name>...]]... [--runtime bun|node] [--app-root <path>] [--generate-only]
onlava worker deployment set-current --build-id <id> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
onlava worker deployment ramp --build-id <id> --percentage <0-100> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]
onlava worker deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]
onlava version [--json]
onlava system toolchain list [--json] [--include-source-locks] [--images]
onlava system toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
onlava system toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
onlava system toolchain path [--json] --tool <name> [--platform <goos/goarch>]
onlava doctor [--app-root <path>] [--json]
onlava build [--app-root <path>] [-o <path>]
onlava check [--app-root <path>] [--json]
onlava db psql [--app-root <path>] [psql args...]
onlava db apply [--app-root <path>] [--json]
onlava db seed [--app-root <path>] [--dry-run] [--json]
onlava db setup [--app-root <path>] [--json]
onlava db reset [--app-root <path>]
onlava db drop [--app-root <path>]
onlava db snapshot create|restore <name> [--app-root <path>]
onlava db branch status|list [--app-root <path>] [--json]
onlava db branch checkout <name> [--app-root <path>] [--json]
onlava db branch reset [--app-root <path>] [--yes]
onlava db branch delete <name> [--app-root <path>] [--force]
onlava db branch restore --at <timestamp-or-lsn> [--app-root <path>] [--yes]
onlava db branch diff <branch> [--app-root <path>] [--json]
onlava db branch expire [<name>] --after <duration> [--app-root <path>] [--json]
onlava db branch prune [--older-than <duration>] [--app-root <path>] [--json]
onlava db neon install|start|status|logs|stop|restart|uninstall [--json]
onlava generate [--app-root <path>] [--dry-run] [--json]
onlava generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
onlava generate sqlc [--app-root <path>] [--dry-run] [--json]
onlava task list [--app-root <path>] [--json]
onlava task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
onlava task run <name> [--app-root <path>]
onlava task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
onlava task graph --json [--app-root <path>]
onlava validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]
onlava validate list [--app-root <path>] [--json]
onlava validate inspect <profile> [--app-root <path>] [--json]
onlava validate graph [<profile>] [--app-root <path>] --json
onlava validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]
onlava harness [--app-root <path>] [--json] [--write] [--with-validation[=<profile>]]
onlava harness self [--repo-root <path>] [--summary|--json|--json=summary|--json=full] [--write] [--quick|--race|--release] [--with-neon-selfhost]
onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|observability|validation --json [--app-root <path>]
onlava inspect docs --json [--repo-root <path>]
onlava inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]
onlava traces list --json [--session current|<id>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
onlava metrics list --json [--session current|<id>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
onlava metrics query --json [--app-root <path>] [--session current|<id>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]
onlava metrics labels --json [--app-root <path>] [--session current|<id>] [--match <selector>] [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
onlava metrics series --json [--app-root <path>] [--session current|<id>] --match <selector> [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
onlava traces clear --json [--app-root <path>]
onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria] [-f|--follow] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
```

Implemented beta/dev helper grammar:

```text
onlava db psql [--app-root <path>] [psql args...]
onlava db branch status|list [--app-root <path>] [--json]
onlava db branch checkout <name> [--app-root <path>] [--json]
onlava db branch reset [--app-root <path>] [--yes]
onlava db branch delete <name> [--app-root <path>] [--force]
onlava db branch restore --at <timestamp-or-lsn> [--app-root <path>] [--yes]
onlava db branch diff <branch> [--app-root <path>] [--json]
onlava db branch expire [<name>] --after <duration> [--app-root <path>] [--json]
onlava db branch prune [--older-than <duration>] [--app-root <path>] [--json]
onlava db neon install|start|status|logs|stop|restart|uninstall [--json]
onlava worktree create <name> [--from <branch>] [--app-root <path>] [--json]
onlava worktree list [--app-root <path>] [--json]
onlava worktree remove <name> [--app-root <path>] [--db] [--json]
```

`onlava db psql` connects to the managed session database when `dev.services.postgres` is configured and an agent session is active; otherwise it uses explicit local database configuration. With `dev.services.postgres.kind: "neon"`, `onlava db psql` reads `.onlava/worktree-db.json` and connects only when the Onlava-owned local lease is ready and contains endpoint metadata; it fails explicitly while the lease is pending, missing, expired, or endpoint-less. `onlava db snapshot create|restore` stores SQL files under the current session's `.onlava/sessions/<session>/db/snapshots/` directory. For regular managed Postgres it uses host `pg_dump` and `psql` against the session database. For self-hosted Neon it resolves the ready branch lease, uses `pg_dump` and `psql` from the branch compute container so client tools match the server version, excludes runtime publications/subscriptions, and restores by resetting the Neon branch before importing the SQL. `onlava db reset` and `onlava db drop` are only available for regular managed session databases. `onlava db apply` runs only an explicit `database.apply` provider and does not run seed files or SQLC generation.

`onlava db neon install --json` creates generated local Neon dev-cell state files under the agent home, attempts to install the source-built `neon-selfhost-driver`, records that driver status in `cell.json.driver`, and emits `onlava.db.neon.status.v1`; it does not itself claim a ready Neon database. `onlava db neon start --json` starts the generated Docker Compose project and then reports probed status; readiness still depends on Docker/container/listener health. `onlava db neon stop --json` stops that generated project without removing local state. `onlava db neon status --json` and `onlava db neon logs` inspect generated state; status also reports the managed driver, backend branch/compute counts from `backend.json`, Docker availability, local image presence, labeled container state, and reserved loopback listener health for running components when Docker is available. `onlava db neon restart --json` restarts existing Onlava-owned Neon containers reported by Docker and fails explicitly when none exist. `onlava db neon uninstall --destroy-data` stops the generated Compose project with volumes before removing local substrate state; when the cell state is corrupt or the compose file is missing, it removes Onlava-labeled Neon containers directly and keeps local state if teardown fails; after a successful Compose teardown it also removes remaining Onlava-labeled Neon compute containers. `onlava db branch status --json` and `onlava db branch list --json` inspect the current app root's `.onlava/worktree-db.json` pin and the global local `branches.json` lease registry, emitting `onlava.db.branch.status.v1` and `onlava.db.branch.list.v1`; `backend_status` distinguishes local pin state from backend branch state and can report `pending`, `missing`, `expired`, `protected`, or `ready`. A pending branch status includes the generated dev-cell prerequisite state; a missing dev-cell is reported as `backend_status: "missing"` even when the local lease exists. Ready non-parent branch status includes redacted `connection` endpoint metadata and never a raw URL; protected parent branch status suppresses connection metadata. `onlava db branch checkout <name> --json` writes the local pin and runs the branch-provider ensure boundary; driver resolution is explicit `ONLAVA_DEV_NEON_SELFHOST_DRIVER`, `cell.json.driver.path`, installed `neon-selfhost-driver`, then `ONLAVA_DEV_LOCAL_POSTGRES_BRANCH_DRIVER` as the development fallback. Without a usable driver, checkout renews only the local lease registry. `onlava db branch delete` can remove pending local leases after parent/current guards and delegates ready branch deletion to the configured driver; `onlava db branch expire` and same-project `onlava db branch prune` update/prune only local lease metadata; reset/restore enforce safety checks and delegate to the configured driver. The current source-built driver handles reset/restore by replacing pageserver timelines and branch compute, delete as stateful `backend.json` plus compute removal, and schema diff for ready backend branches by running `pg_dump --schema-only --no-owner --no-privileges` against the current and target endpoints. `onlava down --db` for Neon configs removes the selected session's local branch lease when session metadata is present, or the current non-parent local branch lease for legacy/worktree pins; `onlava down --state` removes the app root's local `.onlava/worktree-db.json` pin after session state cleanup and does not remove the shared dev-cell state.

`onlava worktree create <name> --json` runs `git worktree add -b <name>` next to the current app root and emits `onlava.worktree.create.v1`. When the target app declares Neon, it also writes the target worktree's local `.onlava/worktree-db.json` branch pin, runs the branch-provider ensure boundary, and rolls back the Git worktree if pin creation or ensure fails. `onlava worktree list --json` emits `onlava.worktree.list.v1` from `git worktree list --porcelain`. `onlava worktree remove <name> --db --json` first resolves the target from `git worktree list --porcelain`, then removes local `.onlava` state before `git worktree remove`, and emits `onlava.worktree.remove.v1`; backend Neon branch deletion is handled by `onlava db branch delete` through the configured branch driver.

DB lifecycle split:
- `onlava db apply` mutates schema or app-owned database setup only. It does not run seed files or SQLC generation.
- `onlava db seed` applies service-local initial data such as `SERVICE/db/seed.sql` only. It runs after schema exists and does not participate in Atlas or SQLC generation. It records successful runs in `onlava_internal.seed_runs` keyed by app ID and seed path. Unchanged seeds are skipped; changed previously-applied seeds fail closed with status `changed`. Seed validation also fails closed before opening the database when SQL contains destructive setup patterns such as `DROP`, `TRUNCATE`, `DELETE FROM ...` without `WHERE`, `WHERE true`, or `WHERE 1 = 1`; diagnostics include the seed path, line, message, and statement context.
- `onlava db setup` runs `db apply`, then `db seed`. It reports both phases in JSON mode and stops before seed if apply fails.
- `onlava generate sqlc` remains the SQLC generated-source command. It may refresh generated schema SQL from schema definitions and run `sqlc generate`; it must not mutate a database or consume seed files.
- `onlava up` runs the setup lifecycle before starting the app when DB setup inputs exist, and reruns it on rebuild only when the `database.apply` config or discovered seed file hashes change. Setup failures are reported through the existing compile/setup failure path and dev event stream, and the previous successful fingerprint is not advanced so the next rebuild can retry.

Doctor rules:
- `onlava doctor` is a fast, read-only local environment diagnostic. It does not install tools, download managed artifacts, start services, run builds, connect to databases, or mutate `.onlava/`.
- `onlava doctor --json` emits `onlava.doctor.result.v1` and exits non-zero only when required checks have status `error`.
- Check statuses are `ok`, `warn`, `error`, and `skipped`. Check severities are `required`, `optional`, and `informational`.
- Required failures currently cover baseline host readiness such as missing/old Go, very low memory, very low disk space, or an explicitly invalid `--app-root`.
- Optional missing tools such as `bun`, `psql`, `pg_dump`, `docker`, `atlas`, `sqlc`, and `git` warn by default. App configuration can make their messages more specific, but the initial doctor contract does not make optional tools fatal.
- `--app-root` tunes app-sensitive diagnostics from `.onlava.json`. If omitted, doctor tries current-directory app discovery and silently continues with environment-only checks when no app is found.

Inspect rules:
- `onlava inspect` requires a subject.
- `onlava inspect` currently requires `--json`.
- `--app-root` is optional. When omitted, onlava walks upward from the current working directory to find `.onlava.json`.
- Stable inspect subjects for v0 are `app`, `routes`, `services`, `endpoints`, `wire`, `build`, `paths`, and `docs`.
- `generators`, `temporal`, `traces`, `metrics`, and `observability` are beta diagnostic subjects. `generators` reports configured generation graph inputs and outputs. `temporal` reports effective Temporal config and, when enabled, a short connectivity check. `traces`, `metrics`, and `observability` read onlava-managed local observability data. Victoria is the current backing substrate, not the integration API. If no local state exists, query/discovery commands return valid JSON with warnings and empty result sets where possible.
- `onlava inspect observability --json` emits `onlava.inspect.observability.v1` with backend readiness for logs, metrics, and traces; native dialect names; examples; and the exact enforced query scope for the selected app/session.
- The `onlava.inspect.traces.v1`, `onlava.inspect.metrics.v1`, `onlava.inspect.observability.v1`, `onlava.logs.query.v1`, `onlava.logs.tail.entry.v1`, `onlava.metrics.query.v1`, `onlava.metrics.labels.v1`, and `onlava.metrics.series.v1` schemas are useful for agents, but their source-selection, retention, rollup, percentile, and clear/delete semantics are not stable v0 API yet.
- `--since` accepts Go duration strings such as `15m`, `1h`, or `24h`.
- `--min-duration-ms` filters root traces by duration in milliseconds.
- `--status` accepts `ok` or `error`.
- `metrics` defaults to `--since 24h` and `--limit 10000` so agents get useful local summaries without scanning unbounded history.
- Explicit `--session <id>` values must resolve to an onlava agent session for the selected app root; use `--session current` for the current app session.
- `logs query` defaults to `--session current`, `--since 15m`, `--limit 200`, `--timeout 3s`, and JSON envelope output. `--limit` is capped at 2000 and reports a JSON warning when clamped. It accepts native VictoriaLogs LogsQL through `--query`; `--logql` is rejected rather than silently treating Loki LogQL as LogsQL. Finite queries use an HTTP context deadline derived from `--timeout`.
- `logs tail` streams scoped `onlava.logs.tail.entry.v1` JSONL log entries from the VictoriaLogs live-tail endpoint, maps `--since` to VictoriaLogs `start_offset`, rejects `--start` and `--end`, and exits through normal context cancellation or interrupt handling.
- `metrics query` defaults to range mode with `--session current`, `--since 15m`, `--step 5s`, `--timeout 3s`, `--limit 100`, and JSON output. `--limit` is capped at 10000 and reports a JSON warning when clamped. `--instant` switches to the instant Prometheus API endpoint. Finite queries use an HTTP context deadline derived from `--timeout`.
- `metrics labels` and `metrics series` default to `--session current`, `--since 1h`, `--timeout 3s`, and `--limit 1000`; catalog limits are capped at 10000 and report a JSON warning when clamped. `metrics labels` accepts optional `--match`, and `metrics series` requires `--match`.
- Query commands are scoped by default. Onlava applies LogsQL scope through VictoriaLogs `extra_filters` and metrics scope through repeated VictoriaMetrics `extra_label` query parameters, and every JSON envelope echoes `scope.enforced=true`.
- `docs` inspects the onlava repo knowledge base, not a target onlava app. It accepts `--repo-root` and otherwise walks upward to the `module github.com/pbrazdil/onlava` repo root.

Toolchain rules:
- `onlava.toolchain.json` is the root checked-in manifest for Onlava-owned development executables, Docker images, plugins, and source lock references.
- The manifest uses `onlava.toolchain.v1`; `onlava system toolchain ... --json` emits `onlava.toolchain.status.v1`.
- Binary artifacts may use `platforms` for downloaded archives or `source_build: {kind: "go", package: "./cmd/..."}` for source-built Onlava binaries. Source-built artifacts are compiled with `go build` into the managed toolchain store and report `source: "source-build"` in toolchain status.
- `onlava version --json` includes `toolchain_manifest.schema_version`, `sha256`, `artifact_count`, and `source_lock_count` for the bundled manifest.
- The default local store is `.onlava/toolchain/` under the app/repo root. Machine-level edge tools use `~/.onlava/toolchain/` under the local agent home. `ONLAVA_TOOLCHAIN_DIR` overrides both store roots.
- `ONLAVA_TOOLCHAIN_DOWNLOAD=0` disables automatic managed binary downloads. Per-tool download disable variables such as `ONLAVA_DEV_GRAFANA_DOWNLOAD=0` and `ONLAVA_DEV_VICTORIA_DOWNLOAD=0` still apply to their startup paths.
- Managed Caddy resolves from the managed store or manifest-driven download. Managed Grafana, Victoria, and Temporal CLI binaries resolve from explicit env overrides, the managed store, or manifest-driven download. They do not use implicit system `PATH` binaries.
- `onlava system toolchain verify --strict --images` fails for tag-only image refs. Tag-only image refs marked `stability: "unstable"` are accepted only outside strict verification during the migration to digest-pinned images.
- Go modules and UI package-manager files are source locks. Commands such as `go`, `bun`, `npm`, `node`, and `tsx` used to run source/package-manager workflows are not hidden Onlava-managed toolchain downloads.

Command split:

- `onlava up` starts the app session: app process, file watching, and rebuild/restart supervision.
- `onlava up --session <id>` registers the dev process under an explicit session ID. `onlava up --new-session` creates a fresh session ID for this run, even when the app root and branch already have a deterministic default session. These flags are mutually exclusive.
- `onlava up --detach` requires the local agent, starts the same dev supervisor in a background child process, waits for that child PID to register as the app root's agent session owner, prints the session URLs plus attach/stop commands, and returns. Detached child stdout/stderr from the supervisor is written under the agent directory; app process output continues to flow through the session-scoped dashboard log store.
- `onlava logs --follow` follows the current agent session's logs by default. It is equivalent to `onlava logs --session current --follow` with the same app-root, limit, stream, source, kind, level, grep, since, backend, and JSONL options, and it does not mutate session state.
- `onlava logs`, plain `onlava logs --follow`, and `onlava console` read structured dev events for the selected session. `--backend auto` and `--backend victoria` currently select the same Victoria-backed substrate path; use backend selection only when intentionally debugging that substrate. `ONLAVA_LOGS_BACKEND` accepts the same values and applies to the console as well.
- If the backing dev-event substrate is unavailable, structured dev-event read commands fail loudly instead of falling back to the deprecated local process-output cache.
- `onlava console` opens the source-aware terminal console when stdin/stdout are real TTYs. In CI, dumb terminals, or redirected output it falls back to normal log following with the same backend option.
- Structured dev logs carry source identity. Current source ids include `api`, `worker:typescript`, `build`, `supervisor`, `temporal`, `electric`, `grafana`, `victoria`, and `frontend:<name>`.
- `onlava system agent restart` stops the currently reachable local agent process, starts a new background agent, waits until the control socket is reachable, and returns. The same `--socket`, `--router-listen`, `--router-tls`, `--trust`, and `--json` options apply to the restarted agent.
- `onlava system edge dns install` resolves the managed `dnsmasq` toolchain artifact, syncing/building it automatically unless managed downloads are disabled, starts user-owned dnsmasq for the configured wildcard dev domain plus other Onlava-managed resolver domains already present on the machine, and on macOS invokes a privileged helper only when `/etc/resolver/<domain>` is missing or mismatched. `onlava system edge privileged install` installs the macOS root-owned loopback helper that listens on `127.0.0.1:443` and `[::1]:443` and forwards raw TCP only to a validated user-owned Caddy target recorded under the agent run directory. Run it as the normal user; it invokes `sudo` only for the minimal helper install. `onlava system edge privileged uninstall` removes that helper. `onlava system edge install` and `onlava system edge restart` refuse root, start user-owned Caddy on an unprivileged high loopback port, ensure the local agent router is running as an unprivileged HTTP upstream on its internal loopback address, disable Caddy response buffering for streaming routes such as Electric SSE while preserving upstream cache headers, and write both edge state and helper target metadata under the agent run directory. If wildcard DNS or the privileged helper is missing or unhealthy, install prepares Caddy but fails with the actionable setup command because browser-ready default-port HTTPS requires both. They resolve Caddy from the managed `caddy` toolchain artifact, syncing it automatically unless managed downloads are disabled. `onlava system edge trust` resolves the same managed Caddy artifact, starts a temporary admin-only Caddy process with `local_certs`, runs Caddy's trust flow against that temporary admin endpoint, and does not require the port-443 edge to be running. `onlava system edge status --json` reports `onlava.edge.status.v1`. `onlava system edge uninstall` stops user-owned Caddy, leaves DNS and the privileged helper alone, and reports `onlava system edge privileged uninstall` as the helper removal command.
- `onlava down` stops and unregisters the selected session but is non-destructive by default. `--db` drops that session's managed Postgres database, `--state` removes that session's `.onlava/sessions/<id>` state root, and `--all` enables both.
- `onlava prune --older-than <duration>` prunes old agent sessions whose recorded owner is gone or mismatched and removes their `.onlava/sessions/<id>` state roots. It accepts Go durations such as `336h` plus day shorthand such as `14d`. It does not drop managed databases or delete VictoriaLogs storage; use `onlava down --db` or `onlava db drop` for destructive database cleanup.
- Starting `onlava up` for an app-root/session requires exclusive ownership of that exact session id. If another live owner already controls the same app-root/session, startup fails with an "already running" error instead of superseding it; use a different `--session`, `--new-session`, or stop the existing session first. If the recorded owner is dead or its fingerprint no longer matches, the new owner may claim the session and clean recorded app, worker, Electric, and managed frontend child processes from the stale owner, plus Onlava-owned session processes whose injected app root/session environment matches. It must not clean other sessions, other app roots, or unrelated user processes.
- Session owner checks treat `owner_pid` as the effective owner. `owner.pid` is the fingerprint for that same PID, not an independent owner field. If the stored owner fingerprint object points at a different stale PID, Onlava refreshes it on the next registration and must not delete or prune the session while the effective `owner_pid` is still live. Dev supervisors unregister sessions with an owner-conditional delete that includes the recorded owner fingerprint; if an older owner exits after ownership moved, or if the same PID now has a different recorded fingerprint, the delete is ignored and the newer session record remains registered.
- `onlava help --json` returns `onlava.help.v1`, a machine-readable command manifest for agents and contract checks. Human root help is intentionally orienting and does not contain the full command grammar; use `onlava help all` for the grouped command reference and `onlava help <command>` for exact flags and subcommands.
- `onlava ps` renders a headed human table by default. `onlava ps --json` treats a `starting` or `running` session with a missing or dead effective owner as `stale`, and a live but fingerprint-mismatched owner, dead app PID, or configured custom route base domain whose session routes point at a non-default internal router port as `degraded`. Duplicate `onlava up` startup prevention uses the recorded session owner and owner fingerprint, not shell command text. Status JSON includes `status_reason` when onlava rewrites the session status. Status JSON also includes the agent substrate registry as `substrates`; failed shared substrates expose `status`, `last_exit`, and `component_exits` with component, PID, started/exited timestamps, exit code or signal, error text, and stdout/stderr log paths.
- When the local agent is active, the agent starts the visible dashboard backend and exposes the dashboard through the session-scoped console route from `route_namespace`, for example `https://console.<session_id>.<route_namespace.base_domain>/`. The old path-shaped `console.../s/<session_id>` form is not the canonical dashboard URL. The Unix-socket control API remains protected by filesystem permissions.
- The agent router serves HTTPS by default when used directly, but the preferred default-port HTTPS path is `onlava system edge`: browser DNS for `local.dev` is provided by `onlava system edge dns install` through managed dnsmasq and a macOS scoped resolver, browser HTTPS reaches the privileged loopback helper on `127.0.0.1:443`, the helper forwards raw TCP to user-owned Caddy on an unprivileged loopback port, and Caddy proxies to the agent router on internal HTTP. API and console session routes are generated from the app-derived `route_namespace`, and router requests resolve by exact registered route-host lookup instead of parsing a fixed localhost suffix. Session-scoped entries in `routes` are canonical. If an app explicitly configures `proxy.route_base_domain`, `onlava up` requires the edge for browser-facing routes under that domain: it checks DNS readiness for the configured domain, the privileged listener, Caddy's current upstream, the live agent router, and a portless HTTPS dashboard probe before returning a session. When this preflight fails, startup refuses to publish internal `:9440` router URLs as normal session routes and reports component-level DNS, privileged listener, Caddy, and router status with `onlava system edge restart`, `onlava system edge status`, `onlava system edge install`, and `onlava system edge trust` fix commands. Direct router URLs remain internal/diagnostic only in that mode. Friendly app-derived hosts are optional alias leases exposed in a separate `aliases` map only for the live session that owns the free alias; a second live session keeps its canonical routes, does not steal the alias, and reports the held aliases in `alias_conflicts`. Stale alias leases are reclaimed only after owner fingerprint verification proves the old owner is gone or mismatched. Live alias leases transfer only through `onlava up --claim-aliases`. Alias routing, router TLS host validation, and the Caddy on-demand TLS ask endpoint use the same exact registry lookup as canonical routes. The edge ask endpoint is `GET /v1/tls/allow?domain=<host>` and returns success only for a registered route or alias whose session owner fingerprint still verifies. Caddy forwards `X-Onlava-Edge-Token`; the agent trusts incoming forwarded proto/port headers only when that token matches and the request comes from loopback. Agent health and state distinguish the internal `router_addr`, browser-facing `public_router_addr`, public `router_scheme`, `edge`, and edge DNS state. `onlava system edge status --json` reports dnsmasq and resolver readiness. `onlava system agent --router-http` or `ONLAVA_AGENT_ROUTER_TLS=0` explicitly keeps the direct router on HTTP for local debugging. `onlava system agent --router-tls` and `ONLAVA_AGENT_ROUTER_TLS=1` force direct HTTPS when an explicit setting is needed. `onlava system agent --trust` and `ONLAVA_AGENT_TRUST=1` also enable direct router TLS and attempt to trust the existing onlava local CA. Trust installation failures are logged; the router still starts. Direct router TLS certificates are issued for `localhost` and registered route or alias hosts, not for arbitrary local names. Public HTTPS route URLs omit the port when the active public edge is on port `443`; non-default router ports stay explicit, and explicit occupied direct router addresses fail instead of silently falling back.
- Agent session manifests always include a `dashboard` route for the global agent-owned dashboard. With the agent dashboard active, the manifest does not need a matching per-session `dashboard` backend; direct/per-session dashboard endpoints are kept for agent-disabled, unavailable-agent, or explicit local-proxy fallback paths.
- `onlava up` exposes local observability and Grafana capabilities for the session. The current substrate may start local VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana when their managed toolchain binaries are installed or can be downloaded. When the local agent is active, shared substrates are registered through one managed substrate lifecycle: owner fingerprint verification before reuse, service-specific reachability probing, stale-record deletion, ready/degraded/exited upserts, component exit monitoring, and structured dev events. Grafana is also registered as the session `grafana` backend, so manifests expose `https://grafana.<session_id>.<route_namespace.base_domain>:<agent-router-port>/` by default, or HTTP when the agent router is explicitly started with `--router-http` or `ONLAVA_AGENT_ROUTER_TLS=0`. Dashboard session metadata is stored as compact, bounded JSON under the agent directory when the agent is active and `ONLAVA_DEV_CACHE_DIR` is unset, so multiple worktrees for the same base app can appear in the global dashboard without report writes growing unbounded. These details are documented for intentional substrate debugging and are not the stable app-facing API.
- The local agent home defaults to `~/.onlava` unless `ONLAVA_AGENT_HOME` is set. `ONLAVA_DEV_CACHE_DIR` controls build and dashboard cache locations, not machine-wide agent identity.
- Managed frontend services start on session-private hidden loopback ports. A manual `ONLAVA_FRONTEND_<NAME>_ADDR` override is accepted, but configured frontend upstreams are ignored unless that frontend sets `"allow_shared_upstream": true`.
- Dev app children are launched through a session-local executable path under `.onlava/sessions/<session_id>/run/app/` so stale same-session app processes can be identified without broad process-name matching.
- Managed Electric processes are session-owned children. They receive Onlava app-root, session, and runtime app identity in their environment and are recorded in the agent session process map so a later owner can clean stale Electric processes for the same app-root/session/runtime without touching other sessions. Before starting Electric, onlava checks live process command lines for the exact `ELECTRIC_REPLICATION_STREAM_ID=<session-stream-id>` stream. It terminates Onlava-owned same app-root/session/runtime Electric processes and fails fast with PID/state/stream/command diagnostics for any remaining process using that stream. Before starting Electric against managed Postgres, onlava tags Electric database connections with a deterministic Onlava `application_name`, checks advisory-lock or replication-slot backends for the exact `electric_slot_<session-stream-id>` slot, terminates only exact same-session Onlava-owned backends, and reports remaining contender PID/state/query/application/client/slot details.
- `onlava up --proxy`, `onlava up --trust`, and `ONLAVA_LOCAL_PROXY=1 onlava up` are rejected from the normal dev path. Use default agent-routed session URLs, and run `onlava system edge dns install`, `onlava system edge privileged install`, `onlava system edge install`, and `onlava system edge trust` when trusted local HTTPS on the default port is needed. The legacy local proxy path remains blocked outside explicit legacy/debug code.
- `onlava up --port <n>` and `onlava up --listen <addr>` force a manual TCP app backend. The default agent path uses a session-private Unix socket and should be preferred for worktree-safe development.
- `onlava serve` builds once and starts the app runtime headlessly. It does not start the dashboard, local proxy, frontend proxy, or file watcher.
- `onlava serve` starts the generated binary with `ONLAVA_ROLE=api`, so it serves HTTP APIs without registering worker-only workflow or activity handlers.
- `onlava task list|inspect|run|graph` is the canonical task surface. Plain targets resolve only to configured tasks from `.onlava.json`; `<domain>:<name>` targets resolve only to code tasks under `<app-root>/<domain>/tasks/...`. Configured task names containing `:` are rejected. Code task target segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`.
- Onlava task flags must appear before the target. Code task arguments must appear after `--`, for example `onlava task run --env production billing:reconcile -- --dry-run`. Configured tasks do not accept `--env`, `--lang`, or extra runtime arguments.
- Supported code task layouts are `<domain>/tasks/<name>.task.go`, `<domain>/tasks/<name>.task.ts`, `<domain>/tasks/<name>/main.go`, and `<domain>/tasks/<name>/index.ts`. Single-file Go tasks must start with `//go:build ignore` so normal app package loading cannot accidentally include them. If multiple candidates match a target, onlava fails unless `--lang go|typescript` selects a single language.
- Code tasks execute with cwd set to the app root. Go tasks use `go run`; TypeScript tasks prefer `bun` and fall back to `node --import tsx`. Task processes receive `ONLAVA_APP_ID`, `ONLAVA_APP_ROOT`, and `ONLAVA_ENV`/`ONLAVA_RUNTIME_ENV` when `--env` is set, with `.env` and `.env.local` loaded when present.
- `onlava inspect validation --json` is read-only and returns `onlava.inspect.validation.v1` with app metadata, default profile, profile records, advisory artifacts, and diagnostics.
- `onlava validate list|inspect|graph --json` returns `onlava.validation.list.v1`, `onlava.validation.inspect.v1`, and `onlava.validation.graph.v1`. `onlava validate <profile> --dry-run --json` returns `onlava.validation.plan.v1` and must not execute shell, task, code-task, harness, database, or generation steps.
- `onlava validate [<profile>] --json --write` runs the resolved profile sequentially, fails fast, keeps stdout as one JSON document, captures child output as bounded evidence tails and artifacts, returns `onlava.validation.result.v1`, and writes `.onlava/harness/validation/latest.json` plus `.onlava/harness/validation/<profile>-latest.json`.
- `onlava validate changed --base <ref>` computes `git diff --name-only <base>...HEAD`, includes the default profile, adds profiles whose `paths` globs match changed files, resolves nested `profile:` steps, deduplicates profiles, and reports selection reasoning in JSON.
- Cron declarations use Temporal Schedules when Temporal is enabled. `onlava serve` reconciles schedules from the API role, while `onlava worker` runs the cron workflow/activity worker on `onlava.<app>.cron.go`. Temporal cron executions derive their onlava request start/idempotency metadata from the workflow scheduled start time.
- `onlava worker` builds once and starts the app runtime in worker-only mode with no public HTTP server. In this beta implementation it runs cron and native Temporal workers; generated binaries use `ONLAVA_ROLE=worker`.
- `onlava worker bindings` validates `.onlava/workers/*.json` manifests and writes language-specific activity starter files. Python manifests produce `onlava_worker.py`; TypeScript/JavaScript manifests produce `onlava_worker.ts`; unknown languages receive a normalized JSON binding file.
- `onlava worker deployment set-current`, `ramp`, and `drain` are the explicit operator commands for Temporal Worker Deployment routing changes in non-local environments. They use the app's Temporal connection settings, including TLS/API-key env vars.
- `onlava build` produces the deployable binary and remains the preferred deployment artifact path.
- `onlava harness ui --json` is an optional browser-backed dashboard check. It starts a temporary `onlava up` process unless `--dashboard-url` points at an existing dashboard, visits core dashboard routes, runs route-specific semantic journeys, checks stable `data-onlava-ui` markers, captures screenshots, writes compact DOM snapshots, and writes console/network artifacts under `.onlava/harness/ui/`.

Runtime safety:

- `onlava serve` and generated binaries do not expose dev/admin endpoints by default.
- Dev/admin endpoints such as `/__onlava/config`, `/platform.Stats`, and `/debug/pprof/*` are enabled only for the development child process launched by `onlava up` or when `ONLAVA_DEV_ENDPOINTS=1` is set explicitly.
- Runtime CORS reflection is enabled in dev endpoint mode. Outside dev mode, CORS origins must be explicitly allowlisted with `ONLAVA_CORS_ALLOW_ORIGINS`.
- Build workspaces skip local secret and machine artifacts such as `.env`, `.env.*`, `.git`, `.onlava`, `node_modules`, `.DS_Store`, `__MACOSX`, and `coverage`.

Local observability:

- The user-facing observability surface is `onlava logs`, `onlava logs query`, `onlava logs tail`, `onlava traces list --json`, `onlava metrics list --json`, `onlava metrics query`, `onlava metrics labels`, `onlava metrics series`, `onlava inspect observability --json`, the dashboard, and Grafana routes. The current backing substrate exports local observability to Victoria sidecars:
  - VictoriaMetrics: `/opentelemetry/v1/metrics`
  - VictoriaLogs: `/insert/opentelemetry/v1/logs`
  - VictoriaTraces: `/insert/opentelemetry/v1/traces`
- Dashboard trace reads and `onlava traces list|metrics --json` use onlava-managed observability data. Victoria is the current substrate when local sidecars are available.
- Victoria sidecars store data under `.onlava/victoria/` by default when running without the agent. With an active agent, shared Victoria state is stored under the agent directory and registered in the agent substrate registry; the dev supervisor reuses registered endpoints instead of owning per-worktree Victoria processes. Reuse requires verified owner fingerprints and reachable metrics/logs/traces listeners. Managed Victoria stdout and stderr are always written to stable substrate log files, and component exits update the substrate to `degraded` with `last_exit` and per-component exit metadata. Substrate exit events are exported to the structured dev log stream with component name, PID, exit code or signal, and log paths.
- `ONLAVA_DEV_VICTORIA=0` disables Victoria sidecars. `ONLAVA_DEV_VICTORIA_DOWNLOAD=0` disables automatic Victoria binary downloads. When enabled, missing Victoria binaries are downloaded into `.onlava/toolchain/` or `ONLAVA_TOOLCHAIN_DIR`.
- Victoria binary names, versions, ports, storage layout, download behavior, and Victoria query semantics are beta substrate details. They are documented so local development is debuggable, but they are hidden during ordinary app work and are not part of the stable v0 runtime contract.
- Grafana binds to loopback and stores generated config, provisioning, and plugin state under `.onlava/grafana/` when running without the agent; downloaded Grafana binaries live under `.onlava/toolchain/` or `ONLAVA_TOOLCHAIN_DIR`. With an active agent, shared Grafana state is stored under the agent directory and registered in the agent substrate registry; later dev sessions reuse the verified shared Grafana and expose a per-session `grafana.<session>.<route_namespace.base_domain>` route that points at the shared upstream.
- Grafana controls are `ONLAVA_DEV_GRAFANA=auto|1|0`, `ONLAVA_DEV_GRAFANA_DOWNLOAD=1|0`, `ONLAVA_GRAFANA_BIN`, `ONLAVA_GRAFANA_VERSION`, `ONLAVA_GRAFANA_PORT`, `ONLAVA_GRAFANA_DIR`, `ONLAVA_GRAFANA_PUBLIC_URL`, `ONLAVA_GRAFANA_REUSE_EXTERNAL`, `ONLAVA_GRAFANA_PRESERVE_GF_ENV`, `ONLAVA_GRAFANA_DOWNLOAD_URL`, `ONLAVA_GRAFANA_DOWNLOAD_SHA256`, and `ONLAVA_GRAFANA_PLUGINS_PREINSTALL_SYNC`.
- Default Caddy, Grafana, Grafana plugin, Victoria sidecar, Temporal CLI, and managed image versions are pinned in `onlava.toolchain.json`; environment variables override explicit startup controls for local testing where documented. Caddy edge is managed-toolchain only.
- The source-built `neon-selfhost-driver` binary artifact and optional unstable Neon dev-cell image refs are declared in `onlava.toolchain.json`. Image refs include `ghcr.io/neondatabase/neon@sha256:7a4f124917bb929964b2d696d710f19584f80bb9bd51b2af4a6e2425434c761f`, `ghcr.io/neondatabase/compute-node-v16@sha256:b3e151661bd2ee11eb2843c8926001966cb23969227e9673c5f42fc3fbe14249`, `quay.io/minio/minio:RELEASE.2022-10-20T00-55-09Z`, and `minio/mc@sha256:a7fe349ef4bd8521fb8497f55c6042871b2ae640607cf99d9bede5e9bdf11727`. They are visible through `onlava system toolchain list|verify --images --json`; strict image verification uses the digest-pinned refs.
- Grafana provisioning uses datasource UIDs `onlava-victoriametrics`, `onlava-victorialogs`, and `onlava-victoriatraces-jaeger`, plus dashboard UIDs `onlava-dev-overview`, `onlava-dev-logs`, and `onlava-dev-endpoint`.
- Missing Grafana does not stop app startup in `auto` mode. `ONLAVA_DEV_GRAFANA=1` makes Grafana startup required. Grafana is marked usable only after the server, expected datasources, and expected dashboards are verified. External Grafana reuse requires `ONLAVA_GRAFANA_REUSE_EXTERNAL=1`.
- Agent sessions inject `ONLAVA_SESSION_ID`, `ONLAVA_BASE_APP_ID`, `ONLAVA_RUNTIME_APP_ID`, `ONLAVA_APP_ROOT_HASH`, `ONLAVA_BRANCH`, and `ONLAVA_WORKTREE` into the app process. Local development reports carry that identity and the reporter PID into stored trace summaries/events and log events.
- Dev report endpoints reject missing-session, stale-session, and invalid-token reports before trace/store work. Rejections are recorded as structured warning log events with `kind=dev-report-rejected`, and app-side report clients back off after repeated deadline/unauthorized/stale-report failures so old processes cannot hot-loop the dashboard.
- The emitted VictoriaMetrics request duration contract is `onlava_request_duration_seconds` with labels `onlava_app`, `onlava_trace_type`, `onlava_is_root`, `onlava_is_error`, `onlava_service`, optional `onlava_session_id`, optional `onlava_app_root_hash`, optional `onlava_branch`, optional `onlava_worktree`, optional `onlava_endpoint`, and optional `onlava_message_id`.
- The emitted VictoriaTraces and VictoriaLogs attribute contract includes `onlava.application_id`, optional `onlava.session_id`, optional `onlava.app_root_hash`, optional `onlava.branch`, and optional `onlava.worktree`.
- `onlava up` writes local ignore markers under `.onlava/` and the Grafana/Victoria state roots so downloaded binaries, local databases, logs, generated build outputs, and other machine-local state are not accidentally committed by target apps.

Secrets and environment:

- The human env-var reference is [Environment Reference](environment.md). The machine-readable env contract is [environment.registry.json](environment.registry.json), and `onlava harness self` fails on unregistered production env usage.
- Do not add a new onlava-owned production env var as a convenience escape hatch. Prefer `.onlava.json`, explicit CLI flags, or checked-in manifests; if env is truly required, add a registry entry with rationale, docs, and tests in the same change.
- Process environment always wins over values loaded from local files.
- The stable runtime path reads `.env` from the app root for local secret population when a value is not already present in the process environment.
- Local startup requires `.env` to exist in the app root. If `.env` is missing, `onlava up`, local `onlava serve`, local `onlava task run`, and local `onlava worker` fail before serving or running with a clear error. `.env.local` is optional.
- `onlava up` passes local file values into the child process before Go package initialization so package-level declarations can read them through `os.Getenv`.
- `onlava up` loads `.env` first and `.env.local` second. `.env.local` overrides `.env` only for keys that are not already present in the parent process environment.
- Missing declared secrets warn in local development mode.
- `onlava serve --env production` can use process environment without a `.env` file, and fails before serving if any declared secret is missing.
- `.env`, `.env.*`, and secret-bearing local files are not copied into build workspaces.

Standard auth:

- Apps may enable the built-in standard auth module from `.onlava.json` instead of writing a `//onlava:authhandler`.
- Auth-protected app code can use `auth.UserID()`, `auth.Data()`, or `auth.CurrentAuthData()` from `github.com/pbrazdil/onlava/auth`.
- Access tokens are HMAC JWTs with required expiration and `tenant_id` claims.
- Standard auth tenant state is framework-owned and lives in `onlava_auth.tenants`; an app-local `tenants` service or table is only an app-domain concern.
- Refresh sessions are stored in PostgreSQL and rotate by hashing refresh tokens. The refresh cookie name defaults to `onlv_refresh` for ONLV compatibility and is configurable.
- Email delivery is a pluggable `auth.EmailSender`; the default sender is a no-op.
- `/users/dev-bootstrap` is local-only and can mint a development token without opening PostgreSQL.
- DB-backed auth endpoints require a database URL from `auth.database_url_env`, `DATABASE_URL`, or `ONLAVA_AUTH_DATABASE_URL`.

Implemented `up --json` rules:

```text
onlava up --json
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
- failed and expensive steps include `evidence` conforming to `onlava.harness.artifact.v1`
- `--write` persists the same result to `.onlava/harness/latest.json`
- `--write` persists large evidence payloads under `.onlava/harness/artifacts/<run-id>/`
- `--with-validation` and `--with-validation=<profile>` run app validation after the core harness and add a small `validation` pointer with `profile`, `ok`, and `result_path`; the validation result itself stays in `.onlava/harness/validation/latest.json`

Implemented `harness self` JSON rules:

```text
onlava harness self --summary
onlava harness self --json
onlava harness self --json=summary
onlava harness self --json=full
onlava harness self --summary --write
onlava harness self --json --write --with-neon-selfhost
```

- `--summary`, `--json`, and `--json=summary` output a single compact JSON document conforming to `onlava.harness.self.summary.v1`
- `--json=full` outputs the full archive JSON document conforming to `onlava.harness.self.v1`
- summary output is the agent-facing default and must reference artifacts instead of embedding full drift inventories, successful stdout/stderr tails, complete timing package lists, or full large-file lists
- green summary output should stay under 12 KB; failed summary output should stay under 32 KB while preserving the first actionable failure and artifact references
- it validates the onlava repo itself instead of a target app
- it runs docs knowledge validation, `onlava inspect docs --json`, architecture checks, UI static architecture checks, Go package tests, parallel dev-session safety, local Neon generated dev-cell start/stop plus branch pin/lease lifecycle safety, dashboard UI typecheck/build, UI freshness checks, worktree-local `go build -o .onlava/harness/bin/onlava ./cmd/onlava`, and local binary freshness checks
- `--with-neon-selfhost` adds an explicit, slow, Docker-backed Neon proof. It installs and starts the generated self-hosted storage cell under a temporary agent home, creates two Git worktrees, requires managed `neon-selfhost-driver` branches to become SQL-ready, checks branch data isolation with `psql`, verifies Electric resolves the same ready branch URL, exercises reset, restore, schema diff, and delete against live branch computes, and tears the temporary cell down. It requires Docker and `psql`, cannot be combined with `--quick`, and is not part of the default deterministic self-harness.
- agents must not run `go install ./cmd/onlava` unless a human explicitly requests updating the shared installed `onlava` binary; multiple worktrees may otherwise overwrite each other's CLI
- architecture checks fail on unapproved direct dependencies, forbidden framework imports, CLI package boundary violations, missing generated/vendored ignore markers, and non-generated source/code files over 2500 lines; Markdown docs are not subject to line-count size checks
- architecture checks warn on non-generated source/code files over 1000 lines, cgo imports, `.DS_Store` artifacts, and compatibility imports outside known migration paths; unchanged warnings outside the changed area are debt summary in compact output, not agent attention
- local harness/report artifacts matching `.onlava/**`, `coverage/**`, `test-results/**`, `*.harness*.json`, or `onlava-harness-self-*.json` are reported as ignored local artifacts and do not drive changed-area recommended commands
- UI static architecture checks fail on raw shadcn install scripts, non-`@onlava` registries, unsafe registry item source/target declarations, legacy `components/ui` imports, direct vendor shadcn imports from screens, and direct Radix/styling utility imports outside onlava primitives/layouts/vendor
- UI static architecture checks scan multiline imports, re-exports, dynamic imports, and CommonJS requires for forbidden UI boundary bypasses
- UI static architecture checks warn on long or advanced `className` literals and common expression forms such as `cn(...)`, template literals, and conditional literals outside onlava primitives/layouts/vendor while the dashboard is migrated into the stricter slot-layout model
- `onlava harness ui --json` is not part of the default self-harness path. It needs a local Chrome/Chromium-compatible browser and is intended for explicit dashboard route validation. The route journeys cover dashboard home app selector/status, API Explorer endpoint/form behavior, service catalog metadata, traces empty/table/detail behavior, DB list or unavailable states, cron status/empty states, and temporal/worker status cards.
- `--write` persists the full archive to `.onlava/harness/self-latest.json`, the compact summary to `.onlava/harness/self-summary-latest.json`, and topic artifacts such as `.onlava/harness/test-timing-latest.json`
- failed and expensive steps include `evidence` conforming to `onlava.harness.artifact.v1`; Go test JSONL evidence is written as `.onlava/harness/artifacts/<run-id>/go-test.jsonl` when `--write` is present
- `--write` refreshes `.onlava/harness/agent-context.json` as the one-file agent handoff. It includes current failing steps, first files to read, exact rerun commands, changed-area recommended commands, relevant active ExecPlans, recent failed harness artifacts, docs freshness, and risk classifications: `runtime`, `CLI contract`, `dashboard`, `schema`, `release`, and `onlv-impacting`.

Default agent loop:

```text
onlava doctor --json
onlava harness self --quick --summary --write
cat .onlava/harness/agent-context.json
# implement
onlava harness self --summary --write
```

Release-risk loop:

```text
onlava harness self --release --summary --write
scripts/release-gate.sh
```

Implemented `inspect harness` rules:

```text
onlava inspect harness --json
onlava inspect harness --json --app-root <path>
onlava inspect harness --json --repo-root <path>
onlava inspect harness artifact test-timing --json
onlava inspect harness diagnostics --severity warning --json
onlava inspect harness timing --top 10 --json
```

- manifest output conforms to `onlava.inspect.harness.v1`
- focused outputs use the same schema version and return bounded topic-specific JSON for artifacts, diagnostics, and timing
- from an app root, manifest output reports `.onlava/harness/latest.json`, `.onlava/harness/ui/latest.json`, and `.onlava/harness/artifacts/`
- from the onlava repo root, manifest output reports `.onlava/harness/self-latest.json`, `.onlava/harness/self-summary-latest.json`, `.onlava/harness/ui/latest.json`, and `.onlava/harness/artifacts/`
- focused artifact output reads known `.onlava/harness/*-latest.json` files by name (`self-harness`, `self-summary`, `toolchain`, `changed-area`, `drift`, `test-timing`, `fixture-matrix`, `schema-validation`, `agent-context`)
- diagnostics output caps returned diagnostics at 50 and supports `--severity error|warning`
- timing output reads `.onlava/harness/test-timing-latest.json`, sorts slow packages/tests by duration, and caps both lists with `--top`
- manifest output reads latest harness outputs when present and returns their normalized `artifacts` and `evidence` arrays
- evidence records use `onlava.harness.artifact.v1` and include `command`, `cwd`, `started_at`, `duration_ms`, `exit_code`, output tails, artifact references, and `repro_command`

Release gate:

```text
scripts/release-gate.sh
```

- this is the high-signal pre-release gate, not the normal inner-loop developer check
- it runs documentation/architecture checks, a parallel dev-session safety check, a real ONLV two-worktree smoke when an ONLV checkout is available, focused Go tests, dashboard UI typecheck/build, worktree-local binary freshness checks, and artifact hygiene checks
- release-gate logs and future ONLV gates should use the same `onlava.harness.artifact.v1` evidence shape for failed or expensive steps
- `ONLAVA_RELEASE_GATE_EXTERNAL_APP_ROOT` may point at a read-only onlava app for the optional external app smoke
- `ONLAVA_RELEASE_GATE_LOG_DIR` may override the log directory; otherwise logs are written under `.onlava/release-gate/`
- `ONLAVA_ONLV_SMOKE_ROOT` may point at the ONLV checkout used by `scripts/onlv-two-worktree-smoke.sh`; otherwise the release gate uses `ONLAVA_RELEASE_GATE_EXTERNAL_APP_ROOT` when set, then `/Users/petrbrazdil/Repos/onlv` when present. The smoke starts two temporary ONLV git worktrees with the current `ONLAVA_BIN`, expects edge DNS and the privileged edge helper to be installed, runs `onlava system edge install` for trusted HTTPS `127.0.0.1:443` routing, asserts session-scoped `local.dev` API, Pulse, Blog, Electric, Grafana, Temporal, and Console routes without `.onlava.localhost`, `:9440`, or explicit HTTPS ports, checks managed database, Electric stream, Temporal queue, and alias exclusivity, then tears the sessions, edge, and worktrees down. The smoke uses managed dnsmasq and Caddy.
- artifact hygiene is intentionally strict and fails on local release artifacts such as `.DS_Store` and `__MACOSX`

Implemented `logs --jsonl` rules:

```text
onlava logs --jsonl
onlava logs --json
```

- `--json` is an alias for `--jsonl`
- output is JSONL
- each line conforms to `onlava.dev.event.v1`
- one JSON object is emitted per VictoriaLogs-backed structured dev event
- structured events include app id/root, session id, source id/kind/name/role/pid/stream/status, level, message, parsed fields, raw output, and parse metadata
- structured dev events are assigned a stable integer ID before export to VictoriaLogs
- human-readable raw output remains the default when neither flag is used

Implemented `traces clear --json` rules:
- output conforms to `onlava.traces.clear.v1`
- trace clearing is dev/admin beta for v0; its existence does not make cron, trace clearing, or queue deletion semantics stable

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

### Repo-Local Cache Locations

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
    validation/
      latest.json
      <profile>-latest.json
      artifacts/
        <run-id>/
    self-latest.json
```

Reserved for upcoming work:

```text
<app-root>/.onlava/
  state/
  logs/
```

Rules:
- Use `onlava inspect ... --json` for app, route, service, endpoint, wire, build, path, docs, generator, and Temporal metadata. Use `onlava traces list --json` and `onlava metrics list --json` for local observability metadata.
- Do not read `.onlava/gen/*` directly unless debugging onlava generation. These files are internal cache artifacts that may mirror inspect output today, but they are not the supported API.
- `wire/capabilities.json` is an internal cache for `onlava inspect wire --json` and the runtime `GET /_wire/capabilities` response.
- `manifest.json` ties generated cache artifacts to schema versions, artifact paths, and deterministic content hashes for debugging generation.
- Use `onlava inspect build --json` for build metadata. `build/latest.json` is a local cache pointer to the latest prepared or compiled build workspace.
- Use `onlava harness --json` for framework app-model proof, `onlava validate <profile> --json` for app-owned quality gates, and `onlava harness self --summary` for onlava repo validation. `harness/latest.json`, `harness/validation/latest.json`, `harness/self-latest.json`, and `harness/self-summary-latest.json` are local snapshots written by `--write`; `--json=full` is the explicit full archive stdout mode.
- Future implementation should keep cache paths predictable for debugging, but external tools and agents should integrate through command JSON output.

## JSON Schemas

Implemented now:
- [onlava.inspect.app.v1.schema.json](schemas/onlava.inspect.app.v1.schema.json)
- [onlava.inspect.routes.v1.schema.json](schemas/onlava.inspect.routes.v1.schema.json)
- [onlava.inspect.services.v1.schema.json](schemas/onlava.inspect.services.v1.schema.json)
- [onlava.inspect.endpoints.v1.schema.json](schemas/onlava.inspect.endpoints.v1.schema.json)
- [onlava.inspect.traces.v1.schema.json](schemas/onlava.inspect.traces.v1.schema.json)
- [onlava.inspect.metrics.v1.schema.json](schemas/onlava.inspect.metrics.v1.schema.json)
- [onlava.inspect.observability.v1.schema.json](schemas/onlava.inspect.observability.v1.schema.json)
- [onlava.logs.query.v1.schema.json](schemas/onlava.logs.query.v1.schema.json)
- [onlava.logs.tail.entry.v1.schema.json](schemas/onlava.logs.tail.entry.v1.schema.json)
- [onlava.help.v1.schema.json](schemas/onlava.help.v1.schema.json)
- [onlava.metrics.query.v1.schema.json](schemas/onlava.metrics.query.v1.schema.json)
- [onlava.metrics.labels.v1.schema.json](schemas/onlava.metrics.labels.v1.schema.json)
- [onlava.metrics.series.v1.schema.json](schemas/onlava.metrics.series.v1.schema.json)
- [onlava.inspect.docs.v1.schema.json](schemas/onlava.inspect.docs.v1.schema.json)
- [onlava.docs.index.v1.schema.json](schemas/onlava.docs.index.v1.schema.json)
- [onlava.wire.capabilities.v1.schema.json](schemas/onlava.wire.capabilities.v1.schema.json)
- [onlava.inspect.build.v1.schema.json](schemas/onlava.inspect.build.v1.schema.json)
- [onlava.inspect.paths.v1.schema.json](schemas/onlava.inspect.paths.v1.schema.json)
- [onlava.inspect.generators.v1.schema.json](schemas/onlava.inspect.generators.v1.schema.json)
- [onlava.inspect.temporal.v1.schema.json](schemas/onlava.inspect.temporal.v1.schema.json)
- [onlava.db.apply.result.v1.schema.json](schemas/onlava.db.apply.result.v1.schema.json)
- [onlava.db.seed.result.v1.schema.json](schemas/onlava.db.seed.result.v1.schema.json)
- [onlava.db.setup.result.v1.schema.json](schemas/onlava.db.setup.result.v1.schema.json)
- [onlava.task.list.v1.schema.json](schemas/onlava.task.list.v1.schema.json)
- [onlava.task.inspect.v1.schema.json](schemas/onlava.task.inspect.v1.schema.json)
- [onlava.task.graph.v1.schema.json](schemas/onlava.task.graph.v1.schema.json)
- [onlava.inspect.validation.v1.schema.json](schemas/onlava.inspect.validation.v1.schema.json)
- [onlava.validation.list.v1.schema.json](schemas/onlava.validation.list.v1.schema.json)
- [onlava.validation.inspect.v1.schema.json](schemas/onlava.validation.inspect.v1.schema.json)
- [onlava.validation.graph.v1.schema.json](schemas/onlava.validation.graph.v1.schema.json)
- [onlava.validation.plan.v1.schema.json](schemas/onlava.validation.plan.v1.schema.json)
- [onlava.validation.result.v1.schema.json](schemas/onlava.validation.result.v1.schema.json)
- [onlava.traces.clear.v1.schema.json](schemas/onlava.traces.clear.v1.schema.json)
- [onlava.worker.manifest.v1.schema.json](schemas/onlava.worker.manifest.v1.schema.json)
- [onlava.worker.manifest.v2.schema.json](schemas/onlava.worker.manifest.v2.schema.json)
- [onlava.gen.manifest.v1.schema.json](schemas/onlava.gen.manifest.v1.schema.json)
- [onlava.build.latest.v1.schema.json](schemas/onlava.build.latest.v1.schema.json)
- [onlava.run.event.v1.schema.json](schemas/onlava.run.event.v1.schema.json)
- [onlava.check.result.v1.schema.json](schemas/onlava.check.result.v1.schema.json)
- [onlava.harness.result.v1.schema.json](schemas/onlava.harness.result.v1.schema.json)
- [onlava.harness.self.v1.schema.json](schemas/onlava.harness.self.v1.schema.json)
- [onlava.harness.self.summary.v1.schema.json](schemas/onlava.harness.self.summary.v1.schema.json)
- [onlava.dev.event.v1.schema.json](schemas/onlava.dev.event.v1.schema.json)
- [onlava.logs.event.v1.schema.json](schemas/onlava.logs.event.v1.schema.json)
- [onlava.version.v1.schema.json](schemas/onlava.version.v1.schema.json)
- [onlava.doctor.result.v1.schema.json](schemas/onlava.doctor.result.v1.schema.json)
- [onlava.toolchain.v1.schema.json](schemas/onlava.toolchain.v1.schema.json)
- [onlava.toolchain.status.v1.schema.json](schemas/onlava.toolchain.status.v1.schema.json)

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

### `onlava traces list --json`

Beta diagnostic subject. Use this when an agent needs concrete local traces
without scraping the dashboard UI. The JSON shape is versioned, but retention,
backend preference, span reconstruction, and clear semantics may change before
this is promoted to stable v0.

Example:

```text
onlava traces list --json --session current --endpoint SyncGet --min-duration-ms 2000 --since 1h --slowest
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

### `onlava metrics list --json`

Beta diagnostic subject. Use this when an agent needs a metrics-style rollup
over locally captured traces and logs. The JSON shape is versioned, but rollup
definitions, percentile calculations, default limits, and Victoria source
selection may change before this is promoted to stable v0.

Example:

```text
onlava metrics list --json --session current --service sync --since 15m
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

### `onlava inspect observability --json`

Beta diagnostic subject. Use this before ad hoc observability queries when an
agent needs to know whether the local Victoria backends are reachable and which
scope will be enforced.

Example:

```text
onlava inspect observability --json --session current
```

The response uses `onlava.inspect.observability.v1` and includes `scope`,
`backends.logs`, `backends.metrics`, `backends.traces`, examples, and optional
warnings. Raw backend URLs are exposed only under the optional `debug.base_urls`
object for intentional substrate debugging.

### `onlava logs query --json`

Beta query surface for scoped VictoriaLogs LogsQL. This is the preferred CLI
path for targeted log debugging when plain `onlava logs --jsonl` is too broad.

Example:

```text
onlava logs query --json --since 15m --limit 100 --query 'error OR panic'
```

The response uses `onlava.logs.query.v1`, echoes the selected scope and query
bounds, and returns normalized entries with `time`, `level`, `source`,
`message`, `fields`, `trace_id`, `span_id`, and `raw` where available. Passing
`--jsonl` writes only log entries as JSON Lines. `onlava logs tail --jsonl`
emits one `onlava.logs.tail.entry.v1` object per line and uses `--since` as the
VictoriaLogs live-tail `start_offset`.

### `onlava metrics query --json`

Beta query surface for scoped PromQL/MetricsQL. Range queries are the default;
`--instant` uses the instant query endpoint.

Example:

```text
onlava metrics query --json --since 15m --step 5s --promql 'max_over_time(onlava_request_duration_seconds[15m])'
```

The response uses `onlava.metrics.query.v1`, echoes scope and bounds, reports
the backend `result_type`, and returns normalized metric series and samples.
`onlava metrics labels --json --since 1h --match 'onlava_request_duration_seconds'` emits `onlava.metrics.labels.v1`.
`onlava metrics series --json --match 'onlava_request_duration_seconds'` emits
`onlava.metrics.series.v1`.

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

### `onlava inspect harness --json`

Use this when an agent needs the latest harness evidence without parsing
terminal output.

Source files:

- `.onlava/harness/latest.json`
- `.onlava/harness/self-latest.json`
- `.onlava/harness/self-summary-latest.json`
- `.onlava/harness/ui/latest.json`
- `.onlava/harness/ui/screenshots/*.png`
- `.onlava/harness/ui/dom/*.json`
- `.onlava/harness/ui/console.jsonl`
- `.onlava/harness/ui/network.jsonl`
- `.onlava/harness/artifacts/`

Example:

```text
onlava inspect harness --json
onlava inspect harness artifact test-timing --json
onlava inspect harness diagnostics --severity warning --json
onlava inspect harness timing --top 10 --json
```

Example output:

```json
{
  "schema_version": "onlava.inspect.harness.v1",
  "scope": "repo",
  "root": "/repo/onlava",
  "latest": [
    {
      "name": "self-harness",
      "path": ".onlava/harness/self-latest.json",
      "schema_version": "onlava.harness.self.v1",
      "exists": true
    }
  ],
  "evidence": [
    {
      "schema_version": "onlava.harness.artifact.v1",
      "command": ["go", "test", "-count=1", "-json", "./..."],
      "cwd": "/repo/onlava",
      "started_at": "2026-06-07T20:45:00Z",
      "duration_ms": 1234,
      "exit_code": 1,
      "stdout_tail": "{\"Action\":\"fail\"}",
      "artifacts": [
        {
          "name": "go-tests-stdout",
          "path": ".onlava/harness/artifacts/20260607T204500Z/go-test.jsonl",
          "schema_version": "go.test.jsonl"
        }
      ],
      "repro_command": "cd /repo/onlava && go test -count=1 -json ./..."
    }
  ]
}
```
