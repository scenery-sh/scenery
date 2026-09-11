# Native Worker Supervisor Preparation Lifecycle

This ExecPlan is a living document. Follow [PLANS.md](../../PLANS.md).

Completed on 2026-09-11 as a bounded implementation and measurement gate; no
product worker promotion.

## Purpose / Big Picture

Transfer 0182's direct worker preparation into the actual supervisor snapshot
and process-cache lifecycle, using shared production code. The source baseline
was `934fcfd22dbeed2242d94b8e0f41714ffda84896`. Preserve complete checks and measure
six matched pairs before deciding whether GenerationCandidate is justified.

The transfer is implemented. **Stop the worker product migration at this gate:**
the surviving gain is 174.013 ms to a checked retained artifact and 228.600 ms
(4.55%) from authored write to the supported SQL response. Most of the earlier
636.515 ms advantage is gone. This is not full-loop acceptance and does not
change the 3153.377720 ms edit or 4017.245375 ms unchanged-start targets.

## Progress

- [x] (2026-09-11) Read the supplied review, inspect baseline 934fcfd2 and allocate
  plan 0183 without changing completed historical plans.
- [x] Pass the real supervisor SourceSnapshot into direct native preparation.
- [x] Share watcher capture, graph selection, preparation, metadata, compilation
  admission and final source checks between product and private probe.
- [x] Preserve explicit private projection ownership on fresh and cached paths.
- [x] Prove initial and unchanged graph-hit requests in persistent owners.
- [x] Prove exact 166-package, 215-operation, full target/ABI, complete input,
  independent 687-input kernel projection and retained-byte boundaries.
- [x] Prove source mutation inside compilation and at final recapture, plus an
  invalid unrelated native body followed by repair, in both arms.
- [x] Preserve the previous live worker's authenticated SQL response throughout
  rejected candidates; pass 56 real auth/SQL/tenant/cancellation/ownership checks.
- [x] Execute the six predetermined AB/BA pairs with one preparation process per
  arm and new application processes per generation; no measured failures/exclusions.
- [x] Pass affected/full Go tests, focused race tests, default/tagged lint,
  default verifier and the four named external probes.
- [x] Close the decision: do not start GenerationCandidate or other broad
  refactoring. Keep current guidance and draft PR #193 aligned with this gate.

## Surprises & Discoveries

`LoadCachedGraphContext` rejects every semantic edit's changed watcher fingerprint
before cached refresh. Thus all measured graph lookups miss, while process-local
compiler/generator caches and persisted preparation hints survive. Separate
unchanged requests exercise the actual graph-hit branch. Native graph hits
prepare a new full verifier/build in the explicitly selected native workspace;
only metadata, source/dependency hints and pure generation caches are reused.
The unchanged native path is not optimized for startup and is not a startup
performance claim.

The earlier direct-preparation advantage reverses in this lifecycle:
preparation medians are 769.414 ms ordinary and 843.396 ms worker. The joined
build/verifier advantage remains smaller at 1762.894/1592.983 ms. This supports
the conservative decision without inventing credit for a surviving kernel,
future protocol work or a generic preparation session.

An early timer-based worker mutation landed about 36 ms before the compile join
under concurrent validation load. It is retained as an unsuccessful placement
attempt, not as in-build coverage. The private trace gate now pauses at the first
full input discovery inside the running compile join. The mutation's wall time
is checked against that join's recorded start/end; no timing sleep selects it.

A separate behavior attempt failed before assertions because PostgreSQL's
socket-only readiness observed its temporary initialization server. Waiting for
TCP readiness corrected the fixture. Both unsuccessful setup attempts remain
recorded; neither belongs to or replaces a measured pair.

Tagged lint initially found the product-only `executeCLI` wrapper unused in the
alternate build. Moving that wrapper with the product main removed the warning
without a suppression or an alternate product command.

## Decision Log

- (2026-09-11, Codex) Use narrow `devBuildPreparation` methods in the CLI package
  shared by `prepareDevRuntimePlan` and an explicit alternate probe entrypoint.
  Keep the actual watcher implementation; do not create a second scanner,
  compiler, runtime protocol, public selector or generic BuildSession.
- (2026-09-11, Codex) Make native graph ownership process-local and projection
  selection explicit. Ordinary refresh rejects experimental results. Native
  refresh re-prepares pending full verification and never imports ordinary
  successful-build state.
- (2026-09-11, Codex) Reject source changes at the shared final watcher recapture.
  Keep the captured baseline so the next iteration still observes pending edits;
  failed candidates cannot update the native owner's accepted graph.
- (2026-09-11, Codex) Freeze AB, BA, AB, BA, AB, BA before measurement, keep one
  owner per arm, and apply identical body bytes within each pair. Report exactly
  two warmups separately. Every generation executable's first execution occurs
  inside its measured proof/activation path.
- (2026-09-11, Codex) Stop product migration under the supplied conservative gate.
  All pairs remain positive, but only about 36% of the earlier absolute
  authored-write gain remains, and the new common artifact gain is 174 ms.
  GenerationCandidate and the later parity groups are not justified by this
  result. This is a gate decision, not a correctness failure or proof that the
  architecture can never be useful.

## Outcomes & Retrospective

The implementation transfer and its acceptance evidence are complete. Six
matched lifecycle pairs show the following medians; positive gain is A minus B.

| Boundary | Control A ms | Worker B ms | Gain ms | Gain percent | Median paired gain ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Captured edit to checked retained artifact | 3546.048 | 3372.035 | 174.013 | 4.91% | 159.217 |
| Captured edit to authenticated SQL response | 4847.965 | 4612.995 | 234.970 | 4.85% | 213.119 |
| Authored write to authenticated SQL response | 5018.746 | 4790.146 | 228.600 | 4.55% | 211.040 |

The authored-write paired gains are 527.331, 266.164, 191.035, 192.071, 81.018
and 230.009 ms. The corresponding 0182 gain was 636.515 ms by arm medians and
631.524 ms within pairs. This comparison describes the requested experiment
stages; it does not causally attribute every millisecond across separate series
or permit subtraction from older full-loop measurements.

All measured candidate/control executions, identity checks, two-tenant queries
and owner shutdowns pass. Both preparation owners exit 0 and the exact owned
PostgreSQL container is removed. The ordinary latest manifest in the native
fixture remains byte-identical. No full ONLV runtime, frontend/assistant loop,
complete parity, 50% acceptance or release promotion is claimed.

## Context and Orientation

`cmd/scenery/dev_build_preparation.go` owns the common graph/preparation/metadata
and compile-admission boundary. `cmd/scenery/dev_build_pipeline.go` retains
console phases, secrets, PostgreSQL/runtime environment and status coordination.
`cmd/scenery/watch*.go` remains the sole snapshot/pending-edit implementation.
`internal/build/native_experiment_prepare.go` accepts the real snapshot and
checks selected workspace ownership on refresh. `workspace_cache.go` rejects
native results on the ordinary path.

`main_product.go` holds the product entrypoint. The mutually exclusive
`main_native_lifecycle_probe.go` build is private integration instrumentation,
not product CLI grammar. Its long-lived JSON request loop performs no application
execution; the owned runtime proof driver starts fresh processes only after
receiving checked retained evidence. Kernel lifetime and the existing unary
proof/activation protocol are unchanged.

## Milestones

M1 shares actual preparation and capture ownership, including explicit native
refresh. M2 proves pending edits, repair, previous-generation survival and the
complete existing behavior boundary. M3 measures the six fixed pairs and closes
the stop decision. No later GenerationCandidate, protocol, debugger or other
large-refactoring milestone has started.

## Plan of Work

Work proceeded from the shared product preparation boundary to explicit native
reuse and persistent owners. Correctness and previous-generation preservation
were proved before the fixed paired series. Measurement retained all checks,
then the artifact and response boundaries determined the migration decision.
No parallel compiler, kernel-lifetime or protocol refactor was mixed into the
experiment.

## Measurement Method and Tables

Preparation PIDs are 53437 (A) and 53438 (B), unchanged through each arm's warmup
and six edits. Source roots and all preparation workspaces are separate. Every
pair has an identical captured watcher fingerprint and identical 509 shared
app/native/embed/public input entries and bytes. Graph hits are false for every
measured semantic edit; the unchanged-hit proofs are separate.

The artifact timestamp follows the full checker/build join, both input captures,
retention and the shared final source check. Diagnostic receipt bookkeeping is
outside that boundary. The SQL interval also includes generated-baseline
bookkeeping, pending-path observation, JSON receipt handoff, first compiled
proof, activation and the actual supported authenticated SQL response.
Artifact-to-receipt medians are 308.549/350.797 ms; those are probe overhead,
not established product costs. The separate artifact metric isolates build
benefit from that handoff. Source writes precede capture by roughly 0.17 seconds.

Warmup authored-write-to-response times are 11777.749 ms (A) and 12445.988 ms
(B), excluded by the preregistered method. No measured sample is excluded or
replaced. The complete source-write series ranges are 4914.895–5263.164 ms (A)
and 4722.824–4861.739 ms (B). Both requested captured-edit boundaries favor B in
all six pairs.

| Pair | Order | Artifact A ms | Artifact B ms | Gain ms | SQL A ms | SQL B ms | Gain ms |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | AB | 3745.182 | 3338.568 | 406.614 | 5074.832 | 4553.771 | 521.061 |
| 2 | BA | 3544.972 | 3386.394 | 158.578 | 4864.050 | 4597.134 | 266.917 |
| 3 | AB | 3547.124 | 3387.267 | 159.857 | 4831.880 | 4628.856 | 203.023 |
| 4 | BA | 3475.956 | 3346.082 | 129.874 | 4741.439 | 4561.185 | 180.254 |
| 5 | AB | 3482.591 | 3389.629 | 92.962 | 4780.504 | 4694.568 | 85.936 |
| 6 | BA | 3572.309 | 3357.676 | 214.633 | 4909.011 | 4685.796 | 223.215 |

| Pair/arm | Preparation ms | Build branch ms | Verifier ms | Joined ms | Captured edit → artifact ms | Captured edit → SQL ms | Source write → SQL ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1A | 807.197 | 1937.536 | 971.484 | 1937.555 | 3745.182 | 5074.832 | 5263.164 |
| 1B | 847.066 | 1576.017 | 864.745 | 1576.055 | 3338.568 | 4553.771 | 4735.833 |
| 2B | 837.438 | 1595.713 | 879.379 | 1595.742 | 3386.394 | 4597.134 | 4771.702 |
| 2A | 754.104 | 1778.176 | 986.068 | 1778.194 | 3544.972 | 4864.050 | 5037.866 |
| 3A | 743.533 | 1772.451 | 963.227 | 1772.472 | 3547.124 | 4831.880 | 4999.626 |
| 3B | 827.675 | 1590.197 | 870.558 | 1590.224 | 3387.267 | 4628.856 | 4808.590 |
| 4B | 839.726 | 1572.498 | 863.057 | 1572.547 | 3346.082 | 4561.185 | 4722.824 |
| 4A | 767.291 | 1752.061 | 969.900 | 1752.083 | 3475.956 | 4741.439 | 4914.895 |
| 5A | 771.537 | 1753.283 | 953.486 | 1753.316 | 3482.591 | 4780.504 | 4942.757 |
| 5B | 852.550 | 1665.236 | 939.431 | 1665.282 | 3389.629 | 4694.568 | 4861.739 |
| 6B | 858.593 | 1620.635 | 897.798 | 1620.662 | 3357.676 | 4685.796 | 4842.997 |
| 6A | 787.733 | 1751.997 | 951.009 | 1752.014 | 3572.309 | 4909.011 | 5073.006 |

Preparation/build/verifier/join medians are respectively 769.414/843.396,
1762.867/1592.955, 966.564/874.968 and 1762.894/1592.983 ms (A/B).
The verifier overlaps the build branch and their savings must not be added.
Other pipeline stages remain inside the common artifact boundary; no component
median sum is presented as an end-to-end result. The fresh kernel's observed
spawn-to-TCP-acceptance median is 44.036 ms with 10 ms polling resolution, not
an available persistent-kernel saving.

## Validation and Acceptance Evidence

Exact independent dependency captures retain 166 native packages and 215
operations. Every pair preserves all 509 common application input identities
and bytes. The full manifests contain 1628 control and 1631 worker entries:
50 ordinary private inputs are replaced by 53 selected inputs. No unexplained
common input changes, ordinary entrypoint, ordinary composition or ordinary
adapter sources appear in the native workspace. Both arms perform two full
input discoveries, and an independent kernel-only capture matches all 687
projected entries, including exact current source and producer executable hashes.
All six workers are distinct retained executables. One verified kernel artifact
is reused, with six distinct kernel processes. Native preparation state has only
`version`, `dependency_fingerprint`, `source_file_stamps` and `generated_files`.

Both arms pass these negative sequences in one persistent owner:

1. Change authored source at the synchronized in-compile gate; reject the result,
   preserve pending paths/previous receipt/artifacts, and build the repair.
2. Change authored source after the verifier/build/retention path but before the
   shared final watcher recapture; reject it and preserve the same ownership.
3. Add an invalid Go body in the unrelated `agents` native package; reach SCN6202
   through full verification, preserve the accepted generation and then remove
   the owned invalid input and build the repair.

Each repair proves the current project source digest and a new complete input
digest. A final unchanged request takes the selected graph-hit path. During all
three rejected worker candidates, the prior live worker/kernel pair still
returns the exact previous authenticated SQL result. The real behavior proof
passes 56 assertions, preserving exact registry identity, anonymous/invalid-auth
rejection, two tenants, trusted AuthData precedence, sanitized SQL failure,
in-flight cancellation, worker loss and both owner-EOF shutdowns.

## Concrete Steps

Commands run from the repository root; all listed final results pass:

| Command | Result/evidence under `.scenery/harness/native-worker/` |
| --- | --- |
| `go test ./internal/build ./cmd/scenery ./scripts/native-worker-experiment` | PASS; `lifecycle-initial-tests.log` |
| `go test ./internal/build ./cmd/scenery` | PASS; `lifecycle-focused-tests.log` |
| `go test ./internal/build ./cmd/scenery ./internal/generate ./scripts/native-worker-experiment` | PASS; `lifecycle-affected-final.log` |
| `go test -race ./internal/build ./cmd/scenery` | PASS; `lifecycle-race.log` |
| `go test -tags scenery_native_lifecycle_probe ./cmd/scenery` | PASS; `lifecycle-tag-tests.log` |
| `golangci-lint run ./...` | PASS, 0 issues; `lifecycle-lint.log` |
| `golangci-lint run --build-tags scenery_native_lifecycle_probe ./cmd/scenery` | PASS, 0 issues after moving the product-only wrapper; `lifecycle-probe-lint.log` |
| `go run ./scripts/verify --summary --write` | PASS including full `go test ./...`, vet and schemas; `lifecycle-default-final.log` |
| `go run ./scripts/verify --summary --write --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime` | PASS; `lifecycle-probes.log`. The real dev-process probe includes batching, mid-build edits and handoff. |
| `go run .scenery/harness/native-worker/lifecycle-build-driver.go` | PASS; producer-stamped alternate executable and `lifecycle-producer.json` |
| `python3 .scenery/harness/native-worker/lifecycle-smoke.py` | PASS; initial + unchanged graph hit in each owner, `lifecycle-smoke-report.json` |
| `python3 .scenery/harness/native-worker/lifecycle-negative-cases.py control` | PASS; three rejection/repair cases + unchanged hit, `lifecycle-negative-control-report.json` |
| `python3 .scenery/harness/native-worker/lifecycle-prove-worker.py` | PASS; same worker negatives with live predecessor, 56 behavior assertions, `lifecycle-negative-worker-report.json` and `lifecycle-behavior-report.json` |
| `python3 .scenery/harness/native-worker/lifecycle-structure.py` | PASS; independent package/operation/input comparison, `lifecycle-structure-report.json` |
| `python3 .scenery/harness/native-worker/lifecycle-measure-pairs.py` | PASS; exactly six pairs, `lifecycle-matched-paired-report.json` |
| `python3 .scenery/harness/native-worker/lifecycle-audit-inputs.py` | PASS; every pair's inputs, snapshots, retained bytes and ownership, `lifecycle-input-retention-report.json` |
| `python3 .scenery/harness/native-worker/lifecycle-kernel-audit.py` | PASS; independent exact 687-entry kernel capture, `lifecycle-kernel-independent-report.json` |
| `python3 .scenery/harness/native-worker/lifecycle-timeline.py` | PASS; complete sample arithmetic, `lifecycle-timeline-report.json` |

Default/probe verification reports 41 existing knowledge-review and 22
architecture warnings, with no errors. The first default run also had an advisory
5.461-second cached-suite warning under concurrent validation; the final default
suite passes at 4.869 seconds without it. This is not a new all-root timing audit.
No validation job runs concurrently with the paired series. Later read-only
artifact audits and final source/documentation verification do not replace
measured runs.

The first final documentation check caught missing required plan headings and
the living-document statement after the completion rewrite; those formatting
errors were corrected. Final documentation uses
`go run ./scripts/verify --quick --summary --write` and
`git diff --check`. Source hashes are checked again before delivery against the
measured producer. No compiler/generator source, generated public contract or
public JSON schema changes; fixture regeneration, frontend/UI checks and
instruction-document changes are intentionally not selected. Full release,
all-root fresh timing, full-loop acceptance and runtime-parity promotion are not
selected for this bounded gate, which stops further migration.

## Idempotence and Recovery

Each arm uses an owned fixture copy, output marker and separate workspace; only
its own source writes are restored, after byte comparison. Failed candidates
cannot replace the accepted receipt or prune predecessor artifacts. Native
successful graph evidence stays in its preparation owner rather than ordinary
build state. EOF confirms owner shutdown; runtime proof confirms process exits
before the next write-capable generation. Container deletion checks its exact
ownership label. No installed shared Scenery executable, original ONLV source,
user service or unrelated worktree is modified.

## Artifacts and Notes

Raw artifacts live under `.scenery/harness/native-worker/lifecycle-*` and are
machine-local, not committed application data. `lifecycle-measurement-plan.json`
records the fixed order before execution. The measurement run is
`lifecycle-pairs-matched-25b8914948`; full raw samples and exact child identities
are retained. All fixture project source bytes restore to SHA-256
`259b9cdc92efd58a2c65847fc11c8c65153a7ba3578088c274dc3f6bf1a8af8c`.

The measured framework source digest is
`sha256:583a01156067c494430ad68be3275066921dd60f09a3d6244962b48c57e8677b`.
The private producer executable SHA-256 is
`37b68a1b5abb691c3006ce88f80b21523e2e75a50806e66b47e1b93cad572924`.
`lifecycle-source-before.json` records the full source inventory. Unsuccessful
negative-placement and PostgreSQL-setup attempts remain in the corresponding
`attempt1`/`attempt2` artifacts; neither is part of the six-pair result.

Current guidance is [native-worker-findings.md](../native-worker-findings.md).
Completed 0180–0182 plans remain immutable; their living index can point to this
later gate. Draft PR #193 includes the shared preparation and this result, with
no production runtime selection.

## Interfaces and Dependencies

`PrepareNativeExperiment` now accepts `*build.SourceSnapshot`.
`RefreshNativeExperiment` requires its accepted private result, exact app root,
workspace and explicitly selected renderer. Ordinary refresh rejects native
results. `devBuildPreparation` is a narrow CLI-local implementation used by the
product and probe; it is not a generic session abstraction. The private alternate
entrypoint adds no product grammar, machine schema, environment knob or runtime
protocol. Existing Go/native checks, retention and shutdown boundaries remain.
