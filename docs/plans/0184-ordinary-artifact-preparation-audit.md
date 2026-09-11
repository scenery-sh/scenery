# Ordinary Artifact Preparation Audit

This ExecPlan is a living document recording the ordinary-build audit and its
separate main-based corrections. Keep Progress, Surprises & Discoveries, Decision Log, and Outcomes &
Retrospective current until acceptance is complete.

## Purpose / Big Picture

Stop worker migration and independently investigate unnecessary work and source
admission in the ordinary development build. Preserve draft PR 193 at
`3edab6878b8c06d69804e68609162e44cde617af` as experimental evidence. Deliver only
ordinary production changes on `feat/ordinary-build-admission`, based on main
`27ebaf12ca87355c6ee7ed340d1be63ffbe814f7`.

The final assignment requires three proofs: shared module inputs are read once
per capture; final admission avoids unused generated-content hashes while
preserving authored identity; rejection after actual cache publication preserves
the serving generation and permits repair. No worker, persistent kernel, runtime
parity, linker tuning, first-execution optimization, or general session framework
is included. The earlier 50 percent startup and edit goals remain open.

## Progress

- [x] 2026-09-11: Verified PR 193 is an open draft at the supplied revision;
  preserved its branch and completed historical plans.
- [x] 2026-09-11: Completed two six-pair ordinary/matched attribution series,
  including the complete artifact interval and exclusive nested work ledger.
- [x] 2026-09-11: Demonstrated repeated module reads and unused generated
  inventory with failing assertions, then implemented bounded corrections.
- [x] 2026-09-11: Extracted ordinary preparation and final source admission onto
  main without worker runtime or temporary instrumentation dependencies.
- [x] 2026-09-11: Passed the real `dev-process` publication, rejection, retained
  serving-generation and subsequent repair journey with confirmed cleanup.
- [x] 2026-09-11: Completed six main/candidate artifact pairs, 24 admission scan
  pairs and 12 same-producer input-capture pairs. Recorded the new guard's cost.
- [x] 2026-09-11: Passed affected/full Go tests, lint, default verifier and the
  combined dev-process/native-contract/build-info probe run.
- [x] 2026-09-11: Published separate [draft PR 194](https://github.com/scenery-sh/scenery/pull/194),
  closed this audit record, and preserved draft PR 193 unchanged.

## Surprises & Discoveries

Main does not contain the experiment's final source-admission check. Consequently,
extracting that correctness check adds work relative to main even after removing
unused generated hashing. Six artifact pairs measured median 3407.895 ms on main
versus 3601.575 ms with this change: **193.680 ms more, or 5.68 percent**. Median
paired change was +212.412 ms; five of six candidates were slower. The remaining
pair had a slow baseline and remains included. This is not a net speedup.

The matched experimental control also performs a complete extra workspace/input
recapture after `CompileContext`. Ordinary product does not do this. Its roughly
388 ms cannot be claimed as a product saving. Metadata preparation was only about
3 ms. Retention copy and pruning varied substantially between series; their
observed cost does not establish that it is architecturally unavoidable.

Earlier measurement attempts encountered full disk and stale generated metadata
in the private ONLV clones after changing the framework base. Failed attempts
remain in the evidence directory. Regeneration repaired only the private clones;
no measured samples were excluded. A package check once failed in unrelated
`TestResolveQueryScopeRequiresExplicitSessionToExist` with a missing Unix socket;
the final affected-package rerun passed. Disk recovery losslessly compressed
owned evidence executables after decompression/SHA verification; manifests retain
original paths, hashes and modes. No global Go cache or original app was deleted.

## Decision Log

- 2026-09-11, user: Stop worker migration and preserve PR 193 as a draft experiment.
  Produce a separate ordinary implementation and audit rather than another runtime.
- 2026-09-11, user: Subsequent feedback supersedes the initial one-intervention
  limit with per-finding assertions, attribution and minimal corrections for the
  three identified cases. Savings enter the budget only after measurement.
- 2026-09-11, agent: Deduplicate identity/path registration within one input
  capture, retaining independent captures and rejecting conflicting identities
  before file reads. Do not introduce cross-build validity caching.
- 2026-09-11, agent: Keep generated-path discovery and the full watcher repair
  inventory; omit only the unused generated inventory in final source admission.
- 2026-09-11, agent: Keep valid earlier-snapshot cache publication after admission
  rejection. Test the actual boundary instead of adding rollback or changing
  pruning. Runtime activation and cache publication have separate ownership.
- 2026-09-11, agent: Retain the final source guard for correctness and report its
  measured cost. Do not describe the small component savings as a main speedup or
  pursue retention, tidy or pruning changes in this patch.

## Outcomes & Retrospective

The three initial cases have implementation and observed proof. Repository
validation passed and [draft PR 194](https://github.com/scenery-sh/scenery/pull/194)
delivers the isolated ordinary change. It is not merged; PR 193 is unchanged. The measured component improvements
are approximately 8.1 ms for input capture and 5.7 ms for source admission relative
to their more expensive equivalents. Neither establishes a full-loop saving.
The final guard adds approximately 194 ms to the measured artifact interval versus
main. No 50 percent latency goal is closed by this work.

## Context and Orientation

`cmd/scenery/dev_build_preparation.go` extracts existing graph reuse, workspace
preparation and metadata from `dev_build_pipeline.go`, then checks source identity
after `build.CompileContext` and before session retention/runtime preparation.
`internal/build/build_input.go` owns consumed-file discovery and capture.
`cmd/scenery/watch.go` owns authored identity, generated discovery and the separate
generated tamper/repair inventory. `scripts/verify/harness_self_dev_publication.go`
extends the explicit `dev-process` probe with a real publication rendezvous.

### Read, write and invalidation ledger

| Call path / work | Owner and reason | Observation / correction |
|---|---|---|
| Load cached graph → source fingerprint | Current authored snapshot admits graph reuse | Fresh bytes remain required; no new memoization |
| Prepare/refresh → workspace membership and bytes | Bind locked private workspace to captured source | Preserve pre/post-build checks and concurrent edit rejection |
| Compile → module tidy and workspace identity | Write-capable preparation precedes build/check fork | Attributed separately; no change |
| Compile → Go input discovery → file/module capture | Bind the actually consumed Go/native graph | Register unique identity/path pairs before reading |
| Capture → framework source and producer executable | Bind framework and executing producer | Preserve source bytes and producer identity |
| Compile → parallel verification/build join | Both branches must succeed | Nested timings overlap; never sum branches as wall time |
| Compile → bundle, state, pruning, compiled manifest | Publish reusable valid captured-revision artifacts | Allowed before later supervisor admission; no rollback |
| Preparation.compile → generated discovery → authored scan | Reject later authored membership/content changes | Skip unused generated-content inventory only |
| Ordinary watcher → generated content | Detect tamper and trigger repair/retry | Full scan and generated repair state unchanged |
| Session retention → hash/copy/fsync/rename/verify | Independent serving executable | Preserve byte/durability checks and ownership |
| Receipt JSON/pending-path reporting | Diagnostic evidence | Outside captured-edit-to-retained-artifact timer |

### Case 1: repeated module capture

Previously `buildInputManifestFromGoList` called `addBuildInput` per package;
`Lstat`, `ReadFile` and SHA-256 preceded map deduplication. A three-package counting
test failed with three reads per capture. `captureBuildInputManifest` now registers
all identity/path bindings and module metadata, rejects collisions, sorts unique
identities and captures each once. Its map is new on every call.

`TestBuildInputCaptureReadsSharedModuleOnceAndRecapturesChanges` proves canonical
manifest identity, one shared-module read and a new digest after same-size,
same-mtime mutation between captures. The ambiguity test rejects different module
paths, module metadata and package paths before any capture, including paths that
might contain identical bytes.

On the main-based candidate's real Go graph, 12 alternating same-process pairs
compared origin/main's original capture body with the new function, using one
saved `go list` output and the same executable producer. All manifest digests were
identical. Module reads fell **402 → 32**, total direct capture reads **1941 → 1571**.
Capture medians were **92.227 → 84.492 ms**; median paired saving **8.078 ms**.
Discovery was outside this component timer and is not claimed as a saving.

### Case 2: unused generated inventory

`preparation.compile → scanWatchedFiles → snapshotFingerprint` previously computed
`generatedContent` that this fingerprint does not consume. The new source-admission
scan keeps `compiler.GeneratedPaths` and the complete fresh authored walk, including
embed membership/bytes, and omits only generated presence/content inventory.
The full watcher still uses the same scanner with that inventory enabled.

The unnecessary-work assertion failed when admission used the old full scan.
Tests compare full/source-only decisions for unchanged source, same-mtime edits,
add/delete, embed edit/add/delete, generated tamper, explicitly embedded generated
content, and authored content under a generated directory. The pending-edit test
rejects changes made during compile and preserves retry; existing generated repair
and accepted-snapshot tests remain selected. The real probe covers concurrent edits.

Twenty-four paired scans in the main-based candidate owner had identical source
fingerprints, nonempty full generated inventory and empty admission inventory.
Medians were **177.144 → 168.819 ms**, median paired saving **5.710 ms**. This is a
saving against a full admission scan, not against main, which has no such scan.

### Case 3: publication before source admission

The explicit `dev-process` probe builds a disposable source overlay that pauses
actual ordinary `CompileContext` return before the final source scan. It first
serves generation 2, compiles candidate 3, and inspects actual bundle input,
successful state, compiled manifest and cache pruning. Cache generation 1 is gone;
2 and 3 remain. The old PID still serves the expected HTTP response.

It writes invalid source 4 and releases admission. Candidate 3 is rejected, the
next real build fails with SCN6202, earlier successful state/bundle bytes remain,
and the latest manifest may describe the prepared failed attempt. The retained
serving executable's hash and HTTP PID remain unchanged. Repair source 5 produces
a new graph, build input, binary and correctly responding PID. All assertions and
cleanup passed. Cache advance for a valid older snapshot is therefore allowed;
activation rejection does not imply a cache transaction rollback.

## Milestones

1. Completed: exclusive artifact attribution and per-finding correctness evidence.
2. Completed: narrow module and scanner corrections plus ordinary final admission.
3. Completed: required validation and separate draft PR 194; experimental PR 193 preserved.

## Plan of Work

The three-case implementation and validation are complete in draft PR 194.
The diff excludes worker/runtime instrumentation. Follow-up work must use a new
plan; this completed record does not authorize merging either PR or extending
the performance budget from these bounded measurements.

## Concrete Steps

All commands run from the repository root. Raw measurement code/results are local
ignored artifacts beneath `.scenery/harness/ordinary-artifact/`.

The two initial attribution series used private instrumentation on experimental
source (local audit commit `35fdcde1b1bfaa51875b4d126b76b0e58020d11a`), built with the then-existing
`scenery_native_lifecycle_probe` tag. That tag is not part of this main-based patch.
`measure.py` ran persistent ordinary and matched owners through a warmup and six
AB/BA semantic edit pairs, restoring owned source bytes and verifying retained
SHA, pending paths and owner exit. `summarize.py` computes exclusive intervals.

`build-ordinary-drivers.go` uses disposable Go overlays to call genuine ordinary
preparation and retention on main-based source. `measure-ordinary.py` records six
AB/BA pairs after warmup. The baseline overlays the original main capture and
omits final admission exactly as main does. Graph inputs match except for the
required different executable producer identity. `prepare-input-benchmark.py`
creates a private same-producer capture comparison, executed by:

```sh
go test -overlay .scenery/harness/ordinary-artifact/input-capture-overlay.json ./internal/build -run '^$' -bench '^BenchmarkOrdinaryInputCaptureAudit$' -benchtime=12x -count=1
```

This explicitly requested measurement uses per-call wall times in
`input-capture-pairs.json`; the Go benchmark ns/op is not an applicable metric
because setup and the custom measurement loop are outside its timer.

## Validation and Acceptance

Selected classes are CLI JSON, Go package and runtime. Required commands and
current results:

| Command | Result |
|---|---|
| `go test ./cmd/scenery ./internal/build ./scripts/verify` | PASS final affected-package run |
| `go test ./...` | PASS |
| `golangci-lint run ./...` | PASS; repeated after final probe assertion refinement |
| `go run ./scripts/verify --summary --write` | PASS, including Go tests, vet, knowledge, drift and schemas |
| `go run ./scripts/verify --quick --summary --write` | PASS, final documentation closure |
| `go run ./scripts/verify --probe dev-process --probe native-contract --probe build-info --summary --write` | PASS all three probes, including actual candidate state identity, publication rejection, repair and confirmed cleanup |

The first default verifier failed only the new plan's required living-document
statement, now corrected. A subsequent probe assertion refinement initially had
a Go type error, corrected before the successful combined probe run. These
attempt logs remain preserved. The dev-process probe exercises a disposable fixture with actual
`scenery up --detach --wait ready`, runtime HTTP and `scenery down`; source overlays
add no product test API or environment knob. Probe output retained 41 existing
knowledge and 22 architecture warnings, without failing diagnostics.

No compiler/generator source changed: both committed client regeneration commands
are unselected. Dashboard/UI validation is unselected because no UI changed.
`scripts/release-gate.sh`, full-loop benchmark and all-root fresh timing are
unselected; this is not full release certification or a 100ms timing audit.
`SKILL.md` is unchanged because no target-app agent command or workflow changed;
living architecture, agent guide and local contract document the actual boundary.

## Idempotence and Recovery

Owners use explicit output directories and exit on stdin EOF. Restore only source
bytes written by this audit after checking ownership. Preserve failed attempts and
all accepted samples. Private clone regeneration does not modify original ONLV.
Retained evidence compressed for disk capacity has SHA-verified archive manifests;
restore original mode/path from those records when replay requires executable
bytes. No shared binary installation, global cache cleanup or historical-plan
rewrite is permitted by this task.

## Artifacts and Notes

All paths below are relative to `.scenery/harness/ordinary-artifact/`:

- `attribution-fc3b56b67e/report.json` and `attribution.json`: first six-pair series,
  ordinary artifact median 4071.756 ms, preparation 806.200 ms, join 2245.966 ms,
  per-row residual median 1017.505 ms. Source identity
  `50c5f8f11f6946aeaa346166ac0320b178a101aeb754fc685b56e03f67c0cb30`.
- `attribution-395e65dcd9/`: refined six-pair series, source identity
  `c4a7a7e152c80eccbce2b2231f6fa55238168eb3673a99ee2f8fac6b8b4ad5c9`.
  Ordinary artifact 5302.930 ms; preparation 895.011, join 3126.376, per-row
  residual 1538.529 ms. Compile outside join 719.063: workspace membership
  16.737, bytes 45.503, framework 61.581, bundle 19.921, state 13.629, prune
  408.584, manifest .993, tidy/identity 168.609, lock .044, load .508, other .128.
  Retention 559.950: source hash 25.167, copy 490.554, fsync 10.175, directory sync
  6.851, verification 25.832. Admission 211.586: generated discovery 33.733,
  generated inventory 5.977, authored/other 171.919. Repeated module operations
  about 11 ms for 379 repeated reads beyond 32 unique modules on that graph.
  Matched extra recapture 388.046 ms is not ordinary product work.
- Those are medians of individual intervals; they need not sum to total medians.
  The raw per-row exclusive decomposition is the source of accounting, not
  subtraction of unrelated median numbers. Both series preserve all 12 samples,
  successful owner exits and restored clone bytes. Producer digests/patches and
  exact command records remain alongside them.
- `ordinary-pairs-b417b9b761/`: final six main/candidate pairs, report and plan;
  all graph misses, one input discovery per build, retained SHA verification,
  no pending edits, no exclusions, both owners exited zero and restored source.
  Main artifact samples: 3339.796, 3293.405, 3183.618, 3475.995, 4840.586,
  3767.614 ms; candidate: 3652.347, 3550.803, 3377.918, 3528.318, 4036.606,
  3998.136 ms. The report also retains the 24 admission pairs.
- `ordinary-producer-source.json` and `ordinary-source-after.json`: exact identical
  main-based source digest before/after measurement
  `754fd5cdf90c232af173f2bafc3e145fc27b21e24fb17921e25e34475c44df3a`.
- `input-capture-pairs.json`, `input-go-list.json`, `input-capture-benchmark.log`:
  12 alternating same-producer pairs, identical canonical manifests.
- `dev-process-report.json` and final `final-probes-report.json`: real publication
  proof, all assertions true, cleanup confirmed. The final run additionally checks
  the candidate graph in saved state; checkpoint executable digest
  `f5e3076993880a9c4d3bd3b7e8723fde69eaa64b98468b527bd3bd4aacb59663`.
- `module-count-before.log`, `admission-before-assertion.log`: real failing
  unnecessary-work assertions, with corresponding passing logs. Earlier ENOSPC
  logs are preserved separately and are not counted as assertion failures.
- `retained-archive-manifest.json` and `prior-executable-archive/`: losslessly
  archived owned evidence; incomplete compression never authorizes source removal.

## Interfaces and Dependencies

No public schema, worker runtime, compatibility path, environment switch, or
persistent cache is added. The ordinary preparation helper is private to the CLI;
input capture injection is private to `internal/build` for bounded counting proof.
The real-process rendezvous lives solely in the verifier's disposable source
overlay, and uses existing framework and producer identity contracts.
