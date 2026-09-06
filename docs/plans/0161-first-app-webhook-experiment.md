# First-App Webhook Experiment

This ExecPlan is a living document maintained under PLANS.md.

## Purpose / Big Picture

Exercise Scenery on an independent webhook inbox, then add authenticated status
and a generated TypeScript client. Measure the actual first-app and routine-change
workflow and fix the largest observed friction without adding resource families
or a benchmark framework. This is one experienced-agent run, not a novice study.

## Progress

- [x] (2026-09-06 07:10 UTC) Begin timed discovery from the cookbook and CLI.
- [x] (2026-09-06 07:25 UTC) Verify admission, API restart, separate worker, duplicate delivery, and malformed input against disposable PostgreSQL.
- [x] (2026-09-06 07:30 UTC) Add authenticated status and an exported typed client; live proof exposes two product bugs.
- [x] (2026-09-06 07:36 UTC) Fix exact numeric constraint generation and invalid-token classification; the isolated end-to-end script passes.
- [x] (2026-09-06 07:45 UTC) Repository tests, lint, release gate, release self-harness, and final native example proof pass.
- [x] (2026-09-06 07:46 UTC) Record command batches, elapsed time, source diff size, and verification limits below.

## Surprises & Discoveries

- Cookbook durable guidance names concepts and worker commands but has no
  complete enqueue declaration or Go implementation. Repository examples were
  needed to connect admission receipts, engines, and durable execution.
- The skill recommends short resource kinds for schema discovery, but
  `scenery schema execution -o json` fails with SCN8001; the fully qualified
  `scenery schema scenery.execution -o json` succeeds. Record and repair this
  mismatch after baseline authoring by documenting the exact qualified spelling.
- Ordinary `generate` is the app loop. `--target contracts` without
  `--materialize` fails; two onboarding docs incorrectly recommended it.
- Repository-nested fixtures intentionally skip managed Go editor workspaces.
  A standalone temporary copy resolves generated imports with raw `go test`.
- HTTP authentication needs a root resource with
  `std.provider.standard_auth`, passed as a typed module input. Package-local
  authentication and HTTP `std.authentication.inherit` are rejected correctly.
- Package operations must be exported to appear in a client; generation with no
  exports succeeds with an empty client. This needs practical documentation.
- `tsFieldConstraints` copied tagged exact numeric scalars into TypeScript
  descriptors. `tsc` rejected those descriptors; numeric constraints also need
  lossless string projection. The fix reuses the existing exact scalar renderer.
- Standard auth returned an ordinary error for malformed/signature/expiry/claim
  failures, making a bad bearer token an HTTP 500. This caused the client's
  contract mismatch. Keep missing server key configuration a server error,
  but classify rejected credentials as unauthenticated.
- Updating the TypeScript semantic digest changes built-in provider descriptor
  integrity too. Refresh both committed provider locks and fixture descriptors
  from the current binary, not by accepting stale locks.

## Decision Log

- 2026-09-06 / Codex: Keep the app in examples/webhook-inbox, a nested Go module
  independent of ONLV. Retain ordinary source and a runnable recipe, not a
  resource DSL extension. No new generic scaffolding engine.
- 2026-09-06 / Codex: The enqueue receipt proves persisted admission, not business
  completion. A durable worker writes the app-owned processed record. Test
  worker absence/restart, HTTP behavior, auth rejection, and real typed-client
  calls independently of compiler success.
- 2026-09-06 / Codex: Use an isolated app/agent home, loopback addresses, and a
  disposable PostgreSQL instance. Never modify ONLV, existing databases, or
  the installed CLI. The webhook example is local-only, not a public provider's
  signed-webhook integration.

## Outcomes & Retrospective

Completed on 2026-09-06. The independent example and its repeatable acceptance
script live in `examples/webhook-inbox`. The runtime was exercised in standalone
temporary copies, not inside ONLV or through the shared agent. The largest
concrete blockers were fixed with two small production changes: exact numeric
TypeScript constraints now use lossless strings, and rejected JWT credentials
are typed authentication failures instead of server errors. Cookbook, installed
skill, and agent command guidance now connect the required steps.

No new resource family, generic scaffold, benchmark subsystem, production
dependency, shared CLI installation, commit, push, or live application deployment
was performed. The release script's install probes use disposable `GOBIN`
directories. Ordinary example source and generated TypeScript are retained;
Go/editor caches and raw proof artifacts are ignored.

### Measurements

Times are UTC wall time from the human's start request, including investigation
and tool latency, not model inference or production endpoint latency.

| Stage | Interval | Elapsed | Tool batches | Shell command batches |
| --- | --- | --- | --- | --- |
| First working durable baseline | 07:10:06–07:25:20 | 15m 14s | 50 | 59 |
| Status/client change, diagnosis, fixes, first complete proof | 07:25:20–07:36:44 | 11m 24s | 43 | 48 |
| Repository validation, proof hardening, handoff bookkeeping | 07:36:44–07:46 | about 9m | not part of the authoring sample | not part of the authoring sample |

A shell command batch is one shell invocation; it can contain multiple commands
or a pipeline. Tool batches also include patches and clock reads. These are not
counts of every subprocess launched by Go, the runtime, or polling loops. The
first explicit clock sample was 07:10:26, 20 seconds after the request. The first
client declaration was generated by 07:28:28, but that was not completion:
live auth calls and `tsc` exposed the two bugs.

Thirteen named guidance/spec files were consulted by read/search commands
through the first complete proof: root `AGENTS.md`, `SKILL.md`, `ARCHITECTURE.md`,
`PLANS.md`, the agent guide, cookbook, local contract, active plans, tech debt,
`docs/spec/SPEC.md`, and compiler/generator/spec scoped `AGENTS.md` files.
This counts unique consulted files, not full-document reads; relevant sections
were selected. The experimental plan itself is excluded. Source/test fixtures
were also needed for enqueue mapping, constructor fields, HTTP auth, exports,
and provider locks. Human interventions after the start request: **0**.

The baseline has 9 files / 277 lines, including config, lock, Go module, SQL,
and ignore rules. The actual status/auth/client domain change adds 102 lines and
removes 1 across `.scenery.json`, `app.scn`, `inbox/package.scn`, and
`inbox/service.go`. It additionally generates 1,535 TypeScript/descriptor lines.
Documentation, the standalone acceptance script/client driver, schema-command
simplification, and provider-lock revision refresh are separate from that
domain-change count. The two production fixes total 12 inserted / 7 removed
lines across `auth/standard_jwt.go` and
`internal/generate/generate_typescript_codec.go`; their focused tests add 74
lines. Semantic-digest and fixture updates are additional bookkeeping.

This is one experienced-agent run with prior Scenery knowledge, warm tools and
dependencies, and no control run. It is not evidence of novice onboarding time,
comparative productivity, or an A/B performance improvement. The later repeat
proof demonstrates reproducibility, not a second independent authoring sample.

### Verification Ledger

All final commands below passed:

- `bash examples/webhook-inbox/verify.sh`: fresh copy + disposable PostgreSQL,
  durable admission, API restart without worker, separate worker completion,
  duplicate preservation, malformed input, missing/invalid/valid credentials,
  typed processed/missing outcomes, digest, and pre-transport client constraints.
  It runs `generate`, `generate --check`, `check`, `go test ./...`, and
  `build --target development --output <temporary binary>`, using `-o json`
  for the Scenery commands.
- In the final standalone copy: `scenery harness -o json --write`.
- `go test ./auth ./internal/generate ./internal/spec`.
- `go test ./internal/compiler ./internal/parse ./internal/contractagent ./cmd/scenery .`.
- `go test ./...` and `golangci-lint run ./...` (zero lint issues).
- `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json`.
- The same generation command for `internal/compiler/testdata/house` and
  `testdata/assistant`; refreshed provider locks and descriptors are included.
- `bun test internal/generate/testdata/typescript_client_conformance.test.ts`.
- `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json`.
- The same `tsc -p` for `internal/generate/testdata/tsconfig.catalog.json` and
  `examples/webhook-inbox/client/tsconfig.json` (client and proof driver).
- `go test -json -count=20 ./auth -run '^TestInvalidAccessTokensAreUnauthenticated$'`
  and the equivalent generator command for
  `^TestTypeScriptFieldConstraintsProjectExactNumericScalars$`: 20 passes per
  exact root, all elapsed values rounded to 0.00s by Go, comfortably below 100ms.
- `.scenery/harness/bin/scenery harness self --release --summary --write`:
  every selected step passed, including full/race Go suites, schemas, native
  runtime and external process probes. This supersedes quick/default lanes.
- `scripts/release-gate.sh`, with the existing `SCENERY_BIN` override pointing
  to the worktree binary and `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` pointing
  to the disposable webhook copy: all steps passed, including external-app
  check, race, lint, dashboard build, isolated install, router and artifact proof.
- `git diff --check`.

The first focused tests failed before the fixes. An intermediate self-harness
failed on the missing child-doc index and a nested-example `go:embed` freshness
check; the small schema command now keeps its SQL literal directly. A release
run begun before the provider digest expectations were refreshed failed only
those expectations; the complete rerun passed. No validation was waived.

Not measured: production signup/email/refresh, provider webhook signatures,
multi-tenant permissions, managed `scenery up` UX, or public deployment. These
are outside this local experiment. The shared-inbox example explicitly warns
against using its public admission and local dev bootstrap in production.

Remaining usability debt is recorded, not hidden: missing build-target and
materialization arguments can produce opaque machine errors, and fresh builtin
provider locking lacks a practical onboarding command. The known-current lock
and recipe make this example runnable but do not solve general provider setup.
An empty `generate` change list is not itself an implementation-check failure:
`check` and runtime proof remain separate acceptance steps.

## Context and Orientation

Start with docs/app-development-cookbook.md, SKILL.md, and testdata/apps/basic.
Read CLI schemas and generated contracts before normative specification text.
The app declares its graph in app.scn and inbox/package.scn, implements ordinary
Go handlers, and has no UI. Repository fixes belong to the affected CLI/doc or
generator owner identified by evidence.

## Milestones

1. Durable public local admission and background processing.
2. Authenticated status with a generated fetch client; preserve the baseline diff.
3. Friction fixes, repeat validation, and write an evidence-backed report.

## Plan of Work

Keep a baseline source snapshot under ignored .scenery/harness before adding
status/auth/client. Record actual failed and successful workflow commands and
wall time; distinguish repository reading and validation from app authoring.
Use focused application tests and one repeatable native acceptance script.

## Concrete Steps

From examples/webhook-inbox, using the absolute worktree-local CLI path:

```sh
scenery fmt --check -o json
scenery compile --view expanded -o json
scenery generate -o json
scenery check -o json
go test ./...
scenery generate --target typescript_client.public_api -o json
scenery generate --check -o json
scenery harness -o json --write
```

From the Scenery root, build go build -o .scenery/harness/bin/scenery
./cmd/scenery, run affected package tests, golangci-lint run ./..., go test ./...,
and .scenery/harness/bin/scenery harness self --quick --summary --write. Run
the exact changed-area union. For runtime/generator changes also run
.scenery/harness/bin/scenery harness self --release --summary --write and
scripts/release-gate.sh. If generator/compiler source changes, regenerate both
house and native TypeScript fixtures using the root AGENTS.md commands.

## Validation and Acceptance

The baseline accepts an event durably, survives an API restart without a worker,
then processes it when a worker starts. The second-stage endpoint rejects missing
or invalid credentials and returns the persisted status through the typed client
with valid credentials. Duplicate delivery does not overwrite processed data.
Malformed input fails before business work. Tests distinguish acceptance from
completion and treat generated artifacts as output, not independent correctness
proof. Every skipped command needs an exact reason. No external app or browser
is required; no frontend is being built.

## Idempotence and Recovery

Only the disposable app database and runtime are reset or removed. Preserve
source and reports. Stop owned processes before deleting temporary homes. Do not
commit machine-local state or cached Go contracts.

## Artifacts and Notes

Raw logs and the baseline snapshot live under ignored
`.scenery/harness/webhook-*` paths. `webhook-self-release.json` preserves the
release report independently of later quick doc validation. The final acceptance
log records its retained temporary copy; its owned database and processes were
removed. Durable source and verification instructions are in the example.

## Interfaces and Dependencies

Use existing HTTP, SQL datasource, durable execution, standard auth, and fetch
client capabilities. No new production dependency or public resource family.
