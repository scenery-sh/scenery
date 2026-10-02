# Exact JSON decoding performance

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries, Decision Log and Outcomes & Retrospective current according to PLANS.md.

## Purpose / Big Picture

Retain exact JSON transport and application-visible values while reducing browser parsing and descriptor-validation work. Compare four bytes-to-typed combinations on the nine existing private ONLV catalog snapshots: current parser/current validator, optimized parser/current validator, current parser/optimized validator, and both optimized. Correctness and generated NextNext integration are acceptance gates; a microbenchmark gain is not a user-visible latency claim.

## Progress

- [x] (2026-10-01 22:40Z) Read generator ownership, preserve clean baseline sources and identify the two hot paths.
- [x] (2026-10-02 01:38Z) Implement parser fast paths, balanced named ancestry and immutable record lookup reuse; deeply freeze generated registry metadata.
- [x] (2026-10-02 01:42Z) Regenerate native, house and assistant clients; pass focused/full Go, lint, TypeScript and Bun checks.
- [x] (2026-10-02 02:22Z) Three final independent document sessions validate all nine fixtures/four lanes; report registry preparation separately.
- [x] (2026-10-02 02:26Z) Generate and browser-test ONLV catalog lists/filter/detail; verify served candidate source and restore the pinned projection afterward.

## Surprises & Discoveries

A pre-existing storage-transfer Bun fixture repeatedly timed out in the full suite, while its isolated case passed. Its three-byte never-ending response could remain buffered before the awaited headers; a private probe using a 64 KiB chunk passed all 21 cases. Update only that fixture chunk to flush headers and retain the never-ending body/cancellation assertion. The final combined suite passes 52 tests (272 assertions); the production development-runtime client is unchanged.

The first diagnostic browser session showed parser gains but no record-cache benefit: the generated typeRegistry is only shallow-frozen (client.ts uses Object.freeze). Deeply freeze generated descriptor metadata through the existing freezeMetadata helper, without changing caller-supplied mutable metadata. Retain this diagnostic session separately and rerun final cohorts.

A frozen object can still expose a changing getter. Cache only descriptors with an own data fields property, a frozen dense plain array, frozen own data entries and own data wire keys. Focused conformance checks verify changing frozen field/array getters still work; final cohorts were rerun with this guard.

The ONLV wire review found numeric/BOM decoder defects and a lossy parity comparator. The companion ONLV experiment must fix these before its measurements are reused. Existing private snapshots contain no credentials and will remain private.

## Decision Log

- Keep exact JSON and generated public contracts unchanged. Optimize the shared generator-owned runtime; retain the old implementation only in ignored benchmark copies. Date/author: 2026-10-01 / Codex.
- Cache only immutable record lookup metadata. Reuse the named-type ancestry Set with balanced insertion/removal; do not cache validation results or assume manually supplied descriptors are deeply frozen. Date/author: 2026-10-01 / Codex.

## Outcomes & Retrospective

The conformant JSON path saves 29.3–33.3% (0.243–0.440 ms) on the 100-row catalog fixtures using medians of three session p50 values. Parser-only savings are larger than validator-only savings; deep freezing enables the lookup cache and adds approximately 0.6 ms of one-time registry preparation (0.5→1.1 ms). All nine fixtures and four variants passed runtime-aware parity, including the corrected OWF decoder. ONLV normal lists, a fresh AHJ filter request and module detail returned HTTP 200 and populated the existing collections. The temporary generated projection was restored to preserve the app pin and retained producer; production generator source and committed fixture clients carry the implementation. No user-visible latency, allocation or RSS gain is claimed. Native profiler methods were unavailable. No global installation, framework pin, runtime/data, merge or deployment changed.

## Context and Orientation

internal/generate/generate_typescript_runtime.go owns parseExactJSON. internal/generate/generate_typescript_runtime_internals.go owns decodeTypedValue. Generated metadata is deeply frozen; callers can also supply mutable JavaScript metadata, whose current behavior must remain intact. ONLV PR #148 contains development/wire-format; its ignored .scratch/wire-format/json-followup holds baseline sources and nine already captured service responses.

## Milestones

First preserve conformance and reduce repeated parser/descriptor work. Then regenerate native, house and assistant clients and validate. Finally measure all four combinations with consistent UTF-8 decoding, output consumption, source identities and exact runtime-aware parity, and exercise actual NextNext catalog reads.

## Plan of Work

Use numeric whitespace scanning and an unescaped-string fast path, retaining escaped-string JSON.parse and Unicode validation. Reuse each immutable record's wire-field lookup. Preserve named recursion errors, unknown fields, constraints, renaming and freezing. Add focused behavioral conformance cases for string/number rejection, metadata mutation, registry isolation and named cycles. The companion ONLV plan owns wire fixes, manifests and benchmark/report implementation.

## Concrete Steps

From Scenery root, run the affected Go and Bun tests, regenerate the three declared clients using the worktree-local binary, and run the full verifier. From ONLV root, use prepare-json.ts with the saved baseline runtime and Scenery source root to build private four-lane copies, serve the experiment at the existing Scenery origin, and run native Chrome controls without changing unrelated runtime/data ownership. Record source hashes and Scenery/ONLV revisions with each cohort.

## Validation and Acceptance

Changed classes are generator and client runtime. Select full validation: go test ./internal/generate; go test ./cmd/scenery -run 'TestGenerate'; go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json; the same command for internal/compiler/testdata/house and testdata/assistant; bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts; tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json; the same tsc for tsconfig.catalog.json; go run ./scripts/verify --summary --write; golangci-lint run ./.... Inspect final agent-context.json and fulfill its command union. The ui probe provisions frozen tools dependencies if absent; no network/server transport boundary changes require a native HTTP probe. No release certification or all-root timing audit is selected. Performance measurements are explicitly requested.

ONLV acceptance uses source-local generation freshness, app lint/typecheck/unit tests/build, exact comparison on all nine snapshots and independent browser sessions at the retained Scenery origin. Report cold preparation separately from warm CPU and preserve batch p95 terminology. Do not infer server CPU, RSS, transfer or request-to-render improvement from these CPU measurements.

## Idempotence and Recovery

Generation is an artifact-set transaction. Retain baseline copies and original cohort data in ignored storage. Never overwrite another task's go.mod edit or upgrade retained state to make generation pass. Build the local product; do not install globally. Restore temporary application source-selection changes exactly after the co-development proof.

## Artifacts and Notes

Validation: go test ./internal/generate passed; go test ./cmd/scenery -run TestGenerate selected no tests, with the complete cmd package covered by the full verifier. Native/house/assistant generation passed. Both tsc fixture/catalog commands passed. bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts passed 52 tests after the deterministic streaming fixture correction. golangci-lint run ./... passed. The full verifier passed with existing freshness/architecture and cached-suite timing warnings; its Go suite, vet, schema and contract checks passed. The final knowledge/index changes also passed the selected verifier before publication.

Companion evidence: ONLV development/wire-format/JSON-RESULTS.md contains four-lane session medians, preparation costs and all source/fixture identities. Private .scratch/wire-format/json-followup retains raw samples, served-source proof and app screenshots. Native HeapProfiler was unsupported and Profiler.enable returned -32601; no alternative browser/profile or instrumentation bypass was used.

Baseline Scenery revision: a1fd9485356a5e7a0d117ce62870a300296bd5ab. Baseline ONLV PR revision: 98d2d2a419fc8e6c9509e9718d8ed22bf7090e2a. Published results will contain hashes and aggregates, not captured bodies.

## Interfaces and Dependencies

No new dependency or public runtime option. Existing TypeDescriptor/TypeRegistry, parseExactJSON and decodeResponseBody contracts remain the boundary. Scenery owns source generation; ONLV owns only the experiment and generated app consumer.
