# Scenery VNEXT: Polyglot Go + Rust Services & Static Vite/React Frontends

## Vision & Product Promise

Scenery declares and operates the application from one canonical `.scn` graph.
Each module lives in its own directory (just as today) and implements its
services in the language that fits best—starting with **Go** and **Rust**, with
**Vite + React (static)** frontends served under the same origin in both
development and production.

> Scenery owns the contract, runtime semantics, and developer/agent workflow.
> Each module implements its declared operations in Go or Rust, and static
> Vite/React frontends ship in the same origin and production bundle.

### Core Strategic Choices

1. **Keep the compiler, CLI, worktree engine, and shared runtime host in Go for
   now.** Do not stall product delivery with a Big Bang rewrite of the control
   plane into Rust. Revisit moving a narrow host kernel to Rust only after the
   polyglot boundary is proven and measured.
2. **One module = one directory.** Preserve Scenery's existing repository
   structure (`app.scn` at root, `package.scn` + implementation + generated
   contracts inside each module folder).
3. **One language per service.** Language choice is a service boundary (build,
   dependencies, DB driver, lifecycle, restart), never a per-handler toggle.
4. **One shared runtime host, thin language SDKs.** Routing, HTTP neutrality,
   contract JSON validation, auth/OIDC, authorization policy, durable execution,
   schedules, MCP federation, generation pinning, and static frontend serving
   live in **one** Host—never duplicated per language.
5. **Same topology in development and production.** Polyglot apps run the same
   manifest-driven Host + per-service worker processes + static frontend bundle
   locally and in production.
6. **Zero Go toolchain requirement for Rust-only apps.** A pure Rust + Vite/React
   application must pass `check`, `up`, and `build` without `go.mod` or Go
   installed.

---

## 1. Repository & Module Layout

Every module stays in its own directory, owning its `package.scn`, authored
implementation, and generated contract directory under `managed_generated_roots`:

```text
myapp/
├── .scenery.json              # envs, Vite/React frontends, ports/domains, framework pin
├── app.scn                    # application, gateways, providers, toolchains/targets, modules
├── go.mod                     # present only when the app has Go modules
├── Cargo.toml                 # optional Cargo workspace aggregating Rust modules
├── orders/                    # Go module (one folder = one module)
│   ├── package.scn            # service "orders" { runtime = "go" ... }
│   ├── service.go
│   └── scenerycontract/       # generated Go contracts + caller-side clients
├── pricing/                   # Rust module (one folder = one module)
│   ├── package.scn            # service "pricing" { runtime = "rust" ... }
│   ├── Cargo.toml
│   ├── src/lib.rs
│   └── scenerycontract/       # generated Rust local path-crate (types, trait, clients, ABI glue)
└── web/                       # Vite + React frontend
    ├── package.json
    ├── vite.config.ts
    ├── src/
    └── generated/             # generated TypeScript API client (+ optional UI catalog)
```

### Layout Invariants

- **`.scn` stays the single source of application semantics.** Go/Rust code only
  implements declared services and handlers; language tooling verifies ABI
  conformance and never injects hidden graph semantics.
- **Swapping a module's runtime (Go ↔ Rust) does not change the public
  contract.** Types, operations, bindings, and policies belong to the contract
  revision; `service.runtime`, implementation locators, toolchains, and build
  inputs belong to the implementation revision.
- **Shared non-contract code stays intra-language.** Internal helper packages or
  crates remain within Go or within Rust. Anything crossing a language boundary
  is an explicit `.scn` `operation` + `binding`.

---

## 2. Target Architecture

```text
                       .scn + .scenery.json
                                │
                                ▼
             Scenery Compiler / CLI / Worktree Supervisor (Go)
             ├─ canonical IR graph & revisioned manifests
             ├─ contract generators (Go, Rust, TypeScript client)
             └─ language & frontend adapters (Go, Rust, Vite)
                                │
                                ▼
       ┌────────────────────────────────────────────────────────┐
       │        Scenery Host (precompiled, manifest-driven)     │
       │  • Same-origin router: /api/* → backend, / → frontend  │
       │  • Vite dev proxy (HMR WS) / prod static asset server  │
       │  • Contract JSON decode/encode & constraint validation │
       │  • Auth, sessions, OIDC, connectionvault, policy eval  │
       │  • Durable engine, schedules, retries, leases, events  │
       │  • Generation pinning, attestation, drain, MCP, OTLP   │
       └──────────────┬─────────────────────────┬───────────────┘
                      │    Worker ABI v1 (h2c   │
                      │    over Unix socket)    │
             ┌────────▼────────┐       ┌────────▼────────┐
             │   Go Service    │       │  Rust Service   │
             │  (thin Go SDK)  │       │ (thin Rust SDK) │
             └────────┬────────┘       └────────┬────────┘
                      │                         │
             native DB (pgx/sqlc)      native DB (sqlx)
```

### Why Manifest-Driven Host Matters

Today, `runtime/process_host.go` routes and pins generations, while HTTP decode,
auth, policy, and durable execution live inside generated Go service binaries.
To support polyglot services **and** pure Rust + Vite/React apps without
requiring `go build`:

- The precompiled `scenery` executable includes the **manifest-driven Host**,
  which executes schema-directed JSON codecs (`internal/contract`), policy
  evaluation (`internal/contractpolicy`), auth, durable leasing
  (`runtime/durable.go`), and static asset serving directly from the compiled
  runtime manifest.
- Go services still compile through `go build` only when the app contains Go
  modules.

### Database vs. Storage Boundary

- **SQL databases stay native to each service language.** The Host injects the
  resolved connection configuration; Go uses `pgx`/`sqlc`, Rust uses `sqlx`.
  Queries never proxy through the Host, and **one SQL transaction belongs to one
  service**. Cross-language atomicity uses idempotent operations or durable
  jobs, never distributed SQL transactions.
- **Object storage stays unified.** `scenery.sh/storage` remains the single
  canonical implementation, exposed to workers over the Host's streamed
  Capability API so tenant scoping, sidecar metadata, and conditional writes
  cannot drift across languages.

---

## 3. Contract, Cross-Language Calls & Worker ABI

### 3.1 Early Spec & Framework Changes

1. **Move `service.runtime` to implementation identity.** In
   `internal/spec/schemas.go`, `scenery.service` currently places `runtime` in
   the `"contract"` revision domain. Reclassify `runtime` under
   `"implementation"` so migrating a service from Go to Rust preserves
   `ContractRevision` and keeps callers unaffected.
2. **Decouple framework pinning from `go.mod`.** `ReadDesiredFramework`
   (`internal/build/framework_session.go`) currently requires `go.mod` with a
   `scenery.sh` pin. Move platform version pinning into Scenery's own
   config/lock model and distribute prebuilt `scenery` binaries so Go is only
   needed when an app declares Go services.
3. **Prune dead `.scn` surface before multiplying generators.** Resolve open
   Plan 0225 items in `docs/tech-debt.md` before duplicating features across
   Go and Rust—**explicitly keeping `service.client`**, which is the primitive
   for cross-language service calls.
4. **Generalize toolchain/target/contract declarations.** Keep `go_module`,
   `go_toolchain`, `go_target`, and `go_contract` for Go modules, and introduce
   minimal symmetric Rust blocks (`rust_workspace`, `rust_toolchain`,
   `rust_target`, `rust_contract`). All domain resources (`record`, `enum`,
   `operation`, `execution`, `binding`, `http_gateway`, `data_source`,
   `authentication`, `module`) remain language-neutral.

### 3.2 Cross-Language Calls (`Go ↔ Rust`)

Today, a Go caller imports the callee's generated Go package directly (e.g.
`echo/scenerycontract`). When the callee is implemented in Rust, no Go package
exists for that module.

- **Caller-side client projection:** For every declared
  `client "<name>" { binding = ... }`, Scenery generates the typed client,
  input/outcome types, and declared error types **into the caller module's own
  `scenerycontract`** directly from the canonical `.scn` graph.
- **Runtime call flow:** Caller invokes its generated typed client → thin SDK
  calls Host capability `binding/invoke` with canonical JSON → Host validates
  policy and pins the active generation → Host invokes the target service worker
  (Go or Rust) → typed outcome or declared error returns to the caller.
- **Delivery guarantees:** Preserve `process_link.go` semantics distinguishing
  `not_sent` (dial failure before transmission) from `unknown` (worker crash or
  disconnect after send). Never blindly retry `unknown` calls outside declared
  idempotency and retry policy.

### 3.3 Worker ABI v1 (`h2c` over Unix Domain Socket)

Build directly on the semantics already proven in `runtime/process_link.go` and
`runtime/process_host.go`, upgrading the private local transport to **HTTP/2
cleartext (`h2c`) over UDS** with canonical JSON payloads. This avoids adding
Protobuf/gRPC as a second schema system while giving multiplexing, streaming
with backpressure, and per-stream cancellation (`RST_STREAM`) natively in both
Go and Rust.

#### Host → Worker (Invocation & Lifecycle)

| Surface | Contract |
|---|---|
| Identity | Service address, operation/binding address, `invocation_id`, generation number, `contract_revision`, worker attestation headers |
| Context | Principal, tenant ID, trace ID / `traceparent`, deadline, locale, declared invocation metadata |
| Execution | Direct HTTP/internal invocation and durable attempt invocation (`input` → declared `outcome` \| declared domain error \| implementation failure) |
| Streaming | Chunked frames, bounded buffers, backpressure, client disconnect propagation |
| Cancellation | HTTP/2 stream cancel + deadline propagation; explicit contract for handlers that finish after cancel |
| Lifecycle | Startup handshake (capabilities + revision attestation), readiness, graceful drain of in-flight calls, shutdown |

#### Worker → Host (Capability API)

| Capability | Purpose |
|---|---|
| `binding/invoke` | Typed internal calls to any service in the pinned generation (Go or Rust) |
| `storage/*` | Streamed object read/write/delete/list/conditional-write (binary HTTP bodies, never base64 JSON) |
| `durable/enqueue` & `events/emit` | Enqueue durable executions and publish declared events through the Host |
| `connections/token` | Fetch owner-bound OAuth/connection tokens from `connectionvault` |
| `config/snapshot` | Read revision-bound deployment config and secrets |
| `telemetry` | Batched, asynchronous child spans and structured logs correlated to `invocation_id` / `trace_id` |

### 3.4 Type Fidelity & Conformance Corpus

Before expanding to additional languages, maintain one shared golden conformance
corpus exercised identically against Go, Rust, and the generated TypeScript
client:

- `int64` / `uint64` (exact precision across Go, Rust, and JS `bigint`),
- exact `decimal`, `timestamp`, `duration`, and `bytes`,
- `optional(nullable(T))` distinguishing absent fields from explicit `null`
  (modeled explicitly in Rust, e.g. `Patch<T>` / `Option<Option<T>>`),
- `enum` and tagged `result`/outcome variants + strict unknown-field handling,
- identical canonical serialization for idempotency keys across Go and Rust,
- strict separation of declared domain errors from panics/unhandled errors.

---

## 4. Rust Service Adapter & Developer Loop

### Generated Contract Crate (`<module>/scenerycontract/`)

For each Rust module, `scenery generate` materializes a local path crate under
`managed_generated_roots`:

- Strongly-typed structs and enums with canonical wire serde codecs.
- A generated service trait (e.g. `PricingService`) with exact method signatures
  for every declared `operation` and constructor input struct carrying declared
  dependencies (DB config, typed clients for `service.client`, storage handle).
- Generated typed client structs for every declared `service.client`.
- A tiny generated binary harness (built in the private build workspace) that
  wires the user's `impl PricingService` into the thin `scenery-sdk` Rust crate.

### ABI Verification & Incremental Builds

- **ABI checking uses `rustc` / `cargo check`.** Because generated registration
  statically binds the user implementation to the generated trait and
  constructor, the Rust compiler itself proves handler ABI conformance—Scenery
  does not write or maintain a Rust typechecker.
- **Fast isolated rebuilds:**
  - Each Rust service compiles as an independent binary sharing a persistent
    `CARGO_TARGET_DIR` (preserving Cargo incremental compilation and dependency
    artifacts across edits and worktrees when inputs match).
  - Keep `scenery-sdk` minimal (small `hyper`/HTTP2 + `serde` + UDS footprint)
    so incremental links stay fast.
  - Editing a Rust module rebuilds and swaps **only** that Rust service process
    while Go services, the Host, and Vite keep running untouched.
  - Editing a Go module rebuilds and swaps **only** that Go service process.

### Language Adapter Interface (Internal to Go Core)

Start with built-in adapters in Go (no premature public plugin API) behind one
small internal interface so TypeScript and Python can follow the exact same seam
later:

1. `Inputs(module)` — exact source, manifest, lockfile, and toolchain inputs for
   fingerprinting and watch invalidation.
2. `Generate(ir, module)` — render `<module>/scenerycontract/` and private
   worker bootstrap files.
3. `Check(ir, module)` — run native verification (`go/packages` + ABI checks for
   Go; `cargo check` for Rust) and map diagnostics cleanly.
4. `Build(ir, module)` — build the service executable and return its artifact
   descriptor.
5. `LaunchSpec(artifact)` — command, args, env, and readiness contract for the
   supervisor.

---

## 5. Vite / React (Static) Frontend Serving

Support Vite + React as a first-class **static** frontend surface in both
development and production, without requiring Node.js in production.

### Configuration & Adapter

Formalize an explicit frontend adapter in `.scenery.json` (auto-detected or
declared, starting with `vite` in `static` mode; designed so `ssr` upstreams
such as Next.js can plug into the same router later):

- **Dev (`serve: "development"`):** `scenery up` runs `vite --host 127.0.0.1
  --port <port> [--base <basePath>]` behind the same-origin local router/edge
  with clean WebSocket upgrade proxying for Vite HMR.
- **Local production preview (`serve: "production"`):** builds `dist/` and
  serves it through the exact same static handler used in production.
- **Same-origin API & config:** Frontends call relative `/api/...` using the
  generated TypeScript client (which seamlessly targets both Go and Rust
  services) and read runtime public settings via `loadPublicConfig()` rather
  than baking environment URLs at build time. Session cookies (`HttpOnly`,
  `SameSite=Lax`) work identically in dev and production without CORS hacks.

### Production Static Serving in the Scenery Host

`scenery build` executes the Vite production build (`vite build`), verifies
output hygiene (rejecting dotfiles outside `/.well-known/*`), and packs `dist/`
into the production application bundle. The Scenery Host serves it directly:

- Content-hashed assets (`/assets/*`): `Cache-Control: public, max-age=31536000,
  immutable`, `ETag`, and gzip/brotli support.
- HTML entry (`index.html`): `Cache-Control: no-cache`.
- **API-safe SPA fallback:** Unmatched paths outside declared `http_gateway`,
  auth, and internal prefixes fall back to `index.html`. Unknown paths under
  `/api/*` (or any gateway prefix) always return a neutral JSON `404`, never
  HTML.
- **Public HTTP neutrality:** Static responses, redirects, headers, and error
  pages never expose `"scenery"`.

---

## 6. Unified Production Artifact

`scenery build` produces one self-contained deployable bundle (and container
entrypoint) that runs without a source checkout, without `scenery up`, and
without Node.js or Go/Rust toolchains installed:

```text
dist/bundle/
├── manifest.json            # compiled runtime manifest + contract/impl revisions
├── scenery-host             # precompiled manifest-driven Host + local supervisor
├── services/
│   ├── orders               # Go service executable
│   └── pricing              # Rust service executable
└── frontends/
    └── web/                 # built Vite/React dist/ static assets
```

- On startup, `scenery-host` reads `manifest.json`, launches and health-checks
  the bundled service processes over private UDS sockets, serves `/frontends/web`
  at `/` (or configured mount) and `/api/*` through the Host pipeline, and
  handles graceful shutdown/drain on `SIGTERM`.
- Multi-machine distribution across separate hosts/pods is intentionally
  deferred; one self-contained bundle keeps deployment simple while using the
  exact same process boundary.

---

## 7. Milestones & Acceptance Criteria

### Milestone 0 — Contract & Surface Preparation

- Decide and prune unused `.scn` surface (`docs/tech-debt.md`, Plan 0225 items)
  while retaining `service.client`.
- Move `service.runtime` to the `"implementation"` revision domain in
  `internal/spec/schemas.go`.
- Decouple framework pinning from `go.mod` so apps without Go services can pin
  Scenery cleanly.

**Done when:** Existing Go fixtures and self-harness pass with `service.runtime`
excluded from `ContractRevision` and framework selection working independently
of `go.mod`.

### Milestone 1 — Manifest-Driven Host, Caller-Side Clients & Worker ABI v1

- Specify Worker ABI v1 (`h2c` over UDS) for both `Host → Worker` invocation and
  `Worker → Host` capabilities (`binding/invoke`, streamed `storage`,
  `durable/enqueue`, `connections/token`, `config/snapshot`, batched telemetry).
- Implement manifest-driven execution in the Host for foreign services (HTTP
  decode/encode, validation, auth, policy, durable orchestration) without
  generated Go glue.
- Switch `service.client` generation to emit typed clients into the **caller
  module's** own generated contract package.
- Create the shared wire/type conformance corpus.

**Done when:** Existing Go multiservice apps pass with caller-side generated
clients and the Worker ABI v1 spec + conformance fixtures are checked in and
green.

### Milestone 2 — Rust Service Adapter & Go ↔ Rust End-to-End

- Add Rust target/toolchain/contract declarations in `.scn`, the Rust contract
  crate generator (`<module>/scenerycontract/`), the thin `scenery-sdk` crate,
  and the Cargo check/build/launch adapter.
- Build an end-to-end `testdata` fixture combining a Go module and a Rust module
  (plus a pure Rust fixture with no `go.mod`).

**Acceptance criteria:**
- HTTP endpoint hits a Rust handler, which calls a Go service via
  `service.client` (and a Go handler calls a Rust service), preserving
  principal, tenant, trace, deadline, and typed domain errors.
- A durable execution runs in a Rust service with leasing, retries, and
  idempotency managed by the Host.
- Editing the Rust module rebuilds and swaps only the Rust process (Go PID and
  Host PID stay unchanged); editing the Go module swaps only Go.
- A pure Rust app (no `go.mod`, no Go toolchain on `PATH`) passes `scenery
  check`, `scenery up`, and `scenery build`.

### Milestone 3 — Vite / React Static Frontend (Dev & Production Bundle)

- Implement the explicit Vite static adapter for `scenery up` (HMR WebSocket
  passthrough + `serve: "production"` parity) and `scenery build` (bundle
  packaging).
- Integrate static asset serving with immutable caching, API-safe SPA fallback,
  and public HTTP neutrality into the manifest-driven Host.
- Extend the Go + Rust fixture with a Vite + React frontend using the generated
  TypeScript client.

**Acceptance criteria:**
- In `scenery up`, Vite serves at `/` with live HMR while calling Go and Rust
  endpoints at `/api/...` with same-origin session cookies.
- Editing React source updates via HMR without restarting Go or Rust services.
- The standalone production bundle runs outside the checkout with **no Node.js
  installed**, serves `/` and SPA deep links as HTML, returns neutral JSON 404
  for unknown `/api/...` routes, and passes end-to-end Go + Rust + React browser
  checks.

### Milestone 4 — Failure-Mode Proof, Measurements & Next Decisions

- Verify fault scenarios across Go and Rust workers: crash mid-call (`not_sent`
  vs `unknown`), client cancellation propagation, generation drain under load,
  contract revision mismatch rejection, and worker crash mid-durable attempt.
- Measure representative latency, memory, and edit-to-ready numbers (short op,
  DB op, Go ↔ Rust chain, durable job, stream, static asset serve).
- Decide on explicit data:
  1. Adding TypeScript and Python backend adapters over the proven Worker ABI.
  2. Whether any measured Host bottleneck justifies moving a narrow runtime
     kernel to Rust behind the same contract.

---

## 8. Explicit Non-Goals (For This Phase)

- Rewriting the Scenery compiler, CLI, or worktree manager in Rust.
- TypeScript or Python backend services before Go + Rust validates the boundary.
- Server-rendered (SSR) frontend runtimes such as Next.js SSR (static Vite/React
  ships first; SSR upstream processes layer cleanly onto the same Host router
  afterward).
- Distributed multi-host service orchestration or cross-service SQL
  transactions.
