# Native Build Compiler Decision

Status: default development compiler under corrective validation by Plan 0196;
deployable artifact builds remain on stock `go build`.

## Decision

Plan 0195 promoted the retained-domain compiler as the internal development
default while keeping the stock Go 1.27 compiler/linker and complete generated
application. A later revision-bound review found that the implementation and
benchmark did not yet justify the published performance claim: successful
builds did not advance baseline/archive state, graph changes cold rebuilt the
complete closure, and the comparison omitted the retained-validation plus
stock-build control. Plan 0196 corrects those defects without changing the
deployable build boundary.

The original 35.8% figure is historical, not a current claim. Its accountable
build values used different capture work between stock and candidate and did
not include the complete enclosing transaction. The corrected benchmark binds
each sample to backend, owner, generation and served edit identity and measures
three lanes over identical full-ONLV edits.

The original activation smoke used a fresh macOS basic-app root. Its initial captured
bootstrap took 26,137 ms, including a 20,625 ms recorded stock command. A
compatible handler-body edit used the retained compiler in 592 ms, rebuilt four
packages, completed the full build request in 1,112 ms, started a new process,
and served the changed typed response with new implementation/build-input
identity. Adding an import rejected retained eligibility and completed a fresh
captured bootstrap in 25,940 ms before serving the new response. Corrected graph
refresh runs an ordinary shared-cache stock build and merges observed actions;
it uses a complete bootstrap only when that recording is incomplete.

## Historical Results (Superseded For Comparison)

The original run is
`.scenery/harness/native-build-compiler/20260914T193915Z-16687730043e95c7/report.json`.
It contains 60 stock and 60 compiler samples over two independently bootstrapped
cohorts with backend/root assignment swapped. The raw observations remain
useful, but the percentage comparison is superseded for the reasons above.

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
| Retained input-domain validation plus retained direct compile/link | 2,017.491 ms | 1,296.027 ms | -35.8% | -11.5% | Superseded comparison |

The missing matrix cell, retained input-domain validation followed by ordinary
stock `go build`, is now an explicit `retained_stock` control. It intentionally
measures the repeated driver work so the direct compiler's contribution can be
separated from retained input discovery.

## Corrected Short macOS Observation

The corrected short run is
`.scenery/harness/native-build-compiler/20260915T092035Z-68081b3adf21f643/report.json`.
It used one full-ONLV cohort, one warmup per lane, and three measured edits per
lane. Every sample was bound to its requested backend, owner, generation, edit,
runtime identity, and served response; source status and owned cleanup passed.
The sample is deliberately too small for a GO/NO-GO decision.

| Boundary | Stock p50 / p95 | Retained + stock p50 / p95 | Retained compiler p50 / p95 |
|---|---:|---:|---:|
| Complete input capture/validation | 1,236.829 / 1,365.733 ms | 240.186 / 264.846 ms | 264.478 / 267.712 ms |
| Artifact compile/link/finalize | 1,138.970 / 1,144.949 ms | 1,175.808 / 1,207.910 ms | 1,176.041 / 1,190.716 ms |
| Accountable build transaction | 2,399.482 / 2,535.689 ms | 1,495.849 / 1,517.705 ms | 1,733.503 / 1,736.465 ms |
| First verified response | 5,589.301 / 5,647.248 ms | 4,516.037 / 4,554.373 ms | 4,793.241 / 4,809.750 ms |
| Accepted edit | 7,494.986 / 7,580.143 ms | 6,412.517 / 6,497.535 ms | 6,724.223 / 6,736.490 ms |

Against stock, the retained compiler reduced accountable-build p50 by 666.0 ms
(27.8 percent) and accepted-edit p50 by 770.8 ms (10.3 percent). The missing
control changes the interpretation: retained validation followed by ordinary
stock `go build` was another 237.7 ms faster at accountable-build p50 and 311.7
ms faster at accepted-edit p50. Direct compile/link therefore showed no speed
advantage in this short run; the observed gain remains attributable to retained
input discovery. The compiler stays the explicitly selected development default
from Plan 0195, but this short observation does not support a performance
promotion claim and should guide the next policy decision.

Compiler-only p50 was 150.479 ms and link p50 was 1,001.768 ms. The direct
compile/link transaction was not faster than stock `go build`; the measured
payoff comes from replacing repeated `go version`, `go env`, `go list`, full
input hashing, and full workspace snapshotting with a retained package recipe,
complete package-directory membership checks, mandatory workspace hashing, and
ctime-backed external-input digest reuse.

## Correctness Boundary

The retained compiler directly supports Go body edits only when package and file
membership, imports, build/embed directives, module graph, selected files,
native inputs, target, flags, environment, and tool identities remain exactly
compatible with the last successfully committed current state. It rebuilds the
newly changed package and all transitive consumers, creates current build IDs
and runtime linker identity, and atomically advances source snapshots and
archive mappings before publishing only the newest owner sequence. Captured
regular-file arguments, including import, embed and symbol-ABI configuration,
are rebound to retained copies. Rebuild frontiers requiring unmodeled native
actions are rejected before compiler execution.

Every known workspace input is read and hashed on each transaction. An
unchanged external input may reuse its bootstrap digest only when the platform
supplies a nonzero change time and size, mode, device, inode, modification time,
and change time all match. Any stamp change causes a content rehash; a platform
without change time hashes the content. Added, missing, symlinked, malformed,
module, import, directive, native, or external-content changes leave the direct
path and enter a recorded warm-cache stock graph refresh. Configuration, tool
and corrupted retained-state changes bootstrap. A refresh that cannot observe
every required compile action also bootstraps rather than inventing a command.

## Evidence Limits And Next Step

The historical benchmark report keeps `product_acceptance: not_measured`
because its repository-only wrapper and owner processes were not a product
lifecycle contract. Plan 0195 separately supplied product activation evidence:
the existing development supervisor owns workspace-keyed recipes, cancellation,
bounded generations, atomic publication, fail-closed rebootstrap, candidate
preflight, activation, and rollback. There is no new global daemon or public
configuration surface. Deployable artifacts remain outside this decision.

Plan 0196 added the bounded `--benchmark-short` observation above. It includes
all three execution cells but is not a replacement for the full two-cohort
GO/NO-GO series. Linux comparison is still outside the current macOS-only
authorization.
