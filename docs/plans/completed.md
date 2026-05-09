# Completed Plans

This file records completed milestones so agents can distinguish shipped behavior from future intent.

Completed means implemented or shipped at least once. It does not imply stable
v0 support. Use [../local-contract.md](../local-contract.md) as the source of
truth for stable, beta, dev-only, and compatibility-mode classification.

## onlava Go Runner Phase 1

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: B

Shipped:

- `onlava run`, `onlava build`, `onlava test`, `onlava check`, `onlava logs`, and beta `onlava psql`
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

## Split `onlava dev` From Headless `onlava run`

- Status: completed
- Owner: onlava runtime
- Completed: 2026-04-27
- Quality: B+
- ExecPlan: [0001 Split `onlava dev` From Headless `onlava run`](0001-devrun-command-split.md)

Shipped:

- `onlava dev` owns the development supervisor, dashboard, MCP, local proxy, DB Studio, watch/rebuild loop, and development logs.
- `onlava run` builds once and starts the app headlessly without dashboard, local proxy, DB Studio, MCP, or file watching.
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
- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) for direct SQL/DB Studio change capture after hardening.

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

- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) for direct SQL/DB Studio outbox events.

## Trigger-Backed Outbox

- Status: completed
- Owner: onlava data platform
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md)

Shipped:

- Optional per-object record-table triggers that capture direct SQL and DB Studio changes.
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
