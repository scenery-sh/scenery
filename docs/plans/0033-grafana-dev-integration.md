# Grafana Dev Integration

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Integrate Grafana into `onlava dev` as a first-class local observability surface for developers using the existing Victoria observability stack.

After this plan, a developer should be able to run:

```sh
onlava dev
```

and get:

```text
onlava dev
  - app runtime
  - onlava dev dashboard
  - VictoriaMetrics
  - VictoriaLogs
  - VictoriaTraces, when available
  - Grafana
      - provisioned VictoriaMetrics datasource
      - provisioned VictoriaLogs datasource
      - optional VictoriaTraces datasource through the Jaeger API
      - onlava dashboards
```

Grafana is a supervised, provisioned dev companion to `onlava dev`. It is not embedded as an iframe and is not part of `onlava run`. The first-class experience is that `onlava dev` starts Victoria and Grafana, provisions datasources and dashboards, shows Grafana health/status/links in the onlava dashboard, and emits Grafana metadata in JSON and event streams.

Grafana should be treated like the existing Victoria sidecars: local, supervised, loopback-only by default, disposable, and rooted under `.onlava/`. The existing local contract already places Victoria artifacts under `.onlava/victoria/`, exposes OTLP endpoints, and allows disabling Victoria with `ONLAVA_DEV_VICTORIA=0`; Grafana should follow the same operational shape.

The intended onlava dashboard UX is:

```text
Observability
  - VictoriaMetrics: ready
  - VictoriaLogs: ready
  - VictoriaTraces: ready/degraded/unavailable
  - Grafana: Open Grafana
```

Non-goals:

* Do not make Grafana required for `onlava run`.
* Do not replace `onlava inspect logs|metrics|traces`.
* Do not make Grafana Cloud a dependency.
* Do not require Docker for the default path.
* Do not make UI-edited Grafana dashboards the source of truth.
* Do not couple onlava internals to Grafana libraries; keep the boundary process/config/HTTP.

## Progress

* [x] 2026-05-25: Create this ExecPlan as `docs/plans/0033-grafana-dev-integration.md`.
* [x] 2026-05-25: Link this ExecPlan from `docs/plans/active.md`.
* [x] 2026-05-25: Add Grafana process specification next to the existing Victoria sidecar code.
* [x] 2026-05-25: Add generated Grafana config/provisioning under `.onlava/grafana/`.
* [x] 2026-05-25: Provision VictoriaMetrics and VictoriaLogs datasources.
* [x] 2026-05-25: Optionally provision VictoriaTraces through Grafana's Jaeger datasource.
* [x] 2026-05-25: Add the first dashboard set.
* [x] 2026-05-25: Add dashboard/UI status and links.
* [x] 2026-05-25: Add CLI JSON/event fields.
* [x] 2026-05-25: Add unit tests and external-Grafana fake health coverage.
* [x] 2026-05-25: Update the local contract and architecture docs.
* [x] 2026-05-25: Validate live Grafana startup and dashboard links in the browser.
* [x] 2026-05-26: Harden Grafana readiness so ready/external requires expected datasource and dashboard UIDs, external reuse is explicit, local proxy public URLs are written into `grafana.ini`, managed pinned Grafana is preferred over `PATH`, ambient `GF_*` overrides are filtered, datasource provisioning prunes stale entries, downloads verify checksums, and UI links are hidden until Grafana is usable.

## Surprises & Discoveries

Known starting discoveries:

* The repo is already structurally prepared for this integration. `cmd/onlava` contains Victoria-specific files such as `victoria.go`, `victoria_export.go`, and `victoria_query.go`, plus dashboard state/RPC files such as `dashboard.go`, `dashboard_state.go`, and `dashboard_rpc.go`.
* `docs/local-contract.md` documents that `onlava dev` starts local VictoriaMetrics, VictoriaLogs, and VictoriaTraces sidecars by default when their binaries can be found or downloaded, while SQLite dashboard storage remains active for parity and fallback.
* `cmd/onlava/victoria.go` already has local sidecar environment controls such as `ONLAVA_DEV_VICTORIA`, `ONLAVA_DEV_VICTORIA_DOWNLOAD`, `ONLAVA_DEV_VICTORIA_DIR`, `ONLAVA_VICTORIA_METRICS_PORT`, `ONLAVA_VICTORIA_LOGS_PORT`, and `ONLAVA_VICTORIA_TRACES_PORT`.
* Grafana provisioning is the right primitive for this feature. Grafana supports provisioning datasources and dashboards from files; UI edits to provisioned dashboards are not written back to the provisioning source, so on restart or reload the file source wins. That matches reproducible onlava dev dashboards.
* VictoriaMetrics and VictoriaLogs publish Grafana datasource plugins with provisioning-friendly datasource types:

```yaml
type: victoriametrics-metrics-datasource
type: victoriametrics-logs-datasource
```

* VictoriaTraces does not need a custom plugin for the first integration because it exposes a Jaeger-compatible select endpoint that Grafana can use through the built-in Jaeger datasource.
* Grafana OSS tarballs are still available through the legacy `https://dl.grafana.com/oss/release/grafana-<version>.<os>-<arch>.tar.gz` shape for the default `13.0.1+security-01` release, including macOS ARM64.
* Managed Grafana, Grafana plugin, and Victoria sidecar versions now live in the embedded `internal/devtools/versions.json` pin file instead of being scattered across supervisor code.
* The dashboard app status path was the right place to expose Grafana state because existing UI polling and process notifications already converge there.
* First-run Grafana startup can exceed 45 seconds because Grafana installs datasource plugins synchronously before reporting healthy. The readiness timeout must be long enough for a cold plugin install path while still surfacing a clear degraded state when startup really fails.
* 2026-05-26: Static follow-up review found the first implementation could expose Grafana links after `/api/health` even when onlava provisioning had not loaded. The durable readiness boundary is now server health plus expected datasource and dashboard API reads.
* 2026-05-26: The local HTTPS proxy can advertise `https://grafana.<workspace>.localhost` before the proxy itself is started. Computing that planned public URL before Grafana provisioning lets Grafana's `root_url` match the browser-facing route.
* 2026-05-26: Inheriting developer shell `GF_*` values is too risky for a generated local config because Grafana treats environment variables as config overrides.

## Decision Log

* Decision: Grafana is dev-only initially.
  Rationale: `onlava dev` already owns local observability, live rebuild, the dashboard, local proxy, Victoria sidecars, and developer convenience features. `onlava run` should remain closer to app execution semantics.
  Date/Author: 2026-05-25 / Codex

* Decision: Use a managed local Grafana binary first, not Docker.
  Rationale: A supervised local process avoids a mandatory Docker dependency, avoids container-to-host loopback confusion with Victoria services bound to `127.0.0.1`, keeps lifecycle under the existing dev supervisor, and matches the current Victoria sidecar posture.
  Date/Author: 2026-05-25 / Codex

* Decision: Generated config/provisioning is the source of truth.
  Rationale: `onlava dev` should produce reproducible and resettable Grafana state under `.onlava/grafana/`, while dashboard JSON templates live in the repo and are copied or rendered into the local Grafana directory.
  Date/Author: 2026-05-25 / Codex

* Decision: Use stable datasource UIDs.
  Rationale: Stable UIDs make dashboards, links, generated Explore URLs, tests, and user troubleshooting deterministic.
  Date/Author: 2026-05-25 / Codex

* Decision: Plugin installation must be deterministic.
  Rationale: Grafana provisioning depends on the Victoria datasource plugins being present. The first implementation can use Grafana's synchronous plugin preinstall path, then pin versions before default-on rollout if plugin churn becomes a risk.
  Date/Author: 2026-05-25 / Codex

* Decision: Stage rollout from opt-in to default-on.
  Rationale: Start with `ONLAVA_DEV_GRAFANA=1` while lifecycle, provisioning, status reporting, and docs stabilize. Move to `auto` only after smoke tests and local docs are reliable.
  Date/Author: 2026-05-25 / Codex

* Decision: Do not iframe Grafana initially.
  Rationale: Direct links are more robust for the first integration. Grafana subpaths, cookies, anonymous auth, and reverse-proxy headers can be handled later if there is strong demand.
  Date/Author: 2026-05-25 / Codex

* Decision: Use a 3-minute readiness timeout for supervised Grafana startup.
  Rationale: Live validation showed that a cold Grafana install with synchronous Victoria datasource plugin installation can exceed 45 seconds. Three minutes leaves room for first-run plugin setup while still bounding required-mode startup failures.
  Date/Author: 2026-05-25 / Codex

* Decision: Treat external Grafana as unusable unless explicitly requested and verified.
  Rationale: An arbitrary Grafana process on the configured port is not equivalent to the onlava workbench. Reuse now requires `ONLAVA_GRAFANA_REUSE_EXTERNAL=1` and successful UID checks.
  Date/Author: 2026-05-26 / Codex

* Decision: Keep the direct upstream URL and public browser URL separate.
  Rationale: The dev supervisor and local proxy need the direct loopback URL, while Grafana's own `root_url` and the UI should use the browser-facing URL when the HTTPS proxy is enabled.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Implementation and live browser validation are complete.

Shipped outcome:

* Developers get Grafana with no manual datasource setup.
* Grafana dashboards query the same local Victoria stack used by onlava logs/metrics/traces.
* Onlava's own dashboard remains useful and agent-friendly.
* Grafana failure never prevents the app from running unless explicitly requested.
* The integration is reproducible, inspectable, and resettable by deleting `.onlava/grafana/`.
* `onlava dev --json` emits `grafana.starting`, `grafana.ready`, and `run.ready` Grafana metadata with stable datasource and dashboard UIDs.
* Live validation opened the provisioned overview, logs, and endpoint dashboards in Grafana, opened the onlava Observability dashboard page, verified datasource/dashboard links, and confirmed no browser console errors for those surfaces.
* Grafana, Victoria sidecars, dashboard server, and the app process all stopped when the dev supervisor was interrupted.
* `onlava run` remains dev-only with respect to Grafana: setting `ONLAVA_DEV_GRAFANA=1` while running `onlava run` did not start Grafana or Victoria sidecars.

## Context and Orientation

Relevant existing repo areas:

```text
cmd/onlava/victoria.go
cmd/onlava/victoria_export.go
cmd/onlava/victoria_query.go
cmd/onlava/victoria_test.go
cmd/onlava/dev_supervisor.go
cmd/onlava/dashboard.go
cmd/onlava/dashboard_state.go
cmd/onlava/dashboard_rpc.go
cmd/onlava/console.go
cmd/onlava/run_json_test.go

internal/devdash/
internal/localproxy/
internal/dbstudio/

docs/local-contract.md
ARCHITECTURE.md
README.md
PLANS.md
docs/plans/active.md
docs/knowledge.json
```

Likely new files:

```text
cmd/onlava/grafana.go
cmd/onlava/grafana_provisioning.go
cmd/onlava/grafana_test.go
cmd/onlava/grafana_assets.go

internal/grafanaassets/
  dashboards/onlava-overview.json
  dashboards/onlava-logs.json
  dashboards/onlava-endpoint.json

docs/grafana.md
```

Current Victoria defaults from the existing implementation:

```text
VictoriaMetrics: 127.0.0.1:8428
VictoriaLogs:    127.0.0.1:9428
VictoriaTraces:  127.0.0.1:10428
```

The Victoria code exports OTLP endpoints and local Victoria URLs through environment variables such as `OTEL_EXPORTER_OTLP_*_ENDPOINT` and `ONLAVA_VICTORIA_*_URL`.

## Milestones

### Milestone 1: Contract and configuration

Add the public contract first.

Environment variables:

```sh
ONLAVA_DEV_GRAFANA=auto|1|0
ONLAVA_DEV_GRAFANA_DOWNLOAD=1|0
ONLAVA_GRAFANA_BIN=/path/to/grafana
ONLAVA_GRAFANA_VERSION=<version>
ONLAVA_GRAFANA_PORT=3000
ONLAVA_GRAFANA_DIR=.onlava/grafana
ONLAVA_GRAFANA_PLUGINS_PREINSTALL_SYNC=<comma-separated plugin ids>
```

The suggested default listen address is:

```text
127.0.0.1:3000
```

Port conflict handling is required because many developers already have Grafana on `3000`.

Add status fields to `onlava dev --json` and dev dashboard state:

```json
{
  "grafana": {
    "enabled": true,
    "status": "ready",
    "url": "http://127.0.0.1:3000",
    "config_path": ".onlava/grafana/conf/grafana.ini",
    "provisioning_path": ".onlava/grafana/provisioning",
    "dashboards_path": ".onlava/grafana/dashboards",
    "datasources": {
      "metrics": "onlava-victoriametrics",
      "logs": "onlava-victorialogs",
      "traces": "onlava-victoriatraces-jaeger"
    }
  }
}
```

Statuses:

```text
disabled
starting
ready
degraded
unavailable
external
```

Recommended environment semantics:

```text
ONLAVA_DEV_GRAFANA=auto  start Grafana when Victoria is enabled and Grafana can be resolved or downloaded
ONLAVA_DEV_GRAFANA=1     require Grafana; report degraded/error if unavailable
ONLAVA_DEV_GRAFANA=0     disable Grafana entirely
```

### Milestone 2: Process supervision

Implement Grafana as a sibling to the Victoria sidecar stack.

Resolution order:

```text
1. ONLAVA_GRAFANA_BIN
2. .onlava/grafana/bin/grafana
3. PATH lookup
4. download, when ONLAVA_DEV_GRAFANA_DOWNLOAD=1
```

Start command:

```sh
grafana server \
  --homepath <grafanaHome> \
  --config <appRoot>/.onlava/grafana/conf/grafana.ini
```

Supervision requirements:

* Bind to loopback only.
* Stop child Grafana when `onlava dev` exits.
* Do not kill externally running Grafana.
* If the port is occupied, health-check it before deciding whether to reuse or choose another port.
* Treat Grafana startup failure as non-fatal when `ONLAVA_DEV_GRAFANA=auto`.
* Treat Grafana startup failure as degraded/error when `ONLAVA_DEV_GRAFANA=1`.

Health check:

```text
GET /api/health
```

### Milestone 3: Provisioning generation

Generate `grafana.ini`:

```ini
[server]
http_addr = 127.0.0.1
http_port = 3000

[paths]
data = .onlava/grafana/data
logs = .onlava/grafana/logs
plugins = .onlava/grafana/plugins
provisioning = .onlava/grafana/provisioning

[auth.anonymous]
enabled = true
org_role = Viewer

[auth]
disable_login_form = true

[plugins]
preinstall_sync = victoriametrics-metrics-datasource@0.24.0,victoriametrics-logs-datasource@0.27.1
```

For local dev, anonymous Viewer is enough if dashboards are provisioned and immutable. Use Admin only if the implementation intentionally supports developers editing and exporting dashboards from the local instance.

Generate datasource provisioning:

```yaml
apiVersion: 1

datasources:
  - name: onlava VictoriaMetrics
    uid: onlava-victoriametrics
    type: victoriametrics-metrics-datasource
    access: proxy
    url: http://127.0.0.1:8428
    isDefault: true
    editable: false

  - name: onlava VictoriaLogs
    uid: onlava-victorialogs
    type: victoriametrics-logs-datasource
    access: proxy
    url: http://127.0.0.1:9428
    editable: false

  - name: onlava VictoriaTraces
    uid: onlava-victoriatraces-jaeger
    type: jaeger
    access: proxy
    url: http://127.0.0.1:10428/select/jaeger
    editable: false
```

Only include the traces datasource when VictoriaTraces is enabled and healthy.

Generate dashboard provider:

```yaml
apiVersion: 1

providers:
  - name: onlava
    orgId: 1
    folder: onlava
    type: file
    disableDeletion: false
    allowUiUpdates: false
    updateIntervalSeconds: 30
    options:
      path: .onlava/grafana/dashboards
```

### Milestone 4: First dashboards

Keep the first dashboard set small and reliable. Avoid ambitious dashboards until real local metric/log names are confirmed.

Ship three dashboards:

```text
onlava-dev-overview
onlava-dev-logs
onlava-dev-endpoint
```

Dashboard 1, `onlava-dev-overview`, answers "is my app healthy right now?" with process/app up status, request rate, error rate, latency percentiles, recent error logs, recent warning logs, top endpoints by request count, and top endpoints by latency.

Dashboard 2, `onlava-dev-logs`, makes VictoriaLogs usable immediately with a log stream, filters by level/service/endpoint/trace ID, count by level over time, and an error log table with message, timestamp, trace ID, route, and source.

Dashboard 3, `onlava-dev-endpoint`, debugs one route or handler with variables for service, endpoint, method, and status. Panels should include requests over time, p50/p95/p99 latency, errors by status code, logs for the selected endpoint, and trace IDs seen for the selected endpoint.

Use stable dashboard UIDs:

```text
onlava-dev-overview
onlava-dev-logs
onlava-dev-endpoint
```

Do not overfit to speculative metric names. The first implementation should use the real emitted metric/log fields from onlava's current OTLP/Victoria pipeline and add tests around those names.

### Milestone 5: Onlava dashboard and CLI integration

Add a Grafana card to the onlava dashboard:

```text
Grafana
  Status: ready
  URL: http://127.0.0.1:3000
  Datasources:
    Metrics: ready
    Logs: ready
    Traces: ready/degraded
  Dashboards:
    Overview
    Logs
    Endpoint
```

Add deep links:

```text
Open Grafana
Open Overview
Open Logs
Open Endpoint Debugger
```

Add dev event stream entries:

```json
{
  "type": "grafana.starting",
  "url": "http://127.0.0.1:3000"
}
```

```json
{
  "type": "grafana.ready",
  "url": "http://127.0.0.1:3000",
  "dashboards": [
    "onlava-dev-overview",
    "onlava-dev-logs",
    "onlava-dev-endpoint"
  ]
}
```

```json
{
  "type": "grafana.degraded",
  "reason": "victoriametrics datasource plugin unavailable"
}
```

### Milestone 6: Tests and harness

Unit tests:

```sh
go test ./cmd/onlava -run Grafana
```

Coverage:

* Env parsing.
* Path generation.
* Port selection.
* Config rendering.
* Datasource YAML rendering.
* Dashboard provider rendering.
* Disabled mode.
* Degraded mode.
* Existing Grafana-on-port behavior.
* Victoria disabled behavior.

Integration-style tests with a fake process runner should cover starting Grafana, waiting for health, emitting ready status, stopping on supervisor shutdown, and not killing an external process.

Optional live smoke behind an environment variable:

```sh
ONLAVA_TEST_GRAFANA=1 go test ./cmd/onlava -run TestGrafanaLiveSmoke
```

Acceptance smoke:

```sh
go test ./...
go install ./cmd/onlava
ONLAVA_DEV_GRAFANA=1 onlava dev --json
onlava harness self --json --write
```

If the UI harness is available for this surface:

```sh
onlava harness ui --json
```

Frontend validation when touching the dashboard UI:

```sh
cd ui
bun run typecheck
bun run build
```

## Plan of Work

1. Create the plan file.

   Add `docs/plans/0033-grafana-dev-integration.md` and update `docs/plans/active.md`.

2. Add the Grafana config model.

   Create a small internal model:

   ```go
   type grafanaConfig struct {
       Enabled              bool
       Required             bool
       Version              string
       BinPath              string
       RootDir              string
       Port                 int
       URL                  string
       DataDir              string
       LogsDir              string
       PluginsDir           string
       ProvisioningDir      string
       DashboardsDir        string
       VictoriaMetricsURL   string
       VictoriaLogsURL      string
       VictoriaTracesURL    string
       MetricsDatasourceUID string
       LogsDatasourceUID    string
       TracesDatasourceUID  string
   }
   ```

3. Add the provisioning renderer.

   Functions:

   ```go
   renderGrafanaINI(cfg grafanaConfig) ([]byte, error)
   renderGrafanaDatasources(cfg grafanaConfig) ([]byte, error)
   renderGrafanaDashboardProvider(cfg grafanaConfig) ([]byte, error)
   writeGrafanaProvisioning(ctx context.Context, cfg grafanaConfig) error
   ```

   Writes should be atomic.

4. Add the Grafana process supervisor.

   Mirror the Victoria style:

   ```go
   type grafanaComponent struct {
       cfg grafanaConfig
       cmd *exec.Cmd
   }

   func startGrafana(ctx context.Context, cfg grafanaConfig) (*grafanaComponent, error)
   func waitGrafanaReady(ctx context.Context, url string) error
   func stopGrafana(ctx context.Context, g *grafanaComponent) error
   ```

5. Wire Grafana into `onlava dev`.

   Startup order:

   ```text
   app root resolution
   Victoria sidecars
   Grafana provisioning generation
   Grafana start
   dashboard state publish
   app start/rebuild loop
   ```

   Grafana can start after Victoria URLs are known. It does not need to block app startup in `auto` mode.

6. Add dashboards.

   Embed static JSON dashboard templates. Keep variables and datasource references stable:

   ```json
   {
     "uid": "onlava-dev-overview",
     "title": "onlava dev overview"
   }
   ```

   Datasource references should use UIDs, not names.

7. Expose Grafana in dashboard/UI state.

   Add dashboard state:

   ```go
   type GrafanaState struct {
       Enabled          bool
       Status           string
       URL              string
       OverviewURL      string
       LogsURL          string
       EndpointURL      string
       ConfigPath       string
       ProvisioningPath string
       Message          string
   }
   ```

8. Update docs.

   Update:

   ```text
   README.md
   ARCHITECTURE.md
   docs/local-contract.md
   docs/knowledge.json
   ```

   Add:

   ```text
   docs/grafana.md
   ```

## Concrete Steps

### Step 1: Add the plan

Create:

```sh
$EDITOR docs/plans/0033-grafana-dev-integration.md
```

Update active plan pointer:

```sh
$EDITOR docs/plans/active.md
```

### Step 2: Implement config/provisioning without starting Grafana

Add:

```text
cmd/onlava/grafana.go
cmd/onlava/grafana_provisioning.go
cmd/onlava/grafana_test.go
```

First tests should snapshot-render:

```text
grafana.ini
provisioning/datasources/onlava.yaml
provisioning/dashboards/onlava.yaml
```

### Step 3: Add process startup

Implement:

```text
resolveGrafanaBinary
downloadGrafanaBinary, if matching Victoria download conventions
startGrafana
waitGrafanaHealth
stopGrafana
```

Do not mix process logic with provisioning rendering.

### Step 4: Add dev supervisor wiring

In `onlava dev`, after Victoria sidecars are resolved:

```text
if grafana enabled:
    render provisioning
    start grafana
    publish state
else:
    publish disabled state
```

### Step 5: Add dashboard state

Expose Grafana status in the dev dashboard state model and add the Observability/Grafana card.

### Step 6: Add starter dashboards

Start with one dashboard if necessary:

```text
onlava-dev-overview
```

Then add:

```text
onlava-dev-logs
onlava-dev-endpoint
```

### Step 7: Add degraded-mode behavior

Cases:

```text
Victoria disabled
VictoriaLogs unavailable
Grafana binary missing
Grafana plugin install failed
Port occupied
Grafana health timeout
Offline mode with missing binary/plugin
```

Each case should produce a useful status message, not a crash in default/auto mode.

### Step 8: Update docs

Document:

```sh
ONLAVA_DEV_GRAFANA=1 onlava dev
ONLAVA_DEV_GRAFANA=0 onlava dev
ONLAVA_DEV_GRAFANA_DOWNLOAD=0 onlava dev
```

Document reset:

```sh
rm -rf .onlava/grafana
```

Document where files live:

```text
.onlava/grafana/conf/grafana.ini
.onlava/grafana/provisioning/
.onlava/grafana/dashboards/
.onlava/grafana/data/
.onlava/grafana/plugins/
```

## Validation and Acceptance

Required validation:

```sh
go test ./cmd/onlava
go test ./...
go install ./cmd/onlava
```

When UI changes are included:

```sh
cd ui
bun run typecheck
bun run build
```

Harness validation, when practical:

```sh
onlava harness self --json --write
```

Manual smoke:

```sh
ONLAVA_DEV_GRAFANA=1 onlava dev
```

Acceptance criteria:

* `onlava dev` can start Grafana locally.
* Grafana binds to loopback.
* Grafana stops when the dev supervisor exits.
* Grafana config is generated under `.onlava/grafana/`.
* VictoriaMetrics datasource is provisioned with UID `onlava-victoriametrics`.
* VictoriaLogs datasource is provisioned with UID `onlava-victorialogs`.
* VictoriaTraces datasource is provisioned with UID `onlava-victoriatraces-jaeger` when traces are enabled.
* At least one dashboard appears under the `onlava` folder in Grafana.
* The onlava dev dashboard shows Grafana status and links.
* `onlava dev --json` exposes Grafana URL/status.
* `ONLAVA_DEV_GRAFANA=0` fully disables the integration.
* `ONLAVA_DEV_VICTORIA=0` does not crash Grafana integration code.
* Missing Grafana binary does not prevent app startup in `auto` mode.
* Plugin installation failure produces degraded status.
* `onlava run` behavior is unchanged.

## Idempotence and Recovery

Provisioning must be safe to rerun.

Rules:

* Re-render generated config and provisioning files on every `onlava dev` start.
* Use atomic writes for config/provisioning/dashboard JSON.
* Keep Grafana data/plugins directories unless the user deletes `.onlava/grafana/`.
* Never delete user-edited external Grafana state.
* Never kill a Grafana process that onlava did not start.
* If port `3000` is occupied by a compatible Grafana, report `external` or choose another port according to the final contract.
* If the selected port is occupied by a non-Grafana process, choose another port or degrade with an actionable error.
* If plugin install fails, keep Grafana running but mark datasource/dashboard status degraded.
* If VictoriaLogs is unavailable, keep metrics dashboards working.
* If VictoriaTraces is unavailable, omit or degrade only trace links.

Recovery commands:

```sh
rm -rf .onlava/grafana
ONLAVA_DEV_GRAFANA=1 onlava dev
```

Offline deterministic mode:

```sh
ONLAVA_DEV_GRAFANA=1 \
ONLAVA_DEV_GRAFANA_DOWNLOAD=0 \
ONLAVA_GRAFANA_BIN=/path/to/grafana \
onlava dev
```

## Artifacts and Notes

New generated local artifacts:

```text
.onlava/grafana/conf/grafana.ini
.onlava/grafana/data/
.onlava/grafana/logs/
.onlava/grafana/plugins/
.onlava/grafana/provisioning/datasources/onlava.yaml
.onlava/grafana/provisioning/dashboards/onlava.yaml
.onlava/grafana/dashboards/onlava-overview.json
.onlava/grafana/dashboards/onlava-logs.json
.onlava/grafana/dashboards/onlava-endpoint.json
```

Stable datasource UIDs:

```text
onlava-victoriametrics
onlava-victorialogs
onlava-victoriatraces-jaeger
```

Stable dashboard UIDs:

```text
onlava-dev-overview
onlava-dev-logs
onlava-dev-endpoint
```

Suggested first CLI-visible message:

```text
Grafana ready: http://127.0.0.1:3000
```

Suggested degraded message:

```text
Grafana degraded: VictoriaLogs datasource plugin unavailable; metrics dashboard is available.
```

Implementation sequencing to follow:

1. Plan and contract: add this ExecPlan, environment variables, status model, and docs stub.
2. Provisioning renderer: generate deterministic `grafana.ini`, datasources, and dashboard provider files.
3. Process supervisor: start/stop Grafana reliably.
4. Dashboard card: expose Grafana status/link in onlava's own dashboard.
5. One tiny dashboard: ship `onlava-dev-overview` with conservative queries.
6. Logs dashboard: add VictoriaLogs once plugin readiness is robust.
7. Trace links: add Jaeger datasource/deep links only after metrics/logs are stable.
8. Default-on: flip from opt-in to `auto` once smoke tests are green.

Grafana should not become the primary onlava dashboard. Onlava's dashboard remains the fast, agent-friendly control plane; Grafana is the rich observability workbench launched from it.

Background references for implementation, not required to understand this plan:

```text
https://grafana.com/docs/grafana/latest/administration/provisioning/
https://grafana.com/docs/grafana/latest/setup-grafana/start-restart-grafana/
https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/
https://docs.victoriametrics.com/victoriametrics/integrations/grafana/datasource/
```

## Interfaces and Dependencies

External dependencies:

* Grafana local binary.
* VictoriaMetrics Grafana datasource plugin.
* VictoriaLogs Grafana datasource plugin.
* Grafana built-in Jaeger datasource for VictoriaTraces.
* Existing VictoriaMetrics/VictoriaLogs/VictoriaTraces sidecars.

No new Go dependency should be needed for the first implementation. Prefer stdlib process management, file rendering, YAML generation through existing project conventions, and embedded dashboard assets.

Public interface additions:

```sh
ONLAVA_DEV_GRAFANA
ONLAVA_DEV_GRAFANA_DOWNLOAD
ONLAVA_GRAFANA_BIN
ONLAVA_GRAFANA_VERSION
ONLAVA_GRAFANA_PORT
ONLAVA_GRAFANA_DIR
ONLAVA_GRAFANA_PLUGINS_PREINSTALL_SYNC
```

Generated Grafana config should not become a manually supported API. The stable API is:

```text
onlava dev behavior
environment variables
JSON/dev event fields
documented .onlava/grafana reset behavior
stable datasource/dashboard UIDs
```
