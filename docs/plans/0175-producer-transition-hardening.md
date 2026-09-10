# Producer Transition and Verification Hardening

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as implementation proceeds.

## Purpose / Big Picture

An agent can update the selected Scenery framework while retaining control of
the existing runtime, then prove which application generation its validation
actually exercised. Preserve the implementation delivered in plan 0174; this
milestone fixes transition boundaries, not the runtime architecture.

## Progress

- [x] (2026-09-10 18:15Z) Read the review and confirmed the unconditional wrapper
  preflight, bootstrap-owned selection identity, and missing changed-path accounting.
- [x] (2026-09-10 18:44Z) Separate active-owner lifecycle control from desired-source verification; normal ONLV pin-drift and shutdown proof passed.
- [x] (2026-09-10 18:44Z) Selected producers publish their own selection and nested manifests; genuinely different-spec native and ONLV-launcher proofs passed.
- [x] (2026-09-10 18:44Z) Per-path checked/exempt/unverified accounting, explicit manual lanes and focused tests passed.
- [x] (2026-09-10 18:44Z) ONLV dependency/UI consumer fan-out and new-package/mixed-path regression cases passed.
- [x] (2026-09-10 18:46Z) Identity-bound `just smoke` passed real auth/project/attachment/object/tenant checks and same-build/new-process restart.
- [x] (2026-09-10 18:50Z) Native/ONLV producer transitions, real API smoke and five separately measured loop stages passed.
- [x] (2026-09-10 18:50Z) Validation matrix and all review requirements completed; publication and broad external/native lanes remain explicitly outside this local handoff.

## Surprises & Discoveries

The ONLV wrapper suppresses the JSON preflight output and applies current
module agreement even to `ps`, `logs` and `down`. `PrepareFramework` constructs
artifact identities in the bootstrap process although it compiles another
producer. `selectChangedProfiles` includes the default but records no unmatched
path obligations. These are current source observations, not runtime proof.

Native `dev-process` and ONLV's explicit transition script now prove A/B
specification revisions `a3a617ba...` and `70362606...`. The candidate accepts
its own outer/nested receipt while A still rejects it. ONLV owner/application
PIDs remained unchanged across both the module edit and B selection; normal
logs and shutdown succeeded. Snapshot provenance also had to avoid Git's
upward search: a snapshot inside ONLV must not report ONLV's HEAD as Scenery's.

The first identity-bound smoke correctly failed: ordinary `build` embeds
production assistant archives, while `up` consumes source-linked assets.
The input diff identified only the generated assistant asset projection and
archives. `build --development` now calls the existing development build path
without starting/replacing a runtime; the smoke uses that explicit variant.
Detached readiness can precede API PID publication; fixture startup waits
boundedly for the exact process fingerprint instead of accepting missing identity.

## Decision Log

- 2026-09-10 / Codex: Keep strict current schema/spec decoding. A producer must
  own the records it publishes; dispatching to that producer is not migration.
- 2026-09-10 / Codex: Work in the existing Scenery checkout and existing
  `onlv-coherent-task-environments` fixture checkout. Do not restart or modify
  the personal ONLV runtime on port 4920 or its unrelated Settings edits.
- 2026-09-10 / Codex: The requested measurements are separate end-to-end loop
  samples, not permission for an all-root timing audit or a release campaign.
- 2026-09-10 / Codex: Add explicit `build --development` for same-variant
  candidate evidence. Do not ignore assistant inputs, remove identity fields,
  or compare a production artifact with a source-linked development process.

## Outcomes & Retrospective

Completed locally on 2026-09-10, without commit or publication. Existing runtime
control and desired build selection are separate exact-producer boundaries;
different-spec candidates own their receipts. Changed validation cannot hide
unmatched paths or manual lanes behind the default profile. Smoke identifies
the actually served development build and rejects a different candidate.

The normal launcher and native probes provide transition evidence, not merely
unit coverage. ONLV remains coherent with its explicitly prepared local source
override in the existing fixture; do not commit that replacement. A later
authorized publication must pin the resulting Scenery module version. The
personal ONLV checkout and runtime on 4920 were not modified or restarted.

## Context and Orientation

`internal/build/framework_prepare.go` owns immutable source/executable
preparation. `cmd/scenery/framework.go` owns public selection commands.
`cmd/scenery/worktree_runtime_owner.go` holds the worktree lifetime lock;
`internal/agent` owns exact process and retained-resource identities.
`internal/validation` plans and runs application validation; `internal/app`
declares profile configuration. ONLV's `scripts/scenery` selects its executable,
`.scenery.json` maps checks, `internal/repoharness/context_checks.go` consumes
that map, and `development/tasks/smoke.task.ts` writes functional evidence.

## Milestones

First fix producer ownership and demonstrate both A-to-B transitions. Next
expose validation obligations and align the ONLV catalog. Finally strengthen
smoke identity, run real fixture acceptance, and measure the completed loop.

## Plan of Work

Keep desired framework selection separate from the producer of the active
runtime. Lifecycle commands must reach an exactly verified owner without
consulting changed authored module inputs. New generation, build and startup
must still reject incoherence. Retain a root-bound producer locator where
needed; locators are not resource authority and cannot permit old-state reads.

Build candidate B with bootstrap A, then let B recompute and publish its own
final selection and source manifest. Forward candidate command output without
requiring A to decode B's current protocol. Test a genuinely changed spec.

Extend changed validation with per-path coverage and visible unresolved
obligations. Default checks alone do not imply behavioral coverage. Map ONLV
root dependencies, shared UI consumers, Astro sources, native owner lanes,
new package paths and mixed changes through the same catalog.

Compose smoke identity from the served runtime's existing implementation and
build-input identity, including producer inputs, before and after requests and
restart. Reject an unrelated generation or changed source being mistaken for
the intended served implementation. Measure wrapper inspection, warm startup,
handler rebuild, focused validation and fresh worktree preparation independently.

## Concrete Steps

From the Scenery root, use `scenery inspect docs --for-path <path> -o json`
through `.scenery/harness/bin/scenery` as each new ownership boundary is touched.
Run affected-package tests before the full matrix. In ONLV use `just context`
on intended paths, the root-local wrapper, and only the marker-owned fixture.
Record exact implementation commands and evidence below as work progresses.

## Validation and Acceptance

From the Scenery root run `go test ./internal/build ./internal/validation
./internal/app ./internal/agent ./internal/machine ./cmd/scenery ./scripts/verify`,
`go test ./internal/edge`, `go test ./...`, `golangci-lint run ./...`, and
`go run ./scripts/verify --summary --write`. Inspect the resulting
`changed_area.recommended_commands` and run its cumulative union. Runtime and
CLI JSON changes require current schemas and contracts in the same diff.
The changed external producer/runtime boundary uses
`go run ./scripts/verify --probe dev-process --summary --write` (the
catalog owner of detached startup and framework preparation proof), plus
`go run ./scripts/verify --probe parallel-runtime --summary --write` for
parallel runtime isolation.

In `/Users/petrbrazdil/Repos/onlv-coherent-task-environments`, run
`just check-harness`, `just repo-harness`, `./scripts/scenery check -o json`,
`go test ./...`, `./scripts/scenery harness -o json --write`, and `just smoke`.
Run table-driven selection tests for root package/lock files, House C++, Blog
Astro, shared UI consumers, a new package, and mixed Go/frontend changes.
Native/GPU execution remains explicitly unexecuted in those selection tests.
No frontend product behavior changes are planned; browser product suites are
not substitutes for these launcher, selection and real API/restart proofs.

Acceptance additionally requires A running, authored pin changed to B, normal
wrapper `ps`, `logs` and `down` succeeding, and incoherent new build rejected.
Build a different-spec candidate B with A and pass B `framework inspect`
through ONLV's normal launcher. Record before/after smoke served identities and
all five requested measurements, including sample counts and limitations.
Full release and all-root timing are not selected and must not be reported passed.

## Idempotence and Recovery

Never rewrite retained identities to bypass rejection. Failed selection leaves
the old runtime controlled by its own verified producer. Partial candidate
preparation is disposable and retryable; preserve authored concurrent edits.
Runtime experiments and data writes use isolated marker-owned fixtures only.
Clean up only processes and fixture resources created and verified by this work.

## Artifacts and Notes

Starting reviewed revisions: Scenery `7f1493eb`, ONLV `c36e0100`. Both task
checkout working trees were clean at initial inspection. Full evidence remains
under ignored `.scenery/harness/`; bounded summaries belong in this plan.

Final ONLV smoke evidence uses framework source
`sha256:dea00886ce507621e0c196aa530dadc8072d73eeb141c64ac344f3a738e48795`
and executable `sha256:4684cf7a543f2250c45aa785f2b7fc4c831cb11e29802a61551c6e5cbbe01d3c`.
Before/after implementation is
`sha256:a81f9fc9a6a49cdd19947807a9f0ec91e388d0914cb844918710e7a932bddb38`,
build inputs `sha256:70fb2a6142740b4e0cfa5fdc926704be37c8cc94ca74d5e0164051da56f55357`.
Application PID changes 12691 to 13627 and owner PID 11244 to 13226 at the
same `http://localhost:4070` origin. Every tested response was identity-checked;
the final rebuilt candidate still matched current authored input.

### Measured loop (serial, warm caches)

The owned ONLV measurement script is `.scenery/harness/measure-hardening.ts`;
raw samples and fresh-root identities are in `hardening-loop.json`. These are
end-to-end wall times, not isolated-test p95 or a cold-machine benchmark.

| Stage | Samples | Median or single sample |
| --- | ---: | ---: |
| Ordinary `./scripts/scenery ps -o json` | 3 | 251.42 ms |
| Same producer `ps` with the same private agent home, no wrapper | 3 | 89.53 ms |
| Desired `framework inspect` through wrapper | 3 | 283.67 ms |
| Verified immutable `framework use --source <selected snapshot>` reuse | 3 | 642.32 ms |
| `./scripts/scenery validate development -o json` | 3 | 585.16 ms |
| Warm `up --detach --wait ready`, including published API identity | 3 | 17,373.69 ms (range 15,271.78–22,454.55) |
| Handler source edit to new served implementation/PID | 1 | 28,405.93 ms |
| Fresh worktree creation, current patch transfer, framework, dependencies, preset and ready app | 1 | 84,250.03 ms |

The wrapper adds about 162 ms over the direct process in these samples; no
claim that hashing alone accounts for all of that difference. Preparation
reuses a verified producer without rerunning dashboard/Go builds. Handler
measurement appended a non-semantic source comment, observed new linked HTTP
identity, then restored the exact original source and implementation.

The fresh root is
`/Users/petrbrazdil/Repos/onlv-coherent-task-environments-featproducer-hardening-measured`.
It used the same `dea00886...` framework source, a separate executable, fixture
`d521ee48-c759-4460-b87b-c463b746b013`, and origin 4074. It was stopped through
its own wrapper after successful preparation; its independent data is retained.
No dirty state or credentials were copied from the personal checkout.

### Validation results

Scenery root commands passed: `go test ./internal/build`,
`go test ./internal/app ./internal/validation ./cmd/scenery`,
`go test ./internal/machine`, `go test ./runtime`,
`go test ./internal/app ./internal/build ./internal/validation ./internal/agent
./internal/machine ./cmd/scenery ./scripts/verify ./internal/edge ./runtime`,
`go test ./...`, `golangci-lint run ./...`,
`go run ./scripts/verify --summary --write`,
`go run ./scripts/verify --probe dev-process --summary --write`, and
`go run ./scripts/verify --probe parallel-runtime --summary --write`.
Default/probe verification has no errors; 41 existing knowledge freshness and
23 architecture warnings remain. The default full-suite aggregate also exceeded
its advisory 5 s cached target on some runs; this is not an isolated-test p95 claim.

ONLV commands passed: `go test ./internal/repoharness ./cmd/repoharness`,
`bun test development` (6 tests),
`apps/nextnext/node_modules/.bin/tsc --noEmit --project development/tsconfig.json`,
`just repo-harness`, `just check-harness`, `./scripts/scenery check -o json`,
`go test ./...`, `./scripts/scenery harness -o json --write`, `just smoke`,
and `bun development/prove-producer-transition.ts`.
The explicit measurement script completed every stage and its fresh-root cleanup.

The updated installable skill passed
`uv run --with pyyaml python /Users/petrbrazdil/.codex/skills/.system/skill-creator/scripts/quick_validate.py /Users/petrbrazdil/Repos/scenery`.
System and bundled Python lacked PyYAML; the isolated uv run changed neither
global installation. Bash syntax and both repository `git diff --check` passed.

Not selected: `scripts/release-gate.sh`, an all-root timing audit, broad ONLV
`just lint`, UI/browser suites, and the manual House C++/GPU lane. No product UI,
native behavior or public deployment changed. Native obligations are visibly
unverified in changed-selection evidence, not reported passed. Compiler and
generator sources were not changed, so committed client regeneration was not
an applicable validation row.

## Interfaces and Dependencies

Use existing Go standard-library subprocess/filesystem boundaries, machine
artifact identities, worktree process locks, validation profiles and build-input
manifests. Do not add ambient environment knobs, compatibility decoders,
another orchestration system or another identity scheme.
