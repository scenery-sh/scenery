# Edition-2027 Conformance Hardening

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current throughout implementation.

## Purpose / Big Picture

Close the conformance gaps found by independent reviews of commits `1dcba0536b307aa0411e8a26df865178881cc426` and `9dbc245faa048928f1c9642012733d08641e505c`. The delivered compiler must preserve truthful source/effective/expanded views with field provenance, provide complete semantic rename receipts and reference rewriting, keep wire-label schemas and authoring aligned, reject unsafe mixed Go receivers statically, normalize exact datetimes and semantic plans, support aliased Go config inputs, classify declarative extensions as known unsupported syntax, and keep the umbrella specification mechanically consistent with its companions.

## Progress

- [x] 2026-07-11 - Confirmed a clean `main` checkout at the reviewed SHA and inspected current documentation state.
- [x] 2026-07-11 - Added exact-size and NFC relative-path regression seams, observed them fail, and implemented the scalar behavior.
- [x] 2026-07-11 - Closed source-level Go config and per-operation bridge migration gaps with source and generated-application fixtures.
- [x] 2026-07-11 - Closed structured mutation and descriptive schema gaps for all thirty-five advertised resource kinds.
- [x] 2026-07-11 - Closed diagnostic catalog and source-map portability gaps, including Unicode/CRLF coverage.
- [x] 2026-07-11 - Reconciled specifications and public profile claims with the feature-complete draft and explicit Appendix E boundary.
- [x] 2026-07-11 - Passed targeted, full-suite, generated-artifact, schema/docs, ONLV regeneration, and self-harness validation.
- [x] 2026-07-11 - Completed the initial two-axis standards/specification review, fixed its findings, and produced reviewed commit `9dbc245f`.
- [x] 2026-07-11 - Preserved authored source values separately from resolved/defaulted effective values and exposed RFC 6901 field provenance with exact suppliers and ranges.
- [x] 2026-07-11 - Completed nested semantic rename, typed traversal rewriting, revision/digest-bound evidence, durable plan/apply receipts, and shared-package safety.
- [x] 2026-07-11 - Aligned wire-label metadata, mixed bridge receiver checking, datetime lexing, plan normalization, config aliases/constraints, extensions, and normative summaries.
- [x] 2026-07-11 - Re-ran generated artifacts, full validation, and the two-axis review; both final review passes reported no remaining actionable findings.

## Surprises & Discoveries

- The HTTP companion already defines `url` as a hierarchical network URI and rejects opaque URIs, while the language primitive table still uses broader absolute-URI wording. The implementation follows the narrower network-URL contract, so the umbrella wording must be aligned rather than broadening runtime behavior silently.
- Canonical mutation values required an explicit lowering step: source traversals and exact scalar constructors are presentation syntax, while agent operations carry canonical addresses and tagged values.
- Correct field-domain metadata changed checked contract revisions, so downstream generated artifacts must be regenerated. ONLV was kept untouched and validated through an isolated clean copy.
- Unicode NFC made `golang.org/x/text` a direct dependency; self-harness required the dependency allowlist to record the edition-pinned normalization rationale.
- The final specification review found two catalog-level gaps beyond the original seven: mandatory binding fields and the HTTP-only gateway condition were under-described, and idempotency keys were described as one reference instead of an ordered composite. The shared schema definitions and mutation fixture now enforce both contracts.
- Focused re-review then exposed two deeper enforcement seams: keyed idempotency still tolerated missing or scalar keys, and unresolved platform listener/certificate fields were advertised as unsupported without compile-time rejection. Conditional authored schemas, canonical validation, mutation validation, retry/generator guards, and unsupported-draft diagnostics now close those seams.
- The next focused pass found that syntactically valid `input.*` key paths were not resolved against the operation input graph. Idempotency now resolves the input record and accepts only existing direct fields; missing, computed, nested, unit-input, and non-input paths fail before retry selection or generation.
- The follow-up two-axis review found that provenance labels were not RFC 6901 pointers, prepared module blocks still contaminated source-module values, config aliases dropped input constraints, and rename evidence was trusted without binding validation. Provenance now resolves against the exact view, source compilation retains original module/export expressions, config aliases carry and enforce constraints, and compatibility consumes only digest-valid receipts for the compared revisions.
- A physical package can be instantiated more than once, so one instance-address rename would otherwise edit shared source while pretending only one graph address changed. Semantic rename now rejects that ambiguous mutation and requires an explicit package-level refactor.

## Decision Log

- Decision: Keep `url` as the existing normalized hierarchical network URL type and align the umbrella specification with the HTTP companion.
  Rationale: Host normalization, IDNA, default-port removal, generated clients, and the HTTP codec all require an authority and cannot correctly implement opaque URI semantics under the same type.
  Date/Author: 2026-07-11 / Codex.
- Decision: Advertise `resource.create` for every resource kind only after its recursive authored metadata is complete.
  Rationale: The shared schema nodes now drive source validation, rendering, public discovery, enum/phase validation, and revision projection; silently accepting a shallower kind would recreate the reviewed defect.
  Date/Author: 2026-07-11 / Codex.
- Decision: Keep unresolved Appendix E areas as an explicit capability rejection list rather than weakening claimed v1 profile semantics.
  Rationale: Resolved normative behavior is implemented and tested; unresolved draft vocabulary is not a stable contract that an implementation can honestly claim.
  Date/Author: 2026-07-11 / Codex.

## Outcomes & Retrospective

Every finding against both reviewed SHAs is closed. Source, effective, and expanded graphs now preserve their distinct meanings; package/module expressions remain authored in source, every provenance key resolves into its graph value, schema defaults appear only in effective/expanded, and supplier/range chains cover inputs, exports, patches, expansions, and providers. Semantic rename handles nested modules and composite traversals, emits durable digest-checked receipts, supports future CLI/agent diffs, and rejects ambiguous shared-package edits. Wire-label creation, mixed bridge receiver safety (including receiver-free package handlers), exact datetime parsing, normalized multi-operation plans, aliased constrained Go config, extension classification, and companion/umbrella consistency are enforced with regressions.

The final standards and specification re-reviews reported no remaining actionable findings. Validation passed with `go test ./... -count=1`, zero-drift generation checks for the house/native/bridge fixtures, fifteen Bun TypeScript codec tests, generated-client TypeScript checking, docs inspection with 98 documents and zero missing/stale entries, and self-harness `ok: true` with all 34 schemas valid, no failing steps, and no timing or drift findings.

## Context and Orientation

The public scalar types live in `vnext_scalars.go`. Edition parsing, authored schemas, canonical resources, generation, mutation, agent schemas, diagnostics, source maps, and their fixtures live under `internal/vnext/`; the v1 CLI envelope and exit classification live under `cmd/scenery/`. The normative contracts live under `docs/specs/vnext/`, with the umbrella language document and five profile companions governed by that directory's `AGENTS.md`.

The implementation at the reviewed SHA is integrated and profile-broad. This plan does not redesign it. It repairs the remaining places where a public profile promises more or different behavior than the implementation provides.

## Milestones

1. Make authored Go configuration and mixed handler adapters compile into one valid runtime integration plan.
2. Make mutation and schema discovery share enough recursive schema metadata to author every supported structured resource.
3. Make diagnostics and source locations stable machine contracts.
4. Make exact scalars match the language grammar and normalize only the named canonical types.
5. Reconcile claims, prove conformance, review the diff, and commit.

## Plan of Work

Work in vertical test-first slices at public seams: compile real `.scn` fixtures, generate application adapters, plan/apply semantic changes, query public schemas, consume CLI envelopes, inspect portable source maps, and parse public scalar constructors. Prefer extending the existing schema and compiler definitions over adding parallel registries. Keep every generated edit deterministic and workspace-confined.

Once behavior passes focused tests, update the umbrella and companion wording, README/local contract, plan indexes, and knowledge metadata. Run the full repository validation matrix and then the required standards/specification review against the starting SHA. Fix review findings before creating one commit on `main`.

## Concrete Steps

1. Add failing fixtures for all seven reported cases and capture the expected diagnostic or generated result.
2. Implement the smallest shared fix at each existing compiler/generator boundary.
3. Regenerate or update public JSON schemas only where the profile contract changes.
4. Update documentation and this plan with verified decisions and evidence.
5. Run targeted tests continuously, then the full validation and review commands below.

## Validation and Acceptance

From `/Users/petrbrazdil/Repos/scenery`:

    go test . -count=1
    go test ./internal/vnext -count=1
    go test ./cmd/scenery -count=1
    go test ./... -count=1
    go run ./cmd/scenery inspect docs --json
    go run ./cmd/scenery harness self --summary --write

Acceptance requires real source fixtures for dynamic service config and mixed operation adapters; structured create fixtures for record, operation, HTTP binding, service config, and nested modules; rich recursive public schemas; a unique checked diagnostic catalog with internal report tokens; collision and Unicode/CRLF source-map fixtures; exact fractional-size and NFC/NUL path fixtures; explicit rejection of unresolved Appendix E surfaces; and a clean two-axis review.

## Idempotence and Recovery

Compilation and checks are read-only. Tests use temporary workspaces. Generation and semantic changes already use recoverable transactions; new rendering must preserve that boundary. If validation fails, rerun the narrow failing package before the full suite. No installed shared `scenery` binary is modified.

## Artifacts and Notes

The independent review text in the originating Codex thread is the defect inventory. Checked-in tests, schema outputs, CLI envelopes, and self-harness artifacts are the durable evidence. Generated `.scenery/` cache remains uncommitted.

## Interfaces and Dependencies

Reuse HashiCorp HCL's authored syntax model, the existing canonical `Resource` graph, and the existing source-schema definitions. Reuse the pinned `golang.org/x/text/unicode/norm` Unicode 15.0 tables for edition-pinned NFC behavior. Do not add an alternate parser, mutation format, diagnostic transport, or source-map representation.
