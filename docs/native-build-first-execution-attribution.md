# Native Build and First-Execution Attribution

Status: macOS measurement complete; Git stamping follow-up identifies a large
avoidable driver cost. Exact platform-loading causality remains unresolved.
Linux is deferred by the human. No production backend changed. The human added
ChatGPT to Developer Tools; the agent did not change security policy.

## Outcome

Stock-Go build remains above the experimental gate even in the faster observed
launch regime. Package loading is a measured substantial phase, not an inferred
remainder. The evidence does not establish a universal 400 ms first-launch floor
or identify the platform mechanism behind the change between runs.

The follow-up below narrows the broad package-loading label: much of its cost
is Git provenance/version discovery, not source parsing. The product's app build
already passes `-buildvcs=false`; this experiment removes overhead from its own
benchmark, not newly from the product. Remaining
loading, action setup and cache validation still need investigation.
A lower-level compile/link experiment is legitimate, but not yet a
validated replacement: it must preserve complete Go-derived dependency inputs,
include ongoing validation costs, and receive separate authorization. No command
strings from these reports may be replayed as a complete build recipe.

## Git stamping follow-up: 2026-09-14

Product-scope correction: `internal/build/compile.go` already appends
`-buildvcs=false` when building `./scenery_internal_main`; the ordinary
`scenery up` pipeline calls that owner. Framework preparation also disables
automatic VCS stamping. Therefore the reductions below are benchmark results,
not an additional available speedup for the product's existing Go build.
Product input discovery requests selected Go JSON fields and uses a guarded
digest cache, unlike this benchmark's full capture. Neither the 257 ms capture
nor the 690 ms total is a measurement of the full product path.

The human authorized trying `-buildvcs=false`. No verifier or production source
changed. From clean Scenery commit `4b275d14c5f35eba2fd7fdaac31f50133e3431e9`,
two serial cohorts used the existing runner, each with two excluded warmups,
30 unique-artifact first/repeated pairs and five separate diagnostic pairs:

```sh
GOFLAGS=-buildvcs=false go run ./scripts/verify --benchmark native-reload-attribution --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
GOFLAGS=-buildvcs=auto go run ./scripts/verify --benchmark native-reload-attribution --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
```

The disabled cohort ran first, then the automatic-stamping control. `GOFLAGS`
applies to the harness's `go list` as well as `go build` and framework preparation;
this is not a build-command-only treatment. The recorded Go environment and
input identity include the flag. No persistent Go configuration was changed.
Both use the same pinned ONLV revision and framework source digest recorded
below, Go 1.27.0, and the same host/launcher. Native Settings showed ChatGPT
enabled in Developer Tools during the disabled run and after both cohorts.

| Boundary | `auto` p50 / p95 ms | `false` p50 / p95 ms |
|---|---:|---:|
| Input discovery, hashing and evidence | 403.742 / 420.171 | 256.584 / 284.686 |
| Build | 551.220 / 606.307 | 398.853 / 431.122 |
| First launch through ready | 34.513 / 36.113 | 34.124 / 36.788 |
| Build through verified first response | 591.987 / 648.775 | 437.447 / 471.951 |
| Edit through verified first response | 992.548 / 1032.757 | 690.349 / 739.013 |

These are separate distributions, not additive phase medians. The observed
build p50 reduction is 152.368 ms (27.6%); edit p50 reduction is 302.199 ms (30.4%).
First launch is essentially unchanged. Both total and experimental build gates
still fail. Removing Go's optional Git stamp does not remove compiler dependency
validation, and the fixture still proves its own linked generation, input
identity, independent executable digest, constructor and new typed response.

The diagnostic `load.PackagesAndErrors` span fell from 266.734–292.869 ms with
automatic stamping to 110.387–121.826 ms without it. Inspection of the stock
Go 1.27 source shows `setBuildInfo` performing `git status`, `git log`, and local
revision/tag/version lookup. In the earlier automatic diagnostic 01, the runtime
syscall profile attributed 139.510 ms aggregate blocking to `setBuildInfo`,
including 62.48 ms under `gitStatus` and 76.58 ms under module revision lookup.
Disabled-cohort diagnostic 01 attributed only 0.147 ms there. Profile blocking
is not an additive wall-clock partition, but it corroborates the mechanism and
the separately measured build reduction.

Limitations: fixed cohort order, separate disposable worktrees, different
run-unique markers/framework executable identities, retained shared caches and
uncontrolled background load. This is not randomized/interleaved same-worktree
A/B evidence, and diagnostics are not pooled with primary samples. The return
to roughly 551 ms in the later control supports the flag effect, but does not
establish an exact guaranteed saving. The product already disables automatic
Git stamping on app compilation; any remaining optimization must be measured
through the actual product path while preserving Scenery's identity checks.

Both benchmark commands passed with the existing 41 knowledge and 21 architecture
warnings. Each report verifies 30 distinct first digests/inodes, same-artifact
repeats with new PIDs, negative identity/lifecycle checks, stopped children,
removed owned worktree and unchanged original checkout. No production source,
completed plan, protected target or installed CLI was modified.

Raw evidence under `.scenery/harness/native-reload-attribution/`:

- Disabled: `attested-2273766442/report.json`, SHA-256
  `102c82e7cab285aa12a92f8f8f0c867932607ba4534a7f7289debb150707884b`.
- Automatic control: `attested-540312906/report.json`, SHA-256
  `bec6e5d4960cee6b4cab221ae58dc09c855bdca95075c6a043b1263b3ce80d19`.

## Policy-observed confirmation cohort

After the human confirmed adding ChatGPT to Developer Tools, run
`attested-3231842688` repeated the complete protocol. Native System Settings
showed **ChatGPT: on** before and after this run (observations recorded at
2026-09-14 12:43:36Z and 12:45:41Z). The installed app's display name and bundle
ID were read from its Info.plist and matched `com.openai.codex`. No switch was
touched by the agent. This is UI evidence, supplementing the denied database
queries; it does not establish kernel-level responsible-process attribution.

| Boundary, 30 primary pairs | p50 ms | p95 ms |
|---|---:|---:|
| Build | 549.368 | 572.230 |
| First launch through ready | 34.292 | 35.921 |
| Same-artifact repeated launch through ready | 31.937 | 35.008 |
| Paired first minus repeated ready | 2.356 | 4.394 |
| Build through verified first response | 589.326 | 610.357 |
| Edit through verified first response | 985.042 | 1026.841 |

All 30 first artifacts again had unique digests and inodes; every repeat retained
artifact identity and used a different PID. All negative identity/lifecycle
checks and owned cleanup passed. The five separate diagnostics had 635.922 ms
build p50 and package-loading spans of 257.730–272.841 ms. Do not pool this cohort
with the earlier one below. The source digest matches the earlier complete run;
the framework executable digest is
`sha256:0b7b48df1279d0e96ec4fb0ac6943ae8d17fb86905c54c5098329f3139c4dd18`.

The human's report establishes a change in the launching environment. Its exact
timestamp relative to the first two runs is not recorded, and the runs were not
a controlled policy A/B. The confirmation cohort supports fast first launch
under the observed enabled setting, not an exact causal speedup estimate. Build
and combined gates still fail; mechanism attribution remains insufficient.

## Earlier complete cohort: scope and identity

Run `attested-1299465927` used Scenery base commit
`5a0108bb0b00377177ec49d943d42aebbdbdc286` plus the uncommitted verifier changes,
framework source digest
`sha256:c2116ebcc008f00b6d34fbe9c29cc8d57c055036863b469051656a4933e22109`,
framework executable digest
`sha256:8e0b4355113d6a714327ffdf43569ec073b29ed576a066752c7758412af8a077`,
and protocol revision
`sha256:f92d0188b57c86b0ad8a2bc2a42d9dfb808935acff5e134eae89d5215aa118df`.
The workload is ONLV commit `4f8126a3e3806b7100ab7efaca1b7dd06b894221`,
the real 238-package AHJ island, Go 1.27.0, darwin/arm64, Apple M2 Ultra,
64 GiB RAM. Existing caches were retained; no cache purge was performed.

The private typed validation operation uses a rejecting SQL capability. This is
not authenticated HTTP, a database journey, or full application acceptance.

Two excluded warmup pairs precede 30 primary pairs, followed by five separately
instrumented diagnostic pairs. All 30 primary artifacts have distinct digests
and inodes. Each repeated execution uses the same artifact and a different PID;
input freshness, linked identity, self digest, constructor-before-ready and new
typed behavior were verified. Wrong-identity/pre-activation checks and lifecycle
rejection checks remain enabled. Children stopped, the owned worktree was
removed, and the original ONLV checkout status stayed unchanged.

## Earlier complete cohort: primary measurements

Nearest-rank quantiles, milliseconds; diagnostic samples are not pooled here.

| Boundary | p50 | p95 |
|---|---:|---:|
| Go build | 531.287 | 551.659 |
| First launch through constructor-ready observation | 32.354 | 34.748 |
| Same-artifact repeated launch through ready | 30.283 | 33.002 |
| Paired first minus repeated ready | 2.311 | 4.617 |
| Build start through verified first typed response | 567.287 | 588.227 |
| Source edit through verified first typed response | 964.243 | 998.071 |

The separate experimental build/launch/combined gates remain 200/100/250 ms.
Build and combined latency fail. The full 300 ms p50 / 500 ms p95 semantic-edit
goal is neither met by these timings nor certified by this private fixture.
Repeated execution never replaces first execution in either comparison.

## Build attribution

Five diagnostic builds took 587.357–644.838 ms (p50 608.713 ms), versus
531.287 ms primary p50. Instrumentation overhead and series ordering are
confounders: do not subtract diagnostic components from primary quantiles.

| Phase | Observed diagnostic range | Interpretation and limits |
|---|---|---|
| Package loading | 245.780–278.983 ms | Stock-Go `load.PackagesAndErrors` span; includes work and waits, not exclusive CPU. |
| Action setup | Not isolated | Driver/action containers are retained; no fabricated exclusive duration. |
| Cache validation | Observed stacks, no exclusive wall interval | Runtime trace profiles include `DiskCache.Get`, `GetMmap`, `FileHash` and `checkCacheActor`; concurrent/nested costs cannot be summed. |
| AHJ compiler command | 29.844–31.566 ms | Subprocess wall time, enclosing action 32.090–33.968 ms. |
| Island-main compiler command | 32.456–35.954 ms | Subprocess wall time, enclosing action 34.449–38.445 ms. |
| Linker command | 148.248–157.471 ms | Subprocess wall time, enclosing action 170.092–180.984 ms. |
| Tool action queue | 0.003–0.031 ms | Go action ready-to-start delay for the three tool actions; not total OS scheduling delay. |
| Post-driver artifact handling | 3.786–3.952 ms | Parent-side hash, permissions and stat; Go-internal installation remains inside driver. |

The action DAG records main/link dependencies rather than assuming every package
action is sequential. Compiler/linker intervals are contained in their actions;
never add both. Driver trace logical context IDs can be reused concurrently:
the parser unions active intervals by context/name, records multiplicity, and
does not invent a nesting tree from those IDs.

For diagnostic 01, the observed final dependency chain is AHJ compile (action 5),
main cache check (239), main compile (2), link (1), install (0). It spans
249.416 ms from AHJ action start to install completion, including the intervening
waits. The main cache-check action itself takes 0.383 ms and the AHJ cache-check
action 0.285 ms; neither includes every cache operation elsewhere in the driver.
This is the observed final chain, not a complete attribution of pre-chain work.
Action command records are shell-form strings, not structured argv or retained
import/embed configuration; they are insufficient for lower-level build replay.

Offline `go tool trace -pprof=sched` analysis of diagnostic 01 reports 83.989 ms
aggregate goroutine scheduling delay. Its syscall profile reports approximately
2.15 s aggregate blocking, including overlapping cache/filesystem operations and
process waits. Neither is a serial component of the 626.378 ms driver elapsed
time. Trace-writing stacks themselves are visible: this is diagnostic evidence,
not a production CPU profile or a cache-validation lower bound.

Parent and Go trace clocks are kept separate. For diagnostic 01, the parent
ledger spans 1053.314 ms, directly covers 392.644 ms of leaf observations and
leaves 660.671 ms unattributed at that clock level. That residual contains the
enclosing driver and launch work; it is not idle time or an optimization budget.

## First-execution attribution and platform uncertainty

The process-start API took 1.739–1.924 ms in the five diagnostics. This wraps the
existing process owner; it is not a pure kernel process-creation measurement.
First launch to attestation took 34.181–35.625 ms. Child main-entry to attestation
preparation took 3.705–3.946 ms, including executable hashing. The actual AHJ
constructor took 0.001125–0.005583 ms. Activation-to-ready includes pipe transfer
and observer scheduling as well as that constructor.

Fresh-artifact init traces reached the last initializer at 23–24 ms; repeated
traces at 21–22 ms. These are Go-relative, rounded diagnostic timestamps. They
do not timestamp executable loading or align Go initialization entry with the
parent clock. No OS loader event or platform-validation span was collected.
Those phases remain unknown rather than zero.

An earlier run, `attested-195946718`, completed 30 primary pairs but failed its
first diagnostic on the original parser's invalid LIFO assumption. Preserve it
as incomplete evidence, not acceptance: first-ready p50 was 394.459 ms and
repeated-ready p50 31.917 ms. The successful run already had approximately 32 ms
first-ready in both warmups. The launcher executable and recorded Go environment
match, but verifier source and temporal/cache state differ. This is not a
controlled A/B and no runtime optimization explains the difference.

The launcher ancestry contains `/Applications/ChatGPT.app`, bundle
`com.openai.codex`, version 26.908.40834, build 8881, executable digest
`sha256:ecad78dbf98adb89ec475edac86630406cbe59d9f3070b17d88065f136b94bcb`.
Ancestry alone does not establish the responsible TCC application. Read-only
queries for this bundle's Developer Tools row were denied in both user and
system TCC databases. Policy at those earlier sample times is **unknown**, not
disabled or absent. The subsequent human confirmation and before/after UI
observations apply as described in the confirmation cohort, without rewriting
the raw earlier reports. No policy modification was performed by the agent.

| Claim | Evidence | Confounder / remaining limit |
|---|---|---|
| Build is a current bottleneck | Every primary series exceeds the build gate; loading and tool spans are observed | Diagnostics are slower and cannot prove achievable speedup |
| Constructor does not explain hundreds of milliseconds | Direct child constructor duration | Private island, not full application construction |
| Fresh artifact can start near 32 ms on this host | 30 unique artifacts plus warmups | Platform policy/state unresolved; not a universal guarantee |
| Earlier slow first launch may involve platform loading/validation | Large first/repeat difference with short child-main work | Hypothesis only; no OS causal trace or controlled policy comparison |

## Reproduction and evidence

From the Scenery repository root:

```sh
go run ./scripts/verify --benchmark native-reload-attribution --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
```

Raw machine-local evidence remains ignored under
`.scenery/harness/native-reload-attribution/`. Successful `report.json` SHA-256:
`640bbbcd36d3c9bcfd6e1314010f10a02708515deca80cf04269390a854933c8`.
Incomplete run report SHA-256:
`95395358192f6cc69f5545398d8e80deb588696a6b56e13020ad9e27c574da29`.
Successful diagnostic-01 driver runtime trace SHA-256:
`6183f7aeb949abf11b3859ef51cae8acf189c094328e6da099ac2072098c4e95`.

Policy-observed confirmation run `attested-3231842688/report.json` SHA-256:
`3632c3752189f59349416397e47bf7c48917af05ae9ff951503e801b78bb4ae6`.
Its diagnostic-01 runtime trace SHA-256:
`2304dee55c977a8a907efa9b3dd669c2e718674c9bdaa5d97d18926b2b34bc2a`.
The adjacent `launcher-policy-observation.json` records the supplementary native
UI observation and human confirmation; the original raw report is unmodified.

The implemented lane bounds individual evidence files at 32 MiB, init output at
64 KiB and total evidence at 2 GiB, rejecting incomplete traces. No OS trace
collector was implemented: the plan's proposed OS-trace cap is not an exercised
capability. See [Plan 0189](plans/0189-native-build-first-execution-attribution.md)
for implementation validation and the deferred follow-up boundaries.
