# Native Build Driver Experiment

## Purpose / Big Picture

Determine whether a bounded retained build driver, using the pinned stock Go
compiler and linker directly, materially reduces a real ONLV handler edit through
an authenticated response when compared with stock `go build` over the identical
full generated application. This is repository-only experimental tooling; it
does not add or select a production backend.

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the experiment runs.

## Progress

- [x] 2026-09-14: Freeze protocol v1, scope, ordering, cache policy, sample counts,
  decision gates, workload commit, and evidence location before primary cohorts.
- [x] 2026-09-14: Record a structured 620-package full-ONLV recipe and prove one
  direct compile/link vertical slice plus 30 diagnostic authenticated edits.
- [x] 2026-09-14: Complete correctness and rejection matrix.
- [x] 2026-09-15: Complete two 30-pair primary cohorts. Observe 48 complete
  churn edits and the successful build artifact for edit 49; explicitly waive
  edit 50 after the watchdog fired rather than repeat the experimental run.
- [x] 2026-09-15: Publish the NO-GO decision and clean owned roots. Repository
  validation is shared with the immediately following Plan 0194 experiment.

## Surprises & Discoveries

The production generated main closes over 620 packages and 5,114 selected,
ignored, embed, native, and module files. A body-only AHJ change conservatively
rebuilds four packages; the direct tool path still spends roughly 0.9 seconds in
the stock linker. A preliminary full-runtime diagnostic completed 30 edits with
verified response and candidate identities, but approximately 0.7 seconds of
complete capture plus 1.1 seconds of artifact work already makes the payoff gate
unlikely. This diagnostic is not a primary paired cohort.

The two primary cohorts confirmed that complete repeated capture erases most of
the direct compile/link advantage. Across 60 samples per backend, stock
accountable-build p50/p95 was 2,352.273/3,048.912 ms and the retained driver was
2,268.237/2,677.773 ms. The 84.036 ms median reduction is only 3.6 percent. The
watchdog fired during churn edit 49 after its build artifact was published but
before the authenticated-response observation completed. Cleanup had already
partially removed the lane when the run was inspected, so edit 50 was explicitly
waived for this non-release experiment. The decision still rests on the complete
60-pair primary evidence, not the waived churn tail.

## Decision Log

- 2026-09-14, Codex: Protocol `scenery.native-build-driver`, revision 1, supports only Go
  function-body edits with unchanged package/file membership, imports, directives,
  module graph, target, flags, tool identities, native/embed inputs and environment.
- 2026-09-14, Codex: Use the same complete `go list -deps -json` plus byte/digest
  capture in both Stage I lanes. Accountable build is capture plus all backend
  validation, planning, archive, compile, link, digest and publication work.
- 2026-09-14, Codex: Use two owned roots, two warmups per lane, 30 alternating
  pairs per cohort, seed `0193-v1-balanced`, then swap backend/root assignment and
  bootstrap independently. Stock and driver caches remain isolated per lane.
- 2026-09-14, Codex: Stage II is conditional on Stage I meeting both the 25 percent
  and 100 ms median payoff gate in each cohort. It is unperformed otherwise.
- 2026-09-14, Codex: Retain at most twice captured archives plus 512 MiB; publish
  only the newest owner sequence by atomic rename and reject foreign identities.
- 2026-09-15, Codex: Reject the candidate after both full-ONLV cohorts. Its
  accountable-build p50 improvement was 84.036 ms (3.6 percent), below both
  payoff gates; its accepted-edit p50 improvement was 97.678 ms (1.3 percent).
- 2026-09-15, Petr: Treat the missing final churn observation as an experimental
  waiver rather than rerunning the hour-long benchmark. Preserve the distinction
  between 48 end-to-end edits, one build-only observation, and one unexecuted edit.

## Outcomes & Retrospective

The experiment completed 60 stock and 60 retained-driver full-ONLV samples.
Stock accountable-build p50 was 2,352.273 ms versus 2,268.237 ms for the driver;
accepted-edit p50 was 7,518.618 ms versus 7,420.941 ms. The driver therefore
failed the 25 percent plus 100 ms payoff gate and Stage II was not run. The churn
tail contains 48 complete edits, one successful build without response proof,
and one explicitly waived edit. Plan 0194 subsequently isolated repeated
discovery and validation as the actionable bottleneck rather than promoting
this candidate.

## Context and Orientation

`scripts/verify/internal/nativebuilddriver` owns recipe capture, direct compile
and link execution, snapshots, action identities, and a benchmark-scoped Unix
owner. `scripts/verify/harness_self_native_build_driver.go` owns disposable ONLV
roots, product runtime observation, pairing, evidence and cleanup. The pinned
workload is ONLV commit `4f8126a3e3806b7100ab7efaca1b7dd06b894221`.

## Milestones

1. Capture the exact production recipe and prove a real executable.
2. Fail closed for every unsupported or corrupted input class.
3. Run Stage I with identical full-ONLV scope and product response observation.
4. Run bounded churn, decide, document, validate and clean up.

## Plan of Work

Bootstrap each driver lane with stock Go, `-a -work -toolexec`, a disposable
cache, structured argv/config/output capture and owned archive copies. Keep the
recipe resident in a lane owner. On each edit, capture all current inputs into an
owned immutable generation, validate eligibility, rebuild the changed package
and transitive consumers, relink, hash, and atomically publish only the newest
generation. The stock lane runs the same complete capture before ordinary Go.

Drive the normal ONLV supervisor in two independent worktrees. Each sample edits
the real AHJ handler with a unique marker, observes the first authenticated typed
response from a new PID, and verifies candidate identity. Alternate pair order,
repeat with backend assignment swapped, then run separate driver churn.

## Concrete Steps

From the Scenery root run:

```sh
go run ./scripts/verify --benchmark native-build-driver --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
```

Evidence is bounded beneath `.scenery/harness/native-build-driver/<run-id>/`.
The original ONLV checkout is read only; all edits occur in owned worktrees.

## Validation and Acceptance

- Run `go test ./scripts/verify/internal/nativebuilddriver/...`.
- Run `go test ./scripts/verify` and `go test ./...`.
- Run `golangci-lint run ./...` and `git diff --check`.
- Run `go run ./scripts/verify --quick --summary --write`, read
  `.scenery/harness/agent-context.json`, and run the cumulative recommended union.
- Run the exact explicit benchmark command above. Require two complete 30-pair
  cohorts, correctness/rejection evidence, 50 churn edits and clean ownership.
- Do not run release or unrelated benchmarks. No production external boundary
  changes, so no named release probe is selected.

## Idempotence and Recovery

Each run uses a random owned directory, owner record and session identities.
Incomplete evidence remains marked invalid or incomplete. Cleanup stops and joins
only recorded children, verifies the owner marker, removes only registered Git
worktrees, and leaves the original checkout and global Go caches unchanged.

## Artifacts and Notes

The protocol, order, seed, cache policy and thresholds are immutable for a run.
Raw recipe records, snapshots, per-generation results, runtime identities,
resources, commands and cleanup receipts remain under the evidence root. The
preliminary `retained-driver-vertical-20260914` series is diagnostic only.

## Interfaces and Dependencies

The experiment uses the standard Go 1.27 compiler/linker, `-toolexec`, Git
worktrees, the existing Scenery producer and supervisor, ONLV's owned small
fixture, Unix sockets, and standard HTTP. Production packages do not import the
experimental helper and no public API, environment knob, fallback or daemon is
added.
