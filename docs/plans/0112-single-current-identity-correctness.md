# Single Current Identity Correctness

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current while implementing it.

## Purpose / Big Picture

Close five correctness gaps found after plan 0111 established Scenery's single
current specification. Normal compilation must never read a partially applied
workspace. External manifests and CLI envelopes must obey their exact current
schemas. Successful implementation verification must report `valid`. The
machine schema revision must identify the complete checked JSON Schema. The
specification revision must include structural source grammar and explicit
behavioral semantic identities, and callers must not receive mutable catalog
storage.

## Progress

- [x] 2026-07-13 - Reproduced the five review findings against `2c209fc9`, read the owning package instructions, and opened this corrective ExecPlan.
- [x] 2026-07-13 - Put crash-safe workspace transaction ownership and recovery below compiler and evolution; added a production compiler recovery test and removed the test-only compile wrapper.
- [x] 2026-07-13 - Added one exact manifest decode/validate boundary for raw manifests and current compile envelopes.
- [x] 2026-07-13 - Propagated structured implementation-check status through check, build, staged validation, and machine output; the native CLI fixture now asserts `valid`.
- [x] 2026-07-13 - Validated envelope revision values and bound CLI/event identities to the complete checked schemas.
- [x] 2026-07-13 - Expanded and froze the canonical specification catalog, regenerated affected artifacts, and completed repository plus ONLV acceptance.

## Surprises & Discoveries

- `internal/evolution/test_helpers_test.go` defines a safer test-only `Compile` than production, so the transaction test does not exercise `compiler.Compile`.
- `LoadManifestReference` permissively decodes both raw documents and a heuristic `data.manifest` wrapper; the fallback validates neither envelope nor manifest identity.
- CLI envelope constants hash compact descriptors even though the graph manifest already proves the intended complete-schema identity pattern with `spec.SchemaDocumentRevision`.
- Strict manifest decoding exposed a command test that constructed identity-incomplete manifests with fake contract revisions; the test now writes exact current manifests and recomputes their revisions.
- Applying a successful generation check initially turned an empty manifest diagnostic array back into `null`; the CLI fixture now decodes its own check envelope through the strict manifest boundary so this cannot recur.
- The complete specification revision intentionally changed builtin provider descriptor digests. ONLV therefore needed exact lock refresh plus two storage-runtime test fixture revisions before regeneration and acceptance could pass.
- ONLV's first harness run correctly rejected its previous build manifest. An explicit current `development` target build refreshed the disposable build identity, after which the harness passed.

## Decision Log

- Decision: Open plan 0112 instead of rewriting plan 0111's completed history.
  Rationale: The review is a bounded corrective release gate after the 0111 commit and needs its own resumable evidence.
  Date/Author: 2026-07-13 / Codex.
- Decision: Fix each issue at its shared production boundary without compatibility decoding.
  Rationale: One workspace transaction package, one manifest validator, one check-result state machine, and existing canonical schema hashing are the smallest fixes that cover every caller.
  Date/Author: 2026-07-13 / Codex.
- Decision: Keep stable diagnostic rule identity in `spec_revision` and explanatory meaning/documentation in the separate diagnostic catalog.
  Rationale: Message prose can improve without invalidating every contract, while codes, categories, identities, severities, and structured fields remain semantic.
  Date/Author: 2026-07-13 / Codex.
- Decision: Use eight explicit reviewed semantic revision constants for behavior not fully described by declarative schemas.
  Rationale: Source composition, defaults, expansion, reference resolution, contract projection, evolution, and both generators now require an intentional identity decision when behavior changes.
  Date/Author: 2026-07-13 / Codex.

## Outcomes & Retrospective

Completed. Production compilation now recovers abandoned source transactions or
rejects live owners before reading source. Raw manifests and compile envelopes
share one strict validator. Native checks report `valid`/`invalid` truthfully,
while non-applicable checks remain `not_requested`. CLI and event envelope
revisions identify their complete schemas and reject invalid revision shapes.
The specification catalog now covers structural grammar and explicit semantic
revisions, separates diagnostic prose identity, and exposes only deep copies.

The resulting current identities are specification
`sha256:e04518667a8b5c7f76d9ed3039a06cd7f98afe6f3d3be85867126b06eeb830aa`,
CLI schema `sha256:63e0e06289654ca0ab355a28890148f4d5bf7d905c3a857f2d6d2ef07f753bb6`,
event schema `sha256:8138ed6b8d979ade5ae1c826a0c2615f36384180ef841d2be1e2c7357f38d46d`,
and manifest schema `sha256:d5b3c19523c452c5fafe25bceac20c42ab1d6de2d285a11615c7d24f21c2c3c7`.

Validation passed `go test ./...`, `go test ./cmd/scenery`, `go vet ./...`, docs
inspection, and the complete self-harness. ONLV regenerated against the
worktree-local binary, reported `implementation_status: valid`, passed
`go test ./...`, and wrote a green app harness after refreshing its current
development build. No hosted CI run was available or claimed.

## Context and Orientation

`internal/compiler` reads `.scn` source. `internal/evolution` plans and applies
multi-file source edits under `.scenery/transactions`. `internal/graph` owns the
manifest and contract revision. `internal/machine` owns CLI JSON/JSONL
envelopes. `internal/generate` verifies generated artifacts and native Go
implementations. `internal/spec` owns the current resource/source schema and
diagnostic catalog.

Plan 0111 intentionally rejects old disposable artifacts. This plan does not
add legacy readers. It makes current readers strict and ensures a normal source
read first recovers an abandoned current transaction or rejects a live owner.

## Milestones

1. Restore crash-safe production compilation.
2. Make manifest and envelope trust boundaries exact.
3. Correct implementation verification status.
4. Make complete machine schemas and complete specification semantics drive identity.
5. Synchronize docs/generated artifacts and pass full acceptance.

## Plan of Work

Extract generic workspace transaction metadata, ownership verification,
commit, and recovery into `internal/workspacetx`. Make normal compiler entry
points recover or reject before source discovery; allow only the current
transaction owner through the staged-validation entry point.

Add `graph.DecodeManifest` and `graph.ValidateManifest`. Exact raw decoding and
an exact current `machine.DecodeData` compile envelope both feed the same
validator. Validate identity, producer, ordering, uniqueness, schemas, and the
recomputed contract revision.

Return a structured result from `generate.Check` and apply its explicit status
in CLI checks, build preparation, and evolution validation. Validate all CLI
revision-value wire shapes and prove envelope constants against complete
checked schemas.

Expand `spec.Catalog` with structural schemas and reviewed semantic revisions.
Return cloned schemas/maps from exported accessors. Regenerate every checked
artifact whose specification or schema identity changes.

## Concrete Steps

1. Add `internal/workspacetx`, migrate transaction tests, and call it from compiler/evolution.
2. Add graph manifest decode/validation tests and replace `LoadManifestReference` heuristics.
3. Change `generate.Check` callers and assert `scenery check -o json` reports `valid` for the native fixture.
4. Tighten machine decoding, update schema revision constants, and add complete-schema conformance tests.
5. Expand/freeze `internal/spec`, update documentation wording and architecture ownership sections, regenerate fixtures/ONLV, then run full validation.

## Validation and Acceptance

Focused validation:

```sh
go test ./internal/workspacetx ./internal/compiler ./internal/evolution
go test ./internal/graph ./internal/machine ./internal/spec ./internal/generate ./internal/build
go test ./cmd/scenery
go run ./cmd/scenery check --app-root testdata/apps/basic -o json
```

Final validation:

```sh
go test ./...
go test ./cmd/scenery
go vet ./...
go run ./cmd/scenery inspect docs -o json
go run ./cmd/scenery harness self --summary --write
git diff --check
```

Against `/Users/petrbrazdil/Repos/onlv`, regenerate with the worktree-local
Scenery binary, then run `scenery check -o json`, `go test ./...`, and
`scenery harness -o json --write`.

## Idempotence and Recovery

Workspace recovery remains idempotent: a current live owner is rejected, a
stale unreceipted transaction rolls back, and a stale receipted transaction
only removes transaction metadata. Manifest and envelope decoding are
read-only. Artifact regeneration is transactional and safe to rerun.

Preserve the pre-existing edits in `cmd/scenery/local_path_router.go`,
`cmd/scenery/local_path_router_test.go`, and `docs/local-contract.md`; touch the
last file only with a non-overlapping contract addition.

## Artifacts and Notes

Primary acceptance artifacts are `.scenery/harness/self-latest.json` in this
repository and `.scenery/harness/latest.json` in ONLV. Both are ignored local
evidence and must not be committed.

## Interfaces and Dependencies

Add one internal package:

```go
package workspacetx

type ReadMode uint8
const (
    NormalRead ReadMode = iota
    CurrentOwnerRead
)

func RecoverOrReject(root string, mode ReadMode) error
func ForceRecover(root string) error
```

Add graph trust-boundary functions and a structured generation check result:

```go
func DecodeManifest(encoded []byte) (*Manifest, error)
func ValidateManifest(manifest *Manifest) error

type CheckResult struct {
    Diagnostics          []graph.Diagnostic
    ImplementationStatus string
    ImplementationChecked bool
}
```

Use only the Go standard library and existing `spec.MarshalCanonical`,
`spec.SchemaDocumentRevision`, machine producer validation, and resource schema
tables. Add no schema engine, compatibility decoder, registry, or dependency.
