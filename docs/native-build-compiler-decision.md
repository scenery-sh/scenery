# Native Build Compiler Decision

Status: promoted as the default development compiler by Plan 0195; deployable
artifact builds remain on stock `go build`.

## Decision

The retained-domain compiler materially reduces the complete full-ONLV build
transaction while preserving the stock Go 1.27 compiler and linker, complete
generated `./scenery_internal_main`, generated contracts, build flags, runtime
identity, and authenticated response behavior. Both independent 30-pair
cohorts passed the frozen median and p95 gates. Fifty additional compiler edits
completed with four retained generations and clean owned-worktree/process
cleanup.

The original result justified designing the production ownership and
rebootstrap boundary. Petr subsequently approved that internal development
default explicitly, and Plan 0195 implemented it under the existing supervisor.
Aggregate accepted-edit p50 in the original ONLV cohort remains 6.234 seconds
and the stock linker remains about one second, so the broader 200/250 ms native
checkpoints are still missed.

The activation smoke used a fresh macOS basic-app root. Its initial captured
bootstrap took 26,137 ms, including a 20,625 ms recorded stock command. A
compatible handler-body edit used the retained compiler in 592 ms, rebuilt four
packages, completed the full build request in 1,112 ms, started a new process,
and served the changed typed response with new implementation/build-input
identity. Adding an import rejected retained eligibility and completed a fresh
captured bootstrap in 25,940 ms before serving the new response. These are
activation diagnostics, not a replacement performance cohort.

## Comparable Results

The final valid run is
`.scenery/harness/native-build-compiler/20260914T193915Z-16687730043e95c7/report.json`.
It contains 60 stock and 60 compiler samples over two independently bootstrapped
cohorts with backend/root assignment swapped.

| Boundary | Stock p50 / p95 | Compiler p50 / p95 | p50 change |
|---|---:|---:|---:|
| Complete input validation | 835.087 / 1,045.216 ms | 106.939 / 124.203 ms | -87.2% |
| Artifact compile/link/finalize | 1,174.870 / 1,240.721 ms | 1,187.624 / 1,265.319 ms | +1.1% |
| Accountable build | 2,017.491 / 2,224.503 ms | 1,296.027 / 1,374.610 ms | -35.8% |
| First verified response | 5,146.984 / 5,467.221 ms | 4,312.820 / 4,505.758 ms | -16.2% |
| Accepted edit | 7,047.562 / 7,399.420 ms | 6,234.328 / 6,441.622 ms | -11.5% |

## All Measured Build Paths

The experiments tried three full-application paths. Compare the relative delta
inside each run; absolute times moved between runs, so the rows are not a single
interleaved cohort.

| Path | Same-run stock build p50 | Candidate build p50 | Build change | Accepted-edit change | Result |
|---|---:|---:|---:|---:|---|
| Stock `go build` after complete capture | 2,352.273 ms | 2,352.273 ms | baseline | baseline | Production baseline |
| Complete capture plus retained direct compile/link driver | 2,352.273 ms | 2,268.237 ms | -3.6% | -1.3% | NO-GO |
| Retained input-domain validation plus retained direct compile/link | 2,017.491 ms | 1,296.027 ms | -35.8% | -11.5% | GO for production-boundary design |

The missing matrix cell, retained input-domain validation followed by ordinary
stock `go build`, was not executed: invoking `go build` would repeat the package
loading that the retained-domain experiment is designed to remove. Both
experimental candidates still use the stock Go compiler and linker; the
difference is whether package discovery and input validation are repeated.

Compiler-only p50 was 150.479 ms and link p50 was 1,001.768 ms. The direct
compile/link transaction was not faster than stock `go build`; the measured
payoff comes from replacing repeated `go version`, `go env`, `go list`, full
input hashing, and full workspace snapshotting with a retained package recipe,
complete package-directory membership checks, mandatory workspace hashing, and
ctime-backed external-input digest reuse.

## Correctness Boundary

The retained compiler supports Go body edits only when package and file
membership, imports, build/embed directives, module graph, selected files,
native inputs, target, flags, environment, and tool identities remain exactly
compatible with bootstrap. It rebuilds the changed package and all transitive
consumers, creates current build IDs and runtime linker identity, and atomically
publishes only the newest owner sequence.

Every known workspace input is read and hashed on each transaction. An
unchanged external input may reuse its bootstrap digest only when the platform
supplies a nonzero change time and size, mode, device, inode, modification time,
and change time all match. Any stamp change causes a content rehash; a platform
without change time hashes the content. Added, missing, symlinked, malformed,
module, import, directive, environment, tool, native, or external-content
changes fail closed as `needs_rebootstrap` or `unsupported`. The experiment has
no stock-build fallback.

## Evidence Limits And Next Step

The historical benchmark report keeps `product_acceptance: not_measured`
because its repository-only wrapper and owner processes were not a product
lifecycle contract. Plan 0195 separately supplied product activation evidence:
the existing development supervisor owns workspace-keyed recipes, cancellation,
bounded generations, atomic publication, fail-closed rebootstrap, candidate
preflight, activation, and rollback. There is no new global daemon or public
configuration surface. Deployable artifacts remain outside this decision.

The performance cohort predates the subsequent support-artifact validation
hardening. The report is retained as performance evidence for the measured
path; the current code passed focused package tests, the full Go suite, lint,
and the quick verifier without repeating the hour-long cohort.
