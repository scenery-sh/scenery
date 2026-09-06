# CLI Onboarding Friction

This ExecPlan follows PLANS.md and preserves the completed 0161 experiment.
This ExecPlan is a living document; update Progress, Surprises & Discoveries, Decision
Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Repair every concrete CLI/onboarding problem found in the webhook experiment:
actionable argument errors, truthful help, qualified schema suggestions, an
explicit builtin provider lock workflow, empty-client warnings, visible editor
workspace status, content-scoped provider integrity, and generated-type/auth
navigation. Preserve the existing uncommitted experiment changes.

## Progress

- [x] (2026-09-06) Reproduce the failing calls and inspect ownership boundaries.
- [x] (2026-09-06) Implement argument/help/schema fixes and focused tests.
- [x] (2026-09-06) Implement explicit transactional provider locking and content integrity.
- [x] (2026-09-06) Report generation coverage/editor status and improve authored examples.
- [x] (2026-09-06) Regenerate native/house/assistant/example fixtures, run native
  proof, 220 isolated test-root measurements, the 46-step release harness and
  all 12 release-gate stages successfully.

## Surprises & Discoveries

Missing argument validation currently becomes SCN9000. Provider integrity hashes
the entire machine identity except producer, including unrelated spec revision.
Evolution already owns the source transaction writer; reuse it for lock updates.

The lock planner must retain the exact validated source bytes; a second read
would weaken its revision precondition. Multiple local provider names can share
one locked source, so selection deduplicates by source. The knowledge gate
requires the literal living-document statement from the plan template; the
first quick/release attempts failed only that documentation check, then passed
after the exact wording was restored.

## Decision Log

- Codex / 2026-09-06: Remove the unsupported plain generate --dry-run help entry;
  retain --check and the real SQLC --dry-run. No compatibility aliases.
- Codex / 2026-09-06: Explicit provider lock updates only declared builtins from
  the running binary, offline; preserve external/module locks and reject unknown
  unpinned providers. Compilation never silently relocks.
- Codex / 2026-09-06: Provider content digests exclude incidental machine identity,
  but bind schema, config, capabilities, instance kinds and runtime/deploy ABIs.

## Outcomes & Retrospective

Completed on 2026-09-06. All reported onboarding friction is addressed:

- Argument/flag failures are `SCN8001` with exit 2; missing build-target defaults
  name the required `artifact` role, available targets and an explicit command.
- Help separates normal generation/checking, explicit Go materialization and
  SQLC's real dry-run mode; short schema names suggest their exact qualified kind.
- `provider lock` explicitly creates/refreshes declared builtin locks offline,
  preserves unrelated dependency entries/comments, checks drift without writes,
  and uses the existing journaled source transaction and revision preconditions.
- Provider content digests exclude unrelated spec/producer identity while
  binding descriptor schema, source, config, capabilities, instance kinds and ABIs.
- Generation reports actual selected HTTP binding counts and empty-selection
  warnings, plus editor ownership/skip status; check-only generation does not
  create an editor workspace.
- Auth/module wiring and exact generated Go type/dependency navigation are
  documented and exercised in the independent webhook example.

No compatibility alias, registry installer, implicit dependency download,
environment knob or parallel source-transaction implementation was added.
External registry installation remains unsupported and is described as such.
Existing unrelated documentation-freshness warnings remain non-blocking.
The shared CLI and real Git index were not changed; no commit or push was made.
Completed plan 0161 was preserved unchanged.

Final verification (all passed):

| Command / boundary | Result and evidence under `.scenery/harness/` |
| --- | --- |
| `go test . ./auth ./cmd/scenery ./internal/compiler ./internal/evolution ./internal/generate ./internal/spec ./internal/parse ./internal/contractagent` | `onboarding-affected-final.log` |
| `go test ./...`, `go test -race ./...`, `go vet ./...` | Full release harness and release gate; standalone suite in `onboarding-go-test.log` |
| `golangci-lint run ./...` | 0 issues; `onboarding-lint-final.log` |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root <fixture> -o json` for `internal/compiler/testdata/native`, `internal/compiler/testdata/house`, `testdata/assistant` | Refreshed committed fixtures; `onboarding-generate-{native,house,assistant}.json` |
| Local CLI `provider lock` and generation for `examples/webhook-inbox` | Updated lock/client; repeat lock check is unchanged; `onboarding-generate-webhook.json` |
| `bun test internal/generate/testdata/typescript_client_conformance.test.ts` | 26 passed, 0 failed; `onboarding-ts-conformance.log` |
| `apps/console/node_modules/.bin/tsc -p <config>` for `internal/generate/testdata/tsconfig.generated-clients.json`, `internal/generate/testdata/tsconfig.catalog.json`, `examples/webhook-inbox/client/tsconfig.json` | All three passed; `onboarding-ts-{generated,catalog,example}.log` |
| Original binary CLI errors: `generate --target contracts`, `generate --dry-run`, `schema execution`, `build` against development-only example | Each returned exit 2 / `SCN8001`, actionable message, no opaque report token; `onboarding-cli-*.json` |
| 20 fresh isolated `go test -count=1 -run '^<root>$' -json` executions for each of 11 changed/new roots | 220 passes; maximum p95 60ms, all below 100ms; `onboarding-timing-summary.json` |
| `bash examples/webhook-inbox/verify.sh` against final local binary | Durable admission, API restart, separate worker, idempotence, validation, anonymous/invalid/valid auth and typed outcomes passed; `onboarding-webhook-proof-final.log` |
| `go doc example.com/webhook-inbox/inbox/scenerycontract.<Type>` in the standalone copy | Constructor input, dependencies, operation input/outcome, and `Event.EventId` resolve through the managed editor workspace |
| `.scenery/harness/bin/scenery inspect docs --for-path docs/spec/SPEC.md -o json` | Task-scoped specification discovery passed; `onboarding-spec-docs.json` |
| `.scenery/harness/bin/scenery harness self --release --summary --write` | All 46 steps passed; preserved `onboarding-self-release.json` |
| `scripts/release-gate.sh` with the worktree-local CLI and standalone example root | All 12 stages passed, including full/race tests, lint, UI, clean source installation, fixture/external-app probes, router safety and artifact hygiene; `onboarding-release-gate.log` |
| `uv run --with pyyaml python <skill-creator>/scripts/quick_validate.py .` | Skill valid; plain system Python lacked PyYAML, so validation used an isolated tool environment |

The release gate's clean-checkout file inventory used a temporary Git index
including this change's untracked authored files. Only that root inventory read
was redirected; normal Git operations and the real index were unchanged. The
gate installed its candidate only into its own temporary `GOBIN`. Both isolated
webhook runs cleaned up their own PostgreSQL container and app processes.

## Context and Orientation

CLI grammar lives in cmd/scenery/help.go and contract_commands.go. Compiler owns
provider descriptors and target resolution; generation owns client coverage and
editor workspaces. Evolution owns confined, journaled source updates. Existing
example and completed experiment remain independent evidence.

## Milestones

1. Correct unsuccessful CLI paths and help.
2. Explicit, safe builtin locking and stable content identity.
3. Observable generation, complete docs and end-to-end verification.

## Plan of Work

Use existing error codes, schema catalog, source parser and transaction writer.
Add small typed result fields where generation skipped work needs explanation.
Do not add environment knobs, a parallel provider installer or a new framework.

## Concrete Steps

From the repository root run affected package tests for cmd/scenery, compiler,
generate, evolution and spec; regenerate native, house and assistant public_api
TypeScript clients with go run ./cmd/scenery generate --target
typescript_client.public_api --app-root <fixture> -o json. Rebuild the local CLI.

## Validation and Acceptance

Run go test ./..., golangci-lint run ./..., the TypeScript conformance suite and
generated/catalog/example tsc configurations, bash examples/webhook-inbox/verify.sh,
.scenery/harness/bin/scenery harness self --release --summary --write and
scripts/release-gate.sh. Refresh quick self-harness after final documentation.
CLI JSON errors must carry SCN8001 and exit 2, not an opaque internal failure.
Provider locking must be offline, idempotent, preserve unrelated entries, refuse
unsafe paths and use existing transactional recovery. Unrelated spec/producer
changes must not alter provider integrity; semantic content changes must.

## Idempotence and Recovery

Preserve dirty source. Only use worktree-local binaries and disposable example
copies/databases. Existing workspace transaction recovery owns interrupted locks.

## Artifacts and Notes

Keep raw verification under ignored .scenery/harness/onboarding-* paths. Record
final results here and close the plan only after all acceptance work passes.

## Interfaces and Dependencies

Use the existing Go standard library, HCL parser/writer, compiler, generator and
evolution transaction implementation. No new dependency or implicit downloads.
