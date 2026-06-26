# Grafana Dev Integration

`scenery up` can supervise a local Grafana process alongside the local Victoria observability sidecars. When the local agent is active, the first dev session registers Grafana as a shared substrate under the agent state root, effectively per user/machine where that agent runs, and later sessions reuse it after verifying the expected datasource and dashboard UIDs. Grafana is dev-only: `scenery serve` does not start it, and Scenery does not install an OS-level Grafana or Victoria service for all users or all possible agent homes.

Generated files live under `.scenery/grafana/` by default when the agent is disabled. Shared agent Grafana state lives under the agent directory, parallel to the shared-agent Victoria substrate:

```text
.scenery/grafana/conf/grafana.ini
.scenery/grafana/provisioning/datasources/scenery.yaml
.scenery/grafana/provisioning/dashboards/scenery.yaml
.scenery/grafana/dashboards/
.scenery/grafana/data/
.scenery/grafana/logs/
.scenery/grafana/plugins/
```

Grafana is provisioned with stable datasource UIDs:

```text
scenery-victoriametrics
scenery-victorialogs
scenery-victoriatraces-jaeger
```

and stable dashboard UIDs:

```text
scenery-dev-overview
scenery-dev-logs
scenery-dev-endpoint
```

Environment controls:

```sh
SCENERY_DEV_GRAFANA=auto|1|0
SCENERY_DEV_GRAFANA_DOWNLOAD=1|0
SCENERY_GRAFANA_BIN=/path/to/grafana
SCENERY_GRAFANA_VERSION=13.0.2
SCENERY_GRAFANA_PORT=10429
SCENERY_GRAFANA_DIR=.scenery/grafana
SCENERY_GRAFANA_PUBLIC_URL=https://grafana.<session>.local.dev
SCENERY_GRAFANA_REUSE_EXTERNAL=1
SCENERY_GRAFANA_PRESERVE_GF_ENV=1
SCENERY_GRAFANA_DOWNLOAD_SHA256=<hex>
SCENERY_GRAFANA_PLUGINS_PREINSTALL_SYNC=victoriametrics-metrics-datasource@0.25.0,victoriametrics-logs-datasource@0.28.0
```

`auto` is the default. Missing Grafana or Victoria sidecars degrades the Grafana status without stopping the app. `SCENERY_DEV_GRAFANA=1` makes Grafana required for `scenery up` startup.

When the local HTTPS proxy is enabled without the agent, scenery computes the Grafana browser URL before writing `grafana.ini` and uses that URL as Grafana's `root_url`. Shared agent Grafana uses its direct loopback URL for provisioning, then each dev session advertises the matching proxy route after the proxy starts. `SCENERY_GRAFANA_PUBLIC_URL` can override the advertised browser URL.

scenery starts a managed Grafana by default. If another Grafana process is already listening on the configured port, `auto` mode chooses another loopback port when the port was not explicitly set. Explicit external reuse requires `SCENERY_GRAFANA_REUSE_EXTERNAL=1`, and any external or shared instance is only marked usable after scenery verifies the expected datasource and dashboard UIDs through Grafana's HTTP API.

The Grafana child process does not inherit ambient `GF_*` variables by default because they can override generated config. Set `SCENERY_GRAFANA_PRESERVE_GF_ENV=1` only for local debugging.

When automatic downloads are enabled, the manifest-pinned Grafana archive is extracted under `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`. scenery prefers `SCENERY_GRAFANA_BIN`, then the managed toolchain store, then a manifest-driven download. It does not use ambient `PATH` Grafana binaries. Custom download URLs are explicit local-testing escape hatches and can set `SCENERY_GRAFANA_DOWNLOAD_SHA256`.

`scenery up` also writes local ignore markers so downloaded binaries and local state stay out of git.

Default Grafana, Grafana plugin, and Victoria sidecar versions are pinned in `scenery.toolchain.json`. Use `scenery system toolchain list --json`, `scenery system toolchain sync --tool grafana --json`, and `scenery system toolchain verify --json` to inspect or repair the managed store.

The starter dashboards query scenery's emitted OTLP request-duration metric, `scenery_request_duration_seconds`, with labels such as `scenery_app`, `scenery_session_id`, `scenery_service`, `scenery_endpoint`, `scenery_trace_type`, and `scenery_is_error`. Generated dashboards include a `Session` variable populated from `scenery_session_id`.

Reset local Grafana state with:

```sh
rm -rf .scenery/grafana
```
