# scenery Environment Reference

This page is the human reference for scenery-owned environment variables. The machine-readable source of truth is [environment.registry.json](environment.registry.json), validated by `scenery harness self`.

Prefer `.scenery.json` for stable app configuration. Use environment variables for local overrides, secrets, process identity, or explicit escape hatches. New production env names must be added to the registry with rationale, docs, and tests; otherwise self-harness fails.

Process environment wins over values loaded from `.env` and `.env.local`. `scenery up`, local `scenery serve`, local `scenery task run`, and local `scenery worker` require an app-root `.env`; `.env.local` is optional.

## Agent And Dev Routing

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_AGENT_HOME` | user input | Overrides the machine-wide local agent home. Default is `~/.scenery`. |
| `SCENERY_AGENT_SOCKET` | user input | Overrides the agent Unix control socket path. |
| `SCENERY_AGENT_ROUTER_ADDR` | user input | Overrides the agent router listen address. Default is `127.0.0.1:9440`. |
| `SCENERY_AGENT_ROUTER_TLS` | user input | `0` disables HTTPS routing; `1` forces HTTPS. Default is HTTPS. |
| `SCENERY_AGENT_TRUST` | user input | `1` asks the agent to trust the existing local scenery CA when starting HTTPS routing. |
| `SCENERY_AGENT_DISABLE` | user input | `1` disables local agent usage. `scenery up --detach` requires this to be unset. |
| `SCENERY_DEV_CACHE_DIR` | user input | Overrides build/dashboard cache root. This does not change agent home. |
| `SCENERY_DEV_DASHBOARD_ADDR` | internal/user input | Overrides the dashboard backend address used by dev sessions. Normally allocated automatically. |
| `SCENERY_DEV_DASHBOARD_UI_DIR` | user input | Overrides the built dashboard UI directory used by the dashboard backend. |
| `SCENERY_LOCAL_PROXY` | legacy/internal | `1` is rejected by normal `scenery up`; the legacy local proxy variable remains only for internal proxy code and tests. |
| `SCENERY_LOCAL_PROXY_SKIP_TRUST_INSTALL` | legacy/internal | Legacy local proxy trust-install control; normal `scenery up` uses the agent trust path instead. |
| `SCENERY_LOCAL_PROXY_HTTP_PORT` | legacy/internal | Legacy local proxy HTTP port override, not used by normal `scenery up`. |
| `SCENERY_LOCAL_PROXY_HTTPS_PORT` | legacy/internal | Legacy local proxy HTTPS port override, not used by normal `scenery up`. |
| `SCENERY_FRONTEND_<NAME>_ADDR` | user input | Manual frontend upstream override, for example `SCENERY_FRONTEND_PULSE_ADDR=127.0.0.1:4321`. |
| `SCENERY_DISABLE_FRONTEND_PROXY` | legacy/internal | Disables frontend proxy/upstream discovery in the legacy local proxy; normal `scenery up` uses agent-routed frontend sessions. |

## App Child Identity

These are injected by scenery into generated app processes. App code may read them, but users normally should not set them.

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_APP_ID` | injected | Base app identity from `.scenery.json`. |
| `SCENERY_APP_ROOT` | injected | Absolute app root path. |
| `SCENERY_LISTEN_NETWORK` | injected | Runtime listen network, usually `unix` in agent dev or `tcp` otherwise. |
| `SCENERY_LISTEN_ADDR` | injected | Runtime listen address or Unix socket path. |
| `SCENERY_ROLE` | injected | Generated binary role: `all`, `api`, or `worker`. |
| `SCENERY_LOG_FORMAT` | injected/user input | Runtime log format selected by CLI flags or env. |
| `SCENERY_ENV` | injected/user input | App environment name such as `development`, `test`, or `production`. |
| `SCENERY_RUNTIME_ENV` | injected/user input | Runtime environment name used by scenery internals. |
| `SCENERY_SESSION_ID` | injected | Agent session ID for local dev. |
| `SCENERY_BASE_APP_ID` | injected | Base app ID for a session. |
| `SCENERY_RUNTIME_APP_ID` | injected | Session-qualified runtime app ID. |
| `SCENERY_APP_ROOT_HASH` | injected | Stable hash of the app root path. |
| `SCENERY_BRANCH` | injected | Git branch captured for the dev session. |
| `SCENERY_WORKTREE` | injected | Worktree directory name captured for the dev session. |
| `SCENERY_DEV_SUPERVISOR` | injected | Marks a child process launched by `scenery up`. |
| `SCENERY_DEV_SUPERVISOR_PID` | injected | Parent dev supervisor PID. |
| `SCENERY_PARENT_MONITOR` | injected/user input | Enables runtime parent monitoring. |
| `SCENERY_PARENT_MONITOR_PID` | injected | Parent PID watched by runtime parent monitoring. |
| `SCENERY_DEV_ENDPOINTS` | injected/user input | `1` enables dev/admin endpoints such as `/__scenery/config` and `/debug/pprof/*`. |
| `SCENERY_CORS_ALLOW_ORIGINS` | user input | Comma-separated production CORS allowlist outside dev endpoint mode. |
| `SCENERY_DEV_REPORT_URL` | injected | Dev dashboard report endpoint. |
| `SCENERY_DEV_REPORT_TOKEN` | injected | Token used by the app child to report logs/traces to the dev dashboard. |
| `SCENERY_DEV_DETACHED_CHILD` | internal | Marks the background child used by `scenery up --detach`. |
| `SCENERY_PUBLIC_BASE_URL` | injected | Public API base URL advertised to app code. |
| `SCENERY_STANDALONE_DEV` | internal | Marks a generated runtime process started in standalone dev mode. |

## App Service URLs And Auth

| Variable | Direction | Description |
| --- | --- | --- |
| `DATABASE_URL` | user input | Conventional database URL. Managed dev Postgres does not inject this into app, setup, DB setup, or worker environments; prefer `DatabaseURL` for Scenery apps. |
| `DatabaseURL` | user input/injected | scenery app-style database URL env and managed app database authority. Used when `auth.database_url_env` is `DatabaseURL`. |
| `SCENERY_AUTH_DATABASE_URL` | user input | Fallback DB URL for standard auth when app-specific envs are unset. |
| `SCENERY_AUTH_JWT_SECRET` | user input | Fallback JWT signing secret for standard auth when `auth.jwt_secret_env` and `JWT_SECRET` are unset. |
| `SCENERY_AUTH_EMAIL_FROM` | user input | Fallback sender address for standard auth email flows when `auth.email_from_env` and `AUTH_EMAIL_FROM` are unset. |
| `SCENERY_MANAGED_DATABASE_NAME` | injected | Name of the managed per-session Postgres database. |
| `SCENERY_MANAGED_DATABASE_URL` | injected | Managed per-session Postgres URL exposed for tooling/debugging. |
| `API_BASE_URL` | injected | API route exposed to app/frontends. |
| `SCENERY_API_BASE_URL` | injected | scenery-prefixed API route exposed to app/frontends. |
| `VITE_API_BASE_URL` | injected | Vite-compatible frontend API route. |
| `SCENERY_PUBLIC_APP_URL` | injected | Public app URL for auth and app code. |
| `SCENERY_AUTH_COOKIE_DOMAIN` | injected | Auth cookie domain; empty in default local agent dev. |
| `ELECTRIC_URL` | injected | Public Electric route for app/frontends. |
| `SCENERY_ELECTRIC_URL` | injected | scenery-prefixed Electric route. |
| `VITE_ELECTRIC_URL` | injected | Vite-compatible frontend Electric route. |

App-defined auth env names such as `JWTSecret`, `GoogleOAuthClientID`, `GoogleOAuthClientSecret`, `AuthCookieDomain`, `PublicAppURL`, `APIBaseURL`, and `AuthEmailFrom` come from `.scenery.json` and are target-app inputs, not fixed scenery global names.

## Toolchain Store

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_TOOLCHAIN_DIR` | user input | Overrides the managed toolchain store root. Default is `.scenery/toolchain/` under the app or repo root; machine-level edge tools use `~/.scenery/toolchain/`. |
| `SCENERY_TOOLCHAIN_DOWNLOAD` | user input | `0` disables automatic downloads for managed toolchain binaries. |

Managed toolchain artifacts come from `scenery.toolchain.json` and manifest-driven downloads or source builds into the managed store. Some app-root tools also expose documented per-tool env overrides. They do not fall back to ambient system `PATH` binaries.

## Managed Postgres And Electric

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_DEV_POSTGRES_ADMIN_URL` | user input | Explicit admin Postgres URL for the managed dev database planner. |
| `SCENERY_DEV_POSTGRES_BIN` | user input | Explicit local `postgres` binary path; scenery does not search `PATH` for it. |
| `SCENERY_DEV_POSTGRES_INITDB` | user input | Explicit local `initdb` binary path; scenery does not search `PATH` for it. |
| `SCENERY_DEV_POSTGRES_EXTERNAL` | user input | `1` keeps an explicit external `DatabaseURL` instead of creating a managed session database. External mode requires `DatabaseURL`; `DATABASE_URL` is ignored as the app database authority. |
| `SCENERY_DEV_ELECTRIC_UPSTREAM` | user input | Explicit Electric upstream; scenery registers it as the session Electric backend. |
| `SCENERY_DEV_ELECTRIC_BIN` | user input | Explicit local Electric binary path; scenery does not search `PATH` for it. |
| `ELECTRIC_REPLICATION_STREAM_ID` | user input/injected | Electric replication stream ID. scenery sets a deterministic session-scoped default. |

## Temporal

| Variable | Direction | Description |
| --- | --- | --- |
| `TEMPORAL_ADDRESS` | user input/injected | Default Temporal address env. Apps can override the env name with `temporal.address_env`. |
| `TEMPORAL_NAMESPACE` | injected/user input | Temporal namespace. |
| `TEMPORAL_API_KEY` | user input | Default Temporal API key env when configured. |
| `TEMPORAL_TLS_SERVER_NAME` | user input | Default Temporal TLS server name env when configured. |
| `TEMPORAL_TLS_CA_CERT_FILE` | user input | Default Temporal TLS CA file env when configured. |
| `TEMPORAL_TLS_CERT_FILE` | user input | Default Temporal client certificate env when configured. |
| `TEMPORAL_TLS_KEY_FILE` | user input | Default Temporal client key env when configured. |
| `SCENERY_TEMPORAL_TASK_QUEUE_PREFIX` | injected/user input | Overrides generated Temporal task queue prefix. Agent dev sets this to a session-scoped value. |
| `SCENERY_TEMPORAL_TASK_QUEUE_TEST_SUFFIX` | injected/user input | Test-runtime suffix appended to the Temporal task queue prefix so `scenery test` workers cannot share live dev queues. |
| `SCENERY_TEMPORAL_TASK_QUEUE` | injected/user input | Worker task queue override; `scenery worker --task-queue` sets it. |
| `SCENERY_TEMPORAL_DEPLOYMENT_NAME` | injected/user input | Temporal Worker Deployment name override. |
| `SCENERY_TEMPORAL_VERSIONING_BEHAVIOR` | user input | `pinned` or `auto_upgrade`; default is `pinned`. |
| `SCENERY_TEMPORAL_HOST_RESOURCE_REPORTING` | user input | `0` disables Temporal Go SDK host resource reporting. Enabled by default. |
| `SCENERY_BUILD_ID` | injected/user input | Worker build ID. Agent dev uses the session ID. |
| `SCENERY_TEMPORAL_BIN` | user input | Explicit Temporal CLI binary path for local dev server and deployment admin commands. Without this, scenery uses the managed toolchain artifact. |

## Observability, Victoria, And Grafana

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_DEV_OBSERVABILITY_BACKEND` | injected | Current dev observability backend, for example `victoria`. |
| `SCENERY_DEV_VICTORIA` | user input | `0` disables Victoria sidecars. |
| `SCENERY_DEV_VICTORIA_DOWNLOAD` | user input | `0` disables automatic Victoria managed-toolchain downloads. |
| `SCENERY_DEV_VICTORIA_DIR` | user input | Overrides Victoria runtime state root. Managed binaries still live under `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`. |
| `SCENERY_VICTORIA_METRICS_BIN` | user input | Explicit VictoriaMetrics binary path. |
| `SCENERY_VICTORIA_LOGS_BIN` | user input | Explicit VictoriaLogs binary path. |
| `SCENERY_VICTORIA_TRACES_BIN` | user input | Explicit VictoriaTraces binary path. |
| `SCENERY_VICTORIA_METRICS` | internal prefix | Prefix used by VictoriaMetrics-specific env naming. Do not set this key directly. |
| `SCENERY_VICTORIA_LOGS` | internal prefix | Prefix used by VictoriaLogs-specific env naming. Do not set this key directly. |
| `SCENERY_VICTORIA_TRACES` | internal prefix | Prefix used by VictoriaTraces-specific env naming. Do not set this key directly. |
| `SCENERY_VICTORIA_METRICS_VERSION` | user input | Overrides the pinned VictoriaMetrics version. |
| `SCENERY_VICTORIA_LOGS_VERSION` | user input | Overrides the pinned VictoriaLogs version. |
| `SCENERY_VICTORIA_TRACES_VERSION` | user input | Overrides the pinned VictoriaTraces version. |
| `SCENERY_VICTORIA_METRICS_PORT` | user input | Preferred VictoriaMetrics loopback port. |
| `SCENERY_VICTORIA_LOGS_PORT` | user input | Preferred VictoriaLogs loopback port. |
| `SCENERY_VICTORIA_TRACES_PORT` | user input | Preferred VictoriaTraces loopback port. |
| `SCENERY_VICTORIA_METRICS_URL` | injected | VictoriaMetrics base URL exposed to children. |
| `SCENERY_VICTORIA_LOGS_URL` | injected | VictoriaLogs base URL exposed to children. |
| `SCENERY_VICTORIA_TRACES_URL` | injected | VictoriaTraces base URL exposed to children. |
| `SCENERY_VICTORIA_METRICS_ENDPOINT` | injected | VictoriaMetrics OTLP endpoint. |
| `SCENERY_VICTORIA_LOGS_ENDPOINT` | injected | VictoriaLogs OTLP endpoint. |
| `SCENERY_VICTORIA_TRACES_ENDPOINT` | injected | VictoriaTraces OTLP endpoint. |
| `SCENERY_DEV_GRAFANA` | user input | `auto`, `1`, or `0`. Default `auto` starts Grafana when possible. |
| `SCENERY_DEV_GRAFANA_DOWNLOAD` | user input | `0` disables automatic Grafana managed-toolchain downloads. |
| `SCENERY_GRAFANA_BIN` | user input | Explicit Grafana binary path. |
| `SCENERY_GRAFANA_VERSION` | user input | Overrides the pinned Grafana version. |
| `SCENERY_GRAFANA_PORT` | user input | Preferred Grafana loopback port. |
| `SCENERY_GRAFANA_DIR` | user input | Overrides Grafana runtime state root. Managed binaries still live under `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`. |
| `SCENERY_GRAFANA_HOME` | user input | Overrides Grafana home for an explicit binary or custom download path. |
| `SCENERY_GRAFANA_PUBLIC_URL` | user input | Overrides advertised Grafana browser URL. |
| `SCENERY_GRAFANA_REUSE_EXTERNAL` | user input | `1` allows reusing an externally managed Grafana. |
| `SCENERY_GRAFANA_PRESERVE_GF_ENV` | user input | `1` allows ambient `GF_*` variables through to Grafana. |
| `SCENERY_GRAFANA_DOWNLOAD_URL` | user input | Custom Grafana archive URL. |
| `SCENERY_GRAFANA_DOWNLOAD_SHA256` | user input | Checksum for a custom Grafana download. |
| `SCENERY_GRAFANA_PLUGINS_PREINSTALL_SYNC` | user input | Comma-separated Grafana plugin install list. |

scenery also injects standard OpenTelemetry endpoint variables when Victoria sidecars are active:

| Variable | Direction | Description |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | injected | Metrics OTLP endpoint, usually VictoriaMetrics. |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | injected | Logs OTLP endpoint, usually VictoriaLogs. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | injected | Traces OTLP endpoint, usually VictoriaTraces. |
| `GF_SERVER_HTTP_ADDR` | injected | Grafana child loopback bind address. |
| `GF_SERVER_HTTP_PORT` | injected | Grafana child loopback bind port. |
| `GF_SERVER_ROOT_URL` | injected | Grafana public root URL. |
| `GF_PATHS_DATA` | injected | Grafana data directory. |
| `GF_PATHS_LOGS` | injected | Grafana logs directory. |
| `GF_PATHS_PLUGINS` | injected | Grafana plugins directory. |
| `GF_PATHS_PROVISIONING` | injected | Grafana provisioning directory. |

## Tooling, Tests, And Release Gates

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_BIN` | user input | Target-app helper override for the scenery binary path. |
| `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` | user input | Optional external app root for release-gate smoke validation. |
| `SCENERY_RELEASE_GATE_LOG_DIR` | user input | Release-gate log directory override. |
| `SCENERY_ONLV_SMOKE_ROOT` | user input | ONLV checkout root for the two-worktree release-gate smoke. |
| `SCENERY_ONLV_SMOKE_LOG_DIR` | user input | Log directory override for the ONLV two-worktree release-gate smoke. |
| `SCENERY_ONLV_SMOKE_TMPDIR` | user input | Short temporary directory root for the ONLV two-worktree release-gate smoke. |
| `SCENERY_TEST_DATABASE_URL` | test input | PostgreSQL admin URL for integration tests that need a real database; tests create package-scoped databases from it. |
| `SCENERY_TEST_WATCH_BACKUP_POLL_MS` | test escape hatch | Overrides `scenery up` file-watch backup poll interval in integration tests so missed fsnotify events do not wait on the production fallback delay. |
| `SCENERY_TEST_WATCH_POLL_MS` | test escape hatch | Overrides `scenery up` file-watch polling interval in integration tests that intentionally exercise polling paths. |
| `SCENERY_TEST_WATCH_SETTLE_DELAY_MS` | test escape hatch | Overrides `scenery up` file-watch settle delay in integration tests so reload assertions do not wait on production debounce timing. This is intentionally registry-approved because the process under test is production dev code. |
| `SCENERY_SHADCN_REGISTRY_ROOT` | user input | UI registry root override for the dashboard shadcn wrapper. |
| `SCENERY_SHADCN_VERSION` | user input | shadcn CLI version override for the dashboard wrapper. |
| `SCENERY_SHADCN_OVERWRITE` | user input | `1` permits overwrite operations in the dashboard shadcn wrapper. |

Variables named `SCENERY_TEST_*` that appear only inside tests are not part of the user-facing contract.

Generated TypeScript clients also contain constants named `SCENERY_WIRE_SCHEMA_HASH`, `SCENERY_WIRE_CONTENT_TYPE`, and `SCENERY_WIRE_JSON_CONTENT_TYPE`. Those are generated code constants, not process environment variables.
