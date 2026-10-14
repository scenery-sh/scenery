# Fresh Source Snapshots And Shared Lifecycle Planning

## Purpose / Big Picture

Implement the supplied Scenery point 1 guide against starting revision
`67cff8fc292a79ef93025f593ed9cbf1092a9935`: source-derived answers identify one
captured, validated revision; invalid source never receives stale success;
inspection/check-only stay read-only; explicit generation publishes a recoverable,
owned artifact set; lifecycle adapters share a dependency-aware plan and explicit
capabilities. OAuth, UI redesign, storage drivers and MCP were outside scope.

Implementation ends at `4b3beeba64193871ef91b23bbe5cea592c28a114` and is published
in [PR 244](https://github.com/scenery-sh/scenery/pull/244). The later completion
record commit changes documentation only. Merge, shared CLI installation,
deployment and retained-data deletion are separate, unperformed actions.

## Progress

- [x] 2026-10-14: Read the guide and applicable ownership/architecture contracts;
  confirmed a clean starting worktree and recorded the baseline.
- [x] 2026-10-14: Add baseline regressions; publish the independently reviewable
  snapshot/response fix as `2ee2f8a2991f1be315b7d327bfc886186859337c`.
- [x] 2026-10-14: Validate compiler membership/bytes and consumed SQL/renderer
  digests; preserve the rendering result in generation response metadata.
- [x] 2026-10-14: Validate actual cache consumers and complete output digests.
- [x] 2026-10-14: Add the typed serial planner and command/build/runtime adapters.
- [x] 2026-10-14: Stage SQLC inputs/output and publish one owned transaction.
- [x] 2026-10-14: Remove orphaned DB sync composition and reject retired syntax.
- [x] 2026-10-14: Synchronize living contracts, schema identity and knowledge index.
- [x] 2026-10-14: Complete package/race/lint/TypeScript, full verifier and named
  integration proof; verify owned cleanup; create and attach PR 244.

### Resume here

2026-10-14: Implementation and delivery are complete; PR 244 is open against
`main`, without merge. No implementation task remains. Validation archives below
are historical exact-input evidence, not permission to adopt/delete runtime data.
For later revisions, reclassify changed paths and validate their actual inputs.

## Surprises & Discoveries

Source inspection already compiled current source and ignored historical
`models.json`/`views.json`; source-edit/invalid-input regressions retain that
behavior. Existing process-local projection caching, exact Go input resolution,
transaction recovery and descriptor ownership were retained.

Reproduced gaps were acceptance of source changes during rendering, acceptance
of a stale snapshot by check-only generation, a second compiler read to label
published output, missing source-inspection revision metadata, authored SQLC
schema paths classified as outputs, direct SQLC writes into the authored tree,
and cached graph hits before complete output validation. Consumed renderer/SQL
bytes are also checked against their graph digests rather than a later workspace
hash. Generator inspection additionally revalidates its captured YAML bytes.

Two new unit roots exceeded 100 ms isolated p95 because they repeated filesystem
publication. Their OS publication scenarios remain in the named generation probe;
unit coverage now checks drift/refusal without repeating those effects. Thirteen
new roots passed 20 isolated serial samples, maximum p95 70 ms. A new config
return-boundary regression is also service-free and covered by package/race runs.

An early combined runtime run changed inputs while executing and was rejected;
it is not acceptance evidence. A later stable run passed every selected runtime
segment. The CLI report rendezvous occasionally ended before observation; its
isolated repetition passed with actual child/fixture cleanup. No telemetry
implementation change was made for that independent probe timing condition.

## Decision Log

- 2026-10-14: Keep semantics in compiler, rendering in generation, Go resolution
  in build, staged checking in tscheck and publication in workspacetx. The new
  standard-library-only planner validates and schedules those existing owners.
- 2026-10-14: Keep semantic contract, exact workspace, implementation and
  deployment identities distinct. Recorded `inspect build` remains historical
  unless explicitly verified; ordinary compilation never starts infrastructure.
- 2026-10-14: Reuse existing SHA-256 projection caches and build fingerprints;
  add no persisted IR, independent filesystem walker, locks, dependencies or
  environment knobs. Node keys use domain-separated canonical JSON with schema,
  producer/configuration/input references and dependency digests; rendering
  owners retain actual reusable-byte caches. Validation/effects run each time.
- 2026-10-14: SQLC's private `.scenery/artifacts/sqlc.json` is necessary output
  ownership evidence, not a cache. Its structural schema, workspace owner,
  captured input revision, tool digests and complete exact output digests bind
  the set. Invalid ownership refuses replacement; it never grants adoption.
- 2026-10-14: Preserve unrelated files inside managed roots. Authored schemas
  remain inputs; Atlas-produced schema has a producer and explicit scratch
  authority. First publication does not overwrite an existing authored schema.

## Outcomes & Retrospective

Normal source commands require no `.scenery/gen` deletion. Compiler success and
response/publication boundaries verify the captured revision; invalid current
source retains its diagnostics. Generation response identity and coverage come
from the same result used to render. SQLC's response describes its actual
captured input digest through planner references.

Source compile/list/get/inspect use the read-only compiler closure. Root,
snapshot, build-Go and cached-TypeScript adapters use the same selected generator
nodes. SQLC uses captured inputs, schema/tool producers, validation and one commit.
DB apply/setup are non-cacheable live effects with existing state/ownership checks.
Development candidate preparation, source verification and activation form one
selected closure; failed/superseded candidates do not label last-good serving
state with their identity. Generator inspection serializes actual planner nodes
without executing rendering, checking, publication or infrastructure.

Malformed disposable build manifests are misses. Hits require current keys,
shaped metadata and complete output membership/digests; ownership conflicts and
unsafe transaction state remain refusals. Process-local renderer outputs are
cloned and failures are not cached. Identical output bytes are not rewritten.
Only exact descriptor-owned paths are excluded from authored membership; outputs
and their newly created directories cannot invalidate tool input keys themselves.

Removed code includes orphaned `dbSyncCommandWithHooks`, its alternate composition,
unused apply/inspection wrappers, duplicate generation orchestration and the now
unused first-error helper. No syntax aliases/legacy decoders were introduced.
Human-owned `VNEXT.md`, completed historical plans, deployment/storage/OAuth/MCP
contracts and unrelated runtime data remain unchanged.

Remaining qualifications: existing knowledge freshness warnings and the default
whole-suite timing advisory are not new functional failures. Release certification,
benchmarks and an all-root timing campaign were not selected. Runtime/SQL/cache
acceptance is bounded to the actual named probe scenarios and host, not arbitrary
production capacity or indefinite process lifetime.

## Plan of Work

The work followed five reviewable stages: baseline regressions and freshness fix;
complete identities/cache checks; typed planner and producer adapters; CLI/watch
and SQLC publication routing; legacy cleanup, living contracts and validation.
The implementation preserves the existing package boundaries and canonical CLI.

| Input family | Authoritative capture/identity |
| --- | --- |
| Contract | Compiler membership, `.scn`, packages/locks/provider descriptors, config, resolver alternatives/absence, consumed SQL/renderer bytes and workspace revision |
| Go | Existing selected Go input manifest, modules/workfiles/local replacements, embeds/native inputs, toolchain/flags/target/framework and generated dependency digests |
| SQLC/Atlas | Exact config, schema/query files/directories/globs, HCL and local plugin inputs; selected scratch configuration; executable digests; complete staged output |
| TypeScript | Declared targets and graph, renderer/catalog projection inputs; mandatory current staged checker against app slots/aliases, tsconfig/dependencies and managed tool readiness |

No normalized Go AST or timestamp-only key was introduced. The compiler's
existing revision walker serves declared tool input capture; only proven exact
producer outputs are excluded. File presence/membership and restored-mtime edits
remain part of byte identities.

## Validation and Acceptance

Commands ran from the repository root with the normal Go cache and worktree-local
product binary. Required cumulative proof is complete:

- `go test ./internal/compiler ./internal/generate ./internal/build ./internal/workspacetx ./cmd/scenery ./internal/lifecycle ./internal/machine ./internal/app ./scripts/verify` passed.
- `go test -race ./internal/lifecycle ./internal/compiler ./internal/generate ./internal/build ./internal/workspacetx ./cmd/scenery` passed.
- `golangci-lint run ./...` passed, 0 issues.
- Exact `go run ./cmd/scenery generate --target typescript_client.public_api --app-root <fixture> -o json` for `internal/compiler/testdata/native`, `internal/compiler/testdata/house` and `testdata/assistant` passed with `changed: []`.
- `bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts`: 56 passed, 290 assertions.
- `tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json` and `-p internal/generate/testdata/tsconfig.catalog.json` passed.
- `go run ./scripts/verify --summary --write --base 67cff8fc292a79ef93025f593ed9cbf1092a9935` passed, including `go test ./...`, architecture, drift, schemas and isolation. Archive: `.scenery/harness/runs/20261014T123933.331798000Z`.
- Combined `--probe generation --probe ui --probe cli-grammar --probe cli-process` completed generation/UI/grammar successfully in `.scenery/harness/runs/20261014T123934.167826000Z`; its CLI observation timing failed and is not a pass. Isolated `--probe cli-process` passed in `.scenery/harness/runs/20261014T124137.863563000Z` with verified cleanup.
- Stable `--probe native-contract --probe dev-process --probe worktree --probe cli-grammar --probe cli-process` passed in `.scenery/harness/runs/20261014T121918.377410000Z`, with matched initial/final input revision and actual served identity/cleanup. Later edits affected only SQLC/CLI return metadata and unit test OS-proof placement; their selected probes/package/race proof are the final-head runs above.

The written full run's `agent-context-summary.json` was read before
`agent-context.json`; its changed-area union was fulfilled, reusing same-scope
successful checks. The later completion/index edit has its own final verifier
receipt in task-local evidence; it does not alter implementation proof above.

| Required failure/behavior family | Proving evidence |
| --- | --- |
| Current source/invalid diagnostics, historical JSON ignored | `TestInspectUsesCurrentSourceAfterSuccessfulProjection`, compiler snapshot/input tests, named runtime captured-input matrix |
| Membership/absence/equal-length restored mtime | compiler snapshot/input-capture tests, build input digest/Go selection/local replacement tests, dev-process native input matrix |
| Drift/check-only no repair; explicit repair | `TestCheckOnlyReportsDriftWithoutRepair`, owned Go projection tests, generation real SQLC deletion/repair assertions and worktree stale-client regeneration |
| Cached missing/tampered/corrupt/wrong input | projection-cache tests, cache-validity tests, preparation/output stamp tests and named shared-build cache mutation/rejection cases |
| One revision across rendering/response | publication snapshot tests, consumed renderer digest regression, generator YAML return-boundary conflict regression, response compiler result ownership |
| Staging/tool/commit failure and concurrency | injected SQLC failure/edit tests, workspacetx recovery/backup tests, generation killed partial 192-file set and two actual CLI publishers |
| Ownership/retired targets/authored files | Go projection foreign/edited/missing receipt and retirement tests, SQLC actual authored `_gen.go` preservation/tampered refusal/owned repair |
| DAG selection/cycles/duplicates/missing producers/capabilities/failure/cancel | lifecycle plan tests, canonical node inspection schema validation and selected production adapters |
| Tool/framework/target/flags; current TS readiness | node-key tests, exact build-input tests, native/worktree/dev-process proofs, staged TypeScript/UI checks |
| Pure inspection / live DB effects / last-good runtime identity | read-only source closure and inspect regression; DB apply/setup existing tests; worktree DB/ownership and dev-process failing-candidate proofs |
| Retired marker/syntax | `TestRetiredConfigMarkerNeverDiscoversOrOverridesAnApp`, `TestRetiredLifecycleSyntaxIsRejectedBeforeWork`, CLI grammar refusal inventory |

## Idempotence and Recovery

All filesystem publication uses workspacetx's existing serialized journal and
normal-reader recover-or-reject gate, with current-owner validation during a
transaction. Context cancellation stops lock admission/staging before publication;
once an external action has committed successfully, later cancellation cannot
relabel it as failed. Reusable renderer bytes are immutable and cloned.

Retry source conflicts against a new capture. Missing/stale disposable cache is
recomputed; invalid source never uses last-good graph state. Output repair and
retirement require exact descriptor/digest evidence. SQLC scratch tools, app DB
mutation and runtime control are distinct capabilities. No retained data is
reinterpreted as disposable cache, adopted, reset or deleted by this work.
