# Identical Full ONLV Build Comparison

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as work proceeds.

## Purpose / Big Picture

The human requires the experimental build to compile all of ONLV, exactly like
the product, rather than the smaller AHJ island used by plan 0190. Compare the
real product Go invocation with the direct-driver experiment on identical full
runtime source, generated main, flags and linked metadata. Both use stock Go;
do not imply a distinct compiler exists.

## Progress

- [x] (2026-09-14) Confirmed both existing paths use stock Go; smaller island
  scope invalidates a same-workload speedup claim.
- [ ] Capture and execute complete full-ONLV driver commands in both lanes.
- [ ] Accept two warmups and 30 paired builds with identical input manifests
  and executable hashes, then restore source and stop the owned runtime.
- [ ] Publish results and validate documentation.

## Surprises & Discoveries

Not yet measured. Other tasks are concurrently editing instruction documents;
preserve their changes and bind measurements to the selected framework snapshot.

## Decision Log

- 2026-09-14, human: identical full ONLV compilation scope is required.
- 2026-09-14, Codex: instrument the actual Go driver launch via a run-owned PATH
  wrapper. It receives complete argv and environment, invokes the actual Go
  executable for both lanes, and only changes output destination and GOCACHE.
  Do not replay lower-level compiler/linker traces or infer Go dependency rules.
- 2026-09-14, Codex: use two isolated caches, two excluded warmups, alternate
  lane order, and include Go's complete dependency/source inventory. Hash inputs
  before and after and require byte-identical binaries. Product runtime checks
  and authenticated semantic edits remain enabled.
- 2026-09-14, Codex: wrapper runs both builds before returning to the supervisor,
  so instrumented edit-to-response and outer go.command times are not product
  latency estimates. Only separately timed real-Go subprocesses are compared.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Product `internal/build/compile.go` invokes `go build` on
`./scenery_internal_main` with flags and metadata from the real prepared result.
The run-owned wrapper intercepts only that target, preserving other Go calls.
Use the already owned stopped ONLV fixture
`/Users/petrbrazdil/Repos/onlv-builder-comparison-20260914` and the existing
authenticated AHJ edit runner. No production builder code needs to change.

## Milestones

1. Create isolated cache and evidence directories and the bounded wrapper.
2. Start only the owned fixture with wrapper PATH and product GOCACHE.
3. Run 32 semantic edits, verifying every full-runtime generation.
4. Restore source, verify cleanup, publish paired build statistics.

## Plan of Work

Capture the full build command directly at execution, not from verbose traces.
Use the same cwd and complete inherited environment in memory, changing only
cache and output. Do not persist environment secrets. Record build-relevant
environment and a digest of the complete environment. Use Go itself for package
and file discovery, including native and embed inputs, and compare both cache
contexts. Record command, source/producer identities, package count, binary size,
hash equality, paired durations and lane order. No runtime equivalence claim is
made from import count alone; require identical executable hashes and the normal
product HTTP response identity proof.

## Concrete Steps

Keep run-owned tooling and evidence under `.scenery/harness/full-onlv-build/`.
Use the existing measurement runner with a unique label, two warmups and 30
measurements. Record exact commands and artifact hashes in the report. Do not
modify the main ONLV checkout, shared caches, installed CLI, or VNEXT.md.

## Validation and Acceptance

Both lanes must compile the complete generated ONLV main with identical
dependency inventory, content hashes, build flags, metadata and toolchain.
Only GOCACHE and output path may differ. All 30 paired executables must be
byte-identical; all 30 semantic HTTP generations must pass current-source and
response identity checks. Source restoration and owned shutdown are mandatory.
Run `go run ./scripts/verify --quick --summary --write` and `git diff --check`
for these documentation and ignored experimental artifacts. If product or
verifier Go source changes become necessary, run affected package tests,
`golangci-lint run ./...` and `go run ./scripts/verify --summary --write` instead.
No Linux, release certification, production promotion, commit or push is requested.

## Idempotence and Recovery

Use a unique evidence root and never overwrite another run. The existing AHJ
runner restores only its own exact bytes. Retain failed evidence and owned
fixture data; stop only the verified owned runtime. Experimental outputs may be
removed after their exact hash/size comparison; do not delete shared caches.

## Artifacts and Notes

Plan 0190 remains immutable history. Its living report must clearly state that
the smaller-island comparison does not satisfy this identical-scope requirement.

## Interfaces and Dependencies

Use existing Go driver, generated product main and ONLV identity helpers. No
new product API, environment-variable knob, alternate runtime or subagents.
