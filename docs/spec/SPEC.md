# Scenery Specification (Compact Reference)

Authoritative machine schemas live in `internal/spec`, `scenery schema <kind> -o json`,
and `docs/schemas/*.schema.json`. This document summarizes the normative `.scn`
language and runtime contract.

## 1. Files & Graph Model

- `.scenery.json`: app root marker and independent runtime configuration (`envs`,
  `frontends`, `build`, `watch`, `validation`). `framework_version` can select
  an exact prebuilt framework version without a `go.mod`.
- `app.scn`: root application declarations (`workspace`, `go_module`,
  `go_toolchain`, `go_target`, `rust_workspace`, `rust_toolchain`,
  `rust_target`, `application`, `http_gateway`, `provider`,
  `data_source`, `execution_engine`, `authentication`, `module`,
  `typescript_client`, `mcp_server`, `assistant`).
- `<module>/package.scn`: package-local declarations (`package`, `input`,
  `export`, `service`, `record`, `enum`, `union`, `operation`, `execution`,
  `binding`, `schedule`, `page`, `renderer`, `authorization`, `pipeline`).
- `app.lock.scn`: locked external provider/module integrity digests.

`.scn` uses a deterministic HCL-like syntax compiled into a typed,
content-revisioned graph (source, effective, and expanded views). Source files
must stay inside the workspace with no symlinks. Go comments and `init()`
register nothing.

Every application runs on the Rust application host (`scenery-host`) over
Worker ABI v1, whatever languages its services use. Go, Rust, JavaScript, and
TypeScript services are workers of one host bundle.

## 2. Core Kernel

```text
operation + binding + execution + policy
```

- **operation**: logical capability bound to a `service`, with typed `input`,
  named `result` variants (`outcomes`), optional `error` variants, optional
  `idempotency { mode = "keyed"; key = [...] }`, and `handler { method = ... }`.
- **binding**: exposes an operation via `protocol = "http"` (on an
  `http_gateway`) or `protocol = "internal"` (`visibility = "application" |
  "package"`). Direct execution supports `call` and HTTP `stream`. Durable
  execution supports `enqueue` and `wait`.
- **execution**: `mode = "direct"` (timeout) or `mode = "durable"` (`engine`,
  `external_name`, `revision`, `timeout`, `lease`, `attempts`, `retry`,
  `retention`, `deduplication`). Changing persisted input while keeping
  `external_name` requires bumping `revision`.
- **policy**: explicit `authentication`, `authorization` (rules evaluated by
  `internal/contractpolicy`), and `pipeline` middleware ordering.

## 3. Types & Canonical Wire Format

Scalar types: `string`, `bool`, `int` / `int32` / `int64`, `uint32` / `uint64`,
`float32` / `float64`, `decimal`, `bytes`, `uuid`, `date`, `datetime`,
`duration`, `size`, `url`, `relative_path`, `json`. Deployment-phase package
inputs may also declare `host_path`.
Composites: `list(T)`, `set(T)`, `map(T)` (string keys), `tuple(T1, T2, ...)`,
`optional(T)`, `nullable(T)`, `record`, `enum`, `union`.

- `optional(nullable(T))` preserves the distinction between absent fields and
  explicit `null`.
- Unknown fields are rejected by default (`unknown_fields = "reject"`).
- Large integers (`int64`/`uint64`) and `decimal` preserve exact precision in
  JSON and TypeScript (`bigint` / exact string representations).
- Floats use the shortest text that reads back as the same `float32` or
  `float64`, in Go `strconv.FormatFloat(v, 'g', -1, bits)` form (`1`, `0.1`,
  `1e+06`, `1.5e-07`). NaN, infinities and negative zero are invalid.
- Canonical JSON serialization determines contract revisions and idempotency
  key hashes deterministically across runtimes.

## 4. Implementation ABI

- Generated package `<module>/scenerycontract` exports typed input structs,
  sealed outcome interfaces/structs, constructor input (dependencies, declared
  `service.client` handles, config), and internal client wrappers.
- Service constructor: `func New<Service>(context.Context, contract.<Service>ConstructorInput) (*Service, error)`.
- Operation handler: `func (*Service) <Method>(context.Context, contract.<Op>Input) (contract.<Op>Outcome, error)`.
- `internal/parse` verifies constructor and handler signatures against the
  compiled graph. Operation-to-operation calls across services go through
  generated `service.client` bindings so auth, policy, tracing, and generation
  pinning are preserved.
- A Rust module declares `rust_contract { crate = "..." }` and services with
  `runtime = "rust"`. The generated contract crate exports service traits,
  typed outcomes, and clients derived from the caller's declarations. The
  selected Cargo target checks the implementation and links it to the
  [Worker ABI v1 adapter](WORKER-ABI.md).
- A JavaScript or TypeScript service declares `runtime = "js"` or
  `runtime = "ts"` and package metadata `js_contract { entry = "service.ts" }`.
  The entry is a regular module-relative `.js`/`.mjs` or `.ts`/`.mts` file
  matching the service runtime. Its named export comes from
  `service.implementation.constructor`. Each worker service has its own
  module directory. The nearest covering `js_workspace { root = "..." }`
  selects its `js_target` with role `development` or `artifact`.
  The target references the workspace and a `js_toolchain` with an exact
  Bun release in `version`.
- Generation publishes `<module>/scenerycontract` beneath a declared
  `managed_generated_roots` entry. Its `index.ts` exports the constructor
  and handler interfaces, operation types, and scoped `service.client`
  clients. It uses the generated TypeScript client's exact codecs, including
  `bigint` for 64-bit integers, exact decimal strings, and optional,
  nullable properties. The constructor can open native database connections
  using deployment-supplied URLs keyed by declared dependency name.
- `scenery check` checks the TypeScript entry with `tsc --noEmit` and
  bundles each JavaScript or TypeScript module to verify syntax and imports
  without running application code. Development replaces only the worker
  whose module build inputs changed. The artifact target embeds the JavaScript
  and Bun runtime in a per-module executable in the Host's portable bundle.
  Bundle execution requires the build platform and architecture.
- A host bundle links each Go service from the `go_target` its build
  selects: the target named by `scenery build --target`, otherwise the target
  with role `artifact`, or `development` for `scenery up` and
  `scenery build --development`.
- `service.runtime` selects the implementation adapter and contributes to the
  implementation revision. Service declarations keep the same contract
  revision when only the implementation runtime changes.

## 5. HTTP Contract & Neutrality

- HTTP bindings map path parameters (including terminal path tails), query
  parameters, headers, cookies, and JSON bodies into typed `operation.input`,
  and map declared `result` / `error` variants to HTTP status codes, headers,
  cookies, and response bodies.
- End-user HTTP surfaces (URLs, headers, cookies, bodies, errors, generated
  browser clients/assets) MUST NOT contain `"scenery"` (case-insensitive).
- Errors use checked diagnostic codes: `SCN8xxx` for request/client failures;
  `SCN9xxx` for internal failures (sanitized message + opaque report token
  inspectable via `scenery inspect report <token> -o json`).

## 6. Generated TypeScript Clients & UI

- `typescript_client` targets emit typed ESM `fetch` clients matching declared
  `http_gateway` bindings, exact wire codecs, retry policies, and optional
  `dev-runtime.ts` (development RPC) and React page adapters (`@scenery/ui`
  Astryx + StyleX catalog).
- Generated outputs live only beneath `workspace.managed_generated_roots` and
  are published atomically via `internal/workspacetx`.
- A frontend can declare `adapter = "vite"` and `mode = "static"` in
  `.scenery.json`. `scenery up` proxies Vite with same-origin HMR and APIs.
  The environment's `serve = "production"` setting and a production worker
  bundle serve the built tree through the same static handler.
  An application with worker services serves only that root frontend, at its
  own origin: `scenery up` rejects any other frontend the environment serves
  instead of starting without it.

## 7. Revisions & Machine Envelopes

- `workspace_revision`: exact authored workspace snapshot (excluding managed
  generated outputs).
- `contract_revision` / `service_contract_revision`: canonical hash of
  contract-domain resources.
- `implementation_revision`: binds build inputs, toolchain, and target context.
- CLI JSON responses use strict `scenery.cli` / `scenery.cli.event` envelopes
  (`docs/schemas/scenery.cli.schema.json`) carrying exact `spec_revision`,
  `schema_revision`, `producer`, and structured `diagnostics`.

## 8. Production Host

`scenery build` writes a bundle that starts as
`<bundle>/scenery-host --listen <host:port> [--config <file>]`. `--listen`
falls back to `--port <port>` (loopback), `SCENERY_LISTEN_ADDR`, and
`127.0.0.1:4000`; a container binds `0.0.0.0:4000`.

- **Configuration.** The runtime configuration snapshot
  (`kind = "scenery.config.snapshot"`, at most 4 MiB) comes from `--config`,
  `SCENERY_CONFIG_FILE`, or the inherited descriptor named by
  `SCENERY_CONFIG_SNAPSHOT_FD`, in that order; naming both a file and a
  descriptor is an error. Any value may be `{"$env": "NAME"}`: the Host
  substitutes the variable's text, or its bytes for an entry of `secrets`.
  An unreadable or malformed snapshot or an unset variable stops the Host
  before any worker starts.
- **Storage.** `SCENERY_STORAGE_CONFIG` stores have `kind` `local`, `proxy`,
  or `s3`. An `s3` store names `region` and `bucket`, optionally `endpoint`
  (an http or https origin; default `https://s3.<region>.amazonaws.com`),
  `prefix`, `path_style` (default true with an `endpoint`), and
  `access_key_id`, `secret_access_key`, `session_token`, which fall back to
  the standard `AWS_*` variables and accept `{"$env": "NAME"}`. Requests are
  signed with AWS Signature Version 4. Objects live under
  `<prefix><store>/shared/` or, tenant-scoped,
  `<prefix><store>/tenants/<base64url(tenant)>/`. Writes upload a signed
  payload digest and honor `If-Match` and `If-None-Match: *`; deletes check
  preconditions against the current version first; user metadata travels as
  one `x-amz-meta-metadata` value (the backend's metadata limit applies).
  Listing entries omit `content_type` and `sha256`; a read returns both.
- **Telemetry.** A development Host exports to its worktree's loopback
  Victoria. A production Host exports OTLP/HTTP protobuf to
  `OTEL_EXPORTER_OTLP_ENDPOINT` with `/v1/traces`, `/v1/logs`, and
  `/v1/metrics` appended, or to the per-signal
  `OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT` as given, sending the URL-encoded
  `OTEL_EXPORTER_OTLP_HEADERS`. URLs must be http or https without
  credentials. Delivery is queued and drops records when the queue is full;
  stopping flushes the queue within 5 seconds.
- **Schedules.** Slots are the UTC instants a schedule's `every` divides.
  With `DATABASE_URL`, replicas claim each slot in
  `scenery.schedule_slots` and run it once between them; `skip`, `replace`,
  and `queue` runs hold a 30-second lease renewed every 10 seconds, so a
  crashed replica's run expires instead of blocking its schedule, a stopping
  Host releases its leases, and `replace` ends the leased run it supersedes. Claims older than a day are
  pruned. Without a database the Host schedules alone, in memory, and an
  unavailable database skips a slot rather than risk running it twice.
