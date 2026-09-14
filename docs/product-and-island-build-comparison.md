# Product and Island Build Comparison

## Identical full-ONLV result (supersedes the scope-mismatched comparison)

The human required identical compilation scope. The direct-driver experiment
now compiles the actual complete product `./scenery_internal_main`, not an AHJ
shim. Thirty accepted paired builds show no observed build-speed advantage:

| Real Go subprocess, nearest-rank statistics | Product invocation | Full-ONLV direct-driver experiment |
|---|---:|---:|
| p50 | 1169.606 ms | 1170.521 ms |
| p95 | 1275.589 ms | 1339.742 ms |
| Mean | 1185.342 ms | 1194.955 ms |

The median paired experiment-minus-product difference is -0.156 ms; the mean
paired difference is +9.613 ms. The experiment is not demonstrably faster in
this cohort. Both lanes use the same stock Go driver algorithm. This experiment
does not implement direct compiler/linker integration or promote a new backend.
The previous approximately 400 ms AHJ build was a smaller workload, not evidence
that the full ONLV application can build that quickly.

### Exact scope and equality proof

Every pair has 620 Go packages and 2899 Go-reported source/module/native/embed
input files, including the real generated main and all its application service
dependencies. Both outputs are byte-identical approximately 52 MB executables.
This is the full production Go application binary, not a frontend build or
independent assistant-worker asset rebuild.

A run-owned Go wrapper receives the complete actual production argv, cwd and
environment directly from the running product. It executes that command and
the experimental command, changing only output destination and GOCACHE. Full
linker identity flags, cgo configuration, build target and source bytes remain
identical; no lower-level trace command is reconstructed. Go itself discovers
the complete dependency/file inventory in both cache contexts. Content hashes
must match before the pair and remain unchanged afterward. Executable SHA-256
must match. The product then passes normal candidate admission, serves the new
authenticated AHJ behavior, and passes independent current-candidate verification.

Both driver caches are run-owned and separate. Native implementation checking
uses a third cache so it cannot precompile the current edit into only one lane.
Initial setup and two warmups are excluded; the 30 pairs alternate execution
order, 15 product-first and 15 experiment-first. The first exploratory cohort,
`full-onlv-paired-20260914`, had a possible validation-cache priming advantage and
is diagnostic only. The final cohort is `full-onlv-isolated-20260914`.

Times start immediately before launching the actual Go binary and end at its
exit. Input discovery, hashing and product orchestration are outside these
numbers. The wrapper runs both builds before returning to the supervisor, so
its outer `go.command` and HTTP edit-loop timings intentionally include duplicate
work and are **not** production latency estimates. First-execution/startup speed
was not compared in this cohort. Warm Go action caches and source pages were
used, not cold-machine compilation. Other developer workloads were not stopped;
no statistical equivalence claim beyond the observed cohort is made.

### Revisions, commands and evidence

ONLV commit is `4f8126a3e3806b7100ab7efaca1b7dd06b894221`. The already selected
immutable Scenery snapshot is HEAD `825f46194bf109887b4856b20c82e261ce565e4c`
plus the source-identity fix, source digest
`sha256:724ebe661cda1eef291a81053b33ab25de0dbc2064307b0497de4fd2a22c869f`,
producer executable digest
`sha256:911af0a7c81db3f94986966a6bed012cece48a54abec6bcf1a3e2e7d79f2e643`.
Native macOS arm64 uses Go 1.27.0, GOMAXPROCS=2, cgo enabled and product
`-buildvcs=false` in both lanes. Concurrent instruction edits in the source
checkout were not substituted into that selected framework snapshot.

The wrapper lives at `.scenery/harness/full-onlv-build/run-20260914/bin/go`.
Only the owned ONLV fixture is started with that directory prepended to PATH
and sibling `product-cache` as GOCACHE. It internally separates
`experimental-cache` and `validation-cache`. From the Scenery root:

```sh
bun .scenery/harness/builder-comparison/measure-product.ts full-onlv-isolated-20260914
bun .scenery/harness/full-onlv-build/summarize.ts full-onlv-isolated-20260914
```

- Summary: `.scenery/harness/full-onlv-build/run-20260914/summary.json`,
  SHA-256 `3d3758f7fbc7ae4ac23d5a4f493a933e3b7dd38a60b3e8e1554688c9872a0da5`.
- Full pair manifests, argv, environment digest, timings and binary hashes are
  under its `pairs/` directory and individually hash-bound by the summary.
- Runtime identity/restoration report:
  `.scenery/harness/builder-comparison/full-onlv-isolated-20260914/report.json`,
  SHA-256 `a7e42dfa77b91980c29dd4ba617de72fb657b5bb213c006360cf8443ac99a6ac`.
- Wrapper SHA-256:
  `48bb5f4940085b48706e91cfac80661497552ae6e3e52aefd76ae3c21cc584b0`.

All 30 pairs passed input, executable and served-identity checks. Original source
was restored byte-for-byte and verified; a fresh `ps` reports the owned fixture
stopped with no sessions. Experimental output copies were removed after hash
comparison; owned caches and evidence are retained. No product default, shared
cache, installed CLI or human-owned target document was changed by this experiment. The wrapper is
run-local experimental tooling, not a new supported product build mode.
See [Plan 0192](plans/0192-identical-full-onlv-build-comparison.md).

Documentation validation: `go run ./scripts/verify --quick --summary --write`
passed with 41 knowledge and 21 architecture warnings, no errors;
`git diff --check` passed. Product/verifier Go code was not changed by this
same-scope experiment, so no new package test/lint or release probe was needed.
Linux and full release certification were not selected.

## Historical smaller-island comparison

The comparison below does not satisfy identical full-ONLV compilation scope and
must not be cited as a same-workload builder speedup.

Status: comparison completed after repairing cached-preparation source identity.
Both current series have 30 accepted samples. This is a comparison of differently
scoped workflows, not proof of a faster compiler or an interchangeable backend.

## Accepted comparison after the fix

The human authorized the fix on 2026-09-14. Cached preparation now captures the
current source fingerprint before copying source, matching full preparation, and
assigns it after successful materialization. The existing implementation-edit
test failed before the fix and passed afterward; candidate checks were not relaxed.

Both runs use Scenery HEAD `825f46194bf109887b4856b20c82e261ce565e4c` plus the
uncommitted identity fix, with exact framework source digest
`sha256:724ebe661cda1eef291a81053b33ab25de0dbc2064307b0497de4fd2a22c869f`.
ONLV remains at `4f8126a3e3806b7100ab7efaca1b7dd06b894221`.
Product producer executable digest is
`sha256:911af0a7c81db3f94986966a6bed012cece48a54abec6bcf1a3e2e7d79f2e643`;
island producer executable digest is
`sha256:8e208370d0a8bc73dd7b12367fd8f525e54a10cd2b945f3c62526865755d7525`.
Both application build paths disable VCS stamping. The island command sets
`GOFLAGS=-buildvcs=false`; product passes the flag explicitly. Producers are
recorded separately rather than claimed byte-identical.

Native macOS arm64, Go 1.27.0, existing shared Go cache; product first, island
second, with no concurrent agent tests or benchmarks. No cache purge or unrelated
workload shutdown occurred. The human previously enabled ChatGPT Developer Tools;
this run did not re-observe the UI, and automated policy attribution remains
`unknown`. No OS policy was changed. Linux remains deferred.

Each series excludes two warmups and accepts 30 unique source edits. Quantiles
use nearest rank (p50 is sorted sample 15, p95 sample 29).

| Boundary | Full ONLV product p50 / p95 | AHJ island p50 / p95 |
|---|---:|---:|
| Go build subprocess | 1265.978 / 1358.096 ms | 399.839 / 432.098 ms |
| Source edit through new semantic response | 4110.905 / 4383.164 ms | 687.096 / 751.062 ms |

The product response is authenticated HTTP through the complete app. The island
response uses the experimental inherited-pipe dispatcher and the smaller AHJ
closure. Product also runs candidate preflight before normal app launch; its
activation is not an isolated first-execution metric. Island numbers above use
first execution, never its faster repeated launch. Its first-launch-through-ready
p50/p95 is 33.929 / 35.338 ms. Five diagnostic pairs are excluded from primary
statistics. Its attribution decision remains `insufficient_attribution`, not
backend promotion or latency-target success.

The workflow ratio is approximately 5.98, and the build-subprocess ratio 3.17;
neither isolates compiler efficiency. Both use stock `go build`, not direct
`go tool compile` / `link`. No identical-artifact or interleaved A/B is claimed.

### Product phase detail

| Recorded phase | p50 | p95 |
|---|---:|---:|
| Framework verification | 43.919 ms | 69.389 ms |
| Cached workspace preparation | 132.616 ms | 193.329 ms |
| Runtime bundle and initial input preparation | 337.135 ms | 381.532 ms |
| Go build subprocess | 1265.978 ms | 1358.096 ms |
| Post-build shared-input check | 337.967 ms | 437.239 ms |
| Candidate preparation | 193.065 ms | 211.784 ms |
| Candidate preflight | 65.878 ms | 71.094 ms |
| Runtime activation | 294.078 ms | 336.259 ms |

Do not add phase quantiles. Implementation verification (824.380 ms p50)
overlaps input preparation and build. Discovery and hashing are nested within
the two input phases; Go/TypeScript projection measurements share an interval.
These named steps do not exhaust elapsed time: watcher/source capture,
orchestration between steps, publication, and HTTP observation also contribute.
The remaining time is not proven scheduler delay or Go initialization cost.
Independent post-response candidate verification costs another 1744.758 /
1908.437 ms p50/p95. It is mandatory and passed every sample but is outside the
explicit edit-to-response latency interval; therefore 4.111 s is not the complete
measurement-runner iteration duration.

### Validation and retained evidence

Passed: `go test ./internal/build`, `go test ./cmd/scenery`, `go test ./...`,
`golangci-lint run ./...` (zero issues), and
`go run ./scripts/verify --summary --write` (41 knowledge and 21 architecture
warnings, zero errors). No public CLI/schema contract changed; the fix restores
the documented current-source identity invariant. No external protocol changed;
the owned ONLV series supplies real build/replacement/HTTP identity proof. Full
release certification and Linux measurements were not selected.

A final standard-verifier rerun after report publication failed because another
task concurrently changed `SKILL.md` and added plan 0191: the shared-CLI install
policy check rejected `go install ./cmd/scenery`, and plan 0191 lacked the
required living-document statement. The corresponding verifier test failed too.
These unrelated files were not edited or reverted by this comparison. The earlier
standard verifier and full Go suite passed; the final shared checkout is not
claimed green. `git diff --check` and exact AHJ source restoration passed.

```sh
bun .scenery/harness/builder-comparison/measure-product.ts product-724ebe66-fixed
GOFLAGS=-buildvcs=false go run ./scripts/verify --benchmark native-reload-attribution --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
```

- Product report: `.scenery/harness/builder-comparison/product-724ebe66-fixed/report.json`,
  SHA-256 `8c1a496134d80d1cb6258220c87c8e35e88ef30a1d33ba0c931c864c759b8746`.
- Island report: `.scenery/harness/native-reload-attribution/attested-1200065712/report.json`,
  SHA-256 `9014705893bc362d079682effee2633f12719961e9399181b4108e9b864e9356`.

Both reports passed identity and owned cleanup. Product source was restored and
verified; fresh `ps` reports the fixture stopped with no sessions. Island children
stopped, its worktree was removed, and original ONLV checkout status was unchanged.
The dedicated product fixture and managed data remain retained, not pruned.

## Earlier rejected attempts

The following sections preserve the pre-fix failures; they are not included in
the accepted statistics above.

### Original scope and setup

On 2026-09-14 the human authorized comparing the current product builder with
the AHJ island experiment. Product source is Scenery
`825f46194bf109887b4856b20c82e261ce565e4c`, framework source digest
`sha256:c2116ebcc008f00b6d34fbe9c29cc8d57c055036863b469051656a4933e22109`,
framework executable digest
`sha256:235f1791bf88d25e3762f35c91c7d77191be972d9bbd10b6e00826a416de7359`.
ONLV is pinned to `4f8126a3e3806b7100ab7efaca1b7dd06b894221`, the same source
revision used by the island. Native Go runs on macOS; only the existing managed
database fixture uses Docker. No production code or security policy was changed.

A dedicated worktree at `/Users/petrbrazdil/Repos/onlv-builder-comparison-20260914`
uses branch `feat/builder-comparison-20260914` and fixture ID
`6b4e6db4-af8d-4e28-8ddf-c1ff47c8818f`. Its selected current framework, coordinated
small snapshot and ordinary NextNext runtime were prepared successfully. The
original ONLV checkout's unrelated frontend changes were not used or modified.

The measurement edits the same `validatedSearchQuery` error-message literal as
the island, then calls `/api/solar/ahjs?query=<512 characters>` with a normal
development bootstrap bearer token. It requires HTTP 400, `invalid_argument`,
the exact new message, a new PID and implementation identity, then independently
checks the current candidate through `inspect build --verify-generation`.
Tokens are not recorded. The planned series is two excluded warmups and 30 edits.

Unlike the island, this exercises the full product runtime, authentication, HTTP
codec, watcher, input checks, candidate preflight and replacement. The operation
still returns before SQL. The product has no standalone AHJ-island target, so
this is not an isolated comparison of two builders consuming identical artifacts.

### What was observed, not accepted

Both attempts stopped in the first excluded warmup:

| Observation | First attempt | Retry |
|---|---:|---:|
| Edit through complete new semantic HTTP response | 4579.137 ms | 4948.965 ms |
| Go build command | 1817.715 ms | 2272.324 ms |
| Runtime bundle preparation | 327.409 ms | 329.566 ms |
| Post-build shared-input check | 388.675 ms | 361.576 ms |
| Candidate preparation | 195.613 ms | 194.044 ms |
| Candidate preflight | 69.876 ms | 65.111 ms |
| Runtime activation | 294.624 ms | 267.788 ms |
| Current candidate verification | Failed | Failed after 15 s retry window |

These are individual failed warmups, not distributions. Nested phases must not
be summed: input discovery/fingerprinting is inside runtime bundle preparation
and repeated inside the post-build check; implementation checking overlaps
compilation. Polling uses a 20 ms interval. Post-response candidate inspection
does not extend the HTTP latency interval, but remains mandatory for acceptance.

The latest valid [island cohort](native-build-first-execution-attribution.md#git-stamping-follow-up-2026-09-14)
has 30 samples, 398.853 / 431.122 ms build p50/p95 and 690.349 / 739.013 ms
edit-to-response p50/p95 with VCS disabled. Those values are a differently scoped
historical reference, not a valid product-versus-island speedup comparison.
Neither an interleaved comparison nor an identical-artifact build A/B completed.

### Original blocking failure

Both product attempts returned:

```text
SCN8003: failed_precondition: current generation is not verified:
current authored source differs from the candidate;
wait for a successful runtime rebuild
```

The second attempt retried the exact candidate check for 15 seconds without
relaxing it. Restoring the original source bytes produced a new response and
made current-candidate verification pass in both attempts. Thus a publication
delay alone does not explain the observed failure.

Static source inspection identifies the likely missing update:
`LoadCachedPreparationContext` loads `Result.SourceFingerprint` from the previous
state in `internal/build/workspace_cache.go`. Its cached preparation path syncs
changed source files and updates source stamps and `SourceMetadataFingerprint`,
but never refreshes `SourceFingerprint`. `savePrimedWorkspace` persists that
retained value. `verifyCurrentSourceStateWithSnapshot` in
`internal/build/runtime_identity.go` compares current source against it and
rejects. The ordinary non-cached preparation computes a fresh fingerprint.
This source path is consistent with both runtime failures; no fix was applied.

The next step requires permission to fix and test product cached-preparation
identity maintenance, then rerun the entire acceptance series. Do not bypass
identity verification, disable reuse to hide the defect, or label the failed
warmups as passing latency evidence.

### Earlier evidence and recovery

The bounded runner is retained at
`.scenery/harness/builder-comparison/measure-product.ts`, SHA-256
`0fe290de2426d4b10ec2635e899cedb84e062b438da9962058ebbf8bbf07757d`.
Run commands, from the Scenery repository root:

```sh
bun .scenery/harness/builder-comparison/measure-product.ts product-825f4619
bun .scenery/harness/builder-comparison/measure-product.ts product-825f4619-retry
```

Raw reports under `.scenery/harness/builder-comparison/`:

- `product-825f4619/report.json`, SHA-256
  `e55baaec1519c8b7535c5fba1cb56c5f9d83bcb35dfaf7fc6beddefe179e83c4`.
- `product-825f4619-retry/report.json`, SHA-256
  `c9e64fbc7bfb2fe17e8aa98211701703fcd79d6682f27d0950ba7f77229c625f`.

Both reports prove byte restoration and verified restored behavior. The owned
runtime was stopped, and a fresh `scenery ps -o json` reports the exact fixture
root as `stopped` with no sessions. Its worktree and managed data remain for
resumption; nothing was pruned. See [Plan 0190](plans/0190-product-and-island-build-comparison.md).
