# scenery Architecture

Scenery is a local runtime, compiler, and agent harness for
applications declared in `app.scn` and package-local `package.scn` files, with
independent runtime config in `.scenery.json`.

```text
.scenery.json + app.scn + package.scn + app.lock.scn + Go, Rust, JS/TS, Python implementations
  → internal/app + internal/scn + internal/spec + internal/compiler
  → canonical source / effective / expanded manifests
  → internal/generate + internal/uigen + internal/build
  → generated worker entrypoints + Worker ABI adapters (scenery.sh/runtime)
  → one Rust application host bundle + CLI/inspect/harness/worktree tooling
```

Every application runs on the Rust `scenery-kernel`. It owns public ingress,
binding policy, standard auth, scoped worker capabilities, durable execution,
schedules, assistants, MCP, and worker supervision. It binds public ingress
after publishing an attested generation. In development `scenery up` keeps the
worktree platform (agent session, port lease, local router, edge domains,
Victoria, `/runtime` RPC, frontends, desktop shells, assistant helpers): it
builds the initial bundle, starts the Host as a supervised child on the
worktree's API backend, and restarts it only for changes the Host cannot apply
in place. The Host watches source inputs (the files no `.gitignore` excludes),
rebuilds affected workers, exports telemetry to the worktree's Victoria, and
invokes the CLI on demand for declaration and build configuration changes. A
lightweight `scenery-kernel --test` mode backs `scenery test --with-host`,
serving ephemeral local capabilities alongside `<50ms` PostgreSQL template
database clones (`CREATE DATABASE ... TEMPLATE ...`) without colliding with a
running `scenery up` session. `SCN9xxx` runtime failures record replayable
invocation metadata so `scenery replay <report-token>` can deterministically
re-invoke the failing call or emit an offline regression test (`--emit-test`).
A production bundle (`scenery build`) starts directly through `scenery-kernel`:
it reads its configuration snapshot from `--config` with `{"$env": ...}`
secret references, serves local or S3-compatible object stores, exports OTLP
to the collector the standard `OTEL_EXPORTER_OTLP_*` variables name, and,
with a database, claims schedule slots in PostgreSQL so that replicas run
each slot once. `scenery build --oci` packages the same bundle as a Linux
image: Go and Bun workers cross-compile on the build machine, and a Debian 12
builder stage compiles the Host, Rust workers, and Python workers.

Go, Rust, JavaScript/TypeScript, and Python workers exchange scoped requests
over [Worker ABI v1](docs/spec/WORKER-ABI.md). The Rust Host and SDK share
the `scenery-wire` crate. Generated Go adapters connect the envelope to
typed operation handlers and use native PostgreSQL connections. The JS/TS
adapter bundles each service with its Bun runtime and reuses the TypeScript
client wire codec. The Python adapter (`internal/pythonbuild`, `sdk/python`)
packages each service with `uv` into a deterministic zipapp carrying a
pure-stdlib HTTP/2 Worker ABI SDK and typed dataclass contracts. Vite can run
behind the development gateway or supply static assets to the production bundle.

## Hard Invariants

1. **Single declaration surface.** App semantics live in the canonical `.scn`
   graph before codegen or runtime wiring. Never rediscover app resources from
   Go comments, `init()`, or generated files.
2. **One rolling contract.** Remove superseded spellings without compatibility
   aliases, old decoders, or fallback paths.
3. **Neutral end-user HTTP surfaces.** URLs, headers, cookies, response bodies,
   errors, and generated browser code/assets must not contain `"scenery"`.
4. **Public SDK stays thin.** App-facing packages (`scenery.sh`, `auth`, `db`,
   `datasource`, `errs`, `object`, `storage`) talk to the runtime through
   `internal/appsdk.Host` and leaves, never linking CLI, compiler, parser, or
   runtime orchestration.
5. **Compiler stays pure.** `internal/compiler` depends only on foundational
   `scn`/`spec`/`graph`/`gotarget`/`workspacetx` leaves—never `internal/parse`,
   `internal/generate`, `runtime`, or PostgreSQL drivers.
6. **Atomic, confined generation.** `internal/generate` renders all selected
   outputs before one transactional commit (`internal/workspacetx`) beneath
   declared managed roots. `.scenery/gen/` is cache, not an API.
7. **Canonical worktree ownership.** `scenery up` owns one control plane and
   runtime per canonical app root. Retained PostgreSQL databases and storage
   live outside the checkout; Git removal never authorizes data deletion.
8. **Minimal dependencies.** Direct Go dependencies are allowlisted in
   `scripts/verify/harness_arch.go`. Hand-roll CLI grammar in `cmd/scenery`.

## Package Map

### CLI & Verification
- `cmd/scenery`: CLI entrypoint, command grammar, `scenery up` supervisor,
  frontend/desktop/Victoria/assistant lifecycle, development runtime RPC
  (no embedded dashboard UI; apps use generated `dev-runtime.ts`). Never
  imported by non-CLI packages.
- `scripts/verify`: repository-only verification harness (`go run ./scripts/verify`).
  Uses the worktree-local `.scenery/harness/bin/scenery` binary and
  `internal/testsuite`.
- `internal/repoinfo`, `internal/harnessreport`, `internal/harnessevidence`,
  `internal/schemacheck`: shared path classification, validation-command
  selection, report formatting, and JSON Schema validation.
- `internal/feature`: repository-local Git feature ledger, isolated integration
  candidates, and serialized `main` publication (`scenery feature land`).

### Parsing, Specification & Compilation
- `internal/app`, `internal/appconfig`: canonical app-root discovery
  (`.scenery.json`), config decoding, per-app environment store, and OS secret
  adapters. Never imports PostgreSQL drivers.
- `internal/scn`: `.scn` discovery (no symlinks), lossless CST parsing,
  positions, and canonical formatting.
- `internal/spec`: singular current schema and `SCNxxxx` diagnostic catalog
  with digest revisions.
- `internal/graph`: canonical resources, provenance, and contract/implementation
  revision projections.
- `internal/machine`: strict `scenery.cli` / `scenery.cli.event` envelopes and
  payload schema-revision registry.
- `internal/compiler`: loads `.scn`, recovers/checks `internal/workspacetx`,
  validates and expands immutable graph snapshots + `SQLRequirements`.
- `internal/parse`, `internal/model`, `internal/gotarget`: narrow Go
  package-analysis boundary (`golang.org/x/tools/go/packages`) for constructor
  and handler ABI verification.
- `internal/evolution`, `internal/contractagent`: `scenery provider lock`
  source transactions and `scenery schema` catalog answers.

### Contracts, Generation & Build
- `internal/contract`, `internal/contractpolicy`, `internal/appsdk`: canonical
  contract wire codecs, constraint validation, authorization policy evaluator,
  and process-local SDK host bridge.
- `internal/generate`, `internal/generate/api`, `internal/uigen`,
  `internal/clientprojection`, `internal/openapiprojection`, `internal/codegen`,
  `ui/`: Go contracts and composition, TypeScript API clients, brand-neutral
  OpenAPI 3.1.1 projection (`scenery openapi` / `scenery generate openapi`),
  optional React pages, and binary-owned `@scenery/ui` Astryx + StyleX catalog.
- `internal/lifecycle`: stdlib-only DAG planner for deterministic generation,
  SQLC publication, and build preparation.
- `internal/build`, `internal/workspacetx`, `internal/dirlisting`,
  `internal/filemeta`, `internal/devcache`: transient build workspace,
  content-addressed fingerprints, crash-safe atomic publication, and stock
  `go build` linking of per-service Go workers.

### Runtime, Capabilities & Local Platform
- `sdk/rust/scenery-kernel`: the only application host, in development and
  production (see above). Its production surface is `config` (arguments,
  snapshot, `$env` references), `s3_storage` (SigV4), `otlp`, and
  `schedule_slots` (PostgreSQL slot claims and leases).
- `runtime` (`scenery.sh/runtime`): the Go Worker ABI adapter linked into each
  generated Go worker: operation registration, worker-binding calls, request
  context and auth reads, SQL bindings, tracing, and telemetry to the Host.
- Public SDK packages: `scenery.sh`, `auth`, `db`, `datasource`, `errs`,
  `object`, `storage` (canonical local filesystem object store).
- `internal/connectionapi`: the provider-neutral user connection contract the
  Host implements.
- `internal/mcpcontract`, `internal/mcpprojection`, `internal/assistant*`:
  provider-neutral MCP ABI and supervised out-of-process assistant helpers.
- `internal/agent`, `internal/devprocess`, `internal/devdash`,
  `internal/localproxy`, `internal/edge`, `internal/desktop`,
  `internal/postgresdb`, `internal/postgresname`, `internal/storagefs`,
  `internal/snapshotarchive`, `internal/stateupgrade`: worktree ownership,
  process supervision, managed Caddy edge, Tauri 2 desktop integration,
  PostgreSQL migrations, leased object storage, and portable snapshots.
- `internal/inspect`, `internal/uireport`: machine-readable app/build/route
  inspection and read-only frontend UI adherence scanning.
- `cmd/scenery/build_oci.go`, `internal/rustbuild`, `internal/jsbuild`:
  Linux image contexts (`scenery build --oci`), the Host's and Rust workers'
  Cargo sources, and Bun compile targets.
- `testdata/`: fixture apps and golden generated outputs.
