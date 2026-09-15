# Unloadable Island Reload

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Plan 0187 rejected standard Go plugins for development reload: the real ONLV
AHJ implementation plugin retained its 238-package closure, built and opened
more slowly than an executable island, and could never be unloaded, so seven
generations grew host RSS by 24.5 MB. The human asked to test plugins again
with an implementation that can release a replaced generation from memory.

This plan compares boundaries whose previous generation can actually be
released, using the same real AHJ implementation, generated typed codec,
validation-only rejecting SQL capability, unique edits and gates as Plan 0187:

- A. In-process WebAssembly: the island compiled with `GOOS=wasip1` as a
  reactor module exporting typed entry points, executed by the wazero runtime in
  a stable Go host. Unloading closes the module instance and its compiled code.
- B. In-process Go `c-shared` library: `dlopen` a new library generation and
  `dlclose` the previous one from a stable host.
- C. Out-of-process plugin: a small island executable per generation, built with
  the development link policy from Plan 0198 (`-w`) and replaced by process
  exit, which returns all of its memory.

This is repository evidence for an architecture decision. It adds no product
runtime mode, public API, `.scn` surface, configuration key or environment
variable.

## Progress

- [x] (2026-09-15) Read Plans 0181, 0187 and 0189, the plugin/process harness
  templates under `scripts/verify/testdata/`, and
  `docs/native-reload-execution-technology-decision.md`.
- [x] (2026-09-15) Stage 1 feasibility probes for A, B and C against a copy of
  the generated ONLV build workspace; evidence below.
- [x] (2026-09-15) Screen decision: A and B rejected; C releases memory but
  misses the build-to-response gate at its measured floor. No candidate passes
  every gate, so Stage 2 was not started.
- [x] (2026-09-15) Updated `docs/native-reload-execution-technology-decision.md`,
  ran validation and closed the plan.

## Surprises & Discoveries

- The real AHJ island does not compile for `wasip1` against the framework:
  `scenery.sh/runtime` imports `scenery.sh/storage`, whose
  `internal/storagefs` has only `darwin || linux` file and lock primitives.
  One probe-only stub file in a scratch copy of the framework module (returning
  "storage is unavailable") was enough for the full 325-package wasip1 closure
  to compile into a 17,423,566-byte reactor module.
- wazero's compiler mode needed 3,829–3,865 ms per generation to compile that
  module and 302–319 ms to instantiate it; activation took at most 0.1 ms and a
  typed invocation 0.9–1.1 ms. Interpreter mode compiled in 331–402 ms but
  instantiation, which runs Go runtime initialization, took 10,684–10,868 ms,
  and an invocation 5.7–5.9 ms. Each generation returned its own edited
  validation message, for example `[wasm-gen-3]`.
- Closing wazero modules does release generations for reuse: host RSS was
  368.4 MB with the first generation loaded, then plateaued at 421.7 MB after
  close and 488.8 MB while loaded for generations four through six.
- `dlclose` of a Go `c-shared` library returns 0 and removes the image from the
  dyld image list, but the library's Go runtime heap stays resident. A toy
  library holding a 64 MB heap grew the C host from 1 MB to 69, 135 and 202 MB
  across three loads, keeping 68, 134 and 201 MB after each `dlclose`. Reopening
  a closed library started a new runtime rather than reusing the old one. The
  host did not crash during three-second pauses, but nothing was released.
- For the process island, direct replay of the recorded tool commands measured
  32–34 ms compiling `clean.tech/solar/ahjs`, 25–26 ms compiling `main` and
  187–196 ms linking with `-w`, plus 14–25 ms copying the executable. Stock
  `go build -ldflags=-w` took 478–562 ms. The executable was 10,262,706 bytes.
- The first execution of each new executable dominated the process boundary
  from this probe's shell: 513–560 ms after direct replay and 428–444 ms after
  stock builds, versus 32–35 ms for a second execution of the same file. Even a
  byte-identical copy at a new path cost 368–545 ms on first execution, so macOS
  applies this per new file rather than per code-signature hash. The probe
  shell's responsible application is the Claude desktop app. Plan 0189 measured
  34 ms first-ready with the launching app enabled for Developer Tools, and the
  Plan 0198 benchmark's supervisor-launched 40 MB candidates took 65–93 ms.

## Decision Log

- Decision: keep Plan 0187's gates (build p50 at most 200 ms, load plus
  activation p50 at most 50 ms, build-to-new-typed-response p50 at most 250 ms)
  and add a release gate: after seven sequential generations, host RSS must
  exceed its one-generation loaded RSS by less than one generation's loaded
  delta. Rationale: the new question is unloadability without giving up the
  latency target that rejected earlier boundaries. Date: 2026-09-15. Author:
  Claude.
- Decision: screen candidates with Stage 1 probes before writing a repository
  benchmark. Rationale: each candidate has a cheap decisive failure mode
  (wasip1 compilation or runtime compile cost for A, runtime unloading for B,
  build/launch floor for C); a full harness is only justified for a survivor.
  Date: 2026-09-15. Author: Claude.
- Decision: reject in-process WebAssembly (A) for ordinary reload. Rationale:
  it releases replaced generations, but a 0.5 s wasip1 build plus 3.8 s module
  compilation and 0.3 s instantiation per edit misses the 250 ms gate by more
  than fifteen times; interpreter mode is slower still. It also needs wasip1
  support in framework platform layers and cannot share SQL handles with the
  host. Date: 2026-09-15. Author: Claude.
- Decision: reject Go `c-shared` libraries with `dlclose` (B). Rationale: the Go
  runtime inside each library keeps its heap after `dlclose`, so every
  generation accumulates memory exactly like a standard plugin. Date:
  2026-09-15. Author: Claude.
- Decision: record the out-of-process island (C) as the only unloadable
  candidate near the target, but do not build Stage 2 under this plan. Rationale:
  process exit releases everything, direct tool replay reduces the build to
  about 250 ms (link 190 ms of it), yet adding even the fastest first launch
  observed on this machine (34 ms in Plan 0189) plus island input validation
  leaves an estimated 300–400 ms build-to-response floor above the 250 ms gate.
  Reaching the gate requires a smaller link closure or a lower launch floor;
  the human decides whether that architecture work is worth pursuing. Date:
  2026-09-15. Author: Claude.

## Outcomes & Retrospective

Completed on 2026-09-15 as a Stage 1 architecture screen on the Apple M2 Ultra
(Go 1.27.0, darwin/arm64, load average about 9–11). No candidate passes every
gate; Stage 2 was not started and no product or benchmark code was added.

| Candidate | Releases replaced generation | Build per edit | Load / first execution | Build to typed response | Result |
|---|---|---:|---:|---:|---|
| Plan 0187 standard plugin | no (+24.5 MB for seven) | 1,333.538 ms p50 | 453.407 ms p50 | 1,814.363 ms p50 | rejected earlier |
| A: wasip1 module in wazero (compiler) | yes, RSS plateau | 475–515 ms | 4,132–4,184 ms compile + instantiate | about 4.6 s | reject |
| A: wasip1 module in wazero (interpreter) | yes, RSS stable | 475–515 ms | 11,014–11,270 ms | about 11.5 s | reject |
| B: Go `c-shared` with `dlclose` | no (runtime heap stays) | not measured | not measured | not measured | reject |
| C: process island, stock `go build -w` | yes, process exit | 478–562 ms | 428–444 ms | about 0.9–1.0 s | fails gates |
| C: process island, direct tool replay | yes, process exit | 262–278 ms | 513–560 ms from this shell | 781–838 ms | fails gates |

The out-of-process island is the only boundary that both releases memory and
has a plausible path toward the latency target. Its remaining costs are the
238-package link (about 190 ms with `-w`) and the macOS first execution of every
new executable, which varies by launching context from 34 ms to more than
500 ms on this machine. A future plan would need to shrink the island link
closure, measure launch inside the supervisor's real context, and design
island-owned SQL state before any product integration.

Evidence lives in the session scratchpad under `unload-probes/`:
`probe-a-wasm.sh`, `probe-b-cshared.sh`, `probe-c-process.sh` and
`probe-c2-replay.sh` with their island, host and framework-stub sources. The
probes used a copy of the generated ONLV workspace from
`~/Library/Caches/scenery/build/clean-tech-dbe32ecb1fa34268` (framework
`scenery.sh v0.3.7-0.20260910222939-de2d81028baf`) and restored
`solar/ahjs/service.go` and `go.mod` after each probe. No ONLV checkout, running
session or Scenery source file was modified. A host restart later on
2026-09-15 cleared that scratchpad, so the measurements recorded in this plan
are the retained evidence; rerunning requires recreating the probe sources.

Validation: `go run ./scripts/verify --summary --write` from
`/Users/petrbrazdil/Repos/scenery` after the documentation changes, and
`git diff --check`.

## Context and Orientation

The island is `clean.tech/solar/ahjs`, operation `ahjs/operation/list_ahjs`,
invoked through `scenerycontract.UnmarshalListAhjsInput`,
`Service.ListVNext` and `scenerycontract.MarshalListAhjsOutcome` with input
`{"query": "<512 x q>"}`, which returns the validation message edited by each
sample. Plan 0181's executable island template is
`scripts/verify/testdata/native-reload/main.go.txt`; Plan 0187's plugin host and
plugin templates are under `scripts/verify/testdata/native-reload-plugin/`.

A WebAssembly reactor is a module without a blocking `main` that initializes via
`_initialize` and then serves exported function calls. Go 1.24 and later build
one with `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared` and
`//go:wasmexport` functions. wazero is a pure-Go WebAssembly runtime; its
compiler mode translates a module to native code before instantiation.

A Go `c-shared` library contains its own Go runtime. Plan 0187 showed a stable
host shares nothing with plugin memory it cannot release; B tests whether the
platform can release a library that started a Go runtime.

Every unloadable boundary gives up sharing Go objects such as `*sql.DB` or open
transactions with the host. The technology decision document requires real
in-process SQL handles; a surviving candidate must therefore also explain how
the island owns SQL state before any product design.

## Milestones

Milestone 1 runs the Stage 1 probes and records measurements. Milestone 2
records the screen decision. Milestone 3 implements a repository benchmark for
survivors, following the Plan 0187 harness shape. Milestone 4 records the final
decision in `docs/native-reload-execution-technology-decision.md`.

## Plan of Work

Stage 1 uses a copy of the generated ONLV build workspace in the session
scratchpad so no ONLV checkout or Scenery source changes. For A, add a
`//go:wasmexport` island main, build it for wasip1, and load generations into a
wazero host that records compile, instantiate, activation, invocation and RSS
after closing each generation, in compiler and interpreter modes. For B, build
two tiny Go `c-shared` libraries and load, call and `dlclose` them from a C host
that counts loaded images and resident memory; only if unloading works does B
move to the real island. For C, build the island executable per unique edit with
`-ldflags=-w`, replay the recorded compile and link commands directly, and time
process start through the typed response.

Stage 2 is written only for candidates that pass. It becomes a repository-only
`--benchmark` selector beside `native-reload-plugin` with owned ONLV worktrees,
exact identity records, retained evidence and cleanup.

## Concrete Steps

Stage 1 commands run from the probe directories recorded in Artifacts and Notes
and are reproduced there with their outputs. Stage 2 steps are added to this
section before any repository benchmark code is written.

## Validation and Acceptance

Stage 1 acceptance is the recorded probe evidence for A, B and C with an explicit
pass or fail against each gate. Stage 1 changes no repository code; the only
repository changes are this plan, `docs/plans/active.md` and
`docs/knowledge.json`, validated with
`go run ./scripts/verify --quick --summary --write` from
`/Users/petrbrazdil/Repos/scenery`.

If Stage 2 adds benchmark code, its validation adds `go test ./scripts/verify`,
`go test ./...`, `golangci-lint run ./...`,
`go run ./scripts/verify --summary --write`, and the new benchmark command
against `/Users/petrbrazdil/Repos/onlv` with `--write`. Stage 2 is skipped when
Stage 1 rejects every candidate; the Decision Log then records the rejection
evidence.

## Idempotence and Recovery

Stage 1 probes write only beneath the session scratchpad and the Go build cache.
They start short-lived processes and stop them before returning. Rerunning a
probe rebuilds its generations from the restored original
`solar/ahjs/service.go` copy.

## Artifacts and Notes

Probe sources and logs live in the session scratchpad under `unload-probes/`
and are summarized here as they complete.

## Interfaces and Dependencies

Stage 1 uses wazero v1.12.0 from the local module cache inside a scratch module;
it adds no dependency to the Scenery module. A Stage 2 benchmark that needs
wazero must record that dependency decision here before adding it.
