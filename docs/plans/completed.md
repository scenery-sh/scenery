# Completed Plans

This file records completed milestones so agents can distinguish shipped behavior from future intent.

Completed means implemented or shipped at least once. It does not imply stable
v0 support. Use [../local-contract.md](../local-contract.md) as the source of
truth for stable, beta, dev-only, and compatibility-mode classification.

## CLI Observability Query Surface

- Status: completed
- Owner: onlava runtime / agent DX
- Completed: 2026-06-08
- Quality: B+
- ExecPlan: [0067 CLI Observability Query Surface](0067-cli-observability-query.md)

Shipped:

- `onlava inspect observability --json` for backend readiness, native dialects,
  examples, warnings, and echoed app/session scope.
- `onlava logs query` and `onlava logs tail` for scoped VictoriaLogs LogsQL,
  with JSON/JSONL output, bounded defaults, and explicit LogQL rejection.
- `onlava metrics query`, `onlava metrics labels`, and `onlava metrics series`
  for scoped PromQL/MetricsQL range, instant, and catalog queries.
- Backend-enforced scope via VictoriaLogs `extra_filters` and VictoriaMetrics
  repeated `extra_label` parameters, plus normalized versioned JSON envelopes.
- Schema, contract, cookbook, skill, agent-guide, and knowledge-index updates
  for the new query surface.

Validation:

- `go test ./internal/observability ./cmd/onlava` passed during implementation.
- Full validation was run before PR creation for the implementation change.

## App Validation Profiles

- Status: completed
- Owner: onlava runtime / agent DX
- Completed: 2026-06-08
- Quality: B+
- ExecPlan: [0068 App Validation Profiles](0068-app-validation-profiles.md)

Shipped:

- `.onlava.json` `validation` profiles with default profile selection, metadata, cost, path globs, env overlays, steps, and advisory artifacts.
- `onlava inspect validation --json`, `onlava validate list|inspect|graph`, `onlava validate <profile> --dry-run --json`, `onlava validate <profile> --json --write`, and `onlava validate changed --base <ref>`.
- Sequential fail-fast execution over nested profiles, configured tasks, code-backed tasks, core harness/UI harness, check/test/generate, and DB lifecycle built-ins.
- Harness-style evidence with output tails, repro commands, validation artifacts under `.onlava/harness/validation/artifacts/<run-id>/`, and latest result files.
- Optional `onlava harness --with-validation[=<profile>]` bridge that adds a compact validation pointer to the harness result.
- JSON schemas, local contract docs, agent guide, installable skill, app cookbook recipe, README command list, self-harness schema inventory, and focused tests.

Validation:

- `go test ./cmd/onlava` passed.
- `go test ./...` passed.
- `python3 -m json.tool docs/knowledge.json docs/schemas/*.json` passed.
- `onlava inspect docs --json` passed.
- Source-driven CLI smoke tests with `go run ./cmd/onlava` passed for inspect, dry-run, execution/write, and harness bridge paths.

## Harness Self Summary Output

- Status: completed
- Owner: onlava runtime / agent DX
- Completed: 2026-06-08
- Quality: B+
- ExecPlan: [0066 Harness Self Summary Output](0066-harness-self-summary-output.md)

Shipped:

- Summary-first self-harness stdout through `onlava.harness.self.summary.v1` for `--summary`, `--json`, and `--json=summary`.
- Explicit full archive stdout through `--json=full`, with `.onlava/harness/self-latest.json` preserved as the full evidence artifact.
- Compact `.onlava/harness/self-summary-latest.json` plus focused `onlava inspect harness artifact`, `diagnostics`, and `timing` drill-downs.
- Worktree-local `.onlava/harness/bin/onlava` build/freshness checks so agent validation does not overwrite the shared installed `onlava` binary.
- Changed-area ignore rules for local harness/report artifacts, repo-relative summary paths, and JSON-aware `onlava version --json` parsing.

Validation:

- `go test ./cmd/onlava` passed.
- `go test ./...` passed.
- `go build -o .onlava/harness/bin/onlava ./cmd/onlava` passed.
- `onlava harness self --summary --write` passed with warnings.
- `onlava harness self --json=summary --write` passed with warnings.
- `onlava harness self --json=full --write` passed with warnings.
- `onlava inspect harness --json` and focused harness drill-downs passed.

## ENV Harness

- Status: completed
- Owner: onlava runtime / agent DX
- Completed: 2026-06-01
- Quality: A-
- ExecPlan: [0061 ENV Harness](0061-env-harness.md)

Shipped:

- Machine-readable env registry in `docs/environment.registry.json`, validated by `docs/schemas/onlava.environment.registry.v1.schema.json`.
- Registry-backed self-harness drift checks for unregistered production env usage, test-only env leakage into production code, undocumented runtime env entries, and direct production `os.*env` calls outside `internal/envpolicy`.
- `internal/envpolicy` as the small central env access and registry layer, with production env reads/writes migrated through it.
- Secret redaction for live harness toolchain env capture based on registry secret metadata and secret-like names.
- Docs and agent guidance updates that make `.onlava.json`, CLI flags, and checked-in manifests the default configuration surfaces.

Validation:

- `go test ./cmd/onlava -run 'TestHarness.*Env|TestEnvPolicy|TestHarnessSelf'` passed.
- `go test ./internal/envpolicy` passed.
- `go test ./cmd/onlava` passed.
- `go test ./...` passed.
- `go install ./cmd/onlava` passed.
- `onlava inspect docs --json` passed.
- `onlava harness self --json --write` passed.
- `git diff --check` passed.

## onlava Script Runner

- Status: completed
- Owner: onlava runtime / developer experience
- Completed: 2026-06-01
- Quality: B+
- ExecPlan: [0058 onlava Script Runner](0058-onlava-script-runner.md)

Shipped:

- `onlava run list`, `onlava run inspect`, and `onlava run <domain>:<script> [script args...]` for app-local operational scripts.
- Filesystem-first discovery for `<domain>/scripts/<name>.script.go`, `<domain>/scripts/<name>.script.ts`, `<domain>/scripts/<name>/main.go`, and `<domain>/scripts/<name>/index.ts`.
- Strict target parsing, clear missing-script errors, and ambiguity errors unless `--lang go|typescript` disambiguates.
- Go execution via `go run`, requiring `//go:build ignore` for single-file Go scripts, plus TypeScript execution through Bun or Node with `tsx`.
- Focused tests, usage text, local-contract/cookbook docs, and a script fixture that also passes the normal app fixture matrix.

Validation:

- `go test ./cmd/onlava` passed.
- `go test ./...` passed.
- `git diff --check` passed.
- `go install ./cmd/onlava` passed.
- Focused `onlava run` fixture scenarios passed.
- `onlava harness self --json --write` was run after fixes; all feature-relevant checks and fixture matrix passed, but the overall harness remained red on the pre-existing full-suite timing budget tracked by `docs/plans/0050-test-suite-speed-hardening.md`.

## Typed Lifecycle Graph Phase 1

- Status: completed
- Owner: onlava runtime / ONLV integration
- Completed: 2026-06-01
- Quality: B+
- ExecPlan: [0057 Typed Lifecycle Graph Phase 1](0057-typed-lifecycle-graph-phase1.md)

Shipped:

- `onlava generate`, `onlava generate client`, and `onlava generate sqlc` for configured file-producing lifecycle work.
- `onlava inspect generators --json` and `onlava generate --dry-run --json` for generator graph inspection.
- `onlava db sync` with an explicit `database.apply` exec provider followed by dependent SQLC regeneration.
- `onlava task list`, `onlava task run <name>`, and `onlava task graph --json` as a thin repo-local task layer.
- `.onlava.json` config/schema support for `generators`, `database.apply`, and `tasks`, plus focused tests and docs.

Validation:

- `go test ./cmd/onlava -run 'Test(ParseGenerate|BuildSQLC|RunGenerate|RunSQLC|DBSync|TaskGraph|DBCommand)'` passed.
- `go test ./cmd/onlava` passed.
- `go test ./...` passed.
- `go install ./cmd/onlava` passed.
- `onlava harness self --json --write` was run after fixes; all feature-relevant checks passed, but the overall harness remained red on the pre-existing full-suite timing budget tracked by `docs/plans/0050-test-suite-speed-hardening.md`.

## Browser Worker Operational Hardening

- Status: completed
- Owner: onlava runtime / Temporal TypeScript workers
- Completed: 2026-05-30
- Quality: B+
- ExecPlan: [0052 Browser Worker Operational Hardening](0052-browser-worker-operational-hardening.md)

Shipped:

- Build prep skips browser runtime artifact directories: `var/browser`, `var/chrome`, and `var/playwright`.
- Build source listing and workspace copying skip unsupported non-regular files such as Unix sockets without changing symlink behavior.
- Generated TypeScript Temporal worker tests now lock supervisor PID monitoring through `ONLAVA_DEV_SUPERVISOR_PID`.
- Dev supervisor shutdown tests prove TypeScript worker children are interrupted, waited on, and detached from supervisor state.
- Detached `onlava dev` children write a generated TypeScript worker registry and conservatively reap stale registry-matched workers for the current app root and generated `worker.ts` path.
- Stale worker cleanup records a dev dashboard process event and leaves foreground `onlava worker typescript` behavior unchanged.
- Focused tests, full `go test -count=1 ./...`, binary install, `git diff --check`, and `onlava harness self --json --write` validation.

## Agent HTTPS Ingress

- Status: completed
- Owner: onlava runtime / dev agent
- Completed: 2026-05-27
- Quality: B+
- ExecPlan: [0044 Agent HTTPS Ingress](0044-agent-https-ingress.md)

Shipped:

- Explicit agent router TLS mode through `onlava agent --router-tls` and `ONLAVA_AGENT_ROUTER_TLS=1`.
- Trust-install controls through `onlava agent --trust` and `ONLAVA_AGENT_TRUST=1`, reusing the existing onlava local CA.
- Agent session routes use `https://...onlava.localhost` when the agent router runs with TLS.
- SNI-based on-demand leaf certificates for routed agent hostnames, including two-label session hosts.
- Router scheme metadata in agent health/state plus CLI docs, local contract updates, focused tests, and full `go test ./...` validation.

## Agent Detached Dev and Attach

- Status: completed
- Owner: onlava runtime / dev agent
- Completed: 2026-05-27
- Quality: B+
- ExecPlan: [0043 Agent Detached Dev and Attach](0043-agent-detached-dev-and-attach.md)

Shipped:

- `onlava dev --detach` starts an agent-backed background dev supervisor, waits for the child PID to register as session owner, writes detached supervisor output under the agent directory, and returns session details.
- Detached child supervisors skip parent-process monitoring while normal attached `onlava dev` keeps parent-death cleanup.
- `onlava attach` follows the current session logs by default and supports explicit app-root, session, limit, stream, and JSONL options.
- Command usage, README, local contract docs, focused tests, and full `go test ./...` validation.

## Agent Global Dashboard

- Status: completed
- Owner: onlava runtime / dev dashboard
- Completed: 2026-05-27
- Quality: B+
- ExecPlan: [0042 Agent Global Dashboard](0042-agent-global-dashboard.md)

Shipped:

- Agent-owned visible dashboard backend for `console.onlava.localhost/s/<session_id>`.
- Session-addressable dashboard app records so multiple worktrees for the same base app can appear independently.
- Runtime reports sent to the agent dashboard using per-session report tokens carried over the Unix-socket control API and omitted from manifests.
- Direct/per-session dashboard fallback for agent-disabled, unavailable-agent, and explicit local-proxy paths.
- Local contract updates, focused tests, full Go test suite, binary install, and self-harness snapshot refresh.

## Agent Managed Postgres and Electric

- Status: completed
- Owner: onlava runtime / dev services
- Completed: 2026-05-27
- Quality: B+
- ExecPlan: [0041 Agent Managed Postgres and Electric](0041-agent-managed-postgres-and-electric.md)

Shipped:

- Managed `dev.services.postgres` defaults for version `18` and database isolation.
- Explicit admin URL reuse plus agent substrate reuse for Postgres.
- Local Postgres startup from `initdb`/`postgres` without a mandatory Docker dependency, using an agent-private Unix socket.
- Deterministic per-session database creation and app env injection for `DatabaseURL` when not explicitly provided.
- `onlava db psql`, `onlava db reset`, and `onlava db snapshot create|restore` against the current managed session database.
- Electric as an agent-routed hidden session backend through explicit upstreams, local binary startup, or an explicitly configured Docker image.
- Contract/schema docs, focused unit coverage, full `go test ./...`, binary install, and self-harness snapshot refresh.

## Agent Shared Substrates and Dev Services

- Status: completed
- Owner: onlava runtime / dev services
- Completed: 2026-05-26
- Quality: B+
- ExecPlan: [0040 Agent Shared Substrates and Dev Services](0040-agent-shared-substrates-and-dev-services.md)

Shipped:

- Agent substrate registry for shared local dev processes.
- Shared agent-registered VictoriaMetrics, VictoriaLogs, VictoriaTraces, Grafana, and Temporal dev server reuse across sessions.
- Grafana dashboards with a `Session` variable backed by `onlava_session_id`.
- Session-scoped Temporal task queue/deployment/build env for app child processes.
- Agent-routed frontend URLs for configured frontend upstreams.
- Beta `.onlava.json` `dev.services` declarations for Postgres and Electric.
- `onlava db psql` as the current managed database shell helper.
- Follow-up Postgres/Electric lifecycle work split to [0041 Agent Managed Postgres and Electric](0041-agent-managed-postgres-and-electric.md).

## Grafana Dev Hardening

- Status: completed
- Owner: onlava dev platform / observability
- Completed: 2026-05-26
- Quality: A-
- ExecPlan: [0036 Grafana Dev Hardening](0036-grafana-dev-hardening.md)

Shipped:

- Verified Grafana readiness requires server health plus expected datasource and dashboard UIDs.
- External Grafana reuse is verified-only; unverified external instances are degraded and do not get dashboard links.
- Grafana upstream and browser public URLs are split, including local proxy `root_url` provisioning.
- Managed pinned Grafana is preferred over `PATH`; `PATH` fallback is version-probed.
- Grafana archives are checksum-verified before extraction, including custom download SHA support.
- Child Grafana processes filter inherited `GF_*` overrides by default.
- Datasource provisioning prunes stale datasources and includes org/version metadata.
- Dashboard state exposes availability/readiness booleans, and the UI disables links unless Grafana is verified usable.
- Dashboard metrics now use the emitted `onlava_request_duration_seconds` contract.
- Fake-process, external-verification, provisioning, local-proxy URL, and optional live-smoke test coverage.

## Grafana Dev Integration

- Status: completed
- Owner: onlava dev runtime
- Completed: 2026-05-25
- Quality: B+
- ExecPlan: [0033 Grafana Dev Integration](0033-grafana-dev-integration.md)

Shipped:

- `onlava dev` can supervise local Grafana alongside VictoriaMetrics, VictoriaLogs, and VictoriaTraces.
- Generated Grafana config, datasource provisioning, and dashboard JSON live under `.onlava/grafana/`.
- Stable datasource UIDs for VictoriaMetrics, VictoriaLogs, and Jaeger-compatible VictoriaTraces.
- Stable dashboard UIDs for overview, logs, and endpoint debugging dashboards.
- Onlava dashboard Observability route with Grafana status, paths, datasource status, and deep links.
- `onlava dev --json` Grafana events and `run.ready` metadata.
- Env controls for opt-in, disable, required mode, binary resolution, download, port, root directory, version, and plugin preinstall.
- Browser validation against a live `onlava dev` stack plus supervised shutdown and headless runtime smoke coverage.

## UI Guardrail Hardening

- Status: completed
- Owner: onlava dashboard
- Completed: 2026-05-09
- Quality: A-
- ExecPlan: [0012 UI Guardrail Hardening](0012-ui-guardrail-hardening.md)

Shipped:

- Pinned, stricter `bun run shadcn:add @onlava/<item>` wrapper that rejects unsupported flags, non-onlava items, unsafe overwrite, and occupied registry port.
- UI static validation for registry item source and target declarations.
- Stronger UI import scanning for multiline imports, re-exports, dynamic imports, and CommonJS requires.
- Stronger className drift warnings for `cn(...)`, template literal, and conditional literal forms.
- Fixture tests for UI static guardrail bypasses.
- Explicit `tailwindcss` UI devDependency.
- `PageToolbar` layout and `@onlava/page-toolbar` registry item.
- Optional sidebar/inspector/event-stream slots no longer create empty fixed-width layout columns.

## Dashboard Data Explorer

- Status: completed
- Owner: onlava dashboard
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0013 Dashboard Data Explorer](0013-dashboard-data-explorer.md)

Shipped:

- Dashboard `/$appId/data` route.
- Data Explorer page composed from onlava `DataExplorerLayout`, `PageToolbar`, and primitives.
- Dashboard RPC bridge for data inspect, metadata-validated record queries, and outbox event tail reads.
- Tenant/object/field/index/migration/trigger/outbox inspection panels.
- Record table with limit and JSON filter controls.
- Focused backend and UI coverage for the new bridge and route surface.

## Browser UI Harness

- Status: completed
- Owner: onlava dashboard
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0014 Browser UI Harness](0014-browser-ui-harness.md)

Shipped:

- `onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]`.
- `onlava.harness.ui.v1` JSON schema.
- Temporary `onlava dev --json` startup path with isolated app/dashboard ports when no dashboard URL is provided.
- Browser route checks for dashboard home, API Explorer, service catalog, traces, Data Explorer, and DB Explorer.
- Screenshot artifacts plus console and network JSONL artifacts under `.onlava/harness/ui/`.
- Focused command tests using a fake browser runner so normal Go tests do not require Chrome.
- Current follow-up debt is deeper fixture-backed mutation coverage; the browser harness itself and route-specific journeys are implemented.

## Dashboard Slot-Layout Migration

- Status: completed
- Owner: onlava dashboard
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0015 Dashboard Slot-Layout Migration](0015-dashboard-slot-layout-migration.md)

Shipped:

- Dashboard shell now composes `AppShell` instead of duplicating shell structure and style ownership.
- Top navigation class recipes live in the onlava layout layer.
- API Explorer and Pub/Sub route actions now use the onlava `Button` primitive.
- `AppShell` render coverage for stable layout markers and styling helpers.
- Self-harness UI static architecture check reports 0 className warnings.

## Data Platform Indexes and Cursor Pagination

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0016 Data Platform Indexes and Cursor Pagination](0016-data-platform-indexes-and-cursor-pagination.md)

Shipped:

- `onlava_data.indexes` and `onlava_data.index_fields` metadata tables.
- Public `data.Store.CreateIndex` and `data.Store.ListIndexes` APIs.
- PostgreSQL btree and GIN physical index creation through migration rows and advisory locks.
- `onlava inspect data` index output with physical existence and drift status.
- Keyset cursor pagination with `id` tie-breaker, encoded cursor state, and sort-shape rejection.
- PostgreSQL-backed coverage for index creation, inspect output, and cursor pagination.

## Data Platform Relationships

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0017 Data Platform Relationships](0017-data-platform-relationships.md)

Shipped:

- Public relation settings for dynamic data fields.
- `many_to_one` relation fields backed by UUID columns and PostgreSQL foreign keys.
- `many_to_many` relation fields backed by physical join tables.
- One-hop `many_to_one` relation path support for filters, sorts, and selected fields.
- Inspect data relation output for target object, relation kind, delete behavior, inverse field, and join table metadata.
- PostgreSQL-backed tests for FK enforcement, join-table creation, relation-path queries, and inspect output.

## Data Platform Saved Views

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0018 Data Platform Saved Views](0018-data-platform-saved-views.md)

Shipped:

- `onlava_data.views` and `onlava_data.view_fields` metadata tables.
- Public saved-view API through `data.Store`.
- Query-by-view execution through the existing metadata SQL compiler.
- Inspect data output for saved views.
- Data Explorer saved view selector.
- PostgreSQL-backed tests for persistence, validation, query execution, updates, deletes, and inspect output.

## Data Platform Public Contract

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0019 Data Platform Public Contract](0019-data-platform-public-contract.md)

Shipped:

- `docs/data-platform.md` as the human-facing beta data package guide.
- Public `data.Error`, `data.ErrorCode`, and `data.CodeOf(err)` helpers.
- Public contract notes for indexes, relations, saved views, cursors, live events, triggers, and error codes.
- Compile-only `examples/data-platform` package.
- Focused public package tests for error classification.

## onlava UI Registry and Agent Guardrails

- Status: completed
- Owner: onlava dashboard
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0011 onlava UI Registry and Agent Guardrails](0011-onlava-ui-registry-and-agent-guardrails.md)

Shipped:

- `@onlava/*` shadcn registry configuration under `ui/components.json`.
- Guarded `bun run shadcn:add @onlava/<item>` wrapper with local registry serving and dry-run-first behavior.
- onlava-owned UI primitives and slot layouts under `ui/src/components/primitives` and `ui/src/components/layouts`.
- Initial registry items for dashboard/data layouts plus ONLV-ported button/card/dialog/input/app surface/filter/sidebar components.
- `docs/ui-agent-contract.md`.
- Self-harness UI static architecture checks for registry/script/import boundaries and className migration warnings.
- ONLV app screen imports switched to onlava-facing primitives/layout paths while preserving current rendered UI.

## onlava Go Runner Phase 1

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: B

Shipped:

- `onlava serve`, `onlava run`, `onlava build`, `onlava test`, `onlava check`, `onlava logs`, and beta `onlava psql`
- onlava API parser/codegen/runtime for common Go service behavior
- Secrets from `.env`
- local HTTPS proxy support
- cron, middleware, Pub/Sub, tracing, logging, DB query tracing, and dashboard support

## Stable Inspect And Harness Contracts

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: A

Shipped:

- `onlava inspect app|routes|services|endpoints|wire|build|paths --json`
- beta `onlava inspect traces|metrics --json`
- `onlava inspect docs --json`
- `.onlava/gen/*` and `.onlava/build/latest.json`
- `onlava harness --json --write`
- `onlava harness self --json --write`

## Split `onlava dev` From Headless Runtime

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: B+
- ExecPlan: [0001 Split `onlava dev` From Headless `onlava run`](0001-devrun-command-split.md)

Shipped:

- `onlava dev` owns the development supervisor, dashboard, removed agent transport, local proxy, watch/rebuild loop, and development logs.
- The headless runtime command builds once and starts the app without dashboard, local proxy, removed agent transport, or file watching. It is now spelled `onlava serve`; the historical plan used `onlava run`.
- Generated app binaries are headless by default unless development behavior is explicitly enabled.
- Command parsing, tests, usage text, and local contract were updated for the split.

## onlava v0 Release Readiness

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: B+
- ExecPlan: [0002 onlava v0 Release Readiness](0002-v0-release-readiness.md)

Shipped:

- Stable/dev/beta surface classification in `docs/local-contract.md`.
- `onlava version --json` and `onlava.version.v1` schema.
- Dev/admin/pprof route gating so public app listeners stay production-like by default.
- Opt-in local proxy/trust behavior for `onlava dev`.
- Central `.env` parsing and production secret validation.
- Build workspace filtering for local artifacts and secret files.
- Response JSON semantics tests and `scripts/release-gate.sh`.

## Queryable Observability

- Status: completed
- Owner: onlava observability
- Completed: 2026-04-27
- Quality: B

Shipped:

- Trace query filters for service, endpoint, trace ID, status, duration, time window, and sort order.
- Metrics rollups by service and endpoint.
- Log-level counts and trace event counts from the dashboard SQLite store.

## Victoria Observability Sidecars

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: A
- ExecPlan: [0003 Victoria Observability Sidecars](0003-victoria-observability-sidecars.md)

Shipped:

- `onlava dev` starts VictoriaMetrics, VictoriaLogs, and VictoriaTraces sidecars by default while preserving SQLite observability writes.
- Sidecars use loopback ports, `.onlava/victoria/` storage, automatic binary resolution/download, and graceful shutdown with the dev supervisor.
- onlava exports built-in trace, log, and request-duration metric reports to Victoria over OTLP protobuf.
- Dashboard and inspect trace reads prefer VictoriaTraces with SQLite fallback.

## onlava-Native Local HTTPS Proxy

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: B
- ExecPlan: [0004 onlava-Native Local HTTPS Proxy](0004-onlava-native-localproxy.md)

Shipped:

- Replaced embedded Caddy local HTTPS proxying with a standard-library route table, TLS certificate cache, trust installer hooks, HTTPS reverse proxy, and optional HTTP redirect listener.
- Preserved `internal/localproxy` public API names and the existing onlava local URL shape.
- Removed `internal/localproxy/caddyimports.go` plus Caddy, CertMagic, and ZeroSSL module dependencies.
- Added behavior tests for routing, frontend config/catch-all handling, Host rewriting, redirects, certificate SANs and reuse, trust installer injection, and lifecycle cleanup.

## onlava Data Platform Vertical Slice

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-08
- Quality: B
- ExecPlan: [0005 onlava Data Platform](0005-onlava-data-platform.md)

Shipped:

- `github.com/pbrazdil/onlava/data` public facade and `internal/objectstore` implementation.
- PostgreSQL metadata bootstrap, real object tables, real field columns, schema migration rows, advisory locks, and physical schema verification.
- Metadata-validated SQL query compiler, transactional record mutations, transactional outbox rows, in-process query-aware live routing, and SSE replay/fanout.
- `testdata/apps/data-platform` fixture app using ordinary onlava services and raw SSE.
- Unit coverage plus testcontainers-backed PostgreSQL integration coverage with `ONLAVA_TEST_DATABASE_URL` override support.

Follow-ups:

- [0007 Data Platform Validation and Inspect](0007-data-platform-validation-and-inspect.md) for PostgreSQL CI and inspectability.
- [0008 Data Platform Migration and Live Hardening](0008-data-platform-migration-and-live-hardening.md) for migration/live correctness.
- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) for direct SQL change capture after hardening.

## onlava Standard Auth

- Status: completed
- Owner: onlava runtime
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0006 onlava Standard Auth](0006-onlava-standard-auth.md)

Shipped:

- onlava-owned standard auth module under `github.com/pbrazdil/onlava/auth`.
- HCL/sqlc auth database tooling for the `onlava_auth` PostgreSQL schema.
- Built-in auth handler and endpoint registration for apps with `"auth": {"enabled": true}`.
- Standard auth TypeScript client generation and inspect visibility.
- ONLV cutover to consume the top-level onlava auth surface instead of owning auth business logic.
- Production migration runbook for preserving existing users, tenants, memberships, password hashes, sessions, and one-time tokens.

## Data Platform Validation and Inspect

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0007 Data Platform Validation and Inspect](0007-data-platform-validation-and-inspect.md)

Shipped:

- `testcontainers-go` PostgreSQL coverage in the regular Go CI job, with DB-backed objectstore and data-inspect tests.
- `onlava inspect data --json --database-url <postgres-url> [--tenant <key>] [--object <name>]`.
- Data inspect JSON schema, docs, self-harness schema tracking, and fixture README.
- More reliable PostgreSQL integration cleanup and explicit SSE watermark usage in the live test.

Follow-ups:

- [0008 Data Platform Migration and Live Hardening](0008-data-platform-migration-and-live-hardening.md) for migration edge cases, live-sync correctness, and public `data` API cleanup.
- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) after migration/live hardening.

## Data Platform Migration and Live Hardening

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0008 Data Platform Migration and Live Hardening](0008-data-platform-migration-and-live-hardening.md)

Shipped:

- Deterministic readable physical table and column names with stable suffixes.
- Retry-safe object and field creation with physical schema verification, drift detection, and failed migration recording.
- PostgreSQL-backed idempotence, concurrency, failure/retry, and drift tests.
- Live update hardening for created/updated/deleted matching, reconnects, selected-field stripping, permission row filters, heartbeats, unsubscribe cleanup, and slow subscribers.
- Public `data.Store` wrapper and app-facing filter/sort helpers.

Follow-ups:

- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) for direct SQL outbox events.

## Trigger-Backed Outbox

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md)

Shipped:

- Optional per-object record-table triggers that capture direct SQL changes.
- Shared `onlava_data.record_change_trigger()` function that writes logical events to `onlava_data.outbox_events`.
- Transaction-local actor context and explicit-mutation skip flag to avoid duplicate events.
- SSE polling/replay compatibility for trigger-created events.
- Inspect output for trigger enablement and physical trigger presence.

## Data Platform Indexes and Cursor Pagination

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0010 Data Platform Indexes and Cursor Pagination](0010-data-platform-indexes-and-pagination.md)

Shipped:

- Metadata-backed logical indexes in `onlava_data.indexes` and `onlava_data.index_fields`.
- Public `data.Store.CreateIndex` and `data.Store.ListIndexes` APIs.
- Migration-managed deterministic physical PostgreSQL indexes with advisory locks, migration rows, and catalog verification.
- Btree scalar and compound index support plus explicit GIN indexes for multi-select and JSON/raw JSON fields.
- `onlava inspect data --json` index reporting with physical presence/drift state.
- Keyset cursor pagination for `QueryRecords` and opaque `RecordPage.NextCursor` values.
- Fixture app endpoints and README examples for index creation/listing and cursor pagination.

## Data Platform Search

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0020 Data Platform Search](0020-data-platform-search.md)

Shipped:

- Field-level search metadata with `is_searchable` and `search_weight`.
- PostgreSQL-backed `onlava_data.search_documents` table with a GIN-indexed `tsvector` document.
- Transactional search document maintenance for create, update, and delete through the public data mutation path.
- Object-wide `search` query filter, public `data.Search(...)` helper, and live-event search matching.
- `onlava inspect data --json` searchable-field reporting and Data Explorer search input.

Follow-ups:

- Direct SQL edits do not refresh search documents in this version. Add trigger-backed search refresh or explicit rebuild tooling before treating direct SQL search freshness as stable.

## Standard Auth x Data Tenant Permissions

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0021 Standard Auth x Data Tenant Permissions](0021-auth-data-tenant-permissions.md)

Shipped:

- `data.Actor` tenant awareness and `data.ActorFromContext` standard-auth tenant mapping.
- `data.TenantKeyFromContext`, `data.RequireTenantKeyFromContext`, and `data.TenantKeyFromActor` helpers.
- `data.StandardAuthPermissions`, which maps standard-auth `tenant_id` directly to data `TenantKey`, fails closed on mismatches, and delegates to an optional base permission provider.
- Tenant key propagation through object and field permission refs.
- Tests for same-tenant access, cross-tenant denial, delegated row filters, and live subscription denial.

## Data Import, Export, and Fixtures

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0022 Data Import, Export, and Fixtures](0022-data-import-export-fixtures.md)

Shipped:

- `onlava.data.export.v1` JSON schema.
- Public `data.Store.ExportTenant` and `data.Store.ImportTenant` APIs.
- Portable bundles for logical tenants, objects, fields/options, indexes, saved views, and records.
- Transactional import through existing mutation paths, with new record IDs and `record_id_map` reconciliation.
- Fixture app export/import endpoints and `company-export.json` fixture data.
- PostgreSQL-backed round-trip coverage for metadata, records, indexes, saved views, and ID remapping.

## Skill Refresh and Agent Onboarding

- Status: completed
- Owner: onlava maintainers
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0027 Skill Refresh and Agent Onboarding](0027-skill-refresh-and-agent-onboarding.md)

Shipped:

- Refreshed `SKILL.md` for current onlava workflows.
- Added current coverage for the data platform, standard auth tenant permissions, dashboard Data Explorer, browser UI harness, UI registry guardrails, ONLV layout migration expectations, and validation command matrices.
- Linked the skill to the local contract, app cookbook, data-platform overview/runbook, UI agent contract, and active plans.

## onlava App Development Cookbook

- Status: completed
- Owner: onlava runtime
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0028 onlava App Development Cookbook](0028-onlava-app-development-cookbook.md)

Shipped:

- `docs/app-development-cookbook.md` with practical recipes for building onlava apps.
- Recipes for typed endpoints, auth endpoints, private calls, service initialization, middleware, request tags, status responses, coded errors, Pub/Sub, cron, pgxpool tracing, TypeScript clients, local proxy config, debugging, harness workflows, and common mistakes.
- Docs index and knowledge index entries for agent discovery.

## Data Platform Developer Runbook

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0029 Data Platform Developer Runbook](0029-data-platform-developer-runbook.md)

Shipped:

- `docs/data-platform-runbook.md` for operational data-platform workflows.
- Runbook coverage for object/field creation, options, composites, relations, indexes, saved views, CRUD, queries/cursors/search, SSE, trigger-backed outbox, import/export, standard-auth permissions, inspect output, migration recovery, drift debugging caveats, performance notes, and beta limitations.
- Docs index and knowledge index entries for agent discovery.

## Documentation Drift Harness

- Status: completed
- Owner: onlava maintainers
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0030 Documentation Drift Harness](0030-documentation-drift-harness.md)

Shipped:

- `SKILL.md` is now a self-harness knowledge entrypoint.
- Self-harness checks required installed-skill capability mentions such as `onlava inspect data --json`, `onlava harness ui --json`, `github.com/pbrazdil/onlava/data`, the `@onlava` registry, and `onlava harness self --json --write`.
- `docs/knowledge.json` is checked for important docs including `SKILL.md`, the app cookbook, the data-platform runbook, the UI agent contract, and the local contract.
- Regression coverage for stale `SKILL.md` detection.

## ONLV Direct onlava Registry Adoption

- Status: completed
- Owner: onlava dashboard / ONLV app
- Completed: 2026-05-10
- Quality: B+
- ExecPlan: [0031 ONLV Direct onlava Registry Adoption](0031-onlv-direct-onlava-registry-adoption.md)

Shipped:

- onlava-approved primitive registry source under `ui/src/components/registry/primitives`.
- Individual `@onlava/*` primitive registry items plus the aggregate `@onlava/primitives` item.
- ONLV app mirrored registry outputs under `apps/app/src/components/primitives`.
- ONLV app-facing imports moved away from raw `@/components/ui/*` and local product-layout compatibility imports.
- ONLV primitive barrel now explicitly exports registry-owned primitive files instead of re-exporting `../ui`.
- Removed unused ONLV app generic compatibility shims and the old local `components/ui` source tree, and updated ONLV app agent instructions to use registry-owned primitives/layouts.
- Added `.ts` public entrypoint re-exports for migrated primitives that Vite may still request during hot reload.
- `apps/app/scripts/check-onlava-ui-registry.mjs`, wired into `bun run typecheck`, to prevent future drift back to local raw shadcn imports.
- ONLV app visual harness remained stable with 24/24 snapshots passing.

## Remove Pub/Sub Package

- Status: completed
- Owner: onlava runtime
- Completed: 2026-05-25
- Quality: B+
- ExecPlan: [0034 Remove Pub/Sub Package](0034-remove-pubsub-package.md)

Shipped:

- Removed the public `github.com/pbrazdil/onlava/pubsub` package, runtime hooks, dashboard/admin surfaces, schemas, and current docs.
- Moved service-method background handler support to `github.com/pbrazdil/onlava/temporal`.
- Migrated ONLV async jobs in `codexsvc`, `jobs`, `house`, and `maps` to native Temporal workflows and activities.
- Validation passed for onlava; ONLV validation is blocked only by the native house `torch/torch.h` environment prerequisite.

## onlava Agent MVP

- Status: completed
- Owner: onlava runtime
- Completed: 2026-05-26
- Quality: B
- ExecPlan: [0037 onlava Agent MVP](0037-onlava-agent-mvp.md)

Shipped:

- `internal/agent`, a standard-library local daemon package with Unix control socket, JSON session registry, host-based HTTP router, session manifest writing, and Unix-socket aware reverse proxying.
- `onlava agent`, `onlava status --json`, and `onlava down`.
- `onlava dev` auto-starts/connects to the agent unless disabled, registers the worktree session, writes `.onlava/sessions/<session_id>/manifest.json`, updates status, and advertises routed API/dashboard/removed agent transport URLs when no explicit local proxy is active.
- Runtime servers support `ONLAVA_LISTEN_NETWORK=unix` with TCP still available.

## Agent Private Dev Backends

- Status: completed
- Owner: onlava runtime
- Completed: 2026-05-26
- Quality: B+
- ExecPlan: [0038 Agent Private Dev Backends](0038-agent-private-dev-backends.md)

Shipped:

- `onlava dev` with no explicit listen flags now registers a session-private Unix API backend at `.onlava/sessions/<session_id>/run/api.sock` when the agent is available.
- Explicit `--listen` and `--port` continue to use TCP and register TCP API backends.
- The legacy local HTTPS proxy is opt-in through `--proxy`, `--trust`, or `ONLAVA_LOCAL_PROXY=1`; those paths use hidden loopback TCP because the proxy only supports TCP upstreams.
- App children receive `ONLAVA_LISTEN_NETWORK` and `ONLAVA_LISTEN_ADDR`, and supervisor startup probes support both TCP and Unix listeners.

## Agent Session Identity and Signals

- Status: completed
- Owner: onlava runtime / observability
- Completed: 2026-05-26
- Quality: B+
- ExecPlan: [0039 Agent Session Identity and Signals](0039-agent-session-identity-and-signals.md)

Shipped:

- Session, base-app, and runtime-app identity are passed into dev children and exposed through runtime metadata plus `/__onlava/config`.
- Devdash app records, process output, logs JSONL, trace summaries, trace events, log events, inspect traces, and inspect metrics carry session identity where applicable.
- `onlava logs --session current|<id>`, `onlava inspect traces --session current|<id> --json`, and `onlava inspect metrics --session current|<id> --json` filter session-scoped records.
- Victoria trace/log/metric export includes session labels.
- Dev-mode standard auth receives session-routed local URL env vars and host-only cookie-domain defaults.
- Dev-mode Temporal receives session-scoped task queue prefix, worker deployment name, and build ID env vars.
