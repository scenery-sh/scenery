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
- [x] (2026-09-15) Milestone 2 generation: build preparation renders
  `scenery_internal_processes/services/<package>_<service>/main.go` (registers
  only that adapter and requires exactly its covered addresses) and
  `scenery_internal_processes/host/main.go` (no adapter imports; literal contract
  revision and route table). Generated internal clients call
  `runtime.InvokeContractBindingCodec`, which stays typed in-process and encodes
  through the callee contract codecs across processes. The multiservice fixture
  now declares `client "echo"` in `greeter`; through `scenery up` both paths
  return the typed responses, and through a generated host plus two generated
  service processes (distinct PIDs, stock `go build` with the prepared linker
  metadata) `POST /echo` returned `{"message":"echo:hi"}`, `POST /greet`
  returned `{"message":"greeter:echo:hello petr"}`, `GET /greet` 405 and an
  unknown path the runtime 404; with `echo` stopped the host answered 503
  `unavailable` for `/echo` and `greeter` a sanitized 500 `system.internal`.
- [x] (2026-09-15) Review of 8f28f9d1 shared by the human confirmed four
  boundary defects, each reproduced in code before fixing: forwarded calls did
  not restore authentication, request or trace state; failures lost cancellation
  and deadline identity; invalid injected wiring started a runtime without its
  process-link route; and the process directory was static and generation-blind.
  The first three are fixed: the callee re-enters the caller's authentication
  (typed standard auth data through a registered codec, unknown types fail
  closed), request metadata, SDK request and trace parent; failures carry
  `transport`, `errs`, `canceled` and `deadline_exceeded` categories and owner
  loss becomes `unavailable` with `delivery` `not_sent` or `unknown`; and
  `runtime.Main` fails before initialization when `SCENERY_PROCESS_LINK` is set
  but invalid. `TestProcessLinkedCallMatchesInProcessSemantics` compares the
  same authorized call in-process and process-linked (principal, tenant, role
  rule, invocation token, `CurrentRequest`, application child span parent) and
  failed with "authorization rule evaluation failed" when state re-entry was
  disabled.
- [x] (2026-09-15) Milestone 3 runtime slice: the process link names only the
  session token and the host's private dispatch listener; service processes send
  every non-local internal call there with the binding and pinned generation as
  headers. `runtime/process_host.go` accepts generation manifests on the private
  listener (strictly increasing numbers, the host's contract revision, an
  instance for every routed process and binding owner), pins public requests to
  the current generation through `X-Scenery-Process-Generation` (moved out of
  application headers by the service runtime), dispatches pinned calls within
  their generation, rejects answers whose identity headers differ from the
  published instance, reports in-flight work, and refuses to retire the current
  generation or one with work in flight. `TestProcessHostPinsRequestsAndCallsToTheirGeneration`
  keeps a request pinned to generation 1 in flight across the publication of
  generation 2 and proves calls pinned to 1 reach the first `echo` instance,
  unpinned calls reach the second, and a retired generation answers
  `unavailable` `not_sent`.
- [x] (2026-09-15) Milestone 3 supervisor slice: with
  `SCENERY_DEV_PROCESS_MODEL=service`, `scenery up` links the host and every
  service process with its own identity (`build.BuildDevelopmentProcessesContext`,
  build input digest over the entrypoint's import closure), preflights and
  starts services on per-instance sockets, starts the host on the API backend,
  and publishes generation 1. After an edit it relinks only processes whose
  identity changed, starts them on new sockets, publishes the next generation
  and retires the replaced instances once the host reports the old generation
  drained. On a copy of `testdata/apps/multiservice`: `/echo` and `/greet`
  answered from three processes; two `echo` body edits replaced only `echo`
  (PIDs 58577, 59261, 59413) while `greeter` (58578) and the host (58584) kept
  theirs, `/greet` returned each new text about 1.23 s after the save and old
  instances exited; a compile error and an `echo` constructor failure each left
  the published generation serving with unchanged PIDs; restoring identical
  source changed nothing; an edit to `internal/text`, imported by both
  services, replaced both in one generation (`packages_rebuilt` `echo_echo`,
  `greeter_greeter`, host unchanged) and the first new response already
  combined both edits; `scenery down` left no process, socket or link file.
- [x] (2026-09-15) Repeatable proof: `go run ./scripts/verify --probe
  process-model --summary --write` copies the multiservice fixture into a
  disposable root and agent home and runs it through `scenery up --detach` with
  the gate. It attributes every response through `X-Scenery-Process-ID`, keeps a
  `greet` request (fixture `wait:8s:` name) in flight while `echo` is replaced
  and requires it to answer from the first `echo`, requires the replaced `echo`
  to exit, a failed build and restored identical source to keep the published
  generation, a shared `internal/text` edit to rebuild exactly `echo_echo` and
  `greeter_greeter` with the host PID unchanged, and every observed process,
  socket and link file to disappear after `scenery down`. First run: pass in
  13.1 s (echo edit to response 1,267 ms, shared edit 1,277 ms). With host
  pinning disabled the probe failed on `greeter:echo-two:hello pinned`.
- [x] (2026-09-15) Milestone 4 readiness for ONLV: the host now runs the
  application-level registrations no service adapter owns (assistant gateways,
  assistant MCP manifests and MCP federation) from a generated
  `scenery_internal_processes/host/application.go` with the entrypoint's SQL
  and authentication wiring, serves those endpoints itself, and its assistant
  MCP gateways forward tool calls to the service process that registers each
  tool (literal `ProcessHostMCPTool` table) and durable status or cancellation
  to the process that accepted the receipt. Service processes answer
  `/__scenery/process/v1/mcp/call`, `/mcp/durable` and `/drain`; the supervisor
  stages and starts assistant helpers around a full generation start, drains
  schedules, event consumers and durable acquisition of replaced instances
  before retiring them, and refuses event consumers and emissions.
- [x] (2026-09-15) Milestone 4 first ONLV run (disposable worktree of ONLV
  `dbc6ca2a`, `scenery framework use --source` this checkout, `bun
  development/prepare.ts` with the gate): preparation, snapshot load,
  dev-bootstrap, scene registration and solar project creation passed through
  the host. The session ran 47 service processes, the host and the two
  assistant helpers (50 children) in 1.64 GB RSS, 45 idle Postgres connections
  (max 100) and 0.05 cores idle CPU; session executables used 1.0 GB. The
  initial build request took 18.3 s (implementation check 8.4 s, one stock
  `go build` of every entrypoint 9.5 s, starting all 48 processes 3.5 s).
  Six edits of an `ahjs` handler message each published a new generation that
  changed only `ahjs_ahjs`; edit to new response was 4.35-4.52 s. Telemetry
  showed 2.3 s before `go build` spent computing 48 implementation revisions
  (each re-projecting the whole contract) and rehashing 47 unchanged
  executables. After batching revisions and remembering executable digests the
  same edits took 2.59-2.67 s (build request 2.47-2.54 s: preparation to
  `go build` 0.77 s, stock `go build` 0.56-0.59 s, 0.28 s before activation,
  activation 0.62 s; the 0.84 s implementation check runs concurrently).
- [x] (2026-09-15) Same worktree and edit in the single application model:
  3 children in 441 MB RSS and 39 Postgres connections; edit to new response
  3.54-3.63 s (retained compiler with a whole-application link 1.13-1.26 s,
  implementation check 0.90-1.04 s, activation 0.25-0.30 s). Returning the
  worktree from the process model to the single model first failed with
  "prepared workspace membership changed: scenery-processes/..." because
  workspace membership verification did not own the process executable
  directory; fixed.
- [ ] Milestone 4 remaining: conformance through the host for internal calls,
  assistant MCP tools, durable work and streams; the retained compiler for
  process entrypoints; the implementation check and activation costs; and
  per-process status in dashboard and session records.
- [ ] Milestone 4: ONLV rebaseline, resources, background-work ownership,
  semantic conformance, and the edit-to-response measurement.

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
- Generated typed internal clients called `runtime.InvokeContractBindingFrom`
  with typed values, which cannot cross a process boundary without the callee
  contract's codecs; `InvokeContractBindingCodec` now carries the generated
  `scenery.MarshalContractValue` input encoder and `Unmarshal<Op>Outcome` decoder.
- Generated entrypoints refuse to start without the runtime bundle linker
  metadata (`VerifyLinkedContractBundle`), so every process main must be linked
  with the same `-X scenery.sh/runtime.linked*` values as the application
  executable; the supervisor must pass them per process in Milestone 3.
- A default `http.Transport` asks for gzip and the runtime server compresses
  when asked, so process-link calls and host forwarding would compress and
  decompress every local response; both transports disable compression and the
  host forwards the client's own `Accept-Encoding`.
- The composition registers application-level assistants and MCP federation
  outside every service adapter, so no generated service process registers them
  yet. Applications with those resources need a host-side or owning-process
  answer before Milestone 4.
- With stop-then-start replacement, internal calls to the stopped process fail
  in callers (now `errs.Unavailable`, `delivery: not_sent`; the fixture run above
  predates this and showed a sanitized `system.internal`) and forwarded requests
  fail as 503 `unavailable` from the host. Generation-aware replacement keeps
  the previous instance serving until the next generation is published.
- Authorization (`CurrentAuth`), the public `CurrentRequest` and application
  spans read goroutine-local request state entered with `enterState` and
  `appsdk.EnterInvocation`, not the `runtimeapi.Invocation` token. An
  in-process internal call inherits that state because it runs on the caller's
  goroutine; the first process link forwarded only the token, so a callee policy
  saw an unauthenticated principal.
- Standard authentication data is the typed `*auth.AuthData`, and
  `auth.CurrentAuthData` and `CurrentAuditIdentity` type-assert it. Rebuilding
  claims as a JSON map would authorize correctly but silently break audit
  identity, so the auth package registers a codec and other data types fail
  closed. Other runtime-created principals already use JSON maps.
- The partition baseline above used framework `de2d81028baf`, where the root
  facade imported `scenery.sh/runtime`; the current facade imports
  `internal/appsdk` and `runtime/shared`, although every generated process main
  still links `scenery.sh/runtime` through `runtime.Main`. The fixture timings
  used stock builds without `-w` from the Claude desktop shell. Performance
  decisions need a rebaseline with the current producer, effective development
  flags and the same launcher for whole-application and per-service replacement.
- One resident process per service is 48 processes plus the host for one ONLV
  worktree before any latency work; memory, database connections, idle CPU and
  cleanup need measurement early rather than after the latency milestone.
- A process-model `echo` edit through `scenery up` spent about 1.16 s in the
  build request: framework verification 44 ms, input fingerprint 60 ms, the
  joined implementation check 237 ms, one stock `go build` of the `echo` entrypoint
  413 ms, and activation 398 ms (session binary copy, preflight execution,
  process start, readiness and publication). The per-process build still uses
  stock `go build`, not the retained compiler, and the implementation check runs
  on every edit; both are the next latency targets before any ONLV measurement.
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
- Decision: the host is a generated entrypoint that imports only
  `scenery.sh/runtime` and calls `runtime.MainProcessHost` with a literal route
  table; it selects the owning process with the runtime's own route table
  (`routeTable.ownerRoute`) and forwards the unmodified request through
  `httputil.ReverseProxy`, restoring the client's forwarded headers. Rationale:
  importing adapters would put every implementation package in the host's Go
  closure and restart it on every edit; forwarding whole requests keeps CORS,
  gzip, trace IDs, response identity, policies and streaming in the owning
  process, and reusing the route table keeps precedence identical. Date:
  2026-09-15. Author: Claude.
- Decision: requests that match no route (including `/__scenery/config`, pprof
  and platform stats) go to the first service process by address; a path that
  matches routes but not the method, and a CORS preflight whose requested method
  no route allows, go to the highest-precedence matching route's owner.
  Rationale: runtime processes produce every response body and header. A path
  whose methods are split across processes reports only the owner's `Allow`
  methods; durable HTTP worker routes stay with the fallback until durable
  ownership is designed in Milestone 4. Date: 2026-09-15. Author: Claude.
- Decision: until Milestone 4 accepts ONLV, the development supervisor selects
  the process model only when `SCENERY_DEV_PROCESS_MODEL=service` is injected;
  the default stays the single application executable. Rationale: `scenery up`
  flags and `.scenery.json` are stable public contracts, while this selector is
  a temporary rollout gate that disappears when the process model becomes the
  only development model or is abandoned; an automatic capability gate would
  switch existing multi-service fixtures and applications to unproven lifecycle
  code. The registry entry records the sunset. Date: 2026-09-15. Author: Claude.
- Decision: every development process is linked with its own runtime identity.
  Its build input digest covers the build-input entries of the packages in that
  main package's Go import closure plus module, framework, producer and native
  entries, and its implementation revision is computed from that digest. The
  compilation graph alone selects the rebuild set: a process is relinked and
  replaced exactly when its identity changed, including a caller that imports
  another service's code as a library, while a caller that reaches a changed
  service only through an internal binding keeps running. The runtime-call
  graph (bindings and routes) decides dispatch and availability, not rebuilds.
  A changed contract revision replaces every process. Rationale: the same
  digest proves what a running process executes and selects the rebuild set
  without trusting watch event paths. Date: 2026-09-15. Author: Claude.
- Decision: dispatch is generation-aware and owned by the host. The supervisor
  publishes an application generation manifest to the host: generation number,
  contract revision, and for each process instance its private socket, PID and
  linked identity (contract revision, implementation revision, build input
  digest, Go target, the meanings of the existing response identity headers).
  Every process instance gets its own socket path; a replacement starts on a new
  path while the previous instance still serves. The host tags each forwarded
  request with the generation current at ingress, service processes carry that
  generation in forwarded call state, and internal calls go to the host, which
  dispatches to the owner of that binding in that generation and verifies the
  identity the owner returns. A generation stays dispatchable until the host has
  no in-flight work pinned to it; its replaced instances are then stopped. Work
  started outside a forwarded request (schedules, durable and event consumers)
  is pinned to the generation current when it calls. Rationale: reusing a
  socket path cannot prove which generation answered and a once-loaded
  directory leaves unchanged callers blind to ownership changes; host dispatch
  gives unchanged callers ownership changes without their own directory updates,
  and generation pinning prevents a request from combining a new and a previous
  service generation that were never published together. Direct
  service-to-service routing remains a later optimization with the same
  guarantees. This replaces an earlier same-day choice of stable socket paths
  with stop-then-start replacement. Date: 2026-09-15. Author: Claude, after the
  human-shared review.
- Decision: a forwarded internal call re-enters the caller's request state in
  the owner through one runtime entry point (`enterProcessLinkedCall`):
  authentication UID and data, request metadata (type, method, path, path
  parameters, headers, invocation, trace, caller binding, execution, deployment,
  locale, deadline, cron idempotency key), log and trace enablement, and the
  current span as trace parent; the invocation token is the caller's token.
  Authentication data crosses only as `nil`, a JSON map, or a type with a codec
  registered through `internal/authbridge` (the standard `*auth.AuthData`); any
  other type fails the call instead of degrading claims. The request payload
  does not cross. The trust basis is the session process-link token, which only
  supervisor-started processes of the session can read. Date: 2026-09-15.
  Author: Claude.
- Decision: forwarded failures preserve only a declared identity: transport
  outcomes with status and message, `errs` codes with metadata, cancellation and
  deadline expiry (matchable with `errors.Is`), each with its supported cause
  chain; other Go errors keep their message only. A call that cannot reach the
  owner returns `errs.Unavailable` with `delivery: not_sent`, one whose
  connection is lost after sending returns `delivery: unknown`, and neither is
  retried automatically because the owner may already have executed. The
  caller's own cancellation or deadline returns a sanitized `system.internal`
  wrapping the context error, as an in-process handler returning it would.
  Date: 2026-09-15. Author: Claude.
- Decision: `runtime.Main` validates injected process wiring before service
  initialization, so a candidate with invalid `SCENERY_PROCESS_LINK` exits and
  never becomes ready; an absent variable keeps the single-process runtime.
  Date: 2026-09-15. Author: Claude.
- Decision: application-level registrations run in the host, and the host
  forwards MCP tool calls of its assistant gateways to the owning service
  process of the current generation; durable status and cancellation go to the
  process that accepted the receipt, whose owner record authorizes them.
  Rationale: assistants and MCP federation belong to no service adapter, the
  host already survives implementation edits, and forwarding keeps each tool's
  policy, codecs and durable owner store in the process that registers it.
  Owner records live in process memory, as in the single application process,
  so replacing an instance loses its receipts exactly as an application restart
  does. Date: 2026-09-15. Author: Claude.
- Decision: a replaced service instance is drained (schedules, event consumers,
  durable workers and schedule loops stop, durable stores stay open) as soon as
  the next generation is published, then retired when its generation has no
  pinned work. Rationale: without draining, an old instance could claim new
  background work with old code during the retirement window; closing stores
  would break durable dispatch by requests still pinned to it. Event consumers
  and emissions are refused until their bus ownership across processes is
  proven. Migrations and seeds stay supervisor-owned (`scenery db setup`).
  Date: 2026-09-15. Author: Claude.
- Decision: per-service routes come from the same generator data that renders
  endpoint registrations (`runtimeBindingPath`, `renderContractPathTail`) and are
  checked against rendered adapter sources in tests. Rationale: a route table
  generated separately from registrations would drift. Date: 2026-09-15.
  Author: Claude.

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

Milestone 3 implements generation-aware dispatch and supervisor replacement.
The runtime host accepts published generation manifests, pins forwarded
requests to a generation, dispatches internal calls, verifies owner identity and
reports in-flight work per generation; service processes send internal calls to
the host. The development supervisor, behind the rollout gate, builds the host
and every service process with per-process identity, starts and preflights
them, publishes generations, and after an edit starts only the rebuild set on
new sockets, publishes the next generation and retires drained instances.
Acceptance through `scenery up` on the multiservice fixture: an `echo` body edit
restarts only `echo`, `greeter` and host keep their PIDs, and `/greet` returns
the new behavior with owner identity proving the new `echo` generation; an
in-flight call pinned to the previous generation completes against the previous
`echo`; a failing candidate leaves the published generation serving; and an
edit to a package imported by both services replaces both in one generation.
The fixture scenario becomes a repeatable repository probe in `scripts/verify`.

Milestone 4 applies the model to ONLV. It first rebaselines whole-application
versus per-service replacement with the current producer, effective flags,
launcher and correctness checks through the normal endpoint; measures memory,
database connections, idle CPU, startup concurrency and cleanup for one
worktree; and records the owner of migrations, schedules, event consumers and
durable acquisition per process. It then runs semantic conformance (typed
errors, auth, SQL transactions within a service, internal calls, durable work,
storage, streams, cancellation) and measures warm body edits against the
300/500 ms targets, reporting packages compiled, processes replaced,
constructors rerun and services that stayed available. Process topology stays
private to Scenery so services can later be co-located or activated lazily.

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
names a JSON file `{"token": "<at least 32 characters>", "dispatch":
{"network": "unix"|"tcp", "address": "<host private socket>"}}`. A service
process sends an internal call it does not register to the dispatch listener
with `X-Scenery-Binding` and, when pinned, `X-Scenery-Process-Generation`. A
process with that file serves `POST /__scenery/process/v1/bindings/invoke`,
which requires `Authorization: Bearer <token>`, answers with the identity
headers `X-Scenery-Contract-Revision`, `X-Scenery-Implementation-Revision`,
`X-Scenery-Build-Input-Digest`, `X-Scenery-Go-Target` and `X-Scenery-Process-ID`,
and accepts
`{"address", "caller_package", "invocation": {"id", "principal", "tenant_id",
"trace_id", "deadline", "caller_binding", "execution_id", "deployment",
"locale"}, "input": <JSON>}`. It answers `{"output": <outcome JSON>}` or
`{"error": {"kind": "transport"|"errs"|"error", ...}}`, and invokes only
bindings registered in that process. The request also carries `"call"`, the caller's request state
(`auth` with `uid`, `data_kind` and `data`; `request` metadata; `trace_id`,
`span_id`, `logs_enabled`, `trace_enabled`), omitted when the caller has none.
Errors use kinds `transport` (`outcome`, `status`, `message`, `cause`), `errs`
(`code`, `message`, `meta`, `cause`), `canceled`, `deadline_exceeded` and
`error`.

Generated internal clients (implemented in `runtime/process_link.go`):
`InvokeContractBindingCodec(ctx, address, callerPackage, invocation, input,
encodeInput, decodeOutput)` invokes a locally registered binding with typed
values and otherwise requires the current invocation, encodes the input, calls
the owner through the process link, and decodes the outcome.

Process host (implemented in `runtime/process_host.go`):
`MainProcessHost(ProcessHostConfig{Name, ListenAddr, Fallback, Routes:
[]ProcessHostRoute{{Process, Methods, Path, PathTail}}})` serves public requests
on `SCENERY_LISTEN_NETWORK` and `SCENERY_LISTEN_ADDR` and, on the process link's dispatch listener with the
session token, internal-call dispatch plus generation control:
`PUT /__scenery/process/v1/generations` with `{"generation", "contract_revision",
"processes": {"<process>": {"network", "address", "pid", "identity":
{"contract_revision", "implementation_revision", "build_input_digest",
"go_target"}}}, "bindings": {"<binding address>": "<process>"}}` (204 or 409),
`GET /__scenery/process/v1/generations` returning `{"current", "generations":
[{"generation", "in_flight", "processes": {"<process>": <pid>}}]}`, and
`DELETE /__scenery/process/v1/generations/<n>` (204, 404, or 409 for the
current generation or one with work in flight). Before the first publication
and for an unreachable instance it answers 503 `unavailable`. Generated
workspace layout: `scenery_internal_processes/host/main.go` and
`scenery_internal_processes/services/<package>_<service>/main.go`, excluded from
watch and source scans like `scenery_internal_main`.
