# scenery Architecture

Scenery is a local runtime, compiler, and agent harness for
applications declared in `app.scn` and package-local `package.scn` files, with
independent runtime config in `.scenery.json`.

```text
.scenery.json + app.scn + package.scn + app.lock.scn + Go and Rust implementations
  → internal/app + internal/scn + internal/spec + internal/compiler
  → canonical source / effective / expanded manifests
  → internal/generate + internal/uigen + internal/build
  → generated workspace + scenery.sh/runtime (process host + per-service processes)
  → single local server + CLI/inspect/harness/worktree tooling
```

For worker bundles, the Host projects Go and Rust bindings from the compiled
manifest. Both worker languages exchange scoped requests with the Host over
[Worker ABI v1](docs/spec/WORKER-ABI.md). Generated Go adapters connect the
envelope to typed operation handlers and use native PostgreSQL connections.
Vite can run behind the development Host or supply static assets to the
production bundle.

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
  `internal/clientprojection`, `internal/codegen`, `ui/`: Go contracts and
  composition, TypeScript API clients, optional React pages, and binary-owned
  `@scenery/ui` Astryx + StyleX catalog.
- `internal/lifecycle`: stdlib-only DAG planner for deterministic generation,
  SQLC publication, and build preparation.
- `internal/build`, `internal/workspacetx`, `internal/dirlisting`,
  `internal/filemeta`, `internal/devcache`: transient build workspace,
  content-addressed fingerprints, crash-safe atomic publication, and stock
  `go build` per-service development linking + single-binary production builds.

### Runtime, Capabilities & Local Platform
- `runtime` (`scenery.sh/runtime`): linked into generated app binaries. In dev,
  runs a stable `ProcessHost` (`runtime/process_host.go`) that pins requests and
  internal calls (`runtime/process_link.go`) to published generations over
  private sockets, plus one process per native service. Owns HTTP routing,
  codecs, auth context, policies, durable workers, schedules, assistants,
  tracing, and graceful drain.
- Public SDK packages: `scenery.sh`, `auth`, `db`, `datasource`, `errs`,
  `object`, `storage` (canonical local filesystem object store).
- `internal/connectionapi`, `internal/connectionruntime`,
  `internal/connectionvault`, `internal/authprovider/*`, `internal/oidc`:
  provider-neutral encrypted OAuth2/OIDC user connections.
- `internal/mcpcontract`, `internal/mcpprojection`, `internal/mcpapi`,
  `internal/mcpgateway`, `internal/assistant*`: provider-neutral MCP ABI and
  supervised out-of-process assistant helpers.
- `internal/agent`, `internal/devprocess`, `internal/devdash`,
  `internal/localproxy`, `internal/edge`, `internal/desktop`,
  `internal/postgresdb`, `internal/postgresname`, `internal/storagefs`,
  `internal/snapshotarchive`, `internal/stateupgrade`: worktree ownership,
  process supervision, managed Caddy edge, Tauri 2 desktop integration,
  PostgreSQL migrations, leased object storage, and portable snapshots.
- `internal/inspect`, `internal/uireport`: machine-readable app/build/route
  inspection and read-only frontend UI adherence scanning.
- `testdata/`: fixture apps and golden generated outputs.
