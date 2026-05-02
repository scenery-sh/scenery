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
