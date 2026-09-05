# TypeScript Client Footprint

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective updated as work proceeds, following
`PLANS.md`.

## Purpose / Big Picture

Reduce generated client source and browser bundles while preserving the public
client API, exact outcome narrowing, metadata immutability, and validation.
This follows the completed table-driven emit plan 0156. The changes are a pure
annotation on metadata initialization, private aliases for repeated failure
unions, and omission of the validation evaluator when no reachable record uses it.

## Progress

- [x] (2026-09-05) Confirmed current generator contracts and measured the existing
  ONLV client: 360,032 minified bytes through the barrel versus 293,806 bytes
  through direct imports; 111,104 source bytes repeat failure members.
- [x] (2026-09-05) Implemented metadata annotation, exact shared failure sets,
  and reachable-record validation capability selection.
- [x] (2026-09-05) Added focused Go and Bun coverage and regenerated native,
  house, and assistant clients. The generated house rule exposed and now covers
  field-path reference handling and TypeScript property-name projection.
- [x] (2026-09-05) Measured the generated SDK and ran the required validation
  matrix. Implementation acceptance passes; release-wide acceptance remains
  blocked by the pre-existing PostgreSQL probe fixture and local linter version
  recorded below. No application deployment or shared CLI installation occurred.

## Surprises & Discoveries

The metadata initializer is the reason a named client import retains unused
metadata. An in-memory Bun build with only the pure annotation matched direct
imports exactly. The native runtime includes approximately 20 KB of validation
code even though its reachable records have no validation rules.

Adding a real `scene_id` validation rule to the house fixture exposed two
existing gaps that synthetic descriptors did not cover: generic reference
checking treated `record.process_scene_input.scene_id` as a resource address,
and the TypeScript validation program retained `scene_id` instead of the client
property `sceneId`. The focused compiler test and generated multipart call now
cover both. Map-key spellings and the shared Go validation program are unchanged.

The release harness failed only its PostgreSQL probe: its existing
`writePostgresHarnessConfig` writes `.scenery.json` but no `app.scn`. The separate
release gate stopped at `golangci-lint run ./...`: the installed linter was built
with Go 1.26, below this repository's Go 1.27.0 target. These are outside this
client-generation change and were not silently repaired or bypassed.

## Decision Log

- Decision: Keep metadata exported and deeply frozen; annotate only the call
  that receives a fresh generated literal. The call has no observable effect
  when the metadata value is unused. Date/Author: 2026-09-05 / Codex.
- Decision: Share complete identical failure unions with private aliases, only
  when repeated. Keep each discriminant as a separate union member so `Extract`
  and exhaustive narrowing retain their meaning. Date/Author: 2026-09-05 / Codex.
- Decision: Select validation from the same reachable record set used for the
  type registry, including nested cross-module references. Preserve ordinary
  scalar and field-constraint validation. Date/Author: 2026-09-05 / Codex.
- Decision: Correct the two narrowly scoped validation integration defects
  revealed by the compiled fixture; project root field references only in the
  TypeScript expression tree, leaving authored map keys and Go expressions
  unchanged. Date/Author: 2026-09-05 / Codex.

## Outcomes & Retrospective

Implemented on 2026-09-05. The generated public client API is unchanged.
All 234 ONLV operation failure unions are structurally equal before and after,
checked with TypeScript `Extract` and exact type-equality assertions. The emitted
ONLV `client.ts` is byte-identical; metadata remains deeply frozen when imported.

Measured against the existing ONLV nextnext SDK, using a temporary regenerated
SDK and Bun 1.3.14 named-import bundles (`PublicApiClient` and `url`):

| Artifact | Before (bytes) | After (bytes) |
| --- | ---: | ---: |
| `client.ts` source | 330,188 | 330,188 |
| `types.ts` source | 192,951 | 89,567 |
| `runtime.ts` source | 87,757 | 67,576 |
| `metadata.ts` source | 75,658 | 75,674 |
| Barrel-import minified bundle | 360,032 | 284,948 |
| Barrel-import gzip bundle | 45,539 | 35,290 |

The minified SDK bundle shrank 20.9%, gzip shrank 22.5%, and type source shrank
53.6%. Native and assistant runtime source each shrank from 79,911 to 59,730
bytes. These are SDK source/bundler measurements, not a production application
build or request-latency claim. ONLV source and its generated files were not
modified. Descriptor pooling and a generic call-type rewrite remain out of scope.

## Context and Orientation

`internal/generate/generate_typescript_types.go` emits metadata.
`generate_typescript.go` emits public types and outcome unions, with private
failure-set rendering in `generate_typescript_outcomes.go`.
`generate_typescript_runtime_capabilities.go` selects sections in the runtime
templates. `generate_typescript_runtime_validation.go` owns the expression
evaluator. The native and assistant fixtures exercise omission; the house
fixture exercises retained validation, including multipart input.

## Milestones

1. Implement the three emit changes.
2. Verify exact type semantics, metadata freezing and bundling, and validation.
3. Regenerate, measure, run the validation matrix, and close this plan.

## Plan of Work

Reuse the current descriptor and reachability helpers. Do not change the public
class/method/outcome exports. Add focused in-process generator coverage and
Bun conformance. Use temporary SDK outputs for measurements against a local
consumer; do not rewrite consumer-owned source during measurement.

## Concrete Steps

Run from the repository root:

```sh
go test ./internal/generate
go test ./cmd/scenery -run 'TestGenerate'
go test ./internal/compiler ./internal/parse
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go test ./...
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery harness self --quick --summary --write
.scenery/harness/bin/scenery harness self --release --summary --write
scripts/release-gate.sh
git diff --check
```

Run the refreshed `changed_area.recommended_commands` union. Expected classes
include compiler/generator and release-sensitive-or-runtime. Release mode
supersedes the default self-harness. If a gate command cannot complete, record
the exact failing command and cause. Keep validation binaries local to the
checkout; the shared installed CLI must not be replaced by validation.

## Validation and Acceptance

Unused metadata must disappear from a named-import bundle while imported
metadata remains recursively frozen. Failure aliases must retain exact names,
payloads, enqueue variants, and operation-specific subsets. Reachable validation
must reject invalid values before fetch; unreachable rules must not retain the
evaluator. All three client regeneration commands must be idempotent.

Report source/minified/gzip bytes separately. Source reductions are not claims
about production request latency. Individual Go roots remain below 100ms p95;
bundler and toolchain proof belongs outside their timing.

### Recorded results (2026-09-05)

- `go test ./internal/generate ./internal/compiler ./internal/parse ./internal/edge ./cmd/scenery`:
  passed. The edge package is part of the cumulative changed-area recommendation
  because unrelated edge work was already dirty in this checkout.
- `go test ./cmd/scenery -run 'TestGenerate'`: exited successfully but matched no
  tests; the full CLI package above provides actual coverage.
- All three `go run ./cmd/scenery generate ...` commands in Concrete Steps:
  passed, with a second regeneration required to report `changed: []` for each.
- `bun test internal/generate/testdata/typescript_client_conformance.test.ts`:
  26 passed, including metadata freezing/tree-shaking and pre-fetch validation.
- Both `apps/console/node_modules/.bin/tsc -p ...` commands above: passed.
- The same `tsc --noEmit --strict --target ES2022 --module ESNext
  --moduleResolution bundler --skipLibCheck` checked the temporary ONLV SDK entry
  and 234 before/after failure-union assertions: passed.
- `go test ./...` and `go test -race ./...`: passed.
- Twenty fresh serial samples of the five changed/new generator test roots and
  `TestRecordCrossFieldValidationIsTypedAndGenerated`: p95 below 10ms at Go JSON
  timing resolution (largest reported sample 10ms), below the 100ms budget.
- `go build -o .scenery/harness/bin/scenery ./cmd/scenery`: passed.
- `.scenery/harness/bin/scenery harness self --quick --summary --write`: the
  initial run found a missing living-document statement in this plan, now fixed.
  The final run checks the completed documentation and changed-area union.
- `.scenery/harness/bin/scenery harness self --release --summary --write`:
  all steps except the PostgreSQL service probe passed, including fixture matrix,
  generated TypeScript checks, generation compile probes, and the full race suite.
  It supersedes the ordinary full self-harness command.
- `scripts/release-gate.sh`, with a temporary `GOBIN` and checkout-local
  `SCENERY_BIN` to protect the shared installed CLI: Go and race suites passed,
  then lint failed with the Go version mismatch above. Later gate steps (UI build,
  dashboard embed, install, self-harness, clean-checkout install, fixture smoke,
  external-app smoke, router safety, artifact hygiene) were not reached.
- `git diff --check`: passed after normalizing the emitted runtime's final newline.

Release-wide green acceptance remains unproven until the PostgreSQL fixture and
Go-compatible linter are repaired and those commands rerun. No production Vite
build, deployment, or end-user latency measurement was performed.

## Idempotence and Recovery

Generation replaces one descriptor-covered artifact set atomically. Re-run the
three commands after an emit fix. Preserve unrelated edge/harness edits already
present in this checkout. Temporary measurement files are disposable and are
never committed with generated clients.

## Artifacts and Notes

Current contract: `docs/spec/typescript-client.md`. Fixture clients live under
`internal/compiler/testdata/{native,house}` and `testdata/assistant`.
Validation artifacts live under `.scenery/harness` and `.scenery/release-gate`.
The task-specific `ts-footprint-*` logs, timing JSONL, and temporary SDK/type
assertions retain local evidence; they are ignored cache, not committed artifacts.

## Interfaces and Dependencies

No dependency, configuration knob, new public helper, or schema change is
required. Private failure aliases and capability section markers are generator
implementation details.
