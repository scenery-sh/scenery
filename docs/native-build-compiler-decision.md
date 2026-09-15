# Native Build Compiler Decision

Status: retained direct compiler/linker is the normal development executor;
stock `go build` is not a development fallback.

## Decision

Normal product development builds use Scenery's retained input model and invoke
the captured Go compiler/linker tools directly. A product binary cannot select
stock execution through app configuration or an environment variable, and a
failed retained path does not silently fall back to a stock build. The stock
execution lanes in the repository benchmark are compiled only with the private
`scenery_benchmark_stock` build tag.

The direct executor deliberately does not reproduce Go dependency rules.
Bootstrap and graph reconciliation invoke the installed `cmd/go` to observe the
complete real compiler/linker action graph selected by Go. Compatible body edits
then validate retained inputs, compile the changed packages and transitive
consumers, link, and atomically advance state without another package-loading
pass. Membership, import, build/embed directive, module, tool, or configuration
changes return to graph reconciliation. Incomplete action capture fails closed.

Ephemeral, production-asset, and deployable artifact builds remain outside this
development decision and continue through their existing stock artifact path.

## Stable Experiment Names

Reports use these names consistently:

| Name | Input preparation | Executor | Product default |
|---|---|---|---|
| `GO-BUILD ONLY` | none beyond Go itself | stock `go build` | no; historical/control |
| `FULL-PREP + GO-BUILD` | complete capture | stock `go build` | no; control |
| `FULL-PREP + GO-TOOLS` | complete capture | direct tools | no; historical experiment |
| `RETAINED-PREP + GO-BUILD` | retained validation/state | stock `go build` | no; control |
| `RETAINED-PREP + GO-TOOLS` | retained validation/state | direct tools | yes |

Before Plan 0195, the production development path was `GO-BUILD ONLY`: ordinary
bare `go build`. “Stock after complete capture” was never that production
baseline.

## Final Short macOS Observation

The final corrected report is
`.scenery/harness/native-build-compiler/20260915T132011Z-f22d32d363813289/report.json`.
It used one fixed full-ONLV commit, one excluded warmup and three measured body
edits per lane. It is a bounded observation, not a release gate or a claim of
statistical stability.

| Path | Preparation/capture p50 / p95 | Artifact p50 / p95 | Accountable build p50 / p95 | First verified response p50 / p95 | Accepted edit p50 / p95 |
|---|---:|---:|---:|---:|---:|
| `GO-BUILD ONLY` | 0 / 0 ms | 2,574.633 / 2,615.545 ms | 2,599.317 / 2,640.803 ms | 4,864.583 / 4,973.170 ms | 6,778.089 / 6,898.230 ms |
| `FULL-PREP + GO-BUILD` | 1,577.988 / 2,425.855 ms | 1,199.411 / 2,577.410 ms | 3,650.096 / 4,180.654 ms | 5,879.699 / 6,411.113 ms | 7,776.102 / 8,332.989 ms |
| `RETAINED-PREP + GO-BUILD` | 251.304 / 284.410 ms | 2,623.186 / 2,628.151 ms | 2,933.540 / 2,971.419 ms | 5,241.790 / 5,368.037 ms | 7,133.020 / 7,341.488 ms |
| `RETAINED-PREP + GO-TOOLS` | 288.917 / 317.470 ms | 2,404.151 / 2,675.385 ms | 2,833.794 / 3,136.587 ms | 4,918.439 / 5,270.801 ms | 7,126.610 / 7,531.394 ms |

Against the equal-input `RETAINED-PREP + GO-BUILD` control, direct tools reduced
accountable-build p50 by 99.746 ms (3.40 percent). Accepted-edit p50 differed by
only 6.410 ms, while direct-tools p95 was worse. This evidence does not establish
a material speed win; the product decision to keep direct tools is explicit and
independent of that small short-run delta.

For `RETAINED-PREP + GO-TOOLS`, compiler p50 was 196.261 ms and linker p50 was
2,175.229 ms. The next measured executor bottleneck is therefore linking, not
package loading. A separate direct-link experiment measured `-w` at 538.757 ms
p50 versus 745.819 ms without it, but rejected the option because it removes
DWARF/debugger information. It is not enabled.

## Boundaries and Attribution

Every sample retains absolute phase start times and durations for package
loading, directory validation, input hashing, snapshotting, compilation,
linking, state commit, artifact handling, scheduler delay, first launch and
attestation, runtime activation, response observation, candidate verification,
and `implementation.check`. Final executables and `result.json` files are kept
per measured lane.

`first_verified_response` is the user-visible boundary. `accepted_edit` also
contains roughly 1.9 to 2.3 seconds of later candidate verification in this run;
that cost is separately reported and must not be attributed to build or first
execution. One `FULL-PREP + GO-BUILD` `implementation.check` sample was 93.7
percent above its lane median and is flagged as an outlier.

The benchmark's owned detached ONLV worktrees all used one pinned commit and
cleaned up successfully. The user's original ONLV checkout changed concurrently,
so the report correctly records `source_status_unchanged: false`; the selected
AHJ target itself did not change.

The launcher ancestry identifies ChatGPT/Codex. The human enabled ChatGPT in the
macOS per-application Developer Tools panel, while `DevToolsSecurity -status`
reported global developer mode disabled. These are separate controls; the
per-application state has no stable command-line readback in this environment.

## Correctness Boundary

Direct retained compilation is eligible only when package/file membership,
imports, build/embed directives, module graph, selected files, native inputs,
target, flags, environment, and tool identities match committed state. Captured
regular-file arguments, including import, embed and symbol-ABI configuration,
are rebound to private captured copies. Unsupported native-action frontiers are
rejected before tool execution.

The retained package graph is also the source for build-input discovery while
its directory and graph-affecting identities remain current. Compatible body
edits avoid both the initial and post-build `go list`; a directory change causes
re-listing. Final stamps still reject an input mutation before publication.
Candidate executable digesting is produced once and passed into the single
copy/hash publication pass.

The supervisor continues to own cancellation, bounded generations, exact
candidate identity, read-only preflight, activation, rollback, and executable
ownership. There is no new daemon or public configuration surface. Linux
comparison and release certification were not selected for this decision.
