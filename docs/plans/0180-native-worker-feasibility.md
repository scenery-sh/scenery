# Native Worker Feasibility Gate

This ExecPlan is a living document while active, following
[PLANS.md](../../PLANS.md). The feasibility experiment is now complete, with
expansion rejected on 2026-09-11. It is the architectural
experiment for [0179](0179-half-latency-development-loop.md), not a replacement
for that plan's unchanged numerical acceptance.

## Purpose / Big Picture

Test whether compiling the complete native application separately from a stable
framework kernel materially reduces the verified development loop. The human
authorized work on this direction on 2026-09-11. Do not perform the subsequent
whole-platform migration until this experiment supplies evidence for it.

The fixed final acceptance remains edit median <=3153.377720 ms and complete
unchanged-start median <=4017.245375 ms, against 0178's 6306.755440 and
8034.490750 ms. The attachment's 4494.714741 ms edit result is historical;
0179 already records the later same-source edit median of 5461.476051 ms and
startup median of 4032.029584 ms. These are reference evidence, not measurements
of this checkout or of a split worker.

## Progress

- [x] (2026-09-11) Read the proposed architecture, live source at
  `27ebaf12ca87355c6ee7ed340d1be63ffbe814f7`, and the active latency plan.
- [x] (2026-09-11) Record process, state, input, performance and promotion gates.
- [x] (2026-09-11) Implement an offline package-closure gate and capture the
  existing ONLV prepared entrypoint: 619 reachable packages, 166 application
  packages excluding private generated adapters/composition/assets.
- [x] (2026-09-11) Validate the preparation increment: eight Python tests,
  full Go suite, CLI package, lint and default repository verification pass.
  The unchanged real capture fails candidate admission with exit 2, as required.
- [x] (2026-09-11) Extract the native import boundary while preserving all 166
  application packages and their real dependencies. Generated bootstrap still
  links the host; a separate worker process remains outstanding.
- [x] (2026-09-11) Extract the first native application boundary into
  `internal/runtimeapp`: metadata/request/span binding, span completion,
  byte-stream ownership and shared dotenv loading. Public `scenery` and `db`
  no longer import the runtime host. Public Go names remain the same; runtime
  request/tracing state remains the sole live owner. Affected/full Go tests,
  targeted race, lint and native-contract/dev-process/build-info/assistant-runtime
  probes pass. The real ONLV entrypoint builds against this source via a private
  modfile without changing its selected framework or authored inputs.
- [x] (2026-09-11) Remove auth's production import of the runtime host. Native
  request auth uses the same immutable owner; standard auth registers through
  a narrow native HTTP/codec/lifecycle bridge. Pre-host registrations are
  retained until binding, preserving package initialization ordering. All 166
  ONLV native packages remain; only three now transitively reach the host.
- [x] (2026-09-11) Extract native internal dispatch and durable step execution.
  `internal/nativecall` owns registration, trusted invocation and visibility
  checks, codecs, reentrant callbacks and registry snapshots. The host retains
  authorization/pipeline callbacks and composition rollback. `internal/nativedurable`
  owns step replay/retry, shared service-name normalization, run values and signal
  dispatch; the host supplies persistence. Public durable helpers no longer
  import the runtime host. Full worker admission remains open.
- [x] (2026-09-11) Separate native service lifecycle and composition admission
  from the host; preserve constructor dependencies, parallel ready batches,
  shutdown order and rollback. Rejected adapter admission no longer leaves
  partial resource reservations.
- [x] (2026-09-11) Split the import boundary: native `scenery.sh/runtime` no
  longer links the host, and generated bootstrap/adapters explicitly import
  `scenery.sh/runtime/host`. Preserve native call spellings, move linked identity
  symbols with bootstrap and bump the Go-generation semantic revision. The
  complete regenerated ONLV application builds with all 166 native packages;
  none of those packages imports the host transitively. The generated full-app
  entrypoint still links it, so this is not a candidate process split.
- [x] (2026-09-11) Render and build separate native worker and framework kernel
  entrypoints. Retain all 166 native packages and all 5063 baseline native text
  symbols; the worker registers 215 native operations. The kernel imports no
  native application package.
- [x] (2026-09-11) Run the real authenticated ONLV project-list through kernel
  and worker with all native constructors and owned PostgreSQL. Verify two
  tenants, anonymous/invalid tokens, SQL failure, in-flight SQL cancellation,
  worker loss and both private owner-channel shutdowns.
- [x] (2026-09-11) Integrate the explicit experiment with pending prepared-target
  verification, complete target input discovery and the ordinary executable
  retention implementation. Add independent kernel artifact identity and
  checksum-verified reuse; the full native checker still runs on every build.
- [x] (2026-09-11) Execute all six alternating baseline/candidate pairs with
  distinct semantic edits and first executions. Every pair passed behavior and
  identity checks, but the candidate was slower in every pair: medians
  4998.205 ms versus 5945.482 ms. Reject expansion of this implementation.
- [x] (2026-09-11) Record production promotion, debugger, broader behavior and
  resource-cost acceptance as unselected after the negative feasibility gate.
  No BuildSession migration or production adoption follows. The separate 0179
  end-to-end edit/start targets remain unmet.

## Surprises & Discoveries

The generated `main` is not the only framework link. `scenery.go` and `stream.go`
import `scenery.sh/runtime`; `auth/auth.go` also uses its request-local state,
and `auth/standard.go` registers HTTP handlers there. `db/db.go` imports its
dotenv loader, and `durable/runtime.go` uses durable execution state.

The real ONLV closure also has direct application imports of the runtime:
`health` calls `TraceDBQueryStart`, `TraceDBQueryEnd` and
`InvokeContractBindingJSON`; `invoices` invokes internal contacts bindings;
`house` dispatches durable executions with options. These must not be omitted
or replaced with empty functions to produce a smaller executable. Internal
typed calls, their Go contexts, SQL handles and callbacks stay in the worker.

The first native API extraction reduces the standalone `scenery.sh` dependency
graph from 315 to 213 packages. `scenery.sh/db` also no longer reaches the
runtime host. In the real prepared ONLV workspace, a private modfile replacing
only `scenery.sh` with this checkout preserves all 166 native packages and
reduces native packages that reach the host from 99 to 36. Total ONLV packages
are 620 instead of 619 because the new leaf is added while auth and direct
native calls still retain the host. This is a measured structural cut, not
evidence of a smaller ONLV executable or development-loop improvement. The
monolithic candidate still fails structural admission with no missing native
packages. Full ONLV compilation with that private modfile succeeds.

The inspected fixture currently selects source
`97c68a43754a56026b2c6538d2b417e7d5f33ae8076e6f0778438aa241ccee4d`
and producer executable
`1f78cd38296d7961832239e199ec0e30b6d9242e4b3706bde4c38474714e5d59`.
That is not 0179's final measured pair. This turn has not changed that selection
or the fixture's authored source. Recapture the baseline against the exact
chosen experiment source before any performance comparison. Package metadata
alone does not prove source freshness, linked symbol retention or behavior.

## Decision Log

- Decision: reject expansion after the six paired measurements below.
  Rationale: all six candidates are slower with complete native verification,
  exact artifact proof and verified kernel reuse; the 156.706 ms median build
  saving is below the material-gain rationale for a platform rewrite.
  Date/author: 2026-09-11, Codex.
- Decision: give the kernel its own consumed-input identity while retaining the
  worker's full declared target and native verifier. Reuse requires fresh kernel
  input/target checks and a complete retained executable checksum.
  Rationale: an unchanged kernel must not be relinked merely to echo a new
  application body identity, and a retained path is not evidence of correctness.
  Date/author: 2026-09-11, Codex.

- Decision: one worker contains all native application code; the kernel owns
  framework behavior that can operate on explicit transport values.
  Rationale: shared globals, pointer identity, transactions, arbitrary context
  values, callbacks and concrete errors cannot be transparently serialized.
  Date/author: 2026-09-11, Petr's supplied direction, implemented by Codex.
- Decision: first implement a structural admission check outside the product.
  Rationale: entrypoint replacement alone still imports the complete runtime,
  and a toy handler or unreachable package inventory cannot establish the cut.
  The check is necessary, never sufficient; it does not authorize promotion.
  Date/author: 2026-09-11, Codex.
- Decision: retain standard Go builds and debugger information, and add no
  plugin, interpreter, build-tag fallback runtime or environment switch.
  Rationale: the experiment must evaluate architecture without shrinking the
  supported native semantics or hiding cost in a second production path.
  Date/author: 2026-09-11, Codex.

## Outcomes & Retrospective

The feasibility gate is complete and rejects expansion of this implementation.
Six alternating pairs measured the real semantic Go edit through full target
preparation, native verification, executable retention, first execution and an
authenticated SQL-backed response. The ordinary control median was 4998.205 ms;
the worker/kernel candidate was 5945.482 ms, 947.278 ms (18.95%) slower. The
candidate lost every pair even though all six reused the same freshly verified
kernel executable. No task-owned validation ran during the measured series.

The native separation itself is real: all 166 native packages, all 5063 baseline
native text symbols, 215 registered operations and all 509 non-private application
input files were retained. Paired input comparisons differ only in the intended
`solar/projects/api.go` body edit. Authentication, tenant isolation, SQL failures,
in-flight SQL cancellation and owner-channel shutdown passed. Every measured
sample returned its distinct edited summary from a new proved process.

The modest build saving does not justify extending the runtime rewrite. Median
`go build` duration fell from 1516.341 ms to 1359.635 ms, only 156.706 ms; the
complete checked path became slower. Further input/session redesign is not
justified by summing hoped-for savings. Keep the private source and evidence
reviewable, do not select it in ordinary app commands, and require a new causal
hypothesis and experiment before reconsidering expansion.

This bounded experiment excludes frontends, managed assistant children and the
full development supervisor loop. It is not a replacement measurement for 0179,
and does not meet that plan's unchanged edit/start thresholds. Full streaming,
custom auth, internal/durable behavior, debugger, memory/throughput and rollback
promotion gates were not selected after rejection. No production promotion,
commit or publication was performed.

## Context and Orientation

`internal/generate/generate_application.go` renders application adapters and
composition. `internal/codegen/config.go` renders the main/preflight entrypoint.
`runtime/host/contract_registry.go` validates registration ownership before applying
callbacks to global state; `runtime/host/app.go` starts the native services, durable
runtime, events, scheduler and listeners. Their state ownership is the cut.

`scripts/native-worker-closure.py` reads complete `go list -deps -json` captures,
traverses from the actual entrypoint and reports native-to-framework import
edges and shortest paths. Native requirements come from the baseline's module,
excluding only the explicitly listed private generated prefix and entrypoint.
Public generated contracts and native library facades remain required. A
candidate must reach every required native package and none of the explicitly
designated kernel packages. The check cannot see linker dead-code elimination
or prove transitive third-party dependency equivalence. Preserve that separate
input-closure and executable proof in the experiment.

## Milestones

### M0: Contract and inventory

Record a fixed source/executable pair and the complete dependency inventory.
Keep the previously retained fork/join, shared projections, copy/environment
overlap and initial scan overlap in the baseline. They are not future savings.

### M1: Real native cut

Separate local request/auth/tracing state, service registration and typed
dispatch from framework orchestration. Account for the direct ONLV imports
above before changing generated code. Introduce only the private interfaces
needed for this experiment. Keep SQL constructors and transactions local.
Do not blanket-RPC the existing Go APIs. Generate all current native service
references so the linker cannot erase the unexercised application behind a
single demonstration handler. Compare package inventories and linked symbols.

Use ONLV's authenticated project-list operation from
`solar/projects/api.go`, the same semantic summary used by the existing latency
driver. The worker receives the full native closure, not only that service.
Exercise valid authentication, anonymous/invalid authentication, SQL-backed
results and application failures. Unknown operations fail explicitly in the
experiment. They are not described as production-equivalent.

### M2: Falsifiable cost and behavior gate

Predeclare six pairs in order AB, BA, AB, BA, AB, BA. Each member gets a distinct
real semantic summary edit and newly built executable, whose first execution
counts. Warm shared toolchain/dependency caches only before measured runs;
never warm a measured executable. Preserve all samples, identity records and
spread, and stop task-owned validation while measuring. Full stops also stop
the kernel. Record build, full verifier, retention, first proof and actual
authenticated response as one measured path.

The older sample's X'=1285.806 ms bound assumes an unchanged 1867.572 ms
remainder; it is a diagnostic bound, not the budget for the newer slower source.
Recompute the remainder on the selected paired baseline. Low hundreds of
milliseconds alone do not justify a platform migration. A positive decision
must explain the measured remaining gap without adding independent promises.

### M3: Conditional expansion

M2 rejected the current cut, so this milestone was not selected. Only a new
experiment supplying positive evidence could justify expansion in this order: a single `BuildSession`
owning captured input bytes and membership; a pure `DeclarationPlan` distinct
from native build identity; one `GoSession` per exact target context; a private
`GenerationArtifact`; proof and activation of the same owned process; verified
retention of independent assistant trees; removal of obsolete production paths.
These are future milestones, not implemented types or approved speedup claims.

## Plan of Work

Use the admission report to identify the concrete native/framework interfaces.
The kernel must not import application packages. Keep the existing runtime as
the control while the experiment is private, then remove it only after all
promotion gates pass. Never add a hidden production fallback.

Preserve the following acceptance matrix:

| Surface | Required proof before promotion |
| --- | --- |
| Native state | Same globals, constructors, pointer identity, context values, direct typed calls, errors and SQL transaction behavior |
| Authentication | Same authenticated and rejected HTTP outcomes and native actor/tenant context |
| Streams | Progressive transport, bounded memory, backpressure, cancellation, terminal errors and reader cleanup |
| Generation | Compiled input proof, loaded-plan proof, retained artifact digest and producer/target binding; no echoed expected identity |
| Transition | Captured -> BuiltAndChecked -> Prepared -> PreviousStopped -> Activated -> Ready; no two write-capable generations |
| Failure cuts | Build/ABI/freshness/proof failure, activation/listener crash, lost supervisor, corrupted artifact, unjoined helper and late old callback |
| Rollback | Old whole generation restored only after confirmed candidate exit; unconfirmed shutdown blocks all new writers |
| Target/ABI | Default and selected Go target checks remain separate; current body compilation includes verification-only packages |
| Debugger | Handler breakpoint after edit, stepping, locals and stack mapped to the current worker |
| Cost | Paired edit/start times, request latency, throughput and memory; any regression requires explicit acceptance |
| Assistants | Actual MCP tools and helper readiness; full membership/bytes/modes/symlink checks and independent worktree ownership |

Input invalidation must retain declarations/config/catalog, Go bodies/signatures,
constructor/lifecycle ABI, tags/CGO/GOOS/GOARCH/flags/toolchain/environment,
imports/module/replace/vendor/workspace selection, native/embed bytes and
membership, negative resolver alternatives/symlinks/ancestor config, generated
tampering, producer identity, fresh database state/endpoints and assistant
toolchain identity. No stored validation-success bit substitutes for these.

## Concrete Steps

From an existing prepared app workspace, capture its real entrypoint without
mutating module files (substitute the inspected absolute output path):

```sh
go list -mod=readonly -deps -json ./scenery_internal_main > /absolute/baseline-packages.json
```

From the Scenery repository root, inspect it:

```sh
python3 scripts/native-worker-closure.py --baseline /absolute/baseline-packages.json --baseline-entry clean.tech/scenery_internal_main --app-module clean.tech --generated-prefix clean.tech/internal/scenerygen --kernel-package scenery.sh/runtime
```

After a candidate exists, append `--candidate /absolute/candidate-packages.json
--candidate-entry <actual-worker-import-path>`. Exit 2 means the structural
cut failed; exit 1 means unusable input; baseline-only exit 0 reports `not_run`,
not a successful candidate. All Go capture commands must succeed without `-e`.
Capture each target separately with its actual build environment; this utility
does not select or infer targets. Inventory prefixes explicitly, never exclude
an application service to get a pass.

## Validation and Acceptance

The changed-area classifier selects the runtime-sensitive row for these scripts
and the CLI row for the plan's command references. Run the cumulative commands:

```sh
python3 -B -m unittest discover -s scripts -p test_native_worker_closure.py
go test ./cmd/scenery
go test ./...
go run ./scripts/verify --summary --write
```

Run these from the Scenery repository root. Inspect
`.scenery/harness/agent-context.json` and execute the cumulative recommended
commands. An unchanged baseline passed as candidate must fail; an incomplete
capture or entrypoint must fail; missing transitive native packages must fail
even if their metadata occurs elsewhere in the capture.

Once production Go changes begin, run affected-package tests before
`go test ./...`, `golangci-lint run ./...` and
`go run ./scripts/verify --summary --write` from this root. Generator changes
also require every command in `internal/generate/AGENTS.md`, including both
committed consumer regenerations and the assistant fixture. Prepare the UI
embed using the Fresh Worktree Preflight before the default verifier.

When M1 changes process/transport boundaries, run:

```sh
go run ./scripts/verify --summary --write --probe dev-process --probe native-contract --probe build-info --probe assistant-runtime --probe parallel-runtime --probe worktree
```

Add `go run ./scripts/verify --summary --write --probe postgres` if SQL process
ownership, endpoint resolution or transaction behavior changes. These probes
were unselected for the initial offline tooling-only increment. The native API
increment runs native-contract, dev-process, build-info and assistant-runtime;
it changes no process ownership, worktree selection or SQL endpoint/transaction
logic, so parallel-runtime, worktree and postgres are unselected for that
increment. Full release and all-root test timing
are not requested by this experiment. The six development-loop pairs are
explicitly requested measurements, separate from ordinary Go test timing.

Run M2 against the owned ONLV fixture described by 0179, preserving the personal
4920 runtime. Use its `development/measure-latency.ts` only after extending
owned experimental orchestration to identify kernel and worker; the existing
driver alone cannot prove a split. On the final selected source, run
`./scripts/scenery check -o json`, `go test ./...`,
`./scripts/scenery harness -o json --write`, `just smoke` and `just feature ahjs`
from that fixture. Record skipped candidate measurements as not run until M1
has a real executable; never manufacture six baseline-only pairs.

## Idempotence and Recovery

The closure tool only reads captures and writes JSON to stdout. Keep captures
and measurements under ignored `.scenery/harness/native-worker/`. It does not
read credentials or source paths from captured metadata. Preserve fixture
selection and source edits owned by other work. Before an experiment changes
selection, record its exact current state and use the fixture's ownership
controls. Stop only owned processes; restore only unchanged owned edits. Never
install over the shared Go binary, change the published pin or delete data.

## Artifacts and Notes

Initial package capture SHA-256:
`7996e1aed4a19b72466a6a024a66173dc3c8efff8bd40ef9a6cbfa816fffa6e5`.
Local capture/report: `.scenery/harness/native-worker/baseline-packages.json`
and `baseline-report.json`. This digest identifies metadata bytes, not a
compiled input identity. Of the 166 native packages, 99 transitively reach
`scenery.sh/runtime`; the report retains their concrete shortest import paths.

Preparation-increment validation on 2026-09-11:

| Command (Scenery root unless noted) | Result |
| --- | --- |
| `python3 -B -m unittest discover -s scripts -p test_native_worker_closure.py` | 8 tests pass |
| Closure command above, baseline only | Reports `not_run`; 619 total / 166 native packages |
| Closure command with the same capture/entrypoint as candidate | Expected exit 2, `structural_gate: fail`, runtime retained |
| `go test ./...` | Pass |
| `go test ./cmd/scenery` | Pass |
| `golangci-lint run ./...` | Pass, zero issues |
| `./scripts/build-dashboard-ui-embed.sh` | Pass; local prerequisite only, no authored UI change |
| `go run ./scripts/verify --summary --write` | Pass with 41 knowledge, 22 architecture and one advisory aggregate Go timing warning (14.769 s); not isolated root timing |
| `git diff --check` | Pass |

An earlier quick run rejected this plan's living-document wording; it was
corrected before the passing default verifier. No product behavior, public
contract or owning instruction changed in that initial preparation increment.
Do not interpret preparation verification as M1/M2 acceptance.

Native API increment validation on 2026-09-11:

| Command | Result |
| --- | --- |
| `go test ./internal/runtimeapp ./runtime ./db . ./scripts/verify` | Pass; moved dotenv ownership test, callback ownership, concurrent span completion and public/native nested-span context proof |
| `go test . ./cmd/scenery ./db ./internal/runtimeapp ./runtime ./scripts/verify` | Final affected-package union passes |
| `go test ./...` | Pass |
| `go test -race ./internal/runtimeapp ./runtime ./db -run 'Test(Application\|Span\|LoadDotEnv)'` | Pass for matching leaf/runtime tests; db has no matching test in this targeted race run |
| `golangci-lint run ./...` | Pass, zero issues |
| `go run ./scripts/verify --summary --write --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime` | Pass; native-contract 10.231 s, dev-process 53.554 s, build-info 0.443 s, assistant-runtime 1.401 s; 41 knowledge and 22 architecture warnings |
| `go run ./scripts/verify --summary --write` | Pass with 41 knowledge, 22 architecture and one advisory aggregate test-time warning (5.957 s); no isolated timing claim |
| `go list -deps scenery.sh` and `go list -deps scenery.sh/db` | Neither closure contains `scenery.sh/runtime`, MCP gateway/federation or assistant runtime |
| `go build -mod=readonly -modfile <Scenery-root>/.scenery/harness/native-worker/onlv.mod -o <Scenery-root>/.scenery/harness/native-worker/onlv-native-leaf ./scenery_internal_main` from the inspected prepared ONLV workspace | Pass; real native closure, no service omitted. Build-only proof; no linked runtime identity or live readiness claim |

`native-leaf-onlv-packages.json` and `native-leaf-onlv-report.json` retain the
current-source graph and failing whole-worker gate. The private modfile copies
the prepared workspace's go.mod/go.sum and replaces only `scenery.sh` with this
checkout. No fixture files or framework selection were changed. Lint initially
flagged a deliberate exact-error-identity comparison and a nil context in a
test; the former has a narrow contract-specific suppression, and the latter
uses a normal context. The final lint passes.

`ARCHITECTURE.md` and the existing verifier import rule document/enforce the
new dependency ownership. Public API names, declarations, JSON, CLI grammar and
generation are unchanged, so schema, user workflow and fixture regeneration
updates are not applicable. Debugger and paired performance acceptance remain
not run because there is still no separate native worker.

Auth boundary increment on 2026-09-11:

`auth` now reaches 264 packages without importing `scenery.sh/runtime`.
The real ONLV capture retains all 166 native packages and 620 total packages;
native packages reaching the host fall from 36 to three (99 in the baseline).
`auth-leaf-onlv-packages.json` and `auth-leaf-onlv-report.json` preserve this
structural result. Whole-worker admission still correctly fails with exit 2.
The remaining direct native host users are health, invoices and house.

The bridge keeps typed handlers, concrete auth pointers, arbitrary context
values and the host's existing JSON/cookie codecs in one Go process. A host
binds once. Registration before binding is delayed until that host is ready;
this does not open a database or initialize the auth service. Using the lazy
standard auth service without a host reports an explicit initialization error.

| Command | Result |
| --- | --- |
| `go test . ./cmd/scenery ./db ./internal/runtimeapp ./internal/authbridge ./auth ./runtime ./scripts/verify` | Pass |
| `go test ./...` | Pass |
| `go test -race ./internal/runtimeapp ./internal/authbridge ./auth ./runtime -run 'Test(Application\|AuthContext\|CurrentAudit\|Standard\|ResolveRefresh\|RuntimeRegistration)'` | Pass |
| `golangci-lint run ./...` | Pass, zero issues |
| `go run ./scripts/verify --summary --write --probe auth --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime` | Pass; auth lifecycle 12.257 s, native-contract 10.366 s, dev-process 52.286 s, build-info 0.372 s, assistant-runtime 1.399 s; 41 knowledge and 22 architecture warnings |
| `go run ./scripts/verify --summary --write --probe auth` after initialization-order preservation | Pass; auth lifecycle 11.970 s |
| `go run ./scripts/verify --summary --write` | Pass; 41 knowledge, 22 architecture warnings and advisory aggregate Go-suite timing 5.953 s |
| ONLV `go build` using the private modfile as above, output `onlv-native-auth` | Pass; no fixture selection or authored inputs changed |

No generator, CLI, JSON schema or public Go spelling changes. Full release,
debugger and paired latency acceptance remain unselected: a separate worker
still does not exist. No performance gain is claimed for dependency extraction.

Native dispatch increment on 2026-09-11:

The internal call registry is now independent of the runtime host. It retains
native callbacks and releases its lock before application code; its snapshots
support the existing composition transaction rollback. JSON input decoding,
trusted invocation checks, package visibility and system-error conversion
remain ordered as before. The host supplies authorization and pipeline behavior.

Durable helpers use the same native step engine as the runtime. The production
store adapter carries the original context and existing JSON persistence
format. Replay suppresses duplicate execution, failed steps may retry, failed
reads/writes do not report success, and callback errors retain their concrete
identity even if saving the failure also fails. Signal dispatch has one owner;
standalone inactive-service errors and service normalization are unchanged.

The ONLV package capture retains all 166 native packages, 622 total packages
and three native packages reaching the host. The whole-worker structural gate
still fails, as expected. `durable` alone reaches 75 packages without the host.
The private-modfile ONLV executable builds; no fixture source or selection was
changed. `native-dispatch-onlv-packages.json` and
`native-dispatch-onlv-report.json` retain the structural evidence.

Affected tests, full Go suite, targeted race and lint passed during extraction.
The first probe run passed PostgreSQL, assistant, build-info and dev-process,
but native-contract correctly rejected source drift (SCN8003) because the
production step adapter and its PostgreSQL probe were still being edited.
Final verification must use the settled source. The PostgreSQL probe now also
executes a step twice through the production store context, checks one callback
execution and reads back the exact persisted JSON result. Final probes passed on the settled source; no worker, transport, debugger
or latency acceptance is claimed.

Final native dispatch validation:

| Command | Result |
| --- | --- |
| `go test ./internal/nativecall ./internal/nativedurable ./internal/durable/store ./durable ./runtime ./scripts/verify` | Pass |
| `go test ./...` | Pass |
| `go test -race ./internal/nativecall ./internal/nativedurable ./runtime -run 'Test(NestedInvocation\|JSONInvocation\|Step\|ContractInternal\|ContractRegistry)'` | Pass |
| `golangci-lint run ./...` | Pass, zero issues |
| `go run ./scripts/verify --summary --write --probe postgres --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime` | Pass; PostgreSQL 60.630 s including native step persistence/replay, native-contract 10.238 s, dev-process 49.400 s, build-info 0.358 s, assistant-runtime 1.255 s |
| `go run ./scripts/verify --summary --write` | Pass; final report in ignored `native-dispatch-default.log` |
| ONLV `go build` using the private modfile above, output `onlv-native-dispatch` | Pass |
| `git diff --check` | Pass |

Verifier warnings include 41 document-review warnings and 22 architecture
warnings. The existing durable store file remains over the 1000-line warning
threshold (1198 lines after extraction); this increment reduces that file and
puts its new adapter in a separate file. No isolated timing audit was requested.
Public contracts and generated fixtures are unchanged, so schema/CLI changes
and compiler/generator fixture regeneration do not apply. Full release,
debugger, real kernel/worker project-list and AB/BA latency acceptance remain
outstanding because the process split is not implemented.

Host import and bootstrap extraction on 2026-09-11:

The application-facing `runtime` package now reaches 205 packages without
`runtime/host`, assistant orchestration or MCP host implementations. Native
calls use immutable process-local owners. The full generated application
explicitly imports `runtime/host` and retains the existing HTTP server, auth,
durable, assistant and MCP behavior. There is no remote-Go protocol or fallback
runtime. The generation semantic revision is now
`sha256:3926cfea05d510bedfd963dad7e8c40b17578607a58a83bbaaaca5de252f6361`;
linker identity keys moved with the host, while the runtime ABI and bundle JSON
shape remain unchanged.

The package-cut checker accepts an explicit original kernel import path when
that path moves (`--baseline-kernel-package scenery.sh/runtime`, candidate
`--kernel-package scenery.sh/runtime/host`). A focused test proves that merely
relocating the host still fails admission. The regenerated full ONLV graph has
625 packages, retains all 166 native packages, and has zero native packages
reaching the host (99 in the original baseline). Whole-entrypoint admission
still fails because the production composition intentionally links the host.

A private copy of the inspected prepared workspace lives under ignored
`.scenery/harness/native-worker/onlv-host-cut`. Current compiler and renderer
APIs read the original app and render 200 Go artifacts plus bootstrap into that
owned copy; they do not publish into the fixture. The private modfile replaces
only Scenery with this checkout. The full app builds as `onlv-host-cut-app`.
All 383 inspected authored native Go/native/embed input files have identical
membership and bytes to the prepared baseline; regenerated contract/library
projections are excluded from this source-byte comparison. The fixture's
selection and original source remain unchanged. Captures/reports use the
`host-cut-` prefix; these checks are not full input-freshness or runtime proof.

A transient CLI-schema test path failure after moving host tests was fixed.
The renderer rejected temporarily modified generated fixture ownership; the
original bytes were restored and the ordinary renderer regenerated current
outputs. `generate --target contracts` was additionally attempted for the house
fixture, but it has no owning Go module (SCN6207), so that extra Go command is
not applicable. Its required TypeScript regeneration passed. No ownership or
schema guard was weakened.

The preceding full native-dispatch executable and the new host-cut executable
have exactly the same 5063 native text symbols (private generated composition
excluded), with zero missing or added symbols. `host-cut-symbol-report.json`
retains the comparison. The checked-in Go conformance artifacts were refreshed
through `RenderGoWorkspaceFiles` for native and assistant fixtures after ordinary
contract publication retired their private composition; this restores the
current renderer-owned schema fixtures without weakening ownership validation.
A root instruction-budget failure was corrected by shortening introductory
wording. The first combined probe run passed auth, PostgreSQL, assistant,
build-info and dev-process, but found stale worktree-postgres TypeScript metadata
and rejected source freshness in native-contract. After regenerating that client,
separate fixture and native-contract probe runs passed. No freshness check was
weakened.

Final host import/bootstrap validation:

| Command | Result |
| --- | --- |
| `go test . ./auth ./db ./durable ./runtime ./runtime/host ./cmd/scenery ./internal/build ./internal/codegen ./internal/generate ./internal/compiler ./internal/parse ./internal/contractagent ./internal/spec ./internal/testsuite ./internal/nativecall ./internal/nativecompose ./internal/nativedurable ./internal/nativeservice ./internal/runtimeapp ./internal/authbridge ./internal/durable/store ./scripts/verify` | All affected packages passed; verifier rerun after the instruction-word-budget fix passed |
| `go test ./...` | Pass, including final default verifier suite |
| `go test -race ./runtime ./runtime/host ./internal/nativecall ./internal/nativecompose ./internal/nativedurable ./internal/nativeservice ./internal/runtimeapp` | Pass |
| `golangci-lint run ./...` | Pass, zero issues |
| `python3 -B -m unittest discover -s scripts -p test_native_worker_closure.py` | Pass, nine tests |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root <fixture> -o json` | Pass for `internal/compiler/testdata/native`, `internal/compiler/testdata/house`, `testdata/assistant`, and `testdata/apps/worktree-postgres` |
| `go run ./cmd/scenery generate --target contracts --app-root <fixture> -o json` | Pass for native and assistant; private conformance files subsequently refreshed through the renderer as described above |
| `bun test internal/generate/testdata/typescript_client_conformance.test.ts` | Pass |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json` | Pass |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json` | Pass |
| `go run ./scripts/verify --summary --write` | Pass; 41 knowledge and 22 architecture warnings |
| `go run ./scripts/verify --summary --write --probe auth --probe postgres --probe fixtures --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime` | Auth 11.974 s, PostgreSQL 63.945 s, assistant 1.371 s, build-info 0.460 s, dev-process 50.640 s pass; fixture/native-contract failures resolved by the next two runs |
| `go run ./scripts/verify --summary --write --probe fixtures` | Pass, 2.358 s |
| `go run ./scripts/verify --summary --write --probe native-contract` | Pass, 10.485 s |
| ONLV private-modfile `go build`, output `onlv-host-cut-app` | Pass; source-byte, package and native-symbol comparisons above |
| `git diff --check` | Pass |

README and the installable skill are intentionally unchanged because this
internal import split does not change the application workflow. Full release,
debugger, authenticated kernel/worker project-list and AB/BA performance
acceptance remain outstanding: the separate process is not implemented. No
isolated all-root timing audit or benchmark was run in this import-extraction
increment; the eventual paired experiment remains explicitly authorized.

Native worker/kernel process experiment on 2026-09-11:

`runtime/worker` is selected only by the explicit experimental renderer. Ordinary
`up`/`build` paths are unchanged. The shared generator preamble and constructor
renderer retain real implementation imports, service methods, dependency
resolvers, config values, typed clients and lifecycle hooks. SQL requirement
literals, runtime SQL supply and metadata resolution are shared with the host.
The worker uses the same native composition and service lifecycle owners.

The experimental kernel compiles the selected binding's current declaration;
it does not yet consume a dynamic declaration plan. It contains no native app
imports. Its endpoint retains the existing HTTP policy, auth and codec owners,
and forwards explicit metadata/input values over a private loopback channel.
Standard auth is exported through `internal/authbridge` and restored as native
`*auth.AuthData`, including session and impersonation fields. Importing auth
straight into the host would create a root-package test cycle; the bridge
preserves the existing dependency direction. Custom native auth data is rejected.

The worker's compiled admission contains only
`projects/binding/projects_list_projects_http`. All 215 operation callbacks are
registered and linked, including unadmitted ones; missing internal/durable
bindings fail through the native owner and stream admission fails before
invocation. No arbitrary Go value or general callback RPC is introduced.
The 8 MiB private unary message cap is an experimental limit, not a claim of
parity with ONLV's larger declared response limits. Native span records are
collected locally, but complete host telemetry/DB tracing integration remains
unconverted.

The private control channel returns compiled contract/input identity and PID
before activation. SQL setup and constructors execute only after activation.
Owner EOF cancels the HTTP base context, drains calls, and joins service shutdown
before worker exit. Kernel owner EOF also stops its framework process. The real
proof uses one owned listener and process per role; no previous write-capable
generation is kept active by this experiment.

Current retained evidence under `.scenery/harness/native-worker/`:

- `worker-onlv-report.json`: structural admission passes, all 166 required native
  packages retained, no host dependency from the complete worker entrypoint.
- `worker-retention-report.json`: 578 worker packages, 331 kernel packages,
  zero native app packages in the kernel. All 383 inspected authored native
  Go/native/embed files have identical membership and bytes to the preceding
  owned host-cut workspace.
- `worker-symbol-report.json`: all 5063 preceding native text symbols retained;
  candidate has 5205 (142 additional symbols, none missing).
- `worker-selected-inputs.json`: selected Go/native/embed/module/toolchain
  capture linked into the worker and checked against its own proof. This ad hoc
  experiment capture is not the production input-discovery/negative-resolution,
  complete target/ABI or artifact-retention proof. It cannot admit M2 by itself.
- `worker-behavior-report.json`: real two-process ONLV proof passes. It applies
  all 39 configured migrations for 37 application SQL bindings in disposable
  PostgreSQL, uses real constructors and standard signed development tokens,
  and checks anonymous/invalid tokens, authenticated tenant separation, SQL
  failure sanitization, cancellation of a query blocked on an owned table lock,
  worker EOF/drain, post-exit rejection and kernel EOF/shutdown. The exact owned
  container is removed after success and failure.

The first activation failed because the actual tasks constructor backfills issue
numbers and the initial proof setup had only project tables. The final setup
applies the fixture's complete configured migration set before activation; no
constructor or SQL call was replaced. The fixture's authored source and selected
runtime remain untouched. Failed run directories retain their bounded logs.

Final worker regression validation passes. The first default verifier rejected
the version suffix in the new private protocol identity and a combined child
instruction-index bullet. The protocol now has one stable name and an explicit
exact schema revision; each child instruction file has its own index entry.
Neither guard was weakened. The ordinary runtime's generated source output is
intentionally unchanged by the shared-renderer refactoring.

| Command | Result |
| --- | --- |
| `go test . ./auth ./db ./durable ./runtime ./runtime/host ./runtime/worker ./cmd/scenery ./internal/build ./internal/codegen ./internal/generate ./internal/compiler ./internal/spec ./internal/testsuite ./internal/nativecall ./internal/nativecompose ./internal/nativedurable ./internal/nativeservice ./internal/nativeprotocol ./internal/nativesql ./internal/runtimeapp ./internal/runtimescope ./internal/authbridge ./internal/durable/store ./scripts/verify` | Pass; `worker-final-affected.log` |
| `go test ./...` | Pass, executed by the final default verifier |
| `go test -race ./runtime/worker ./runtime/host ./internal/runtimeapp ./internal/runtimescope ./internal/nativecall ./internal/nativeservice ./internal/nativecompose` | Pass; `worker-race.log` |
| `golangci-lint run ./...` | Pass, zero issues |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root <fixture> -o json` | Pass for `internal/compiler/testdata/native`, `internal/compiler/testdata/house`, and `testdata/assistant` |
| `bun test internal/generate/testdata/typescript_client_conformance.test.ts` | Pass, 27 tests |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json` | Pass |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json` | Pass |
| `python3 -B -m unittest discover -s scripts -p test_native_worker_closure.py` | Pass, nine tests |
| `go run ./scripts/verify --summary --write` | Pass; full Go, vet, schema, knowledge and architecture checks; 41 knowledge and 22 architecture warnings |
| `go run ./scripts/verify --summary --write --probe fixtures` | Pass, 2.700 s; run before the other probes to settle ignored fixture output |
| `go run ./scripts/verify --summary --write --probe auth --probe postgres --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime` | Pass: auth 10.875 s, PostgreSQL 63.785 s, native-contract 11.133 s, dev-process 51.775 s, build-info 0.424 s, assistant-runtime 1.384 s |
| `python3 -B .scenery/harness/native-worker/build-worker.py` | Both real ONLV worker/kernel Go builds pass; selected-input manifest and independent linked worker proof retained |
| `python3 -B .scenery/harness/native-worker/prove-worker.py` | Real ONLV authenticated/SQL/in-flight cancellation/owner-shutdown checks pass; exact owned container removed |
| `scripts/native-worker-closure.py` with the retained baseline/candidate arguments | Complete worker structural admission passes; latest native symbol comparison also retains all 5063 baseline symbols |
| `git diff --check` | Pass |

The ONLV fixture's live Git status still contains only the preexisting untracked
`development/measure-latency.ts`; no authored fixture file was modified. Full
release, debugger/streaming/complete target acceptance and the six AB/BA pairs
remain outstanding. README and the installable skill are intentionally unchanged
because this experiment is not selected by the user-facing application workflow.

## Interfaces and Dependencies

The experiment uses Python's standard library to read Go JSON metadata. It
adds no product dependency, public CLI/schema, environment knob or alternate
runtime path. `BuildSession`, `FrameworkKernel`, `NativeAppWorker` and
`GenerationArtifact` remain design terms until their milestones are implemented.
The experimental worker is concrete, but does not establish those later owners.

### Full prepared-target experiment integration (2026-09-11)

The committed driver is `scripts/native-worker-experiment`. Build it once from
the selected source; invoking `go run` anew inside every measured sample would
change the producer executable identity and include unrelated tool compilation.
The driver requires the same content-bound framework producer stamp as the
ordinary CLI and rejects stale source before preparation. Its output and cache
must belong to an explicitly owned fixture. Example from
the Scenery root:

```sh
go run ./scripts/verify --summary --write
# Link the driver with -X scenery.sh/internal/build.linkedFrameworkDigest=<digest>,
# using steps[0].summary.framework_source_digest from .scenery/harness/self-latest.json.
# Build once to .scenery/harness/native-worker/experiment-driver from this source.
.scenery/harness/native-worker/experiment-driver --app-root /absolute/owned-app --output /absolute/owned-evidence --mode worker --binding projects/binding/projects_list_projects_http
```

`--mode baseline` runs ordinary `CompileContext` and the same `RetainBinary`
boundary. Worker mode never writes a successful ordinary runtime bundle or
prunes ordinary executables. It adds private generated files only when existing
prepared projections match byte-for-byte, runs the existing default/selected
native verifier, and retains full target patterns plus both entrypoints in the
worker input manifest. It re-discovers both manifests after the build/checker
join before retention. The separate kernel closure supplements, never replaces,
that full target proof. Kernel reuse compares current input and implementation
identity and verifies the content-addressed file's complete digest. A corrupt
matching artifact fails closed.

An initial full-target run captured 1681 input entries and passed actual ONLV
SQL/authentication behavior from independently retained binaries. That first
integration linked the same implementation identity into both processes; this
would unnecessarily relink the framework kernel on every application body edit.
The final candidate uses separate compiled kernel and worker identities. Its
updated live proof, reuse checks and paired results passed behavior but rejected
performance expansion, as recorded below.

The dedicated ONLV source copy is under ignored
`.scenery/harness/native-worker/m2-app`. It contains tracked fixture source,
excludes local environment files, and explicitly replaces only `scenery.sh` with
this checkout. Both declared TypeScript clients were refreshed in that copy.
The original fixture's published module selection could not contain the new
packages; the failed tidy and stale-client diagnostics were preserved, not
suppressed. Original fixture source, selected framework and personal runtime
remain unchanged.

### Final paired decision and validation (2026-09-11)

Measured framework source:
`sha256:b1164eaca778c95d4d54fd8db8a4c5c028fd130fb8d716896763577114fd8a34`.
Fixed, content-stamped experiment driver SHA-256:
`7bd02032a6b2efb23dd7979be5f4f819230badcb72318b5189d7b1b31fc46016`.
The verifier report for that source is retained as
`.scenery/harness/native-worker/m2-measured-source-verifier.json`.

A is ordinary `CompileContext` plus the same retained-binary copy and actual
runtime preflight/start. B uses `CompileNativeExperiment`, the full native
checker, 1681 full-target input entries and a separately verified 687-entry
kernel closure. Every B reuses the same kernel bytes but starts a new kernel
process. Every A/B gets a different real project-summary body edit, a distinct
new native executable and its first execution inside the timed path. Both
variants use the same owned PostgreSQL setup, complete configured migrations,
real constructors, auth tokens and independent retention. No measured executable
was warmed; separate warmup builds populated shared caches before sampling.

| Pair/order | Baseline A (ms) | Worker B (ms) | B minus A (ms) |
| --- | ---: | ---: | ---: |
| 1 / AB | 5221.324 | 6040.568 | 819.245 |
| 2 / BA | 5045.958 | 5938.749 | 892.791 |
| 3 / AB | 4988.801 | 5864.865 | 876.064 |
| 4 / BA | 4954.021 | 6007.460 | 1053.439 |
| 5 / AB | 5007.608 | 5921.222 | 913.613 |
| 6 / BA | 4970.490 | 5952.215 | 981.725 |
| Median | 4998.205 | 5945.482 | 947.278 difference of medians |

Ranges are 4954.021–5221.324 ms for A and 5864.865–6040.568 ms for B. This
scope does not supply a new full-loop remainder for 0179; its frontend/assistant
and complete supervisor work is absent. Do not subtract these medians from the
historical whole-loop observations or claim the unchanged numerical target.

Median preparation/build/retention wall time was 3971.925 ms for A and
5017.025 ms for B. Recorded `go build` durations were 1516.341 and 1359.635 ms.
Input discovery/fingerprinting is more expensive in B, while the native checker
still runs. Individual step durations can overlap and must not be added into a
synthetic critical path. The measured complete path is the decision authority.

Raw evidence lives under `.scenery/harness/native-worker/`:

- `m2-paired-report.json` and `pairs-0563abcd60/`: all warmups and twelve measured
  samples, exact source/executable/input/target/process identities, stage traces,
  distinct returned summaries and child exit codes. Every measured assertion
  passed, all children exited zero and the owned container was removed.
- `m2-input-retention-report.json`: each pair has identical membership for all
  509 non-private application package inputs; only the semantic source differs.
- `m2-measured-symbol-report.json`: 5063 baseline versus 5205 worker native text
  symbols, zero missing. `m2-closure-report.json` retains the 166-package gate.
- `m2-kernel-reuse-report.json` and `m2-verified-behavior-report.json`: real edit
  changes worker/implementation identity while preserving kernel bytes; SQL/auth
  and in-flight cancellation/owner-EOF proofs pass.
- `m2-warmup-first-report.json`: an unsuccessful warmup exposed incorrect Python
  fd-3 inheritance. The corrected owner passes fd 3 explicitly and restores its
  own descriptor. `m2-warmup-success-report.json` preserves the successful warmup.
- `m2-setup-failure-report.json`: a preparation-only PostgreSQL socket-bootstrap
  race. Waiting for the final TCP listener fixed readiness. There were no
  measured samples in that failed setup; no measured sample was discarded.

Validation commands ran from the Scenery root. Full verifier includes the
repository `go test ./...` suite and supersedes the quick verifier.

| Command | Result / evidence under `.scenery/harness/native-worker/` |
| --- | --- |
| `go test . ./auth ./db ./durable ./cmd/scenery ./internal/authbridge ./internal/build ./internal/codegen ./internal/compiler ./internal/durable/store ./internal/generate ./internal/nativecall ./internal/nativecompose ./internal/nativedurable ./internal/nativeprotocol ./internal/nativeservice ./internal/nativesql ./internal/runtimeapp ./internal/runtimescope ./internal/spec ./internal/testsuite ./runtime ./runtime/host ./runtime/worker ./scripts/native-worker-experiment ./scripts/verify` | Pass; `m2-final-union.log` |
| `go test -race ./internal/build ./runtime/worker ./runtime/host` | Pass; `m2-race.log` |
| `golangci-lint run ./...` | Pass, zero issues; `m2-complete-lint.log`. Initial QF1001 and an unused forwarding helper were fixed. |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json` | Pass; `m2-fixture-native.json` |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json` | Pass; `m2-fixture-house.json` |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json` | Pass; `m2-fixture-assistant.json` |
| `bun test internal/generate/testdata/typescript_client_conformance.test.ts` | 27 tests pass; `m2-ts-conformance.log` |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json` | Pass; `m2-ts-clients.log` |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json` | Pass; `m2-ts-catalog.log` |
| `python3 -B -m unittest discover -s scripts -p test_native_worker_closure.py` | Nine tests pass; `m2-python-tests.log` |
| `go run ./scripts/verify --summary --write --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime` | Pass; 11.880 s / 57.413 s / 0.474 s / 1.653 s respectively; `m2-probes.log` |
| `go run ./scripts/verify --summary --write` | Pass before measurement; `m2-premeasurement-default.log`, with 41 knowledge and 22 architecture warnings and an advisory aggregate Go-suite timing warning (6.479 s). |
| `python3 .scenery/harness/native-worker/prove-verified-worker.py` | Real retained executable SQL/auth/error/cancellation/owner-EOF proof passes; `m2-final-behavior.log` |
| `python3 .scenery/harness/native-worker/measure-pairs.py` | Final six alternating pairs pass all identity/behavior assertions; performance gate rejects B; `m2-measurement-final.log` |

Ordinary runtime behavior and user-facing commands remain unchanged by this
integration, so README and the installable SKILL were intentionally unchanged.
The shared retention helper preserves its previous semantics; existing session
retention tests and the dev-process probe cover it. SQL provider/worktree
allocation and parallel supervisor ownership were not changed in this increment;
prior M1 provider probes remain recorded above, and the new private SQL/process
boundary was exercised with the owned proof. Full release, isolated all-root
100 ms auditing, debugger, streaming/custom-auth/durable parity, resource-cost
qualification and full ONLV frontend/assistant smoke/AHJ acceptance were not
selected because the candidate was rejected before production promotion.
The original ONLV fixture still has only its preexisting untracked
`development/measure-latency.ts`; its published pin and personal runtime were
not changed. Every task-owned process/container was stopped and only matching
owned semantic edits were restored. No commit or push was requested.
