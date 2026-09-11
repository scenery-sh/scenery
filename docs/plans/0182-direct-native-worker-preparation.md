# Direct Native Worker Preparation

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current. It follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Implement the developer's revision-bound review of draft PR 193 at `fe5705ad`:
prepare the worker/kernel artifact directly, without first rendering, validating,
synchronizing or fingerprinting the ordinary private application. This is one
bounded follow-up to 0181, not permission for the larger runtime/session rewrite.
Keep the ordinary product path functionally unchanged and keep the PR a draft.

## Progress

- [x] (2026-09-11) Read the supplied review and verify local and PR HEAD `fe5705ad`.
- [x] Separate shared adapter metadata/validation from ordinary source rendering.
- [x] Select public plus worker/kernel projection before one owned materialization.
- [x] Remove the internally forced ordinary entrypoint from candidate discovery.
- [x] Prove exact native/public membership, absent ordinary rendering/output,
  full negative verification, freshness and failure/retention ownership.
- [x] Repeat real auth/SQL/tenant/error/cancellation proof on the direct candidate.
- [x] Run six matched AB/BA pairs with fresh kernel processes, complete timelines,
  paired deltas, difference of medians and exposed kernel-start intervals.
- [x] Record a closed decision, validation and remaining limits in the review
  handoff for draft PR #193; preserve draft status.

## Surprises & Discoveries

- The old driver prepared ordinary private artifacts before adding worker files,
  and the worker obtained metadata from adapters containing ordinary source.
  Separate metadata and direct projection selection remove both paths together.
- A new owned workspace has no ordinary preparation cache. The candidate retains
  only source stamps, generated paths and dependency fingerprint after success;
  these hints carry no ordinary successful binary/graph identity or pruning right.
- The first distinct-target negative test stopped on stale public TypeScript
  before reaching Go validation. It is preserved as an unsuccessful proof attempt.
  Refreshing public projections before/after the temporary authored target edit
  lets the repeated test fail at the intended selected Go context (`SCN6202`).
- The early matched samples are slower in both arms despite warmups. All remain
  in the reported series; within-pair differences are the primary paired result.
- Canonical macOS temporary paths differ from `/var` aliases. The ownership unit
  fixture uses its resolved path; the API refuses ambiguous workspace parents.

## Decision Log

- (2026-09-11, Codex) Preserve the ordinary control, shared public publication,
  full native verifier, target contexts and post-build recapture. Select only the
  private renderer/entrypoint before materialization in an explicit owned root.
- (2026-09-11, Codex) Do not introduce persistent kernels or generic BuildSession,
  DeclarationPlan or GoSession infrastructure. One structural variable changes.
- (2026-09-11, Codex) Retain previous 0180/0181 samples and completed plans as
  immutable evidence. Compare the new candidate only within the new matched series.

## Outcomes & Retrospective

Completed locally as the one bounded direct-preparation experiment requested
against `fe5705ad`. Ordinary private source never enters the worker preparation
path, common validations remain, and the real ownership/behavior proofs pass.

Six matched pairs measure 5471.196271 ms control and 4834.681105 ms worker medians:
636.515166 ms / 11.633930% lower candidate time. The median within-pair gain is
631.523917 ms, with all six gains positive (520.522834–1035.093666 ms). Both
statistics are reported; neither is a general architectural equivalence claim.

Keep the candidate in draft for review. This is a measurable bounded gain, but
not evidence that the broad migration will meet the complete-loop 50% objective.
No BuildSession/DeclarationPlan/GoSession, persistent kernel or product runtime
promotion was implemented or authorized by this result. The completed 0180/0181
plans remain immutable; the living findings document carries the new conclusion.

## Context and Orientation

`internal/generate/generated_go_workspace.go` owns public publication and private
projection selection. `generate_application.go` and `projection_adapters.go`
now separate validated adapter metadata from ordinary adapter source. The worker/kernel
renderers live beside them. `internal/build/prepare.go` owns materialization;
`native_experiment*.go` owns explicit workspace ownership, direct preparation,
full target discovery, verification and retention.
`scripts/native-worker-experiment` is the explicit producer-stamped driver.

The owned ONLV fixture and previous scripts are under
`.scenery/harness/native-worker/`; the original ONLV checkout and personal runtime
are outside the write scope. New proof uses a `direct-` prefix and separate owned
cache/process/database roots. The measured app contains 166 exact native packages
and 215 operation identities, not just one small representative implementation.

## Milestones

M1 implements direct selected preparation and retains common validations.
M2 proves structure, negative verification, ownership and real operation behavior.
M3 runs the matched series and closes the decision against the supplied gate.

## Plan of Work

Separate adapter metadata from `Source`; both ordinary and worker renderers use
the same metadata and common native validation. Factor common compiler/public
projection/TypeScript/target preparation without rendering the ordinary executable
on the worker branch. Materialize the selected complete generated set once in the
owned candidate workspace. Compilation consumes that verified set without later
additions or repairs. Do not rewrite explicitly declared target patterns; a target
requiring an unavailable ordinary entrypoint must fail rather than be narrowed.

Capture full target inputs before the build and after the verifier/build join;
derive the independent kernel closure from each full capture. Compare concrete
native package and operation identities and authored/native/embed/public file
membership, explaining every removed private input. Add timelines for preparation,
both fork branches and their joined interval, recapture/retention, proof/activation
and authenticated response. Record actual exposed kernel startup without counting
it as a future saving or changing process lifetime.

## Concrete Steps

From the repository root, first run focused tests and explicit structural and
negative proofs. Build the driver with the current verifier's framework digest.
Repeat the existing owned real SQL proof, including table failure and in-flight
cancellation. Then run six alternating matched pairs with one warmup per arm and
no concurrent tests. Keep every sample and failed attempt. Compute both the
difference of arm medians and the median/range of within-pair differences.

## Validation and Acceptance

Run from the repository root:

```sh
go test ./internal/build ./internal/generate ./internal/codegen ./cmd/scenery ./scripts/native-worker-experiment
go test -race ./internal/build ./internal/generate
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --summary --write --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime
```

The full verifier includes the repository Go suite and refreshes agent context.
Negative proof must cover a wrong native signature and invalid body outside the
selected HTTP path, distinct selected/default targets, changed imports/tags/embed
membership and tampered generated bytes. Preparation or final-recapture failure
must not emit a new success receipt, publish ordinary success state, or remove
previous retained artifacts. Real process/toolchain proofs use explicit scripts
or probes; ordinary Go roots retain in-process coverage under the 100 ms contract.

Accept the experiment only after all structure, membership, verification and
behavior evidence passes. A gain confined to tens or low hundreds of milliseconds
does not justify the large rewrite for the 50% goal. A larger repeatable gain must
be located on the real edit critical path before further migration is proposed.
Full release, debugger, stream/custom-auth/protocol promotion and the full
supervisor/frontend/assistant loop stay unselected because this task does not
admit a production split runtime. Report those exact limits; do not count them as
passed or extrapolate this probe onto older development-loop measurements.

## Idempotence and Recovery

Use a dedicated owned native workspace and the existing serialized publication
and workspace locks. Keep ordinary success state and retained binaries outside
candidate mutation. Restore fixture edits only if bytes still match the last
owned write; remove only containers/processes with verified experiment ownership.
Preserve errors and incomplete run records. No installed Scenery binary is changed.

## Artifacts and Notes

The measured framework source digest is
`sha256:d51bee8eb2fc693e0806687a3ccea96709d2e3499b7a4b5c1422c3a01557f054`.
The producer-stamped experiment driver has SHA-256
`214d2b6135933a2826b64f8d2bcc7255c9bcfd866e600bd9c21982192fdfa5a6`.
The owned series root is `direct-pairs-matched-49e23efab4`; both warmups and every
sample are retained. The series output is `direct-series-matched`. Every child
exited zero, the owned PostgreSQL container was removed, and the fixture body was
restored to SHA-256
`259b9cdc92efd58a2c65847fc11c8c65153a7ba3578088c274dc3f6bf1a8af8c`.

All paths below are relative to `.scenery/harness/native-worker/` and remain
machine-local evidence, not committed private ONLV source:

- `direct-source-manifest.json`, `direct-experiment-driver`,
  `direct-independent-inputs/build-evidence.json`: producer and independent kernel proof.
- `direct-structure-report.json`, `direct-{control,worker}-packages.json`:
  exact package/operation/input sets and absent ordinary outputs.
- `direct-negative-report.json`, `direct-negative-attempt1-report.json`,
  `direct-negatives2.log`: accepted negative proofs plus the preserved earlier
  stale-TypeScript attempt. `direct-ownership-report.json` also checks the actual
  app `.scenery/build/latest.json`, previous receipt and real retained artifacts.
- `direct-behavior-report.json`, `direct-behavior.log`: 53 real SQL/auth/lifecycle assertions.
- `direct-matched-paired-report.json`, `direct-timeline-report.json`,
  `direct-input-retention-report.json`: every sample, timeline and retained-byte audit.
- `direct-affected.log`, `direct-race.log`, `direct-lint.log`,
  `direct-default.log`, `direct-probes.log`, `direct-fixture-{native,house,assistant}.json`,
  `direct-ts-{conformance,clients,catalog}.log`: validation outputs.

The explicit local commands are:

```sh
.scenery/harness/native-worker/direct-experiment-driver --app-root "$PWD/.scenery/harness/native-worker/m2-app" --output "$PWD/.scenery/harness/native-worker/direct-independent-inputs" --mode worker --binding projects/binding/projects_list_projects_http --verify-kernel-projection
python3 .scenery/harness/native-worker/direct-negatives.py
python3 .scenery/harness/native-worker/direct-ownership.py
python3 .scenery/harness/native-worker/direct-prove-worker.py
python3 .scenery/harness/native-worker/direct-measure-pairs.py
python3 .scenery/harness/native-worker/direct-audit-inputs.py
```

## Interfaces and Dependencies

Use compiler-owned resources and injected generation at the build boundary.
Shared generator types remain stdlib-only. Ordinary public JSON and CLI grammar
do not change. The direct candidate is private and must retain full default plus
selected target ABI checks and all native application identities.

## Structural and Negative Evidence

The independently captured entrypoint closures contain identical sets of 166
native packages and 215 operation addresses. Full authored patterns additionally
cover packages outside the reachable entrypoint graph; none is omitted from the
verifier or complete manifest. Every pair preserves the same 509 native,
authored, embedded and public-projection file identities, with only the intended
`solar/projects/api.go` body bytes different between its two semantic edits.

Full input count is 1628 for the control and 1631 for the worker. The explained
set difference is 50 removed ordinary inputs (47 adapters, composition, empty
assistant-assets registry and ordinary entrypoint), versus 53 added inputs
(47 worker adapters, worker/kernel entrypoints and four worker runtime files).
All other common input bytes are equal. Independent kernel discovery matches
its projected 687 inputs. Both arms perform exactly two complete discoveries.
All retained binaries match their digest-named paths; there are six distinct
candidate worker executables, one reused kernel executable and six fresh kernel
process identities.

| Negative control | Observed rejection |
| --- | --- |
| Unrelated `agents` constructor signature | `SCN6202` native implementation verification |
| Invalid unrelated native body | `SCN6202` native implementation verification |
| Distinct selected development tag vs default contract context | `SCN6202` on the tagged invalid body |
| Explicit authored ordinary-entrypoint pattern | `SCN6202`; the missing entrypoint is not silently removed |
| Selected projection preparation failure | No prepared candidate or success receipt |
| Tampered generated worker entrypoint | Prepared workspace byte mismatch |
| New import-bearing file after join | Prepared workspace membership mismatch |
| New Darwin-tagged file after join | Prepared workspace membership mismatch |
| New file in an actual embed wildcard after join | Prepared workspace membership mismatch |
| Canceled second input capture | Context cancellation before retention/receipt |

The last two ownership-specific reruns compare the actual ordinary latest-build
manifest, prior receipt and real retained worker/kernel bytes before and after
preparation/recapture failure. They remain identical. The candidate's own cached
state contains only version, source stamps, generated paths and dependency
fingerprint, never an ordinary successful build identity.

## Paired Results and Complete Timelines

A is the ordinary control with matched post-build recapture; B is direct worker
preparation. Positive gain means A minus B. Warmups are separate and no measured
sample is excluded.

| Pair/order | A ms | B ms | Gain ms |
| --- | ---: | ---: | ---: |
| 1 / AB | 6869.628 | 6197.790 | 671.838 |
| 2 / BA | 6343.951 | 5308.857 | 1035.094 |
| 3 / AB | 5546.635 | 4836.310 | 710.326 |
| 4 / BA | 5395.757 | 4804.547 | 591.210 |
| 5 / AB | 5301.065 | 4780.542 | 520.523 |
| 6 / BA | 5364.831 | 4833.053 | 531.779 |

Arm medians: A **5471.196 ms**, B **4834.681 ms**. Difference of medians:
**636.515 ms (11.634%)**. Median within-pair gain: **631.524 ms**.

Each row below is one actual execution. P is full common plus selected private
preparation; B is the actual build branch including capture/identity and compiler
work; V is the full verifier; J is their joined interval, approximately max(B,V).
R is final recapture plus retention. O is remaining driver time, including
producer startup, pre-fork workspace verification, ordinary post-join publication
where applicable and receipt/process overhead. X is retained-artifact readiness
through first proof, activation and authenticated SQL response. P + J + R + O + X
accounts for each row's total (sub-millisecond event/readback rounding applies).
Do not sum B and V or add component medians into an end-to-end gain.

| Sample | P ms | B ms | V ms | J ms | R ms | O ms | X ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1-A | 1740.736 | 2769.090 | 1268.403 | 2769.107 | 620.099 | 675.556 | 1064.138 |
| 1-B | 1509.826 | 2602.727 | 1239.822 | 2602.882 | 573.884 | 545.602 | 965.633 |
| 2-B | 1292.836 | 1935.559 | 1198.746 | 1935.589 | 641.110 | 509.328 | 930.006 |
| 2-A | 1586.443 | 2486.021 | 1172.775 | 2486.053 | 571.966 | 623.856 | 1075.647 |
| 3-A | 1514.515 | 1905.638 | 1055.514 | 1905.665 | 556.622 | 543.400 | 1026.470 |
| 3-B | 1178.218 | 1741.267 | 985.219 | 1741.288 | 572.223 | 413.949 | 930.643 |
| 4-B | 1201.566 | 1708.823 | 975.995 | 1708.839 | 548.900 | 452.166 | 893.089 |
| 4-A | 1373.363 | 1866.722 | 1052.766 | 1866.743 | 584.002 | 552.204 | 1019.463 |
| 5-A | 1414.914 | 1849.809 | 1001.459 | 1849.845 | 507.776 | 515.271 | 1013.269 |
| 5-B | 1211.956 | 1659.261 | 974.138 | 1659.288 | 548.135 | 442.414 | 918.763 |
| 6-B | 1235.760 | 1696.834 | 967.808 | 1696.852 | 557.961 | 466.341 | 876.149 |
| 6-A | 1428.659 | 1823.728 | 1011.655 | 1823.743 | 510.852 | 546.998 | 1054.591 |

The build branch dominates every join. Median paired join gain is 165.301 ms;
median paired preparation gain is 216.934 ms. These are separate distributions,
not additive medians. Go-command sums (1523.415/1316.689 ms arm medians) exclude
capture, hashing and other branch work and are not a substitute for B.
Recapture/retention arm medians are 564.294/565.092 ms; those checks remain.

### Exposed kernel startup and dependencies

The worker is spawned, proves its linked identity, receives explicit activation,
initializes real native services and reports readiness before the kernel starts.
The new kernel process emits compiled proof, receives the current worker identity
and private token, and starts the HTTP listener. The measured exposed interval
runs from kernel spawn to first successful TCP connection, sampled at 10 ms.
The subsequent authenticated SQL response is recorded separately.

| Pair | Kernel spawn to TCP acceptance ms |
| --- | ---: |
| 1 | 53.543 |
| 2 | 49.980 |
| 3 | 44.338 |
| 4 | 45.103 |
| 5 | 44.147 |
| 6 | 44.290 |

Median is 44.720 ms, range 44.147–53.543 ms. Poll delay makes this a loose
observed upper bound on removable process restart cost in these runs, not a
universal bound or a measured persistent-kernel saving. A surviving process would
still need generation-safe worker identity, routing, configuration and task
rebinding. No such lifecycle was added or given time credit.

### Placement on the real edit path

`cmd/scenery/dev_build_pipeline.go` first tries cached workspace refresh, falls
back to `PrepareForCompileWithSnapshotContext` with a source snapshot, then calls
`CompileContext` before further metadata, database/runtime setup and activation.
`internal/build/workspace_cache.go` also selects ordinary public/private
projection while refreshing cached workspaces. These are source-level locations
where the preparation/build cut would need integration.

The experiment instead starts a new driver and passes a nil snapshot. Its
projection cache lifetime, control post-build checks and subsequent runtime work
differ from the full supervisor/frontend/assistant loop. Source mapping is not
measurement of that loop. Neither the 636.515 ms arm-median gain nor any individual
span is subtracted from earlier 0179 or 4.495-second observations.

## Validation Results and Remaining Gates

Every command in Validation and Acceptance above passed. The affected-package
command covers build, generate, codegen, CLI and the experiment driver; the full
repository Go suite runs inside the default verifier. Both committed fixture
regenerations plus the assistant fixture produce no changed committed bytes.
Focused race tests, lint (zero issues), TypeScript conformance and both TypeScript
projects pass. All four explicit probes pass, including real native contract,
managed-process, build-info and assistant process boundaries.

The default verifier reports the existing 41 knowledge-review and 22 architecture
warnings and an advisory 6.792-second repository Go-suite time against its
5-second cached-suite budget. This is not a failed exact-root 100 ms test budget.
The named-probe run carries the same knowledge/architecture warnings. No new
all-root timing audit or benchmark was selected.

Final documentation validation uses
`go run ./scripts/verify --quick --summary --write`; the complete implementation
was already validated by the full verifier. Full release, dashboard/UI/browser,
stream/debugger/custom-auth/internal-durable promotion and complete-loop 0179
measurements were not selected because this bounded candidate is not a product
runtime choice. Repository guidance for ordinary app workflows and the installed
skill remain intentionally unchanged.
