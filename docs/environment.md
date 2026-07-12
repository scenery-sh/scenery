# scenery Environment Reference

This page is the human reference for scenery-owned environment variables. The machine-readable source of truth is [environment.registry.json](environment.registry.json), validated by `scenery harness self`.

Prefer `.scenery.json` for stable app configuration. Use environment variables for local overrides, secrets, process identity, or explicit escape hatches. New production env names must be added to the registry with rationale, docs, and tests; otherwise self-harness fails.

Process environment wins over values loaded from `.env` and `.env.local`. `scenery up`, local `scenery task run`, and local `scenery worker` require an app-root `.env`; `.env.local` is optional.

## Agent And Dev Routing

| Variable | Direction | Description |
| --- | --- | --- |
| `HOME` | host input | Host home directory, read as a fallback during browser discovery and default agent-home resolution. Not a scenery configuration surface. |
| `SCENERY_AGENT_HOME` | user input | Overrides the machine-wide local agent home. Default is `~/.scenery`. |
| `SCENERY_AGENT_SOCKET` | user input | Overrides the agent Unix control socket path. |
| `SCENERY_AGENT_ROUTER_ADDR` | user input | Overrides the agent router listen address. Default is `127.0.0.1:9440`. |
| `SCENERY_AGENT_TRUST` | user input | `1` asks the agent to trust the existing local scenery CA when starting HTTPS routing. |
| `SCENERY_AGENT_DISABLE` | user input | `1` disables local agent usage. `scenery up --detach` requires this to be unset. |
| `SCENERY_DEV_CACHE_DIR` | user input | Overrides build/dashboard cache root. This does not change agent home. |
| `SCENERY_DEV_DASHBOARD_ADDR` | internal/user input | Overrides the dashboard backend address used by dev sessions. Normally allocated automatically. |
| `SCENERY_DEV_DASHBOARD_UI_DIR` | user input | Overrides the built dashboard UI directory used by the dashboard backend. |
| `SCENERY_FRONTEND_<NAME>_ADDR` | user input | Manual frontend upstream override, for example `SCENERY_FRONTEND_PULSE_ADDR=127.0.0.1:4321`. |

## App Child Identity

These are injected by scenery into generated app processes. App code may read them, but users normally should not set them.

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_APP_ID` | injected | Base app identity from `.scenery.json`. |
| `SCENERY_APP_ROOT` | injected | Absolute app root path. |
| `SCENERY_LISTEN_NETWORK` | injected | Runtime listen network, usually `unix` in agent dev or `tcp` otherwise. |
| `SCENERY_LISTEN_ADDR` | injected | Runtime listen address or Unix socket path. |
| `SCENERY_ROLE` | injected | Generated binary role: `all`, `api`, or `worker`. |
| `SCENERY_DURABLE_ENDPOINT` | injected | Remote durable API endpoint for `scenery worker durable`. |
| `SCENERY_DURABLE_TOKEN` | injected secret | Remote durable worker bearer token passed by `scenery worker durable`. |
| `SCENERY_DURABLE_SERVICES` | injected | Comma-separated durable services the remote worker should poll. |
| `SCENERY_DURABLE_WORKER_ID` | injected | Optional durable remote worker ID; defaults to the worker process ID. |
| `SCENERY_LOG_FORMAT` | injected/user input | Runtime log format selected by CLI flags or env. |
| `SCENERY_ENV` | injected/user input | App environment name such as `development`, `test`, or `production`. |
| `SCENERY_RUNTIME_ENV` | injected/user input | Runtime environment name used by scenery internals. |
| `SCENERY_SESSION_ID` | injected | Agent session ID for local dev. |
| `SCENERY_BASE_APP_ID` | injected | Base app ID for a session. |
| `SCENERY_RUNTIME_APP_ID` | injected | Session-qualified runtime app ID. |
| `SCENERY_APP_ROOT_HASH` | injected | Stable hash of the app root path. |
| `SCENERY_BRANCH` | injected | Git branch captured for the dev session. |
| `SCENERY_WORKTREE` | injected | Worktree directory name captured for the dev session. |
| `SCENERY_ROUTE_MODE` | injected | Dev routing mode for the current session, usually `path` or `host`. |
| `SCENERY_BASE_URL` | injected | Browser-facing base URL for the current dev runtime. In default path mode this is `http://localhost:<port>`. |
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

## App Service URLs And Auth

| Variable | Direction | Description |
| --- | --- | --- |
| `DATABASE_URL` | user input/injected | App-level Postgres database URL. When set, it wins and Scenery manages no server or database; otherwise Scenery injects the managed app database URL. |
| `JWT_SECRET` | user input secret | Standard-auth JWT signing secret. |
| `AUTH_COOKIE_DOMAIN` | user input/injected | Default standard-auth cookie domain; empty in default local agent development. |
| `AUTH_EMAIL_FROM` | user input/injected | Default sender address for standard-auth email flows. |
| `AUTH_TOKEN_CIPHER_KEY` | user input secret | Default base64 32-byte AES-GCM key for encrypted Google connection token storage. |
| `GOOGLE_OAUTH_CLIENT_ID` | user input | Standard-auth Google OAuth client ID. |
| `GOOGLE_OAUTH_CLIENT_SECRET` | user input secret | Standard-auth Google OAuth client secret. |
| `<SERVICE>_DATABASE_URL` | injected | Service schema Postgres URL derived from the app database URL with `search_path=<service>,scenery`. |
| `SCENERY_DATABASE_JSON` | injected | JSON object describing the app database, source (`managed` or `external`), and service schemas. |
| `API_BASE_URL` | injected | API route exposed to app/frontends. |
| `SCENERY_API_BASE_URL` | injected | scenery-prefixed API route exposed to app/frontends. |
| `SCENERY_API_URL` | injected | Canonical API route URL exposed to app/frontends. |
| `SCENERY_API_BASE_PATH` | injected | API path prefix for path-mode routing, normally `/api/`. |
| `VITE_API_BASE_URL` | injected | Vite-compatible frontend API route. |
| `SCENERY_FRONTEND_BASE_PATH` | injected | Managed frontend path prefix for the current frontend in path mode. |
| `SCENERY_FRONTEND_PUBLIC_URL` | injected | Browser-facing URL for the current managed frontend. |
| `VITE_SCENERY_*` | injected | Vite-compatible mirrors of Scenery dev route metadata for managed frontends. |
| `SCENERY_PUBLIC_APP_URL` | injected | Public app URL for auth and app code. |

Each standard-auth setting resolves exactly its configured env name; omitted names use the canonical defaults above plus `SCENERY_PUBLIC_APP_URL` and `SCENERY_API_BASE_URL`. `scenery check -o json` warns when Google OAuth is enabled but the configured client ID or secret env cannot be resolved. Local `scenery up` derives a dev-only Google token cipher key from the local JWT secret if `AUTH_TOKEN_CIPHER_KEY` is absent; production should set a real base64 32-byte key.

## Toolchain Store

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_TOOLCHAIN_DIR` | user input | Overrides the managed toolchain store root. Default is `.scenery/toolchain/` under the app or repo root; machine-level edge tools use `~/.scenery/toolchain/`. |
| `SCENERY_TOOLCHAIN_DOWNLOAD` | user input | `0` disables automatic downloads for managed toolchain binaries. |

Managed toolchain artifacts come from `scenery.toolchain.json` and manifest-driven downloads or source builds into the managed store. Scenery-owned toolchain binaries and images do not fall back to ambient system `PATH` binaries.

## Managed Dev Services

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_STORAGE_CONFIG` | injected/user input | App-facing storage capability config consumed by `scenery.sh/storage`. Dev sessions inject it; headless runtimes require an explicit operator-provided config whose stores use `kind: "local"` (absolute `root`) or `kind: "proxy"` (`proxy_socket`). It contains configured store names and Scenery-owned backend metadata, not raw object-store credentials. |
| `SCENERY_STORAGE_CELL_ID` | injected | Stable shared storage cell ID for the app's configured storage capability. |

## Observability And Victoria

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

scenery also injects standard OpenTelemetry endpoint variables when Victoria sidecars are active:

| Variable | Direction | Description |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | injected | Metrics OTLP endpoint, usually VictoriaMetrics. |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | injected | Logs OTLP endpoint, usually VictoriaLogs. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | injected | Traces OTLP endpoint, usually VictoriaTraces. |

## Tooling, Tests, And Release Gates

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_BIN` | user input | Target-app helper override for the scenery binary path. |
| `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` | user input | Optional external app root for release-gate smoke validation. |
| `SCENERY_RELEASE_GATE_LOG_DIR` | user input | Release-gate log directory override. |
| `SCENERY_TEST_DATABASE_URL` | test input secret | Optional live Postgres DSN for gated database integration tests; tests create and drop per-test databases. |
| `SCENERY_TEST_WATCH_BACKUP_POLL_MS` | test escape hatch | Overrides `scenery up` file-watch backup poll interval in integration tests so missed fsnotify events do not wait on the production fallback delay. |
| `SCENERY_TEST_WATCH_POLL_MS` | test escape hatch | Overrides `scenery up` file-watch polling interval in integration tests that intentionally exercise polling paths. |
| `SCENERY_TEST_WATCH_SETTLE_DELAY_MS` | test escape hatch | Overrides `scenery up` file-watch settle delay in integration tests so reload assertions do not wait on production debounce timing. This is intentionally registry-approved because the process under test is production dev code. |
| `SCENERY_SHADCN_REGISTRY_ROOT` | user input | UI registry root override for the dashboard shadcn wrapper. |
| `SCENERY_SHADCN_VERSION` | user input | shadcn CLI version override for the dashboard wrapper. |
| `SCENERY_SHADCN_OVERWRITE` | user input | `1` permits overwrite operations in the dashboard shadcn wrapper. |

Variables named `SCENERY_TEST_*` that appear only inside tests are not part of the user-facing contract.
