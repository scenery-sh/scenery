# HTTP Path-Tail Profile

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current throughout
implementation.

## Purpose / Big Picture

Implement the additive `scenery.http-path-tail/v1` and
`scenery.runtime-http-path-tail/v1` profiles specified by
`docs/specs/vnext/SCENERY_HTTP_PATH_TAIL_V1.md`. The immediate product proof is
the remaining ONLV Drive migration: legacy GET and DELETE routes shaped like
`/drive/*path` must lower to typed native bindings without raw Go HTTP access,
while malformed, ambiguous, or otherwise raw facets remain independently
blocked.

After this plan is complete, an edition-2027 app can declare
`/drive/{path...}`, map it through `path_tail "path"`, receive an ordinary typed
Go input, generate a segment-safe TypeScript URL, and migrate an eligible
legacy terminal wildcard without `SCN5401` being emitted solely for that route
shape.

## Progress

- [x] 2026-07-12 - Added the normative path-tail companion and synchronized the umbrella, HTTP codec, Go ABI, TypeScript client, legacy bridge, agent, and local-contract summaries.
- [x] 2026-07-12 - Implemented source schema, parsing, formatting, type checking, canonical graph projection, profile negotiation, and semantic revision behavior.
- [x] 2026-07-12 - Implemented compile-time route conflict analysis and runtime matching, decoding, precedence, CORS ownership, and registration verification.
- [x] 2026-07-12 - Implemented typed generated Go adapters, runtime-table metadata, TypeScript segment encoding, OpenAPI projection, and descriptor identities.
- [x] 2026-07-12 - Implemented profile-gated terminal-wildcard candidate lowering while retaining independent raw and unsupported-wildcard diagnostics.
- [x] 2026-07-12 - Added compiler, runtime, typed-value, TypeScript, OpenAPI, migration, and Drive-shaped conformance proof and passed the full Go and generated-client validation gates.

## Surprises & Discoveries

- The existing compiler accepts only single-segment parameters and rejects every wildcard path before lowering.
- Current migration candidate generation uses one `endpoint.Raw || strings.Contains(path, "*")` condition for `SCN5401`; implementing the profile requires classifying the route shape separately from body, response, handler, dependency, and guarantee facets.
- The umbrella profile table is checked against the implementation's exact claimed profile list. The two new profiles must remain outside that table until the implementation and profile advertisement land together.
- The shared router already had the required catch-all primitive and deterministic ordering. A contract-only strict marker was sufficient to enforce non-empty path-tail segments without changing generic wildcard behavior used by storage and legacy routes.
- Legacy `/*path` routes accept trailing or empty segments that the native profile deliberately rejects. Candidate generation can now represent the route shape, but comparison remains behaviorally advisory, exposes that boundary, and still requires the existing revision-bound risk approval before activation.

## Decision Log

- Decision: Add two extension profiles rather than replacing `scenery.http-codec/v1` or `scenery.runtime-http/v1` with v2 profiles.
  Rationale: Path tails add one independently negotiable route capability and do not change existing single-segment, body, response, negotiation, or gateway semantics.
  Date/Author: 2026-07-12 / Codex.
- Decision: Use only terminal `{name...}` plus a matching typed `path_tail` block.
  Rationale: A singular declarative spelling keeps route analysis, schema-driven authoring, generated clients, and migration lowering exact; globs, regular expressions, and raw router parameters remain outside the language.
  Date/Author: 2026-07-12 / Codex.
- Decision: Define zero-or-more complete segments with precedence `literal > single parameter > exact end > path tail` and no fallback after route selection.
  Rationale: This preserves existing exact and parameterized routes while allowing `/drive` to represent an empty tail deterministically.
  Date/Author: 2026-07-12 / Codex.
- Decision: Decode each captured segment exactly once and keep the Go operation ABI unchanged.
  Rationale: Segment boundaries remain structural and security-checkable, while application handlers receive the same typed input contract as every other binding.
  Date/Author: 2026-07-12 / Codex.
- Decision: Record canonical template, cardinality, type, empty behavior, decoding, precedence, guarantee, and both required profiles in the generated runtime table and validate them again at registration.
  Rationale: Runtime startup independently rejects compiler/runtime drift before accepting traffic.
  Date/Author: 2026-07-12 / Codex.
- Decision: Keep legacy terminal-wildcard lowering advisory when slash or decoding behavior differs instead of claiming verified equality.
  Rationale: The new profile removes the route-shape rewrite blocker, while `migrate compare` and `risk_advisory_migration_evidence` continue to own behavioral cutover risk honestly.
  Date/Author: 2026-07-12 / Codex.

## Outcomes & Retrospective

Completed on 2026-07-12. The toolchain now claims both additive profiles and
implements their source, effective graph, compiler, runtime, Go, TypeScript,
OpenAPI, compatibility, and migration projections. Drive-shaped GET and DELETE
legacy terminal wildcards generate typed native candidates without `SCN5401`
solely for the supported route shape; raw and unsupported shapes remain
diagnosed, and `SCN5405` explicitly retains advisory legacy parity review.
Native strict-tail conformance covers zero, one, and nested segments,
precedence, no fallback, unsafe decoding, canonical typed construction, CORS,
registration drift, and generated metadata. Legacy trailing-slash differences
remain advisory comparison evidence rather than being upgraded to verified
native equality.

Final validation passed `go test -count=1 ./...`, both configured generation
drift checks, sixteen TypeScript conformance tests, generated-client typecheck,
docs inspection, and self-harness. Self-harness reported `ok: true` and
`pass_with_warnings`: zero errors, 34 schema validations, 10 fixture-matrix
passes, and only advisory existing review-due, generated-runtime file-size,
and test-timing warnings.

## Context and Orientation

The source schema and compiler HTTP validation live in
`internal/vnext/source_schemas.go`, `internal/vnext/http.go`, and related
type-checking files. Claimed profiles live in `internal/vnext/model.go` and are
kept synchronized with the umbrella specification by
`internal/vnext/spec_consistency_test.go`. HTTP runtime routing and generated
composition live under `internal/vnext/`; generated TypeScript URL behavior is
owned by the TypeScript generator files there. Legacy candidate classification
is in `internal/vnext/migration_candidate.go`, and `SCN5401` is registered in
`internal/vnext/diagnostics_catalog.go`.

The normative contract is
`docs/specs/vnext/SCENERY_HTTP_PATH_TAIL_V1.md`, with integration summaries in
the language, HTTP, Go, TypeScript, and legacy-bridge companions. The current
implemented boundary is documented in `docs/local-contract.md` and
`docs/agent-guide.md`.

## Milestones

1. Compile and format the source syntax into a canonical, typed path-tail binding with honest profile negotiation.
2. Prove deterministic matching, conflicts, strict segment decoding, and registration parity at compile time and runtime.
3. Populate the normal generated Go input and generate exact TypeScript URLs without exposing raw transport objects.
4. Lower eligible legacy terminal wildcards while retaining independent diagnostics for every other unresolved facet.
5. Validate generated artifacts and the ONLV Drive candidate, then advertise the profiles and update the current contract.

## Plan of Work

Implement one vertical route fixture first: a GET binding at
`/drive/{path...}` targeting `relative_path`. Add source metadata and formatter
support, then canonical lowering, type checks, and profile requirements. Extend
the route matcher only after compiler conflict fixtures define the complete
precedence matrix. Reuse the existing HTTP scalar decoder and
`relative_path` constructor, adding segment-boundary and double-decode checks
at the route boundary rather than another path-cleaning abstraction.

Once runtime dispatch populates the generated input, extend TypeScript request
generation to split semantic tail values, encode segments independently, and
join with structural slash. Finally, split legacy wildcard classification from
other raw facets and lower only candidates whose semantics and evidence meet
the companion specification. Do not weaken `SCN5401` for unsupported wildcard
shapes or unresolved raw behavior.

## Concrete Steps

1. Add `path_tail` authored-schema metadata, `{name...}` parsing/formatting, exact mapping validation, supported target types, and unsupported-profile behavior.
2. Add canonical binding metadata, required profile identities, provenance, and revision inputs; update machine-readable schemas and fixtures together.
3. Implement complete-match route comparison, zero-segment matching, equal-tail conflicts, runtime registration verification, and no fallback after selection.
4. Implement raw-segment capture, exact one-time UTF-8 percent decoding, hazardous separator/traversal/double-decode rejection, typed construction, and transport diagnostics.
5. Populate ordinary generated Go input fields without changing handler signatures or exposing raw request/router state.
6. Generate TypeScript URLs by validating semantic segments, encoding them independently, and omitting the slash for an empty tail.
7. Split migration candidate route-shape classification from remaining raw facets, lower eligible `/*path` routes, and narrow `SCN5401` to unsupported cases.
8. Add compiler, runtime, Go, TypeScript, OpenAPI, compatibility, migration, and Drive-shaped conformance fixtures; synchronize docs, schemas, profile advertisement, and generated artifacts.

## Validation and Acceptance

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./internal/vnext -run 'PathTail|HTTP|Migration|TypeScript' -count=1
    go test ./internal/vnext -count=1
    bun test internal/vnext/testdata/typescript_client_conformance.test.ts
    apps/consolenext/node_modules/.bin/tsc -p internal/vnext/testdata/tsconfig.generated-clients.json
    go test ./...
    go test ./cmd/scenery
    go run ./cmd/scenery inspect docs --json
    go run ./cmd/scenery harness self --summary --write

Acceptance requires all companion conformance fixtures to pass, exact profile
advertisement in CLI/agent/manifests, no raw transport object in a path-tail Go
handler, byte-stable generated artifacts, and a Drive-shaped migration
candidate whose GET and DELETE operations are present without `SCN5401` solely
for `/drive/*path`. Empty, nested, malformed, traversal, encoded-separator,
double-encoded, overlap, conflict, and no-fallback cases must all have executable
proof. Remaining raw multipart, response, dependency, or handler gaps must stay
visible and continue to block activation as applicable.

## Idempotence and Recovery

Keep generation staged and atomic. Tests use temporary app roots and must not
install a shared `scenery` binary. If route-table or generated-output work
fails, the prior checked-in fixtures remain the recovery point; rerun focused
fixtures before the full suite. Migration planning remains read-only until the
ordinary immutable apply boundary commits a candidate.

## Artifacts and Notes

The product input is the shared Drive migration design discussion at
<https://chatgpt.com/share/6a536088-b58c-83ed-a744-3d21fa81f85b>. The durable
design artifact is `docs/specs/vnext/SCENERY_HTTP_PATH_TAIL_V1.md`; checked-in
fixtures, generated descriptors, docs inspection, self-harness output, and the
Drive migration candidate are the implementation evidence. `.scenery/` output
remains uncommitted operational state.

## Interfaces and Dependencies

Reuse the existing HCL parser/CST, recursive authored schemas, canonical graph,
HTTP scalar decoder, `relative_path` type, route registry, generated Go input
types, TypeScript encoder, semantic compatibility engine, and migration plan
model. Add no environment variable, router dependency, raw-handler ABI,
filesystem path cleaner, compatibility spelling, or second JSON codec.
