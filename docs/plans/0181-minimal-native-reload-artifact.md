# Minimal Native Reload Artifact

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current. It follows
[PLANS.md](../../PLANS.md).

## Purpose / Big Picture

An ordinary implementation-only Go edit still rebuilds and restarts the full
Scenery application executable. On the final Plan 0180 comparison, the warm
edit loop measured p50 1,533.570 ms and p95 1,571.196 ms. Average Go build and
link time was 529.116 ms and average exact-executable preflight/first execution
was 501.526 ms. These two native artifact boundaries dominate the remaining
critical path.

This plan tests a materially different boundary: keep a stable Scenery host and
replace only a Go implementation island containing the changed application
implementation package, its real dependencies, minimal generated typed dispatch,
and the SDK/ABI leaves needed to invoke it. The first checkpoint is deliberately
experimental. It must use real ONLV source and decide whether a stock-Go island
can replace itself within 250 ms p50 before any production host integration is
attempted.

Plan 0180 remains active and unchanged. Its snapshot, SDK extraction, exact
artifact identity, atomic publication, worktree isolation, and retained-generation
results remain valid. Its 300/500 ms end-to-end acceptance target remains unmet.
This plan does not reinterpret that result as success.

## Progress

- [x] (2026-09-13) Confirm the starting branch
  `feat/incremental-worktree-native-loop` at commit
  `50d89dad1892e75fb5b505e1d4db51a64d4ac225`, with no pre-existing checkout
  changes.
- [x] (2026-09-13) Record Plan 0180's current 30-edit p50/p95, average native
  build/link and preflight/first-execution costs, and rejected 619/620-package
  worker result.
- [x] (2026-09-13) Define the private semantic execution ABI and the numerical
  promotion/rejection gate before choosing a wire transport.
- [x] (2026-09-13) Trace the rejected worker's complete real ONLV dependency closure and
  record the generated composition, runtime, application, and framework paths
  that made it nearly monolithic.
- [x] (2026-09-13) Select one representative real ONLV unary operation without changing its
  authored API or handler shape, then construct a task-owned implementation-island
  artifact that excludes global composition and unrelated implementations.
- [x] (2026-09-13) Measure package count, bytes, initialization, RSS, initial build,
  warm compile/link, launch to the initial handshake and typed response time.
  These first five samples did not prove full activation or artifact-owned
  execution identity. They failed the continuation gate; 30 edits were not run.
- [x] (2026-09-13) Record a NO-GO decision for a process-per-edit stock-Go
  implementation island. Do not begin production host integration
  until the developer has received this experimental checkpoint.
- [ ] If and only if the experimental gate passes and continuation is explicitly
  selected after the checkpoint, integrate the singular stable development host,
  exact generation switch, drain, retained rollback, database behavior, internal
  calls, streaming, and worktree isolation.
- [x] (2026-09-13) Run the exact cumulative experimental changed-area validation.
  Keep this plan open because the stock-Go feasibility gate failed and no
  production-equivalence lane was selected.
- [x] (2026-09-13, resumed) Replace the ad hoc runner with an explicit
  `native-reload` repository benchmark, retained source and bounded raw evidence.
- [x] (2026-09-13) Prove linked generation plus self-executable digest, current owner,
  contract/ABI and PID; reject wrong identity, invocation before activation,
  failed constructor, canceled candidate and predecessor-as-candidate.
- [x] (2026-09-13) Separate package initialization, attestation, activation/constructor and
  first typed response. Document the existing preflight/real-start ordering.
- [x] (2026-09-13) Rerun two warmups and the first five unique edits with the corrected proof,
  retaining all failures. Continue to 30 only under the existing numerical rule.
- [x] (2026-09-13) Validate the resumed implementation and update the checkpoint
  claims: affected packages, full Go suite, lint, quick/default verifier, the
  explicit real ONLV benchmark and eight exact-root timing confirmations passed.
  Default verification retained an advisory full-suite wall-time warning.

## Surprises & Discoveries

- Corrected checkpoint (2026-09-13): the reproducible benchmark confirms
  238 packages, now 8,166,370 bytes with independent identity/activation proof.
  Five unique edits measured build p50 518.559 ms, launch/attestation/ready p50
  401.598 ms, and build-to-new-typed-response p50 934.457 ms. The 325 ms
  continuation and 250 ms feasibility gates still fail. Negative identity,
  constructor, cancellation and stale-artifact cases passed. No host integration
  or alternative technology is authorized by this result.

- Review correction (2026-09-13): the first prototype copied
  `ExecutionGeneration` from the request, and its handshake preceded
  `ahjs.NewService`. Its response verified the changed literal but was not
  independent execution attestation. The original 928.710 ms sum excludes
  activation and response, and its 0.308 ms value is a typed round trip, not
  isolated transport overhead. Preserve those raw measurements as historical
  timing evidence with these limitations.

- Plan 0180's rejected worker imported the complete generated application
  composition. That composition registered every generated adapter and
  transitively every application implementation, while the adapters imported
  `scenery.sh/runtime`. The resulting worker retained 619 of the monolith's 620
  packages, remained 51,853,250 bytes versus 51,974,626 bytes, and took about
  1.36 s for a warm unique-main link after a 2.35 s first build. This is the
  rejected boundary to explain, not repeat.
- The Plan 0180 SDK split makes the root `scenery.sh` facade a 212-package leaf
  closure instead of the earlier 315-package runtime closure. Some adjacent
  application-facing packages may still import `scenery.sh/runtime`; the island
  experiment must report such imports rather than silently treating the root
  SDK result as universal.
- The current generated composition has 50 direct imports: 47 generated
  adapters, application assets, `scenery.sh`, and `scenery.sh/runtime`. Every
  generated adapter directly imports `scenery.sh/runtime` plus one authored
  application implementation package, so composition registers 47 authored
  implementation packages transitively. The composition closure is 619
  packages; the reproduced composition worker is 620 packages once its own
  main package is counted.
- The selected natural implementation package, `clean.tech/solar/ahjs`, has a
  238-package closure and produces an 8,060,898-byte executable without the
  global composition or `scenery.sh/runtime`. There are 383 monolith packages
  absent and one new island entrypoint: a net reduction of 382 packages.
  The absent set includes 213 ONLV, 108 external and 30 Scenery packages.
- The smaller closure did not reduce the supported stock-Go boundary. Across
  five unique handler edits, `go build` measured 531.280 ms p50 and a link-only
  `go build` measured 496.424 ms p50. The exact changed-package compiler and
  linker tools took 30.062 ms and 148.465 ms respectively in separate command
  replays. Those replays are not an additive attribution of the full build;
  package/action loading, cache materialization, external-link coordination and
  publication remain possible contributors, not independently measured causes.
- Each newly linked artifact required 397.648 ms p50 to launch and emit its
  initial handshake, while the same inode subsequently launched in
  24.992 ms p50. A new-path 2.41 MB diagnostic Go binary still needed
  191.865--211.706 ms on first execution. The AHJ init trace reached `main`
  after about 42 ms, including about 34 ms in `internal/spec`, so package
  initialization is measurable but does not explain most of the first-launch
  floor.
- `scenery build --target development` failed closed with SCN9000 report token
  `rpt_fhxthvr5u2txtemumx67gy2y3m` after preparing the exact private build
  workspace. The experiment retained that failure and used direct diagnostic
  builds from the prepared workspace; it does not treat the failed product
  build as a passing sample.

## Decision Log

- Decision: retain NO-GO after corrected independent attestation and activation.
  Rationale: 934.457 ms native replacement p50 exceeds the feasibility gate by
  684.457 ms, before full public-endpoint or production-contract work. The
  524.286 ms average supported build remains comparable to Plan 0180's
  historical 529.116 ms average; this is not an interleaved improvement claim.
  Date: 2026-09-13. Author: Codex.

- Decision: finish the missing checkpoint proof within the existing repository
  verifier, using an explicitly selected benchmark with `--workload-root` to
  identify a read-only ONLV repository containing the pinned commit. No new
  product CLI or environment variable is added. Reuse `internal/devprocess.Start`
  and its `Configure` hook for private inherited pipes, bounded output, completion
  and cleanup. Link the experimental generation/owner/input record; read the
  executable's actual SHA-256 independently in both supervisor and child.
  Activation follows successful attestation and precedes a distinct ready
  message. Rationale: correct identity and timing without production host work.
  Date: 2026-09-13. Author: Codex.

- Decision: allocate Plan 0181 as an architecture feasibility plan layered on
  Plan 0180. Rationale: the remaining floor is the native artifact boundary;
  further watcher, hashing, projection, copy, or cache tuning is not authorized
  without new dominant-phase evidence. Date: 2026-09-13. Author: Codex.
- Decision: define semantic execution independently of transport. Rationale:
  Unix sockets, socketpairs, and pipes are replaceable mechanisms; identity,
  authorization, typed outcomes, cancellation, trace context, and stream
  ownership are the actual contract. Date: 2026-09-13. Author: Codex.
- Decision: admit production host integration only when the real ONLV island has
  at most 200 ms warm compile+link, at most 100 ms launch/load+handshake, at most
  250 ms combined p50, a dramatically smaller closure than 620 packages, and no
  identity or correctness failure. Rationale: RPC and lifecycle machinery is not
  justified without native replacement headroom. Date: 2026-09-13. Author: Codex.
- Decision: the first checkpoint may exclude streaming and a real database
  transaction, but production promotion may not. Rationale: closure feasibility
  should precede broad transport work, while database and streaming semantics
  remain mandatory cutover gates. Date: 2026-09-13. Author: Codex.
- Decision: use `clean.tech/solar/ahjs` and
  `ahjs/operation/list_ahjs` for the real island. Rationale: it is an unchanged
  authored implementation with generated typed input/outcome, sqlc-generated
  database code, `pgtype`, `lib/pq`, and the normal SQL constructor dependency,
  yet it does not import the global runtime composition. Date: 2026-09-13.
  Author: Codex.
- Decision: stop the unique-edit series after five samples. Rationale: its
  928.710 ms combined p50 exceeded the predeclared 325 ms continuation threshold,
  so another 25 samples could not justify production RPC or host integration.
  Date: 2026-09-13. Author: Codex.
- Decision: apply decision-tree Case C and reject the process-per-edit stock-Go
  executable island. Rationale: the closure is radically smaller, but both the
  supported link path and first execution remain above their feasibility gates.
  Record alternative execution technologies separately without selecting or
  implementing one. Date: 2026-09-13. Author: Codex.

## Outcomes & Retrospective

The corrected, reproducible experimental checkpoint is NO-GO. It now proves
compiled generation identity and both sides' executable SHA-256, exact owner,
protocol/contract/producer identity, supervised PID, construction before ready,
and the genuinely new typed handler response. It does not prove the full
semantic ABI or a response through the advertised HTTP endpoint.

| Corrected boundary, five measured edits after two warmups | p50 (ms) | p95 / worst (ms) |
|---|---:|---:|
| Supported `go build` | 518.559 | 567.220 |
| New process launch, attestation and constructor-ready | 401.598 | 417.637 |
| Build start through verified new typed response | 934.457 | 977.022 |
| Edit completion through typed response, including experiment capture | 1,303.663 | 1,393.062 |

The real island remains 238 packages and is 8,166,370 bytes. A separate unique-edit
action diagnostic measured AHJ compilation 29.294 ms, dispatch-main compilation
32.036 ms, and linker command 145.071 ms; complete `go build` was 516.853 ms.
Those command intervals are not substituted for the supported build boundary.
Across 100 steady calls, pipe ping p50/p95 was 0.041/0.069 ms and typed invocation
was 0.061/0.106 ms. No unrelated framework package needed a compiler command.
The first-execution floor, not transport or constructors, remains dominant.

This run stopped at five under the predeclared rule. All measured samples and
negative cases passed their bounded correctness checks; all child processes
were stopped, the owned worktree/root was removed, and the original ONLV
checkout remained unchanged. Raw evidence was retained. SQL transactions,
streaming, auth/context propagation, internal calls, public endpoint activation,
rollback and production packaging remain unperformed. Plan 0181 stays open;
neither the product architecture nor its 300/500 ms target is complete.

### Earlier timing-only checkpoint

The initial timing experiment returned NO-GO for a
process-per-edit stock-Go executable island. The implementation artifact proved
that the registration/composition coupling can be removed: the real ONLV AHJ
island is 238 packages and 8,060,898 bytes, compared with the current 620-package
monolith and Plan 0180's 619/620-package rejected worker.

That smaller closure did not produce native replacement headroom. Five unique
edits measured build p50 531.280 ms, new-artifact launch/handshake p50 397.648
ms, and combined p50 928.710 ms. Typed framed-pipe invocation itself was only
0.308 ms p50 and every sample returned the exact new behavior. Its echoed
invocation/execution generation was not independent identity proof. The supported island build is effectively no
faster than Plan 0180's 529.116 ms average monolithic build/link floor, while
new-artifact launch is an additional dominant cost.

The 30-edit series was intentionally not run because the first-five continuation
gate failed by 603.710 ms. No production Scenery runtime, stable host, RPC path,
public contract, or ONLV handler API was changed. Database execution, streaming,
internal calls, auth, process replacement, rollback, and end-to-end `scenery up`
equivalence remain unperformed because a failing feasibility experiment is not
eligible for production integration. The separate
[Native Reload Execution Technology Decision](../native-reload-execution-technology-decision.md)
records the remaining native floor and compares later experiment categories
without selecting or implementing one. This plan remains active because the
300/500 ms product acceptance target is still unmet and Milestone 5 is blocked.
The owned ONLV worktree was removed, the original ONLV checkout remained clean,
no experiment process remained, and temporary binaries/prototype directories
were moved to the macOS Trash for recoverable cleanup.

## Context and Orientation

The current development artifact is generated under the private build workspace.
`internal/generate/generate_application.go` renders per-package adapters and one
`internal/scenerygen/composition` package. The generated entrypoint imports that
composition, and `composition.Register` imports and registers every adapter.
Each native adapter imports its authored implementation package, generated
contract package, the root `scenery.sh` facade, and `scenery.sh/runtime` for
registration, policy, dispatch, tracing, and HTTP/runtime behavior. This makes
composition the fan-out root for the entire application.

Plan 0180's rejected experiment moved the entrypoint but kept this fan-out root.
The exact dependency analysis in this plan must answer:

| Question | Evidence to record |
|---|---|
| Which generated package imports application composition? | exact entrypoint import and path from complete `go list -deps -json` capture |
| Which composition imports reach runtime? | shortest complete paths from composition through adapters to `scenery.sh/runtime` |
| Which application packages register transitively? | authored package list reachable from every adapter imported by composition |
| Which framework packages dominate? | reachable non-standard package counts grouped by first Scenery/runtime owner |
| What invokes one selected handler? | constructor, contract input/outcome codec, method call, context bridge, and required capability values |
| What is composition-only baggage? | packages absent from the minimal island but present in the rejected worker |

The completed graph answers are:

| Boundary | Complete closure result |
|---|---|
| Generated entrypoint | `clean.tech/scenery_internal_main` directly imports `clean.tech/internal/scenerygen/composition`, `scenery.sh/auth`, and `scenery.sh/runtime`. |
| Generated composition | `clean.tech/internal/scenerygen/composition` has 50 direct imports: 47 generated adapters, application assets, `scenery.sh`, and `scenery.sh/runtime`. |
| Application registration | The 47 adapters each directly import one authored application implementation package, its generated `scenerycontract`, and `scenery.sh/runtime`; all 47 authored packages therefore register transitively. |
| Framework-dominant fan-out | `scenery.sh/runtime` reaches 315 packages: 222 standard, 59 external, and 34 Scenery packages. Its closure includes assistants, MCP gateway/federation, durable execution storage, storage configuration/filesystems, PostgreSQL integration, development reporting, policy, runtime assets, and state upgrade machinery. |
| One AHJ invocation needs | `clean.tech/solar/ahjs`, `clean.tech/solar/ahjs/scenerycontract`, generated typed decode/outcome encode, its normal SQL constructor dependency, `scenery.sh` SDK values, and `runtime/shared` request/span leaves. |
| Composition-only baggage | The minimal island drops global composition, all 47 adapters, 46 unrelated authored implementations and contracts, public HTTP runtime, assistants, provisioning, supervisor/runtime orchestration, and unrelated generated clients. |

The experimental implementation island follows Go package and state topology,
not `.scn` service aesthetics. Importing one authored Go package necessarily
compiles all files in that package and its transitive imports. If a package
mixes independent handlers or imports framework registration only for unrelated
files, record that as a source-level grouping constraint. Do not invent a public
reload-island concept or ask applications to annotate handlers.

Runtime capability ownership starts with three classes:

| Class | Initial ownership |
|---|---|
| Must remain with implementation | authored implementation package initialization, application globals, constructor state, user libraries, real SQL handles and local transactions, native state |
| Stable host candidate | public listener, route and binding metadata, authentication admission, policy where semantics remain exact, framework generation ownership, supervision, readiness, drain and rollback routing |
| Requires proof | request/span propagation, logs, traces, metrics, internal generated calls, durable dispatch, storage, streams and helper access |

No SQL RPC proxy is allowed. A database-backed promotion sample must construct
the service with the same generated constructor contract and an equivalent real
connection in the implementation process. Transactions and cancellation remain
local to that process.

### Private semantic execution ABI

The semantic ABI is private and versioned. It uses generated contract codecs,
not `http.Request`, reflection, or an application-visible SDK. The provisional
values below describe meaning; transport framing is intentionally undecided.

`InvocationEnvelope` contains:

- a private ABI revision and exact framework producer;
- contract revision and package contract ABI revision;
- operation address and selected binding address;
- execution generation, implementation-island revision, artifact digest,
  build-input revision, worktree identity, session owner epoch, and a
  single-use invocation ID;
- the existing request kind, service, endpoint, method, logical path, copied
  headers, path parameters, locale, deployment, execution ID, caller binding,
  and request start time represented by `runtime/shared.Request` semantics;
- authenticated principal plus the exact typed authentication data needed by
  current application APIs, including tenant identity;
- absolute deadline, cancellation linkage, and trace/span parent context;
- typed input bytes encoded by the selected operation's generated contract
  codec and its exact type expression.

`ResultEnvelope` contains:

- the same invocation, execution-generation, island-revision, and artifact
  identity needed to reject cross-session or stale responses;
- exactly one generated typed outcome encoding or one Scenery system/transport
  failure encoding;
- trace completion metadata and, for a stream result, a separately framed
  reader transfer whose ownership is accepted exactly once.

Semantic rules:

1. Host admission authenticates the private peer and binds it to the exact
   app root, worktree, session owner epoch, contract, producer, candidate and
   artifact. Mismatch fails closed before application code runs.
2. The implementation side reconstructs the existing `runtimeapi.Invocation`
   and `runtime/shared.Request` meanings. `scenery.CurrentRequest`, auth access,
   `scenery.StartSpan`, internal-call caller identity, deadline and cancellation
   observe the same values as the monolith.
3. Generated typed decode, clone, handler invocation, outcome clone and encode
   retain the current nil/outcome/error checks. Application errors do not become
   arbitrary transport strings.
4. Internal calls re-enter the host semantic boundary with the current
   invocation and caller package/binding; co-located handlers may optimize only
   beneath the same checks.
5. Streams are never eagerly buffered for the production boundary. The producer
   owns its reader until the host accepts transfer; after acceptance the host
   owns exactly one close, propagates backpressure and cancellation, and
   preserves EOF versus terminal error.
6. Activation is separate from handshake. A candidate may attest exact identity
   without becoming routable; the host switches one immutable routing generation
   atomically only after all required checks pass.

### Startup and side effects: current runtime versus the experiment

The following ordering is grounded in the current code, not inferred from the
presence of a flag. Go always initializes imported packages before entering
`main`; a `main` flag cannot fence that work. Existing native preflight already
runs package initialization. It does not prove arbitrary package initialization
safe to overlap, and this plan does not permit overlap of write-capable app
generations.

| Work | Current executable preflight | Current real activation | Corrected AHJ island |
|---|---|---|---|
| Go package variables and `init()` | Already executed before the flag branch | Execute again in the newly launched process | Execute once before attestation; include the measured first-execution interval. The selected authored AHJ package has no `init()`; generated contract registration and SDK init remain in its closure. |
| Exact linked identity / storage descriptor | `runtime.WriteRuntimePreflight` verifies linked bundle and supplied storage descriptor, writes fd 3 and exits | Generated main verifies its linked contract before registration | Hash own executable and report linked experimental owner/generation/inputs before activation. No storage capability is supplied or claimed. |
| SQL environment configuration | Not run | `runtime.ConfigureSQLBindings` loads dotenv and resolves endpoint/schema environment without opening a DB | Not run; validation-only fixture injects a rejecting SQL capability. |
| Standard auth registration | Not run | `auth.RegisterStandard` loads configuration/secrets and registers handlers. Actual database initialization is lazy in `standardAuthService`. | Not imported or claimed. |
| Composition / service registration | Not run | Generated main registers all adapters and seals the registry | Global composition absent; the single generated contract is used directly. |
| Capability acquisition / constructor / Start | Not run | `runtime.InitializeServices` runs dependency-ordered batches; each generated initializer resolves dependencies, calls the constructor, then its declared Start hook | Only an authenticated/scoped activation frame calls the unchanged `ahjs.NewService`. AHJ has no declared Start/Stop hook. Ready is emitted only after a non-nil service and nil error. |
| Durable, event and schedule execution | Not run | `runtime.Main` starts them after successful service initialization and before listening | Absent; no durable/schedule equivalence claim. |
| Database apply / migrations / seed | Not performed by preflight | Supervisor `dev_build_pipeline.go` prepares required setup before app activation and fingerprints unchanged work; rollback cannot undo such external writes | Never run. |
| Assistant/helper lifetime | No app helper startup | Supervisor `startPreparedApp` activates prepared assistant state before child launch and starts prepared helpers after listener readiness; runtime bootstrap follows registered dependency ordering | Absent; no helper lifetime change. |
| Readiness and first response | Not readiness | Listener begins after service/durable/event/scheduler startup; normal endpoint identity remains the final proof | Separate attested, ready and typed result timestamps. The typed pipe result is not a normal public endpoint. |

Source owners are `internal/codegen/config.go`, `runtime/contract_preflight.go`,
`runtime/sql_bindings.go`, `runtime/registry.go`, `runtime/app.go`,
`internal/generate/generate_application.go`, `auth/standard.go`,
`cmd/scenery/dev_build_pipeline.go`, and `cmd/scenery/dev_app_start.go`.
Existing in-process tests in `internal/codegen/config_test.go` and
`runtime/contract_preflight_test.go` retain the current ordering/identity checks.
The explicit island benchmark additionally rejects invocations before
activation and constructors that return an error, and confirms termination
before deleting candidate resources. Launch-once applies only to this isolated
fixture; no application-wide side-effect or deployment claim follows.

## Milestones

### Milestone 1: rejected-boundary attribution

Capture complete package graphs for the real ONLV monolith and rejected
composition-importing worker. Produce shortest import paths, application package
fan-out, grouped framework counts, artifact sizes, and the minimal necessary
invocation subset. Preserve raw captures under a bounded ignored harness root;
put the compact findings in this plan.

### Milestone 2: minimal real implementation island

Use an owned disposable worktree at the exact ONLV revision used by Plan 0180.
Select a representative unary operation based on its real dependency and state
topology. Build a tiny generated dispatch shim that imports only its authored
implementation and generated contract. It must decode one generated typed input,
construct or receive the real service dependency shape, invoke the authored
method unchanged, and encode the generated typed outcome.

The prototype may use the smallest local pipe/socket framing needed to prove one
operation only after closure is shown to be dramatically smaller. It may not
import global composition, unrelated adapters, public HTTP runtime, runtime
orchestration, assistants, provisioning, supervision, or unrelated generated
clients.

### Milestone 3: stock-Go feasibility measurement

Measure cold build, separate warm compile and link action durations, artifact
size, package count, launch to ABI handshake, first invocation, steady transport
overhead, RSS and package initialization. Run two unmeasured warmups. If the first
five unique edits have combined replacement p50 at most 325 ms and closure is at
most half of the 620-package monolith, continue to 30 unique compiled behaviors.
Otherwise stop the series, retain every sample, and issue NO-GO without building
RPC infrastructure.

The promotion gate is:

| Boundary | Ideal | Maximum credible |
|---|---:|---:|
| warm compile + link | 150 ms | 200 ms |
| launch/load + exact handshake | 50 ms | 100 ms |
| combined native replacement p50 | — | 250 ms |
| 30 unique edits | no identity or correctness failure | required |

### Milestone 4: checkpoint decision

Report the new plan, rejected-worker explanation, semantic ABI, selected real
operation, exact closure, bytes, compile/link/launch distributions, comparison
with the 529.116 ms monolithic build/link average, and GO / NO-GO. Stop before
production integration so the developer can evaluate the fork in the road.

### Milestone 5: conditional stable-host integration

This milestone remains blocked by the Milestone 4 GO result and developer
continuation after the checkpoint. A passing experiment permits one singular
development execution path: stable host listener, exact candidate handshake,
atomic implementation-generation routing, predecessor drain, retained rollback,
and no host restart for implementation-only edits. A failing experiment does not
permit this milestone.

## Plan of Work

First create one task-owned ONLV Git worktree without copying live state or data.
Generate the current contracts with the exact worktree-local Scenery executable
and a current-source framework selection. Capture complete `go list -deps -json`
graphs for the generated monolith and composition worker, then reduce them into a
small deterministic report. Do not infer absence from partial `go list` output.

Inventory generated composition and adapters. Follow every application import
from composition and find shortest paths to `scenery.sh/runtime`. Group retained
framework packages by the first non-standard import from the generated layer.
Separate authored implementation reachability from framework-only reachability.

Select the real ONLV operation only after those graphs are available. Prefer a
database-backed unary operation with an ordinary generated input/outcome and
representative user dependencies. If its Go package imports unrelated runtime
registration through other files, compare a second natural implementation
package rather than editing the handler into an artificial shape. Record why the
selected package represents an honest island.

Generate the experimental main/shim under the ignored task-owned harness root.
Keep ONLV handler source unchanged for closure and initial measurements. For
unique edit timing, alter a behavior-affecting literal in the owned worktree only,
invoke that exact new behavior through the semantic shim, and restore it after
the series. Never alternate previously built variants.

Use `go build -x` or `go tool` action evidence only in a separate diagnostic run
to distinguish compile from link; do not add tracing overhead to accepted wall
samples. Launch samples end only after the artifact returns an exact identity
handshake. Invocation overhead is measured separately from the handler's own
work.

If the gate fails because closure remains nearly monolithic, stop and identify
the import or package grouping that forces it. If the closure is small but stock
Go remains over the time gate, write a separate architecture decision comparing
dynamic/plugin-like loading, recyclable c-shared loading, alternative incremental
Go backends, and dev-only interpreted/JIT execution; do not implement them here.

## Concrete Steps

All Scenery commands run from `/Users/petrbrazdil/Repos/scenery` unless another
working directory is named.

Start and refresh changed-area selection:

    go run ./scripts/verify --quick --summary --write
    jq '.changed_area.validation_classes, .changed_area.recommended_commands' .scenery/harness/agent-context.json

Record immutable identities:

    git status --short --branch
    git rev-parse HEAD
    shasum -a 256 .scenery/harness/bin/scenery
    go version
    go env GOOS GOARCH GOVERSION GOTOOLDIR CGO_ENABLED
    sw_vers
    sysctl -n machdep.cpu.brand_string hw.model hw.memsize hw.ncpu
    git -C /Users/petrbrazdil/Repos/onlv rev-parse HEAD
    git -C /Users/petrbrazdil/Repos/onlv status --short

Create an owned temporary parent with `mktemp -d`, add an ONLV detached worktree
at `4f8126a3e3806b7100ab7efaca1b7dd06b894221`, and record the exact path in the
ignored experiment manifest. Remove only that exact registered worktree after
all processes are stopped and artifacts are copied into the bounded Scenery
harness evidence root.

Use the exact prepared binary for current-source selection and contract generation
inside the disposable ONLV worktree:

    /Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery framework use --source /Users/petrbrazdil/Repos/scenery -o json
    /Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery generate --target contracts -o json
    /Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery check -o json

Capture the monolith and each prototype from their real entrypoint directories:

    go list -mod=readonly -deps -json ./scenery_internal_main > <evidence>/monolith-deps.json
    go list -mod=readonly -deps -json ./scenery_implementation_island > <evidence>/island-deps.json

The measurement runner writes bounded JSON containing every sample and failure,
machine/toolchain/source/artifact identities, action phases, package counts,
bytes, RSS, and the chosen operation. It never clears shared caches or edits the
Scenery source after the prepared executable is recorded.

## Validation and Acceptance

Resumed checkpoint implementation changes only repository experiment support
and living documentation. The refreshed cumulative classes are
`cli-json-contract`, `go-package`, and `release-sensitive-or-runtime`. Before the corrected
measurement, finish source edits and prepare the worktree-local product. Then
freeze the framework source for the entire measurement run. Exact commands
from the Scenery repository root are:

    go test ./scripts/verify
    go test ./cmd/scenery
    go test ./internal/codegen ./runtime ./internal/devprocess
    go run ./scripts/verify --quick --summary --write
    go run ./scripts/verify --benchmark native-reload --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write

The corrected runner retains an additional, separately labelled unique-edit
`-debug-actiongraph` diagnostic outside the warm decision series. Its raw Go
action graph records compiler/linker command time and enclosing action/queue
intervals. These spans can overlap and are not added as sequential latency.
The ordinary decision samples do not enable action tracing. The protocol has an
unversioned logical identity plus an exact source-content protocol revision;
prototype source bytes are captured alongside the application build inputs.

The benchmark only reads the supplied source repository and creates its own
detached worktree at `4f8126a3e3806b7100ab7efaca1b7dd06b894221`. It generates
contracts and runs current-source `check`, the AHJ package tests and app harness
in that disposable worktree. It never calls `up`, resets/seeds a database or
touches the original application's live state. Database and streaming execution
remain explicit promotion gates, not requirements for rejecting this native
boundary. The benchmark is excluded from default, quick, race and release.
Do not run integration or alternative execution technologies after a NO-GO.

The experimental checkpoint changes documentation and repository-only experiment
support. Run the exact commands selected by the refreshed agent context, including:

    go test ./scripts/verify
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --quick --summary --write

If product generator source changes, also run:

    go test ./internal/generate ./cmd/scenery
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json

If Milestone 5 changes the real development runtime, the completion union is:

    go run ./scripts/verify --summary --write
    go run ./scripts/verify --race --summary --write
    go run ./scripts/verify --probe generation --probe native-contract --probe build-info --probe dev-process --probe parallel-runtime --probe core-separation --summary --write
    go run ./scripts/verify --probe auth --probe postgres --probe fixtures --summary --write
    go run ./scripts/verify --benchmark worktree-cost --summary --write

The production-equivalence command with auth/PostgreSQL/fixtures may be skipped
only while Milestone 5 has not begun; the plan records that condition as
experimental NO-GO or checkpoint hold, never as passed proof. Release
certification remains unselected until the architecture is proposed as ready for
merge; at that point the only release command is `scripts/release-gate.sh`.

Experimental acceptance requires all of the following:

- complete package captures with no `Incomplete`, `Error`, omitted dependency,
  or unresolved module replacement;
- one unchanged real ONLV implementation package and generated contract invoked
  through the semantic shim;
- exact identity match between requested, handshaken, and returned implementation
  artifact on every accepted sample;
- dependency count and bytes reported against the same toolchain/target as the
  620-package, 51,974,626-byte monolith;
- the numerical decision tree applied without discarding failures or selecting
  favorable samples afterward;
- the original ONLV checkout remains byte-clean and its services/data untouched;
- every task-owned process is stopped and every task-owned worktree registration
  is removed.

The earlier documentation-only checkpoint on 2026-09-13 selected only the cumulative
`documentation-only` class. The following commands passed:

    go run ./scripts/verify --quick --summary --write
    go test ./...
    golangci-lint run ./...

The quick verifier completed with 41 pre-existing knowledge freshness warnings
and 21 pre-existing architecture warnings, with no errors. Product integration
probes, the worktree benchmark, race verifier, real ONLV database transaction,
streaming/internal-call proof, and release certification were not selected
because the feasibility decision is NO-GO and Milestone 5 did not begin.

### Resumed validation results

The resumed checkout retained the earlier uncommitted Plan 0181/index/decision
documents on the same branch/HEAD. This slice added only repository-verifier
code/tests and living documentation; no production runtime, generator or SDK
code changed. The exact cumulative recommended commands were:

    go run ./scripts/verify --summary --write
    go test ./...
    go test ./cmd/scenery
    go test ./scripts/verify

All passed. Additional commands run and passing were:

    go test ./internal/codegen ./runtime ./internal/devprocess
    golangci-lint run ./scripts/verify/...
    golangci-lint run ./...
    go run ./scripts/verify --quick --summary --write
    go run ./scripts/verify --benchmark native-reload --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
    git diff --check

The first quick run failed on a versioned experimental protocol name and two
uncovered `go:embed` inputs. Both were corrected: use an unversioned logical
identity plus exact protocol revision and capture the prototype sources as
explicit experiment inputs, without embedding a verifier-only fixture into the
product. An initial scoped lint run caught a capitalized error string; final
scoped and full lint returned zero issues. These were harness implementation
failures before the corrected native measurement, not discarded latency samples.

Both default runs passed architecture, drift, schema, vet and Go checks
with 41 existing knowledge-freshness and 21 existing large-file warnings. The
full-suite wall time was 6.814 s initially and 5.649 s after the metadata
correction, against the advisory cached 5 s lane; this is a warning, not proof
of the cached-suite performance target. No assertion or timing policy was relaxed.

For the seven new/changed exact roots, one run-local test executable was linked
with `go test -c -o /tmp/scenery-0181-root-timing.y311ev/verify.test scenery.sh/scripts/verify`.
The package directory came from `go list -f '{{.Dir}}' scenery.sh/scripts/verify`.
Each root ran 20 times, serially in fresh processes from
`/Users/petrbrazdil/Repos/scenery/scripts/verify`, with:

    go tool test2json -t -p scenery.sh/scripts/verify /tmp/scenery-0181-root-timing.y311ev/verify.test -test.v=test2json -test.count=1 -test.parallel=1 '-test.run=^<exact-root>$'

Full active root lifetime includes run-to-pause and continue-to-terminal time;
parallel queue wait is excluded, matching the existing timing algorithm. The
largest exact-root p95 was 0.256 ms and largest individual observation 0.487 ms,
well below 100 ms. Raw events, the seven exact root names, all 140 samples and
expanded commands are retained in `test-root-confirmation.json` beside the
native report, SHA-256
`0ddfd2bf9251296894dc71298a728cfd7163e781023319dc6e6a693322edde91`.
An initial collector rejected timezone-offset timestamps before recording a
sample; the corrected collector accepts RFC3339 offsets. The temporary test
binary was moved to the macOS Trash after all processes completed. No all-root
timing audit was run.

Final review also corrected the benchmark's report-effect classification to
declare filesystem reads/writes, temporary directories, external binaries and
test-cache use. This changed reporting metadata after the native run, not its
captured worker or protocol. Those two prototype sources still match retained
SHA-256 values `637b338da438c9637fea6a35bbc3b11b926988835fd5434cdc5367823980ccec`
and `8b841525a8cc3bbef4a65ffa6196872727ee2816984aad421f285df6f4d34bfe`.
The eighth root, `TestNativeReloadEffects`, passed the same 20-process algorithm
with p95 0.133 ms and worst 0.141 ms; its commands and raw samples are in
`effect-root-confirmation.json`. Its separately linked run-local binary was
`/tmp/scenery-0181-effects-timing.YaGLZx/verify.test`, subsequently moved to Trash.
Affected-package tests, the full Go suite, lint and default verification were
repeated after this metadata-only correction. Native timings continue to be
attributed only to the exact frozen producer recorded below, not silently
relabelled as another source revision.

Inside the owned ONLV cwd recorded below, the exact selected CLI passed
`generate --target contracts -o json`, `check -o json` before and after the
experiment, and `harness -o json --write`; `go test ./solar/ahjs/...` passed.
The app harness exercised check/inspect only. It explicitly reported absent
local observability; it did not claim a live application response. The original
ONLV checkout remained clean. Full ONLV application tests, database reset/proof,
public endpoint proof, auth/internal-call/stream equivalence, generator consumer
refreshes, runtime integration probes, race/release lanes and `worktree-cost`
were not selected: this slice changes no production/generator boundary and its
native feasibility result forbids integration. These remain unperformed proof,
not passing acceptance.

## Idempotence and Recovery

The experiment root has an explicit marker containing the source repository,
exact ONLV commit, Scenery commit/source digest, creation time, and owner PID.
Reruns create a new root and never adopt an unmarked directory. Measurements use
unique sample IDs and write a temporary JSON file followed by atomic rename.

Before cleanup, stop and join only PIDs recorded beneath the task-owned marker.
Verify their executable paths and worktree root before signaling them. Remove
the exact Git worktree registration with `git worktree remove`; if untracked
evidence prevents removal, copy the bounded report first and move the residual
task-owned root to the macOS Trash rather than recursively deleting an unresolved
path. Never prune unrelated worktrees, caches, services, or databases.

If source changes during capture or measurement, retain the failed sample,
discard its candidate, restore the owned handler from its recorded original
digest, and restart from a new sample ID. A failed candidate never becomes an
accepted generation.

## Artifacts and Notes

Bounded raw experiment evidence lives under
`.scenery/harness/minimal-native-reload/<run-id>/` and is ignored by Git. The
plan records its SHA-256, exact reproduction command, and compact tables. Raw
`go list` captures stay in that bounded directory rather than in repository
documentation.

### Corrected reproducible checkpoint

Run from `/Users/petrbrazdil/Repos/scenery`:

    go run ./scripts/verify --benchmark native-reload --workload-root /Users/petrbrazdil/Repos/onlv --summary --write

The corrected run's evidence directory is
`.scenery/harness/minimal-native-reload/attested-2575520164/`. Its `report.json`
SHA-256 is `e244f723b1fc526b549c9f7a11bdc56975385ac591dc793ed33d1fca768d775a`.
The separate `diagnostic-actions-actions.json` SHA-256 is
`4a2bc105be465ec7506d204a5c81096d7ba21f5097f67c37632ff019652006c2`.
The report contains every command/cwd, sample, raw protocol reply, phase,
selected framework manifest, per-file input hashes, Go environment and failure.
The complete dependency captures and captured prototype source remain beside it.

The owned app cwd was
`/private/var/folders/v3/1ff5y8y13dqc_czsbf085w1h0000gn/T/scn-native-reload-3448498551/onlv`.
Its exact prepared CLI was:

    /private/var/folders/v3/1ff5y8y13dqc_czsbf085w1h0000gn/T/scn-native-reload-3448498551/onlv/.scenery/framework/bin/006a4e04513adc03b6f1978ea4d5c2f2c18c73c1a65b6f5f3d9b360e7079cd5c/darwin-arm64-go1.27.0/cb42b62898980bdfcc14fe8936f1c6950afef1f5370465eac9fa50547a3cd48e/scenery

Framework source `sha256:006a4e04513adc03b6f1978ea4d5c2f2c18c73c1a65b6f5f3d9b360e7079cd5c`
and executable `sha256:cb42b62898980bdfcc14fe8936f1c6950afef1f5370465eac9fa50547a3cd48e`
were verified again before cleanup. No framework source changed during the run.
This source includes the dirty experimental harness changes atop HEAD
`50d89dad1892e75fb5b505e1d4db51a64d4ac225`; the commit alone is not its identity.
ONLV is pinned to `4f8126a3e3806b7100ab7efaca1b7dd06b894221`.

Hardware was Apple M2 Ultra / Mac14,14, 24 logical CPUs, 64 GiB, Darwin 25.5.0,
Go 1.27.0 darwin/arm64, cgo enabled, default debug information and the existing
Go package/module caches. Load averages at start were 8.07 / 6.51 / 5.46;
unrelated background work was not stopped. These are one-machine early
feasibility results, not a 30-edit distribution or a universal toolchain bound.
The initial build in the new worktree took 513.225 ms with existing caches; it
is not a cold-cache result. The historical private-cache cold result remains
separate below. Initialized RSS was 19,988,480 bytes; a separate init trace
reached the final package initializer at about 22 ms, including about 20 ms in
`internal/spec`. Constructor time in that diagnostic was 0.001625 ms and
self-executable hashing 3.723 ms. Neither diagnostic is an accepted timing sample.

All five samples verified new compiled error-message literals in the real
`ListVNext` handler. Before invocation, the benchmark rejected invocation before
activation, foreign session/worktree, wrong ABI, producer, executable and
generation. It also launched an older actual artifact against the latest
expected identity and rejected its independently reported old identity; a failed
real constructor exited without ready, and cancellation stopped an attested
but inactive process. These are experiment-boundary checks, not broad runtime
equivalence or a proof that arbitrary Go `init()` is inert.

Source verification is a complete Go-reported membership/content capture before
and after build/execution in an owned disposable worktree, with the source edit
performed only by this runner. It is not a replacement for Plan 0180's captured
production workspace engine. External system headers/libraries are not separately
snapshotted, so this evidence cannot authorize relocatable production reuse.

### Earlier ad hoc experiment

The earlier checkpoint evidence root is
`.scenery/harness/minimal-native-reload/20260913-onlv-island/`. Its compact
`experiment-report.json` has SHA-256
`4b28d634d293ddf414e1a0c5f679bac46f61f43e997f8ecf4ab18081390a5804`.
The bounded supporting captures are:

| Artifact | SHA-256 |
|---|---|
| `monolith-deps.json` | `535309d8cd0664fe741f3210591ec1eac4502daa9d40ed3a26c38d22b52f1f51` |
| `rejected-worker-deps.json` | `a410a62d68db9eabda280d4d7c9312e55e9385001cc3bdb665c8c5ff4a3cc324` |
| `island-deps.json` | `878f5e965b2b260d544ac460c1b707ed5e38ece3cbc9a597860379bd8eefd344` |
| `dependency-report.json` | `147971cdf03288a56b29b617cbb7b33fce972a4f023ad462214005793d775e26` |
| `link-only-x.log` | `b360da4b16e0bf350f31708f60eb35061167ab38e579d213ab2270baaee9c076` |
| `unique-edit-x.log` | `225066487ec8ee057e2b82a66cbdf694913eb9a9ee241ca7f5025db414e52beb` |
| `inittrace.log` | `6169e1f890a4ca8e5145c0ef22261bd815a8ecaa327fc79082780d01aa408a1e` |
| `island-main.go` | `ec387e925a05c028a275e64740ccdfbdce0b3e8287a39de935b02daf58c22be4` |

The experiment ran on an Apple M2 Ultra `Mac14,14` with 64 GiB RAM,
macOS 26.5.2 build 25F84, and Go 1.27.0 darwin/arm64 with cgo enabled. It
used Scenery commit `50d89dad1892e75fb5b505e1d4db51a64d4ac225`, prepared
current-source framework digest
`sha256:6f2af0a9de536351fd1c4fb44e8a20a028d16f92bc1bab72b349b2de1ca5108f`,
and ONLV commit `4f8126a3e3806b7100ab7efaca1b7dd06b894221`.

The Plan 0180 final comparison remains the control: 30 baseline and 30 candidate
unique edits, candidate p50/p95/worst 1,533.570/1,571.196/1,583.519 ms,
average build/link 529.116 ms, and average exact preflight 501.526 ms. The
minimal-island result compares only the native replacement boundary until a
stable host is integrated; it is not yet an end-to-end Scenery latency claim.

## Interfaces and Dependencies

The experiment reuses `internal/appsdk`, `internal/runtimeapi`,
`runtime/shared`, generated operation contract packages, and the authored
implementation constructor/method signatures. It adds no public package,
configuration, environment variable, `.scn` construct, CLI grammar, machine
envelope, or deployment promise.

Any eventual private ABI implementation needs a narrow internal owner for the
envelopes and identity validation. It must not import `cmd/scenery`, HTTP runtime,
the application composition, generator, compiler, provisioning, or supervision.
The stable host and artifact may depend on that leaf; generated dispatch may
depend on the leaf plus the selected operation contract and implementation only.

The corrected experiment uses two inherited pipes on fd 3/4, created through
the existing `devprocess.StartRequest.Configure` hook. Stdout/stderr remain
bounded application output, never protocol framing. Pipe possession scopes
the private peer, and linked session/worktree identity rejects cross-session
requests. This is a subset of the semantic ABI above: principal, tenant,
CurrentRequest/StartSpan, cancellation propagation, internal-call policy and
stream transfer remain unimplemented and unproven. Its small error strings are
experiment failures, not a substitute for Scenery's public failure contracts.
Network ports, MCP, gRPC, another daemon, dynamic loading, JIT/interpreters,
alternative languages and custom compilers remain outside this experiment.
