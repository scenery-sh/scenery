# Shortlink (Polyglot Example)

A compact, end-to-end Scenery example application demonstrating **Go**, **Rust**, **TypeScript**, and **Python** services plus a **React + Vite** frontend running behind one `scenery-kernel` host.

## Services & Capabilities

| Module | Runtime | Role | Demonstrated Capabilities |
|---|---|---|---|
| `links` | **Go** | Link CRUD and `GET /api/r/{slug}` redirects (`302` + `Location` header) | Managed PostgreSQL, session authentication, authorization policy (`allow_url`), cross-language RPC client (`guard`), transactional domain events (`link_created`, `link_clicked`) |
| `guard` | **Rust** | URL validation, deterministic base62 slugging, SVG QR badge generation | Internal RPC handler, binary `delivery = "stream"` (`GET /api/links/{slug}/qr`), Object Storage caching (`assets`) |
| `preview` | **TypeScript** (Bun) | Background link metadata enrichment and real-time click feed | Durable execution (`mode = "durable"` with retry & deduplication) triggered by `subscription`, native PostgreSQL via `bun` SQL, typed Server-Sent Events (`delivery = "stream_out"`, `codec = "sse"` on `GET /api/events/live`) |
| `analytics` | **Python** (uv) | Click tracking, live summary stats (`GET /api/stats`), and periodic rollups | Pure-stdlib Python worker, `subscription` on `link_clicked`, scheduled execution (`schedule "analytics_rollup"` every `1m`), Object Storage read/write, exact `decimal` and `int64` wire types |
| `web` | **React + Vite** | Single-origin UI consuming the generated TypeScript fetch client | Generated `typescript_client`, same-origin Vite dev proxy, binary stream display, typed `AsyncIterable` SSE consumption |

## Quick Start

From `examples/shortlink`:

```sh
# 1. Install frontend dependencies once
(cd web && bun install)

# 2. Generate contracts and the typed browser client
scenery generate -o json

# 3. Validate all .scn contracts, Go, Rust, TypeScript, and Python services
scenery check -o json

# 4. Run the full stack (Rust host, 4 workers, managed PostgreSQL, Vite frontend)
scenery up
```

When developing inside this repository, invoke the local harness binary instead of a globally installed CLI:

```sh
../../.scenery/harness/bin/scenery generate -o json
../../.scenery/harness/bin/scenery check -o json
../../.scenery/harness/bin/scenery up
```

## Trying the Flow

1. Open the URL printed by `scenery up`.
2. Enter any `https://...` URL and click **Shorten** (try `https://blocked.example` to see the declarative `allow_url` policy reject it with `403`, or `http://localhost` to see the Rust `guard` reject it with `422`).
3. Watch the link title populate asynchronously from the **TypeScript** durable worker and the SVG QR code stream from the **Rust** worker (cached in local Object Storage).
4. Click the `/api/r/{slug}` link — **Go** redirects with `302`, **TypeScript** streams the click over SSE into the **Live Clicks** panel, and **Python** updates click analytics and writes periodic rollups on its 1-minute schedule.
