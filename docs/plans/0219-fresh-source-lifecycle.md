# Fresh Source Snapshots And Shared Lifecycle Planning

## Purpose / Big Picture

Implement the supplied Scenery point 1 guide against starting revision
`67cff8fc292a79ef93025f593ed9cbf1092a9935`. Every successful source-derived
answer must identify one captured, validated source revision. Invalid source
must return current diagnostics. Cache state must not change semantics.
Inspection and check-only commands must never repair app output or start tools
or infrastructure. Explicit generation publishes one recoverable, owned set.
The lifecycle must use one dependency-aware plan with explicit capabilities
across command and development adapters. Canonical syntax stays canonical.

OAuth, UI redesign, storage drivers and MCP are outside this work. No shared
CLI installation, merge, deployment or retained-data deletion is authorized.
Delivery includes required validation and a pull request.

## Progress

- [x] 2026-10-14: Read the supplied guide and repository ownership instructions;
  confirmed the starting worktree was clean and detached at the revision above.
- [x] 2026-10-14: Baseline tests reproduced generation-time edit acceptance,
  stale check snapshot acceptance and authored SQLC schema misclassification.
  Source inspection already follows edits/invalid diagnostics; added coverage
  also exposed missing inspection revision metadata. Drift without repair passed.
- [x] 2026-10-14: Bind generation output and response metadata to one compiler result;
  validate membership and bytes at the return/publication boundary.
- [x] 2026-10-14: Corrupt build state becomes a miss; graph hits require complete current output digests and shaped metadata.
- [x] 2026-10-14: Add a small typed serial lifecycle planner with dependencies, ownership,
  cache policies and capability admission; adapt existing producer owners.
- [x] 2026-10-14: Route source commands, check/generate, SQLC, DB and watch preparation through
  shared planning without adding effects to inspection or check-only execution.
- [x] 2026-10-14: Remove orphaned DB sync composition; root-discovery regression added. Canonical CLI rejection proof remains in validation.
- [ ] Update owning living docs, schemas and plan/index metadata.
- [ ] Complete cumulative validation, observable CLI/runtime scenarios and PR.

### Resume here

2026-10-14: branch `feat/fresh-source-lifecycle`, baseline clean. Local product
binary built at `.scenery/harness/bin/scenery`. Documentation selection is saved
at `/tmp/scenery-069c-docs.json`. Next: baseline regression tests, then the first
independently reviewable snapshot/publication fix. No scope choice is pending.

2026-10-14: Implementation now covers shared typed plans, compiler/response
revision validation, cache output validation and staged SQLC ownership receipts.
Focused package tests passed; an unchanged Victoria cleanup test transiently
failed during a parallel package run and passed in isolation. Next: complete
SQLC/retired-syntax boundary tests, lint, exact regeneration/TypeScript lanes,
full verifier and named probes, then publication.

## Surprises & Discoveries

- Source inspection already calls the compiler directly; historical graph JSON
  is not its authority. Public projections already have a bounded process-local
  SHA-256 cache and generation already uses `internal/workspacetx`.
- `runContractGenerate` recompiles after publishing to obtain metadata. Its
  response can therefore describe another revision. `generateFromRoot` renders
  inside the transaction but does not revalidate after rendering.
- SQLC currently writes schema output and invokes SQLC directly in the app
  tree. Its record also classifies an authored SQL schema as an output.
- `dbSyncCommandWithHooks` is reachable only from a test, not CLI dispatch.

## Decision Log

- 2026-10-14: Keep compiler graph semantics, generation rendering, build input
  resolution, staged TypeScript checking and workspace publication in their
  existing owners. The planner only validates and schedules direct Go actions.
- 2026-10-14: Reuse existing source membership and digest helpers, descriptor
  ownership, projection caches and transaction recovery. Add no persisted IR,
  independent filesystem walker, lock framework or environment knob.
- 2026-10-14: Preserve separate semantic contract, exact workspace, Go
  implementation and deployment identities. A historical `inspect build`
  remains historical unless explicitly verified.

## Outcomes & Retrospective

Not yet completed. Ending revision, PR, reproduced/already-fixed findings,
cache identities, command paths, removals and exact proof will be recorded here.

## Plan of Work

Begin with in-process temporary-app regressions beside each existing owner.
Test cached success followed by declaration edits and invalid source, check-only
drift without mutation, ignored historical graph files and publication-time
edits. Use fault hooks for concurrency and external runners; ordinary tests
remain service-free.

Bind generation's compiler result to its response rather than recompiling.
Revalidate full source membership and bytes after rendering and before success
or commit. Normal readers use recovery/rejection; transaction validation uses
the current-owner mode. Cancellation must stop a whole attempt. Retain the
watcher's captured-byte compilation and supersession gate.

Input ownership is conservative and belongs to current resolvers:

| Node family | Required inputs and owner |
| --- | --- |
| Contract | Compiler-owned authored `.scn`, resolved package bytes, app config, lock/provider descriptors and absent resolver alternatives |
| Go verification/build | Build's selected Go package/embeds/native inputs, modules/workspaces/local replacements, toolchain, flags/environment, framework and generated dependencies |
| Schema/SQLC | Authored HCL/SQL/queries, SQLC/Atlas config and referenced inputs, engines/tool identities; authored schema is input, rendered schema has a producer |
| TypeScript | Declared targets, client/catalog renderer bytes, app slots, tsconfig/extends, package/lock config, checker and current dependency readiness |

Exact descriptor-owned outputs alone are excluded from authored membership.
Do not exclude entire managed roots or all `_gen.go` files. Hash exact bytes and
detect membership changes, including absent alternatives and restored-mtime
edits. Reusable caches validate owner/schema/input identity and complete output
membership/digests; malformed disposable state is a miss, unsafe ownership is
a refusal. Never apply disposable-cache recovery to runtime or database state.

Introduce a typed, deterministic serial graph only where no reusable one exists.
Validate duplicate IDs, missing producers, cycles and overlapping output owners
before any action. Nodes declare typed inputs/outputs, configuration/tool
identity, reusable/current-validation/external-effect cache policy and explicit
read/cache/publication/scratch/database/runtime capabilities. Selected dependency
closure runs each producer once; no miss grants permission. Existing pure
projection caches remain with their owners and validations always execute.

Use the same plans for inspect/compile/list/get, check, selected generation,
SQLC schema production, explicit DB apply/setup and authorized development
preparation/activation. Inspection never selects implementation/tool/DB/runtime
effects solely because those nodes exist. Planner-backed `inspect generators`
must expose deterministic dependencies/effects/ownership with synchronized schema
and docs. Operational progress stays outside JSON stdout.

Stage SQLC and selected generation before one owned publication. Preserve
unrelated files, reject unknown ownership, and retire only proven obsolete
descriptor-owned output. Database application is a separate non-cacheable live
effect; an unchanged fingerprint cannot establish live synchronization. Atlas
scratch capability is distinct from app database mutation.

Verify and remove only live orphan/legacy code. Keep completed numbered plans
and human-owned `VNEXT.md` unchanged. Reject `.config.json` root discovery,
`gen client`, `generate client`, `db sync` and `--json` without aliases.

## Validation and Acceptance

Run from this repository root, retaining the normal Go test cache:

```sh
go test ./internal/compiler ./internal/parse ./internal/generate ./internal/build ./internal/workspacetx ./internal/tscheck ./cmd/scenery
go test ./internal/lifecycle
go test -race ./internal/lifecycle ./internal/compiler ./internal/generate ./internal/build ./internal/workspacetx ./cmd/scenery
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
golangci-lint run ./...
go run ./scripts/verify --summary --write
```

Read the selected full run's `agent-context-summary.json` first, then complete
the command union in `agent-context.json`; reuse successful same-input checks.
The full verifier supplies `go test ./...`. No release/benchmark/timing campaign
is selected. Changes to external CLI/generation/runtime boundaries require the
named `cli-grammar`, `cli-process`, `generation`, `native-contract`, `worktree` and `dev-process` probes from
the current catalog; adjust exact IDs from the catalog before execution.

Acceptance covers edits/config/packages/HCL/queries/SQLC config, invalid input,
membership changes and restored-mtime edits; deleted/tampered cache/output;
unknown ownership and interrupted/two-writer publication; tool/framework/flag
selection; current TS staging/dependencies; concurrent edits; pure inspection;
unchanged reuse with validations; live DB drift; removed targets; invalid DAGs,
capability refusal and failure/cancellation blocking dependents/publication.
Name each proving test/probe in the completion record. Unselected or blocked
integration proof is a coverage gap, never a pass. Demonstrate CLI freshness
and generation identities on a temporary app, plus existing runtime candidate
identity/supersession through the named runtime probe.

## Idempotence and Recovery

Read and preserve dirty state before editing. Publication uses workspacetx's
existing journal and reader recovery gate. Retry safe failures after recovering
or rejecting the exact transaction; do not delete `.scenery/gen` to force
freshness. Pure output bytes are immutable and cloned across cache users.
Generated output repair requires publication authority and descriptor/digest
evidence. Scratch tools and app DB/runtime ownership are separate capabilities.
Never adopt mismatched retained data or overwrite unrelated managed-root files.


Validation checkpoint 2026-10-14 (not completion):

- First checkpoint commit: `2ee2f8a2991f1be315b7d327bfc886186859337c`.
- Focused package tests and lint passed. Changed-surface race command passed
  (compiler/generator/build/workspacetx/CLI/lifecycle).
- All three exact fixture generation commands passed with `changed: []`;
  output JSON is `/tmp/scenery-069c-{native,house,assistant}-generation.json`.
- UI probe's selected TypeScript checks passed in archive
  `.scenery/harness/runs/20261014T120721.677514000Z`; that overall run failed
  the subsequently repaired instruction budget and versioned-identity checks.
- Generation probe passed, including real SQLC and crash/two-writer publication:
  `.scenery/harness/runs/20261014T121050.148592000Z`. Its detailed proof is
  `.scenery/harness/ordinary-go-contracts/generation-proof.json`.
- Full verifier passed with 27 existing knowledge freshness warnings and a
  cached-suite timing advisory in
  `.scenery/harness/runs/20261014T121226.674811000Z`. Its compact context was
  read; the final full context/union still needs review on the latest run.
- Subsequent source changes prevent treating those runs as final proof:
  declared input capture no longer hashes directories created solely by owned
  outputs; graph hits now reject empty generated-output manifests. Re-run the
  affected checks/full verifier and new real SQLC segment after these edits.
- Running exec handles: `78773` final full verifier; `65112` focused package
  checks; `55386` combined native-contract/dev-process/worktree/cli-grammar/
  cli-process probes. Poll those exact live sessions; do not restart based on
  elapsed time. `85557` race run completed successfully before the two changes.
- Still required: completion audit against every guide scenario, current lint
  and TypeScript proof, named probes and cleanup, final plan/index closure,
  commit/push, create and attach the requested PR. No PR has been created yet.


Latest checkpoint 2026-10-14:

- Focused post-edit package checks passed (`65112` completed).
- Full post-edit verifier passed with existing knowledge/timing advisories:
  `.scenery/harness/runs/20261014T121606.089384000Z` (`78773` completed).
- The combined runtime probe (`55386`) completed but is not valid final proof:
  repository source changed while it was running. Native-contract, dev-process
  and CLI grammar segments passed; worktree refused the stale framework binary,
  CLI report rendezvous failed, and the overall archive explicitly reports
  changed validation inputs. Archive:
  `.scenery/harness/runs/20261014T121227.875528000Z`.
- Re-run the combined named probe against stable final source without edits or
  another binary build during its execution. Investigate any repeat failure as
  an actual defect; do not classify the prior unstable run as a pass.
- Final completion audit, final lint/TypeScript proof, current context union,
  plan closure and PR publication are still outstanding. Work remains active.
