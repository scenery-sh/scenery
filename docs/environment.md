# scenery Environment Reference

This page is the human reference for scenery-owned environment variables. The machine-readable source of truth is [environment.registry.json](environment.registry.json), validated by `go run ./scripts/verify`.

Prefer `.scenery.json` for structural app settings. Application configuration values are never environment variables: typed deployment inputs and framework inputs such as `auth.jwt_secret` are set per application and environment with `scenery config set KEY --env NAME` and reach runtimes through a private snapshot (see [Environment Configuration](local-contract.md#environment-configuration)). Scenery reads no dotenv file anywhere; a leftover `.env` in an app root is ignored. The variables below are Scenery's own process identity, host inputs, tooling knobs and injected runtime wiring. New production env names must be added to the registry with rationale, docs, and tests; otherwise self-harness fails.

## Agent And Dev Routing

| Variable | Direction | Description |
| --- | --- | --- |
| `HOME` | host input | Host home directory, read as a fallback during browser discovery and default agent-home resolution. Not a scenery configuration surface. |
| `SCENERY_AGENT_HOME` | user input | Overrides the durable state home, including `worktrees/<canonical-root-hash>/` and explicit machine edge/deploy state. Default is `~/.scenery`. Ordinary runtimes and managed SQL resources are owned per root without a private-home setting. This does not isolate machine-global DNS/edge listeners. |
| `SCENERY_AGENT_SOCKET` | user input | Overrides only the explicitly managed machine agent Unix control socket. Ordinary worktree runtimes derive private sockets from their retained root identity. |
| `SCENERY_AGENT_ROUTER_ADDR` | user input | Overrides only the explicitly managed machine agent router address (default `127.0.0.1:9440`), not ordinary worktree routing. |
| `SCENERY_AGENT_TRUST` | user input | `1` asks the agent to trust the existing local scenery CA when starting HTTPS routing. |
| `SCENERY_DEV_CACHE_DIR` | user input | Overrides build cache and explicitly standalone cache consumers, not durable worktree ownership or its private dashboard. |
| `SCENERY_DEV_DASHBOARD_ADDR` | internal/user input | Dashboard client backend address. Ordinary development sets it from the acquired worktree owner; a parent value cannot select or redirect that owner. |
| `SCENERY_FRONTEND_<NAME>_ADDR` | user input | Manual frontend upstream override, for example `SCENERY_FRONTEND_PULSE_ADDR=127.0.0.1:4321`. |

## App Child Identity

These are injected by scenery into generated app processes. App code may read them, but users normally should not set them.

Application processes started by `scenery up` and `scenery worker` do not inherit the invoking environment wholesale. They receive only operating-system and toolchain protocols (`PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, temporary directories, `TZ`, `LANG`/`LANGUAGE`/`LC_*`, terminal and color settings, `SSL_CERT_FILE`/`SSL_CERT_DIR`, HTTP proxy variables, `XDG_*` directories, dynamic-loader library paths and Go runtime knobs), `SCENERY_*` wiring, and the variables Scenery sets for them. Former application settings, SDK credential chains and loaders such as `NODE_OPTIONS` never reach them; configuration arrives only through the snapshot.

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_APP_ID` | injected | Base app identity from `.scenery.json`. |
| `SCENERY_APP_ROOT` | injected | Absolute app root path. |
| `SCENERY_LISTEN_NETWORK` | injected | Runtime listen network, usually `unix` in agent dev or `tcp` otherwise. |
| `SCENERY_LISTEN_ADDR` | injected | Runtime listen address or Unix socket path. |
| `SCENERY_ROLE` | injected | Generated binary role: `all`, `api`, or `worker`. |
| `SCENERY_PROCESS_LINK` | injected | Private process-link file for development service processes and their host: session token, the host's private dispatch listener and the session's private host state directory. A service runtime started with it serves requests but acquires schedules, event consumers and durable work only after the supervisor activates it. Invalid wiring fails runtime startup. |
| `SCENERY_DURABLE_ENDPOINT` | injected | Remote durable API endpoint for `scenery worker durable`. |
| `SCENERY_DURABLE_TOKEN` | injected secret | Remote durable worker bearer token passed by `scenery worker durable`. |
| `SCENERY_DURABLE_SERVICES` | injected | Comma-separated durable services the remote worker should poll. |
| `SCENERY_DURABLE_WORKER_ID` | injected | Optional durable remote worker ID; defaults to the worker process ID. |
| `SCENERY_LOG_FORMAT` | injected/user input | Runtime log format selected by CLI flags or env. |
| `SCENERY_ENV` | injected | Resolved `.scenery.json` environment name; always present in app, frontend, task, and worker processes. |
| `SCENERY_RUNTIME_ENV` | injected | Same resolved environment name for runtime internals. |
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
| `SCENERY_DEV_DETACHED_CHILD` | internal | Marks the background supervisor used by `scenery up --detach`; it receives a private startup-result pipe on inherited descriptor 3, closed before executing application children. Not a user-settable mode. |
| `SCENERY_PUBLIC_BASE_URL` | injected | Public API base URL advertised to app code. |
| `SCENERY_CONFIG_SNAPSHOT_FD` | injected | Number of the inherited descriptor carrying the process's environment configuration snapshot. The runtime reads it once, closes it and unsets the name before any application code can start children. Not a user input. |

## Assistant Runtime Handoff

These names are a private, provider-neutral boundary between the scenery
supervisor/launcher, the generated app child, and a managed assistant helper.
They are not public API configuration, must not be copied into browser-facing
configuration or ordinary logs, and helper control values are always loopback
scoped. Only the two token-key names are operator-supplied secret inputs; all
other names in this section are injected handoff values.

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_ASSISTANT_RUNTIME_CONFIG` | injected | App-child path to a private mode-0600 JSON descriptor written by the supervisor or production launcher. It contains helper control addresses and credentials and must never be exposed publicly or logged. |
| `SCENERY_ASSISTANT_TOKEN_KEY` | user input secret | Stable 32-byte framework token key, supplied as raw text or hex/base64 encoding through the operator's existing secret mechanism. Never log or expose it. |
| `SCENERY_ASSISTANT_TOKEN_KEY_FILE` | user input secret | Private mode-0600 file containing the stable framework token key. The supervisor may inject the resolved path into the app child; never expose the file or its contents. |
| `SCENERY_ASSISTANT_ID` | injected child-only | Provider-neutral assistant address injected into the managed helper. It is an internal identity, not a user-configurable public name. |
| `SCENERY_ASSISTANT_CONTROL_TOKEN` | injected child-only secret | Short-lived loopback control bearer token for the managed helper. It must never appear in logs, status, browser responses, or public inspection. |
| `SCENERY_ASSISTANT_CONTROL_ADDR` | injected child-only | Private loopback control address for the managed helper. It is not a public route and must not be emitted in provider-neutral inspection or logs. |
| `SCENERY_MCP_URL` | injected child-only | Private loopback Scenery MCP gateway URL used by the helper. It is a parent-child handoff value and must not be exposed publicly or logged. |
| `SCENERY_MCP_BRIDGE_SECRET` | injected child-only secret | Short-lived HMAC bridge secret used for helper-to-gateway assertions. Never log or expose it. |
| `SCENERY_CAPABILITY_REVISION` | injected child-only | Expected capability revision supplied to the helper for strict handshake and event validation. |
| `SCENERY_RUNTIME_REVISION` | injected child-only | Expected helper runtime revision supplied for strict handshake and restart validation. |

## App Service URLs And Auth

| Variable | Direction | Description |
| --- | --- | --- |
| `DATABASE_URL` | injected | App-level Postgres database URL that Scenery supplies to app processes: the managed app database, or the environment's configured `sql.database_url`. The CLI ignores an inherited value; a standalone generated runtime launched without Scenery reads it as its explicit SQL endpoint. |
| `<SERVICE>_DATABASE_URL` | injected | Compiled logical binding's Postgres URL with `search_path=<schema>,scenery`; standalone generated runtimes also accept an explicit per-binding endpoint. |
| `SCENERY_DATABASE_JSON` | injected | Resolved SQL supply (app database, source and logical schemas), configured before generated constructors. Not a requirements or ownership cache. |
| `API_BASE_URL` | injected | API route exposed to app/frontends. |
| `SCENERY_API_BASE_URL` | injected | scenery-prefixed API route exposed to app/frontends. |
| `SCENERY_API_URL` | injected | Canonical API route URL exposed to app/frontends. |
| `SCENERY_API_BASE_PATH` | injected | API path prefix for path-mode routing, normally `/api/`. |
| `VITE_API_BASE_URL` | injected | Vite-compatible frontend API route. |
| `SCENERY_FRONTEND_BASE_PATH` | injected | Managed frontend path prefix for the current frontend in path mode. |
| `SCENERY_FRONTEND_PUBLIC_URL` | injected | Browser-facing URL for the current managed frontend. |
| `VITE_SCENERY_*` | injected | Vite-compatible mirrors of Scenery dev route metadata for managed frontends. |
| `SCENERY_PUBLIC_APP_URL` | injected | Public app URL for auth and app code. |

Standard auth reads its configuration from the environment configuration snapshot, never the process environment: `auth.jwt_secret`, `auth.google_client_secret` and `auth.token_cipher_key` (base64 of 32 bytes) are secrets; `auth.google_client_id`, `auth.cookie_domain` and `auth.email_from` are strings. The former variables `JWT_SECRET`, `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET`, `AUTH_TOKEN_CIPHER_KEY`, `AUTH_COOKIE_DOMAIN` and `AUTH_EMAIL_FROM` have no effect. A deployable environment must configure the JWT secret, and with Google OAuth enabled also the client ID, client secret and token cipher key; local development keeps its dev-only defaults. `refresh_cookie_name` has no configuration replacement: standard auth reads, issues, and clears only `scenery_refresh`.

`scenery check -o json` warns when Google OAuth is enabled but the default local environment does not configure `auth.google_client_id` and `auth.google_client_secret`. Local `scenery up` derives a dev-only Google token cipher key from the local JWT secret when `auth.token_cipher_key` is not configured.

## Toolchain Store

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_TOOLCHAIN_DIR` | user input | Overrides the managed toolchain store root. Default is `.scenery/toolchain/` under the app or repo root; machine-level edge tools use `~/.scenery/toolchain/`. |
| `SCENERY_TOOLCHAIN_DOWNLOAD` | user input | `0` disables automatic downloads for managed toolchain binaries. |

Managed toolchain artifacts come from `scenery.toolchain.json` and manifest-driven downloads or source builds into the managed store. Scenery-owned toolchain binaries and images do not fall back to ambient system `PATH` binaries.

## Managed Dev Services

| Variable | Direction | Description |
| --- | --- | --- |
| `SCENERY_STORAGE_CONFIG` | injected/user input | Strict current `scenery.storage.runtime` artifact consumed by `scenery.sh/storage`, with exact schema/spec revisions and producer identity. Dev sessions inject it; headless runtimes require an explicit operator-provided config whose stores use `kind: "local"` (absolute `root`) or `kind: "proxy"` (`proxy_socket`). It contains configured store names and Scenery-owned backend metadata, not raw object-store credentials. |

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
| `SCENERY_TEST_DATABASE_URL` | test input secret | Optional live Postgres DSN for gated durable store, runtime and worker tests; tests create and drop per-test databases. Standard-auth release proof provisions its own database and does not read this variable. |
| `SCENERY_TEST_WATCH_BACKUP_POLL_MS` | test escape hatch | Overrides `scenery up` file-watch backup poll interval in integration tests so missed fsnotify events do not wait on the production fallback delay. |
| `SCENERY_TEST_WATCH_POLL_MS` | test escape hatch | Overrides `scenery up` file-watch polling interval in integration tests that intentionally exercise polling paths. |
| `SCENERY_TEST_WATCH_SETTLE_DELAY_MS` | test escape hatch | Overrides `scenery up` file-watch settle delay in integration tests so reload assertions do not wait on production debounce timing. This is intentionally registry-approved because the process under test is production dev code. |

Variables named `SCENERY_TEST_*` that appear only inside tests are not part of the user-facing contract.
