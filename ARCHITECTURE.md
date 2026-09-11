# scenery Architecture

This document is a stable map of the scenery repository. It should help a new
contributor answer two questions quickly: where does a change belong, and which
boundaries should the change preserve?

Keep this file short and architectural. It names important packages, types, and
invariants, but intentionally avoids file-by-file detail. Use symbol search for
the names mentioned here.

## Bird's Eye View

scenery is a Go-native local runtime and toolchain for current Scenery applications
that declare their canonical resource graph in `app.scn` and package-local
`package.scn` files. `.scenery.json` carries independent runtime config.

At a high level, scenery does four things:

- discovers an app root and compiles `.scn` source into a typed resource graph
- generates a transient build workspace and synthetic runtime entrypoint
- runs one local HTTP server for the app's public, auth, and internal surfaces
- exposes local development, inspection, harness, and dashboard tools around
  that server

The central flow is:

```text
.scenery.json + app.scn + package.scn + app.lock.scn + Go implementations
        |
        v
internal/app + internal/scn + internal/spec + compiler
        |
        v
canonical source/effective/expanded manifests
        |
        v
generated contracts/composition/library facades + internal/build
        |
        v
generated workspace + scenery.sh/runtime/host
        |
        v
single local server + dev/inspect/harness tooling
```

Declared assistants add one provider-neutral path to that flow: ordinary MCP
bindings and MCP servers are compiled into a capability manifest, generated
conversation routes and browser clients are registered in the app runtime, and
each assistant implementation runs in a supervised child behind private
control and loopback MCP listeners. The public app never depends on the child
adapter's package or event vocabulary.

Architecture invariant: the public scenery surface is scenery-named. User apps
should depend on `scenery.sh/...` packages and current `.scn` resources,
without alternate declaration frontends or compatibility syntax.

Architecture invariant: app semantics live in the canonical current graph
before code generation or runtime wiring. Avoid rediscovering graph facts from
Go source or generated artifacts.

## Code Map

### `cmd/scenery`

This is the CLI entrypoint and orchestration layer. `main`, `run`, and the
command-specific functions parse flags and connect internal packages into user
commands such as `up`, `worker`, `build`, `check`, `inspect`,
`harness`, `logs`, `console`, `db`, `task`, and `generate`.

`scenery up` starts the local
app session around the app runtime: dashboard, agent routing, live rebuild behavior,
logs, traces, metrics, managed dev services, optional frontend routing, and
process supervision.

The same supervisor owns managed assistant children. `scenery assistant init`,
`assistant sync`, and `assistant status` are provider-neutral CLI surfaces;
`inspect assistants --implementation` is the explicit developer/operator view
for implementation paths and private process descriptors.

Architecture invariant: non-CLI packages must not import `cmd/scenery`. Shared
logic belongs in `internal/` or a public package, depending on whether user apps
need it.

Architecture invariant: the CLI stays hand-rolled unless a new dependency has a
clear payoff. The command grammar is part of scenery's local contract and should
remain easy to audit.

`scenery inspect docs --for-path` combines the indexed knowledge base with the
same changed-area relevance and verification-command logic used by self-harness,
then narrows Markdown results to owning sections. It does not maintain a second
path-routing catalog.

`internal/repoinfo` owns the read-only knowledge-index types and reader,
repository discovery, and the single pure path-classification table. CLI docs
rendering stays here; neither classification nor artifact reading executes tests.

### Shared verification data and child-process boundaries

`internal/schemacheck` owns the existing schema-validation vocabulary shared by
repository verification and product contract tests. It is not a product runtime
dependency and adds no independent test binary.

`internal/harnessreport` contains only current report values and pure bounded
summary formatting. `internal/harnessevidence` owns explicitly requested artifact
writes and output/reproduction formatting. Neither runs repository checks.
The one payload schema identity registry lives in `internal/machine`.

`internal/devprocess` owns the existing concrete child-process start, readiness,
output capture and process-tree stop mechanics. The CLI still owns application,
session, frontend, desktop and assistant lifecycle decisions. Process completion
is exposed as a receive-only signal, not a second mutable runtime record.
Process observations and named substrate locks live in that same concrete
process layer. `internal/agent` owns session process-cleanup selection, retaining
the existing owner and orphan-scope checks. `internal/watchignore` owns the
shared watch-input path policy and embed-pattern parsing; snapshot caching
remains in the CLI.

`scripts/verify` is the repository-only orchestration owner. Run it from this
root with `go run ./scripts/verify`. It uses the prepared absolute worktree-local
product executable for product assertions and the existing fresh-test engine
for repository tests. The application CLI neither dispatches repository
verification nor depends on its execution engine. Its app/UI harnesses and
bounded report readers remain product capabilities.

### `internal/app`

`internal/app` owns repository and app-root discovery. It walks upward to find
`.scenery.json`, decodes app config, and provides repo-root helpers for self-harness
work.

Architecture invariant: `.scenery.json` is the app root marker for scenery apps. App
loading should fail clearly when the marker is missing or invalid.

Architecture invariant: configuration parsing must not import the PostgreSQL
driver layer. Pure database, schema, and environment name derivation lives in
`internal/postgresname`; database IO lives in `internal/postgresdb`.

SQL requirements are not config declarations. `compiler.Result.SQLRequirements`
is the immutable projection of registered services' typed dependencies and
framework auth/durable registrations. The compiler owns service selection,
dependency binding and schema validation once; generation, inspection, runtime
supply and setup consume that result. Environment endpoints supply it; persisted
worktree records own actual allocations. Snapshot recovery reads actual schemas
without compiling desired source. Generated entrypoints configure the existing
SQL environment before constructors; `db` never rediscovers app configuration.

`internal/postgresdb.ResolveServiceEndpoint` owns the pure override-versus-base
URL choice shared by generated runtime bindings and named standalone `db`
calls. Consumers retain their distinct registry precedence, validation order,
framework handling and managed provenance; the selector has no environment,
configuration, database IO or allocation authority.

`internal/postgresdb` also owns transactional numbered schema migrations,
schema-local checksum/owner ledgers and explicit initial-baseline verification.
The CLI resolves app-authored files and verified stopped worktree authority;
this package executes SQL and ledger publication in one transaction. It does
not infer migrations, reset populated schemas or adopt unknown schema shapes.

### `internal/desktop`

`internal/desktop` owns the Tauri-specific project contract: resolving a
configured desktop project, producing exact Tauri 2 dev/build overlays and
commands, running those commands, and discovering installer bundles. The CLI
package supplies frontend builds, session process registration, and output
rendering around that package.

Architecture invariant: Tauri project and command behavior stays independent
of the `cmd/scenery` dev supervisor. Agent/session lifecycle remains CLI
orchestration and does not leak into the desktop integration package.

### `internal/gotarget`

`internal/gotarget` is the tiny shared Go target-context value and hermetic
environment used by both compiler and parse. Field identity is part of
implementation revision.

### `internal/parse`

`internal/parse` is the narrow Go package-analysis boundary used by current
constructor and handler ABI verification. It loads syntax, types, and package
metadata with `go/packages`; it does not discover application declarations.

Architecture invariant: `golang.org/x/tools/go/packages` is owned by this
loader boundary. Downstream model consumers receive only model-owned analysis
data, not the loader package itself. Compiler tests must not import parse.

### `internal/model`

`internal/model` owns only the Go analysis types passed from `internal/parse` to
Go ABI verification. Canonical application resources belong to the graph and
compiler layers, not this package.

### `internal/scn`, `internal/spec`, `internal/graph`, and `internal/machine`

`internal/scn` owns safe `.scn` discovery, parsing, lossless CSTs, positions, and
formatting. `internal/spec` owns the singular current schema and diagnostic
catalog with digest revisions. `internal/graph` owns canonical resources, graph
views, provenance, and general revision projections, including the
`mcp_connection`, `mcp_server`, and `assistant` resource families.
`internal/machine` owns the strict `scenery.cli` and `scenery.cli.event`
envelopes. Compiler, evolution, generation, deployment, and Go verification
build on these foundational boundaries; compiler packages remain independent
of MCP SDKs, Node, and provider adapters.

Architecture invariant: `app.scn` is required. There is no Go-comment,
package-init, or alternate application-model frontend. Generated roots are declared, confined,
transactional, and reproducible from the canonical graph.

### `internal/contract` and `internal/contractpolicy`

`internal/contract` owns the contract value types, their canonical JSON wire
form, schema-directed marshalling, constraint validation, composite keys, and
approval tokens. `internal/contractpolicy` owns the authorization expression
lexer, parser, and evaluator. Both depend only on `internal/spec`,
`internal/machine`, and `internal/runtimeapi`.

The root `scenery.sh` package is the app-facing spelling of the contract
surface: it re-exports `internal/contract` as type aliases and thin forwarders,
so generated app code keeps using `scenery.Duration`, `scenery.Registry`, and
`scenery.MarshalContractValue` unchanged. `scenery.sh/runtime/host` is the only
consumer of `internal/contractpolicy`'s evaluator.

Architecture invariant: compiler-side packages depend on `internal/contract` and
`internal/contractpolicy` directly, never on the root `scenery.sh` façade or on
`scenery.sh/runtime/host`. The façade's application values and process-local binding
live in `internal/runtimeapp`, without importing the runtime host. Source,
compiler, generator, and deployment packages must not link the runtime,
its HTTP stack, or the PostgreSQL driver. `contract_surface_test.go` pins the
façade to the leaf so the app-facing spelling cannot silently drift.

### `internal/runtimeapp`

This leaf owns application span completion, native byte-stream ownership, the
process-wide dotenv parse, and the small application binding for metadata,
current request, authentication context and span creation. `runtime/host` installs one complete binding
during initialization; duplicate owners fail. Callbacks execute in the same
native Go process, preserving contexts, pointer identity and request scope.
The binding does not serialize calls or provide a second runtime implementation.
Standalone library use retains empty request and no-op span behavior. The
public `scenery`, `db`, `durable` and `auth` packages no longer import the runtime host.
`internal/authbridge` supplies standard auth with native HTTP registration,
codec and lifecycle callbacks. Registrations made before host initialization
are retained until the single host binds; service initialization remains lazy.
Full native worker separation, including internal/durable dispatch, is not
promoted. [0181](docs/plans/0181-native-worker-measurement-audit.md) corrected
asymmetric validation in 0180's measurements and withdrew the architectural
rejection. [0182](docs/plans/0182-direct-native-worker-preparation.md) then
removed ordinary private preparation from the candidate and measured an 11.6%
gain in six matched API pairs. This does not establish the full-loop 50% goal
or qualify the worker for production.

### Native invocation and durable helpers

`internal/nativecall` owns internal binding registration, package visibility,
trusted invocation checks, codec dispatch and native callback execution. The
runtime supplies the existing authorization/pipeline and system-error owner.
Registry snapshots preserve composition rollback; locks are released before
callbacks, including nested calls. Native input/output pointers and arbitrary
context values stay in the application process.

`internal/nativedurable` owns durable service-name normalization, native run
values, signal dispatch and step replay/retry. The runtime binds the signal
store owner and supplies a request-local step store adapter. SQL handles and
step callbacks remain native; persisted result/error bytes keep their existing
format. Neither package imports the runtime host or a database driver. This
is dependency ownership, not a completed kernel/worker process boundary.

`internal/nativeservice` owns the constructor dependency graph, parallel ready
batches, failure propagation and reverse shutdown. `internal/nativecompose`
owns adapter ABI/resource admission and delayed transactional apply. The host
supplies rollback and final page validation. Admission verifies all resources
before claiming any, so a rejected adapter cannot poison a later retry.
These packages retain native callback values and do not import the host.

### `internal/compiler`

`internal/compiler` loads the current `.scn` workspace and produces immutable
source, effective, and expanded graph snapshots. Before any ordinary source
read, it uses `internal/workspacetx` to recover an abandoned source transaction
or reject a live owner; staged validation admits only that transaction's owner.

Architecture invariant: compiler depends on the foundational source/spec/graph
packages and `internal/gotarget`. It does not import `internal/parse`.
Evolution, generation, deployment, and runtime orchestration consume
compiler results and never sit below it.

### `internal/generate`

`internal/generate` renders Go contracts and composition, TypeScript clients,
OpenAPI projections, generated React page adapters, and the binary-owned
`@scenery/ui` catalog from one compiler result. `internal/generate/api` is the
stdlib-only leaf for `LibraryBuildSpec`,
`RuntimeIntegrationPlan`, and assistant-asset descriptor types;
packages that only need that surface must not import `internal/generate`. The
catalog materializes its root component barrel and the direct
`tokens.stylex.ts` defining module in the same artifact-set transaction. Its check path reports both diagnostics and
whether native implementation verification was requested and valid.

Generated React routes carry static or dynamic path contracts. A
`detail_page` adapter receives decoded router params, loads one typed record,
and feeds one shared content component into its routed-page and controlled-
dialog wrappers; generated related tables remain scoped to those params.

Declared Go libraries add a `scenerylib_<name>` facade with source/shared
backends and an `export/` c-shared shim inside the declared existing Go module.
Application source imports only the facade; these files remain generated
projections rather than declaration or edit surfaces.

Application-imported Go packages are ordinary in-module projections, ignored by
Git by default. One renderer supplies those packages and private cached
composition. `workspacetx` serializes publication and abandoned-write recovery;
descriptor/digest evidence authorizes exact replacement, never whole-directory
ownership. Compilation has no generation side effect and owns no `go.work`.

For assistants, generation also emits the provider-neutral conversation client,
public route registration, MCP capability projection, private runtime
descriptor, and (for a build target) the assistant asset registry. Provider
implementation overlays are generated projections and never declaration
surfaces.

Architecture invariant: render every selected output before one atomic commit;
verification is read-only and generated paths stay beneath declared managed
roots. Architecture invariant: `internal/generate/api` stays free of
compiler, parse, TypeScript verification, and generate itself.

### `internal/mcpcontract`, `internal/mcpprojection`, `internal/mcpgateway`, and `internal/mcpfederation`

These packages own the provider-neutral MCP ABI. `internal/mcpcontract` defines
the manifest, tool policy, assertions, and limits; `internal/mcpprojection`
projects the expanded graph and does not import the compiler; `internal/mcpgateway`
dispatches local generated bindings and federated tools; and
`internal/mcpfederation` owns Scenery's external Streamable HTTP clients,
namespaces, filters, auth, readiness, and refresh lifecycle. They do not expose
a public MCP listener and do not import the developer adapter.

### `internal/assistantapi`, `internal/assistantcontrol`, `internal/assistantruntime`, and `internal/assistantadapter`

`internal/assistantapi` owns the provider-neutral public conversation JSON and
NDJSON contracts, opaque handles, normalized errors, and streaming redaction.
`internal/assistantcontrol` owns the private versioned helper protocol, while
`internal/assistantruntime` owns helper lifecycle interfaces, typed requests,
revision handshakes, and supervision-facing states. Provider code is isolated
below `internal/assistantadapter/<provider>`; the current developer/operator
adapter is `internal/assistantadapter/eve` and is not imported by public API,
compiler, graph, or generic runtime packages.

### `internal/evolution`

`internal/evolution` owns semantic diffs, source mutation plans, approvals,
migration consequences, and revision-bound receipts. It shares workspace
transaction metadata/recovery with the compiler through `internal/workspacetx`.

Architecture invariant: evolution never defines another graph or transaction
reader. Production evolution does not import `internal/generate`; predicted-
artifact and implementation checks are injected by CLI and agent sessions.
Plans and receipts bind exact current revisions and reject stale or old
disposable shapes.

### `internal/deployplan`

`internal/deployplan` resolves deployment graphs into exact provider plans and
applies them with crash-safe progress and revision checks.

Architecture invariant: deployment planning consumes canonical compiler output
and provider contracts; it does not reinterpret source or own language schema.

### `internal/contractagent`

`internal/contractagent` exposes graph inspection and semantic-evolution
capabilities over JSON-RPC. It composes compiler, evolution, and schema queries
without owning their models.

Architecture invariant: advertised capabilities and schemas are exact current
contracts. The agent rejects unadvertised creation kinds and stale artifacts
instead of guessing or translating them.

### `internal/codegen`

`internal/codegen` writes the small runtime entrypoint and configuration glue
consumed by current builds. Resource-specific contracts and adapters are
generated from the canonical expanded graph.

Architecture invariant: generated code should be boring Go. Prefer explicit
wrappers and registration over runtime reflection when the parser already knows
the shape of the app.

Architecture invariant: operation-to-operation calls go through generated
binding clients when scenery semantics matter. Direct user function calls must
not bypass auth context, private access rules, tracing, or delivery semantics.

### `internal/devcache`

`internal/devcache` is the stdlib-plus-envpolicy leaf for the scenery
development cache root. Production honors `SCENERY_DEV_CACHE_DIR`; tests inject
`SetRoot`. Doctor, build, and CLI resolve the cache here instead of linking
through `internal/build`.

### `internal/build`

`internal/build` owns the transient app build workspace. It materializes the
current generated overlay, syncs source and generated files, tracks
build fingerprints, runs `go mod tidy` when needed, compiles the app binary, and
writes latest-build metadata. Generation is injected through `GenerateHooks`;
the production package does not import `internal/generate`.

Framework preparation snapshots the selected module's relevant source inputs,
builds its content-stamped CLI and binds both digests into existing build input
manifests. `cmd/scenery/framework.go` owns explicit preparation/inspection, not
automatic version changes. Cached runtime bundles must restore the same target,
build-input and implementation identity before reuse. The supervisor owns
candidate preflight and sequential stop/start/recovery; retained generation
executables have independent bytes, not links into an evictable build cache.

Preparation publishes current public Go before implementation analysis, including
cache reuse. Cached private bytes must match the current renderer. Authored
fingerprints exclude exact generated paths; implementation fingerprints include
the generated bytes actually consumed. Watch tracks missing generated files
separately so ignored output can be restored without a self-triggered rebuild.

When assistants are declared, it also archives the managed child runtime and
authored assistant capsule, writes verified content-addressed descriptors, and
adds provider-neutral assistant asset metadata to the runtime bundle. The Go
binary still launches the child out of process; Node/V8 is never linked into
the Go process.

Architecture invariant: build outputs are disposable and reproducible from the
app root, config, source, and generated model. Do not make the transient
workspace the source of truth.

Architecture invariant: build metadata should be machine-readable enough for
agents and humans to diagnose drift without scraping terminal output.

### `internal/librarybuild` and `scenery.sh/library`

`internal/librarybuild` turns a verified declared-library export shim into the
exact darwin/arm64 and linux/amd64 artifact matrix. It builds Darwin natively,
builds Linux in the pinned oldest-supported container, hashes each artifact,
and writes the current portable manifest.

The public `scenery.sh/library` package strictly decodes that manifest, selects
the host artifact, verifies its digest and ABI/version symbols, binds operation
symbols with `RTLD_NOW|RTLD_LOCAL`, and atomically routes new calls to a swapped
version. Loaded Go runtimes remain resident forever; `dlclose` is forbidden.

Architecture invariant: shared linkage substitutes only a declared,
record-shaped operation contract. It never exposes an arbitrary Go package ABI
or bypasses the generated facade.

### `internal/testsuite`

`internal/testsuite` discovers the complete repository test graph through the
Go tool, caches linked test binaries by Go build ID, runs every test body fresh,
and emits Go-compatible JSON timing events for the self-harness.

Architecture invariant: the binary cache may avoid repeated linking but must
never reuse test results or narrow the `./...` package and test surface.

### `internal/edge`

`internal/edge` owns the managed Caddy lifecycle behind `scenery system edge`:
process start and stop, admin-socket reload, local-CA trust, and persistence of
the corresponding `internal/agent` edge state. It also owns Caddyfile and DNS
configuration, static frontend artifact publication, and the systemd edge unit.
`cmd/scenery` adapts CLI grammar, output, managed-tool resolution,
DNS/helper process orchestration, and privileged-listener setup. See
[the edge instructions](internal/edge/AGENTS.md) for lifecycle boundaries.

Architecture invariant: edge lifecycle code exposes a small concrete interface
and does not import the CLI. Platform-specific child-process behavior stays
inside the module so command tests do not need to duplicate process semantics.

### `scenery.sh/runtime` and `scenery.sh/runtime/host`

`runtime` is the lightweight native application-call facade: current request,
auth, spans, database tracing, internal dispatch and durable operations. Its
process-local bindings retain Go contexts, pointers and concrete errors.
It does not import the host. Generated entrypoints and application adapters
explicitly import `runtime/host`; that host is linked into generated app binaries. It registers generated
services, bindings, middleware, auth policies, durable executions, schedules,
events, data resources, and pages, then starts one local HTTP server.

Important runtime concerns include route matching, request decode/encode, auth
context, current request metadata, structured error responses, middleware,
observability reports, secrets, DB tracing, durable workers, schedules, and graceful shutdown.

Assistant routes are registered in this same runtime server. The runtime owns
the five public conversation endpoints, initiator ownership and sealed handles,
NDJSON normalization/redaction, approval and cancellation, and revision-bound
dispatch. Helper control and MCP listeners are private loopback services, so a
helper crash can degrade an assistant without exposing a second public server.

Architecture invariant: there is one local app server per generated app process.
`scenery up` may run extra development services around it, but app API execution
stays inside the generated app binary.

Architecture invariant: runtime request state must be scoped to the current
request or internal call. Public helpers such as `scenery.CurrentRequest()` and
`auth.UserID()` should not rely on global mutable app state that leaks across
requests.

### Public API Packages

The public packages at the module root are what user apps import:

- `scenery.sh` exposes `Meta`, `CurrentRequest`, and application-owned child
  spans through `StartSpan`
- `scenery.sh/auth` exposes request auth state helpers and the
  standard auth module surface (`AuthData`, token helpers, standard auth
  registration, and pluggable email delivery)
- `scenery.sh/errs` exposes coded errors and HTTP status mapping
- `scenery.sh/library` owns verified cgo-free loading, operation calls, and
  load-alongside swaps for generated shared-library facades
- `scenery.sh/storage` exposes the storage capability and owns the canonical
  local filesystem store used by app runtimes, CLI commands, and the managed
  storage proxy
- `scenery.sh/durable`, `scenery.sh/db`, `scenery.sh/datasource`,
  `scenery.sh/object`, and related small packages expose runtime capabilities

Architecture invariant: public packages are boundaries. Keep them small,
stable, and oriented around user-app concepts. Internal implementation can move;
public names and behavior are much harder to change.

Architecture invariant: public packages may delegate inward to runtime internals
when necessary, but they should not pull in CLI, dashboard, parser, build, or
codegen concerns.

Architecture invariant: local object, metadata-sidecar, fsync, range, list,
delete, and conditional-write behavior has one implementation in
`scenery.sh/storage`; do not recreate a parallel backend under `internal/`.

### `internal/inspect`

`internal/inspect` renders app, route, service, endpoint, build, path, trace,
metric, and docs information as stable JSON responses.

Architecture invariant: inspect outputs are contracts. If the shape
changes, update the corresponding schema and tests in the same change.

### `internal/uireport`

`internal/uireport` lexically scans hand-authored React frontend source for
Astryx and `@scenery/ui` markup adoption, StyleX token use, hardcoded design
values, and true inline styles. It also owns safe frontend collection,
generated/dependency exclusions, aggregate shares, and deterministic triage
ranking.

Architecture invariant: UI inspection is read-only and heuristic. Keep markup
and style axes independent, pin lexical rules with fixtures, and do not turn the
score into validation or check-time enforcement without a separate contract
decision.

### Worktree Runtime Ownership

`internal/agent/worktree_*` owns canonical-root identity, private state paths,
lifetime and operation locks, strict retained resource records, read-only
orphan discovery, and the embedded worktree control server. `cmd/scenery`
composes those boundaries in `worktree_runtime_owner.go`; its
`worktree_postgres*` resolver verifies Docker daemon/object/volume identity and
authenticated cluster identity before lifecycle actions. Pending credentials
and resource intent are durable before provisioning begins. Database CLI,
snapshot, down, and prune commands use that single resolver.

`internal/stateupgrade` owns explicit same-schema metadata transactions under
one private worktree root: revision binding, exact backups, durable publication
and interrupted-operation recovery. Agent/storage owners validate payloads and
hold existing stopped-owner, operation and maintenance locks. The
`worktree upgrade` CLI composes them; ordinary decoders stay strictly current.
It never changes roots, credentials, database data or immutable object payloads.

Architecture invariant: ordinary `up` owns one control plane and runtime per
canonical app root in the same supervisor process. It never ensures a shared
machine agent, adopts shared PostgreSQL resources, or replaces incompatible
live ownership. Retained data and credentials live outside the checkout; Git
removal does not authorize deletion. Explicit external databases retain their
declared sharing semantics; managed storage never shares across canonical roots.

`internal/storagefs` owns retained namespace/incarnation authority, stable
cross-process leases, immutable payloads and atomic complete references,
metadata-only bounded scans, conservative reclamation and stopped-owner
restore/purge records. `storage` is the app-facing logical API and transport
adapter; application authentication supplies tenant identity. `cmd/scenery`
composes retained worktree and storage ownership without adding a provisioner.
`internal/snapshotarchive` owns checksummed portable logical records and leased
disposable materialization; targets get independent clone-or-copy files, never
links into a shared blob pool. The one-shot legacy exporter lives only under
`scripts/`; it is not an alternative runtime decoder.

The machine agent and privileged helper remain explicit edge/deploy surfaces.
An explicitly configured domain can register an edge-only forwarding lease
to a compatible live worktree router, without moving runtime ownership or
writing its session manifest. Localhost serving requires no machine edge.

### `internal/devdash` and `internal/localproxy`

These packages support the local development platform around a running app.

`internal/devdash` stores dashboard-visible state and observability data. It
also owns the shared asset-name hashing used to compare a running dashboard
bundle with built assets; only the product embeds and serves those assets.
`internal/localproxy` owns the local proxy layer. Victoria sidecars are supervised
from `cmd/scenery` as worktree-owned optional companions, and native dashboard views
surface local logs, traces, and metrics. The dashboard server and UI embedding
are orchestrated from `cmd/scenery`. `dashboard_storage.go` resolves registered
app scopes for storage RPC; `dashboard_storage_http.go` streams file transfers
on the same dashboard listener under a pinned namespace maintenance lease.

Architecture invariant: development services should be optional around the app
runtime. They can improve local ergonomics, but the generated app binary must
remain runnable as a headless execution path.

### `ui`

`ui` is the editable Astryx + StyleX source for the binary-owned `@scenery/ui`
catalog materialized into React-enabled TypeScript clients. `apps/console` is
the separate Scenery dashboard frontend.

Architecture invariant: the catalog stays domain-neutral, keeps its runtime
libraries as peer dependencies, exports curated components from `index.ts`,
and exposes StyleX variables only from the direct `tokens.stylex.ts` defining
module. The catalog is router-agnostic; generated adapters own the route tree
and app shell, while consuming apps supply route extensions, state, data,
themes, and domain slots.

### `docs`, `PLANS.md`, and `PLAN.md`

`docs` contains local contracts, schemas, active plans, completed plans,
runbooks, and the agent-readable knowledge index. `PLANS.md` defines the execution-plan
format. `PLAN.md` is strategic roadmap material, not the place to track
step-by-step implementation progress.

Architecture invariant: substantial implementation plans live under
`docs/plans/` and are linked from `docs/plans/active.md` while active.

### `testdata`

`testdata` contains current native fixture apps and golden generated files.
It is the acceptance corpus for compiler, codegen, runtime, and CLI behavior.

Architecture invariant: fixture apps should speak scenery syntax directly. Use
historical reference material only as a corpus when porting behavior into
scenery-native tests.

## Cross-Cutting Concerns

### Dependencies

scenery prefers the Go standard library. Direct Go dependencies are allowlisted by
the self-harness with a concrete rationale. New dependencies should be rare and
should solve a specific maintenance, correctness, or interoperability problem.

Dependency-heavy concerns should stay near the edge that needs them. For
example, local proxy, package loading, dashboard storage, and websocket support
are boundary concerns; parser/model/runtime fundamentals should stay as small as
practical.

### Testing And Harnesses

Prefer tests at stable boundaries: `.scn` parsing and validation, canonical
graphs, generated code, CLI JSON contracts, runtime HTTP behavior, and fixture apps. Use helper
checks to keep tests data-driven and easy to update when internals move.

After repository changes, refresh `.scenery/harness/agent-context.json` and run
the exact changed-area command union and
[root validation policy](AGENTS.md#validation-matrix), including named probes
for changed external boundaries. Full release and resource measurement are
explicit workflows, not the ordinary iteration loop. Use
[Fresh Worktree Preflight](docs/agent-guide.md#fresh-worktree-preflight) to
prepare the local binary and dashboard embed.

### Generated Artifacts

Generated app files should be deterministic. Golden tests should make generated
shape changes explicit, and inspect schemas should describe JSON contracts that
agents and tools consume.

Generated workspaces, dashboard build artifacts, and harness snapshots are
outputs, not primary source. Keep source-of-truth logic in Go source, schemas,
fixtures, and docs.

### Observability

Local observability is part of the product surface. Runtime traces, logs,
metrics, dashboard state, and inspect commands should give enough evidence to
debug a local app without relying on external services.

`scenery up` uses supervised VictoriaMetrics, VictoriaLogs, and VictoriaTraces
sidecars for local observability when their managed binaries are available.
Dashboard session metadata and saved request state live in a small JSON store
under the worktree's private control root; the project does not carry an embedded SQL driver for
dashboard state. Runtime remains decoupled from Victoria server packages;
the stable boundary is HTTP/OTLP, not Go library imports.

### File Size And Placement

scenery favors code that can be found quickly. Keep related concepts adjacent in
the tree, split very large files before they become hard to review, and prefer a
flat package map over deeply nested internal hierarchies unless a boundary earns
the extra structure.

## Inspiration

This document follows the style suggested by matklad's `ARCHITECTURE.md` essay:
short overview, codemap, invariants, boundaries, and cross-cutting concerns. It
also borrows ideas from the linked rust-analyzer architecture document and the
same series' notes on testing, workspaces, and build-time discipline.

### Native worker feasibility experiment

`internal/build.PrepareNativeExperiment` selects public plus worker/kernel
projection before one materialization in an explicitly owned private workspace.
`internal/generate.PrepareNativeWorkerGoWorkspace` shares compiler/public
publication, module ownership and required verification patterns with ordinary
preparation. Both adapter renderers consume validated metadata; the worker never
renders ordinary adapter source to obtain it. Ordinary composition, adapters and
entrypoint are absent from the candidate. Declared target patterns are preserved;
only the internally forced ordinary entrypoint is omitted from its discovery.

`internal/build.CompileNativeExperiment` consumes that exact pending projection,
preserves full native verification and input discovery, and publishes only
private evidence after the checker/build/freshness join. It cannot add files to
an ordinary prepared workspace. Separate native preparation hints contain no
ordinary successful binary or graph identity; ordinary compile/prime entrypoints
reject the experimental result. Failed preparation or recapture preserves the
ordinary latest manifest, previous receipt and retained artifacts.

The framework kernel has a separate consumed-input identity projected from the
same complete graph and hashed bytes. The full graph is rediscovered after the
join; reuse verifies current inputs and exact retained executable bytes.
Independent kernel discovery is an explicit untimed audit check.
`internal/build.RetainBinary` is the shared executable-copy boundary.
`scripts/native-worker-experiment` explicitly drives the unchanged ordinary
baseline, a control with matched post-build input checks, or the direct worker.
These modes are not product commands. Traces record preparation, full build and
verifier branches, their joined interval and final recapture/retention; overlapping
Go-command sums are not the build branch's wall time.

`runtime/worker` owns an explicitly generated plan-0180 experiment, selected only
through `internal/generate.RenderNativeWorkerWorkspaceFiles`; ordinary app
commands do not select it. Its complete native operation/constructor registry
uses `internal/nativecompose` and `internal/nativeservice`. Shared SQL supply,
metadata and synchronous request scope live in `internal/nativesql`,
`internal/runtimeapp` and `internal/runtimescope` respectively.

`internal/nativeprotocol` defines the private unary wire values. The experimental
kernel uses the current host HTTP admission/auth/codec path and
`host.NativeWorkerClient`, with no application imports. Standard auth crosses via
an explicit identity snapshot and restores `*auth.AuthData` inside the worker.
Other auth types, stream transport and unconverted native internal/durable
bindings are not admitted. Native callback registration does not claim that
these unconverted framework behaviors are implemented. The current experiment
compiles one binding's declarative policy into its kernel; dynamic declaration
plans and ordinary runtime promotion remain later milestones.
