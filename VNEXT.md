# Scenery VNEXT: Roadmap & Next Milestones

> Authoritative current architecture and contracts live in
> [ARCHITECTURE.md](ARCHITECTURE.md), [docs/spec/SPEC.md](docs/spec/SPEC.md),
> and [docs/spec/WORKER-ABI.md](docs/spec/WORKER-ABI.md). This document records
> what has shipped from the polyglot host plan and what remains ahead.

## 1. Shipped Foundation (In `main`)

The original Go + Rust polyglot and static Vite/React milestones have been
completed and surpassed:

- **Unified Rust application host (`scenery-kernel`).** Every application—in
  development (`scenery up`), ephemeral testing (`scenery-kernel --test`), and
  production (`scenery build`, `scenery build --oci`)—runs on
  `sdk/rust/scenery-kernel`. The Host owns public ingress, binding policy,
  standard auth/sessions/organizations/connections, scoped worker capabilities,
  durable execution, schedules, domain events, MCP/assistants, static Vite/React
  serving, and worker supervision.
- **Polyglot Worker ABI v1 (`h2c` over UDS) across four runtimes.** Go, Rust,
  JavaScript/TypeScript (Bun), and Python (`uv` zipapp) workers share one
  host/worker protocol, caller-side generated `service.client` wrappers, and a
  shared cross-language wire conformance corpus (`int64`/`uint64`, `decimal`,
  canonical floats, `optional(nullable(T))`, enums, unions, and sealed outcomes).
  Editing a worker rebuilds and swaps only that worker while unaffected workers
  keep serving.
- **Typed streaming & OpenAPI 3.1.1.** Direct HTTP bindings support binary
  streams (`stream`) and typed Server-Sent Events (`stream_out` via
  `application/vnd.worker.events`). `scenery openapi` and
  `scenery generate openapi` export deterministic, brand-neutral OpenAPI 3.1.1
  JSON/YAML specifications correlated with each gateway's `openapi_revision`.
- **Polyglot domain event bus.** Declared `event`, `service.publisher`, and
  `subscription` resources deliver cross-module, cross-language events with
  keyed deduplication, subscription policy checks, atomic PostgreSQL fan-out
  into durable jobs, and cascade depth enforcement.
- **Software-factory workflow & fast isolation.** Isolated per-worktree
  runtimes with `<50ms` PostgreSQL template database branching
  (`scenery test --branch-db`, `scenery worktree create --prepare-db`),
  lightweight `scenery test --with-host`, deterministic `SCN9xxx` failure replay
  and regression-test emission (`scenery replay <token> [--emit-test]`), plus
  JSON-native agent primitives (`context`, `call`, `db query`, `durable run`,
  `add module`, `ps --wait`).
- **Production deployment surface.** Self-contained bundles (`scenery build`)
  and two-stage distroless Linux images (`scenery build --oci`) with `--config`
  / `SCENERY_CONFIG_FILE` `{"$env": "NAME"}` secret resolution, S3-compatible
  SigV4 object storage, OTLP/HTTP protobuf telemetry export, and
  PostgreSQL-leased `schedule_slots` for multi-replica scheduling.

---

## 2. Next Milestones

### Milestone A — Prebuilt Toolchain Distribution (Zero Host-Build Prerequisites)

Today, running or building an app compiles `sdk/rust/scenery-kernel` from embedded
Cargo sources on first cache miss (`internal/rustbuild/host.go`), which means
even Go-only, TypeScript-only, or Python-only apps require a local Rust
toolchain (`cargo +1.99.1`) alongside Go.

- Ship prebuilt, checksum-verified `scenery` + `scenery-kernel` release artifacts
  per supported OS/architecture (and wire `framework_version` resolution to them
  without requiring `go.mod` or Cargo on the developer machine).
- Keep source fallback cleanly scoped to Scenery framework co-development
  (`scenery framework use --source`).

**Done when:** A TypeScript-only or Python-only app passes `scenery check`,
`scenery up`, `scenery test --with-host`, and `scenery build` on a machine with
neither Go nor Rust installed.

### Milestone B — Rust Worker Native SQL Supply & Parity

Go, JS/TS, and Python workers receive resolved per-service PostgreSQL URLs at
startup, whereas Rust worker startup currently passes `None` in
`sdk/rust/scenery-kernel/src/plan.rs`.

- Project declared SQL dependencies (`databases` map keyed by dependency name)
  into Rust worker startup config and generated `scenerycontract` constructor
  inputs, matching the Go/JS/Python contract boundary.
- Extend the polyglot fixture and probes to prove Rust native SQL reads/writes
  alongside Go, TS, and Python workers under both `scenery up` and
  `scenery test --with-host --branch-db`.

### Milestone C — Server-Rendered (SSR) Frontend Upstreams

Static Vite/React (`adapter = "vite"`, `mode = "static"`) is served directly by
`scenery-kernel` with API-safe SPA fallback and immutable asset caching.

- Add an explicit `mode = "ssr"` upstream frontend contract behind the same
  origin router in both `scenery up` and `scenery build` bundles (supporting
  SSR frameworks such as Next.js or TanStack Start while preserving same-origin
  auth cookies, `/api/*` precedence, and end-user HTTP neutrality).

### Milestone D — Multi-Replica & Operational Hardening

Multi-replica deployments already share PostgreSQL durable queues,
`schedule_slots`, S3 storage, and OTLP export, but each deployment unit still
packages one Host + all workers together.

- Define rolling-upgrade and graceful-drain guarantees across replicas (durable
  revision migrations without downtime, readiness/liveness probes, structured
  production diagnostics).
- Evaluate whether workload-specific scaling warrants splitting worker pools
  from the ingress Host, guided by real production measurements rather than
  speculative complexity.

### Milestone E — Latency & Verification Ergonomics

- Benchmark and tighten edit-to-ready rebuild latency across Go, Rust, JS/TS,
  and Python workers (keeping unaffected workers warm and measuring via
  `--benchmark edit-latency`).
- Expand fault-injection coverage across all four runtimes (mid-call crashes,
  client cancellation on `stream`/`stream_out`, durable lease expiry, and
  event cascade limits).

---

## 3. Explicit Non-Goals

- **No rewrite of the `.scn` compiler, CLI, or worktree manager in Rust.** The
  control plane stays in Go; `scenery-kernel` owns the application runtime plane.
- **No distributed cross-service SQL transactions.** One SQL transaction belongs
  to one service; cross-service consistency uses idempotent operations, durable
  executions, or domain events.
- **No duplicated runtime semantics in language SDKs.** Ingress, contract
  validation, auth, policy, durable orchestration, scheduling, and event fan-out
  remain in one Host.
- **No compatibility aliases or parallel legacy protocols.** One rolling `.scn`
  specification, compiler, Worker ABI, and machine contract.
