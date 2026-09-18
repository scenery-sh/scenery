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
  the process model selector (since removed) set to `service`, `scenery up` links the host and every
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
- [x] (2026-09-15) ONLV conformance smoke through the public origin, run first
  in the single model and then in the process model with identical results: a
  streamed `GET /api/drive/maps/<scene>/scene.glb` returned the 8,924,616
  preset bytes with the asset digest (served by the `drive` process); an
  invoice for a seeded contact passed `contacts/binding/contacts_get_internal`
  (which reads the typed `*auth.AuthData` tenant) and failed at its later
  `issue_date` check; an unauthenticated request answered 401; and a signed
  assertion to the host's private assistant MCP gateway listed 54 tools and
  `workspace__describe` answered `described` from the `copilot` process.
  Durable house jobs need the native lane and were not exercised.
- [x] (2026-09-15) Telemetry for one-service ONLV edits: rebuilding the
  retained Go input graph after every proven-current listing took 253 ms and is
  now skipped on hits (both models); `process.identity` 115-123 ms; stock
  `go build` of one entrypoint 556-778 ms; `process.retain` 21-29 ms;
  `process.preflight` 360-364 ms (first execution of a new file from this
  launching context) and `process.start` 47-56 ms. Edits then took 2.44-2.96 s
  (median about 2.49 s). The timeline to publication (2.13 s) is 0.37 s of
  pipeline preparation before input discovery, 0.12 s identity, 0.56 s build,
  about 0.45 s between the build and activation (status, database setup check,
  current snapshot rescan, runtime environment resolution) and 0.42 s preflight
  and start; the implementation check ends before the build.
- [x] (2026-09-15) Rebuilds now resolve runtime capabilities while Go compiles
  (both models; a candidate whose checked contract changed its SQL requirements
  resolves again). Post-build telemetry on ONLV: status 6-7 ms, database setup
  check 62-67 ms, current snapshot rescan 200-214 ms, then activation 420-427 ms
  (retain 20-25, preflight 349-360, start 45). One-service edits took
  2.31-2.52 s (median about 2.41 s) with publication about 2.0-2.1 s after the
  build starts. The implementation check (0.86-0.95 s, concurrent) now ends
  after the 0.55 s entrypoint build, so a faster link alone no longer shortens
  the path.
- [x] (2026-09-16) Review of 9a0b54d0 shared by the human found lifecycle
  ownership gaps, each confirmed in code before fixing. Retiring a generation
  stopped the instances its successor replaced even when an older retained
  generation still named them; supervisor instance lifetime is now reference
  based (an instance stops only when neither the current services nor any
  retained generation name it), and a generation whose pinned work outlives
  30 s is force-retired by the host (`DELETE .../generations/<n>?force=true`),
  which ends dispatch within it before its unreferenced instances stop.
  Host-forwarded MCP tool calls entered unpinned request state; the host now
  sends the generation it selected and the tool state carries it to nested
  internal calls. Durable MCP receipts were routed by service name to whatever
  instance was current and authorized only by that instance's memory; the host
  now authorizes receipts itself (principal, owning process, durable service and
  task, bounded to 4,096 records, conflicting owners fail closed) and the owning
  process of the current generation reads the shared durable store. Candidates
  started schedules, event consumers and durable acquisition at startup; service
  processes under a process link now serve requests first and acquire
  background work only on `/__scenery/process/v1/activate`, which the supervisor
  sends after publication and after draining the replaced instances (drain
  revokes for the rest of the process life). A complete replacement stopped the
  previous host and services before starting the new generation and could not
  restore them; candidate services now start while the previous generation
  serves, the previous host stops only when they are ready, and a failed
  candidate host is abandoned and the previous host restarted from its retained
  launch with the previous services, which were never drained. Each host
  incarnation gets its own link file and dispatch socket. The process build now
  takes the host-wide fair link slot, and configured `-ldflags value` pairs no
  longer leak their value as a top-level build argument. Unit tests:
  `TestProcessHostPinsForwardedMCPToolCallsAndTheirInternalCalls`,
  `TestProcessHostForwardsMCPToolsAndAuthorizesDurableReceiptsAcrossReplacement`,
  `TestProcessHostForcedRetirementEndsDispatchWithinTheGeneration`,
  `TestRuntimeBackgroundStartsOnlyOnActivationAndNeverAfterDrain`,
  `TestDevProcessInstancesStopOnlyWhenNoRetainedGenerationNamesThem`,
  `TestDevProcessReplacementRestoresThePreviousHostOnlyAfterCandidatesStop`,
  `TestDevelopmentProcessBuildArgsMoveEveryLinkerFlagIntoEachEntrypoint` and
  `TestRetainedDevelopmentProcessDigestsDoNotRereadUnchangedExecutables`; each
  failed with its fix mutated away. `TestProcessHostCountsStreamsAndUpgradedConnectionsAsPinnedWork`
  keeps a generation unretirable while an event stream or an upgraded
  connection forwarded in it is open.
- [x] (2026-09-16) The `process-model` probe now keeps a `greet` request
  (fixture `wait:15s:`) pinned to generation 1 while `greeter` and then `echo`
  are replaced, requires it to answer `greeter:echo:hello pinned` from the first
  instances after generation 3 was published and generation 2 retired, requires
  both first instances to exit afterwards, and requires the `echo` replacement
  to record `process.background` drain then activate. After the existing failed
  build, identical-source and shared-edit checks, it makes `echo`'s constructor
  fail, adds a result field to `echo`'s contract (the complete generation fails
  while the host, `greeter` and `echo` keep their PIDs), then fixes the
  constructor so the contract-changing generation commits with a new host and
  the previous host and services exit. Pass in 27.4 s (echo edit to response
  1,199 ms, shared edit 1,228 ms). With retained-generation references removed
  the probe failed: the pinned request answered 500 `system.internal`.
- [x] (2026-09-16) ONLV after the lifecycle changes (same disposable worktree,
  framework a12d7174, process model): the initial build request took 16.6 s
  (stock `go build` of 48 entrypoints 8.8 s with no fair-slot wait,
  implementation check 6.1 s, activation 3.5 s including activation of 47
  services in 3 ms). The conformance smoke matched the earlier runs: the
  streamed map download returned the preset digest from `drive`, the invoice
  request passed the `contacts` internal binding and failed at `issue_date`, an
  unauthenticated request answered 401, and the host MCP gateway listed 54 tools
  with `workspace__describe` answered by `copilot`. Six `ahjs` handler edits
  each published a generation that changed only `ahjs_ahjs`, and the edit to
  response took 2,686 ms for the first edit, then 2,295-2,381 ms; drain and
  activation each took under 1 ms. Replaced instances retired: the supervisor
  kept 48 application processes (47 services and the host) beside the frontend,
  assistant helpers and Victoria.
- [x] (2026-09-16) Replacement instances are now retained, preflighted and
  given their socket while the supervisor verifies the candidate (status,
  database setup inputs and the current source snapshot), so activation only
  starts them. On ONLV the activation step fell from 418-430 ms to 119-138 ms
  and one-service edits from 2,295-2,381 ms to 2,026-2,095 ms. The timeline
  after the 552 ms entrypoint build is now: implementation check to 1,222 ms,
  preflight 1,283-1,633 ms beside the 216 ms snapshot rescan, process start
  47 ms, publication at 1,680 ms of a 1,951 ms build request. The next gates
  are the preflight itself, the implementation check (which delays the
  preparation), the 375 ms of preparation before input discovery and the
  entrypoint build.
- [x] (2026-09-16) The implementation check now joins after the process build
  has verified and saved its workspace, so preparing the replacement instance
  no longer waits for it. ONLV one-service edits took 1,969-1,991 ms, and the
  publication timeline of a 1,846 ms build request is: 376 ms of preparation
  before input discovery, 90 ms input fingerprint, 120 ms process identities,
  548 ms entrypoint build, 45 ms workspace verification, then the preflight
  (1,215-1,561 ms) beside the database and snapshot checks, 55 ms process start
  and publication at 1,616 ms. The check itself ended at 1,189 ms, off the
  critical path.
- [x] (2026-09-16) Identifying every process entrypoint now memoizes package
  closure digests over the import graph instead of unioning the inputs of each
  entrypoint's closure, and package inputs are stamped concurrently. Go package
  tests and lint pass. On ONLV afterwards (2026-09-16) `process.identity` took
  103-118 ms and `go.input_fingerprint` 82-105 ms of a one-service edit, so
  neither change measurably moved the earlier 120 ms and 90 ms.
- [x] (2026-09-16) A service process that stops on its own restarts from its own
  verified executable and is published as the next generation, bounded by three
  restarts per minute per service; beyond that the service stays degraded until
  its next build, and the published generation keeps naming the process that is
  gone so requests to it fail visibly. `TestDevProcessRestartBudgetDegradesAServiceThatKeepsCrashing`
  covers the budget, and the `process-model` probe kills `echo` and requires the
  same implementation to serve again while `greeter` keeps its process.
- [x] (2026-09-16) Per-process status: `devdash.AppStatus` reports every service
  process (name, PID, generation, implementation revision, running or degraded
  with its reason), the console shows them in an overview panel, and the agent
  session record names each running service process as `service:<name>`.
- [x] (2026-09-16) Generation-bound evidence: every answer the host serves names
  its application generation in `X-Scenery-Process-Generation`, including the
  answer to a request pinned to a replaced generation, and the host's generation
  status lists each retained generation's instances with their linked
  identities. The `process-model` probe requires the pinned answer to name the
  generation it entered while later answers name a newer one.
- [x] (2026-09-16) Deterministic failure injection: a session's host accepts
  fault rules on its private control listener that refuse, delay or lose the
  answer of selected dispatched calls and forwarded requests, each bounded by a
  count, so a disposable session can prove application retry and idempotency
  behavior. `TestProcessHostFailsSelectedWorkOnPurpose` covers every mode.
- [ ] Reaching the 300/500 ms targets needs the remaining path to shrink about
  fivefold, and no single step dominates it any more: the entrypoint build
  (548 ms of stock `go build`), the first execution of the new executable
  (346 ms preflight), the preparation before input discovery (376 ms: framework
  verification 53, workspace cache and materialization about 140, projections
  37), input fingerprint and process identities (210 ms), and the process start
  (55 ms). The retained compiler for entrypoints and a cheaper workspace
  preparation are the next candidates; a single-start handshake would save only
  the second execution (about 55 ms) because the first execution's cost stays.
- [ ] Remaining follow-ups from the 9a0b54d0 review: bound link parallelism
  inside one process build (the fair slot admits the build, but `go build` still
  links up to `-p` entrypoints at once; measure the peak memory of a complete
  ONLV build first), and replace the separate preflight execution with a
  single-start attestation, which saves only the second execution because the
  first execution's cost stays.
- [ ] The disposable ONLV worktrees of this work are removed: their retained
  clusters, containers and volumes through `scenery prune --older-than 1s --all
  --app-root <root>`, then the checkouts and their build workspaces and retained
  compiler state, which returned 9.7 GB. `scenery prune` reported every usage
  mistake as an internal failure with an opaque token, so an unusable
  `--older-than` is now an invalid request that names the flag and an example.
  Repeating the ONLV measurements needs a new disposable worktree.
- [x] (2026-09-16) ONLV process-model startup is unblocked. A disposable
  worktree of ONLV `fd5bd25b` prepared in the single application model and
  restarted in the process model and with `--wait registered`
  reached `run.ready` 18-21 s after start on every one of six restarts:
  compilation 10.6-10.8 s, "Starting prepared assistant runtimes" 419-454 ms.
  Edits of `solar/ahjs/detail.go` rebuilt and published only `ahjs_ahjs`. A
  contract edit failed on stale TypeScript clients while generation 1 kept
  serving, and after `scenery generate` published a complete generation whose
  helpers started in 454 ms. Remaining Milestone 4 acceptance (resources,
  conformance smoke, the 300 ms p50 goal) is still open.
- [x] (2026-09-16) ONLV edit baseline for a warm one-service handler edit (edit
  to verified response, polled every 20-50 ms): 2,200-3,041 ms with the
  retained entrypoint (backend 787-857 ms) and 2,010-2,481 ms with stock builds
  (528-705 ms, a recording running beside some of them). The retained backend
  spent 162-233 ms discovering and hashing its whole input domain before a
  110 ms compile and a 280 ms link. Two changes then removed measured cost:
  retained input capture hashes and resolves workspace membership concurrently
  (discovery 46-78 ms, backend 532-583 ms), and a compile-start status report no
  longer runs `ps` once per registered process twice per edit (650-735 ms to
  20-24 ms). Edits now take 1,961-2,172 ms; a 1.83-2.0 s build request is about
  0.45 s of preparation, 0.09 s input fingerprint, 0.11 s identity, 0.55 s
  entrypoint build, 0.35 s candidate preflight beside the 0.21 s snapshot
  rescan, and 0.05 s activation.

- [x] Service entrypoints can link through the retained compiler. Each
  entrypoint keeps its own recorded recipe, and the recipes of one workspace
  share a single content-addressed store of archives, support inputs and source
  snapshots. A session records a recipe only for an entrypoint it has linked by
  stock Go more than once, one recording at a time, at a lowered scheduling
  priority. Measured on the three-service fixture: recording one entrypoint
  takes 20-25 s and leaves 223 MB of shared state (the same three entrypoints
  cost 1.4 GB before the store was shared), and a retained service build takes
  528-622 ms against 434-708 ms for the stock build of one service. The retained
  path is therefore not yet a latency win at this closure size; its value has to
  be measured on ONLV, which stays blocked below.

- [x] Link concurrency of an entrypoint build is measured instead of assumed.
  Relinking all 48 ONLV entrypoints from a warm cache takes 3.2 s of wall time
  and 30.6 s of CPU on 24 cores, and the concurrent compiler/linker processes
  reach 5.2 GB of resident memory together; one entrypoint links in 0.83 s at
  363 MB. The Go command already bounds those actions by `-p`, so the budget
  that was actually missing is Scenery's own: one background recipe recording
  at a time, at a lowered priority, beside the host-wide fair link slot. A
  machine whose available memory is below its core count times 450 MB would
  need an explicit lower bound; this one is not close.
- [x] The implementation check needs no incremental form. In a one-service
  rebuild it starts at 95 ms and ends at 338 ms while the entrypoint build runs
  from 156 ms to 775 ms, so it is fully overlapped. The remaining critical path
  of a 1261 ms edit is the entrypoint build (619 ms) and activation (410 ms, of
  which candidate preflight is 359 ms).
- [x] Review corrections of 2026-09-16, before any further optimization. Status
  readers use a status the process model publishes at each committed change, so
  assistant startup inside an activation no longer waits for `model.mu`. An
  unconfirmed activation reports a degraded service and is repeated under
  `model.mu` until it confirms or the instance is no longer current. Background
  recipe recording is owned by the session (cancelled and joined at close),
  captures its input revision before its tools run, validates it afterwards,
  and merges only recorded actions whose captured inputs match. A rejected
  recipe is replaced; an existing store entry is hashed before adoption; the
  shared store is collected every 16 publications outside capture leases.
  The multiservice fixture and ONLV (see the ONLV entries above) exercised an
  edit discarding a recording, a replacement recording, and a session close
  cancelling one; a long-edit endurance run through the product path remains
  to be measured.
- [x] (2026-09-16) Recipe recording is admitted machine-wide: one recording at a
  time across every supervisor, only while no foreground link of any worktree
  is queued or running, with tool parallelism bounded to a quarter of the
  cores. On ONLV a bounded recording took 13.7-14.6 s instead of 24.8 s.
- [x] (2026-09-16) Prepared assistant helpers start after the process model is
  released, on a complete replacement and on a restore, so no helper start or
  its callbacks run under `model.mu`.
- [x] (2026-09-16) Second review of the recording and activation paths, each
  finding reproduced in code first. A recorded action is now rejected when it
  read a file from a selected package directory that its capture does not name
  (a source added, compiled and removed during a recording kept every stamp
  and directory listing) or compiled a package outside the selection. A
  recording's Go command and every tool it starts form one process group that
  is killed and confirmed empty before the recording releases its slot, lease
  and directory; the tagged `TestRetainedRecordingCrossProcess` journey, run by
  the `process-model` probe, fails without that and passes with it. On Darwin a
  group of exited, unreaped members answers `EPERM`, which ONLV exposed and the
  confirmation now waits out. A foreground link that arrives during a recording
  makes it yield and retry. Control requests are serialized per instance and
  an activation is repeated without holding `model.mu`, so an unresponsive
  service delays only itself; a drained instance is never activated again.
- [x] (2026-09-16) ONLV comparison of automatic recording, seven `ahjs` handler
  edits per series with the supervisor process tree sampled every 250 ms (the
  first edit of a session includes a cold 1.8 s entrypoint build):

  | Series | Backend per edit | Edit to response, edits 2-7 | Mean CPU | Mean RSS |
  |---|---|---|---|---|
  | A: recording disabled (`scenery_benchmark_stock` binary) | stock 610-657 ms | 2,171-2,281 ms, median 2,239 | 68 % | 2.98 GB |
  | B: recording running and yielding to every edit | stock 657-819 ms | 2,477-2,687 ms, median 2,638 | 150 % | 3.24 GB |
  | C: retained recipe ready, no recording | retained 557-603 ms | 2,153-2,286 ms, median 2,227 | 75 % | 2.78 GB |

  A ready recipe does not shorten an edit measurably, and an edit made while a
  recording runs is about 400 ms slower at twice the CPU. Automatic recording
  therefore does not improve this session; see the Decision Log.
- [x] (2026-09-16) Per-service recipe recording and retained entrypoint linking
  are removed (see the NO-GO decision). A workspace's first process build
  deletes the entrypoint recipes an earlier session recorded, which on the ONLV
  worktree were 806 MB, and leaves the application entrypoint's retained state.
  A recorded compile is also rejected when its embed configuration names a
  file its capture does not, closing a transient embedded file (a shared
  recipe-loader check the single application model still relies on). The
  ONLV session record kept service processes under the key the agent derives
  from `service:<name>`, so a registration never replaced them and `scenery ps`
  showed replaced instances; keys are now `service-<label>`. ONLV stock-only
  journey (disposable worktree, framework from this checkout): `run.ready`
  after 18-21 s; seven `ahjs` handler edits took 1,980-2,080 ms after the first
  (median 2,038 ms) with 576-585 ms entrypoint builds, 66 % mean process-tree
  CPU and 2.65 GB mean RSS; a body edit replaced only `ahjs_ahjs`; an edit of
  `solar/tariffs/statecodes`, which only `tariffs_tariffs` imports according to
  `go list -deps`, replaced only that service; a failing edit kept `ahjs`
  serving from the same process and restoring the source kept it; the session
  record then named exactly the 47 live service processes; after
  `scenery down` none of the session's 54 recorded or child processes lived;
  and the log held no recipe recording or retained entrypoint link.
- [x] (2026-09-16) The process model is the default `scenery up` runtime and
  its selector value `application` (since removed) is deprecated (warning on start).
  An application with event consumers or emissions, or without a native
  service, failed before its build with guidance to select `application`
  (both are supported since the conformance entry below).
  Running the `scenery up` probes on the new default exposed that the Go
  command reports package directories through its resolved working directory:
  in a workspace under a symbolic link (Darwin's `/tmp` and `/var`) process
  entrypoints were not recognized and every build failed with "build inputs do
  not include development process host"; entrypoints are now recognized under
  both forms. `parallel-runtime`, `storage`, `capability-authority`,
  `dev-follower`, `process-model` and `desktop` pass on the default; the
  `dev-process` detached startup journey, `native-contract` and the native
  build driver benchmark assert single-executable behavior and select
  `application` explicitly (tracked in `docs/tech-debt.md`).
- [x] (2026-09-16) The `worktree` probe (A1-A17) passes on the default process
  model in 232 s (574 s in the single application model). It first failed A1
  on stale `testdata/apps/worktree-postgres` clients: `fad0aefb` removed
  library schemas and diagnostics from the specification catalog, which moved
  the specification and therefore every contract revision, and regenerated
  only the `native` and `house` clients; `fad0aefb^` generates the committed
  `77e995d7` revision and `fad0aefb` generates `e23917c6`. It then failed A5:
  after the worktree PostgreSQL container restarted on a new port the
  supervisor requested a rebuild, but no process identity changed, so service
  processes kept the old database endpoint. A generation now records the
  identity of the environment its processes start with, and a changed
  environment starts a complete generation. A6 asserted a new application
  process after a source edit; it now accepts the replaced service process the
  process model publishes (`service-library-library`).
- [x] (2026-09-16) Default-path conformance after a review of `c45450f2`, each
  finding confirmed in code first. A reused service executable must still
  have the digest its link published (recorded beside it); a changed or
  unrecorded executable is linked again instead of being adopted by rehashing.
  The host forwards the exact raw query, because the reverse proxy drops
  query parameters it cannot parse and the contract decoder then saw
  different values than in the single application model (`q=alice;bob`
  vanished, and `broken=%ZZ` turned `q=a%20b` into `a+b`). A generation's
  environment identity is the effective environment, so a different winning
  duplicate replaces processes and an overridden value does not. Retiring old
  recipe state is retried until the removal succeeds. The process model now
  runs every application class: event consumers, emissions and schedules were
  already registered by each service's own adapter and started at activation,
  so only the supervisor's rejection is removed (no event bus implementation
  ships in this repository, in either model; `examples/webhook-inbox` uses no
  contract events, contrary to an earlier note), and an application without a
  native service runs a host alone that serves framework routes itself.
  A copy of `testdata/apps/basic` without its service module started through
  `scenery up` on the default: the host linked and published generation 1,
  `/__scenery/config` answered 200 and an unknown path 404, both naming the
  generation, and the session registered only `api`.
  Afterwards `process-model`, `dev-process` and `worktree` (A1-A17) passed, and
  the ONLV worktree on the default model reached `run.ready` in 18 s with
  warm `ahjs` edits of 1,917-2,110 ms (median 1,953 ms), 538-617 ms entrypoint
  builds and 67 % mean process-tree CPU.
- [x] (2026-09-16) Entry-point conformance after a review of `9bf19d28`, both
  findings confirmed in code first. A host without services rendered SQL and
  standard authentication only beside assistant registrations, so an
  application with authentication and no native service answered
  `/users/dev-bootstrap` with 404; such a host now renders the application
  entrypoint's SQL, authentication and observability configuration. The
  `capability-authority` probe starts a copy of `testdata/apps/basic` without
  its service module with standard authentication, bootstraps a stored user,
  reads `/auth/me` with and without its token (200 and 401) and asserts that no
  service process started; with the old condition restored the same journey
  fails with the 404. Event delivery attempts, scheduled runs and durable task
  attempts entered no generation, so a handler that called another service
  before and after a replacement reached both generations. Each attempt is now
  admitted through the host's private control listener to the newest published
  generation that includes its process and holds it while the attempt runs. A
  runtime test with a synchronous test bus pauses a handler between two internal
  calls, publishes a replacement, and proves both calls reach generation 1,
  retirement of generation 1 waits for the attempt, a later attempt reaches
  generation 2, and an instance no generation includes runs no attempt; without
  the pin the second call reached generation 2. A consumer whose bus no provider
  registered refuses activation with HTTP 503, and the supervisor reports the
  service `degraded` with `background work unavailable: capability_unavailable:
  ...` instead of repeating an activation that cannot succeed.
- [x] (2026-09-16) One development runtime. The single application
  development model, its selector, candidate restart path, retained compiler,
  `internal/nativebuilddriver`, the `internal native-build-toolexec` command
  and the `native-build-compiler`/`native-build-driver` benchmarks are removed;
  `build --development`, workers and binding CLI calls link one executable with
  stock Go through the shared executable cache, and each workspace's retained
  compiler state is removed on its next process build. Moving the pinned
  probes onto the process model found a regression the default switch had
  introduced: every public answer carried the identity of the service process
  that answered, which differs from the development target's runtime bundle,
  and its PID, which is not the session's `app_pid`, so a check that binds
  answers to `build --development --verify-generation` (ONLV acceptance) or to
  `build/runtime/development.json` could never pass, and `scenery up` wrote no
  runtime bundle at all. A process build now computes the development target's
  build identity, writes its runtime bundle once the implementation check
  passed, and publishes that identity with every generation; the host stamps
  public answers with it and its own PID and names the answering instance in
  `X-Scenery-Service-Process-ID` and `X-Scenery-Service-Implementation-Revision`.
  A build that changes no process but the build identity publishes the same
  instances as a new generation. `native-contract` now links through
  `scenery up`, checks the application, service and host entrypoints, calls the
  grouped routes and runs the generated TypeScript client against the session,
  requires both answers to attest the runtime bundle, and requires public
  restarts to reuse every process executable; `process-model` also requires
  each answer to attest the host and the generation's build. `dev-process`
  identifies replacements by the attested build rather than a changed PID,
  keeps a rejected preflight and a failing replacement constructor on the
  published generation, and ends by requiring the served build to equal the
  `build --development --verify-generation` candidate. Its fixture no longer
  takes an exclusive lock in the service constructor: serving instances of the
  process model overlap while pinned requests finish, and background work,
  which does not overlap, is covered by `process-model`. A contract edit
  replaces the host too, so the journey follows the session's current host
  and asserts an unchanged host only where no replacement is expected.
- [x] (2026-09-17) Warm preparation reduction on ONLV. A traced body edit in the
  disposable ONLV worktree (stock Go, commit `a9dfe364` plus instrumentation,
  Apple M2 Ultra, detached `scenery up`, 47 service processes) spent about
  305 ms from save to captured snapshot, 1,596 ms from build request to
  published generation and 45 ms to the first verified response. New
  `watch.scan`, `process.plan`, `workspace.verify` and `supervisor.publish`
  steps attributed the untraced gaps. The largest avoidable cost was the
  whole-tree snapshot scan, run by the watcher and again as
  `supervisor.snapshot_verify` (about 200 ms each) and repeated inside
  generated-path discovery: 93 ms of each 155 ms scan matched every file against
  every gitignore rule and every parent segment (4,541 checks, 91 root rules),
  and most of the rest listed 857 unchanged directories. A walk now evaluates
  gitignore rules against each entry once, with its parents' contribution
  derived per directory (identical decisions, proven against the full-path
  matcher), and `internal/dirlisting` reuses a directory listing while the
  directory's identity, modification time and size are unchanged and it had
  settled for two seconds. The scan took 42 ms in isolation. Against the same
  producer without the change, same protocol and non-repeating behavior
  changes: body edits across `ahjs`, `tariffs` and `incentives` (24 each)
  p50 1,940 to 1,742 ms, p95 2,442 to 1,837 ms; 20-edit churn p50 1,938 to
  1,733 ms, p95 2,001 to 1,898 ms; `pkg/appfs` edits replacing five services
  p50 2,825 to 2,559 ms; contract edits with `scenery generate` p50 23.4 to
  22.1 s; a failing edit reported its failure after about 1.8 s instead of
  2.0-2.4 s. In the session `watch.scan` fell from 210 to 92 ms,
  `supervisor.snapshot_verify` from 202 to 87 ms and `workspace.cache` from 142
  to 82 ms; 52 of 64 scans read no directory and hashed one file. A body edit
  now waits mostly on `go build` (about 535 ms), preparation before it (about
  495 ms, of which process identity 103 ms and input fingerprint 74 ms), the
  service preflight (about 350 ms, first execution) and the save-to-capture
  settle.
- [x] (2026-09-17) Listing reuse made sound and host-local attestation made
  exact (review of `87c58551`). A listing keyed by modification time missed a
  rename whose directory time was restored, and a reused entry answered `Info`
  from the first read. A listing now keeps entry names and types only, is
  reused only while device, inode, size, modification time and status-change
  time equal those observed before and after its read, entry metadata is always
  read from the entry, each walker owns a bounded tree whose complete walks
  evict unvisited directories, a watcher error discards it, and the
  pre-activation snapshot verification reads every directory again. Tests
  compare reused scans with independent `os.ReadDir` and `filepath.WalkDir`
  results after restored times (rename, new `package.scn`, generated
  descriptor, removed and replaced entries). A host-local answer now holds the
  generation it attests, and a tool call of its conversation made meanwhile
  runs in that generation. Three variants in the disposable ONLV worktree
  (same protocol, 24 body edits, 20 churn, 4 shared, 3 contract, 3 failing;
  p50/p95 ms): old matching with fresh reads, new matching with fresh reads,
  new matching with the fixed listing reuse. Body edit to response
  1,928/2,032, 1,878/1,963, 1,736/2,092; churn 1,926/1,982, 1,871/2,051,
  1,749/1,810; shared 2,904/2,950, 2,724/3,605, 2,620/2,639; `watch.scan`
  216, 140, 90; `supervisor.snapshot_verify` 216, 134, 134 (it reads every
  directory in the fixed variant, 47 ms more than the reuse it replaced);
  `workspace.cache` 156, 149, 84; preparation before `go build` 562, 571, 497;
  failing edit to reported failure 2,207, 2,057, 1,808. Contract edits took
  23.5, 23.2 and 25.1 s; the difference is `runtime.activation` (10.1-10.7 s
  against 11.3-12.5 s for 48 process starts), which does not scan, while every
  scan step was lowest in the fixed variant.
- [x] (2026-09-17) Preparation before `go build` reduced on ONLV. A CPU profile
  of the supervisor over 21 body edits showed the implementation revision
  projection canonically encoded once per process (48 times) plus once more
  for the target, the generated adapter digest computed twice per build, the
  framework source read in full by `framework.verify` and again by
  `go.input_fingerprint`, and the runtime integration plan indexing every
  resource three times per service. A batch now encodes the projection once
  and hashes each digest in place of a placeholder (proven equal to the
  independent computation), the target and process revisions are one batch,
  the adapter digest is retained by contract revision, the build input
  manifest binds the framework source its build request just verified, and
  the plan indexes resources once. Same protocol against the fixed listing
  reuse (p50/p95 ms): body edit to response 1,736/2,092 to 1,598/1,720; churn
  1,749/1,810 to 1,568/1,700; shared 2,620/2,639 to 2,416/2,490; preparation
  before `go build` 497 to 295; `process.identity` 101 plus a 36 ms untraced
  target revision to 8; `go.input_fingerprint` 70 to 22; `process.plan` 66 to
  26. The remaining preparation is `framework.verify` (48 ms, a fresh content
  read before each build by contract), `workspace.cache` (86 ms spread over
  inventory, dependency, projection and generated-path checks) and the
  synchronous status and compile-start notifications (34 ms).
- [x] (2026-09-17) Contract edits relink only what their contract reaches. A
  service adapter records a service contract revision (its module instance,
  that instance's resources and every other resource it covers) instead of the
  application's, the composition records the revision each registration must
  carry, a service process's linked identity is its service contract revision
  with a service-process implementation revision, and the host keeps the
  application's. Service-side MCP tool registrations no longer record the
  application revision: the assistant gateway already admits a call only with
  the assistant's capability revision, which is now the projection of the
  assistant's contract and its server's capabilities. Retained executables of
  an unchanged digest are verified instead of copied again, and a runtime
  preflight an unchanged retained executable passed with the same identity
  and environment is not repeated when a complete generation restarts it. On
  ONLV (same protocol; contract edits change one execution timeout and run
  `scenery generate`): contract edit to response 25,144 to 5,690 ms p50, build
  request 24,785 to 3,960 ms, relinked entrypoints 48 to 2 (host and the edited
  service), `go build` 8.8 to 0.6 s, runtime activation 12.5 to 1.2 s, assistant
  stages from two 5.8 s preparations to hits; body edits 1,736/2,092 to
  1,548/1,675 ms, churn 1,749/1,810 to 1,542/1,626 ms, shared 2,620/2,639 to
  2,361/2,406 ms, failing edit to reported failure 1,808 to 1,736 ms. Assistant
  helpers stayed ready with matching expected and actual capability revisions
  and `scenery doctor` reported them matching.
- [x] (2026-09-17) Remaining preparation reduced. `framework.verify` read every
  framework Go file again for its embed directives (18 of 36 ms per call on
  the ONLV snapshot of 12,153 files); directives are now retained by a stamp
  that includes the status-change time. The private workspace was read and
  hashed in full three times per build (the cached-workspace fingerprint and
  both `workspace.verify` checks) and every Go file reparsed for imports; its
  fingerprint now hashes retained content digests and imports under the same
  stamp rule. Preparation of a captured snapshot rediscovered the managed
  generated paths from disk up to three times; the capture now records them.
  Tests prove a same-size edit behind a restored modification time still
  changes the embed set, the dependency fingerprint and the workspace
  fingerprint. On ONLV (p50 ms): `framework.verify` 50 to 17,
  `workspace.cache` 83 to 50, `workspace.verify` 43 to 12 before and 38 to 20
  after compilation, preparation before `go build` 309 to 219; body edits
  1,548 to 1,480, churn 1,542 to 1,472, failing edit to reported failure 1,736
  to 1,659. The workspace framework fingerprint keeps its persisted metadata
  cache (about 12 ms) because one-shot commands rely on it across processes.
- [x] (2026-09-17) A replaced host takes over unchanged service instances. Every
  host incarnation of a session now uses one link file and dispatch socket,
  which a service reads once at startup and a new host binds only after its
  predecessor exited; generation numbers continue across incarnations so a
  request or retirement addressed to the previous host never names a
  generation of the new one. A complete generation started by a contract or
  host change keeps every running instance whose linked identity is
  unchanged, when the processes' environment is unchanged, and starts only the
  others; a rollback restores the previous host with the same instances. The
  `process-model` probe now also changes greeter's contract alone and requires
  a new host and greeter while the same echo process answers through the new
  host. On ONLV a contract edit starts two processes (host and the edited
  service) instead of 48: runtime activation 1.2 s to 565 ms, build request
  4.0 to 3.4 s, edit to response 5.6 to 5.0 s p50; assistants stayed ready with
  matching capability revisions and body edits were unchanged (1,480 ms).
- [x] (2026-09-17) Host takeover preserves the authority of kept work, after an
  external review of the takeover. (1) The supervisor allocates generation
  numbers from a session counter that a rollback never lowers, so a
  publication whose answer and confirmation were both lost cannot have its
  number reused by the restored host for another implementation set. (2) A
  background attempt whose admission ends (host stopped or replaced, or forced
  retirement, which now also ends admissions) is interrupted with a cancelled
  context and fails as unavailable instead of continuing with a generation no
  host dispatches. (3) Durable receipt authorizations are journaled in the
  session's private host state directory, which every host incarnation
  replays, so status and cancellation keep working for the original principal
  after a host replacement. (4) An assistant tool call executes the generation
  of its run (started by create or turn, continued by approval, ended by an
  observed terminal event, the next run or forced retirement) instead of the
  oldest open gateway request; streams attest that generation, end cleanly when
  superseded, and a replacement host replays run scopes so a run started on an
  earlier host fails its tool calls rather than switching generation.
- [x] (2026-09-17) Authority of kept work is committed and scoped per run, after
  a second external review. (1) Assistant tool calls name their run: the signed
  MCP assertion carries `run_id`, which the generated Eve helper attributes
  from the starting request while it is in progress and otherwise from the
  session's latest started run; the host reserves a run before the helper sees
  its start, keeps pending/active/unknown/ended states per run, and a missing,
  ended or revoked run fails instead of executing the current generation. (2)
  Receipt and run journals are the commit point: a change is appended before
  the host acts on it, a failed append, rewrite or corrupt record poisons the
  journal for the session (marker plus removal), and only a torn final record is
  discarded. (3) Run ends are journaled, the host observes terminal events from
  the helper's private stream itself (woken by approval and cancellation), and
  the run limit only forgets runs that can no longer execute. (4) A same-host
  publication whose outcome is unknown keeps its candidates, republishes the
  known services under a new number and only then retires the unconfirmed
  generation; the `process-model` probe injects the lost answers through new
  generation-control faults and passed.
- [x] (2026-09-17) Run identity is proven through the real Eve helper, after a
  third external review. The helper no longer attributes a call to the
  session's latest run: Eve's header context names the executing turn, and the
  helper binds turns to runs only by stream evidence (the run's message, or the
  continuation of its resolved approval), runs a conversation's runs one at a
  time, publishes a run's terminal event when it closes rather than when its
  turn parks for approval, and owns one provider cursor and normalized history
  per session. An unknown start that stays unobserved is revoked with a
  journaled tombstone. The supervisor starts a new host state epoch whenever the
  previous host's authority is uncertain, which keeps a poisoned host whose
  marker and removal both failed from restoring authority. New probes:
  `assistant-helper` (generated helper under Node against simulated Eve) and
  `assistant-journey` (testdata/assistant with real Eve and its mock model in a
  disposable copy with its own agent home); the assistant acceptance script now
  runs only in such a copy with the verifier's binary.
- [x] (2026-09-17) Accepted cancellations, revocations and host handoffs have an
  observable transition, after a fourth external review. A run the provider
  accepted but has not started is cancelled when its turn appears and refuses
  tool calls from the accepted cancellation on, and a run the provider never
  starts ends at the next session boundary. The unknown-start bound now governs
  the event read in progress and the observer records a run's events as they
  arrive. A client stream that is cancelled stops the reads made for it. A host
  the supervisor replaces quiesces before it stops, so the host state it reports
  is final; its epoch is reused only on that report, and a replaced epoch is
  removed only after the replacement commits. The assistant asset overlays and
  capsules are staged beside the prepared Go workspace instead of inside it,
  which lets the production artifact build finish and keeps workspace membership
  verification strict.

## Surprises & Discoveries

- Observed with the real Eve 0.39.1 helper and mock model: a turn that requests
  approval emits `turn.completed` and `session.waiting` immediately; the
  approval's `input.resolved` carries the original turn ID and the approved call
  runs there; the model's continuation starts a new turn without
  `message.received`; and an approval answered while a later turn is active is
  executed inside that later turn. The earlier helper published `run.completed`
  at the park, which would have ended the run on the host before its approval.
- The assistant acceptance script ran product commands in the authored fixture,
  used the machine's real agent home, and deleted generated caches of the
  authored root during its production step; its `go build` binary also lacks a
  content-bound framework producer. Run in a disposable copy with its own agent
  home, it no longer reaches the retained-database claim and every development
  case passes. Its production artifact case now builds, extracts its embedded
  Node and capsule and serves HTTP without ambient Node (the packaging fix
  above), but the embedded helper never reports ready: the extracted Node
  process runs while `conversation.create` answers `unavailable` for the full
  180-second bound.
- The production assistant's unavailability was one unresolved reference. The
  artifact build passed the assistant's declared `mcp_server` reference to the
  MCP projection unresolved (`mcp_server.support`), while generation passes the
  canonical address (`app/mcp_server/support`). The server address is part of
  the capability revision and selects the projected capabilities, so the capsule
  and its asset descriptor carried a revision computed over an empty capability
  set, and the generated application carried another. Startup therefore
  succeeded, because the helper's health and info answers are compared with the
  descriptor the same build wrote; every conversation request then failed inside
  the client, which refused its own outgoing request: the request carries the
  registered application's revisions and the client was configured from the
  descriptor. One resolution now serves generation, the development runtime and
  the build (`mcpprojection.AssistantServerAddress`), the helper implementation
  revision has one answer as well (`compiler.AssistantRuntimeRevision`), and a
  descriptor that disagrees with its registration is refused at startup as
  `revision_mismatch` instead of being installed ready.
- The private startup report of a production runtime named that failure. Without
  it the helper's own output was discarded and the public answer stayed
  neutral, so the only evidence was a timed-out wait.
- A working production assistant then failed its first MCP operation with a
  transport error. The provider's build snapshots a static connection's resolved
  URL into the compiled agent manifest, and its runtime reads that snapshot, not
  the module: the same capsule started with a reachable gateway address never
  contacted it, while its build-time placeholder was refused. Eve 0.39.1 offered
  no runtime resolution, and its build rejects a `url` thunk outright. Eve 0.59.1
  resolves a whole connection at a session boundary through `defineDynamic`, so
  the generated connection is dynamic now and the compiled capsule carries no
  address at all. Two starts of identical capsule bytes reach their own gateways
  and nothing else.
- The development overlay cache existed only to rewrite that snapshot: it
  relocated a prepared build's roots and its connection URL. With a dynamic
  connection there is no URL to rewrite, so the relocation refused the build and
  every helper start failed before the provider ran. The cache now rewrites roots
  only and requires the assistant's connection to be the dynamic one; a prepared
  output is therefore independent of the address that prepared it.
- A production binary stopped while its helper was connected left the helper
  running. The helper, a client of the assistant's MCP gateway, stops after the
  gateway, and Eve 0.59.1 keeps a streaming connection open that never becomes
  idle, so the gateway's graceful shutdown waited out the whole five-second
  service budget; the shutdown loop then stopped at the expired deadline and
  never asked the production runtime to stop its helper. The acceptance had
  reported a clean shutdown because it asked for children of the stopped binary,
  which a reparented orphan no longer is. The gateway now closes connections
  that are still open after a short grace, every service is asked to stop even
  after the deadline passed, and the acceptance identifies the owned processes
  by PID and start time before the binary stops and requires each one gone.
- A development helper that could not be prepared was retried with its cause
  lost in three places: the stage held the error, the step event published only
  `ok: false`, and the provider's output was discarded with the overlay it was
  written into. A failed step now names its cause in its event and keeps a
  private record, with the provider's bounded and redacted output tail, beside
  the removed overlay.
- `scripts/accept-assistant-runtime.sh`, the real Eve journey (mock model,
  approvals, durable receipt/status/cancel through `scenery up`), is blocked
  independently of this work: its own `go build` binary has no content-bound
  framework producer, and with the verifier's binary the retained PostgreSQL of
  `testdata/assistant` refuses startup because the checkout's execution state
  may refer to pre-cutover data. The data was not adopted or reset.
- A contract edit in ONLV replaces every one of its 48 processes as a complete
  generation (about 22 s including `scenery generate`: 8.5 s of stock links
  and 48 concurrent preflights), because the contract revision is part of every
  process identity. ONLV's TypeScript clients are materialized into source, so
  a contract edit without `scenery generate` fails its build with SCN6204 and
  keeps the published generation serving.
- The snapshot scan's cost was CPU in gitignore matching, not filesystem access:
  each file was matched against every rule at every parent segment.

- An application root spelled through a symbolic link (macOS `/tmp`) gives
  `scenery up`, which works on the canonical root, and `scenery build`, which
  keeps the spelling, different private workspaces. With a framework source
  selection the absolute replacement path in each workspace `go.mod` differs,
  so the two builds report different build-input digests for the same source.
  The `dev-process` probe builds its candidate through the canonical root; the
  command-wide fix is tracked separately.
- The first run of the consolidation probes filled the disk: failed probes keep
  their temporary roots for inspection, and the removed retained compiler had
  left 7.1 GB of per-workspace state in the development cache. Both were
  removed, and the next process build of each workspace removes its own
  retained compiler state.

- The implementation check runs beside the entrypoint build, so a faster build
  alone does not shorten an edit: the check (843 ms, of which about 290 ms
  verifies generated artifact staleness and 560 ms analyzes Go targets) ends
  after the 552 ms build. A `go list -export -deps` of the whole ONLV
  application costs 591 ms warm against 258 ms for one service package.
- Rescanning ONLV's watched files cost about 170 ms, half of it looking up
  `.gitignore` in each of 851 directories although the walk had already read
  every directory listing.
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

- A three-generation, two-service sequence (a request pinned to generation 1,
  greeter replaced in 2, echo replaced in 3) stopped the first echo when
  generation 2 retired, although generation 1 still named it: the supervisor
  stopped the instances replaced by a transition, and the host only checked
  work pinned to the generation being retired.
- The generation a host selected for an MCP tool call did not reach the service
  process, so a nested internal call after a publication crossed into the new
  generation. Host-local application endpoints are served before ingress
  pinning; each forwarded tool call is now pinned individually, and a whole
  assistant conversation is deliberately not pinned.
- Durable status and cancellation resolved the accepting service name in the
  current generation, whose instance had none of the accepting instance's
  in-memory receipt records and answered `not_found` while the accepting
  instance was still alive.
- The previous process wiring (one `process-link.json` and `d.sock` per session)
  could not let a candidate host start while the previous host still owned the
  dispatch socket; per-incarnation link files remove that constraint.
- No public input of the multiservice fixture makes a candidate host fail after
  its services started, so the restore ordering is proven with injected steps
  in `TestDevProcessReplacementRestoresThePreviousHostOnlyAfterCandidatesStop`,
  and the probe proves the realistic case: a contract-changing generation whose
  service constructor fails never stops the previous host.
- A single `-ldflags` token was filtered from the shared build arguments without
  its value, so `-ldflags -s=false` would have passed `-s=false` to `go build`.
- Reverting a contract-changing `.scn` edit activated the reverted contract in
  both development models: the generated `scenerycontract` projections kept the
  added field and the next builds compiled it, even after the source settled.
  This predates Plan 0200 and is tracked as a separate task; the probe commits
  its contract change instead of reverting it.
- `httputil.ReverseProxy` copies both directions of an upgraded connection
  until both end, so a host generation stays in flight until the client closes
  its side as well.

- The recorded recipe of a process entrypoint failed to load for two reasons
  that only appear away from the application entrypoint: the pattern is an
  import path, not a directory, so the recorded `main` compile matched no
  package; and the per-process `-ldflags` carry the linked identity, which
  changes with every edit, so a recipe that compared them was never eligible.
  Both are now explicit: a capture records the import path its pattern resolved
  to, and only stable build configuration decides eligibility.
- A background recording that discards its error is invisible. The first
  attempts produced nothing for twenty minutes without a single log line;
  `build.recipe_capture` now reports every recording with its reason, which
  found both failures in one run.
- Recording archives inside the recording directory cost 471-477 MB per
  entrypoint, because each recipe kept its own copy of the same closure and
  kept both the recorded and the finalized archive. Moving bootstrap state into
  the content-addressed store made a second entrypoint adopt what the first
  recorded.
- The ONLV "Starting prepared assistant runtimes" stall has a deterministic
  cause. A complete activation held `model.mu` while `StartPrepared` waited for
  helper starts; a started helper reported its PID, which registered the agent
  session, whose process list read the service-process status under
  `model.mu`. Status now comes from a published snapshot instead.
- A background recording captured its input state after the stock build, so an
  edit during the 20-25 s recording could pair archives compiled from one source
  with the identity of the next. Its incompatible recipe also could never be
  replaced: only the in-memory entry was dropped, and the recording returned as
  soon as the stale `current.json` existed. Shared-store collection ran once
  per process, so every advanced recipe's superseded archives stayed for the
  whole session.

- Restoring a directory's modification time after a rename (as `touch -m -d`,
  `rsync -t` and some editors' atomic saves do) leaves size and modification
  time equal, so a listing keyed by them reused the old membership. The
  status-change time moves with any such restore and cannot be set by a
  caller, which is why it is part of the stamp.

- After service identities stopped relinking unaffected services, a contract
  edit still took 11.7 s: every retained executable was copied and fsynced
  again (8.5 s) because a retained target was checked only after the copy, 49
  preflights ran concurrently at about 375 ms each, and both assistants
  reinstalled dependencies and rebuilt their helpers (5.8 s) because an
  assistant's definition identity carried the application contract revision.
- Twelve ONLV service adapters embedded the application contract revision a
  second time, as the capability revision of their MCP tool registrations, so
  a naive service contract revision would have made every assistant tool call
  fail as stale; the service-side check it fed duplicated the gateway's.
- `testdata/assistant` tracks generated Go projections from an earlier
  specification revision; regenerating it rewrites its contract revision and
  removes those files, so it was left unchanged.

## Decision Log

- Decision: a host hands its authority over by quiescing (refusing further
  authority changes and reporting a final host state) before it stops, rather
  than by a status sample taken before the replacement begins. Rationale: the
  earlier sample could go stale while the old host kept serving, and the epoch
  decision must rest on a state that can no longer change. Date: 2026-09-17.
  Author: Claude.
- Decision: assistant asset overlays and capsules are build scratch beside the
  prepared workspace, not files inside it. Rationale: the production artifact
  build wrote an Eve cache file into the verified input tree, which the
  membership check correctly rejected; relaxing that check would weaken the
  input guarantee for every build. Date: 2026-09-17. Author: Claude.
- Decision: the generated helper runs one run of a conversation at a time and
  binds provider turns to runs only by stream evidence (superseding the
  latest-started-run attribution). Rationale: with the real Eve 0.39 runtime a
  turn that requests approval ends at once, the approved call runs in the
  original turn, the continuation arrives as a new turn without a received
  message, and an approval answered while another turn is active is folded into
  that turn; only serialized runs let every call and event keep the run that
  caused it. Eve's header context names the executing turn, so no per-call
  inference is needed. Date: 2026-09-17. Author: Claude.
- Decision: uncertain host authority rotates the session's host state epoch
  instead of trusting a poison marker. Rationale: the marker and the journal's
  removal are further writes to the storage that just failed; an empty epoch is
  fail-closed because a missing record never authorizes anything. Date:
  2026-09-17. Author: Claude.
- Decision: an assistant run, not its conversation, selects a tool call's
  generation, and the helper names the run in its MCP assertion. Rationale: a
  conversation-wide slot let an unaccepted or rejected turn redirect a running
  run's calls. Eve exposes no per-action run identity, so the helper attributes
  a call to the run whose start request is in progress, else to the session's
  latest started run; this relies on an Eve session running its turns one at a
  time. Date: 2026-09-17. Author: Claude.
- Decision: a journal that cannot commit or prove a record poisons host state
  for the session rather than replaying a valid prefix, and an unknown
  publication outcome is resolved by republishing the known services rather
  than adopting the candidates. Rationale: a lost ambiguity or run record must
  never restore an authorization or unscoped execution; adopting candidates
  would need the post-publication drain and activation under uncertainty,
  while republishing reaches a known host state and the next edit retries.
  Date: 2026-09-17. Author: Claude.
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
- Decision (superseded 2026-09-16: the process model is the default): until
  Milestone 4 accepts ONLV, the development supervisor selects
  the process model only when its selector (since removed) is set to `service`;
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
- Decision: service-instance lifetime is reference based. The supervisor keeps
  the instances of every generation the host still retains; retiring a
  generation releases its references and stops only instances that neither the
  current services nor another retained generation name. A replaced generation
  retires when the host reports no pinned work, or after 30 s by forced
  retirement: the host removes the manifest, dispatch pinned to it answers
  `unavailable` `not_sent`, and work already forwarded to a stopped instance
  ends with its connection. Rationale: pinning is only a guarantee while every
  instance a pinned generation names stays alive, and development sessions need
  a bound for long-lived streams pinned to old generations. This refines the
  earlier generation-aware dispatch decision. Date: 2026-09-16. Author: Claude,
  after the human-shared review.
- Decision: one runtime-owned generation context covers every entry point that
  forwards into a service process: ingress requests and host-originated MCP
  tool calls carry the generation the host selected, the service runtime moves it
  into request state, and descendant internal calls keep it. Host-local
  application endpoints (assistant conversations) are not pinned; each tool
  call they make is pinned individually. Work started outside a forwarded call
  (schedules, durable workers) stays unpinned and dispatches to the current
  generation. Date: 2026-09-16. Author: Claude.
- Decision: the host authorizes durable MCP receipts. A service process that
  accepts a durable receipt returns its durable service and task with the
  outcome; the host records principal, execution ID, owning process, service and
  task (4,096 records, oldest forgotten first, an execution ID accepted by two
  owners for one principal fails closed) and sends authorized status and
  cancellation to the owning process of the current generation, which reads the
  shared durable store without its own receipt records. Process-local receipt
  records in one application process use the same bound. Rationale: durable
  work outlives implementation instances; the host outlives service
  replacements, and the durable store is the state authority. This replaces
  routing status to "the process that accepted the receipt". Date: 2026-09-16.
  Author: Claude, after the human-shared review.
- Decision: readiness and background authority are separate. A runtime with
  `SCENERY_PROCESS_LINK` opens durable stores and serves requests but starts
  schedules, event consumers and durable acquisition, schedule and retention
  loops only on `POST /__scenery/process/v1/activate`; `/drain` revokes them
  immediately and for the rest of the process life (it waits at most 100 ms for
  running work and answers 202 when work is still stopping). Instance states are
  prepared (preflight), ready (serving, inactive), activated, draining and
  retired. The supervisor activates a generation's new instances only after
  publication and after draining the instances it replaced; an instance that
  does not confirm drain is stopped first. Application constructors and `init`
  still run at candidate startup, so their side effects are not gated. This
  replaces draining replaced instances as the only barrier. Date: 2026-09-16.
  Author: Claude, after the human-shared review.
- Decision: a complete replacement (changed contract or host identity) starts
  the candidate services with a new host incarnation link while the previous
  generation serves, stops the previous host only when they are ready (the
  candidate host needs the application listener and assistant descriptors), and
  commits only after the candidate host accepted its generation: previous
  instances then stop before the new ones are activated. A failed candidate host
  is stopped and the previous host restarts from its retained executable and
  environment with the previous services republished as generation 1; candidate
  shutdown that cannot be confirmed refuses the restore, as for one application
  process. Date: 2026-09-16. Author: Claude, after the human-shared review.
- Decision: per-service routes come from the same generator data that renders
  endpoint registrations (`runtimeBindingPath`, `renderContractPathTail`) and are
  checked against rendered adapter sources in tests. Rationale: a route table
  generated separately from registrations would drift. Date: 2026-09-15.
  Author: Claude.

- Decision: a development session records an entrypoint recipe only after it
  has linked that entrypoint by stock Go more than once, and records one
  entrypoint at a time. Rationale: an application of fifty services would
  otherwise answer its first edit by rebuilding fifty complete closures, and
  a developer works on a few services at a time. Date: 2026-09-16. Author:
  Claude.
- Decision: recorded archives, support inputs and source snapshots move into one
  content-addressed store per workspace, and a recording directory is deleted
  once its recipe is published. Rationale: entrypoints of one application
  compile nearly the same packages; separate stores retained each closure again.
  A snapshot is copied rather than moved, because a capture may name the
  developer's own workspace file. Date: 2026-09-16. Author: Claude.
- Decision: the process model is the default development runtime and the
  single application model is deprecated. The model selector (since removed) defaults
  to `service`; `application` remains selectable with a deprecation warning,
  and an application the process model could not run yet failed with guidance
  rather than falling back automatically. Remaining gaps and the removal path
  are tracked in `docs/tech-debt.md`. This supersedes the rollout-gate decision
  of 2026-09-15. Date: 2026-09-16. Author: human.
- Decision: NO-GO for automatic per-service recipe recording under the
  measured workload. Service entrypoints are linked by stock Go only; the
  per-service retained executor, its recording, admission, yielding, shared
  store collection and background-work owner are removed rather than left
  dormant, and recipes earlier sessions recorded are deleted on a workspace's
  first process build so behavior never depends on historical cache contents.
  Rationale: on ONLV a ready recipe changed the warm edit median by 12 ms while
  a running recording added about 400 ms at twice the CPU. This is not a
  judgment on the multi-process model or on the single-application retained
  compiler, which stays, nor on direct compiler invocation in general. Date:
  2026-09-16. Author: human.
- Decision (superseded by the NO-GO above): keep the retained entrypoint path
  and make it cheaper rather than
  remove it. Rationale (human choice after the ONLV measurement): its compile
  and link are below the stock build and its overhead was measurable validation
  work. After concurrent input capture it is roughly at parity (532-583 ms
  against 528-705 ms stock), so it must not be treated as a latency win until a
  closure where stock package loading dominates shows one. Date: 2026-09-16.
  Author: human and Claude.
- Decision: a recording's machine-wide admission reuses the host-wide link
  queue and a lock file beside its slots instead of a memory reservation.
  Rationale: the queue already orders every worktree's foreground links, and a
  reservation would need platform memory accounting the Go standard library
  does not provide; the bounded `-p` limits what one recording can hold. Date:
  2026-09-16. Author: Claude.
- Decision: a background recording binds to the input revision it captures
  before running tools and validates it afterwards, instead of holding the
  workspace lock or materializing a separate build view. Rationale: holding the
  lock would stall edits for the whole recording, a copied view would change
  the recorded source paths recipes rebind, and the stamp validation plus a
  per-action digest comparison already reject every revision change. Date:
  2026-09-16. Author: Claude.
- Decision: an unconfirmed activation keeps its instance serving, reports it
  degraded, and is reconciled while holding `model.mu`, rather than failing the
  generation. Rationale: HTTP availability and background work are separate
  capabilities, and holding the lock for each attempt guarantees that a drain
  can never be followed by a late activation. Date: 2026-09-16. Author: Claude.
- Decision: a background attempt is admitted to a generation when the attempt
  starts, to the newest published generation that includes the process running
  it, and the admission is one open request to the host rather than an acquire
  and release pair. Rationale: work created long before (a queued event or
  durable job) must not run against the generation that created it, the
  running process's own generation is the only one whose instances its code
  was built against, and a connection that ends with the attempt or its
  process cannot leak a hold that blocks retirement. Date: 2026-09-16. Author:
  Claude.
- Decision: an activation refused for a missing capability is reported as
  unavailable background work and not repeated. Rationale: a capability such
  as an event bus is registered by the build itself, so repeating the
  activation cannot succeed until a new build replaces the process, and an
  "unconfirmed" reason would misdescribe a definite refusal. Date: 2026-09-16.
  Author: Claude.
- Decision: remove the single application development model instead of
  keeping it deprecated. Rationale (human choice after a review of `9bf19d28`):
  its three remaining consumers were verification, every lifecycle change had
  to be implemented twice, and the retained compiler it alone used was at parity
  with stock Go. Production keeps one executable. Date: 2026-09-16. Author:
  human.
- Decision: reduce the repeated whole-tree scan before any identity caching.
  Rationale: the trace showed it as the largest avoidable cost on the critical
  path, it ran three times per edit and grew with the application tree rather
  than with the change; the reduction keeps every freshness check (each file's
  own metadata is still read, and a directory listing is reused only on an
  unchanged, settled directory stamp). Date: 2026-09-17. Author: Claude.
- Decision: a process host attests on public answers the build identity of the
  serving generation and its own PID, not the answering service instance's
  identity. Rationale: the response identity headers mean "the linked runtime
  bundle that served this request" for consumers that compare answers with the
  development target's verified candidate; the host verified the instance
  belongs to the generation and every instance of a generation has its process
  identity from that build, so the claim is exact, and existing consumers such
  as ONLV acceptance need no change. The instance stays observable in separate
  headers. Date: 2026-09-16. Author: Claude.
- Decision: verify the snapshot before activation with a walk that reads every
  directory, and keep listing reuse for the watcher's capture scans.
  Rationale: the verification is the independent observation that a published
  generation's sources are the captured ones, so it must not share the
  capture's retained state; it costs about 45 ms per build on ONLV while the
  capture keeps its reduction. Date: 2026-09-17. Author: Claude.
- Decision: a host-local request holds its generation for its lifetime, and an
  assistant tool call runs in the generation of the oldest in-flight gateway
  request (create, turn, approval or run event stream) of its conversation,
  else in the current generation. Rationale: the answer then attests the
  build its tool calls executed, the hold is bounded by one request (an event
  stream ends when its run waits or ends) and by forced retirement, and a
  conversation is never pinned between requests; per-tool identity reporting
  would need a public protocol change for the same guarantee. Date:
  2026-09-17. Author: Claude.
- Decision: the generation of an assistant tool call is selected by its run
  scope, not by in-flight gateway requests (superseding the decision above).
  Rationale: an event stream observes a conversation, and a second tab or a
  reconnect must not choose what a tool call executes; the oldest-request rule
  let overlapping streams attest different generations and left a forced
  retirement pinning a deleted generation. The run is the bounded operation
  whose behavior is attested. The helper's MCP assertion carries no run ID, so
  the scope is per conversation, and a run ends for the host only when a
  stream observes its terminal event: a client that never streams keeps its
  run's generation until the next run or forced retirement. Date: 2026-09-17.
  Author: Claude.
- Decision: state that must outlive one host incarnation (durable receipt
  authorizations, assistant run scopes) is journaled in a session-private host
  state directory named by the process link, and a background attempt that
  loses its admission is interrupted rather than handed over. Rationale: the
  host is replaceable while service processes and assistant helpers are kept;
  bounded append-only journals with compaction give the session owner's
  lifetime without a new daemon, and handing a running attempt to a newer
  generation would recreate the cross-generation execution admissions exist
  to prevent. Date: 2026-09-17. Author: Claude.
- Decision: a service contract revision projects the service's module
  instance, that instance's resources and the other resources its adapter
  covers. Rationale: those are the contract resources the adapter registers;
  generated inputs outside them (another module's client, a shared type) change
  the process's build-input digest and so its identity, and the registration
  check stays exact because the composition generated in the same artifact set
  records each adapter's expected revision. Date: 2026-09-17. Author: Claude.
- Decision: an assistant's capability revision is the projection of its own
  contract and its MCP server's capabilities and connections, and generated
  tool registrations carry no revision. Rationale: the gateway is the single
  admission point for tool calls and already requires the assistant's
  capability revision; tying helpers to the whole application contract rebuilt
  both ONLV helpers on every unrelated contract edit. Date: 2026-09-17.
  Author: Claude.
- Decision: a replaced host takes over the running service instances whose
  identity is unchanged (superseding the earlier decision to restart them all).
  Rationale (human request after the contract identity work): a service reads
  its link once and admits background attempts by its identity, so one link
  path per session and generation numbers that continue across host
  incarnations let a new host serve an unchanged instance without restarting
  it; an attempt during the host swap fails as unavailable, as it would while
  its process restarted. Date: 2026-09-17. Author: human, Claude.

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
