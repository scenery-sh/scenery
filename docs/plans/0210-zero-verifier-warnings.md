# 0210 Zero Repository Verifier Warnings

## Purpose / Big Picture

Remove the remaining default verifier warnings by reviewing overdue living
documentation and splitting large authored files along existing responsibilities.
Acceptance is a full default verifier run with zero warnings and unchanged public
behavior, generated clients, schemas, and resource revisions. Preserve the earlier
uncommitted warning corrections in this checkout.

## Progress

- [x] (2026-10-03) Recorded the baseline: 30 overdue documents, 15 authored-file
  size warnings, and one advisory cached-suite warning (7.463s versus 5s).
- [x] (2026-10-03) Split the 15 authored files within their existing packages and prove that
  declarations and embedded renderer output are preserved.
- [x] (2026-10-03) Review the 30 indexed documents against current owners, schemas, tests and
  active-plan evidence; correct drift before updating their review metadata.
- [x] (2026-10-03) Completed cumulative validation and published stable default
  run `20261003T215302.187208000Z` with zero warnings/errors and a 2.616s Go suite.

## Surprises & Discoveries

The baseline is `.scenery/harness/runs/20261003T203259.538950000Z/self.json`.
Its suite-duration warning is advisory cached execution, not isolated test-root
timing evidence. Keep the 5s budget and investigate actual uncached work if the
final run still exceeds it.

All 7,310 ordinary Go declarations are unchanged. The only declaration changes
are `renderTSRuntimePublic` and four concatenated constants; the original
TypeScript template bytes remain SHA-256
`20f69e1d0c70eba5135104c6ad26070230e6d25e7e0e1a1eef0784d221152c67`.
The affected-package suite passed after the moves. Every resulting file is below
1,000 lines; no limits or generated-source classifications were changed in this
round.

Documentation review found real drift: the Go implementation companion still
named the removed library build path; SPEC Appendix E called implemented
compatibility, approval, deployment-plan and native-tool identity contracts
future work; the auth migration runbook still instructed setting `JWT_SECRET`
despite the environment-only `auth.jwt_secret` configuration cutover. Corrected
those references to their current owners and kept the two reviewed plans active.

Review evidence for the 30 overdue documents:

- The 22 checked schemas retain their exact content. Read their required fields,
  discriminated variants and identities against current CLI payloads, machine
  envelopes, assistant public/control types, graph manifests and build/generation
  descriptors. Existing assistant variant/privacy tests, checked identity tests
  in `internal/machine`, `internal/build`, `internal/generate`, and
  `internal/graph`, and the verifier's representative schema validation own proof.
- `docs/spec/SPEC.md` and `go-implementation.md`: checked the implementation
  boundary, configuration layers, public generation ownership, typed Go ABI,
  target identities, and unavailable-feature declarations against current
  compiler/generator/build owners and fixture tests. Corrected the obsolete
  future-feature and library references above.
- `docs/spec/http-path-tail.md`: checked syntax, type/cardinality, overlap,
  percent-decoding, traversal rejection and projection against compiler,
  generated-client and runtime conformance tests. No contract change needed.
- `docs/spec/evolution.md`: compared dimensions, directional type transitions,
  security relations and exact rename evidence with the current classifiers and
  their focused tests. No contract change needed.
- Plans 0145 and 0101: inspected current outcomes and owners. Added review notes
  that preserve unresolved timing and literal reboot/login acceptance.
- `docs/environment.registry.json`: checked current schema, supported/removed
  input directions and config replacements, including `JWT_SECRET`, against
  the environment-policy scan and `internal/appconfig`. No registry change needed.
- `docs/runbooks/standard-auth-migration.md`: compared the target table inventory,
  copy/foreign-key checks, cookie behavior and configuration with
  `auth/db/gen/schema.sql`, `auth/standard.go` and the auth configuration catalog.
  Corrected the signing-secret configuration and table inventory. This is a
  documentation review; no production migration was executed.

The first full run (`20261003T213821.879407000Z`) had zero documentation/size
warnings but rejected the relocated legacy deployment-journal detector because
its exact source-path exemption still named `deployplan.go`. Moved that exemption
to `deployment_state.go`; no new migration spelling or runtime path was added.
The run's 9.945s suite warning followed source/document changes. A diagnostic
cached run still executed 12 packages (9.239s); an identical cache-debug replay
then reused all 69 test-package results. The cache is functional. Retain the
5s gate and verify the normal final command without diagnostic settings.

The assistant CLI tests compiled the shared native fixture and one wrote its
status snapshot there. They now use isolated copies. The existing copy helper
also copied 196 MB of ignored `.scenery` state before deleting it; it now skips
machine-local directories before reading them, using the existing app-walk
policy. The CLI package passed in 2.405s after this correction, preserving the
same status/privacy/schema assertions. This test-only isolation correction is
outside the earlier declaration-parity comparison.

The changed-area union also recommends `scenery logs --limit 500 -o jsonl` for
runtime paths. It is unselected here: this framework checkout has no root
`.scenery.json` or running target application, and declaration/renderer parity
proves that no runtime behavior changed. No external probe or release gate is
required for file organization and documentation corrections. These commands
are unverified, not passes.

## Decision Log

- (2026-10-03) Keep package ownership and exported APIs unchanged. Split related
  declarations and rendering sections rather than introduce new abstractions.
- (2026-10-03) Review dates record an actual source/contract review. Do not close
  active plans or mark documents historical to remove overdue warnings.
- (2026-10-03) Select full verification immediately because generator, runtime,
  specification and repository-verifier sources are affected. No new runtime
  behavior or external boundary is intended; named external probes are required
  only if review reveals a necessary behavioral change at that boundary.

## Outcomes & Retrospective

Completed on 2026-10-03. Full default run
`.scenery/harness/runs/20261003T215302.187208000Z/self.json` passed every selected
stage with zero diagnostics and stable input revision
`sha256:c0a47eddc1c50ad162b4e7aad8292d2a53f0d1eab47f94ccfa970e393ef752a8`.
The 5s cached-suite budget remains unchanged. The preceding post-edit run
`20261003T215220.990169000Z` passed checks but reported 11.528s while newly changed
test inputs were populated; the unchanged replay took 2.616s. Cache timing is
advisory and does not establish fresh isolated-root p95 acceptance.

All 15 warned files were split within their original packages. Reviewed all 30
overdue living documents, corrected actual contract drift, and retained unresolved
acceptance in plans 0101 and 0145. The affected Go suite and full repository Go
suite passed; full verification also passed vet, knowledge, architecture, drift,
schema and local-binary freshness checks. Native, house and assistant client
regenerations reported no changed artifacts. Bun conformance passed 52 tests
(272 assertions), both TypeScript configurations passed, final lint reported
zero issues, and `git diff --check` passed. Public behavior and checked schema
content are unchanged; documentation corrections are recorded above.

Close this plan in the active/completed indexes and run full verification against
those final documentation inputs. The resulting immutable run bundle is the
delivery evidence; this historical plan will not be edited again. Runtime logs,
external probes, release certification and all-root isolated timing remain
unselected for the reasons above. No application data was changed or deleted.

## Plan of Work

In `/Users/petrbrazdil/Repos/scenery`, split the warned CLI deployment, edge and
development-supervisor files; agent registry; deployment planner; development
report store; durable store; evolution planning and compatibility; TypeScript
renderers and generation tests; source-schema metadata; and assistant gateway.
Keep cohesive functions, types and their comments together. Compare Go AST
declarations before and after, ignoring imports and file placement. For the one
large TypeScript raw literal, partition at named functional boundaries and verify
the concatenated bytes exactly.

Review the overdue checked schemas against their current identities, producer
types and existing schema validation. Review specification companions against
their owning implementation and conformance tests. Review the auth migration
runbook and environment registry against current supported recovery/configuration
paths. Inspect current acceptance and remaining work in plans 0145 and 0101;
preserve unfinished operator or measurement acceptance.

Record review evidence and any corrections here. Update `docs/knowledge.json`
only for reviewed documents and keep its authored formatting. Run affected tests,
regenerate the required clients, and finish with full verification and lint.

## Validation and Acceptance

All commands run from `/Users/petrbrazdil/Repos/scenery`:

```sh
go test ./cmd/scenery ./internal/agent ./internal/edge ./internal/deployplan ./internal/devdash ./internal/durable/store ./internal/evolution ./internal/generate ./internal/spec ./internal/compiler ./internal/contractagent ./runtime ./scripts/verify
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
golangci-lint run ./...
go run ./scripts/verify --summary --write
git diff --check
```

Full verification includes `go test ./...`. Inspect its archived
`agent-context.json` and fulfill the union of recommended commands for
`go-package`, `compiler-generator`, `cli-json-contract`, and
`release-sensitive-or-runtime` (plus any newly reported class). Acceptance requires
zero diagnostics of warning/error severity, `inputs_stable: true`, declaration
parity, and no generated artifact changes from file organization alone. Release
certification and all-root isolated timing audits are outside this request.

## Idempotence and Recovery

All source moves stay in the same package and can be reverted independently.
Snapshot declaration/rendered-byte evidence under ignored
`.scenery/harness/warnings-0210/` before editing. Generated clients use the
existing atomic publication mechanism. Never reset unrelated work, resource
ownership or retained application data. A failed validation run is retained;
correct its cause before publishing a new final run.
