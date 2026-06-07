# Victoria Observability Sidecars

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

Current contract note, reviewed 2026-06-07: this completed plan uses the earlier
`onlava dev` wording. Current local app sessions start with `onlava up`; the
Victoria sidecar contract is now documented in `docs/local-contract.md`,
`docs/grafana.md`, and `docs/environment.md`.

## Purpose / Big Picture

onlava currently keeps local development traces, logs, and metrics in its own dashboard store. That is useful for agent-visible JSON and dashboard parity, but it is not the long-term storage/query engine we want.

This plan prepares `onlava dev` to supervise the VictoriaMetrics observability stack locally:

- VictoriaTraces for OTLP traces
- VictoriaLogs for OTLP logs
- VictoriaMetrics for OTLP metrics

The first milestone keeps SQLite parity. onlava continues writing and reading its current SQLite-backed dashboard data while `onlava dev` starts local Victoria sidecars by default and passes OTLP endpoint URLs to the app process. Runtime dual-write and dashboard read migration happen later.

## Progress

- [x] (2026-04-27 17:55Z) Created this ExecPlan and assigned historical ID 0003.
- [x] (2026-04-27 18:02Z) Start Victoria sidecars by default from `onlava dev` with environment-only escape hatches for CI/offline use.
- [x] (2026-04-27 18:02Z) Add a sidecar supervisor that locates or downloads Victoria binaries, starts them on local ports, stores data under `.onlava/`, and stops them with `onlava dev`.
- [x] (2026-04-27 18:02Z) Export OTLP endpoint environment variables to the app process while preserving SQLite reporting as the active dashboard path.
- [x] (2026-04-27 18:02Z) Add tests for endpoint construction, binary resolution, and lifecycle-safe command setup.
- [x] (2026-04-27 18:10Z) Add first-pass OTLP dual-write from the dashboard report path for trace summaries, log events, and request-duration metrics.
- [x] (2026-04-27 18:31Z) Add Victoria-backed query adapters for dashboard and `onlava inspect` while keeping SQLite fallback.
- [x] (2026-04-27 18:31Z) Harden binary download with checksum verification when release checksum assets are available.
- [x] (2026-04-27 18:31Z) Document the Victoria-plus-SQLite architecture in `ARCHITECTURE.md` and `docs/local-contract.md`.
- [x] (2026-04-27 18:52Z) Switch onlava-owned Victoria export to OTLP protobuf for traces, logs, and metrics.

## Surprises & Discoveries

- VictoriaTraces is designed as a binary/server, not a clean in-process Go library. Its single-node main wires package globals, command-line flags, process-level logging, metrics, HTTP serving, and signal handling. Treating it as a sidecar is the safer boundary.
- The first dual-write can live in the dashboard report path instead of the generated app runtime. This preserves the existing onlava report envelope, keeps SQLite parity exact, and lets Victoria export fail independently of app request handling.
- VictoriaTraces exposes enough Jaeger-compatible query surface for first-pass dashboard and inspect reads. onlava still keeps SQLite fallback because Victoria clear/delete semantics and full event reconstruction need more hardening.
- Victoria OTLP insert endpoints reject or partially reject JSON payloads. onlava needs to send protobuf-encoded OTLP envelopes even for its small built-in trace/log/metric export.

## Decision Log

- Decision: Run Victoria components as supervised local sidecar processes, not imported Go libraries.
  Rationale: Victoria binaries expose stable HTTP/OTLP contracts, while importing the server packages would couple onlava to global flags, process lifecycle, and a large dependency graph.
  Date/Author: 2026-04-27 / Codex

- Decision: Keep SQLite trace/log/metric parity during the first integration.
  Rationale: Existing dashboard and `onlava inspect` contracts stay stable while sidecars and OTLP export are introduced incrementally.
  Date/Author: 2026-04-27 / Codex

- Decision: Start Victoria sidecars by default instead of requiring CLI flags.
  Rationale: The desired local development posture is Victoria plus SQLite parity without extra user ceremony.
  Date/Author: 2026-04-27 / Codex

- Decision: Perform the first OTLP dual-write from `cmd/onlava/dashboard.go` after SQLite writes.
  Rationale: The dashboard already receives traces, logs, and derived request-duration data after runtime request handling. Exporting there keeps runtime changes small and makes Victoria failures non-blocking for app requests.
  Date/Author: 2026-04-27 / Codex

- Decision: Prefer Victoria reads and fall back to SQLite.
  Rationale: This exercises the intended backend by default while preserving existing dashboard and CLI contracts during migration.
  Date/Author: 2026-04-27 / Codex

- Decision: Use minimal standard-library OTLP protobuf encoding for onlava's built-in Victoria dual-write.
  Rationale: The exported envelope is tiny, and avoiding generated OTLP dependencies keeps the default sidecar integration aligned with the repo's minimal-dependency posture.
  Date/Author: 2026-04-27 / Codex

## Outcomes & Retrospective

Implemented. `onlava dev` now attempts VictoriaMetrics, VictoriaLogs, and
VictoriaTraces by default, stores their local data under `.onlava/victoria/`,
exports endpoint URLs to the app process, writes SQLite first for parity, and
best-effort exports onlava trace/log/metric reports to Victoria over OTLP
protobuf. Dashboard and inspect trace reads prefer VictoriaTraces with SQLite
fallback. A live smoke verified sidecar startup, endpoint export, Victoria trace
and metric ingestion, VictoriaLogs query output, and graceful sidecar shutdown.

## Context and Orientation

Current local observability flows through `runtime/devreport.go`, `runtime/dbtrace.go`, `runtime/consolelog.go`, `cmd/onlava/dashboard.go`, and `internal/devdash`. Runtime emits onlava-specific report envelopes. The dashboard server receives them at `devdash.ReportPath`, stores summaries/events/logs in SQLite, and notifies the dashboard UI.

The new sidecar orchestration belongs in `cmd/onlava` next to `devSupervisor`, and local proxy lifecycle. App runtime should only receive endpoint configuration for now. Later, runtime can add an OTLP exporter that uses the same endpoint environment variables.

Victoria local defaults:

- VictoriaMetrics: `127.0.0.1:8428`, metrics OTLP endpoint `/opentelemetry/v1/metrics`
- VictoriaLogs: `127.0.0.1:9428`, logs OTLP endpoint `/insert/opentelemetry/v1/logs`
- VictoriaTraces: `127.0.0.1:10428`, traces OTLP endpoint `/insert/opentelemetry/v1/traces`

## Milestones

Milestone 1 adds sidecar process management and app environment export. This is complete when `onlava dev` can locate or download binaries, start available components, expose endpoint env vars to the app, and stop child processes on exit while continuing to run if Victoria is unavailable.

Milestone 2 adds explicit binary download support. This is complete when `onlava dev --victoria --victoria-download` can download known community release archives for supported OS/architecture pairs into `.onlava/victoria/bin`.

Milestone 3 adds runtime OTLP dual-write while SQLite remains the dashboard source of truth. This is complete when traces are sent to VictoriaTraces and logs are sent to VictoriaLogs without changing existing dashboard behavior.

Milestone 4 adds query adapters and dashboard/CLI selection. This is complete when the dashboard and `onlava inspect traces|metrics` can read from Victoria backends with SQLite fallback.

## Plan of Work

Add a small sidecar abstraction in `cmd/onlava` with component specs for metrics, logs, and traces. It should be boring process supervision code: resolve binary, choose address, create storage directory, start command, capture output, and stop on supervisor close.

When sidecars are active, append OTLP endpoint environment variables to the generated app process:

- `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
- `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`
- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
- onlava-specific mirrors under `ONLAVA_VICTORIA_*`

Do not remove or bypass `ONLAVA_DEV_REPORT_URL`. SQLite parity remains active.

## Concrete Steps

Add files and tests:

    cmd/onlava/victoria.go
    cmd/onlava/victoria_test.go

Update existing files:

    cmd/onlava/main.go
    cmd/onlava/dev_supervisor.go
    cmd/onlava/console.go
    docs/plans/active.md
    docs/knowledge.json

Run focused validation:

    go test ./cmd/onlava
    onlava inspect docs --json
    go install ./cmd/onlava

Run full validation when the wider dirty tree is green:

    go test ./...
    onlava harness self --json --write

## Validation and Acceptance

Victoria sidecars are attempted by default during `onlava dev`.

Missing binaries produce a clear warning and do not disable SQLite local observability.

Missing binaries are downloaded into the configured Victoria bin dir when the platform and version are supported. Download failures warn and fall back to SQLite-only local observability.

Sidecars use local loopback addresses and storage directories under `.onlava/victoria/`.

`onlava dev` stops sidecars when the supervisor closes.

## Idempotence and Recovery

Downloaded binaries are stored under `.onlava/victoria/bin` and reused on later runs.

Storage directories are stable per app and per component. Re-running `onlava dev --victoria` should reuse existing local data.

If a default port is already in use, onlava may treat the component as externally running at that address and still export endpoint variables. It must not kill processes it did not start.

## Artifacts and Notes

Official docs:

- VictoriaTraces OTLP traces: `http://<victoria-traces>:10428/insert/opentelemetry/v1/traces`
- VictoriaLogs OTLP logs: `http://<victorialogs>:9428/insert/opentelemetry/v1/logs`
- VictoriaMetrics OTLP metrics: `http://<victoriametrics>:8428/opentelemetry/v1/metrics`

## Interfaces and Dependencies

No Go library dependency on Victoria packages should be added for sidecar supervision. Use the Go standard library to locate, download, extract, start, and stop binaries.

Environment overrides:

    ONLAVA_DEV_VICTORIA=0
    ONLAVA_DEV_VICTORIA_DOWNLOAD=0
    ONLAVA_DEV_VICTORIA_DIR=/path/to/cache
    ONLAVA_VICTORIA_METRICS_BIN=/path/to/victoria-metrics-prod
    ONLAVA_VICTORIA_LOGS_BIN=/path/to/victoria-logs-prod
    ONLAVA_VICTORIA_TRACES_BIN=/path/to/victoria-traces-prod
    ONLAVA_VICTORIA_METRICS_PORT=8428
    ONLAVA_VICTORIA_LOGS_PORT=9428
    ONLAVA_VICTORIA_TRACES_PORT=10428
