# Development Link Latency

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Every compatible development edit relinks the complete application executable.
For the full ONLV application the Go linker itself needs about 0.8 seconds on
an unloaded Apple M2 Ultra performance core, yet the Plan 0197 benchmark
reported a 2,175 ms link p50. This plan removes the avoidable part of that gap
without changing the retained compiler model:

1. Scenery's own import-configuration rewrite in front of each compiler and
   linker invocation must stop scanning the whole file once per archive.
2. A macOS development supervisor that inherited the process-level Darwin
   background policy must remove it before building, and every build request
   must report what it observed.
3. Development executables link without DWARF debugging information, by
   explicit human decision, while ephemeral, production-asset and deployable
   artifacts keep their current linker behavior.

After this work a developer can see a `process.scheduling` build step in
`scenery up -o jsonl`, development executables contain no `__DWARF` segment
unless `build.go_flags` restores it with `-ldflags=-w=false`, and the direct
executor's link phase no longer contains a quadratic text rewrite.

## Progress

- [x] (2026-09-15) Measured the full-ONLV linker, its phases, DWARF variants,
  Darwin scheduling policies, CPU contention, idle effects and the import
  configuration rewrite; recorded the evidence below.
- [x] (2026-09-15) Replaced the quadratic import configuration rewrite with an
  exact line-based mapping covered by
  `TestRewriteImportCfgRebindsExactArchivePathsOnly`. On the real ONLV link
  import configuration (618 mappings) the rewrite fell from 92–94 ms to
  0.07–0.28 ms with byte-identical output.
- [x] (2026-09-15) Added `cmd/scenery/dev_scheduling*.go`: each development
  build request clears an inherited Darwin background policy and records
  `process.scheduling`; the native build benchmark now records each sample's
  `scheduling_policy`, report-level `scheduling_policies` counts and host load
  average before and after the run.
- [x] (2026-09-15) Development builds prepend `-w` to the merged `-ldflags`
  (`developmentLinkerDefaults` in `internal/build/runtime_bundle.go`); a
  configured `-w=false` follows it, and the shared executable key names the
  development linker default.
- [x] (2026-09-15) Updated `docs/local-contract.md`, `docs/agent-guide.md`,
  `ARCHITECTURE.md`, `docs/native-build-compiler-decision.md`,
  `docs/plans/active.md` and `docs/knowledge.json`.
- [x] (2026-09-15) Ran the validation union, the `dev-process` probe, the Darwin
  background acceptance, and one short full-ONLV benchmark; results are in
  Outcomes & Retrospective.

## Surprises & Discoveries

- The Go linker is effectively single-threaded for this workload: 0.98–1.05 s
  user CPU for 0.78 s wall time. `GOGC=off` and `GOGC=400` did not change the
  0.77–0.78 s total.
- Phase attribution for the full ONLV link (`link -benchmark=cpu`, 619
  packages, 52 MB PIE, internal linking, no cgo): archive loading ~210 ms, DWARF
  ~200 ms (generation 66, symbols 32, zlib compression 104), data layout
  ~100 ms, dead-code elimination ~90 ms, pclntab ~57 ms, output writes ~90 ms.
- Variants of the same link: default 0.78–0.82 s and 51.0 MB; `-w` 0.55–0.59 s
  and 39.6 MB; `-compressdwarf=false` 0.73 s and 83.4 MB.
- Scheduling: `taskpolicy -c utility` 0.80 s; 16–24 concurrent busy loops
  0.95–1.09 s; the first link after 25 s idle 0.81–1.09 s; `taskpolicy -b`
  (Darwin background) 2.6–2.8 s; `taskpolicy -c background` (QoS clamp)
  2.8–3.0 s. Only background policies reproduced the 2.2–3.0 s range.
- `getpriority(PRIO_DARWIN_PROCESS, 0)` returns 1 under `taskpolicy -b`, and
  `setpriority(PRIO_DARWIN_PROCESS, 0, 0)` clears it: child links then took
  0.78–0.80 s. A QoS clamp is invisible to that call and clearing it has no
  effect, so it cannot be repaired without cgo.
- `rewriteImportCfg` in `internal/nativebuilddriver/recipe.go` spent 107–112 ms
  applying 618 archive mappings with one `strings.ReplaceAll` pass each over
  the 86 KB link import configuration, inside the measured link phase.
- The linker accepts `-w -w=false`; the later flag restores DWARF sections.
- The first full verifier run failed once in
  `TestSharedDevelopmentBinaryDeduplicatesInflightAndDetachesCanceledWaiter`
  with `prepared workspace membership changed: .shared-binary-<N>`. Under 20
  concurrent busy loops the test failed 14/300 times on this tree and 23/300 on
  unmodified `3b76bbee`, so it is a pre-existing race between workspace
  membership verification and `writeExecutableAtomically`; it was not changed
  here. The rerun passed.
- Editing `testdata/apps/basic` from a session rooted there fails
  `framework.verify`, because the fixture is inside the framework source tree the
  producer was built from. Body-edit acceptance therefore used a copy of the
  fixture outside the repository with an absolute `scenery.sh` replacement.
  The in-place run also regenerated the tracked
  `testdata/apps/basic/service/scenerycontract/scenery.package-generated.json`
  producer record; that file was restored to `HEAD`.
- Inside the short benchmark the link phase stayed near 2 s even though every
  sample reported `darwin_background_absent` and every lane executable was
  40.5 MB without DWARF. The same `-w` link measured 0.50–0.55 s in isolation,
  including when spawned from a Go process, written to a new generation path,
  or run with the hermetic `GOMAXPROCS=2` that `gotarget.Environment` passes to
  builds (0.55–0.60 s). Stock lanes slowed the same way, so the remaining
  inflation belongs to the benchmark environment: load average rose from 7.67 to
  12.67, four lanes each ran a supervisor, application and `frontend-nextnext`
  development server, and unrelated desktop workloads were active. The
  benchmark does not record tool CPU time, so it cannot yet separate waiting for
  CPU from slower cores.

## Decision Log

- Decision: rewrite import configurations line by line and replace only exact
  `packagefile` archive paths. Rationale: import configurations name archives
  only in `packagefile` entries, exact matching removes accidental prefix
  replacement, and the cost becomes linear. Date: 2026-09-15. Author: Claude.
- Decision: clear only the process-level Darwin background policy, once per
  development build request, and report `darwin_background_absent`,
  `darwin_background_cleared` or `darwin_background_clear_failed` in a
  `process.scheduling` step. Rationale: the policy is observable and
  reversible from inside the process, children inherit the corrected state, and
  a later re-application is caught by the next request. QoS clamps are not
  observable without cgo and remain unchanged. No configuration or environment
  selector is added. Date: 2026-09-15. Author: Petr and Claude.
- Decision: development executables link with `-w`, reversing the Plan 0197
  rejection by explicit human decision. Rationale: DWARF costs about 240 ms of a
  0.8 s link; stack traces, panics and profiles use pclntab and keep working.
  Debuggers need DWARF, so Scenery places `-w` before configured linker flags
  and `build.go_flags: ["-ldflags=-w=false"]` restores it without a new knob.
  Date: 2026-09-15. Author: Petr and Claude.

## Outcomes & Retrospective

Completed on 2026-09-15. The three changes are implemented and demonstrated.

Isolated on the Apple M2 Ultra with the full-ONLV link inputs (619 packages):
the import configuration rewrite fell from 92–94 ms to 0.07–0.28 ms with
byte-identical output; the linker fell from 0.78–0.82 s and 51.0 MB to
0.50–0.55 s and 40.5 MB with `-w`; a `taskpolicy -b` launcher cost 2.6–2.8 s
before clearing and 0.78–0.80 s after.

Runtime acceptance: `taskpolicy -b .scenery/harness/bin/scenery up -o jsonl`
against `testdata/apps/basic` recorded `process.scheduling` with
`darwin_background_cleared` (0.034 ms), completed its retained bootstrap and
started the application; the candidate executable had no `__DWARF` segment and
build info `-ldflags="-w -X=...`. A copy of the fixture outside the repository,
also launched under `taskpolicy -b`, then served a body edit through the direct
executor (`retained_compiler`, compile 67 ms, link 274 ms, build request
1,449 ms); its relinked executable had no DWARF and `POST /api/echo` returned
`{"message":"echo-0198-direct:hi"}`. Later requests reported
`darwin_background_absent`. Both sessions were stopped with `scenery down`.

Short full-ONLV observation, before
`.scenery/harness/native-build-compiler/20260915T132011Z-f22d32d363813289/report.json`
and after
`.scenery/harness/native-build-compiler/20260915T154608Z-3f7cf470c47031bb/report.json`
(SHA-256 `7582e4733543a7f8a5a5a9814b4a801bf479332bbc0c273e3b581d36c47101c8`,
ONLV `4f8126a3e3806b7100ab7efaca1b7dd06b894221`, source status unchanged,
owned cleanup passed). Both are one cohort with three measured edits per lane;
they are observations, not statistical comparisons.

| Path | Accountable build p50 before → after | Accepted edit p50 before → after |
|---|---:|---:|
| `GO-BUILD ONLY` | 2,599.317 → 2,426.772 ms | 6,778.089 → 7,149.836 ms |
| `FULL-PREP + GO-BUILD` | 3,650.096 → 3,730.339 ms | 7,776.102 → 8,531.351 ms |
| `RETAINED-PREP + GO-BUILD` | 2,933.540 → 2,648.045 ms | 7,133.020 → 7,721.812 ms |
| `RETAINED-PREP + GO-TOOLS` | 2,833.794 → 2,579.478 ms | 7,126.610 → 7,520.294 ms |

For `RETAINED-PREP + GO-TOOLS`, link p50 moved from 2,175.229 to 1,991.245 ms
and compile p50 from 196.261 to 174.833 ms; its executable shrank from
52,007,602 to 40,482,226 bytes. Response and accepted-edit times rose in the
more heavily loaded after run (load average 7.67 at start, 12.67 at end), so
these rows do not attribute end-to-end change to this plan. The benchmark link
phase remains roughly four times the isolated linker; Surprises & Discoveries
records what was ruled out.

Validation: `go test ./internal/nativebuilddriver ./internal/build ./cmd/scenery ./scripts/verify`,
`go test ./...`, `golangci-lint run ./...` (0 issues),
`go run ./scripts/verify --summary --write` (`pass_with_warnings`: 41
knowledge-contract and 21 existing architecture warnings, after the recorded
pre-existing flake on the first run), `go vet ./cmd/scenery` for darwin and
`GOOS=linux`, `go run ./scripts/verify --probe dev-process --summary --write`
(pass, 182 s), and `git diff --check`. The changed-area union was
`go run ./scripts/verify --summary --write`, `go test ./...`,
`go test ./cmd/scenery`, `go test ./internal/build`,
`go test ./internal/nativebuilddriver` and `go test ./scripts/verify`; all
passed. `VNEXT.md` is unchanged. Release certification, race mode and Linux
measurements were not selected.

Next: record compiler and linker CPU time and involuntary context switches for
direct tool phases, then measure a single full-ONLV session before treating
the remaining benchmark link phase as a linker property.

## Context and Orientation

`internal/nativebuilddriver` owns the retained compiler/linker recipe. Its
`Recipe.compileArgs` and `Recipe.linkArgs` rebind each captured action to
retained archives by calling `rewriteImportCfg`, which writes a per-generation
copy of the Go import configuration (`packagefile <import path>=<archive>`
lines plus an opaque `modinfo` line for links).

`internal/build` owns build policy. `effectiveGoBuildFlags` in
`internal/build/runtime_bundle.go` merges configured `build.go_flags` with the
runtime identity `-X` linker assignments into one `-ldflags` value. That value
reaches ordinary stock builds (`runGoBuildContext`), the retained bootstrap and
graph refresh `go build` commands, and the direct executor, which reuses the
recorded link command. `shouldUseRetainedNativeCompiler` in
`internal/build/retained_native.go` identifies ordinary development builds.
`sharedBinaryKey` in `internal/build/shared_binary_cache.go` names shared
development executables.

`cmd/scenery` owns the development supervisor. `RebuildAndRestart` in
`cmd/scenery/dev_app_start.go` handles every build request and projects
`build.Step` values to `build.step` JSONL events through `emitBuildStep`.

`scripts/verify` owns the explicit native build benchmark. Samples carry the
parsed build steps of their request in `harnessEditLatencyPhase` values.

A Darwin background policy ("darwinbg") is a macOS scheduling state that moves
work to efficiency cores and throttles I/O. It is inherited by child processes.

## Milestones

Milestone 1 replaces the import configuration rewrite. Milestone 2 adds the
Darwin scheduling step and benchmark field. Milestone 3 applies the development
linker policy and cache identity. Milestone 4 updates documents and runs the
validation, runtime acceptance and short benchmark.

## Plan of Work

Change `rewriteImportCfg` to parse lines and consult the archive map once per
`packagefile` entry, preserving every other line byte for byte and still
rejecting a missing archive. Add a platform-specific supervisor helper in
`cmd/scenery` (`dev_scheduling_darwin.go` and `dev_scheduling_other.go`) and call
it at the start of `RebuildAndRestart`. Extend the benchmark sample with the
observed scheduling reason and summarize it in the report. In `internal/build`,
derive a development linker default of `-w` for ordinary development builds,
prepend it to the merged `-ldflags`, and include the same default in the shared
executable key. Update `docs/local-contract.md`, `docs/agent-guide.md`,
`ARCHITECTURE.md`, `docs/native-build-compiler-decision.md`, this plan,
`docs/plans/active.md` and `docs/knowledge.json`.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

1. Edit `internal/nativebuilddriver/recipe.go` and its tests; run
   `go test ./internal/nativebuilddriver`.
2. Edit `cmd/scenery/dev_app_start.go`, add the scheduling helpers and tests;
   run `go test ./cmd/scenery`.
3. Edit `internal/build/runtime_bundle.go`,
   `internal/build/shared_binary_cache.go` and tests; run
   `go test ./internal/build`.
4. Edit the benchmark sample/report in `scripts/verify`; run
   `go test ./scripts/verify`.
5. Update documents, then run the validation below.

## Validation and Acceptance

Expected changed-area classes: `go-package`, `cli-json-contract`, and
`release-sensitive-or-runtime`. The full verifier is selected.

- `go test ./internal/nativebuilddriver ./internal/build ./cmd/scenery ./scripts/verify`
  passes, including exact import configuration rewriting, development versus
  ephemeral/production linker defaults, a configured `-w=false` ordered after
  the default, and scheduling step projection.
- `go test ./...` and `golangci-lint run ./...` pass.
- `go run ./scripts/verify --summary --write` passes (warnings reported) and
  refreshes `.scenery/harness/agent-context.json`; every command in its final
  `changed_area.recommended_commands` union passes.
- `go run ./scripts/verify --probe dev-process --summary --write` passes with
  owned cleanup.
- Darwin background acceptance, from the repository root after the verifier
  built `.scenery/harness/bin/scenery`: start
  `taskpolicy -b .scenery/harness/bin/scenery up -o jsonl --app-root testdata/apps/basic`,
  observe a `build.step` named `process.scheduling` with reason
  `darwin_background_cleared` followed by a successful build, confirm the
  development executable has no `__DWARF` segment with `size -m`, then stop it
  with `.scenery/harness/bin/scenery down --app-root testdata/apps/basic`.
  The acceptance is skipped only when `scenery ps -o json` shows a session
  already owning `testdata/apps/basic`; the plan then records that output.
- The human requested before/after measurement on 2026-09-15:
  `go run ./scripts/verify --benchmark native-build-compiler --workload-root /Users/petrbrazdil/Repos/onlv --summary --write --benchmark-short`
  records link, compile and accountable-build phases plus the per-sample
  scheduling reason. The Plan 0197 report
  `.scenery/harness/native-build-compiler/20260915T132011Z-f22d32d363813289/report.json`
  is the before observation; both are short observations, not release gates.
- `git diff --check` passes and `VNEXT.md` is unchanged.
- Release certification, race mode and Linux measurements are not selected.

## Idempotence and Recovery

All edits are ordinary source changes. Tests use temporary roots. The runtime
acceptance owns only the `testdata/apps/basic` session it starts and stops it
with `scenery down`; rerunning is safe after `scenery ps -o json` shows no
session for that root. The benchmark owns detached ONLV worktrees and cleans
them; a failed run can be repeated with a new run ID. Deleting the retained
development cache is safe and forces a new capture with the new linker policy.

## Artifacts and Notes

Investigation probes lived in the session scratchpad and are not repository
artifacts. Benchmark reports are written beneath the ignored
`.scenery/harness/native-build-compiler/` directory; the final report path is
recorded in Outcomes & Retrospective.

## Interfaces and Dependencies

No dependency, configuration key or environment variable is added. The
`build.step` JSONL event gains the `process.scheduling` step name on macOS.
Development executables change from DWARF-bearing to DWARF-free by default;
`build.go_flags` with `-ldflags=-w=false` restores DWARF. The shared development
executable cache key includes the development linker default.
