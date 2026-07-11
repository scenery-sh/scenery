# Edition-2027 Transaction and Migration Conformance

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current throughout implementation.

## Purpose / Big Picture

Close the independent re-review findings against commit `e5164b1f8833d643d68d3c11ce9daa0976d16557`. Apply must trust only plans actually issued by this workspace, advisory legacy evidence must never be upgraded to verified equivalence, migration candidates must validate in their predicted active graph, semantic rename must cover migration references and module descendants, bounded legacy TypeScript generation must survive removal of the v0 config, defaults must precede patches, generated Go config schemas need exact input provenance, and source positions must scale near-linearly.

## Progress

- [x] 2026-07-11 - Reproduced all eight reported mechanisms against the current implementation and controlling 0.4-draft specifications.
- [x] 2026-07-11 - Added public-boundary regressions for plan tampering, migration evidence/candidates, rename, config-free legacy clients, defaults/patches, provenance, and source positions.
- [x] 2026-07-11 - Implemented every vertical slice and passed the focused `internal/vnext` regressions, including change/migration/deployment tampering and cross-owner route/durable/schedule/event identities.
- [x] 2026-07-11 - Synchronized the umbrella, Go, TypeScript, legacy-bridge, local, agent, skill, README, root instruction, and knowledge contracts; existing plan schemas remain unchanged because issued-plan retention is operational state and comparison fields are an existing versioned response extension.
- [x] 2026-07-11 - Verified byte-identical House, native, and bridge Go/TypeScript artifact sets; the bridge check used the declared Go 1.26.3 toolchain binary rather than the underlying 1.26.0 bootstrap binary.
- [x] 2026-07-11 - Ran independent standards and specification reviews, then closed their follow-up findings: canonical-only client auth, typed evidence, unioned cutover classes, exact cross-owner fixtures, retained migration apply, and independently valid durable collision coverage.
- [x] 2026-07-11 - Passed full validation and repeated independent standards/specification review until both axes reported no actionable findings.
- [x] 2026-07-11 - Committed the completed change on `main`.

## Surprises & Discoveries

- Change, deployment, migration transition, initialization, candidate, and finish plans all use caller-recomputable content hashes. The same trusted-issuance check must cover every apply entry point, not only the three examples in the review.
- Exported patch targets have already been canonicalized through module exports by the time exact patches run; default-before-patch ordering also required canonical export-aware patch resolution.
- Formatting during rename can shift declaration byte ranges. Descendant rename lineage therefore uses stable source identity plus kind/name and the old/new module chains instead of treating byte offsets as durable identity.
- The compiled mixed snapshot already carries application identity, active canonical authentication, selected legacy services, and binding ownership, so post-config legacy TypeScript generation needs no replacement ambient configuration surface.
- Authentication/client options in that snapshot must come only from structured authentication and handler resources. Free-form legacy symbols and construct descriptions are explanatory metadata and can contain misleading business words such as `Google` or `standard auth`.
- Operational cutover classes belong to the service transition, not only the destination candidate. Removing a legacy-only durable execution, schedule, or event consumer still requires the corresponding evidence during native activation.
- Compatibility evidence must come from typed `LegacyCompatibility` metadata. Scanning arbitrary resource values for words such as `advisory` incorrectly classifies ordinary business data as migration evidence.
- Re-running a migration transition after a dry run creates a new `expires_at` and therefore a different plan ID. Detached approval needs a raw retained transition plan plus a separate apply command; passing the old token to a freshly replanned transition can never work.
- Collision regressions must prove the candidate is otherwise valid. Merely asserting that the expected collision code appears can hide missing operations, providers, or other unrelated diagnostics in the same fixture.
- A shared flag parser does not make planning flags meaningful on apply. `migrate apply --dry-run` must fail before mutation, and raw plan decoding must reject unknown fields rather than silently projecting them away before the issued-plan comparison.
- Exact supplied-plan decoding is one transaction-family invariant. Change, migration, and deployment apply must share the same unknown-field and single-JSON-value boundary rather than letting only the newest command satisfy the normative rule.
- A line-start source index plus per-line rune decoding preserves Unicode-scalar columns while the many-token benchmark scales near-linearly when source size doubles.
- Self-harness correctly flagged the touched mutation and test files after they crossed the 1,000-line warning threshold. Moving rename receipts, legacy compatibility construction, and the new conformance tests into focused files restored every changed source file below the threshold without changing behavior.

## Decision Log

- Decision: Retain the exact canonical issued plan under `.scenery/plans/issued/` and require apply to match it before trusting expiry, approvals, operations, edits, or provider actions.
  Rationale: This uses the existing app-local transaction state, preserves public plan JSON and detached approvals, needs no key lifecycle, and closes caller recomputation across every plan family in one shared boundary.
  Date/Author: 2026-07-11 / Codex.
- Decision: Report static contract completeness/equality independently from behavioral and operational evidence, aggregate the weakest construct evidence, and require `risk_advisory_migration_evidence` while behavioral evidence is incomplete.
  Rationale: Static shape comparison remains useful without falsely claiming behavioral equivalence or silently bypassing approval at cutover.
  Date/Author: 2026-07-11 / Codex.
- Decision: Validate a migration candidate as the current active graph with only its service owner replaced.
  Rationale: This preserves valid cross-service dependencies and makes all global ownership collisions visible before status or comparison claims candidate validity.
  Date/Author: 2026-07-11 / Codex.
- Decision: Derive legacy TypeScript authentication only from canonical authentication resources and exact standard-auth handler metadata.
  Rationale: Free-form source symbols are not a contract and substring inference can silently enable client features for unrelated operations.
  Date/Author: 2026-07-11 / Codex.
- Decision: Compute operational cutover classes from the union of legacy and native candidates and derive evidence completeness only from typed compatibility metadata.
  Rationale: A transition must account for stateful identities removed from the legacy side while ordinary business values must not change evidence status.
  Date/Author: 2026-07-11 / Codex.
- Decision: Add `--out <plan>` to migration transition planning and `scenery migrate apply <plan>` as the approval-bearing execution boundary.
  Rationale: The external approver can bind a token to the exact issued, expiry-bound plan and apply can load that same object; replanning is neither required nor presented as equivalent.
  Date/Author: 2026-07-11 / Codex.
- Decision: Keep migration apply positional and strict: no `--plan` alias, no transition-planning flags, and no permissive JSON fields or trailing values.
  Rationale: One public spelling and exact decoding keep the authenticated object boundary observable and prevent a flag named `--dry-run` from performing writes.
  Date/Author: 2026-07-11 / Codex.
- Decision: Reuse one strict plan-file decoder across change, migration, and deployment apply.
  Rationale: Unknown fields or trailing JSON must fail before each family reconstructs and compares the authenticated issued plan.
  Date/Author: 2026-07-11 / Codex.
- Decision: Apply effective defaults before exact patches and let patch provenance replace the default at the patched path.
  Rationale: A patch precondition must observe the effective default promised by the edition while explain still identifies the final supplier exactly.
  Date/Author: 2026-07-11 / Codex.

## Outcomes & Retrospective

Every reported release blocker, high finding, and medium finding is closed. Apply authenticates exact issued change, deployment, migration transition, initialization, candidate, and finish plans before trusting mutable fields; CLI plan readers reject expanded or trailing caller JSON; approval-bearing migration transitions retain and apply one exact plan. Migration status now separates static, behavioral, and operational evidence, aggregates the weakest typed compatibility status, includes both candidates' cutover classes, and validates candidates in the predicted multi-owner graph.

Mixed rename covers migration references and containing-module descendants, config-free legacy TypeScript generation uses canonical compiled resources, defaults precede exact patches, generated config schema provenance points to its package input, and Unicode source positions use a near-linear index. The final standards and specification reviews both passed after their follow-up findings were converted into executable regressions, including strict non-mutating apply grammar and independently valid collision fixtures.

## Context and Orientation

Plan/apply transactions live in `internal/vnext/changes.go`, `deployment_plan.go`, `migration_plan.go`, `migration_init.go`, `migration_candidate.go`, and `migration_finish.go`. Migration graph linking and comparison live in `migration.go`; semantic rename lives in `changes.go`; legacy TypeScript generation lives in `generate_typescript.go`; compilation ordering and provenance live in `compiler.go`, `go_config.go`, `module_inputs.go`, and `provenance.go`; source/CST position conversion lives in `source.go` and `cst.go`. Normative contracts live under `docs/specs/vnext/`.

## Milestones

1. Authenticate every issued plan at apply and prove tampered approvals, edits, operations, expiry, and provider actions fail before staging.
2. Make migration comparison and candidate validation truthful at the activation boundary.
3. Complete mixed-app rename and TypeScript generation after v0 config removal.
4. Correct effective compilation/provenance ordering and remove quadratic source-position scans.
5. Synchronize contracts, run the full validation matrix, review, and commit.

## Plan of Work

Work in test-first vertical slices at the already agreed public seams: plan then apply; compile then migrate status/compare/transition; semantic change plan then apply/diff; compile and generate a clean mixed fixture; compile and inspect effective provenance; parse Unicode source and benchmark many-token sources. Reuse current transaction paths, graph validators, migration inventory, source metadata, and standard-library crypto/JSON/filesystem support. Add no new dependency or alternate plan API.

## Concrete Steps

1. Add one shared issued-plan persistence and exact-match verifier, then call it from every plan/apply pair.
2. Separate static-contract equality from behavioral and operational evidence, aggregate service status from the weakest construct, and require advisory-evidence approval at activation.
3. Validate each candidate as current active graph minus its active owner plus the candidate.
4. Rewrite typed migration-manifest traversals and derive revision-bound descendant rename receipts for module-instance renames.
5. Build legacy client identity/options from the compiled migration snapshot when `legacy_config` is absent.
6. Apply effective defaults before exact patches and attach package-input ranges to every generated config-schema field.
7. Build one line-start index per source and use it for all parser/CST ranges; add Unicode correctness and scaling benchmarks.
8. Update public docs, agent instructions, schemas/fixtures, this plan, and plan indexes.

## Validation and Acceptance

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./internal/vnext -count=1
    go test ./cmd/scenery -count=1
    go test ./... -count=1
    bun test internal/vnext/testdata/typescript_client_conformance.test.ts
    apps/consolenext/node_modules/.bin/tsc -p internal/vnext/testdata/tsconfig.generated-clients.json
    go run ./cmd/scenery inspect docs --json
    go run ./cmd/scenery harness self --summary --write

Acceptance additionally requires negative tampering tests for change, migration, and deployment plans; advisory comparison/approval proof; cross-service candidate references and collision proof; mixed migration and module-descendant rename proof; config-free bounded legacy TypeScript generation and planning; default-before-patch behavior; exact config-schema provenance; and a many-token source benchmark.

## Idempotence and Recovery

Issued plan writes are atomic, mode `0600`, content-addressed, and safe to repeat only with identical bytes. Apply continues to stage and revalidate before committing source/provider effects. Tests use temporary workspaces. If a focused test fails, rerun that package before the full suite. Do not install a shared `scenery` binary.

## Artifacts and Notes

The independent re-review in the originating Codex thread is the defect inventory. Checked-in tests, schemas, generated fixtures, self-harness output, and the final two-axis review are the durable evidence. `.scenery/` remains uncommitted operational state.

Final validation evidence: focused `cmd/scenery` and `internal/vnext` packages and uncached `go test ./... -count=1` pass; all three fixture generation checks report no changed files; all fifteen TypeScript conformance tests and generated-client typechecking pass; docs inspection reports 99 documents with zero missing/stale entries; the source benchmark scales from 5.26 ms at 256 blocks to 10.63 ms at 512 and 20.27 ms at 1024. Final default self-harness reports `ok: true`, no failing steps, 10/10 fixtures, all 34 schema payloads valid, and zero changed-area architecture warnings. Its `pass_with_warnings` status contains the pre-existing 30 review-due documents and advisory Go timing confirmations only.

## Interfaces and Dependencies

Reuse canonical JSON, `atomicWriteSynced`, existing receipt directories, active migration resources, `Origin` and `FieldProvenance`, HCL traversal nodes, and the Go standard library. Do not add a signing dependency, alternate mutation language, duplicate graph validator, or ambient legacy-config fallback.
