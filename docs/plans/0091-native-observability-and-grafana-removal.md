# 0091 - Native Observability Workbench and Grafana Removal

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Scenery is in the middle of a deliberate dependency and surface-area reduction. The recent and active direction is to replace large managed substrates with smaller Scenery-owned primitives: Postgres becomes service-scoped SQLite, DNS/domain routing becomes optional instead of default, Temporal server dependency is being reduced or replaced where possible, and the local developer surface is being made smaller, more deterministic, and more agent-friendly.

Grafana is the next candidate for removal.

Today Grafana is a dev-only visual workbench layered on top of Scenery's real observability data. It is not the source of truth. The source of truth is the Victoria substrate:

```text
VictoriaMetrics   request-duration metrics and metric labels/series
VictoriaLogs      structured runtime logs and dev-event logs
VictoriaTraces    trace summaries, spans, and span events through Jaeger-compatible APIs
```

The desired end state is:

```text
Scenery owns the observability data plane through Victoria.
Scenery owns the observability user experience through the console app.
Grafana is not downloaded, provisioned, supervised, proxied, verified, tested, documented, or advertised by Scenery.
```

After this plan is complete, `scenery up` must not start Grafana, must not download Grafana, must not provision Grafana datasources or dashboards, and must not expose Grafana status as part of the local app status contract. The Scenery console must become the default observability workbench for local debugging.

The new user-facing contract is:

```text
scenery up starts or reuses Victoria observability backends.
The console app displays logs, metrics, traces, and request correlation directly.
CLI JSON surfaces expose the same data for agents.
Grafana is no longer part of the supported Scenery local runtime.
```

This is not a UI-only refactor. It is a dependency-removal migration across runtime startup, toolchain manifest, environment registry, docs, agent/session status, dev dashboard state, React routes, tests, harness expectations, and local contracts.

The work must preserve the important debugging capabilities that Grafana currently provides:

```text
Overview:
  recent request count
  latest latency
  request duration over time
  error count over time
  recent warnings/errors
  request traces

Logs:
  log stream
  warning/error filtering
  current app/session scope
  trace/span correlation when present

Endpoint debugging:
  service/endpoint filter
  endpoint latency
  endpoint errors
  related logs
  related traces

Traces:
  recent trace list
  trace detail
  span summaries
  span events
  request traces
  workflow/Temporal traces where those still exist in the branch

Metrics:
  request-duration query
  label discovery
  series discovery
  scoped PromQL/MetricsQL query
```

The plan intentionally does not add another general-purpose dashboard dependency. Prefer a small Scenery-native UI and stable JSON contracts over embedding Grafana, adding a charting platform, or requiring agents to scrape a third-party web app.

## Progress

* [x] 2026-06-27 - Initial ExecPlan drafted for native observability workbench and managed Grafana removal.
* [x] 2026-06-27 - Add this file as `docs/plans/0091-native-observability-and-grafana-removal.md`.
* [x] 2026-06-27 - Link this ExecPlan from `docs/plans/active.md`.
* [x] 2026-06-27 - Add this ExecPlan to `docs/knowledge.json`.
* [x] 2026-06-27 - Re-run repository inventory for Grafana references before editing implementation code.
* [x] 2026-06-27 - Record the exact inventory results in `Surprises & Discoveries`.
* [x] 2026-06-27 - Implement native dashboard RPC/query surfaces needed by the console UI.
* [x] 2026-06-27 - Implement native console observability pages and remove Grafana handoff links.
* [x] 2026-06-27 - Remove managed Grafana startup, provisioning, substrate state, and status reporting.
* [x] 2026-06-27 - Remove Grafana toolchain artifacts, plugins, env vars, docs, and tests.
* [x] 2026-06-27 - Update local contracts, agent guide, schemas, environment registry, and harness expectations.
* [x] 2026-06-27 - Run validation and record exact outcomes.

Update this section at every meaningful stopping point. Every update must include the date, what changed, and whether validation was run.

## Surprises & Discoveries

Initial known facts from source review:

* `PLANS.md` requires every ExecPlan to be self-contained and to include the exact required section headings. Do not rely on chat history or hidden context.
* The user explicitly allocated `0091`. At plan-add time in this worktree, `docs/plans/active.md` did not contain `0090` and no `docs/plans/0090-local-path-routing.md` file existed.
* `docs/plans/0088-sqlite-service-databases.md` is active and targets Postgres removal. Grafana removal should avoid reintroducing database assumptions while that migration is in flight.
* `docs/plans/0090-local-path-routing.md` may exist on another implementation branch and, if present, changes local routing defaults. Grafana removal must not assume host/domain routing is the default path.
* Current `docs/grafana.md` says `scenery up` can supervise local Grafana alongside Victoria sidecars, that managed Grafana starts by default, and that it is provisioned with stable datasource and dashboard UIDs.
* Current `scenery.toolchain.json` contains a managed `grafana` binary artifact and Grafana datasource plugin artifacts for VictoriaMetrics and VictoriaLogs.
* Current `cmd/scenery/grafana.go` owns Grafana config, process startup, binary resolution, port conflict handling, readiness checks, external reuse checks, state reporting, and status messages.
* Current `cmd/scenery/grafana_provisioning.go` writes `grafana.ini`, datasource provisioning YAML, dashboard provider YAML, and dashboard JSON models.
* Current Grafana dashboards are mostly derived from one Scenery metric, `scenery_request_duration_seconds`, plus VictoriaLogs and VictoriaTraces queries.
* Current `ui/src/routes/traces.tsx` says the dashboard trace viewer is deprecated and that Grafana is the trace workbench. This plan reverses that ownership.
* Current `ui/src/routes/observability.tsx` renders Grafana status, datasource status, and dashboard links. It does not yet present the console as the primary workbench.
* Current `ui/src/lib/grafana.ts` is only link-selection glue for Grafana dashboards. This file should disappear.
* Current `ui/src/lib/types.ts` exposes `GrafanaState` under `AppStatus`. This should become a Scenery-native observability state or be replaced by a Victoria-oriented status type.
* Current `cmd/scenery/dashboard_rpc.go` already exposes Victoria-backed trace RPC methods:

  * `traces/list`
  * `traces/get`
  * `traces/spans/summaries/list`
  * `traces/spans/events/list`
  * `traces/clear`
* Current `cmd/scenery/victoria_query.go` already queries VictoriaTraces through Jaeger-compatible endpoints.
* Current `internal/observability/query.go` already contains scoped VictoriaLogs and VictoriaMetrics query helpers with versioned JSON result schemas.
* Current `cmd/scenery/dashboard.go` exports incoming trace summaries, trace events, metrics, and logs to Victoria backends. Grafana does not receive data directly.
* Current `cmd/scenery/dashboard_graphql.go` only handles stored request GraphQL operations. Native observability UI should use the existing WebSocket JSON-RPC pattern unless there is a strong reason to expand local GraphQL.
* Current `cmd/scenery/dashboard.go` still has Postgres-specific `db/query` and `db/transaction` paths in the inspected branch. Do not couple this Grafana removal to the SQLite migration except where status/docs need to avoid stale Postgres wording.
* Current `scenery.toolchain.json` in the inspected branch still contains `dnsmasq` and `postgres` artifacts. Those may already be removed in the implementation branch. Re-run inventory locally before editing.

Add new discoveries here with the command, test, or file that exposed them. Use this section to record unexpected compile failures, hidden Grafana dependencies, UI assumptions, stale docs, or harness contract issues.

Suggested inventory commands:

```sh
git grep -n -i 'grafana' -- .
git grep -n 'SCENERY_DEV_GRAFANA\|SCENERY_GRAFANA' -- .
git grep -n 'victoriametrics-metrics-datasource\|victoriametrics-logs-datasource' -- .
git grep -n 'scenery-dev-overview\|scenery-dev-logs\|scenery-dev-endpoint\|scenery-dev-temporal' -- .
git grep -n 'GrafanaState\|grafanaState\|SubstrateGrafana' -- .
git grep -n 'trace workbench\|Open in Grafana\|Grafana is' -- ui docs cmd internal
```

Record whether the inventory was run on `main`, an implementation branch, or a local worktree with cleanup commits applied.

2026-06-27 current worktree inventory before implementation edits:

* Command: `go run ./cmd/scenery inspect docs --json | jq '.summary'`. Result: 66 documents, 0 missing, 0 review due, 0 stale, 1 AGENTS scope.
* Command: `git grep -l -i 'grafana' -- .`. Result: current Grafana references exist in `AGENTS.md`, `ARCHITECTURE.md`, `README.md`, `SKILL.md`, `cmd/scenery/agent_dashboard.go`, `cmd/scenery/console.go`, `cmd/scenery/dashboard_service_routes.go`, `cmd/scenery/dev_console.go`, `cmd/scenery/dev_supervisor.go`, `cmd/scenery/dev_supervisor_test.go`, `cmd/scenery/grafana.go`, `cmd/scenery/grafana_provisioning.go`, `cmd/scenery/grafana_test.go`, harness files, watch files, docs, schemas, `internal/agent`, `internal/devdash`, `internal/devtools`, `internal/localproxy`, `internal/parse`, `internal/toolchain`, `runtime/registry.go`, `scenery.toolchain.json`, and UI routes/libs.
* Command: `git grep -l 'SCENERY_DEV_GRAFANA\|SCENERY_GRAFANA' -- .`. Result: env-var surface is in `README.md`, `cmd/scenery/grafana.go`, `cmd/scenery/grafana_test.go`, `docs/environment.md`, `docs/environment.registry.json`, `docs/grafana.md`, `docs/local-contract.md`, and historical plans.
* Command: `git grep -l 'victoriametrics-metrics-datasource\|victoriametrics-logs-datasource' -- .`. Result: plugin surface is in Grafana provisioning/tests/docs plus `internal/toolchain/manifest_gen.go` and `scenery.toolchain.json`.
* Command: `git grep -l 'scenery-dev-overview\|scenery-dev-logs\|scenery-dev-endpoint\|scenery-dev-temporal' -- .`. Result: dashboard UID surface is in Grafana runtime/docs/local contract and local proxy tests.
* Command: `git grep -l 'GrafanaState\|grafanaState\|SubstrateGrafana' -- .`. Result: status/substrate types are in `cmd/scenery/agent_dashboard.go`, `cmd/scenery/console.go`, `cmd/scenery/dev_supervisor.go`, `cmd/scenery/grafana.go`, `cmd/scenery/grafana_test.go`, `internal/agent/types.go`, `internal/devdash/types.go`, and UI types/helpers.
* Command: `git grep -n -i 'grafana' -- '*_test.go' 'ui/**/*.test.*'`. Result: Grafana-specific tests are concentrated in `cmd/scenery/grafana_test.go`, `cmd/scenery/dev_supervisor_test.go`, `cmd/scenery/toolchain_test.go`, `cmd/scenery/harness_test.go`, `cmd/scenery/watch_test.go`, `internal/agent/agent_test.go`, `internal/localproxy/proxy_test.go`, `internal/parse/relocated_unit_test.go`, and integration helper tests.

2026-06-27 implementation discoveries:

* The only dashboard RPC addition needed for this slice was a small `observability/status` method; trace lists, trace details, log query, and metrics query already existed through Scenery-owned dashboard/CLI surfaces.
* Removing `cmd/scenery/grafana.go` exposed a few generic helpers that were not Grafana-specific: `freeLoopbackPort`, managed toolchain artifact sync/status helpers, `atomicWriteFile`, and a platform test helper. These were moved into generic files before deleting the Grafana implementation.
* `ui/node_modules` was absent in this worktree. `bun install --frozen-lockfile` restored local dependencies without changing `ui/bun.lock`.
* Command: `go test ./cmd/scenery ./internal/devdash ./internal/agent ./internal/localproxy ./internal/parse ./internal/devtools ./internal/toolchain ./internal/envpolicy ./runtime`. Result: pass.
* Command: `cd ui && bun run typecheck && bun run test && bun run build`. Result: pass; Vite emitted existing Lightning CSS warnings for Tailwind `@theme`/`@tailwind` at-rules, then built successfully.
* Command: `rg -n "GF_|SCENERY_DEV_GRAFANA|SCENERY_GRAFANA|grafana|Grafana|victoriametrics-(metrics|logs)-datasource" cmd internal runtime docs/schemas scenery.toolchain.json ui/src README.md SKILL.md ARCHITECTURE.md docs/index.md docs/environment.md docs/environment.registry.json docs/local-contract.md docs/agent-guide.md docs/app-development-cookbook.md`. Result: no matches.

## Decision Log

* Decision: Remove managed Grafana from the Scenery local runtime instead of keeping it disabled-by-default.

  Rationale: A disabled-but-present integration still keeps toolchain artifacts, env vars, docs, tests, status fields, and mental-model weight alive. The goal of this cleanup is surface reduction, not just changing a default.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Make the Scenery console the primary observability workbench.

  Rationale: Scenery already owns the data in Victoria and can expose a smaller, purpose-built UI. Agents can modify Scenery UI quickly, and a repo-local console is easier to test, document, and automate than a provisioned third-party dashboard.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Keep Victoria as the observability substrate.

  Rationale: Victoria is where Scenery already writes logs, metrics, and traces. Removing Grafana should not discard the storage/query substrate or require inventing a new data store.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Represent dashboard observability status as `AppStatus.observability` with Victoria metrics/logs/traces backend fields instead of reusing `GrafanaState`.

  Rationale: The UI and JSON status should describe Scenery's current workbench directly. A renamed or degraded Grafana-shaped type would keep the removed integration in the data model.

  Date: 2026-06-27

  Author: implementation

* Decision: Do not add a new frontend charting or query dependency in this removal slice.

  Rationale: The goal is to remove the third-party dashboard surface and keep the first native UI small. Existing trace tables, request links, output summaries, and backend status cards are enough to preserve a useful workbench while the CLI/query surfaces remain available.

  Date: 2026-06-27

  Author: implementation

* Decision: Do not embed Grafana in the console through iframe or proxy tricks.

  Rationale: Embedding would keep the dependency, security surface, version skew, plugin provisioning, and startup failure modes. It would not satisfy the cleanup goal.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Prefer no new frontend dependency for charts in the first implementation.

  Rationale: The cleanup should shrink dependencies. Simple time-series displays can be implemented with SVG, table views, and small React components. If a chart library becomes necessary, record a new decision with size, license, lockfile impact, and why raw SVG was insufficient.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Use existing dashboard WebSocket JSON-RPC patterns for native observability UI queries.

  Rationale: The dashboard already has JSON-RPC methods for status, traces, API calls, DB calls, editors, and stored state. Adding observability RPCs keeps the UI architecture consistent and avoids expanding the local GraphQL compatibility shim.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Keep CLI observability JSON as an agent contract, and make UI use equivalent backend logic.

  Rationale: Agents need stable JSON surfaces and should not scrape the console DOM. The UI and CLI should share scope resolution and Victoria query normalization where possible, not diverge into separate query semantics.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Remove Grafana env vars from the current contract.

  Rationale: Variables such as `SCENERY_DEV_GRAFANA`, `SCENERY_GRAFANA_BIN`, and `SCENERY_GRAFANA_PORT` would imply a supported managed Grafana surface. Keeping them as no-op compatibility aliases preserves confusion. If the implementation chooses to emit one-release warnings, record that as a temporary compatibility exception and remove it before the plan completes unless explicitly approved.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Preserve optional manual external-Grafana use only as a non-Scenery escape hatch.

  Rationale: Advanced users can point their own Grafana at Victoria endpoints if they want. Scenery should expose Victoria URLs through inspect/ps/status surfaces, but should not provision or verify external Grafana.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Delete generated Grafana dashboard model code instead of translating it one-to-one into UI.

  Rationale: The current dashboard JSON is optimized for Grafana panel definitions. The console UI should be designed around Scenery workflows: failing request, endpoint latency, related logs, trace/span timeline, and current session scope.

  Date: 2026-06-27

  Author: initial ExecPlan

* Decision: Keep historical completed plans that mention Grafana, but remove active/current docs that advertise managed Grafana.

  Rationale: Historical plans are useful provenance. Current local-contract, environment, agent guide, README, and dashboard docs should describe the supported present-day behavior.

  Date: 2026-06-27

  Author: initial ExecPlan

When implementation chooses exact RPC method names, status schema shape, UI route layout, chart rendering technique, or compatibility warning behavior, append new decision entries with date, rationale, and author.

## Outcomes & Retrospective

Outcome:

* Managed Grafana was removed from local development. `scenery up` no longer starts, provisions, downloads, routes, or reports Grafana.
* App/session dashboard status now exposes native `observability` state for Victoria metrics, logs, and traces instead of `grafana`.
* The dashboard Observability, Traces, Requests, Services, Cron, and Home routes now use Scenery-native links and status. `ui/src/lib/grafana.ts` was deleted.
* Grafana binary/plugin artifacts were removed from `scenery.toolchain.json` and regenerated toolchain metadata. Grafana env vars were removed from the environment registry and human docs.
* Current docs and root agent instructions now describe Victoria plus the Scenery console/CLI as the local observability surface. Historical completed plans remain as provenance.
* A separate pre-existing active plan contract issue was fixed by adding the required living-document statement to `docs/plans/0088-sqlite-service-databases.md`; this was necessary for self-harness knowledge-contract validation.

Validation:

* `jq empty scenery.toolchain.json docs/knowledge.json docs/schemas/scenery.config.v1.schema.json docs/environment.registry.json`: pass.
* `go test ./cmd/scenery ./internal/devdash ./internal/agent ./internal/localproxy ./internal/parse ./internal/devtools ./internal/toolchain ./internal/envpolicy ./runtime`: pass.
* `bun install --frozen-lockfile` in `ui/`: pass; restored local `node_modules` without lockfile changes.
* `cd ui && bun run typecheck && bun run test && bun run build`: pass; Vite emitted existing Lightning CSS warnings for Tailwind `@theme`/`@tailwind` at-rules, then built successfully.
* `go run ./cmd/scenery inspect docs --json | jq '.summary'`: pass with 65 docs, 0 missing, 0 review due, 0 stale, 1 AGENTS scope.
* `go test ./...`: pass.
* `go run ./cmd/scenery harness self --summary --write`: pass with warnings. Accepted warnings are existing large-file architecture warnings and slow-test timing warnings; no harness errors remained.
* `git diff --check`: pass.
* Final active-surface grep for `Grafana`, `grafana`, `SCENERY_DEV_GRAFANA`, `SCENERY_GRAFANA`, and removed Victoria datasource plugin names is clean across current code/docs. The only current grep hits are the 0091 plan metadata in `docs/knowledge.json`.
* Live dashboard proof with a worktree-local built binary: `scripts/build-dashboard-ui-embed.sh && go build -o .scenery/live-proof/bin/scenery ./cmd/scenery && SCENERY_AGENT_HOME="$PWD/.scenery/live-proof/agent-home" .scenery/live-proof/bin/scenery harness ui --json --write --app-root testdata/apps/basic`: pass. Evidence is under `testdata/apps/basic/.scenery/harness/ui/latest.json`; Observability rendered `ObservabilityRoute` and `ObservabilityBackends`, Traces rendered a trace table or intentional empty state, and Requests rendered the API Explorer request form.
* Runtime/status proof with the same built binary: detached `testdata/apps/basic`, waited for `running`, queried `ps --json` and dashboard JSON-RPC. Routes were `api` and `dashboard`; the removed route key was absent; live status exposed native `observability` and did not expose the removed status field. Evidence is under `.scenery/live-proof/runtime-proof.json` and `.scenery/live-proof/rpc-proof.json`.
* After updating the UI harness expectations for native Observability markers, `go test ./cmd/scenery`: pass.

Follow-up:

* The first native dashboard slice intentionally shows backend status, trace lists/details, request trace links, and recent output rather than translating every old Grafana panel into charts.
* Broader trace timeline/span event visualization can build on the existing Victoria-backed JSON-RPC methods without reintroducing a third-party dashboard dependency.

## Context and Orientation

Read these files before editing:

```text
AGENTS.md
PLANS.md
docs/plans/active.md
docs/plans/0067-cli-observability-query.md
docs/plans/0079-victoria-shared-substrate-visibility.md
docs/plans/0088-sqlite-service-databases.md
docs/plans/0090-local-path-routing.md (if present on the implementation branch)
docs/local-contract.md
docs/agent-guide.md
docs/environment.md
docs/environment.registry.json
docs/schemas/scenery.config.v1.schema.json
scenery.toolchain.json
cmd/scenery/dev_supervisor.go
cmd/scenery/grafana.go
cmd/scenery/grafana_provisioning.go
cmd/scenery/victoria.go
cmd/scenery/victoria_query.go
cmd/scenery/victoria_export.go
cmd/scenery/inspect_observability.go
cmd/scenery/observability_commands.go
cmd/scenery/logs.go
cmd/scenery/dashboard.go
cmd/scenery/dashboard_rpc.go
cmd/scenery/dashboard_traces.go
cmd/scenery/dashboard_graphql.go
cmd/scenery/agent.go
cmd/scenery/agent_status_table.go
cmd/scenery/dev_substrate_manager.go
internal/agent/types.go
internal/agent/session.go
internal/agent/router.go
internal/devdash/types.go
internal/devdash/store.go
internal/observability/query.go
ui/package.json
ui/bun.lock
ui/src/lib/types.ts
ui/src/lib/grafana.ts
ui/src/lib/dashboard-context.tsx
ui/src/lib/utils.ts
ui/src/routes/observability.tsx
ui/src/routes/traces.tsx
ui/src/routes/home.tsx
ui/src/routes/requests.tsx
ui/src/routes/services.tsx
```

The repo's root `AGENTS.md` is authoritative for agent behavior. Before editing non-trivial code, read the root instructions and any narrower `AGENTS.md` files for the directories being edited. Do not spawn subagents or background tasks. Do all implementation, exploration, and validation in the main session.

Current Grafana mental model to remove:

```text
scenery up
  starts Victoria sidecars or reuses shared-agent Victoria
  starts/provisions Grafana when enabled
  writes .scenery/grafana or agent Grafana state
  provisions datasources:
    scenery-victoriametrics
    scenery-victorialogs
    scenery-victoriatraces-jaeger
  provisions dashboards:
    scenery-dev-overview
    scenery-dev-logs
    scenery-dev-endpoint
    scenery-dev-temporal
  reports Grafana status to the dashboard
  UI links users to Grafana for trace/observability workflows
```

Target native observability mental model:

```text
scenery up
  starts or reuses Victoria
  does not start Grafana
  reports Victoria observability status to the dashboard

console app
  shows observability status
  shows request metrics
  shows logs
  shows trace list and trace detail
  links API call results to traces/logs
  uses Scenery JSON-RPC backed by Victoria queries

CLI
  exposes the same data through stable JSON:
    scenery inspect observability --json
    scenery logs query --json
    scenery logs tail --jsonl
    scenery metrics query --json
    scenery metrics labels --json
    scenery metrics series --json
    scenery traces list --json
```

Important current backend capabilities:

```text
cmd/scenery/dashboard.go
  handleReport exports trace-summary, trace-event, and log reports to Victoria.
  apiCall returns X-Scenery-Trace-Id from app responses.

cmd/scenery/victoria_export.go
  exports OTLP traces to VictoriaTraces.
  exports OTLP logs to VictoriaLogs.
  exports request-duration metrics to VictoriaMetrics.

cmd/scenery/victoria_query.go
  queries VictoriaTraces through /select/jaeger/api/traces.
  gets specific trace details.
  builds devdash.TraceSummary values from Victoria spans.

cmd/scenery/dashboard_rpc.go
  exposes trace list/get/span methods over dashboard JSON-RPC.

internal/observability/query.go
  implements scoped VictoriaLogs query/tail.
  implements VictoriaMetrics query/labels/series.
  normalizes logs and metrics into stable JSON result records.
```

Current UI gaps to close:

```text
ui/src/routes/traces.tsx
  currently hands off to Grafana and only shows a lightweight recent-traces table.

ui/src/routes/observability.tsx
  currently shows Grafana status and dashboard links.

ui/src/lib/types.ts
  currently models GrafanaState rather than a Scenery-native observability state.

ui/src/lib/grafana.ts
  only computes Grafana dashboard URLs and should be deleted.

ui/src/lib/dashboard-context.tsx
  likely owns status and trace loading. Extend it or nearby hooks to load logs,
  metrics, trace spans, and trace events through JSON-RPC.
```

Terminology for this plan:

```text
Native observability workbench:
  The Scenery console pages and components that display logs, metrics, traces,
  and request correlation without Grafana.

Victoria backend:
  One VictoriaMetrics, VictoriaLogs, or VictoriaTraces component reachable through
  the active Victoria stack.

Observability state:
  A dashboard status object describing whether Victoria metrics/logs/traces are
  enabled and reachable, plus URLs and query capabilities. It replaces GrafanaState.

Trace summary:
  A compact row for a trace or span: trace_id, span_id, type, status, service,
  endpoint, start, duration, message_id, parent_span_id.

Span event:
  A trace event payload normalized by Scenery for display in the console.

Request correlation:
  The flow from an API call response trace ID to trace details, span events,
  logs with the same trace/span IDs, and metrics for the related service/endpoint.
```

## Milestones

### Milestone 0 - Plan, inventory, and safety rails

Add this ExecPlan, link it from `docs/plans/active.md`, and index it in `docs/knowledge.json`. Run a fresh Grafana inventory before touching implementation code. Record exact results in `Surprises & Discoveries`.

Successful milestone result:

```text
docs/plans/0091-native-observability-and-grafana-removal.md exists.
docs/plans/active.md links it.
docs/knowledge.json indexes it.
A fresh Grafana inventory is recorded in this plan.
No implementation files changed yet except plan/index files.
```

### Milestone 1 - Native observability status model

Replace the dashboard's Grafana-oriented status with a Scenery-native observability status. This status should describe Victoria backend readiness and query capability. It should not mention Grafana.

Target shape, to be refined during implementation:

```go
type ObservabilityState struct {
    Enabled bool `json:"enabled"`

    Backend string `json:"backend"` // "victoria"

    Metrics ObservabilityBackendState `json:"metrics"`
    Logs    ObservabilityBackendState `json:"logs"`
    Traces  ObservabilityBackendState `json:"traces"`

    CurrentSessionID string `json:"current_session_id,omitempty"`
    Scope            any    `json:"scope,omitempty"`

    Message string `json:"message,omitempty"`
}

type ObservabilityBackendState struct {
    Enabled   bool   `json:"enabled"`
    Available bool   `json:"available"`
    Status    string `json:"status"` // ready, degraded, disabled, unavailable
    URL       string `json:"url,omitempty"`
    QueryURL  string `json:"query_url,omitempty"`
    Dialect   string `json:"dialect,omitempty"`
    Message   string `json:"message,omitempty"`
}
```

The exact Go package and field shape may differ, but the implementation must satisfy these semantics:

```text
status.observability exists.
status.grafana does not exist in the final state.
The UI can tell whether logs/metrics/traces are available.
The UI can show useful degraded messages.
Agents can use JSON status without scraping text.
```

Successful milestone result:

```text
AppStatus has a native observability status shape.
GrafanaState is no longer used by new UI code.
Existing status tests are updated.
A degraded or disabled Victoria backend renders a useful UI message.
```

### Milestone 2 - Dashboard RPCs for logs and metrics

The console needs native access to VictoriaLogs and VictoriaMetrics. Add JSON-RPC methods that reuse `internal/observability` where possible.

Proposed RPC methods:

```text
observability/status
observability/logs/query
observability/logs/tail/start       optional; use polling first if streaming is too much
observability/metrics/query
observability/metrics/labels
observability/metrics/series
```

The first implementation may avoid live tail streaming if polling is simpler and reliable. The workbench must at least support bounded logs query and bounded metrics query.

Request defaults:

```text
session: current app session
since: 15m for logs and metrics
limit: 100 for logs
step: 5s or 10s for metrics range query
timeout: bounded; do not allow unbounded backend calls from UI
scope: app ID + session ID + app root hash where available
```

Successful milestone result:

```text
The dashboard server can return scoped log query results from VictoriaLogs.
The dashboard server can return scoped metric query results from VictoriaMetrics.
The UI does not need Grafana URLs to read logs or metrics.
RPC tests cover default scope, explicit query, limit, and backend-unavailable behavior.
```

### Milestone 3 - Native traces workbench

Replace the Grafana trace handoff page with a native trace workbench.

The trace workbench must include:

```text
Recent traces table:
  service.endpoint or operation name
  trace ID
  status
  start time
  duration
  type
  message/correlation ID when present

Trace detail:
  trace ID
  selected span ID
  span list / span tree
  timing/waterfall
  status
  duration
  service and endpoint
  parent-child relationships
  span events

Trace event detail:
  event time
  event ID
  event kind
  structured payload
  raw backend fields where needed

Navigation:
  clicking a trace row opens trace detail
  clicking a span opens span events
  API call responses with trace_id link to trace detail
```

Successful milestone result:

```text
ui/src/routes/traces.tsx no longer says Grafana is the trace workbench.
No "Open in Grafana" link remains.
Trace list and trace detail are usable when VictoriaTraces is available.
Trace empty/degraded states are explicit and deterministic.
```

### Milestone 4 - Native observability overview page

Replace the Grafana status page with a native observability page.

The page should have these sections:

```text
Backend status:
  VictoriaMetrics ready/degraded/unavailable
  VictoriaLogs ready/degraded/unavailable
  VictoriaTraces ready/degraded/unavailable
  current app/session scope
  links to raw Victoria UI URLs only as debug details, not primary workbench

Request overview:
  request count in the selected time range
  latest latency
  latency time series
  error count time series
  top slow endpoints
  top failing endpoints

Logs:
  query input
  recent warnings/errors
  log table
  filters for level, trace ID, service, endpoint if supported

Traces:
  recent request traces
  recent error traces
  link to full trace workbench

Metrics:
  small PromQL/MetricsQL query input
  label/series explorer
  scoped query results table/chart
```

The first implementation can be modest. It must prove that the console has replaced Grafana as the primary workbench, not necessarily match every Grafana panel feature.

Successful milestone result:

```text
ui/src/routes/observability.tsx no longer renders Grafana dashboard links.
The page renders Victoria backend status and native observability views.
The page has useful degraded/empty states.
The UI can answer "what errors happened in this session?" without Grafana.
The UI can answer "which endpoint is slow?" without Grafana.
```

### Milestone 5 - Remove managed Grafana runtime

Once the native UI has enough parity to debug requests, remove the managed Grafana runtime code.

Targets include, but may not be limited to:

```text
cmd/scenery/grafana.go
cmd/scenery/grafana_provisioning.go
Grafana startup calls in cmd/scenery/dev_supervisor.go
Grafana shutdown/interrupt/wait paths
Grafana shared substrate registration
Grafana status serialization
Grafana route/proxy wiring
Grafana console events
Grafana tests
```

Successful milestone result:

```text
scenery up does not start Grafana.
scenery up output contains no Grafana phase.
App status contains observability/Victoria status, not Grafana status.
No Go production code imports or references Grafana runtime types.
```

### Milestone 6 - Remove Grafana toolchain, env, docs, and public contract

Remove current Grafana surface from docs and toolchain manifest.

Targets include:

```text
scenery.toolchain.json
docs/grafana.md
docs/environment.md
docs/environment.registry.json
docs/local-contract.md
docs/agent-guide.md
SKILL.md
README.md
docs/knowledge.json
docs/schemas/*
harness expectations
```

Remove or update environment variables:

```text
SCENERY_DEV_GRAFANA
SCENERY_DEV_GRAFANA_DOWNLOAD
SCENERY_GRAFANA_BIN
SCENERY_GRAFANA_VERSION
SCENERY_GRAFANA_PORT
SCENERY_GRAFANA_DIR
SCENERY_GRAFANA_PUBLIC_URL
SCENERY_GRAFANA_REUSE_EXTERNAL
SCENERY_GRAFANA_PRESERVE_GF_ENV
SCENERY_GRAFANA_DOWNLOAD_SHA256
SCENERY_GRAFANA_PLUGINS_PREINSTALL_SYNC
```

Remove toolchain artifacts:

```text
grafana
victoriametrics-metrics-datasource
victoriametrics-logs-datasource
```

Successful milestone result:

```text
Current docs no longer tell users that Scenery manages Grafana.
Environment registry no longer lists Grafana env vars.
Toolchain manifest no longer downloads Grafana or Grafana plugins.
Harness/docs inspection passes.
Historical completed plans may still mention Grafana as history.
```

### Milestone 7 - Cleanup, validation, and regression proof

Run full validation. Record exact commands and results in `Outcomes & Retrospective`.

Successful milestone result:

```text
go test ./...
ui typecheck passes.
ui build passes.
docs JSON validates.
toolchain JSON validates.
docs inspection passes.
self-harness passes or known unrelated warnings are recorded.
Final grep shows no active Grafana surface outside historical docs or this plan.
```

## Plan of Work

Start with inventory. Do not assume the inspected branch is identical to the implementation branch. This repository is actively changing. Before editing, run `git status --short`, read the active plan index, and grep for Grafana references. Record surprises in this plan.

Implement the replacement before deleting the old workbench. The native console does not need to match Grafana feature-for-feature, but it must cover the core debugging loop:

```text
A request failed.
I can see the failed request.
I can open its trace.
I can see spans and span events.
I can see related logs.
I can see whether the endpoint is generally slow or failing.
I can do that inside the Scenery console and through CLI/JSON surfaces.
```

Use the existing Victoria query code. Do not create a second query implementation in the UI. The UI should call dashboard RPC methods; dashboard RPC methods should reuse `internal/observability` and existing Victoria trace helpers where possible. If `internal/observability` lacks a needed shape, extend it with small stable response types and tests.

Keep query scope explicit. Every logs/metrics/traces call must default to the active app and current session. Cross-session or unscoped queries should not be the default console behavior. If cross-session debugging is added, make it an explicit UI control and include the active scope in the rendered page.

Make empty and degraded states intentional. Observability can be unavailable when Victoria is disabled, still starting, degraded, or not present in no-agent fallback. The UI should distinguish these from "no data yet." Tests should assert these empty states with stable `data-scenery-ui` and `data-scenery-state` selectors.

After the native console is usable, remove Grafana startup. Avoid a long-lived state where both workbenches are first-class. If temporarily keeping Grafana code helps bisect failures, keep it only on a local WIP commit. The final PR for this plan should not advertise or start managed Grafana.

Finally delete docs, env vars, toolchain artifacts, and tests. The cleanup is not complete until `git grep -i grafana` only finds historical completed plans, this ExecPlan, or intentional retrospective notes.

## Concrete Steps

### 1. Create and index the ExecPlan

Create this file:

```sh
$EDITOR docs/plans/0091-native-observability-and-grafana-removal.md
```

Add an entry near the other active runtime/dependency cleanup plans in `docs/plans/active.md`:

```md
- [0091 Native Observability Workbench and Grafana Removal](0091-native-observability-and-grafana-removal.md)
  - Status: active
  - Owner: scenery runtime / dashboard / agent DX
  - Created: 2026-06-27
  - Focus: replace managed Grafana with a Scenery-native console workbench over Victoria logs, metrics, and traces; remove Grafana toolchain, runtime, docs, env vars, and tests.
```

Add a `docs/knowledge.json` entry:

```json
{
  "path": "docs/plans/0091-native-observability-and-grafana-removal.md",
  "title": "Native Observability Workbench and Grafana Removal",
  "owner": "scenery runtime / dashboard / agent DX",
  "status": "active",
  "quality": "B",
  "freshness": "current",
  "last_reviewed": "2026-06-27",
  "review_after": "2026-07-27",
  "summary": "Active ExecPlan for replacing managed Grafana with native Scenery console observability over Victoria logs, metrics, and traces, then removing Grafana toolchain artifacts, runtime provisioning, docs, env vars, and tests.",
  "tags": [
    "plans",
    "execplans",
    "observability",
    "grafana",
    "victoria",
    "dashboard",
    "agents",
    "dependency-cleanup"
  ]
}
```

Validate plan structure and JSON before implementation:

```sh
jq empty docs/knowledge.json
go run ./cmd/scenery inspect docs --json
git diff --check
```

If `go run ./cmd/scenery inspect docs --json` fails because the installed/current Scenery binary differs from the working tree, record the exact failure in `Surprises & Discoveries` and continue only after deciding whether to use `go run ./cmd/scenery` or installed `scenery` for docs checks on this branch.

### 2. Run the Grafana inventory

From the repository root, run:

```sh
git grep -n -i 'grafana' -- .
git grep -n 'SCENERY_DEV_GRAFANA\|SCENERY_GRAFANA' -- .
git grep -n 'victoriametrics-metrics-datasource\|victoriametrics-logs-datasource' -- .
git grep -n 'scenery-dev-overview\|scenery-dev-logs\|scenery-dev-endpoint\|scenery-dev-temporal' -- .
git grep -n 'GrafanaState\|grafanaState\|SubstrateGrafana' -- .
git grep -n 'Open in Grafana\|trace workbench\|Grafana is' -- ui docs cmd internal
```

Also inspect likely tests:

```sh
git grep -n -i 'grafana' -- '*_test.go' 'ui/**/*.test.*'
```

Record the inventory in `Surprises & Discoveries`. It is acceptable to summarize large output by category, but include enough file names that a future agent can resume without redoing the first pass.

Expected categories:

```text
runtime startup/provisioning
status serialization
toolchain manifest
docs/env registry
UI links/types/routes
tests
historical plans
```

### 3. Define native observability status types

Find the current status definitions. Likely starting points:

```text
internal/devdash/types.go
ui/src/lib/types.ts
cmd/scenery/dev_supervisor.go
cmd/scenery/dashboard.go
cmd/scenery/dashboard_rpc.go
```

Add a Scenery-native observability state. Prefer a Go type under `internal/devdash` if that package already owns dashboard wire types.

Suggested Go shape:

```go
type ObservabilityState struct {
    Enabled bool `json:"enabled"`

    Backend string `json:"backend"`

    Metrics ObservabilityBackendState `json:"metrics"`
    Logs    ObservabilityBackendState `json:"logs"`
    Traces  ObservabilityBackendState `json:"traces"`

    Scope   *ObservabilityScope `json:"scope,omitempty"`
    Message string              `json:"message,omitempty"`
}

type ObservabilityBackendState struct {
    Enabled   bool   `json:"enabled"`
    Available bool   `json:"available"`
    Status    string `json:"status"`
    URL       string `json:"url,omitempty"`
    QueryPath string `json:"query_path,omitempty"`
    Dialect   string `json:"dialect,omitempty"`
    Message   string `json:"message,omitempty"`
}

type ObservabilityScope struct {
    AppID       string `json:"app_id,omitempty"`
    SessionID   string `json:"session_id,omitempty"`
    AppRootHash string `json:"app_root_hash,omitempty"`
    Worktree    string `json:"worktree,omitempty"`
    Branch      string `json:"branch,omitempty"`
}
```

Do not keep `GrafanaState` as the new state under a different status string. The final app status should have an explicit `observability` field or equivalent.

Update TypeScript types in `ui/src/lib/types.ts` with the equivalent shape.

Suggested TypeScript shape:

```ts
export interface ObservabilityState {
  enabled: boolean;
  backend: "victoria" | string;
  metrics: ObservabilityBackendState;
  logs: ObservabilityBackendState;
  traces: ObservabilityBackendState;
  scope?: ObservabilityScope;
  message?: string;
}

export interface ObservabilityBackendState {
  enabled: boolean;
  available: boolean;
  status: string;
  url?: string;
  query_path?: string;
  dialect?: string;
  message?: string;
}

export interface ObservabilityScope {
  app_id?: string;
  session_id?: string;
  app_root_hash?: string;
  worktree?: string;
  branch?: string;
}
```

Update status builders so the dashboard can render:

```text
metrics ready when VictoriaMetrics base URL exists/reachable
logs ready when VictoriaLogs base URL exists/reachable
traces ready when VictoriaTraces base URL exists/reachable
```

If current code only checks TCP reachability, preserve that for the first pass. Do not add expensive query probes to every status tick unless tests prove the overhead is acceptable.

Add focused tests for status serialization. Use synthetic Victoria stacks or injected controller state; do not require real Victoria binaries.

### 4. Add dashboard RPCs for logs and metrics

Open `cmd/scenery/dashboard_rpc.go`. Add methods for native observability queries.

Suggested method names:

```text
observability/logs/query
observability/metrics/query
observability/metrics/labels
observability/metrics/series
```

Suggested request types:

```go
type dashboardLogsQueryParams struct {
    AppID   string   `json:"app_id,omitempty"`
    Query   string   `json:"query,omitempty"`
    Since   string   `json:"since,omitempty"`
    Start   string   `json:"start,omitempty"`
    End     string   `json:"end,omitempty"`
    Limit   int      `json:"limit,omitempty"`
    Fields  []string `json:"fields,omitempty"`
    Timeout string   `json:"timeout,omitempty"`
}

type dashboardMetricsQueryParams struct {
    AppID   string `json:"app_id,omitempty"`
    PromQL  string `json:"promql"`
    Since   string `json:"since,omitempty"`
    Start   string `json:"start,omitempty"`
    End     string `json:"end,omitempty"`
    Step    string `json:"step,omitempty"`
    Instant bool   `json:"instant,omitempty"`
    Limit   int    `json:"limit,omitempty"`
    Timeout string `json:"timeout,omitempty"`
}

type dashboardMetricsCatalogParams struct {
    AppID   string `json:"app_id,omitempty"`
    Match   string `json:"match,omitempty"`
    Since   string `json:"since,omitempty"`
    Start   string `json:"start,omitempty"`
    End     string `json:"end,omitempty"`
    Limit   int    `json:"limit,omitempty"`
    Timeout string `json:"timeout,omitempty"`
}
```

Default values:

```text
logs query:
  query: *
  since: 15m
  limit: 100
  timeout: 3s

metrics query:
  promql: required
  since: 15m
  step: 5s or 10s
  limit: 20 series
  timeout: 3s

metrics labels/series:
  since: 1h
  limit: 200
  timeout: 3s
```

Add helper functions in `cmd/scenery` to build `observability.QueryScope` from dashboard status:

```text
active app ID
dashboardStoreAppID(status)
current session ID
app root hash if available
worktree and branch if available
```

Do not duplicate scope filter construction in `cmd/scenery` if `internal/observability` already owns it. The dashboard should pass a scope object and let `internal/observability` apply backend parameters.

When VictoriaLogs or VictoriaMetrics is unavailable, return a successful JSON-RPC response with warnings if that is how `internal/observability` already behaves. Do not make the UI treat "backend missing" as an uncaught RPC failure unless the request itself is invalid.

Add tests in `cmd/scenery/dashboard_rpc_test.go` or a nearby file:

```text
TestDashboardObservabilityLogsQueryUsesCurrentSessionScope
TestDashboardObservabilityLogsQueryDefaults
TestDashboardObservabilityLogsQueryUnavailableBackendReturnsWarning
TestDashboardObservabilityMetricsQueryUsesExtraLabels
TestDashboardObservabilityMetricsLabelsLimitsResults
TestDashboardObservabilityMetricsSeriesLimitsResults
TestDashboardObservabilityRejectsMissingPromQL
```

Use `httptest.Server` for Victoria backends where possible. Assert request path and form parameters:

```text
VictoriaLogs:
  /select/logsql/query
  query
  start
  end
  limit
  timeout
  extra_filters

VictoriaMetrics:
  /prometheus/api/v1/query_range
  /prometheus/api/v1/query
  /prometheus/api/v1/labels
  /prometheus/api/v1/series
  query
  start
  end
  step
  timeout
  extra_label
```

### 5. Improve trace detail RPCs if needed

Current trace RPCs may already be enough for the UI:

```text
traces/list
traces/get
traces/spans/summaries/list
traces/spans/events/list
```

Inspect current response shapes and frontend usage. If needed, add one convenience RPC:

```text
traces/detail
```

Suggested `traces/detail` result:

```json
{
  "trace_id": "abc...",
  "spans": [
    {
      "trace_id": "abc...",
      "span_id": "def...",
      "parent_span_id": "000...",
      "type": "REQUEST",
      "is_root": true,
      "is_error": false,
      "started_at": "2026-06-27T...",
      "duration_nanos": 123000000,
      "service_name": "api",
      "endpoint_name": "Ping"
    }
  ],
  "events_by_span_id": {
    "def...": [
      {
        "event_id": "1",
        "event_time": "2026-06-27T...",
        "span_start": {}
      }
    ]
  }
}
```

Only add this if it simplifies UI and test coverage. Otherwise use the existing `traces/spans/summaries/list` and `traces/spans/events/list`.

Ensure trace detail scope remains current-session by default. If querying by trace ID returns spans from other sessions, filter by session ID where possible.

Add tests for:

```text
root span first
children sorted by start time
session filter applied
trace not found renders empty/degraded response
VictoriaTraces unavailable response
```

### 6. Update dashboard context and client helpers

Inspect `ui/src/lib/dashboard-context.tsx`. Identify how it calls JSON-RPC, stores traces, and refreshes status.

Add client functions or hooks for the new RPCs. Prefer small typed wrappers:

```ts
export async function rpcLogsQuery(params: LogsQueryParams): Promise<LogsQueryResult>
export async function rpcMetricsQuery(params: MetricsQueryParams): Promise<MetricsQueryResult>
export async function rpcMetricsLabels(params: MetricsCatalogParams): Promise<MetricsLabelsResult>
export async function rpcMetricsSeries(params: MetricsCatalogParams): Promise<MetricsSeriesResult>
export async function rpcTraceSpanSummaries(traceId: string): Promise<TraceSummary[]>
export async function rpcTraceSpanEvents(traceId: string, spanId: string): Promise<TraceEvent[]>
```

If the dashboard context already has a generic `request` method, use it. Do not introduce a second WebSocket connection for observability unless there is no reusable client path.

Add TypeScript interfaces for:

```text
LogsQueryResult
LogEntry
MetricsQueryResult
MetricSeries
MetricSample
MetricsLabelsResult
MetricsSeriesResult
TraceEvent
TraceDetail
```

Keep wire fields aligned with Go JSON names. Avoid client-only renaming unless there is a clear reason.

### 7. Replace `ui/src/lib/grafana.ts`

Delete `ui/src/lib/grafana.ts` after removing its imports.

Before deletion, inventory imports:

```sh
git grep -n 'lib/grafana\|requestTracesURL\|temporalTracesURL\|traceDashboardURL\|isGrafanaAvailable' -- ui
```

Replace with native helpers if needed:

```ts
export function isObservabilityBackendAvailable(backend?: ObservabilityBackendState | null): boolean {
  return backend?.available === true;
}

export function isTemporalTrace(trace?: Pick<TraceSummary, "type"> | null): boolean {
  return (trace?.type ?? "").startsWith("TEMPORAL_");
}

export function traceDisplayName(trace: TraceSummary): string {
  const service = trace.service_name || "unknown";
  const endpoint = trace.endpoint_name || trace.type;
  return `${service}.${endpoint}`;
}
```

If Temporal is removed or renamed in the implementation branch, do not preserve `isTemporalTrace` only for Grafana. Keep it only if trace types still use `TEMPORAL_` in runtime data.

### 8. Rebuild the traces route as native UI

Rewrite `ui/src/routes/traces.tsx`.

Remove this concept:

```text
The dashboard trace viewer is deprecated. Grafana is the trace workbench.
```

Replace with:

```text
Traces show request and runtime spans recorded for the current Scenery dev session.
```

Suggested component structure:

```text
TracesPage
  TraceWorkbench
    TraceToolbar
      refresh button
      status filter
      service filter
      endpoint filter
      text trace ID filter
    TraceTable
    TraceDetailPanel
      TraceSummaryHeader
      SpanWaterfall
      SpanList
      SpanEventsPanel
```

Minimum UI behavior:

```text
No trace selected:
  show recent traces table.

Trace selected:
  show selected trace header.
  load span summaries.
  show root span and child spans.
  selecting a span loads span events.
  show events as structured JSON/table.
```

Use stable selectors for tests and harnesses:

```tsx
data-scenery-ui="TracesRoute"
data-scenery-ui="TraceTable"
data-scenery-ui="TraceTableRow"
data-scenery-ui="TraceDetail"
data-scenery-ui="SpanWaterfall"
data-scenery-ui="SpanRow"
data-scenery-ui="SpanEvents"
data-scenery-state="intentional-empty"
data-scenery-state="backend-unavailable"
```

Waterfall implementation can be simple:

```text
Find earliest span start.
Find latest span end.
For each span:
  left = (span start - earliest) / total duration
  width = span duration / total duration
Render a div/SVG row with percentage left/width.
Do not require a chart library.
```

Handle missing or malformed times gracefully.

For span events, render:

```text
event_time
event_id
event kind
summary string
expandable raw JSON
```

If an event has `span_start` or `span_end`, show friendly labels. If it has Victoria log fields, show raw fields under an expandable block.

### 9. Rebuild the observability route as native UI

Rewrite `ui/src/routes/observability.tsx`.

Remove Grafana concepts:

```text
Grafana status card
Open Grafana
Overview dashboard
Logs dashboard
Endpoint Debugger dashboard
Temporal dashboard
Datasource UID table
Dashboard UID table
```

Replace with native concepts:

```text
Observability
  Backend status
  Request overview
  Recent errors
  Logs query
  Metrics query
  Recent traces
```

Suggested page structure:

```text
ObservabilityPage
  ObservabilityStatusCards
    MetricsBackendCard
    LogsBackendCard
    TracesBackendCard

  RequestOverviewSection
    RequestsObservedStat
    LatestLatencyStat
    RequestDurationChart
    ErrorsChart
    SlowEndpointsTable
    FailingEndpointsTable

  LogsSection
    LogsQueryInput
    LogLevelFilter
    LogTable

  MetricsSection
    PromQLInput
    MetricSeriesTable
    MetricLabelsDrawer or simple labels list

  RecentTracesSection
    RecentErrorTraces
    RecentSlowTraces
```

Initial PromQL/MetricsQL queries:

```text
Requests observed:
  count_over_time(scenery_request_duration_seconds{scenery_session_id=~"$session"}[15m])

Latest latency:
  scenery_request_duration_seconds{scenery_session_id=~"$session"}

Endpoint latency:
  scenery_request_duration_seconds{scenery_session_id=~"$session",scenery_service="<svc>",scenery_endpoint="<endpoint>"}

Errors:
  count_over_time(scenery_request_duration_seconds{scenery_session_id=~"$session",scenery_is_error="true"}[5m])

Slow endpoints:
  topk(10, max_over_time(scenery_request_duration_seconds{scenery_session_id=~"$session"}[15m]))
```

Do not put `$session` into the actual backend query if backend-enforced scope uses `extra_label`. The UI examples can display scoped queries, but the implementation should rely on server-side scope enforcement where `internal/observability` supports it.

For charts, use a small internal component:

```tsx
function TimeSeriesChart({ series }: { series: MetricSeries[] }) {
  // normalize timestamps and values
  // render SVG polyline(s)
  // render empty state when no samples
}
```

Do not hard-code colors unless the UI design system already has token classes. Use existing CSS variables/classes.

Use stable selectors:

```tsx
data-scenery-ui="ObservabilityRoute"
data-scenery-ui="ObservabilityBackendStatus"
data-scenery-ui="RequestOverview"
data-scenery-ui="RequestDurationChart"
data-scenery-ui="ErrorRateChart"
data-scenery-ui="LogsQuery"
data-scenery-ui="LogsTable"
data-scenery-ui="MetricsQuery"
data-scenery-ui="MetricsSeries"
data-scenery-state="backend-unavailable"
data-scenery-state="intentional-empty"
```

### 10. Add request-to-trace correlation in the API/request UI

Inspect:

```text
ui/src/routes/requests.tsx
ui/src/routes/requests-editor.tsx
cmd/scenery/dashboard.go apiCall
ui/src/lib/types.ts ApiCallResponse
```

`ApiCallResponse` already has `trace_id?: string`. Make the UI use it.

Behavior:

```text
After an API call completes:
  if response.trace_id exists:
    show "View trace" link to /$appId/envs/local/traces/$traceId
    show "View related logs" action or link to Observability logs query with trace_id filter if implemented
```

If URL search params are already used in the router, add:

```text
/$appId/observability?trace_id=<trace_id>
```

The observability page can use `trace_id` to prefill log query/filter and show a selected trace card.

Add tests or lightweight component assertions for:

```text
API response with trace_id shows trace link.
API response without trace_id does not show disabled broken link.
Trace link uses current appId and traceId.
```

### 11. Remove Grafana startup from supervisor

Open `cmd/scenery/dev_supervisor.go` and related tests. Locate fields and lifecycle:

```text
s.grafana
startGrafanaForDev
startGrafanaForDevWithRoot
grafana shared substrate Ensure
Grafana shutdown in Close
Grafana Interrupt / WaitOrKill
console phases or events mentioning Grafana
status.grafana population
```

Remove Grafana from startup flow. Startup should still handle Victoria:

```text
start/reuse Victoria
set app env with Victoria endpoints
start app
start dashboard
report observability status
```

Preserve startup timing integrity. If a previous commit added timing for Grafana, remove that phase and ensure total wall-clock output still adds up.

If the startup console currently prints a URL row for Grafana, remove it. Replace with native console/observability URL only if needed.

Update tests that assert phase names, event counts, status fields, or startup output.

Expected final behavior:

```text
scenery up
  no Grafana phase
  no Grafana warning
  no Grafana download
  no .scenery/grafana writes
  no Grafana process
```

### 12. Remove Grafana provisioning code

After supervisor no longer references it, delete:

```text
cmd/scenery/grafana.go
cmd/scenery/grafana_provisioning.go
```

Run:

```sh
go test ./cmd/scenery -run Grafana
```

This should either report no tests to run or fail only because stale tests still reference Grafana. Remove or rewrite stale tests.

Run:

```sh
git grep -n 'grafana' -- cmd/scenery internal
```

Remove remaining production references. For test references, decide whether they are historical or stale. In general, remove Grafana tests rather than leaving "disabled" assertions.

### 13. Remove Grafana substrate types and registry surfaces

Inspect:

```text
internal/agent/types.go
cmd/scenery/dev_substrate_manager.go
cmd/scenery/agent.go
cmd/scenery/agent_status_table.go
cmd/scenery/harness_*.go
```

Remove `SubstrateGrafana` or equivalent constants and handling if present. If substrate registry data from older local agents can still contain kind `grafana`, decide whether to ignore unknown substrates generically rather than preserving a typed Grafana constant.

Preferred behavior:

```text
Old local agent registry contains grafana:
  scenery ps ignores it or prints unknown historical substrate only if generic code already does that.
  Scenery does not try to verify, restart, or display it as current supported substrate.
```

Do not add a Grafana migration command. Users can delete old local state manually.

Add recovery note to docs:

```text
Old local Grafana state under .scenery/grafana or the agent state root can be safely deleted.
```

### 14. Remove Grafana status from devdash and UI types

Remove or replace:

```text
internal/devdash.GrafanaState
internal/devdash.GrafanaDashboard
ui/src/lib/types.ts GrafanaState
ui/src/lib/types.ts GrafanaDashboard
AppStatus.grafana
```

Add or verify:

```text
AppStatus.observability
ObservabilityState
ObservabilityBackendState
```

Update all tests and JSON fixtures.

Run:

```sh
git grep -n 'GrafanaState\|GrafanaDashboard\|grafana?' -- .
```

Final active code should have no match except historical docs/this plan.

### 15. Remove Grafana toolchain artifacts

Edit `scenery.toolchain.json`.

Remove artifacts:

```text
grafana
victoriametrics-metrics-datasource
victoriametrics-logs-datasource
```

Run:

```sh
jq empty scenery.toolchain.json
go test ./internal/toolchain ./internal/devtools ./cmd/scenery -run Toolchain
git grep -n 'victoriametrics-metrics-datasource\|victoriametrics-logs-datasource\|grafana-13' -- .
```

If toolchain tests expect Grafana as a pinned artifact, update them to assert Victoria, Caddy, SQLite-related tools, or whatever remains current.

Do not remove Victoria toolchain artifacts in this plan.

### 16. Remove Grafana environment variables and docs registry entries

Edit:

```text
docs/environment.md
docs/environment.registry.json
docs/local-contract.md
docs/agent-guide.md
SKILL.md
README.md
docs/app-development-cookbook.md
```

Remove env vars:

```text
SCENERY_DEV_GRAFANA
SCENERY_DEV_GRAFANA_DOWNLOAD
SCENERY_GRAFANA_BIN
SCENERY_GRAFANA_VERSION
SCENERY_GRAFANA_PORT
SCENERY_GRAFANA_DIR
SCENERY_GRAFANA_PUBLIC_URL
SCENERY_GRAFANA_REUSE_EXTERNAL
SCENERY_GRAFANA_PRESERVE_GF_ENV
SCENERY_GRAFANA_DOWNLOAD_SHA256
SCENERY_GRAFANA_PLUGINS_PREINSTALL_SYNC
```

Add or update native observability docs:

```text
scenery inspect observability --json
scenery logs query --json --since 15m --query 'error OR panic'
scenery logs tail --jsonl --query 'error'
scenery metrics query --json --since 15m --step 5s --promql '<query>'
scenery metrics labels --json --since 1h
scenery metrics series --json --match 'scenery_request_duration_seconds'
scenery traces list --json --since 15m --status error
```

Update local contract wording:

```text
Scenery local observability uses Victoria backends and the Scenery console.
Scenery no longer manages Grafana.
The console and CLI are the supported workbench surfaces.
Victoria raw URLs are debug details.
```

Run:

```sh
jq empty docs/environment.registry.json docs/knowledge.json
go run ./cmd/scenery inspect docs --json
```

### 17. Remove or replace `docs/grafana.md`

Preferred final state:

```text
docs/grafana.md deleted.
docs/index.md no longer links it.
docs/knowledge.json no longer indexes it as current docs.
```

If docs indexing or historical references make deletion inconvenient, replace it with a short historical tombstone only if maintainers approve:

```md
# Grafana Dev Integration

Managed Grafana was removed from Scenery local development in plan 0091.
Use the Scenery console and `scenery inspect observability --json`,
`scenery logs query`, `scenery metrics query`, and `scenery traces list`
for local observability.

Old local state under `.scenery/grafana/` can be deleted.
```

The tombstone option is less clean. Prefer deletion unless tooling requires the path.

### 18. Update CLI observability docs and inspect output if needed

Inspect:

```text
cmd/scenery/inspect_observability.go
cmd/scenery/observability_commands.go
cmd/scenery/logs.go
docs/schemas/*
docs/local-contract.md
docs/agent-guide.md
```

Ensure `inspect observability` does not mention Grafana as optional visual surface in current docs. It may expose Victoria backend URLs and dialects.

If `inspect observability --json` currently includes Grafana fields, remove them.

Expected inspect output should be conceptually like:

```json
{
  "schema_version": "scenery.inspect.observability.v1",
  "scope": {
    "app_id": "example",
    "session_id": "example-main-abc123",
    "app_root": "/path/to/app",
    "app_root_hash": "abc123",
    "enforced": true
  },
  "backends": {
    "metrics": {
      "kind": "victoriametrics",
      "dialect": "PromQL/MetricsQL",
      "ready": true,
      "base_url": "http://127.0.0.1:8428"
    },
    "logs": {
      "kind": "victorialogs",
      "dialect": "LogsQL",
      "ready": true,
      "base_url": "http://127.0.0.1:9428"
    },
    "traces": {
      "kind": "victoriatraces",
      "dialect": "Jaeger query API",
      "ready": true,
      "base_url": "http://127.0.0.1:10428"
    }
  },
  "examples": {
    "logs": "scenery logs query --json --since 15m --query 'error OR panic'",
    "metrics": "scenery metrics query --json --since 15m --promql 'scenery_request_duration_seconds'",
    "traces": "scenery traces list --json --since 15m"
  }
}
```

Do not include a Grafana URL.

### 19. Update frontend route navigation

Search UI navigation definitions. Likely files include:

```text
ui/src/routes/*
ui/src/lib/dashboard-context.tsx
ui/src/components/*
```

Remove Grafana wording from nav labels, page descriptions, tooltips, and empty states.

Ensure route labels are native:

```text
Observability
Traces
Logs
Metrics
```

If the app currently only has `Observability` and `Traces`, it is acceptable to keep logs/metrics as sections within `Observability` for the first implementation. Do not create more routes than needed.

### 20. Add UI tests or strengthen existing ones

Inspect current UI test setup. Run:

```sh
cd ui
bun run test
```

If UI tests exist, add tests for:

```text
ObservabilityPage renders Victoria backend cards.
ObservabilityPage does not render Grafana links.
TracesPage does not render Grafana handoff copy.
TracesPage renders intentional empty state.
TraceDetail renders selected trace fields.
API response trace_id renders trace link.
```

If no suitable test framework exists, at minimum run:

```sh
cd ui
bun run typecheck
bun run build
```

and add stable `data-scenery-ui` selectors so future harness/browser checks can assert behavior.

### 21. Update Go tests

Run focused tests first:

```sh
go test ./internal/observability
go test ./cmd/scenery -run 'Observability|Victoria|Trace|Dashboard'
go test ./internal/agent -run 'Substrate|Session|Status'
```

Remove or update tests matching Grafana:

```sh
go test ./cmd/scenery -run Grafana
git grep -n -i 'grafana' -- '*_test.go'
```

Expected changes:

```text
Grafana startup tests deleted.
Grafana provisioning tests deleted.
Grafana external reuse tests deleted.
Toolchain Grafana tests deleted or rewritten.
Dashboard status tests expect observability/Victoria state.
UI tests expect native pages.
```

Do not keep tests that assert Grafana is disabled. The target is not "disabled Grafana"; the target is "no Grafana surface."

### 22. Update harness and schema expectations

Search for Grafana in harness and schema files:

```sh
git grep -n -i 'grafana' -- docs/schemas cmd/scenery '*harness*' testdata
```

Update expected JSON schemas:

```text
AppStatus schema if present:
  remove grafana
  add observability

Environment schema/registry:
  remove Grafana env vars

Toolchain schema snapshots:
  remove Grafana artifacts if snapshots list artifacts

Harness self:
  update expected docs/routes/status output
```

Run:

```sh
jq empty docs/schemas/*.json docs/environment.registry.json docs/knowledge.json scenery.toolchain.json
go run ./cmd/scenery harness self --summary --write
```

If self-harness writes artifacts, inspect them. Commit only intentional fixture updates. Do not commit local runtime state.

### 23. Final active-code grep

Before final validation, run:

```sh
git grep -n -i 'grafana' -- .
```

Allowed final matches:

```text
docs/plans/0091-native-observability-and-grafana-removal.md
historical completed plans
possibly changelog/release notes if they explicitly describe removal
```

Not allowed final matches:

```text
cmd/
internal/
ui/src/
docs/local-contract.md current Grafana support wording
docs/environment.registry.json env vars
scenery.toolchain.json artifacts/plugins
README current setup instructions
SKILL current agent workflow instructions
```

If historical active plans still mention Grafana as current behavior, update them or move those references to retrospective wording. Do not rewrite completed historical plans unless docs tooling treats them as current guidance.

### 24. End-to-end proof fixture

Use an existing fixture app if one exists that emits API traces/logs/metrics. If none exists, add a small fixture:

```text
testdata/apps/observability-workbench/
  .scenery.json
  go.mod
  svc/api.go
```

The fixture should expose at least:

```text
GET /ok
GET /slow
GET /fail
```

The implementation should produce:

```text
a successful request trace
a slow request duration metric
an error request trace
an error log or warning log
```

Example manual proof commands, adapted to actual fixture and current route mode:

```sh
go run ./cmd/scenery up --app-root testdata/apps/observability-workbench --json
go run ./cmd/scenery ps --json
curl "$(go run ./cmd/scenery ps --json | jq -r '.sessions[0].route_manifest.routes.api.url')/fail"
go run ./cmd/scenery traces list --json --app-root testdata/apps/observability-workbench --since 15m --status error
go run ./cmd/scenery logs query --json --app-root testdata/apps/observability-workbench --since 15m --query 'error OR fail'
go run ./cmd/scenery metrics query --json --app-root testdata/apps/observability-workbench --since 15m --promql 'scenery_request_duration_seconds'
```

If `scenery up` is long-running and not suitable for automated validation in this branch, document the manual proof in `Outcomes & Retrospective` and rely on harness/self tests for CI proof.

### 25. Full validation

Run from repository root:

```sh
git diff --check
jq empty docs/knowledge.json docs/environment.registry.json scenery.toolchain.json
go run ./cmd/scenery inspect docs --json
go test ./internal/observability
go test ./internal/agent
go test ./cmd/scenery
go test ./...
go install ./cmd/scenery
```

Run from `ui/`:

```sh
bun install --frozen-lockfile
bun run typecheck
bun run test
bun run build
```

Run harness when practical:

```sh
go run ./cmd/scenery harness self --summary --write
```

If the repo convention on the implementation branch prefers installed `scenery` for harness, use:

```sh
scenery harness self --summary --write
```

Record exact results in `Outcomes & Retrospective`.

## Validation and Acceptance

Acceptance criteria:

```text
Plan/index:
  docs/plans/0091-native-observability-and-grafana-removal.md exists.
  docs/plans/active.md links it.
  docs/knowledge.json indexes it.
  scenery inspect docs --json passes or any pre-existing warnings are recorded.

Runtime:
  scenery up no longer starts Grafana.
  scenery up no longer downloads Grafana.
  scenery up no longer writes Grafana provisioning files.
  scenery up no longer reports Grafana status.
  Process lifecycle code has no Grafana component.

Toolchain:
  scenery.toolchain.json no longer contains grafana.
  scenery.toolchain.json no longer contains victoriametrics Grafana plugins.
  Toolchain tests pass with the reduced manifest.

Status/API:
  AppStatus exposes native observability/Victoria status.
  AppStatus does not expose grafana in the final current contract.
  Dashboard JSON-RPC can query logs from VictoriaLogs.
  Dashboard JSON-RPC can query metrics from VictoriaMetrics.
  Existing trace JSON-RPC continues to work or is replaced by equivalent native trace detail RPCs.

UI:
  Observability route is native and does not link to Grafana.
  Traces route is native and does not link to Grafana.
  API request UI links trace_id to native trace detail.
  Empty and degraded states are explicit.
  UI typecheck and build pass.

Docs/env:
  Current docs no longer advertise managed Grafana.
  Environment registry no longer lists Grafana env vars.
  Agent guide tells agents to use Scenery CLI/console observability surfaces.
  Local contract says Scenery uses Victoria + console, not Grafana.

Cleanup:
  Final active-code grep for Grafana is clean.
  Historical mentions remain only where intentionally historical.
```

Default validation commands:

```sh
git diff --check
jq empty docs/knowledge.json docs/environment.registry.json scenery.toolchain.json
go run ./cmd/scenery inspect docs --json
go test ./internal/observability
go test ./internal/agent
go test ./cmd/scenery
go test ./...
go install ./cmd/scenery
```

Frontend validation commands:

```sh
cd ui
bun install --frozen-lockfile
bun run typecheck
bun run test
bun run build
```

Harness validation:

```sh
go run ./cmd/scenery harness self --summary --write
```

Final grep validation:

```sh
git grep -n -i 'grafana' -- .
git grep -n 'SCENERY_DEV_GRAFANA\|SCENERY_GRAFANA' -- .
git grep -n 'victoriametrics-metrics-datasource\|victoriametrics-logs-datasource' -- .
```

A final implementation is not accepted if any current production code still starts, provisions, verifies, exposes, or links to Grafana.

## Idempotence and Recovery

This plan is safe to resume if each milestone is kept independently testable.

If the plan/index edit is interrupted:

```sh
jq empty docs/knowledge.json
go run ./cmd/scenery inspect docs --json
```

Fix JSON or plan-section diagnostics before changing implementation.

If native UI work is incomplete:

```text
Do not remove Grafana runtime yet.
Keep the branch in a state where existing tests pass.
Record the missing UI capability in Progress.
```

If dashboard RPC work fails:

```text
Back out only the new RPC methods and tests.
Keep type additions if they are already used by status.
Use internal/observability tests to isolate query behavior from dashboard transport.
```

If UI build fails after Grafana link removal:

```text
Search for stale imports from ui/src/lib/grafana.ts.
Search for stale GrafanaState/GrafanaDashboard type references.
Restore a simple native empty-state component rather than reintroducing Grafana links.
```

If removing `cmd/scenery/grafana.go` causes widespread compile failures:

```text
Revert the file deletion temporarily.
Remove references from supervisor/status/tests first.
Delete the file only after `go test ./cmd/scenery -run Grafana` no longer finds required code.
```

If old local agent registry state contains Grafana substrate records:

```text
Do not add managed migration code unless tests show startup breaks.
Prefer ignoring unknown/historical substrate kinds.
Document that old `.scenery/grafana` or agent Grafana state can be deleted.
```

If toolchain tests fail after removing Grafana artifacts:

```text
Update expected artifact counts and names.
Do not preserve Grafana in the manifest just to satisfy snapshots.
Confirm Victoria artifacts remain.
```

If docs inspection fails because historical plans mention Grafana:

```text
Check whether the docs tool treats completed plans as current guidance.
Do not rewrite history unnecessarily.
If needed, adjust docs metadata to mark old Grafana docs historical/obsolete.
```

If self-harness fails with unrelated warnings:

```text
Record exact warnings in Outcomes & Retrospective.
Do not expand this plan to fix unrelated large-file, slow-test, Postgres, DNS, or route-mode work unless the failure is caused by Grafana removal.
```

## Artifacts and Notes

Expected removed files or heavily rewritten files:

```text
cmd/scenery/grafana.go
cmd/scenery/grafana_provisioning.go
ui/src/lib/grafana.ts
docs/grafana.md
```

Expected edited files:

```text
docs/plans/active.md
docs/knowledge.json
docs/local-contract.md
docs/agent-guide.md
docs/environment.md
docs/environment.registry.json
README.md
SKILL.md
scenery.toolchain.json
cmd/scenery/dev_supervisor.go
cmd/scenery/dashboard.go
cmd/scenery/dashboard_rpc.go
cmd/scenery/dashboard_traces.go
cmd/scenery/victoria.go
cmd/scenery/inspect_observability.go
cmd/scenery/observability_commands.go
internal/agent/types.go
internal/devdash/types.go
internal/observability/query.go
ui/src/lib/types.ts
ui/src/lib/dashboard-context.tsx
ui/src/routes/observability.tsx
ui/src/routes/traces.tsx
ui/src/routes/requests.tsx
```

Expected local runtime artifacts that may remain on developer machines but should not be written by new code:

```text
.scenery/grafana/
<agent-state-root>/grafana/
```

User-facing recovery note for docs or release notes:

```text
Managed Grafana has been removed from Scenery local development. Existing local
Grafana state under `.scenery/grafana/` or the Scenery agent state directory is
no longer used and can be deleted. Use the Scenery console, `scenery inspect
observability --json`, `scenery logs query`, `scenery metrics query`, and
`scenery traces list` for local observability.
```

Suggested release note:

```md
### Removed

- Removed managed Grafana from local development. `scenery up` no longer downloads,
  provisions, starts, or links to Grafana. Local observability now uses the
  Scenery console and CLI JSON surfaces over VictoriaMetrics, VictoriaLogs, and
  VictoriaTraces.

### Migration

- Delete old local Grafana state if desired: `rm -rf .scenery/grafana`.
- Remove any local `SCENERY_DEV_GRAFANA` or `SCENERY_GRAFANA_*` environment
  overrides; they are no longer part of the Scenery contract.
```

Do not commit local state directories:

```text
.scenery/
.scenery/grafana/
.scenery/victoria/
.scenery/harness/
```

Do not add new third-party UI dependencies unless a new Decision Log entry justifies it.

## Interfaces and Dependencies

This plan depends on existing Scenery-owned interfaces:

```text
Victoria stack:
  cmd/scenery/victoria.go
  victoriaStack.BaseURL
  victoriaStack.Endpoint
  victoriaStack.URLs
  victoriaStack.Reachable

Victoria query/export:
  cmd/scenery/victoria_query.go
  cmd/scenery/victoria_export.go
  internal/observability/query.go

Dashboard server:
  cmd/scenery/dashboard.go
  cmd/scenery/dashboard_rpc.go
  cmd/scenery/dashboard_traces.go
  internal/devdash/types.go

Agent/session status:
  internal/agent/types.go
  internal/agent/session.go
  cmd/scenery/agent.go
  cmd/scenery/agent_status_table.go

Console UI:
  ui/src/lib/dashboard-context.tsx
  ui/src/lib/types.ts
  ui/src/routes/observability.tsx
  ui/src/routes/traces.tsx
  ui/src/routes/requests.tsx
```

External protocols used by this plan:

```text
VictoriaMetrics:
  /prometheus/api/v1/query
  /prometheus/api/v1/query_range
  /prometheus/api/v1/labels
  /prometheus/api/v1/series
  extra_label scope parameters

VictoriaLogs:
  /select/logsql/query
  /select/logsql/tail
  extra_filters scope parameters

VictoriaTraces:
  /select/jaeger/api/traces
  /select/jaeger/api/traces/<trace_id>
```

Interfaces to remove:

```text
Managed Grafana process startup
Grafana provisioning files
Grafana datasource UID contract
Grafana dashboard UID contract
Grafana plugins in toolchain
Grafana env vars
Grafana AppStatus field
Grafana UI links
Grafana shared substrate kind
```

Interfaces to preserve:

```text
Victoria OTLP endpoint env vars
Scenery session/app identity labels
Dashboard JSON-RPC transport
CLI JSON observability commands
Trace ID returned from API calls
Current-session default query scope
```

Dependency policy:

```text
Do not add Grafana, Grafana plugins, Loki, Prometheus server, Jaeger UI, or another
general-purpose dashboard as a replacement dependency.

Do not add a frontend charting dependency in the first implementation unless raw
SVG/table rendering is proven insufficient and the Decision Log records the tradeoff.

Do not remove Victoria as part of this plan.

Do not couple this work to Postgres/SQLite, DNS/path routing, or Temporal cleanup
except where stale Grafana code directly references those surfaces.
```
