# Native Build and First-Execution Attribution

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as work proceeds.

## Purpose / Big Picture

Explain where the real ONLV implementation island spends time building and
executing a newly produced native artifact for the first time. Produce a
revision-bound phase report that distinguishes measured work, overlap, waiting,
and unknown intervals before recommending another execution backend.

The human approved preparing this plan on 2026-09-14, then explicitly requested
implementation and measurement until done. The subsequent instruction limits
the current execution scope to macOS; Linux is deferred, not a completion gate
for this run. This plan does
not authorize production runtime changes, a security-policy change, or edits to
the human-owned `VNEXT.md`. Its 300 ms p50 / 500 ms p95 warm semantic-edit targets
remain unchanged; a warm build still produces an artifact whose first execution
belongs inside the acceptance boundary.

## Progress

- [x] (2026-09-14 11:54Z) Inspected the current process benchmark, its separate
  diagnostics, the execution-technology decision, and primary Go/Apple sources.
- [x] (2026-09-14 11:54Z) Defined the attribution protocol and approval boundaries.
- [x] (2026-09-14 11:59Z) Registered this documentation-only plan and ran the
  required quick verifier; existing naming-policy failures remain unresolved.
- [x] (2026-09-14 12:03Z) Human authorized implementation and measurement, then
  explicitly limited the current execution to macOS.
- [x] (2026-09-14) Implemented the separate attribution lane, clock/interval
  validation, bounded evidence and exact first/repeated artifact checks.
- [x] (2026-09-14) Collected 30 macOS primary pairs and five diagnostic pairs in
  run `attested-1299465927`; all child/worktree cleanup checks passed.
- [x] (2026-09-14 12:45Z) Human confirmed adding ChatGPT to Developer Tools.
  Native Settings showed it enabled before and after confirmation cohort
  `attested-3231842688`; all 30 primary and five diagnostic pairs passed.
- [ ] Deferred by human (2026-09-14): native Linux comparison, outside this run.
- [x] (2026-09-14) Published the phase report with explicit cache, loader,
  scheduler and cross-clock limits; causal outcome is insufficient attribution.
- [x] (2026-09-14) Identified package loading as a measured build investigation
  target; no lower-level command replay, backend selection or promotion made.
- [x] (2026-09-14) Both shared-helper benchmark reruns passed with owned cleanup;
  each retained its `no_go` performance outcome.
- [x] (2026-09-14) Final validation union passed; full verifier retained advisory
  documentation/architecture warnings. An earlier cached-suite duration warning
  did not recur on the final refresh.

## Surprises & Discoveries

- The first implementation run completed 30 primary pairs but rejected its first
  diagnostic because stock-Go trace context IDs are not LIFO execution stacks.
  Concurrent module-fetch spans shared context 0. The corrected parser unions
  intervals by context/name and records multiplicity without invented nesting.
- First-ready p50 changed from 394.459 ms in that incomplete run to 32.354 ms in
  the complete run, already present in its warmups. All 30 successful-run first
  artifacts had unique digests/inodes. This was not a runtime optimization or a
  controlled A/B; platform policy and temporal/cache state remain confounders.
- Go diagnostic loading spans measure 245.780–278.983 ms. Runtime trace profiles
  expose cache and scheduler activity but aggregate concurrent goroutine delay,
  not additive elapsed phases. The report retains these limits explicitly.
- The human approved the narrow protected-target naming exception. Its exact
  governance/catalog references now pass while unrelated names and other rules
  remain checked; the protected file's content hash is unchanged.

- At planning baseline `5a0108bb0b00377177ec49d943d42aebbdbdc286`,
  `nativeReloadBenchmark.initializationDiagnostic` in
  `scripts/verify/harness_self_native_reload_samples.go` runs `inittrace` against
  an artifact already used by the decision series. That repeated execution does
  not diagnose the entire first-execution boundary.
- The same file records a separate action-graph sample. Compiler, linker, and
  enclosing action intervals overlap; their sum is not elapsed driver overhead.
- Plan 0188 already exists in another development worktree. Allocate 0189 without
  renumbering or modifying that worktree's independent work.
- Documentation validation reports seven existing active-next-generation-name
  errors in `AGENTS.md`, its `CLAUDE.md` view, the human-owned target path,
  `docs/index.md`, and the target's existing `docs/knowledge.json` entry. The
  initial run also found this plan's missing living-document statement, now
  added. Naming-policy resolution belongs to the maintainers; changing the
  protected target or the guard is outside this plan-preparation request.

## Decision Log

- 2026-09-14, Petr (confirmation), Codex (observation): the human added ChatGPT
  to Developer Tools. Native UI independently confirmed its enabled state before
  and after a new complete cohort. Preserve unknown policy timestamps for older
  runs and do not infer a controlled causal effect from their timing difference.
- 2026-09-14, Codex: complete the authorized macOS attribution delivery with
  explicit insufficient causal attribution. Linux remains human-deferred and
  lower-level driver experiments remain conditional, not unfinished authorized
  implementation. The measured target still fails and no backend is promoted.

- 2026-09-14, Petr: allow only the protected target's naming exception and its
  exact governance/catalog references, without editing the target content.
- 2026-09-14, Codex: keep the incomplete first run and complete second run
  separate. Record an insufficient-attribution outcome and request the missing
  operator policy observation instead of diagnosing macOS validation from timing.
- 2026-09-14, Codex: do not add an OS trace collector or change OS permissions to
  fill an unknown. The proposed OS-trace limit remains unimplemented; bounded Go
  traces and explicit unknowns satisfy the non-causal report path, while the
  operator observation remains open under the plan completion contract.

- 2026-09-14, Petr: implement and measure the macOS part until done; defer Linux.
  Do not treat the absent Linux cohort as a blocker for this scoped delivery or
  claim any cross-platform result.
- 2026-09-14, Petr (request), Codex (plan): attribution precedes backend selection.
  The existing aggregate NO-GO results remain valid, but do not establish a
  universal stock-Go floor or identify the platform mechanism.
- 2026-09-14, Codex: add a separately selected diagnostic benchmark, preserving
  the existing feasibility benchmarks and their first-five stopping rules. An
  attribution series must not silently rewrite a previous acceptance algorithm.
- 2026-09-14, Codex: compare native macOS with a separately provisioned native
  Linux host. Docker- or VM-emulated Linux is excluded; unavailable evidence is
  reported as unmeasured, not inferred from macOS.
- 2026-09-14, Codex: treat Developer Tools policy as observed launcher metadata,
  not a performance diagnosis or permission to change security settings.

## Outcomes & Retrospective

Completed for the authorized macOS scope, with insufficient causal attribution
and no performance-target attainment. Implementation, measurements, validation
and launcher-policy observation are complete. The
[attribution report](../native-build-first-execution-attribution.md) records
531.287 ms build p50, 32.354 ms first-ready p50 and 964.243 ms edit-through-typed-
response p50 in the complete run. These do not meet the target or promote a
production backend. Linux remains deferred by the human.

The final policy-observed cohort `attested-3231842688` passed in 89.121 s with
549.368 ms build p50, 34.292 ms first-ready p50 and 985.042 ms edit-to-response
p50. Its 30 unique artifacts, same-artifact repeats, negative checks, stopped
children, removed owned worktree and unchanged original checkout were verified.
The operator confirmed adding ChatGPT to Developer Tools; the native UI showed
it enabled before and after the run. Earlier raw policy queries remain denied,
and their historical state/timing is not rewritten. The report and adjacent
policy-observation evidence retain provenance and exact hashes.

Historically, plan preparation changed only this file and its index entries.
Its observed changed-area class was `documentation-only`; its recommended
command union is `go run ./scripts/verify --quick --summary --write`. That command
passes the plan's knowledge/structure checks after adding the living-document
statement, but still fails on the seven pre-existing naming errors recorded
above (41 knowledge and 21 architecture warnings also remain). `git diff --check`
passes. The protected target's SHA-256 is unchanged:
`f62ac5c914a57ec944d7d65a02247f5f30e191b9b19843f3346653e0ad3e7a70`.
Those results describe plan preparation only. The authorized naming exception
subsequently removed all seven errors, and the full verifier passed with the
same 41 knowledge / 21 architecture warnings. Final changed-source results are
recorded below.

Shared-helper regression evidence (each command prefixed by
`go run ./scripts/verify`, from the repository root):

- `--benchmark native-reload --workload-root /Users/petrbrazdil/Repos/onlv --summary --write`:
  PASS_WITH_WARNINGS, experiment 43.790 s, decision `no_go`, evidence
  `.scenery/harness/minimal-native-reload/attested-4066860547/`.
- `--benchmark native-reload-plugin --workload-root /Users/petrbrazdil/Repos/onlv --summary --write`:
  PASS_WITH_WARNINGS, experiment 53.400 s, decision `no_go`, evidence
  `.scenery/harness/minimal-native-reload-plugin/attested-2118584993/`.

Both report all children stopped, owned worktree removed and original checkout
status unchanged. The runs were serial, retained the original stopping/gate
algorithms, and did not change production launch/build code.

Final `go test ./scripts/verify` passes (0.242 s package total, not an isolated
root timing audit). The focused tests also prove that first and repeated
statistics stay separate and invalid pairs are not silently discarded.

Final validation command union: `go test ./scripts/verify`,
`go test ./cmd/scenery`, `go test ./...`, and
`go run ./scripts/verify --summary --write` all passed. The full verifier reports
41 knowledge warnings and 21 architecture warnings, with no assertion or schema
errors. One run recorded advisory cached-suite duration of 6.355 s over 5 s;
the subsequent refresh passed its test step in 2.132 s without that warning. Separately,
`golangci-lint run ./...` reports zero issues and `git diff --check` passes.
Changed-area classes are `cli-json-contract`, `go-package`, and
`release-sensitive-or-runtime`. Documentation-only final bookkeeping is followed
by another full verifier refresh; quick is superseded.

The production `--probe dev-process` is intentionally unselected: the diff
contains verifier/private fixture changes, not `internal/devprocess`,
`internal/build` or production dev orchestration. Full release and all-root
timing audits are unselected workflows, not claimed proof. Native Linux is
deferred explicitly. No generated client/schema/public SDK surface changed.

## Context and Orientation

[The execution-technology decision](../native-reload-execution-technology-decision.md)
records the corrected Plan 0181 five-edit checkpoint: build p50 518.559 ms,
launch/attestation/constructor-ready p50 401.598 ms, and build-start through
verified typed response p50 934.457 ms. Its separate initialization diagnostic
does not account for the remaining first-launch interval. These are historical,
revision-bound observations, not a universal platform baseline.

Reuse ONLV commit `4f8126a3e3806b7100ab7efaca1b7dd06b894221`, package
`clean.tech/solar/ahjs`, operation `ahjs/operation/list_ahjs`, and its generated
typed contract. Every edit changes real compiled handler behavior. The fixture's
rejecting SQL capability exercises validation before SQL; this is not public
HTTP, authentication, database, streaming, debugger, or full runtime proof.

Ownership stays under `scripts/verify`:

- `harness_self.go` owns explicit benchmark selection and workload validation.
- `harness_self_native_reload.go` owns pinned-worktree preparation and cleanup;
  `harness_self_native_reload_inputs.go` owns command/input evidence.
- `harness_self_native_reload_samples.go` owns the current sampling algorithm;
  `harness_self_native_reload_process.go` uses `internal/devprocess.Start` and
  inherited pipes for attestation, activation, readiness, and typed responses.
- `harness_native_reload_protocol.go` and `testdata/native-reload/main.go.txt`
  own the experimental identity and constructor boundary.
- `internal/build/compile.go`, `internal/build/trace.go`, and
  `internal/build/shared_binary_cache.go` are production reference points for
  artifact handling and link admission; this plan does not change their policy.

Go explicitly recognizes lower-level compiler/linker integration for independent
build systems seeking to avoid driver overhead. That makes a constrained
experiment legitimate, not a complete dependency recipe. The compiler consumes
import and embed configuration; the linker consumes package configuration and
build/link settings. Preserve Go-derived inputs rather than inventing rules.
Sources: [Go build documentation](https://pkg.go.dev/cmd/go#hdr-Compile_packages_and_dependencies),
[compiler documentation](https://pkg.go.dev/cmd/compile), and
[linker documentation](https://pkg.go.dev/cmd/link).

Apple documents Developer Tools controls under Privacy & Security. Their
existence supports recording the actual launching application's policy; it does
not establish that this setting causes the observed delay.
Source: [Apple Privacy & Security settings](https://support.apple.com/en-ie/guide/mac-help/mchl211c911f/mac).

## Milestones

1. **Evidence contract and implementation.** The verifier owner adds one explicit
   attribution selector, a bounded phase ledger, deterministic tests, and the
   owning command documentation. No production execution mode is added.
2. **Native macOS cohorts.** After measurement authorization, the verifier owner
   records the real launcher and executes the first/repeated protocol below,
   including separate fresh-artifact diagnostics. The human supplies any policy
   observation that cannot be obtained read-only without elevated access.
3. **Native Linux cohort.** The same owner runs the protocol on the human's
   designated native Linux host and records differences in hardware/toolchain.
   This milestone is deferred by the human and excluded from the current macOS
   delivery. It remains unmeasured, not passed.
4. **Attribution report and decision.** The build/runtime owner reviews critical
   paths and uncertainties, then either proposes the bounded compile/link
   experiment, identifies another specific bottleneck, or reports insufficient
   attribution. None of those outcomes promotes a backend automatically.

## Plan of Work

### Build boundary and interval accounting

Add a separate `native-reload-attribution` benchmark runner using the existing
owned fixture and identity helpers. This selector is now implemented.
Keep its runner and platform observations in focused files such as
`scripts/verify/harness_self_native_reload_attribution.go`; do not expand the
existing feasibility sample loop into a second algorithm selected implicitly.

Record source-write completion, explicit input discovery/capture, admission,
driver start/exit, artifact finalization, first launch, and verified response.
The outer edit-to-response clock includes capture and all required publication
work. Separately label the narrower driver-start-to-exit build interval.

| Build category | Required evidence and limit |
|---|---|
| Package loading and action setup | Distinguish the harness's `go list` from loading, package selection, dependency/action construction inside the driver. Record each separately. |
| Cache validation | Record action-key/input hashing, lookup, hit/miss decisions and cache materialization where observable; a hit is not zero work. |
| Subprocess execution | Record executable identity, exact argv/cwd, start/exit, status, and available CPU time for compiler, assembler, cgo/native tools and linker. Parent action spans are enclosing intervals. |
| Artifact handling | Record import/embed metadata preparation, archive/output writes, build-ID finalization, cache store, copy/rename, permissions and independent parent digest work; locate each inside or outside the driver. |
| Scheduler delay | Separate dependency wait, ready-to-admitted tool work, Scenery link admission, and OS runnable/off-CPU evidence. A bypassed Scenery queue is `not_applicable`, not a measured zero. |

Use parent-process monotonic elapsed time as the end-to-end authority. Each event
has a clock domain, start/end or duration, parent/action ID, category, evidence
source and observation status. Child-local elapsed values can describe child
work but cannot be subtracted from unrelated parent timestamps. Cross-process
placement requires a common OS trace clock or an explicitly bounded handshake;
JSON wall timestamps alone do not establish monotonic alignment.

Build an interval ledger and dependency critical path. Report enclosing wall
time, interval unions, overlapping work, CPU time, and unattributed residuals
separately. Never subtract summed parallel commands or summed phase percentiles
from total wall time. Reject malformed, out-of-range, duplicate or reversed
events; missing observation is `unknown` with a reason, never zero.

Start with the existing Go action graph and version-bound tool/process traces.
A `-toolexec` wrapper can observe actual tool invocations, but its process cost,
version-query behavior and cache effects need their own diagnostic comparison.
It does not expose all driver internals. If package setup or cache spans remain
hidden, first document exact proposed hooks in a disposable, revision-pinned
Go driver build and obtain approval before changing toolchain code. Never
replace the installed Go toolchain or use a modified driver for stock acceptance.

### First-execution boundary and launcher policy

Record process-start request, entry/return around the native start operation,
observable executable-loader/runtime-entry events, Go initialization, entry into
application `main`, self-attestation, activation/constructor start/end, readiness
emission/observation, and the first verified typed response. Preserve the actual
order: attestation precedes authorized construction in the existing fixture.
The parent start-call duration is not automatically pure process-creation time.

An application `init` marker runs after its dependencies, so it cannot represent
entry into all Go initialization. `GODEBUG=inittrace=1` is a separate diagnostic
for package initialization, not a kernel-loader stopwatch; its documented output
has omissions and a version-dependent format. For pre-Go attribution, retain
OS process/loader/scheduling trace events when permitted. If those events cannot
be observed, publish an unresolved pre-observation interval, not an OS diagnosis.
Source: [Go runtime initialization tracing](https://pkg.go.dev/runtime#hdr-Environment_Variables).

Record the real process ancestry, executable paths/digests, launcher application
bundle/version/signature identity, OS build, architecture and translation state.
Record the actual launcher's Developer Tools entry as enabled, disabled,
not-listed, or unknown, with observation time and evidence provenance. A shell
name or another terminal's setting does not identify the responsible GUI
launcher. If responsibility cannot be resolved, preserve that uncertainty.

The hypothesis is that platform loading or validation contributes materially to
first execution. Distinguish loader/page faults, validation, scheduling, Go init,
construction, attestation and observer delay before attributing a mechanism.
Do not change Developer Tools permissions, TCC, Gatekeeper, SIP, quarantine,
signing policy, or shared caches. Read-only metadata inspections that can warm
or assess an artifact belong after its measured first execution unless they
are required by the normal path; record every required pre-launch artifact read.

### Cohorts and measurement protocol

Before execution, freeze the Scenery commit plus dirty source digest, prepared
binary identity, ONLV commit, Go version/tool binaries, generated contract,
build-affecting flags/environment, module/native input inventory, link mode,
artifact path/device/inode/digest/size/signature, and host CPU/RAM/storage/power
and load metadata. Retain the existing package caches and record their state;
do not flush shared caches or stop unrelated developer workloads.

On each authorized native host, run two excluded warmups using their own unique
artifacts, then 30 unique semantic-edit samples. For every measured artifact:

1. Capture the edit, build, and perform the existing required parent digest and
   finalization without executing the candidate as a preflight or smoke check.
2. Launch that new artifact once, verify its linked identity and independent
   digest, activate the real constructor, observe readiness, and verify the new
   typed behavior. This first execution is the acceptance observation.
3. Stop and join that child, then launch the exact unchanged artifact again in a
   new process through the same launcher and protocol. Report repeated execution
   separately, retaining matching digest/path/inode and first/repeat pairing.

New artifact means a new compiled behavior and executable digest, not a renamed
copy of a previously executed binary. First execution does not mean cold disk
or cold OS caches: compilation and required hashing already touch artifact
pages. Retain this distinction and all pre-launch touches in the report.

Keep the primary series free of costly action/OS/init tracing. After it, collect
five additional unique-artifact first/repeat diagnostic pairs with tracing,
using the same workload, flags and launcher. Never explain first-load behavior
solely from a trace of a previously launched primary-series artifact. Report
instrumentation changes and distribution differences; do not pool traced and
untraced samples or subtract one run's diagnostic time from another's latency.

Retain every failure, timeout and exclusion with its reason. Stop on identity,
behavior, ownership, cleanup or evidence-integrity failure; stop the cohort at
20 minutes and retain an incomplete result. Slowness alone does not trigger the
old feasibility first-five cutoff in this separately named attribution lane.
Report count, nearest-rank p50/p95, full samples and paired first-minus-repeat
differences; a partial series cannot pass 30-sample acceptance.

Run the same protocol on native Linux with the same source and Go release,
matching architecture when available. Record native libraries, filesystem and
hardware differences; do not compare Mach-O and ELF digests for equality or
treat cross-host differences as the causal effect of the operating system.
Unavailable Linux access is an explicit missing cohort. An optional comparison
of existing launcher environments requires a recorded human request; never
silently switch launchers or change policy to obtain a faster result.

### Conditional lower-level Go experiment

Advance only after the report identifies substantial driver work outside the
compiler/linker critical path, gives a defensible potential saving including
replacement planning cost, and the human authorizes the experiment. An
unattributed residual alone does not qualify. Compiler/linker dominance or
first-execution dominance instead motivates a bottleneck-specific proposal.

Derive the complete action graph and inputs from the pinned Go implementation:
package selection/build tags, module/workspace replacements, generated source,
transitive archives, import/embed configuration, assembly/cgo/native commands
and headers/libraries, compiler/link flags, toolchain, build IDs and artifact
finalization. Retain exact argv, cwd and input bytes, not shell-log fragments.
Neither `go build -x` nor an old temporary work directory alone establishes a
replayable, complete build. Missing provenance makes the candidate ineligible.

Qualify body edits, transitive dependency edits, restored A-to-B-to-A contents,
file addition/removal and selection changes, embedded data, module replacement,
cgo/native inputs, flags/toolchain changes, interrupted builds and stale-output
rejection against stock Go. Unsupported cases are a restricted experimental
scope, not a production fallback path. Preserve ordinary Go semantics, exact
identity, debug information and existing artifact authority checks.

Before implementing this candidate, add its exact runner command and frozen
conformance cases to this plan. Compare interleaved stock/candidate series with
separate owned artifacts and controlled cache ordering. Include planning,
validation, compilation, linking, publication and first verified execution in
the candidate's total. Do not claim a lower bound from incomplete command replay
or omit ongoing dependency planning costs. This gate selects an experiment,
not a production backend.

## Concrete Steps

All commands below use the Scenery repository root as their working directory.
At preparation time it is `/Users/petrbrazdil/Repos/scenery`.

For this documentation-only change, register this plan in
`docs/plans/active.md` and `docs/knowledge.json`, then run:

```sh
go run ./scripts/verify --quick --summary --write
git diff --check
git diff --stat
shasum -a 256 VNEXT.md
```

After implementation is explicitly authorized, add the selector and focused
files, update `scripts/verify/AGENTS.md`, `docs/harness-engineering.md`, and the
repository-verifier section of `docs/local-contract.md` in the same change.
Keep the existing report envelope; update checked schemas and tests together
if a checked report shape changes. Capture exact launcher/cohort metadata and
register the report document in `docs/knowledge.json` when it is created.

The following command is implemented and was authorized for measurement.
ONLV is read-only input;
the runner creates its own pinned disposable worktree:

```sh
go run ./scripts/verify --benchmark native-reload-attribution --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
```

Before Linux execution, record the human-designated native host, exact Scenery
working directory, and absolute ONLV path here and in its cohort manifest. Run
the same selector there with that path. Until those literal values and access
are supplied, there is no executable Linux step and no Linux acceptance claim.

## Validation and Acceptance

The preparation-only changed-area class was **Documentation only**. Inspect
`.scenery/harness/agent-context.json` and record its current exact
`changed_area.validation_classes` and union of `recommended_commands`. Any
existing failure must retain its diagnostic and ownership; do not modify the
human-owned target or weaken guards to make this plan green.

For the verifier implementation, applicable classes include **One Go package**
(`scripts/verify`), **Documentation only**, and **Release-sensitive or runtime**
for the experimental native boundary. Following Fresh Worktree Preflight in
`docs/agent-guide.md`, run these exact commands from the repository root:

```sh
go test ./scripts/verify
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
git diff --check
```

The full verifier supersedes quick for that implementation. Refresh and execute
the changed-area command union; ordinary tests use the Go test cache. Unit tests
cover clock/interval accounting, pairing, omitted observations, bounds and
report decoding in process under the absolute 100 ms exact-root policy. Native
tools/processes are proved only by the explicitly selected benchmark.

Run the existing `--benchmark native-reload --workload-root
/Users/petrbrazdil/Repos/onlv --summary --write` and
`--benchmark native-reload-plugin --workload-root
/Users/petrbrazdil/Repos/onlv --summary --write`, both prefixed by
`go run ./scripts/verify`, if shared fixture/protocol/preparation helpers change.
They require explicit measurement authorization; otherwise record them pending.
They may be skipped only when `git diff --name-only` proves those shared helpers
unchanged. Their historical NO-GO is not a new regression or permission to
relax gates.

Product launch/build changes are outside the intended scope. If subsequently
authorized changes touch `internal/devprocess` or production dev orchestration,
read the owning instructions, run `go test ./internal/devprocess ./cmd/scenery`
before `go test ./...`, and run
`go run ./scripts/verify --probe dev-process --summary --write`; all named
identity/lifecycle assertions and owned cleanup must pass. Skip that probe only
with a recorded diff showing those production boundaries unchanged. Full
release, all-root timing audits and unrelated benchmark lanes remain unselected;
full release requires a separate request and only `scripts/release-gate.sh`.

Acceptance requires complete first/repeat cohort identity, required lifecycle
negative cases, preserved new typed behavior, bounded owned cleanup, a valid
phase ledger, and a report distinguishing observation from inference. The
negative inventory retains wrong owner/session, ABI, producer, artifact and
generation, invocation before activation, constructor failure, predecessor
substitution and cancellation. Reuse the existing real-process assertions.
The report must list material unknown intervals and whether each conclusion remains
supported without them. The native Linux comparison is explicitly deferred
outside the current macOS delivery and must be reported as unmeasured.
An unresolved bottleneck yields `insufficient_attribution`, not an
invented explanation or a backend recommendation presented as proven.

Report experimental build/launch/combined 200/100/250 ms feasibility gates
separately from the 300/500 ms full semantic-edit target. This private fixture
cannot certify the latter without the ordinary authenticated app path. A warm
second launch never replaces a failed first-launch measurement in either gate.

## Idempotence and Recovery

Use a run-unique owned root and immutable evidence directory under
`.scenery/harness/native-reload-attribution/`. Preserve the existing owner marker,
source identity checks, process-tree cancellation and child joins. Do not adopt
or delete another task's worktree, process, cache or evidence. Verify ownership
before removing the runner's disposable worktree; ambiguous ownership or live
children retain that root and fail cleanup.

A retry receives a new run ID and keeps the failed record; never combine samples
across different source/toolchain/launcher identities. Existing Go caches may be
reused, but record the intervening run and cache ordering. Stop immediately when
the human stops the experiment. Do not install a global CLI, mutate the original
ONLV checkout, change a security setting, or delete shared caches for recovery.

## Artifacts and Notes

Retain a bounded manifest, commands and tool identities, per-sample JSON,
interval ledger, action/OS/init traces, first/repeat pairs, failures, and cleanup
results. Record report/trace hashes and exact reproduction commands. Evidence
limits must reject truncation instead of treating a partial trace as complete;
retain the existing 32 MiB command-output and 64 KiB init-trace caps, add a
128 MiB per-OS-trace cap and a 2 GiB total run-evidence cap, and stop on
exhaustion while retaining the failure manifest.

Create `docs/native-build-first-execution-attribution.md` only when results
exist. Include per-cohort distributions, critical paths, residuals, instrumentation
limits, launcher policy evidence and a claim/evidence/confounder table. Update the
living execution-technology decision with the supported conclusion; do not
rewrite completed Plan 0187 or relabel historical results. Link the exact source
revision and ignored raw evidence rather than committing machine-local state.

## Interfaces and Dependencies

No app-facing SDK, machine protocol, generated contract, production scheduler,
runtime mode or backend is changed by preparing this plan. The proposed selector
is repository-only and stays outside default, quick, race and release runs.
Private diagnostic frames must retain compiled owner/generation identity and
must not allow a requested identity to stand in for an artifact-owned identity.

Dependencies are the pinned ONLV source, current worktree-local Scenery binary,
recorded stock Go toolchain/native tools, read-only launcher observations and an
explicitly supplied native Linux host. No new environment-variable knobs,
mandatory service, browser automation, or subagent workflow is introduced.
