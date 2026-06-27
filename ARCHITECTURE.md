# scenery Architecture

This document is a stable map of the scenery repository. It should help a new
contributor answer two questions quickly: where does a change belong, and which
boundaries should the change preserve?

Keep this file short and architectural. It names important packages, types, and
invariants, but intentionally avoids file-by-file detail. Use symbol search for
the names mentioned here.

## Bird's Eye View

scenery is a Go-native local runtime and toolchain for applications that declare
services with `//scenery:` directives and a `.scenery.json` root marker.

At a high level, scenery does four things:

- discovers an app root and parses Go packages into an app model
- generates a transient build workspace and synthetic runtime entrypoint
- runs one local HTTP server for the app's public, auth, and internal surfaces
- exposes local development, inspection, harness, and dashboard tools around
  that server

The central flow is:

```text
.scenery.json + Go source
        |
        v
internal/app + internal/parse
        |
        v
internal/model
        |
        v
internal/codegen + internal/build
        |
        v
generated workspace + scenery.sh/runtime
        |
        v
single local server + dev/inspect/harness tooling
```

Architecture invariant: the public scenery surface is scenery-named. User apps
should depend on `scenery.sh/...` packages and `//scenery:` directives, without
legacy compatibility packages, daemon layers, cloud layers, or non-scenery syntax.

Architecture invariant: app semantics should be captured as data in
`internal/model` before code generation or runtime wiring. Avoid duplicating
parser-derived decisions downstream when the model can represent them once.

## Code Map

### `cmd/scenery`

This is the CLI entrypoint and orchestration layer. `main`, `run`, and the
command-specific functions parse flags and connect internal packages into user
commands such as `up`, `serve`, `worker`, `build`, `check`, `inspect`,
`harness`, `logs`, `console`, `db`, `task`, and `generate`.

`scenery serve` is the headless app execution path. `scenery up` starts the local
app session around that runtime: dashboard, agent routing, live rebuild behavior,
logs, traces, metrics, managed dev services, optional frontend routing, and
process supervision.

Architecture invariant: non-CLI packages must not import `cmd/scenery`. Shared
logic belongs in `internal/` or a public package, depending on whether user apps
need it.

Architecture invariant: the CLI stays hand-rolled unless a new dependency has a
clear payoff. The command grammar is part of scenery's local contract and should
remain easy to audit.

### `internal/app`

`internal/app` owns repository and app-root discovery. It walks upward to find
`.scenery.json`, decodes app config, and provides repo-root helpers for self-harness
work.

Architecture invariant: `.scenery.json` is the app root marker for scenery apps. App
loading should fail clearly when the marker is missing or invalid.

### `internal/parse`

`internal/parse` loads Go packages with `go/packages`, reads `//scenery:`
directives from AST comments, validates endpoint/service/auth/middleware shapes,
and builds the app model.

It is responsible for service discovery, route defaults, typed and raw handler
signature validation, path parameter validation, service struct rules, and
auth-handler shape validation.

Architecture invariant: parser errors should point at source-level concepts:
services, endpoints, directives, signatures, paths, and tags. Later stages
should not need to rediscover invalid source shapes.

Architecture invariant: service names and service roots are model facts. Keep
nested-service and duplicate-name validation here rather than spreading it into
runtime or codegen.

### `internal/model`

`internal/model` is the shared vocabulary between parser, inspector, codegen,
wire modeling, and build. Important types include `App`, `Service`, `Package`,
`Endpoint`, `Middleware`, `AuthHandler`, `ServiceStruct`, `Entity`, and `View`.

Architecture invariant: the model is an in-memory description of a parsed app,
not a runtime registry and not a JSON schema. Public JSON responses live in
`internal/inspect`; runtime registration lives in `scenery.sh/runtime`.

### `internal/codegen`

`internal/codegen` turns the model into rewritten source files, per-package
generated files, endpoint wrappers, service struct wiring, middleware/auth
registration, wire metadata, and a synthetic `main`.

Architecture invariant: generated code should be boring Go. Prefer explicit
wrappers and registration over runtime reflection when the parser already knows
the shape of the app.

Architecture invariant: endpoint-to-endpoint calls should go through generated
scenery call helpers when scenery semantics matter. Direct user function calls must
not bypass auth context, private access rules, routing metadata, or internal
transport behavior.

### `internal/build`

`internal/build` owns the transient app build workspace. It writes generated
inspect artifacts, syncs source and generated files into the workspace, tracks
build fingerprints, runs `go mod tidy` when needed, compiles the app binary, and
writes latest-build metadata.

Architecture invariant: build outputs are disposable and reproducible from the
app root, config, source, and generated model. Do not make the transient
workspace the source of truth.

Architecture invariant: build metadata should be machine-readable enough for
agents and humans to diagnose drift without scraping terminal output.

### `scenery.sh/runtime`

`runtime` is linked into generated app binaries. It registers generated
endpoints, service initializers, middleware, auth handlers, legacy async runtime workers,
cron jobs, and wire endpoints, then starts one local HTTP server.

Important runtime concerns include route matching, request decode/encode, auth
context, current request metadata, structured error responses, middleware,
observability reports, secrets, DB tracing, legacy async runtime workers, cron, and graceful shutdown.

Architecture invariant: there is one local app server per generated app process.
`scenery up` may run extra development services around it, but app API execution
stays inside the generated app binary.

Architecture invariant: runtime request state must be scoped to the current
request or internal call. Public helpers such as `scenery.CurrentRequest()` and
`auth.UserID()` should not rely on global mutable app state that leaks across
requests.

### Public API Packages

The public packages at the module root are what user apps import:

- `scenery.sh` exposes `Meta` and `CurrentRequest`
- `scenery.sh/auth` exposes request auth state helpers and the
  standard auth module surface (`AuthData`, token helpers, standard auth
  registration, and pluggable email delivery)
- `scenery.sh/errs` exposes coded errors and HTTP status mapping
- `scenery.sh/middleware` exposes middleware types
- `scenery.sh/model` and `scenery.sh/page` expose static compile-time vocabulary
  for model/view IR; they do not maintain a runtime registry
- `scenery.sh/legacy-async-runtime`, `scenery.sh/cron`, `scenery.sh/pgxpool`, and related small
  packages expose local runtime integrations

Architecture invariant: public packages are boundaries. Keep them small,
stable, and oriented around user-app concepts. Internal implementation can move;
public names and behavior are much harder to change.

Architecture invariant: public packages may delegate inward to runtime internals
when necessary, but they should not pull in CLI, dashboard, parser, build, or
codegen concerns.

### `internal/inspect`, `internal/wire`, and `internal/wiremodel`

`internal/inspect` renders app, route, service, endpoint, build, path, trace,
metric, and docs information as stable JSON responses.

`internal/wire` defines the local binary wire format and capability protocol.
`internal/wiremodel` derives wire endpoint availability and schema hashes from
the parsed app model.

Architecture invariant: inspect and wire outputs are contracts. If the shape
changes, update the corresponding schema and tests in the same change.

Architecture invariant: wire compatibility is data-driven. Endpoint IDs, schema
hashes, fallback behavior, and unsupported reasons should be deterministic.

### `internal/devdash` and `internal/localproxy`

These packages support the local development platform around a running app.

`internal/devdash` stores dashboard-visible state and observability data.
`internal/localproxy` owns the local proxy layer. Victoria sidecars are supervised
from `cmd/scenery` as local development companions, and native dashboard views
surface local logs, traces, and metrics. The dashboard server and UI embedding
are orchestrated from `cmd/scenery`.

Architecture invariant: development services should be optional around the app
runtime. They can improve local ergonomics, but `scenery serve` must remain a
headless execution path.

### `ui`

`ui` is the scenery dashboard frontend. It is a TypeScript/React application that
is built and embedded for local development use.

Architecture invariant: frontend state should come from CLI/dashboard APIs and
stable inspect/observability data, not from duplicated guesses about parser or
runtime behavior.

### `docs`, `PLANS.md`, and `PLAN.md`

`docs` contains local contracts, schemas, active plans, completed plans,
runbooks, and the agent-readable knowledge index. `PLANS.md` defines the execution-plan
format. `PLAN.md` is strategic roadmap material, not the place to track
step-by-step implementation progress.

Architecture invariant: substantial implementation plans live under
`docs/plans/` and are linked from `docs/plans/active.md` while active.

### `testdata`

`testdata` contains fixture apps and golden generated files. It is the acceptance
corpus for parser, codegen, runtime, and CLI behavior.

Architecture invariant: fixture apps should speak scenery syntax directly. Use
Historical reference material only as a corpus when porting behavior into
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

Prefer tests at stable boundaries: directive parsing, app modeling, generated
code, CLI JSON contracts, runtime HTTP behavior, and fixture apps. Use helper
checks to keep tests data-driven and easy to update when internals move.

After repository changes, rebuild the CLI with `go install ./cmd/scenery`. For
substantial changes, run `scenery harness self --json --write` when practical so
`.scenery/harness/self-latest.json` captures one stable validation snapshot.

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
under the dev cache root; the project does not carry an embedded SQL driver for
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
