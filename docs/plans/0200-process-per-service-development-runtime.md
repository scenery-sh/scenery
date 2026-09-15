# Process-Per-Service Development Runtime

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Today `scenery up` builds one generated application executable
(`scenery_internal_main`) and restarts it for every Go implementation edit. For
the full ONLV application that executable links 619 packages; its linker alone
needs about 0.52 s, and the edit-to-response loop stays in whole seconds.

This plan changes the internal development artifact model, not the public app
model. Each Scenery package that declares a Go service runs as its own process.
A stable host process owns HTTP routing and framework-wide concerns, forwards
each request to the owning service process, and service processes call each
other through the runtime's existing internal-binding boundary over private
local sockets. An implementation edit rebuilds and restarts only the service
processes whose Go dependency closure contains a changed package; every other
process keeps running.

The observable goal on the reference Apple M2 Ultra: a warm handler-body edit in
one ONLV service reaches a verified new typed HTTP response with p50 at most
300 ms and p95 at most 500 ms, while all other service processes keep their PIDs,
and typed errors, authentication, SQL, internal calls, durable work and streams
keep their current behavior.

The human authorized this direction on 2026-09-15: one process per service is
acceptable, inter-service communication may cross processes, and Scenery should
compile the application graph as it needs.

## Progress

- [x] (2026-09-15) Measured ONLV partition evidence: 48 Scenery packages with Go
  services; per-service executables link 213–314 ms versus 517–544 ms for the
  whole application; 82 percent of application Go files belong to exactly one
  service; seven cross-service Go imports and one shared non-service package.
- [x] (2026-09-15) Confirmed runtime seams: HTTP endpoints with contract codecs,
  internal bindings addressed by string with JSON codecs, Unix-socket listening,
  and existing API/worker roles.
- [x] (2026-09-15) Milestone 1: `testdata/apps/multiservice` (`greeter` calls
  `echo/binding/echo_internal`) passes `scenery check` and contract generation;
  one `scenery up` process returned `{"message":"greeter:echo:hello petr"}` from
  `POST /api/greet` and `{"message":"echo:hi"}` from `POST /api/echo`.
- [x] (2026-09-15) Milestone 2 runtime slice: `runtime/process_link.go` routes an
  internal binding absent from the calling process to its owner over a private
  socket with the caller's invocation metadata and restores transport, `errs`
  and plain errors; `SCENERY_PROCESS_LINK` is registered as injected wiring.
  `go test ./runtime` covers owner invocation, caller forwarding, token
  rejection and malformed links.
- [x] (2026-09-15) Hand-assembled prototype: separate `echo` and `greeter`
  processes (each registering only its adapter on a Unix socket) behind a
  path-routing gateway served both fixture paths; replacing only `echo` three
  times kept `greeter` and the gateway running while `/greet` returned each new
  `echo` behavior.
- [ ] Milestone 2 generation: generated per-service mains with exact required
  addresses, a generated host route table, and typed internal clients that
  encode through the callee contract before crossing processes.
- [ ] Milestone 3: supervisor builds, starts, preflights, routes and replaces
  individual service processes; rebuild sets from the Go dependency closure.
- [ ] Milestone 4: ONLV acceptance for all services, semantic conformance, and
  the edit-to-response measurement.

## Surprises & Discoveries

- ONLV partition data (generated workspace for the main checkout, framework
  `scenery.sh v0.3.7-0.20260910222939-de2d81028baf`): a per-service implementation
  closure is 318–414 packages (median about 326) because every service imports
  the root `scenery` facade, which imports `scenery.sh/runtime` (314-package
  closure). Removing the host `runtime.Main` from a per-service executable saved
  only 5–20 ms of link time.
- Cross-service Go imports in ONLV are library-style calls with injected
  resources (`dataset` and `maps` call `drive.SaveFilesContext` with a declared
  `DriveDatabase` and `Storage`; `health` uses `audit.Error`; `tasks` calls
  `agents.QueueOracleSubmitAndWait`; `house` and `maps` call the declared
  `maps3d` library) plus one runtime internal call (`invoices` invokes
  `contacts/binding/contacts_get_internal` through
  `runtime.InvokeContractBindingJSON`). Only `pkg/appfs` is a non-service
  package shared by several services (five).
- Declared constructor dependencies across ONLV services are `Database` (38),
  `Storage` (4), `DriveDatabase` (2) and scalar configuration values.
- An internal binding's JSON response is the operation outcome envelope, for
  example `{"kind":"result","name":"ok","value":{"message":"echo:hello petr"}}`,
  not the bare result value.
- Any edit under the repository, including a fixture inside `testdata/`, changes
  the framework source digest, so a running session whose CLI was built earlier
  rejects the next rebuild with SCN8003 until the worktree-local CLI is rebuilt.
  Runtime prototyping therefore runs service processes directly instead of
  through `scenery up`.
- Generated typed internal clients call `runtime.InvokeContractBindingFrom` with
  typed values, which cannot cross a process boundary without the callee
  contract's codecs. Only the JSON entry point is linked across processes so far.
- Fixture timing with stock `go build` (no `-w`, load average about 15, probe
  launched from the Claude desktop shell): build 649–666 ms after the first
  1,712 ms, stopping the old `echo` 41–45 ms, new process start to listening
  socket 351–371 ms, first cross-process `/greet` response 11–12 ms, about
  1.07 s total. The start interval again reflects macOS first execution of a
  new file from this launching context.

## Decision Log

- Decision: the process unit is a Scenery package that declares a Go service.
  Packages without a service are libraries linked into every process whose Go
  closure imports them. Rationale: the package contract, adapter registration
  and constructor dependencies already form the typed boundary, and most code is
  owned by exactly one service. Date: 2026-09-15. Author: Petr and Claude.
- Decision: service processes serve their own registered HTTP endpoints over a
  private Unix socket, and a stable host forwards requests by route. Rationale:
  forwarding complete HTTP requests reuses the existing endpoint decode, auth,
  outcome encode and streaming paths instead of inventing a second payload
  protocol. Date: 2026-09-15. Author: Claude.
- Decision: an internal binding not registered in the calling process is routed
  to its owning service process with the current invocation context. Rationale:
  internal calls are already addressed by string with JSON codecs and an
  explicit invocation; that is the cross-process seam. Date: 2026-09-15.
  Author: Claude.
- Decision: library-style cross-service Go imports stay compiled into the
  importing process with that process's own declared resources. Rationale: the
  observed ONLV imports receive their database and storage handles as explicit
  dependencies rather than sharing another process's state. Package-level state
  written by another service's constructor remains a risk that conformance must
  detect. Date: 2026-09-15. Author: Claude.
- Decision: pass process-link wiring as an injected `SCENERY_PROCESS_LINK` path
  to a private 0600 file holding the session token and binding owners.
  Rationale: generated mains reserve arguments for CLI requests, the supervisor
  already injects runtime wiring through `SCENERY_LISTEN_*`, `SCENERY_ROLE` and
  `SCENERY_DURABLE_*`, and a file keeps the token out of process environment
  listings. It is supervisor wiring, not user configuration. Date: 2026-09-15.
  Author: Claude.
- Decision: a process-link request that reaches a process without the binding
  returns "not registered in this process" instead of forwarding again.
  Rationale: a stale or inconsistent directory must fail visibly rather than
  loop. Date: 2026-09-15. Author: Claude.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Generated composition lives in `internal/generate/generate_application.go`
(`renderApplicationComposition`). For each Go service package it renders an
adapter package `internal/scenerygen/<package>_<service>_adapter` whose
`Register(scenery.Registry)` calls `runtime.RegisterNativeService` (constructor
and dependency resolution through `scenery.sh/db.Get`) and registers endpoints
and internal bindings. The composition package registers every adapter, and the
generated `scenery_internal_main/main.go` configures SQL bindings and auth,
builds a `runtime.ContractRegistry` with the application's required addresses,
and calls `runtime.Main`.

`runtime/app.go` `Main` initializes services, starts durable, event and cron
runtimes, and serves HTTP on `SCENERY_LISTEN_*` unless `SCENERY_ROLE=worker`.
`runtime/contract_internal.go` `InvokeContractBindingJSON` looks up an internal
binding in the process-global registry and invokes it with the current
`runtimeapi` invocation. `internal/agent/router.go` proxies `/api/` of a session
to its API backend socket. The development supervisor in `cmd/scenery`
(`RebuildAndRestart`, `prepareAppStart`, `replaceAppGeneration`) owns building,
candidate preflight, activation and rollback of the single application process.
`internal/build` and `internal/nativebuilddriver` own the retained per-workspace
compiler/linker recipe used for development builds.

Terms: a service process is the executable for one Scenery service package; the
host process is the stable executable that owns routing and framework-wide
state; a rebuild set is the set of service processes whose Go closure contains a
changed package.

## Milestones

Milestone 1 adds `testdata/apps/multiservice`: package `greeter` exposes an HTTP
binding whose handler invokes package `echo`'s internal binding, and `echo` also
exposes its own HTTP binding. The fixture passes `scenery check`, generation and
a single-process `scenery up` request through both paths. This is the baseline
and the smallest application with a cross-service call.

Milestone 2 adds, behind the internal development artifact model only, a
generated main per service package that registers that package's adapter and
the application's SQL/auth wiring it needs, and a generated host main without
service implementations. The runtime gains a service-process mode listening on
a private socket, remote endpoint registrations in the host that forward HTTP
requests to the owning socket, and remote internal-binding stubs that forward to
the owning service with the encoded invocation. Unit tests cover forwarding,
invocation propagation and failure mapping; a fixture probe proves both
fixture paths through separate processes.

Milestone 3 teaches the development supervisor to build the host and every
service process (retained recipe per main), start and preflight them, publish a
route and binding directory, and replace only the rebuild set after an edit.
Logs, status and runtime identity report per-process generations plus an
application generation that binds them.

Milestone 4 applies the model to ONLV, runs semantic conformance (typed errors,
auth, SQL transactions within a service, internal calls, durable work, storage,
streams, cancellation) and measures warm body edits against the 300/500 ms
targets.

## Plan of Work

Start with the fixture and the single-process baseline. Then prototype the
runtime pieces in `runtime/` with focused unit tests, using hand-assembled mains
in a fixture copy before changing the generator. Only after two processes serve
both fixture paths correctly, move main generation into `internal/generate`,
then supervisor lifecycle in `cmd/scenery`, then ONLV. Record every contract or
architecture statement that changes (for example ARCHITECTURE.md's statement
that application API execution stays inside the generated application binary)
and update `docs/local-contract.md`, `docs/agent-guide.md` and `ARCHITECTURE.md`
with the milestone that changes behavior.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

1. Create `testdata/apps/multiservice` by following `testdata/apps/basic`, run
   `go run ./cmd/scenery check -o json --app-root testdata/apps/multiservice`
   and the generation command reported by `go run ./cmd/scenery help generate -o json`.
2. Run the fixture with `.scenery/harness/bin/scenery up -o jsonl --app-root testdata/apps/multiservice`,
   call both HTTP paths, and stop it with `scenery down`.
3. Continue with Milestone 2 as described above.

## Validation and Acceptance

Expected changed-area classes grow by milestone: fixture and documentation
(`go test ./...`, `go run ./scripts/verify --summary --write`), runtime and
supervisor (`go test ./runtime ./cmd/scenery ./internal/build`, `go test ./...`,
`golangci-lint run ./...`, `go run ./scripts/verify --summary --write`,
`go run ./scripts/verify --probe dev-process --summary --write`), and generator
changes (`go test ./internal/generate` plus both committed client regenerations
from the root instructions). Milestone 1 acceptance is the fixture check,
generation, and both HTTP paths returning their typed responses through one
process. Milestone 2 acceptance is the same responses through a host process and
two service processes with distinct PIDs. Milestone 3 acceptance is an edit to
`echo` restarting only the `echo` process while `greeter` keeps its PID and the
cross-service path returns the new behavior. Milestone 4 acceptance is the ONLV
conformance list above and a short edit-to-response measurement against the
300/500 ms targets; the human requested this measurement direction on
2026-09-15.

## Idempotence and Recovery

Fixture runs are owned sessions stopped with `scenery down`; generated fixture
output that is tracked must be regenerated deliberately and reviewed in the
diff. Prototype mains live in a fixture copy under the session scratchpad until
Milestone 2 moves generation into the repository. No ONLV checkout is modified;
ONLV evidence uses the generated build workspace copy or owned worktrees.

## Artifacts and Notes

Partition measurements from 2026-09-15 are summarized in Surprises &
Discoveries. The scratchpad analysis scripts are not repository artifacts; any
analysis that becomes a decision input moves into `scripts/verify` with tests.

## Interfaces and Dependencies

No new third-party dependency is planned. Public `.scn` declarations and Go APIs
stay unchanged. Internal interfaces added by this plan must be recorded here as
they are designed: the service-process listening contract, the route and
binding directory the supervisor publishes, and the invocation encoding used for
remote internal bindings.

Process link (implemented in `runtime/process_link.go`): `SCENERY_PROCESS_LINK`
names a JSON file `{"token": "<at least 32 characters>", "bindings":
{"<binding address>": {"network": "unix"|"tcp", "address": "<socket>"}}}`. A
process with that file serves `POST /__scenery/process/v1/bindings/invoke`,
which requires `Authorization: Bearer <token>` and accepts
`{"address", "caller_package", "invocation": {"id", "principal", "tenant_id",
"trace_id", "deadline", "caller_binding", "execution_id", "deployment",
"locale"}, "input": <JSON>}`. It answers `{"output": <outcome JSON>}` or
`{"error": {"kind": "transport"|"errs"|"error", ...}}`, and invokes only
bindings registered in that process.
