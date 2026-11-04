# Reviewed Documentation And Cached Verification

## Purpose / Big Picture

Remove all 44 overdue-document warnings and the advisory Go-suite wall-time
warning reported after Plan 0222. Review documents against their current owners
before recording new review dates. Preserve the 5-second cached-suite budget,
every Go package/test, ordinary result caching, and all unrelated working-tree
changes from JSON v2 adoption. Completion is a stable-input full verifier with
zero warning/error diagnostics; a quick-only run cannot prove the suite budget.

## Progress

- [x] 2026-11-04: Capture the exact 44-document baseline from full run
  `20261104T195815.311701000Z` and inventory current document status/owners.
- [x] 2026-11-04: Diagnose cached tests without disabling caching: the first
  ordinary suite reuses 67 of 79 test-package results; a repeated identical
  cache-debug suite reuses all 79. Changing `GODEBUG` itself invalidates cache
  inputs, so its first all-uncached run is not a production regression sample.
- [x] 2026-11-04: Review all 44 entries and record their owner evidence below;
  correct the README, Go examples, active-plan orientation/schema link and
  environment-index summary before refreshing metadata.
- [x] 2026-11-04: Ordinary cached diagnostic replay completes in 4.287 seconds,
  with 77 of 79 test-package results cached and no test omissions. Final full
  verifier acceptance is still required after document metadata settles.
- [x] 2026-11-04: Fix per-lane cache invalidation by giving only Go-test
  descendants a stable null-device default agent home; parent/probe homes remain
  disposable. Add allocation-refusal and parent-isolation coverage. Correct the
  source-wait test's invalid 6ms disk-scan assumption without raising its timeout.
- [x] 2026-11-04: Initial source acceptance passes with zero diagnostics and a
  3.770-second Go suite, but final closure runs still exceed 5 seconds. Retain
  these measurements as diagnostic evidence; the plan remains open.
- [x] 2026-11-04: Paired measurements identify duplicate implicit vet preparation.
  Full cached lanes now disable only implicit vet and retain their independent
  all-package vet step; quick mode retains implicit vet. Coverage still requires
  successful full vet evidence.
- [x] 2026-11-04: Prevent optional Git index writes throughout verification
  descendants, including Go's VCS discovery. Caller policy is restored afterward.
- [x] 2026-11-04: Final source acceptance passes with zero warnings/errors,
  3.011-second Go wall time, all 79 test packages replayed, unchanged `.git`
  metadata, matching CLI-process proof, lint, verifier race and Linux vet.
  Close the plan/indexes and certify those documentation-only closure edits.

### Resume here

2026-11-04: Complete. All 44 document reviews and focused verifier/test corrections
are accepted by the zero-warning full/probe archives below. Final plan/index
closure is certified by the immutable full run linked in the handoff. Preserve
uncommitted JSON v2 changes. Plans 0101, 0203 and 0205 retain separate operator/
external-app acceptance boundaries; this review did not perform them.

## Surprises & Discoveries

- The earlier full run's 12.764-second suite executed 3,484 cases and replayed
  only 36 packages; its total is not an all-cached baseline. The ordinary
  diagnostic suite takes 10.559 seconds with 12 uncached test packages.
- `GODEBUG=gocachetest=1` produces 79 misses on its first invocation because
  the environment is a tracked test input. Its identical repeat replays 79/79
  results in 5.631 seconds. Keep raw logs and measure the ordinary final command
  without that diagnostic setting; do not claim the debug result meets 5 seconds.
- Completed numbered plans are immutable and do not contribute overdue review
  warnings. The 44 warned entries are living/reference documents, including
  three still-active plans; refreshing them cannot close outstanding acceptance.
- The subsequent ordinary replay passes in 4.287 seconds (77/79 test-package
  results cached), but the next real verifier still reruns tests: each lane's
  random `SCENERY_AGENT_HOME` is a recorded Go-test input. Bare repeated commands
  do not reproduce that per-lane invalidation, so the ordinary replay alone is
  insufficient proof of the requested verifier behavior.
- Full run `.scenery/harness/runs/20261104T202954.212008000Z/` has zero freshness
  warnings but fails `TestSourceWaitEndsOnFailureBlockSupersessionAndTimeout`:
  its 6ms timeout can expire during the disk scan, legitimately reporting
  `SourceFreshness: unknown`. The test now separately proves the initial stale
  state and accepts only the two schema-valid timeout freshness values while
  retaining captured-target/serving-identity assertions. Production wait behavior
  and timeout durations do not change.

- Final closure runs `20261104T205004.304163000Z` and
  `20261104T205143.775069000Z` still take 6.919 and 6.444 seconds. In three paired
  measurements, full cached test median falls from 4.377 to 3.773 seconds when
  implicit vet is disabled (the first ordinary-vet sample also populates cache).
  The verifier already runs independent `go vet ./...` after the test step.

- A comparison of recorded module test inputs finds `.git` directory metadata
  changing between otherwise unchanged runs. The owned diagnostic
  `git-optional-locks.json` confirms both normal `git status` and `go build`
  change it; `git --no-optional-locks status` preserves it. Go invokes Git itself
  for VCS discovery, so changing only the verifier's Git helper is insufficient.
- Three cached scheduling pairs show only a small difference for `-p 6` (median
  3.826 versus 4.018 seconds); do not change default package parallelism on that
  evidence. The machine concurrently runs substantial Blender/OrbStack work;
  preserve those unrelated processes and the existing timing budget.

## Decision Log

- 2026-11-04: Do not raise timing limits, suppress diagnostics, skip packages,
  add cache bypasses, or mark living documents historical to hide warnings.
- 2026-11-04: Review schema identities, required fields, nested variants and
  current producer/tests. Keep schema bytes unchanged unless a real mismatch
  is found, so metadata maintenance does not change protocol revisions.
- 2026-11-04: Historical agent evaluation measurements remain historical. Review
  their scope and live reference paths; do not rewrite their measured results
  as current performance claims.
- 2026-11-04: Add no Go change unless ordinary stable-input cache evidence shows
  a persistent cause requiring one. A post-edit cache population is distinct
  from a warm cache regression.
- 2026-11-04: A real verifier's random private home is the persistent invalidation
  source. Override only the Go-test child environment with `os.DevNull`: default
  allocation fails closed, tests needing state still own explicit homes, and the
  parent lane/probes keep fresh real private homes. No shared persistent test
  state, lock, cache bypass, new environment knob or timing exception is added.

- 2026-11-04: Disable duplicate implicit vet only in full cached lanes. Keep
  independent all-package vet mandatory for full/quick coverage, and retain
  implicit vet in quick mode, which has no independent vet step. No analyzer,
  package, test, budget or diagnostic is removed.

- 2026-11-04: Cache-input stat comparison proves that read-only Git status and
  Go's VCS build discovery modify the `.git` directory via optional index locks.
  Force Git's existing `GIT_OPTIONAL_LOCKS=0` only inside the disposable verifier
  lane and restore the caller afterward. A Git command flag cannot cover the
  Git processes invoked internally by the Go driver, so process propagation is
  required. This is fixed tool behavior, not a new Scenery configuration knob.
  Do not bypass VCS stamping, ignore source inputs, or modify index contents.

## Outcomes & Retrospective

Completed on 2026-11-04. All 44 overdue entries were reviewed against current
owners before their metadata advanced. The 30 checked schemas and 111-entry
environment registry retain their exact bytes and identities. Content corrections
cover illustrative Go versions, the removed config-schema path, current dotenv/
standard-auth orientation, the environment-index summary, and the unsupported
README token-savings claim. Historical measurements and unresolved operator
acceptance are preserved.

The verifier uses a stable, non-allocating default agent home for Go-test
children, prevents optional Git index writes throughout the lane, and avoids
implicit vet preparation in full cached tests while retaining independent full
vet. All 79 test-package results now replay; the full run preserves `.git`
metadata. Parent/probe isolation and caller environment restoration remain
covered. The timeout test checks the valid timeout contract rather than assuming
disk work finishes before 6ms; its timeout and runtime implementation are unchanged.

Acceptance evidence, intentionally stored under ignored local artifacts:

- Full `go run ./scripts/verify --summary --write`:
  `.scenery/harness/runs/20261104T210716.785106000Z/`, stable input
  `sha256:e65da0102b86f6386c74e8b1cd6ea09cfa97b743adca8b65c93e50f665930610`,
  **zero warnings/errors**, complete 4,316-case inventory across all 112 packages,
  Go-suite wall 3.011 seconds against 5.000 seconds, all 79 test packages
  replayed, vet/schema/architecture/knowledge/drift/isolation all pass.
  `final-git-read-isolation.json` records unchanged `.git` metadata across the
  complete command. The post-edit cache-population run `20261104T210625.810156000Z`
  took 10.461 seconds with 73 packages replayed; it is not a zero-warning result.
  Earlier diagnostic/slow archives remain available and are not acceptance.
- `golangci-lint run ./...`: `0 issues.`
- `go test ./scripts/verify ./cmd/scenery`: pass.
- `go test -race ./scripts/verify`: pass; `GOOS=linux go vet ./scripts/verify`: pass.
- `go test ./cmd/scenery -run '^TestSourceWaitEndsOnFailureBlockSupersessionAndTimeout$' -count=20`:
  all 20 focused nondeterminism-investigation repetitions pass in 0.628 seconds
  of package-reported time. No all-root timing audit or cache bypass is selected.
- `go run ./scripts/verify --probe cli-process --summary --write`:
  `.scenery/harness/runs/20261104T210745.790559000Z/`, same stable input revision,
  **zero warnings/errors**. Real CLI exit/telemetry cases, owned cleanup, shared
  build-cache cleanup and verification-private-home evidence all pass.
- `inspect docs --review-due -o json`: zero selected/overdue/stale documents.
  The complete per-document ledger is below and in
  `.scenery/harness/warnings-0223/review-ledger.json`.

The required context command union is covered by that full run plus separate
lint and owner checks. Only plan/index closure follows source acceptance; the
final full verifier archive is linked in the handoff. All 140 checked schema files
and the environment registry also compare byte-for-byte with HEAD. The JSON v2
production-source digest remains
`d37e425ec6277002c5a4c04bf133bcc63f158c6ed642773f8918819e5bdfeebe`,
matching both Plan 0222 performance reports. No budget, package scope, test roots,
application runtime, generated artifact, schema identity
or retained user state was removed or relaxed. Plan 0222's application performance
and external-boundary evidence remains source-matching. Production actions,
release certification and all-root isolated timing certification are unselected.

### Document review ledger

All rows were reviewed on 2026-11-04 before changing freshness metadata.
The 30 checked schema documents and the 111-entry environment registry retain
their exact bytes. Schema owner tests and full-verifier representative validation
are the executable evidence; historical evaluations remain bounded records.

| Document | Current owner evidence | Review outcome |
| --- | --- | --- |
| `docs/agent-context-evaluation-0211.md` | scripts/verify/harness_agent_context_summary.go; scripts/verify/harness_agent_context.go; historical evaluation scope | Historical results and bounded conclusions retained |
| `docs/schemas/scenery.agent_context.summary.schema.json` | scripts/verify/harness_agent_context_summary.go; scripts/verify/harness_schema.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/agent-instruction-evaluation-0209.md` | scripts/verify/harness_run_archive.go; PLANS.md; historical evaluation scope | Historical results retained; token/speed limits remain explicit |
| `docs/plans/0205-environment-configuration.md` | internal/appconfig; cmd/scenery/dev_config_watch.go; runtime/deployment_config.go; auth/standard.go | Correct removed dotenv/current-auth orientation; preserve external acceptance |
| `docs/plans/0203-framework-handoff.md` | internal/build/framework_{session,prepare,source}.go; cmd/scenery framework handoff tests | Refresh source-review checkpoint; pinned ONLV observation remains open |
| `README.md` | go.mod; live inspect routes for testdata/apps/basic; evaluation reports | Replace unsupported 70% token claim with scoped-inspection behavior |
| `docs/schemas/scenery.inspect.docs.schema.json` | internal/repoinfo/knowledge.go; scripts/verify/harness_schema.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.agent.cleanup.schema.json` | cmd/scenery/agent_cleanup.go; internal/machine/payload_identity.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.build.result.schema.json` | cmd/scenery/build.go; internal/machine/payload_identity.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.init.schema.json` | cmd/scenery/assistant_command.go; assistant init/sync/status types; scripts/verify/harness_schema.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.sync.schema.json` | cmd/scenery/assistant_command.go; assistant init/sync/status types; scripts/verify/harness_schema.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.control.event.schema.json` | internal/assistantcontrol/types.go; cmd/scenery/assistant_control_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.control.request.schema.json` | internal/assistantcontrol/types.go; cmd/scenery/assistant_control_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.control.response.schema.json` | internal/assistantcontrol/types.go; cmd/scenery/assistant_control_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.public-error.schema.json` | internal/assistantapi/types.go; cmd/scenery/assistant_public_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.public-event.schema.json` | internal/assistantapi/types.go; cmd/scenery/assistant_public_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.public.request.schema.json` | internal/assistantapi/types.go; cmd/scenery/assistant_public_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.public.response.schema.json` | internal/assistantapi/types.go; cmd/scenery/assistant_public_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.runtime-descriptor.schema.json` | internal/assistantcontrol/types.go; cmd/scenery/assistant_control_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.assistant.status.schema.json` | cmd/scenery/assistant_command.go; assistant init/sync/status types; scripts/verify/harness_schema.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.inspect.assistants.schema.json` | cmd/scenery/assistant_command.go; assistant init/sync/status types; scripts/verify/harness_schema.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.approval-token.schema.json` | internal/contract/approval.go; internal/contract/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.approval-trust.schema.json` | internal/evolution artifacts; internal/evolution/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.change-plan.schema.json` | internal/evolution artifacts; internal/evolution/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.change-receipt.schema.json` | internal/evolution artifacts; internal/evolution/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.deployment-plan.schema.json` | internal/deployplan; internal/deployplan/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.deployment-receipt.schema.json` | internal/deployplan; internal/deployplan/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.generated.schema.json` | internal/generate descriptors; internal/generate/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.go-build-input-manifest.schema.json` | internal/build/build_input.go/runtime_bundle.go; internal/build/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.package-generated.schema.json` | internal/generate descriptors; internal/generate/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.runtime-bundle.schema.json` | internal/build/build_input.go/runtime_bundle.go; internal/build/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.cli.schema.json` | internal/machine/envelope.go; internal/machine/envelope_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.cli.event.schema.json` | internal/machine/envelope.go; internal/machine/envelope_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.manifest.schema.json` | internal/graph/model.go; internal/graph/model_identity_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/schemas/scenery.typescript-client-generated.schema.json` | internal/generate descriptors; internal/generate/artifact_schema_test.go | Identity, required/properties, references and variants reviewed; schema bytes unchanged |
| `docs/spec/SPEC.md` | internal/spec; compiler/generator/runtime conformance; SPEC sections 20.4 and 26 | Update illustrative toolchain/producer versions to Go 1.27.0; no normative change |
| `docs/spec/go-implementation.md` | internal/build; internal/generate; current go.mod/toolchain fixtures | Update illustrative Go version to 1.27.0; preserve ABI/tooling rules |
| `docs/spec/http-path-tail.md` | compiler path-tail fixtures; runtime path-tail decoding; generated-client conformance | No content change: cardinality, precedence, traversal and strict decoding match owners |
| `docs/spec/evolution.md` | internal/evolution; internal/deployplan; checked schema identity tests | No content change: directional transitions, revisions and rename receipts match owners |
| `docs/plans/0101-public-deploy-edge.md` | cmd/scenery deploy/edge owners; docs/schemas/scenery.config.schema.json | Correct removed versioned schema path; literal reboot/login remains open |
| `docs/index.md` | knowledge.json; current inspect docs CLI; active/completed plan indexes | No content change: discovery/index links and current ownership match |
| `PLANS.md` | scripts/verify plan/knowledge checks; harness_run_archive.go; current root instructions | No content change: eight sections, resumption and immutable-history rules match |
| `docs/environment.registry.json` | 111 entries; internal/envpolicy scan; docs/environment.md; internal/appconfig | Registry bytes unchanged; correct index summary for removed auth env variables |
| `docs/ui-agent-contract.md` | ui/index.ts; QueryTable/DataTable/PageLayout; internal/uigen table slot renderers/tests | No content change: editable catalog, slots, windowing, aliases and dependency ownership match |

## Plan of Work

All commands run from the repository root. The exact review inventory is
`.scenery/harness/warnings-0223/baseline-docs.json`. Read each warned schema's
identity, property/required definitions and variants, then inspect its producer
and existing shape/identity/conformance tests in machine, assistant, build,
generation, graph, evolution, deployplan and verifier owners. Review specification
sections and examples against current compiler/runtime boundaries. Review README,
the documentation index, PLANS, environment registry and UI agent contract against
current supported commands, ownership and code. Retain active-plan unresolved
acceptance, and update only their current orientation/review checkpoints.

Record one ledger row per warned path, including the owner references and whether
content changed. Then advance `last_reviewed` to 2026-11-04 and `review_after` to
2026-12-04 only for those reviewed paths. Preserve authored JSON formatting.
Index this plan in `docs/knowledge.json` and the active/completed plan indexes.

For the suite warning, inspect raw `go test -json ./...` events and cache-debug
receipts. After document/source inputs settle, run the full verifier. If its suite
still exceeds 5 seconds, identify uncached packages/input changes or Go frontend
cost and make the smallest evidence-backed correction; rerun only checks affected
by that correction. Ordinary cached wall evidence does not certify isolated test
root p95 or runtime HTTP latency.

## Validation and Acceptance

Select full after editing because the cumulative working tree includes the
already-validated JSON v2 source changes and acceptance needs the Go-suite step:

```sh
go run ./scripts/verify --summary --write
golangci-lint run ./...
go test ./scripts/verify ./cmd/scenery
go test -race ./scripts/verify
GOOS=linux go vet ./scripts/verify
go test ./cmd/scenery -run '^TestSourceWaitEndsOnFailureBlockSupersessionAndTimeout$' -count=20
go run ./scripts/verify --probe cli-process --summary --write
git diff --check
```

Read the run's archived `agent-context-summary.json` before `agent-context.json`
and fulfill the exact command union. Full's all-package Go suite and vet cover
the matching package/CLI tests. If the first post-edit run populates cache or the
final documentation evidence/index changes inputs, repeat the same full command
on the settled tree and preserve both archives. Acceptance requires stable
inputs, every selected check passed, no warning/error diagnostics, all 44 reviewed
entries no longer overdue, unchanged schema content unless explicitly corrected,
and the cached suite below its existing 5-second threshold.

No app runtime/schema implementation or generated artifact changes; reuse
Plan 0222's source-matching native/configuration/race/Linux-vet/performance
evidence. The verifier child environment changes, so select CLI-process proof
and the verifier race/Linux-vet checks above. The explicit `-count=20` is only
the diagnosed one-root nondeterminism investigation, not a suite cache bypass.
Generator/UI changes require their
cumulative regeneration/typechecks; skip them only while those source/artifact
paths remain unchanged. Release certification, all-root isolated timing audits,
production/external-app actions and fresh-test cache bypasses are unselected.

## Idempotence and Recovery

Keep the initial dirty-file inventory and document metadata under the ignored
evidence directory. Review edits are ordinary reversible file changes; never
reset JSON v2 work or edit completed numbered plans. Test cache is retained, not
cleared or replaced. Preserve failed/slow run archives as diagnostic history.
If interrupted, resume from the ledger and measured cache receipts instead of
blindly re-reviewing or re-dating every indexed document.
