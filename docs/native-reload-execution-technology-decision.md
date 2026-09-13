# Native Reload Execution Technology Decision

Status: evidence checkpoint; no alternative selected or implemented.

## Decision

Do not integrate a process-per-edit stock-Go executable island into `scenery up`.
The real ONLV AHJ implementation island is dramatically smaller than the current
monolith, but it does not pass the predeclared native replacement gate. Keep the
Plan 0180 snapshot/SDK/artifact-safety work and stop optimizing Scenery
orchestration around whole native executables.

Scope clarification: only the Plan 0181 execution experiment was not promoted.
PR #195 already changes production preparation and shared executable caching in
Plan 0180. The experiment's NO-GO evidence does not certify those paths; their
reviewed input-ownership/publication and recovery repairs are tracked in
[Plan 0182](plans/0182-shared-build-input-ownership.md).

No dynamic execution technology is selected by this document. The next
architecture experiment must compare one candidate against the exact real island
and contracts below before it can change production ownership.

## Evidence

The corrected measurement is bound to Scenery commit
`50d89dad1892e75fb5b505e1d4db51a64d4ac225`, framework source digest
`sha256:006a4e04513adc03b6f1978ea4d5c2f2c18c73c1a65b6f5f3d9b360e7079cd5c`
(including the dirty experiment harness),
ONLV commit `4f8126a3e3806b7100ab7efaca1b7dd06b894221`, Go 1.27.0,
darwin/arm64, and an Apple M2 Ultra with 64 GiB RAM and 24 logical CPUs.
The exact framework executable digest is
`sha256:cb42b62898980bdfcc14fe8936f1c6950afef1f5370465eac9fa50547a3cd48e`.
Existing Go caches were retained; load averages were 8.07 / 6.51 / 5.46.

The selected real implementation is `clean.tech/solar/ahjs`, operation
`ahjs/operation/list_ahjs`. It uses the generated typed input/outcome and
constructor contract, Scenery's root SDK types, the real authored implementation,
sqlc-generated queries, `pgtype`, and the SQL dependency shape. The closure
experiment supplied a rejecting SQL implementation because each invocation
exercised the handler's typed validation path before database access. It did not
claim a real database transaction or production equivalence.

| Artifact or boundary | Result |
|---|---:|
| ONLV monolith closure | 620 packages |
| Plan 0180 rejected composition worker | 619/620 packages; 51,853,250 / 51,974,626 bytes |
| Current direct monolith diagnostic build | 620 packages; 157,488,018 bytes |
| Current direct composition-worker reproduction | 620 packages; 156,125,858 bytes |
| AHJ implementation island closure | 238 packages |
| Corrected AHJ implementation island | 8,166,370 bytes |
| Monolith packages absent / net closure reduction | 383 / 382 (one new island main) |
| Five unique implementation edits, build p50 / p95 | 518.559 / 567.220 ms |
| Build mean, versus historical 0180 monolith mean | 524.286 / 529.116 ms |
| New artifact launch + attestation + constructor-ready p50 / p95 | 401.598 / 417.637 ms |
| Build start through verified new typed response p50 / p95 | 934.457 / 977.022 ms |
| Edit through typed response including experimental capture p50 / p95 | 1,303.663 / 1,393.062 ms |
| Steady pipe ping p50 / p95, 100 calls | 0.041 / 0.069 ms |
| Steady typed invocation p50 / p95, 100 calls | 0.061 / 0.106 ms |
| Separate unique-edit AHJ / main compiler commands | 29.294 / 32.036 ms |
| Separate unique-edit linker command | 145.071 ms |
| Separate diagnostic complete `go build` | 516.853 ms |
| Initial owned-worktree build, existing caches | 513.225 ms |
| Initialized RSS | 19,988,480 bytes |

Two separate warmups preceded five run-unique error-message literal edits in the
real `ListVNext` handler. Each response proved the new compiled message. The
worker's generation/owner/build record was linked into the artifact; its digest
was independently read from its executable, not echoed from a request. Ready
followed the real constructor, not just entry to `main`. Wrong identity,
pre-activation invocation, an actual predecessor presented as the candidate,
constructor failure and cancellation all passed their negative checks. No
identity or behavior failure occurred in this corrected experiment. The 934 ms
native p50 still exceeds the 250 ms gate by 684 ms; the predeclared first-five
rule stopped continuation to 30. A five-sample p95 is early evidence, not final
acceptance.

The separate Go action graph attributes two compiler commands and one linker;
no unrelated framework package was recompiled. Enclosing AHJ/main/link actions
took 32.727 / 34.165 / 165.996 ms. Command and action intervals must not be added
as sequential latency. The full driver boundary still took 516.853 ms; the
remaining interval is not fully attributed to individual Go internals. This
diagnostic did not add tracing overhead to the decision series.

A separate init trace reached the final package initializer around 22 ms,
including about 20 ms and 13.4 MB allocated by `internal/spec`. Actual AHJ
construction took 0.001625 ms and self-executable hashing 3.723 ms in that
diagnostic. Neither construction nor low-latency pipe transport explains the
roughly 400 ms first-launch boundary. The evidence does not identify the OS
mechanism responsible for all of that remaining delay.

Corrected raw evidence is under
`.scenery/harness/minimal-native-reload/attested-2575520164/`; `report.json`
SHA-256 is `e244f723b1fc526b549c9f7a11bdc56975385ac591dc793ed33d1fca768d775a`.
The reproducible command and exact executable/cwd are in
[Plan 0181](plans/0181-minimal-native-reload-artifact.md#corrected-reproducible-checkpoint).
All owned processes and the disposable worktree were removed; original ONLV
source/services/data were not changed. The app harness passed its check/inspect
lane without a running runtime; absent observability is not runtime proof.

The earlier ad hoc evidence under
`.scenery/harness/minimal-native-reload/20260913-onlv-island/` remains historical:
531.280 ms build p50, 397.648 ms launch/initial-handshake p50, 24.992 ms same-inode
warm launch, 496.424 ms link-only `go build`, and 4.42 s private-cache cold build.
Its response echoed requested generation and its initial handshake preceded the
constructor. It proved changed behavior but not artifact-owned identity or
activation; its 928.710 ms sum must not be described as verified replacement.
The historical direct command replays cannot establish an exact additive
attribution of the remaining build time. These results describe this machine,
not a universal stock-Go floor or an interleaved improvement comparison.

## Required Semantic Boundary

Any alternative must preserve the private semantic ABI defined by Plan 0181:
exact operation and binding identity, principal and tenant, request metadata,
invocation/execution/deployment/caller identity, deadlines, cancellation, trace
context and application spans, generated typed input/outcomes, system failures,
internal-call policy, and stream ownership/backpressure. It must preserve real
in-process SQL handles and transactions rather than proxying them as transparent
RPC.

Framework producer, host generation, contract revision, build-input revision,
implementation-island revision, artifact digest, execution generation, and
worktree/session owner remain separate identities. Existing public fields keep
their documented meaning.

## Alternatives for a Separate Experiment

| Technology | Why it could change the measured floor | Principal risks and required proof |
|---|---|---|
| Standard Go plugin-like loading | Keeps the host process alive and can share Go values directly, removing process launch and IPC from the happy path. | Local Go documentation warns that plugins cannot be closed, initialize packages once, have poor race-detector support, require exact toolchain/flags/common sources, and are supported only on Linux, FreeBSD, and macOS. Measure real AHJ plugin build/load, unique generations, symbol identity, debugger behavior, failure containment, memory growth, and cross-platform deployment separation before selection. |
| Recyclable c-shared loader process | A stable loader could isolate crashes and load a smaller C ABI library without replacing the public host listener. Scenery already knows how to build explicit c-shared libraries. | The library carries a Go runtime, C framing can erode typed semantics, unloading Go shared libraries is not assumed safe, and build/link time may be worse. Measure real island build/load, repeated generations, runtime coexistence, callbacks, cancellation, streams, SQL/native state, debugging, and bounded loader recycling. |
| Alternative incremental Go compiler/backend | Could remove the stock `go build` package/action and link floor while preserving a process boundary. | Must compile ordinary Go and cgo accurately, preserve standard toolchain ABI/debug information/race behavior, accept current generated code, and remain supportable without an external mandatory service. First qualify source compatibility and real island artifacts; do not infer it from a toy benchmark. |
| Development-only interpreted or JIT execution | Could replace handler bodies without native linking. | Largest semantic gap: ordinary Go language coverage, generics/reflection/cgo, initialization, debugger behavior, races, SQL/native libraries, and exact generated contract types. It is inadmissible unless it runs unchanged application source and passes the same native contracts. |
| Non-Go reload/loader machinery | A small loader or coordinator in another language might improve dynamic library lifecycle or crash isolation. | It does not by itself reduce Go compilation/linking and must not become a new application language, declaration frontend, generic build system, or public runtime. Evaluate only as machinery around a separately proven Go execution artifact. |

## Next Evidence Gate

If another spike is authorized, standard Go plugin-like loading could test
whether removing process replacement changes the measured floor while retaining
typed in-process Go values. That is a hypothesis for comparison, not a selected
technology, production recommendation or implementation authority.

Before any production change, that experiment must predeclare thresholds, use
at least five unique implementation edits before a 30-edit series, retain every
failure, and prove exact toolchain/common-dependency identity. A result is not
promotable until race/debug limitations, non-unloadable generations, memory
retention, initialization, rollback, SQL, internal calls, streaming, worktree
isolation, and standalone deployment have explicit answers.

If plugin build plus first `Open` remains above 250 ms or cannot meet those
contracts, stop again. Compare c-shared loader and compiler/backend evidence
without implementing a permanent fallback mode.
