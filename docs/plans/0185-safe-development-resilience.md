# Safe Development Path Resilience

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current under `PLANS.md`.

## Purpose / Big Picture

Finish the resilience acceptance already left open by Plan 0180 and establish a
current performance/resource baseline for PR #195 after shared executable reuse
was deliberately restricted. This is the last acceptance slice for the repair:
it adds no new execution model and does not broaden whole-executable reuse.

The observable result is one explicitly selected development-process probe that
performs a behavior-preserving native input edit, a bounded series of unique Go
edits, a compile cancellation, failed candidates and predecessor recovery, then
proves that process count, runtime children, file descriptors, owner/aggregate
RSS and private cache growth settle within predeclared bounds. The existing
worktree-cost benchmark remains the creation/removal and 1/5/10 contention proof.

## Progress

- [x] 2026-09-13: Reconcile clean HEAD
  `c4702b336882dd70ed5c048b05fdd3097ed39adf`, active Plans 0180/0181,
  the supplied review, verifier ownership and the existing A18 benchmark.
- [x] 2026-09-13: Run and retain the current 30+30 unique-edit benchmark and
  repeated 1/5/10-worktree cost benchmark before changing the acceptance harness.
- [ ] Add the native-input and bounded repeated-edit/resource evidence to the
  existing `dev-process` probe without adding an ordinary test or runtime mode.
- [ ] Run focused tests, refresh changed-area selection, run its exact cumulative
  commands and the selected `dev-process` probe, then record cleanup and limits.
- [ ] Commit and update PR #195 only with this already-scoped acceptance evidence;
  leave owned external input capture to a separate plan and branch.

## Surprises & Discoveries

The safe `c4702b33` path measured p50 1,623.085 ms, p95 1,676.726 ms and
worst 1,686.346 ms for 30 unique candidate edits on the recorded Mac14,14.
Median phase evidence was: Go command 541.312 ms, exact executable preflight
512.573 ms, runtime bundle 112.037 ms, second current-input check 140.486 ms,
and activation 64.945 ms. Median queueing was 0.028 ms. This is a current
baseline, not evidence for another cache optimization.

The current framework selection already rewrites `scenery.sh` to an app-owned,
content-addressed immutable source snapshot and compiles the matching CLI there.
The remaining private-compilation exposure is other local module replacements,
not the selected framework. That correction keeps the next production refactor
narrow and avoids building a second framework snapshot mechanism.

## Decision Log

- 2026-09-13, Codex: finish resilience using the existing `dev-process` and
  `worktree-cost` lanes. Real process/toolchain evidence belongs there; a new
  public command, runtime mode or parallel telemetry product is unnecessary.
- 2026-09-13, Codex: measure current `c4702b33` before harness edits. The earlier
  executable-hit and 0181 worker timings describe different implementations.
- 2026-09-13, Codex: keep the repaired shared-cache reuse domain unchanged. This
  plan closes acceptance evidence only; owned external module sources are a
  separately scoped production change after PR #195.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

`scripts/verify/harness_self_dev_detach.go` owns the real detached development
fixture. `scripts/verify/harness_self_dev_handoff.go` proves failed preflight,
failed constructor recovery, a unique implementation edit and exact response
identity. `scripts/verify/harness_self_watch_batch.go` already exercises atomic
multi-file edits, edits during compilation, an older-generation round trip,
shared Go dependencies, declarations, configuration and embedded assets.

The new evidence stays in those owners. A private cgo package makes one C source
edit change compilation inputs while returning the same value. Additional unique
Go edits use the normal HTTP endpoint and exact candidate identity. A process
tree/resource sampler observes only the private supervisor and its descendants;
it does not enumerate, stop or attribute unrelated developer processes.

`scripts/verify/harness_self_worktree_cost.go` already creates and removes 48
Git worktrees across 1/5/10 cohorts, starts each cold and warm, samples native and
container resources under load and verifies exact SQL ownership before cleanup.

## Milestones

1. Preserve the current safe-path timing and worktree-resource reports.
2. Add a behavior-preserving cgo/native edit and 20 unique implementation edits
   to the real development handoff, with every response tied to its generation.
3. Record before/after process, child, FD, RSS and cache samples and enforce the
   fixed limits described below after the last generation settles.
4. Complete cumulative local validation and update PR #195 with the evidence.

## Plan of Work

Prepare the existing basic fixture with `cgo = "host"`, native extensions in its
declared implementation revision, and a tiny in-process C function whose result
is called by the ordinary handler. Change `return 7` to `return 3 + 4`; require a
new implementation/build identity and process while the normal response remains
identical.

After the existing invalidation matrix, make 20 unique Go implementation edits.
Each must compile genuinely new behavior and be verified at the normal endpoint
with the exact current candidate identity. Keep the existing rapid-edit sequence
as the cancellation/supersession proof and existing failed preflight/constructor
paths as failure and recovery proof.

Sample the private process tree after initial readiness and after the final
generation has settled. Require no extra process or runtime child, at most 32
additional file descriptors, at most 64 MiB owner RSS growth and 96 MiB aggregate
tree RSS growth. Private Go and Scenery caches may grow by at most 512 MiB and
5,000 regular entries during this bounded series. Record actual values even when
an assertion fails. These limits are selected before executing the modified probe.

## Concrete Steps

All commands run from `/Users/petrbrazdil/Repos/scenery`. Do not install a global
binary or mutate shared caches.

1. Edit only the verifier-owned fixture preparation and real-process probe.
2. Run `go test ./scripts/verify` and any focused helper tests.
3. Run `go run ./scripts/verify --quick --summary --write`, read
   `.scenery/harness/agent-context.json`, and execute every cumulative recommended
   command.
4. Run `go run ./scripts/verify --probe dev-process --summary --write` and retain
   its report under `.scenery/harness/0185-safe-path/`.
5. Run `git diff --check`, inspect the patch, commit and push the existing PR
   branch only after every selected check passes.

## Validation and Acceptance

The expected changed-area classes are `go-package`, `release-sensitive-or-runtime`
and verifier documentation. The exact minimum is:

```sh
go test ./scripts/verify
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --race --summary --write
go run ./scripts/verify --probe dev-process --summary --write
git diff --check
```

The selected probe must report 20 unique edit identities, the native input edit,
the existing superseded/failed/recovery assertions, fixed resource limits and
actual before/after samples. All private processes and roots must be cleaned up.
The already completed `go run ./scripts/verify --benchmark edit-latency --summary
--write` and `go run ./scripts/verify --benchmark worktree-cost --summary --write`
runs are accepted only as current baseline/resource evidence and do not satisfy
the 300/500 ms target or full release certification.

Compiler/generator fixtures are unselected unless those sources change. Storage,
assistant and deployment probes are unselected because their boundaries do not
change. Full release certification is unselected because this PR is not adding a
new execution model and the explicit cumulative probe is the acceptance owner.

## Idempotence and Recovery

The probe creates a private root, agent home, Go cache and Scenery cache. It owns
only descendant processes and exact roots created for its run. Cleanup uses the
normal `down` command before removing that root; failures retain bounded evidence
without stopping unrelated processes. The native and Go edits are made only in
the disposable fixture. Rerunning either benchmark creates a fresh private cohort.

## Artifacts and Notes

Current baseline reports are ignored machine evidence:

- `.scenery/harness/0185-safe-path/edit-latency-c4702b33.json`, SHA-256
  `520e0312523775397e341cdbafe099983f8bda84027d138ad60597ff1a40965c`.
- `.scenery/harness/0185-safe-path/worktree-cost-c4702b33.json`, SHA-256
  `feae501f0f3fa015f5113659474706d0f8133f5f84edff7d3ab1e6d561b34422`.

The edit benchmark ran on macOS 26.5.2, Mac14,14, 64 GiB, 24 logical CPUs,
Go 1.27.0 darwin/arm64, with developer workloads left running. The worktree run
completed all nine cohorts in 695,210 ms and removed their verified SQL and
observability resources. Warm cohort wall times were 2,263–2,277 ms (1),
4,876–5,071 ms (5), and 8,781–9,576 ms (10).

## Interfaces and Dependencies

No public interface changes. The verifier may add private evidence structs for:

```go
type harnessDevResourceSample struct {
    ProcessCount, RuntimeChildren, FileDescriptors int
    OwnerRSSKiB, AggregateRSSKiB                    int64
    SceneryCache, GoCache                          harnessTreeUsage
}
```

The production `internal/devprocess` supervisor remains the sole child-process
owner. The Go toolchain remains the native compiler and cache owner.
