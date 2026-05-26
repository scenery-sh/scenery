# Grafana Dev Integration

`onlava dev` can supervise a local Grafana process alongside the local Victoria observability sidecars. Grafana is dev-only: `onlava run` does not start it.

Generated files live under `.onlava/grafana/` by default:

```text
.onlava/grafana/conf/grafana.ini
.onlava/grafana/provisioning/datasources/onlava.yaml
.onlava/grafana/provisioning/dashboards/onlava.yaml
.onlava/grafana/dashboards/
.onlava/grafana/data/
.onlava/grafana/logs/
.onlava/grafana/plugins/
```

Grafana is provisioned with stable datasource UIDs:

```text
onlava-victoriametrics
onlava-victorialogs
onlava-victoriatraces-jaeger
```

and stable dashboard UIDs:

```text
onlava-dev-overview
onlava-dev-logs
onlava-dev-endpoint
```

Environment controls:

```sh
ONLAVA_DEV_GRAFANA=auto|1|0
ONLAVA_DEV_GRAFANA_DOWNLOAD=1|0
ONLAVA_GRAFANA_BIN=/path/to/grafana
ONLAVA_GRAFANA_VERSION=13.0.1+security-01
ONLAVA_GRAFANA_PORT=10429
ONLAVA_GRAFANA_DIR=.onlava/grafana
ONLAVA_GRAFANA_PUBLIC_URL=https://grafana.<workspace>.localhost
ONLAVA_GRAFANA_REUSE_EXTERNAL=1
ONLAVA_GRAFANA_PRESERVE_GF_ENV=1
ONLAVA_GRAFANA_DOWNLOAD_SHA256=<hex>
ONLAVA_GRAFANA_PLUGINS_PREINSTALL_SYNC=victoriametrics-metrics-datasource@0.24.0,victoriametrics-logs-datasource@0.27.1
```

`auto` is the default. Missing Grafana or Victoria sidecars degrades the Grafana status without stopping the app. `ONLAVA_DEV_GRAFANA=1` makes Grafana required for `onlava dev` startup.

When the local HTTPS proxy is enabled, onlava computes the Grafana browser URL before writing `grafana.ini` and uses that URL as Grafana's `root_url`. `ONLAVA_GRAFANA_PUBLIC_URL` can override the advertised browser URL.

onlava starts a managed Grafana by default. If another Grafana process is already listening on the configured port, `auto` mode chooses another loopback port when the port was not explicitly set. Explicit external reuse requires `ONLAVA_GRAFANA_REUSE_EXTERNAL=1`, and the external instance is only marked usable after onlava verifies the expected datasource and dashboard UIDs through Grafana's HTTP API.

The Grafana child process does not inherit ambient `GF_*` variables by default because they can override generated config. Set `ONLAVA_GRAFANA_PRESERVE_GF_ENV=1` only for local debugging.

When automatic downloads are enabled, the Grafana archive is extracted under `.onlava/grafana/home/grafana-<version>/`. onlava prefers `ONLAVA_GRAFANA_BIN`, then the managed pinned version, then a fresh download, and only then a `PATH` fallback. Default downloads verify Grafana's `.sha256` checksum file; custom download URLs can set `ONLAVA_GRAFANA_DOWNLOAD_SHA256`.

`onlava dev` also writes local ignore markers so downloaded binaries and local state stay out of git.

Default Grafana, Grafana plugin, and Victoria sidecar versions are pinned in `internal/devtools/versions.json`. Environment variables override those pins for local testing.

The starter dashboards query onlava's emitted OTLP request-duration metric, `onlava_request_duration_seconds`, with labels such as `onlava_app`, `onlava_service`, `onlava_endpoint`, `onlava_trace_type`, and `onlava_is_error`.

Reset local Grafana state with:

```sh
rm -rf .onlava/grafana
```
