# 0185: retrospective of the rejected direct-retention candidate

This is an evidence-only review of the archived candidate and existing 2026-09-11 timelines. No benchmark, runtime, candidate repair or validation rerun was performed for this review. The candidate remains rejected. Results do not establish that every implementation of direct retention is slower.

## Answer

The measured edit regression is concentrated **after the observed build/check join**, not in the Go build command or an exposed verifier tail. That residual grows in all six pairs (89.983–131.541 ms; paired median +112.169 ms). The downstream retain phase saves 57.777 ms paired, but candidate preparation-reference release adds 20.451 ms and handoff adds 26.033 ms paired.

The archived implementation places output hashing/sync/publication and durable cache-reference work inside that post-join residual. It also contains demonstrable duplicate metadata persistence: Cache persists and then collect persists again without changing state while old and new artifacts remain referenced; releasing the preparation reference does the same while the cache/runtime still retain the artifact. These are concrete extra operations in this prototype, not an inherent property of direct output ownership.

However, there are **no individual Publish, Cache, collect, hash, fsync, prune, post-build freshness, or final-admission spans**. The +112.169 ms residual cannot be assigned entirely to the new owner, or divided among those operations. The data identify a location and the code identifies unnecessary work; they do not measure how much of the regression that unnecessary work caused. No claim that deleting it would pass the 250 ms gate follows.

For unchanged startup, there is no build/check pair or new Publish call. The largest measured change is actual preflight: 621.612 → 49.190 ms (paired saving 572.422 ms). The direct arm reuses the retained artifact; control shutdown requests removal of its session copy and next preparation recreates it if absent. During edit both arms preflight newly produced executable bytes, with much closer durations (732.404 → 723.685 ms). This explains the differing *code paths and location of the benefit*. The preflight span does not isolate process creation, loading, runtime initialization, filesystem or OS security work. We cannot name an OS mechanism, nor prove every requested deletion succeeded from the ignored Remove return alone.

## Non-overlapping accounting

All delta columns below are **direct minus ordinary**; positive means slower. Each row reconciles to that pair's whole response interval. Parent spans and nested children are never added twice.

For edits, split audit.compile_admission into: (1) before the first observed bundle/check span; (2) the wall-clock envelope from the earlier runtime.bundle/implementation.check start through the later Go-build/check finish; (3) the remaining interval until compile/admission returns. The envelope includes input/bundle work on the build branch as well as its Go command, with the concurrent checker counted once. It is a reconstruction from existing child spans, not an independently instrumented exact dispatcher/join span. Remaining tiny boundaries belong to the residuals. The other column includes capture, framework/graph preparation, analysis, generated acceptance, HTTP response and unspanned gaps.

Go-build-only and verifier-only durations are diagnostic fields in analysis.json/CSV, excluded from additive columns. In all 12 edit samples the checker finishes before the build; it does not add a post-build wait. Go-build-only paired delta median is -1.755 ms; observed joined-envelope delta median is -10.590 ms. The verifier body itself grows 16.360 ms paired, but adding that to elapsed time would double-count overlapped work.

| Pair | Before join | Joined envelope | Post-join residual | Retain | Preflight | Handoff | Prep release | Other | Total ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | -11.124 | -47.648 | +113.083 | -75.224 | -27.684 | +28.520 | +35.907 | -0.572 | +15.258 |
| 2 | -17.176 | +55.163 | +110.945 | -54.188 | -18.206 | +23.704 | +19.985 | -29.955 | +90.273 |
| 3 | -7.810 | +3.326 | +131.541 | -52.601 | -21.273 | +27.005 | +20.918 | +19.559 | +120.666 |
| 4 | -19.051 | -39.011 | +131.163 | -69.169 | +14.525 | +32.737 | +21.026 | +21.359 | +93.579 |
| 5 | -2.882 | +10.700 | +89.983 | -61.366 | -1.742 | +15.592 | +18.975 | +35.796 | +105.055 |
| 6 | +1.399 | -24.507 | +111.255 | -49.236 | -20.301 | +25.062 | +17.004 | -27.515 | +33.161 |

Per-column **means** reconcile to the mean total delta, +76.332 ms; per-column **medians must not be summed** to explain the declared +91.926 ms median paired total. Difference of whole-run medians remains +94.261 ms. The gate is unchanged.

## Startup pairs

These use the stored preparation-request-start to verified response interval so that the same decomposition is possible. Its paired saving is 550.480 ms. The earlier 544.775 ms figure uses the broader owner-spawn-through-response-receipt/reporting clock; its additional setup/reporting has no individual spans. Both original metrics remain in timelines.json. Neither is complete scenery up.

All workspace.cache spans report verified_workspace_and_runtime_identity hits. No Go build, bundle generation or implementation-check spans occur in startup. Cache compile/admission is retained as one unsplit parent.

| Pair | Cached compile/admission | Retain | Preflight | Handoff | Prep release | Other | Total ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | -4.684 | -42.682 | -575.380 | -20.381 | +19.314 | +41.316 | -582.496 |
| 2 | +10.089 | -45.550 | -564.935 | +30.158 | +19.722 | +56.988 | -493.529 |
| 3 | +0.489 | -51.189 | -601.538 | -39.835 | +21.723 | +74.878 | -595.472 |
| 4 | +11.742 | -50.575 | -569.464 | +20.965 | +20.681 | +48.186 | -518.464 |
| 5 | +2.434 | -45.045 | -583.284 | -30.725 | +19.808 | +29.149 | -607.662 |
| 6 | +8.142 | -49.468 | -567.076 | +60.268 | +20.588 | +71.060 | -456.486 |

The preflight improvement is present in every startup pair (-564.935 to -601.538 ms). Startup handoff is variable (-39.835 to +60.268 ms), not a consistent source of the half-second benefit.

## Static operation ledger, not syscall measurements

For a successful fresh edit with the previously active artifact still referenced:

| New owner operation | owner.json commits | Placement |
| --- | ---: | --- |
| Publish intent, then artifact/preparation reference | 2 | post-join residual |
| Cache replacement, then unchanged collect | 2 | post-join residual |
| Retain candidate reference and reverify artifact | 1 | retain |
| Release stopped previous generation, reclaim its artifact | 2 | handoff |
| Release preparation reference, then unchanged collect | 2 | prep release |
| Total inferred successful calls | 9 | all before response |

Each persist calls atomicfile.WriteRoot with SyncFile and SyncDir. Thus the successful path requests 18 metadata synchronization operations, plus sync of original output, private directory, published owner directory and reclaimed owner directory (22 requested calls total). This is a source-level count conditional on the successful fresh path, not a syscall trace or duration attribution. It excludes shared existing build-state/bundle/manifest I/O, standard Go's internal I/O, store creation outside edit timing and final shutdown outside the edit series.

Two commits per edit repeat byte-identical state: collect after Cache while all artifacts remain referenced, and collect after preparation Release while the new artifact remains cache/runtime-owned. They request four redundant metadata sync operations after the same state was already durably committed. This is unnecessary work in the tested implementation. The release parent span (~20 ms paired) measures the complete two-commit transition; it does not establish the cost of either commit separately.

Direct fresh output is hashed in Publish, verified after publication, then verified again by Retain: three full reads, versus the ordinary source hash and retained-copy verification (two full hash reads). Copying is removed, but hashing is not reduced; some hashing moves into compile/admission. The third verification is a conservative acquire check, not classified here as a proven removable safety requirement.

Startup uses Load (verify + one reference commit), Retain (verify + one commit), and preparation Release/collect (two commits, one duplicate). It skips fresh publication and prior-generation reclamation within the startup request. Control's previous owner cleanup occurs before the startup timer in both arms. The direct cache deliberately retains its artifact through that cleanup, while the control requests session-file removal. Raw paths show a stable direct retained path through startup; there is no inode or OS-internal trace.

## What is established, and what is not

1. **Measured:** the edit overhead appears principally in post-join residual, with smaller handoff/release overhead; it is not an exposed verifier tail or a measured slower Go build. Startup gains occur principally in preflight.
2. **Established from code:** the prototype adds durable metadata transitions and two byte-identical duplicate commits per fresh edit. These duplicate writes are implementation-specific extra work.
3. **Unresolved:** exact time spent in publication, hashes, sync, pruning, freshness/admission, and the mechanism behind preflight differences. No per-operation causal attribution or alternative-implementation prediction is possible from these traces.

Accordingly, the three proposed explanations are not mutually exclusive here: concrete unnecessary prototype work is present, while the available timelines still cannot quantify its causal share or label the remaining overhead necessary. The eight unchecked Close lint findings are not an explanation for 92 ms. Rejection of this candidate remains independent of this retrospective.

## Evidence and reproduction

- [Original completed 0185 report](0185-original.md), unchanged snapshot.
- [Exact rejected candidate diff](candidate.patch), against 741c0d7a; product changes are not restored.
- [Existing per-pair timelines](timelines.json): every recorded Step (name/start/duration/reason), start/capture/response timestamp and elapsed metric retained unchanged. Build input inventories, HTTP bodies/headers and local absolute paths are omitted. The original private report SHA-256 is recorded for provenance; this is explicitly a selected-field export, not that original file.
- [Derived decomposition](analysis.json), [edit CSV](samples-paired.csv), [startup CSV](startup-paired.csv).
- [Read-only calculation script](analyze.py): run `python3 analyze.py` in this evidence directory. It reads saved JSON, validates phase containment/counts, and writes derived tables. It does not build or execute any application.
- [Reviewed source archive](review-source.zip): candidate source, unchanged baseline context, measured/proof driver source and original 0185 report. No executables, environment files or runtime credentials are included.
- [Content hashes](SHA256SUMS).

The exact measured producer hash is 0de26c71244c7f65ff1c31304c16a15b5928b65b63f50249f2a1aaf3ebc1fd85. The archived candidate patch includes two post-measurement tests, as disclosed in 0185; runtime source did not change. This is a public evidence-only branch, not a production PR, and #194 is untouched.
